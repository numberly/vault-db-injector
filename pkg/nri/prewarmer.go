package nri

import (
	"context"
	"time"

	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"golang.org/x/sync/semaphore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// resolveFn is the function signature of plugin.resolveMappingWithSource,
// abstracted so tests can substitute a stub resolver.
type resolveFn func(ctx context.Context, uid, namespace, name, source string) (map[string]string, error)

// prewarmer watches labelled pods on the local node via a SharedInformer and
// pre-populates plugin.cache before NRI's CreateContainer fires. See the
// design spec at docs/superpowers/specs/2026-05-13-nri-prewarmer-design.md.
//
// The lister is private to this struct — fetchAndBuildMapping continues to do
// a linearizable apiserver GET for trust-establishing reads.
type prewarmer struct {
	plugin   *plugin
	client   kubernetes.Interface
	nodeName string
	sem      *semaphore.Weighted
	log      logger.Logger
	// resolver is the function called for each prewarm fetch. Defaults to
	// plugin.resolveMappingWithSource; replaced by stubs in tests.
	resolver resolveFn
	// fetchTimeout caps each async fetch context. Generous — the prewarmer
	// is NOT on containerd's hot path.
	fetchTimeout time.Duration
}

func newPrewarmer(p *plugin, client kubernetes.Interface, nodeName string, maxConcurrent int, log logger.Logger) *prewarmer {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	pw := &prewarmer{
		plugin:       p,
		client:       client,
		nodeName:     nodeName,
		sem:          semaphore.NewWeighted(int64(maxConcurrent)),
		log:          log,
		fetchTimeout: 30 * time.Second,
	}
	pw.resolver = p.resolveMappingWithSource
	return pw
}

// Run starts the informer and blocks until ctx is cancelled.
func (pw *prewarmer) Run(ctx context.Context) error {
	if pw.client == nil || pw.nodeName == "" {
		pw.log.Warn("NRI prewarmer disabled (no k8s client or NODE_NAME)")
		<-ctx.Done()
		return nil
	}
	podLabel := pw.plugin.cfg.NRI.PodLabel
	factory := informers.NewSharedInformerFactoryWithOptions(
		pw.client, 0,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			if podLabel != "" {
				opts.LabelSelector = podLabel + "=true"
			}
			opts.FieldSelector = "spec.nodeName=" + pw.nodeName
		}),
	)
	podInformer := factory.Core().V1().Pods().Informer()
	if _, err := podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    pw.onAdd,
		UpdateFunc: func(_, _ any) {}, // see spec: pod identity fields are immutable post-admission
		DeleteFunc: pw.onDelete,
	}); err != nil {
		pw.log.Errorf("NRI prewarmer AddEventHandler: %v", err)
		return err
	}
	factory.Start(ctx.Done())
	// Wait for the initial LIST to populate the lister before declaring ready.
	// Tests rely on this to avoid races between Create() and event delivery.
	if !cache.WaitForCacheSync(ctx.Done(), podInformer.HasSynced) {
		pw.log.Warn("NRI prewarmer cache sync cancelled before completion")
		return nil
	}
	pw.log.Infof("NRI prewarmer running for node %s (label=%s)",
		pw.nodeName, podLabel)
	<-ctx.Done()
	return nil
}

func (pw *prewarmer) onAdd(obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return
	}
	if pod.DeletionTimestamp != nil {
		metrics.NRIPrewarmError.WithLabelValues("terminating_pod").Inc()
		return
	}
	if !pw.sem.TryAcquire(1) {
		metrics.NRIPrewarmError.WithLabelValues("semaphore_full").Inc()
		pw.log.Warnf("NRI prewarmer semaphore full, skipping pod %s/%s (uid=%s)",
			pod.Namespace, pod.Name, pod.UID)
		return
	}
	uid := string(pod.UID)
	ns := pod.Namespace
	name := pod.Name
	// DeletionTimestamp was checked at handler entry. Pods that transition
	// to Terminating after dispatch are handled by DeleteFunc (cache evict)
	// + revoker safetyNetSync (Vault revocation) — see design spec §4.
	go func() {
		defer pw.sem.Release(1)
		metrics.NRIPrewarmInflight.Inc()
		defer metrics.NRIPrewarmInflight.Dec()
		ctx, cancel := context.WithTimeout(context.Background(), pw.fetchTimeout)
		defer cancel()
		if _, err := pw.resolver(ctx, uid, ns, name, "prewarm"); err != nil {
			pw.log.Warnf("NRI prewarm failed for pod %s/%s (uid=%s): %v", ns, name, uid, err)
			metrics.NRIPrewarmError.WithLabelValues("vault_fetch").Inc()
			return
		}
		metrics.NRIPrewarmSuccess.Inc()
	}()
}

func (pw *prewarmer) onDelete(obj any) {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		// Handle DeletedFinalStateUnknown which informer delivers when it
		// missed the actual delete event during a watch interruption.
		if u, uok := obj.(cache.DeletedFinalStateUnknown); uok {
			if p, pok := u.Obj.(*corev1.Pod); pok {
				pod = p
			}
		}
		if pod == nil {
			return
		}
	}
	if pw.plugin.evictCacheEntry(string(pod.UID)) {
		pw.log.Infof("NRI prewarmer DeleteFunc evicted cache for pod %s/%s (uid=%s)",
			pod.Namespace, pod.Name, pod.UID)
	}
}
