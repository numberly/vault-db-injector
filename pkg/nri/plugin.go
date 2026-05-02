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
func (p *plugin) Synchronize(_ context.Context, _ []*nriapi.PodSandbox, _ []*nriapi.Container) ([]*nriapi.ContainerUpdate, error) {
	p.log.Info("NRI plugin synchronized with containerd")
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

// RemovePodSandbox evicts the per-pod unwrap cache entry.
func (p *plugin) RemovePodSandbox(_ context.Context, pod *nriapi.PodSandbox) error {
	p.mu.Lock()
	delete(p.cache, pod.GetUid())
	p.mu.Unlock()
	return nil
}

// resolveMapping returns the placeholder→value map for a pod, using a
// per-pod cache so multiple containers in the same pod share one unwrap
// (the wrap-token is single-use).
func (p *plugin) resolveMapping(ctx context.Context, podUID string, m k8s.NRIMapping) (map[string]string, error) {
	p.mu.Lock()
	cached, ok := p.cache[podUID]
	p.mu.Unlock()
	if ok {
		return cached, nil
	}
	mapping, err := unwrapAndBuildMapping(ctx, p.cfg, m)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.cache[podUID] = mapping
	p.mu.Unlock()
	return mapping, nil
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
