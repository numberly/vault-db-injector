//go:build linux

package bpf

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/vault"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

// Run is the body of controller.RunBPF on Linux. Blocks until ctx is done.
func Run(ctx context.Context, cfg *config.Config, clientset k8s.KubernetesClient, log logger.Logger) error {
	loader, err := Load()
	if err != nil {
		return errors.Wrap(err, "load BPF program")
	}
	defer func() { _ = loader.Close() }()

	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return errors.New("NODE_NAME env var not set; required by BPF runner")
	}

	persister := NewPersister(cfg.BPF.TmpfsPath)

	saToken, err := clientset.GetServiceAccountToken()
	if err != nil {
		return errors.Wrap(err, "get SA token for unwrap auth")
	}
	vaultConn, err := vault.ConnectAndRenew(ctx, cfg, saToken)
	if err != nil {
		return errors.Wrap(err, "vault connect")
	}

	r := &runner{
		nodeName:  nodeName,
		log:       log,
		unwrapper: vaultConn,
		mapWriter: loader,
		persister: persister,
		resolveCG: ResolveCgroupID,
		processed: make(map[string]struct{}),
	}

	if err := r.restoreFromTmpfs(); err != nil {
		log.Errorf("BPF tmpfs restore failed: %v", err)
		// Non-fatal: in-memory cache stays empty; informer events
		// will re-program live pods on demand.
	}

	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		clientset.RawClientset(),
		30*time.Second,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", nodeName).String()
		}),
	)
	podInformer := informerFactory.Core().V1().Pods().Informer()
	if _, err := podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return
			}
			if err := r.processPodAdded(ctx, pod); err != nil {
				log.Errorf("processPodAdded(%s/%s): %v", pod.Namespace, pod.Name, err)
				unwrapErrors.WithLabelValues("process_failed").Inc()
			}
		},
		UpdateFunc: func(_, newObj any) {
			pod, ok := newObj.(*corev1.Pod)
			if !ok {
				return
			}
			if err := r.processPodAdded(ctx, pod); err != nil {
				log.Errorf("processPodAdded update(%s/%s): %v", pod.Namespace, pod.Name, err)
				unwrapErrors.WithLabelValues("process_failed").Inc()
			}
		},
		DeleteFunc: func(obj any) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				// After an informer re-sync, deletions arrive wrapped in
				// DeletedFinalStateUnknown; unwrap or we'd leak BPF entries.
				tombstone, tombOK := obj.(cache.DeletedFinalStateUnknown)
				if !tombOK {
					log.Errorf("DeleteFunc: unexpected type %T", obj)
					return
				}
				pod, ok = tombstone.Obj.(*corev1.Pod)
				if !ok {
					log.Errorf("DeleteFunc: tombstone contained unexpected type %T", tombstone.Obj)
					return
				}
			}
			r.processPodDeleted(pod)
		},
	}); err != nil {
		return errors.Wrap(err, "add informer handler")
	}

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	log.Infof("BPF runner ready on node %s", nodeName)
	<-ctx.Done()
	return ctx.Err()
}

// Interfaces below let runner_test.go construct the runner without a real
// kernel or Vault.
type unwrapper interface {
	UnwrapValues(ctx context.Context, token string) (map[string]string, error)
}

type bpfMapWriter interface {
	PutMapping(cgroupID uint64, mappings map[string]string) error
	DeleteMapping(cgroupID uint64) error
}

type cgroupResolver func(podUID, containerID string) (uint64, error)

type runner struct {
	nodeName  string
	log       logger.Logger
	unwrapper unwrapper
	mapWriter bpfMapWriter
	persister *Persister
	resolveCG cgroupResolver

	mu        sync.Mutex
	processed map[string]struct{}
}

func (r *runner) processPodAdded(ctx context.Context, pod *corev1.Pod) error {
	if pod.Spec.NodeName != r.nodeName {
		return nil
	}
	raw, ok := pod.Annotations[k8s.ANNOTATION_BPF_MAPPING]
	if !ok {
		return nil
	}
	r.mu.Lock()
	if _, done := r.processed[string(pod.UID)]; done {
		r.mu.Unlock()
		return nil
	}
	r.processed[string(pod.UID)] = struct{}{}
	r.mu.Unlock()

	var payload k8s.BPFMapping
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return errors.Wrap(err, "parse bpf-mapping annotation")
	}

	values, err := r.unwrapper.UnwrapValues(ctx, payload.WrapToken)
	if err != nil {
		// Remove from processed so a retry can happen.
		r.mu.Lock()
		delete(r.processed, string(pod.UID))
		r.mu.Unlock()
		unwrapErrors.WithLabelValues("vault_unwrap").Inc()
		return errors.Wrap(err, "unwrap")
	}

	mappings := make(map[string]string, len(payload.Placeholders))
	for ph, field := range payload.Placeholders {
		v, ok := values[field]
		if !ok {
			// Token already consumed by UnwrapValues; clear the processed
			// flag so observability shows the pod as failed (vs silently
			// skipped on subsequent informer events).
			r.mu.Lock()
			delete(r.processed, string(pod.UID))
			r.mu.Unlock()
			unwrapErrors.WithLabelValues("missing_field").Inc()
			return errors.Newf("unwrapped data missing field %q", field)
		}
		mappings[ph] = v
	}

	// Collect all container IDs from regular, init, and ephemeral containers.
	// We need at least one container ID before proceeding; if none are assigned
	// yet, wait briefly and retry on the next informer event.
	containerIDs := collectContainerIDs(pod)
	if len(containerIDs) == 0 {
		// Reset processed flag so the next informer event retries.
		r.mu.Lock()
		delete(r.processed, string(pod.UID))
		r.mu.Unlock()
		return errors.New("no container ID assigned yet; will retry on next informer event")
	}

	// Program the BPF map for every container: they all run in the same pod
	// and need the same credential substitution.
	var cgroupIDs []uint64
	for _, cid := range containerIDs {
		cgroupID, err := r.resolveCG(string(pod.UID), cid)
		if err != nil {
			// Best-effort: skip containers whose cgroup can't be resolved
			// (e.g. already exited init containers).
			r.log.Warnf("processPodAdded(%s/%s): resolve cgroup for %s: %v (skipped)", pod.Namespace, pod.Name, cid, err)
			continue
		}
		if err := r.mapWriter.PutMapping(cgroupID, mappings); err != nil {
			return errors.Wrapf(err, "BPF map put for container %s", cid)
		}
		mapSize.Inc()
		cgroupIDs = append(cgroupIDs, cgroupID)
	}
	if len(cgroupIDs) == 0 {
		return errors.New("could not resolve any cgroup ID for pod containers")
	}

	pm := PersistedMapping{Mappings: mappings, CgroupIDs: cgroupIDs}
	if err := r.persister.Save(string(pod.UID), pm); err != nil {
		return errors.Wrap(err, "tmpfs persist")
	}
	mappingsLoaded.Inc()
	return nil
}

func (r *runner) processPodDeleted(pod *corev1.Pod) {
	if pod.Spec.NodeName != r.nodeName {
		return
	}
	r.mu.Lock()
	delete(r.processed, string(pod.UID))
	r.mu.Unlock()

	// Use the cgroup_ids stored in tmpfs to delete ALL BPF map entries for
	// the pod. This is more reliable than re-resolving cgroups from the pod
	// object (which may already be stale at deletion time).
	pm, err := r.persister.Load(string(pod.UID))
	if err == nil {
		deletedCount := 0
		for _, cg := range pm.CgroupIDs {
			if err := r.mapWriter.DeleteMapping(cg); err == nil {
				mapSize.Dec()
				deletedCount++
			}
		}
		if deletedCount > 0 {
			mappingsLoaded.Dec()
		}
	}
	_ = r.persister.Delete(string(pod.UID))
}

// restoreFromTmpfs preloads the in-memory processed-set so we don't
// re-unwrap tokens that were already consumed by an earlier DS instance.
// Full BPF map repopulation (using the stored cgroup_ids) is done in
// restoreBPFMaps after the informer cache syncs.
func (r *runner) restoreFromTmpfs() error {
	all, err := r.persister.LoadAll()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for uid := range all {
		r.processed[uid] = struct{}{}
	}
	return nil
}

// collectContainerIDs returns all non-empty container IDs from the pod's
// regular, init, and ephemeral container statuses.
func collectContainerIDs(pod *corev1.Pod) []string {
	var ids []string
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.ContainerID != "" {
			ids = append(ids, cs.ContainerID)
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.ContainerID != "" {
			ids = append(ids, cs.ContainerID)
		}
	}
	for _, cs := range pod.Status.EphemeralContainerStatuses {
		if cs.ContainerID != "" {
			ids = append(ids, cs.ContainerID)
		}
	}
	return ids
}

// Prometheus metrics
var (
	mappingsLoaded = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "vault_injector_bpf_mappings_loaded",
		Help: "Number of pod mappings currently programmed in the BPF map.",
	})
	mapSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "vault_injector_bpf_map_size",
		Help: "Number of cgroup entries in the BPF map (capacity is bpf.maxMappingsPerNode).",
	})
	unwrapErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vault_injector_bpf_unwrap_errors_total",
		Help: "Number of failed Vault unwraps from the BPF runner.",
	}, []string{"reason"})
)

func init() {
	prometheus.MustRegister(mappingsLoaded, mapSize, unwrapErrors)
}
