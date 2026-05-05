package nri

import (
	"context"
	"net/url"
	"strings"
	"sync"

	"github.com/cockroachdb/errors"
	nriapi "github.com/containerd/nri/pkg/api"
	"github.com/containerd/nri/pkg/stub"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"github.com/numberly/vault-db-injector/pkg/placeholder"
	"golang.org/x/sync/singleflight"
)

type plugin struct {
	cfg *config.Config
	log logger.Logger

	mu    sync.Mutex
	cache map[string]map[string]string // pod UID → placeholder→value map
	// sf deduplicates concurrent fetchAndBuildMapping calls for the same pod UID.
	// Multi-container pods trigger CreateContainer near-simultaneously; without
	// singleflight both calls would issue separate Vault credentials — only the
	// second cache write survives, leaving the first token+lease unmanageable.
	sf singleflight.Group
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

// CreateContainer is the substitution hook. Reads the user's existing
// db-creds-injector.numberly.io/* annotations (same ones the webhook
// reads at admission), filters by the configured pod label, scans
// container env for placeholder strings, fetches credentials from
// Vault, and emits a ContainerAdjustment with substituted env.
//
// Transparent design (no nri-mapping annotation):
// - The webhook only puts placeholders in env at admission. It does
//   not stamp any new annotation on the pod.
// - The plugin recognises that a pod is "ours" by:
//   1. The configured pod label (e.g. vault-db-injector="true").
//      This is the same label the webhook's objectSelector uses,
//      so we never process a pod the webhook didn't.
//   2. The presence of one or more placeholders (fixed shape) in
//      the container env, with their env-key matching the user's
//      env-key-* annotation.
func (p *plugin) CreateContainer(ctx context.Context, pod *nriapi.PodSandbox, container *nriapi.Container) (*nriapi.ContainerAdjustment, []*nriapi.ContainerUpdate, error) {
	// Filter by configured pod label. With multiple injector releases on
	// the same cluster (e.g. prod + dev), each plugin DS only processes
	// pods carrying its own label.
	if label := p.cfg.NRI.PodLabel; label != "" {
		if pod.GetLabels()[label] != "true" {
			return nil, nil, nil
		}
	}

	// Scan env for placeholders. If none, this pod has no NRI-shaped
	// substitutions to perform, regardless of annotations.
	inEnv := container.GetEnv()
	if !envHasAnyPlaceholder(inEnv) {
		return nil, nil, nil
	}

	mapping, err := p.resolveMapping(ctx, pod.GetUid(), pod.GetNamespace(), pod.GetName())
	if err != nil {
		p.log.Errorf("credential fetch failed for pod %s/%s: %v", pod.GetNamespace(), pod.GetName(), err)
		metrics.NRIUnwrapFailures.WithLabelValues("fetch_error").Inc()
		return nil, nil, nil
	}
	if len(mapping) == 0 {
		// Pod has placeholders in env but the plugin couldn't resolve any
		// of them (no matching dbConfiguration found by env-key). Skip
		// silently; the visible failure will be the app crashing on the
		// literal placeholder string.
		return nil, nil, nil
	}

	outEnv := Substitute(inEnv, mapping)

	// Emit AddEnv ONLY for env vars that actually changed. NRI rejects a
	// CreateContainer when two plugin connections both adjust the same key
	// (even with the same value), so reasserting unchanged vars like
	// ROCKET_PORT would conflict with any other plugin — or with a stale
	// connection of this same plugin that containerd hasn't cleaned up
	// after a DS pod restart.
	adj := &nriapi.ContainerAdjustment{}
	changed := 0
	n := len(inEnv)
	if len(outEnv) < n {
		n = len(outEnv)
	}
	for i := 0; i < n; i++ {
		if inEnv[i] == outEnv[i] {
			continue
		}
		k, v := splitKV(outEnv[i])
		adj.AddEnv(k, v)
		changed++
	}
	if changed == 0 {
		return nil, nil, nil
	}
	metrics.NRISubstitutionsTotal.WithLabelValues().Inc()
	return adj, nil, nil
}

// RemovePodSandbox evicts the per-pod cache entry and persists.
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
// per-pod cache so multiple containers in the same pod share one
// credential fetch. Cache is write-through to disk so the mapping
// survives plugin restart within the pod's lifetime.
//
// On cache miss the plugin reads the pod's annotations (via the K8s
// API for trusted identity), runs CanIGetRoles for the actual pod's
// SA, and creates dynamic database credentials. The lease is tagged
// with the pod UID so the existing renewer/revoker pipeline picks
// it up unchanged.
func (p *plugin) resolveMapping(ctx context.Context, podUID, podNamespace, podName string) (map[string]string, error) {
	p.mu.Lock()
	cached, ok := p.cache[podUID]
	p.mu.Unlock()
	if ok {
		return cached, nil
	}

	// Single-flight: the first concurrent caller for a given podUID fetches
	// credentials from Vault; all other callers for the same pod wait and
	// share the result. This prevents duplicate credential issuance when
	// multiple containers in the same pod trigger CreateContainer simultaneously.
	v, err, shared := p.sf.Do(podUID, func() (interface{}, error) {
		// Re-check cache under the singleflight slot — a concurrent caller
		// that arrived just before us may have already populated it.
		p.mu.Lock()
		if cached, ok := p.cache[podUID]; ok {
			p.mu.Unlock()
			return cached, nil
		}
		p.mu.Unlock()

		mapping, _, err := fetchAndBuildMapping(ctx, p.cfg, podUID, podNamespace, podName)
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
	})
	if err != nil {
		return nil, err
	}
	if shared {
		metrics.NRIResolveDuplicateTotal.WithLabelValues().Inc()
	}
	return v.(map[string]string), nil
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

// envHasAnyPlaceholder is a fast-path filter — returns true if any env
// entry's value contains at least one well-shaped placeholder. Avoids
// calling the K8s API and Vault for pods that don't need substitution.
func envHasAnyPlaceholder(env []string) bool {
	for _, line := range env {
		_, v := splitKV(line)
		if !strings.Contains(v, placeholder.Prefix) {
			continue
		}
		// Cheap heuristic: prefix present. Don't run full IsPlaceholder
		// here on the whole value (URL parsing in URI mode), just
		// confirm something looks like a placeholder.
		return true
	}
	return false
}

// extractPlaceholdersFromEnv scans a container's env for placeholder
// strings, then maps each to the corresponding credential field
// ("username" or "password") using the user's env-key-dbuser /
// env-key-dbpassword / env-key-uri annotations on the DbConfiguration.
//
// Returns map[placeholder]credential-field. Empty map = nothing to do.
func extractPlaceholdersFromEnv(env []string, dbConf k8s.DbConfiguration) map[string]string {
	out := make(map[string]string, 2)
	mode := strings.ToLower(dbConf.Mode)
	if mode == "" {
		mode = k8s.DbModeClassic
	}
	switch mode {
	case k8s.DbModeClassic:
		userKeys := splitCSV(dbConf.DbUserEnvKey)
		passKeys := splitCSV(dbConf.DbPasswordEnvKey)
		for _, line := range env {
			k, v := splitKV(line)
			if !placeholder.IsPlaceholder(v) {
				continue
			}
			if containsString(userKeys, k) {
				out[v] = "username"
			} else if containsString(passKeys, k) {
				out[v] = "password"
			}
		}
	case k8s.DbModeURI:
		uriKeys := splitCSV(dbConf.DbURIEnvKey)
		for _, line := range env {
			k, v := splitKV(line)
			if !containsString(uriKeys, k) {
				continue
			}
			u, err := url.Parse(v)
			if err != nil || u.User == nil {
				continue
			}
			if user := u.User.Username(); placeholder.IsPlaceholder(user) {
				out[user] = "username"
			}
			if pass, set := u.User.Password(); set && placeholder.IsPlaceholder(pass) {
				out[pass] = "password"
			}
		}
	}
	return out
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func containsString(s []string, target string) bool {
	for _, x := range s {
		if x == target {
			return true
		}
	}
	return false
}

// stubFor wires the plugin into the NRI stub framework. Plugin name and
// idx are configurable so multiple injector releases (e.g. prod + dev)
// can register independent plugins on the same containerd socket.
func stubFor(p *plugin) (stub.Stub, error) {
	name := p.cfg.NRI.PluginName
	if name == "" {
		name = "vault-db-injector"
	}
	idx := p.cfg.NRI.PluginIndex
	if idx == "" {
		idx = "10"
	}
	s, err := stub.New(p,
		stub.WithPluginName(name),
		stub.WithPluginIdx(idx),
		stub.WithSocketPath(p.cfg.NRI.SocketPath),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create NRI stub")
	}
	return s, nil
}
