package nri

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/cockroachdb/errors"
	nriapi "github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
)

type plugin struct {
	cfg *config.Config
	log logger.Logger

	mu    sync.Mutex
	cache map[string]map[string]string // pod UID → placeholder→value map
}

func newPlugin(cfg *config.Config, log logger.Logger) *plugin {
	return &plugin{
		cfg:   cfg,
		log:   log,
		cache: make(map[string]map[string]string),
	}
}

// Synchronize is called when the plugin connects/reconnects to containerd.
// Already-running containers cannot be mutated (envp is fixed post-execve);
// we only need to be ready for future CreateContainer events.
//
// We use this opportunity to evict cache entries for pods that no longer
// exist on this node — covers the case where pods were deleted while the
// plugin DS was down (RemovePodSandbox missed).
func (p *plugin) Synchronize(_ context.Context, pods []*nriapi.PodSandbox, _ []*nriapi.Container) ([]*nriapi.ContainerUpdate, error) {
	live := make(map[string]struct{}, len(pods))
	for _, pod := range pods {
		live[pod.GetUid()] = struct{}{}
	}
	p.mu.Lock()
	evicted := 0
	for uid := range p.cache {
		if _, alive := live[uid]; !alive {
			delete(p.cache, uid)
			evicted++
		}
	}
	cacheSize := len(p.cache)
	p.mu.Unlock()
	if evicted > 0 {
		if err := saveCache(p.cfg.NRI.CachePath, p.snapshot()); err != nil {
			p.log.Warnf("save cache after Synchronize evict: %v", err)
		}
	}
	p.log.Infof("NRI plugin synchronized with containerd: %d cached pods, %d stale evicted", cacheSize, evicted)
	return nil, nil
}

// CreateContainer is the substitution hook. Reads the pod-sandbox annotation,
// unwraps the wrap-token (cached per pod), and emits a ContainerAdjustment
// with substituted env.
func (p *plugin) CreateContainer(ctx context.Context, pod *nriapi.PodSandbox, container *nriapi.Container) (*nriapi.ContainerAdjustment, []*nriapi.ContainerUpdate, error) {
	annotations := pod.GetAnnotations()
	raw, ok := annotations[k8s.ANNOTATION_NRI_MAPPING]
	if !ok || raw == "" {
		return nil, nil, nil
	}
	var m k8s.NRIMapping
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		p.log.Warnf("malformed nri-mapping annotation on pod %s/%s: %v", pod.GetNamespace(), pod.GetName(), err)
		metrics.NRIUnwrapFailures.WithLabelValues("malformed_annotation").Inc()
		return nil, nil, nil
	}

	mapping, err := p.resolveMapping(ctx, pod.GetUid(), m)
	if err != nil {
		p.log.Errorf("unwrap failed for pod %s/%s: %v", pod.GetNamespace(), pod.GetName(), err)
		metrics.NRIUnwrapFailures.WithLabelValues("unwrap_error").Inc()
		return nil, nil, nil
	}

	inEnv := container.GetEnv()
	outEnv := Substitute(inEnv, mapping)

	// Only emit an adjustment if something actually changed.
	changed := false
	for i := range inEnv {
		if i >= len(outEnv) || inEnv[i] != outEnv[i] {
			changed = true
			break
		}
	}
	if !changed {
		return nil, nil, nil
	}

	adj := &nriapi.ContainerAdjustment{}
	for _, line := range outEnv {
		k, v := splitKV(line)
		adj.AddEnv(k, v)
	}
	metrics.NRISubstitutionsTotal.WithLabelValues().Inc()
	return adj, nil, nil
}

// RemovePodSandbox evicts the per-pod unwrap cache entry and persists.
func (p *plugin) RemovePodSandbox(_ context.Context, pod *nriapi.PodSandbox) error {
	p.mu.Lock()
	_, existed := p.cache[pod.GetUid()]
	delete(p.cache, pod.GetUid())
	p.mu.Unlock()
	if existed {
		if err := saveCache(p.cfg.NRI.CachePath, p.snapshot()); err != nil {
			p.log.Warnf("save cache after RemovePodSandbox %s: %v", pod.GetUid(), err)
		}
	}
	return nil
}

// resolveMapping returns the placeholder→value map for a pod, using a
// per-pod cache so multiple containers in the same pod share one credential
// fetch. Cache is write-through to disk so the mapping survives plugin
// restart within the pod's lifetime.
//
// On cache miss the plugin authenticates to Vault as itself, re-runs
// CanIGetRoles for the target pod identity (defense-in-depth against
// annotation forgery), and creates dynamic database credentials. The lease
// is tagged with the pod UID so the existing renewer/revoker pipeline
// picks it up unchanged.
func (p *plugin) resolveMapping(ctx context.Context, podUID string, m k8s.NRIMapping) (map[string]string, error) {
	p.mu.Lock()
	cached, ok := p.cache[podUID]
	p.mu.Unlock()
	if ok {
		return cached, nil
	}
	mapping, _, err := fetchAndBuildMapping(ctx, p.cfg, m, podUID)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.cache[podUID] = mapping
	p.mu.Unlock()
	if err := saveCache(p.cfg.NRI.CachePath, p.snapshot()); err != nil {
		// Cache write failure is non-fatal: the in-memory cache still works
		// for this plugin instance. We just lose persistence — the pod will
		// hit the bug if both this plugin restarts AND the container retries.
		p.log.Warnf("save cache after fetch for pod %s: %v", podUID, err)
	}
	return mapping, nil
}

// snapshot returns a deep copy of the cache for atomic write to disk.
// Caller must NOT hold p.mu.
func (p *plugin) snapshot() map[string]map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]map[string]string, len(p.cache))
	for uid, m := range p.cache {
		dup := make(map[string]string, len(m))
		for k, v := range m {
			dup[k] = v
		}
		out[uid] = dup
	}
	return out
}

// splitKV splits "KEY=value" on the first '='. If no '=' is present,
// the whole line is treated as the key with an empty value.
func splitKV(line string) (string, string) {
	for i := 0; i < len(line); i++ {
		if line[i] == '=' {
			return line[:i], line[i+1:]
		}
	}
	return line, ""
}

// stubFor wires the plugin into the NRI stub framework.
func stubFor(p *plugin) (stub.Stub, error) {
	s, err := stub.New(p,
		stub.WithPluginName("vault-db-injector"),
		stub.WithPluginIdx("10"),
		stub.WithSocketPath(p.cfg.NRI.SocketPath),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create NRI stub")
	}
	return s, nil
}
