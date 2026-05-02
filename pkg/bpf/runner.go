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
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

// Run is the body of controller.RunBPF on Linux. Blocks until ctx is done.
func Run(ctx context.Context, cfg *config.Config, clientset k8s.KubernetesClient, log logger.Logger) error {
	if err := checkCgroupSetup(); err != nil {
		return errors.Wrap(err, "BPF runner precondition check")
	}

	loader, err := Load(cfg.BPF.MaxMappingsPerNode)
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

	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		clientset.RawClientset(),
		30*time.Second,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.OneTermEqualSelector("spec.nodeName", nodeName).String()
		}),
	)
	// Capture the lister before Start so restoreBPFMaps can use it after sync.
	podLister := informerFactory.Core().V1().Pods().Lister()

	r := &runner{
		nodeName:     nodeName,
		log:          log,
		unwrapper:    vaultConn,
		mapWriter:    loader,
		persister:    persister,
		resolveCG:    ResolveCgroupID,
		resolvePodCG: ResolvePodCgroupID,
		processed:    make(map[string]struct{}),
	}

	// Preload the processed set from tmpfs so that informer add events that
	// arrive before restoreBPFMaps runs do not re-unwrap already-consumed tokens.
	if err := r.restoreFromTmpfs(); err != nil {
		log.Errorf("BPF tmpfs restore (processed set): %v", err)
	}

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

	// After the cache syncs we have a complete local pod list. Re-program the
	// BPF map for all pods that have a tmpfs entry and are still running here.
	if err := r.restoreBPFMaps(podLister); err != nil {
		log.Errorf("BPF map restore after cache sync: %v", err)
		// Non-fatal: existing pods may see placeholder values until the
		// next execve triggers a CrashLoopBackoff; the informer update path
		// will re-program them if they restart.
	}

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
type podCgroupResolver func(podUID string) (uint64, error)

type runner struct {
	nodeName     string
	log          logger.Logger
	unwrapper    unwrapper
	mapWriter    bpfMapWriter
	persister    *Persister
	resolveCG    cgroupResolver
	resolvePodCG podCgroupResolver

	mu        sync.Mutex
	processed map[string]struct{}
	// restoredUIDs are pod UIDs found on tmpfs at startup. restoreBPFMaps
	// re-programs ONLY these. New pods that arrive during the informer
	// cache sync window are handled by processPodAdded, never by
	// restoreBPFMaps — preventing mappingsLoaded from being double-counted.
	restoredUIDs map[string]PersistedMapping
}

func (r *runner) processPodAdded(ctx context.Context, pod *corev1.Pod) error {
	if pod.Spec.NodeName != r.nodeName {
		return nil
	}
	raw, ok := pod.Annotations[k8s.ANNOTATION_BPF_MAPPING]
	if !ok {
		return nil
	}

	uid := string(pod.UID)

	// Resolve the pod-level cgroup_id. kubelet creates the pod cgroup at
	// admission time, BEFORE any container starts — so this id exists by the
	// time the informer surfaces the pod to us. Programming the BPF map at
	// the pod-level id (in addition to per-container ids) means the BPF
	// program's ancestor walk catches an init container's first execve even
	// if it races our discovery of per-container cgroup ids.
	//
	// If the pod cgroup is not yet visible (very tight race on slow nodes,
	// or the pod is not actually scheduled to this node yet despite the
	// nodeName match), we silently wait for the next informer event. We do
	// NOT consume the wrap token until we have a cgroup to program.
	podCgroupID, err := r.resolvePodCG(uid)
	if err != nil {
		return nil
	}

	r.mu.Lock()
	if _, done := r.processed[uid]; done {
		r.mu.Unlock()
		return nil
	}
	r.processed[uid] = struct{}{}
	r.mu.Unlock()

	var payload k8s.BPFMapping
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		r.mu.Lock()
		delete(r.processed, uid)
		r.mu.Unlock()
		return errors.Wrap(err, "parse bpf-mapping annotation")
	}

	values, err := r.unwrapper.UnwrapValues(ctx, payload.WrapToken)
	if err != nil {
		// Remove from processed so a retry can happen if the token is still
		// valid (e.g. transient Vault network blip — the API call may have
		// failed before the token was actually consumed).
		r.mu.Lock()
		delete(r.processed, uid)
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
			delete(r.processed, uid)
			r.mu.Unlock()
			unwrapErrors.WithLabelValues("missing_field").Inc()
			return errors.Newf("unwrapped data missing field %q", field)
		}
		mappings[ph] = v
	}

	// Program the pod-level cgroup FIRST. The BPF program's ancestor walk
	// resolves any container in this pod via this single entry, so even if
	// per-container resolution fails or runs late, substitution still works.
	cgroupIDs := []uint64{podCgroupID}
	programmed := []uint64{}
	if err := r.mapWriter.PutMapping(podCgroupID, mappings); err != nil {
		r.mu.Lock()
		delete(r.processed, uid)
		r.mu.Unlock()
		return errors.Wrap(err, "BPF map put for pod cgroup")
	}
	programmed = append(programmed, podCgroupID)
	mapSize.Inc()

	// Best-effort: also program each container cgroup. Faster lookup at
	// runtime (no ancestor walk) and a defence-in-depth if the kernel ever
	// changes ancestor-id semantics. Skip silently on failure.
	for _, cid := range collectContainerIDs(pod) {
		cgroupID, err := r.resolveCG(uid, cid)
		if err != nil {
			r.log.Warnf("processPodAdded(%s/%s): resolve cgroup for %s: %v (skipped, ancestor walk will cover)", pod.Namespace, pod.Name, cid, err)
			continue
		}
		if cgroupID == podCgroupID {
			continue // already programmed
		}
		if err := r.mapWriter.PutMapping(cgroupID, mappings); err != nil {
			r.log.Warnf("processPodAdded(%s/%s): put container cgroup %d: %v (skipped)", pod.Namespace, pod.Name, cgroupID, err)
			continue
		}
		programmed = append(programmed, cgroupID)
		mapSize.Inc()
		cgroupIDs = append(cgroupIDs, cgroupID)
	}

	pm := PersistedMapping{Mappings: mappings, CgroupIDs: cgroupIDs}
	if err := r.persister.Save(uid, pm); err != nil {
		persistErrors.WithLabelValues("save").Inc()
		for _, cg := range programmed {
			if delErr := r.mapWriter.DeleteMapping(cg); delErr == nil {
				mapSize.Dec()
			}
		}
		r.mu.Lock()
		delete(r.processed, uid)
		r.mu.Unlock()
		return errors.Wrap(err, "tmpfs persist")
	}
	mappingsLoaded.Inc()
	return nil
}

func (r *runner) processPodDeleted(pod *corev1.Pod) {
	uid := string(pod.UID)

	// Try to load the tmpfs entry first. If found, proceed with cleanup
	// regardless of NodeName — a tombstone event may arrive with an empty
	// NodeName after an informer re-sync, but the entry still needs cleaning.
	pm, err := r.persister.Load(uid)
	if err != nil {
		// Not our pod (or tmpfs gone) — nothing to clean up.
		return
	}

	r.mu.Lock()
	delete(r.processed, uid)
	r.mu.Unlock()

	// Use the cgroup_ids stored in tmpfs to delete ALL BPF map entries for
	// the pod. This is more reliable than re-resolving cgroups from the pod
	// object (which may already be stale at deletion time).
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

	if err := r.persister.Delete(uid); err != nil {
		persistErrors.WithLabelValues("delete").Inc()
	}
}

// restoreFromTmpfs preloads the in-memory processed-set so we don't
// re-unwrap tokens that were already consumed by an earlier DS instance.
// Full BPF map repopulation (using the stored cgroup_ids) is done in
// restoreBPFMaps after the informer cache syncs.
//
// The snapshot is also stored in restoredUIDs so that restoreBPFMaps
// iterates only pre-existing pods, not pods written by processPodAdded
// during the WaitForCacheSync window (which would double-count mappingsLoaded).
func (r *runner) restoreFromTmpfs() error {
	all, err := r.persister.LoadAll()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoredUIDs = make(map[string]PersistedMapping, len(all))
	for uid, pm := range all {
		r.processed[uid] = struct{}{}
		r.restoredUIDs[uid] = pm
	}
	return nil
}

// restoreBPFMaps re-programs the BPF map for pods that had a tmpfs entry at
// startup and are still running on this node. It must be called after the
// informer cache has synced so that the pod lister reflects current cluster
// state.
//
// It iterates the restoredUIDs snapshot captured by restoreFromTmpfs, NOT a
// fresh LoadAll. This prevents pods written to tmpfs by processPodAdded
// during the WaitForCacheSync window from being double-counted in
// mappingsLoaded.
//
// For each snapshot entry:
//   - If the pod is still present in the lister → re-program its cgroup_ids
//     using the persisted mappings (token already consumed; no Vault call needed).
//   - If the pod is gone (rescheduled elsewhere during DS downtime) → delete
//     the stale tmpfs entry.
func (r *runner) restoreBPFMaps(lister listersv1.PodLister) error {
	r.mu.Lock()
	restored := r.restoredUIDs
	r.restoredUIDs = nil // free memory; no longer needed after this
	r.mu.Unlock()

	if len(restored) == 0 {
		return nil
	}

	// Build a set of pod UIDs currently running on this node.
	pods, err := lister.List(labels.Everything())
	if err != nil {
		return errors.Wrap(err, "list pods from informer cache")
	}
	runningUIDs := make(map[string]struct{}, len(pods))
	for _, p := range pods {
		if p.Spec.NodeName == r.nodeName {
			runningUIDs[string(p.UID)] = struct{}{}
		}
	}

	for uid, pm := range restored {
		if _, running := runningUIDs[uid]; !running {
			// Pod is no longer on this node — clean up the stale entry.
			r.log.Infof("restoreBPFMaps: pod %s no longer on node %s, deleting tmpfs entry", uid, r.nodeName)
			_ = r.persister.Delete(uid)
			r.mu.Lock()
			delete(r.processed, uid)
			r.mu.Unlock()
			continue
		}

		// Re-program the BPF map for each cgroup_id stored for this pod.
		for _, cgroupID := range pm.CgroupIDs {
			if err := r.mapWriter.PutMapping(cgroupID, pm.Mappings); err != nil {
				r.log.Errorf("restoreBPFMaps: PutMapping(cgroup=%d, pod=%s): %v", cgroupID, uid, err)
				continue
			}
			mapSize.Inc()
		}
		mappingsLoaded.Inc()
		r.log.Infof("restoreBPFMaps: restored %d cgroup(s) for pod %s", len(pm.CgroupIDs), uid)
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
	persistErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "vault_injector_bpf_persist_errors_total",
		Help: "Number of tmpfs persist failures by operation.",
	}, []string{"op"})
)

func init() {
	prometheus.MustRegister(mappingsLoaded, mapSize, unwrapErrors, persistErrors)
}
