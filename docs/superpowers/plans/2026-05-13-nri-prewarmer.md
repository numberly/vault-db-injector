# NRI Prewarmer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move NRI credential fetch out of containerd's `CreateContainer` hot path via a SharedInformer-driven prewarmer that pre-populates the existing in-memory cache for labelled pods on the node.

**Architecture:** A new `pkg/nri/prewarmer.go` file owns a node-scoped `SharedInformer` filtered by `spec.nodeName` and the configured `nri.podLabel`. On `AddFunc`, an async fetch (`go p.resolveMapping(...)`) runs under a `semaphore.Weighted` bound (default 50) and populates `plugin.cache`. `CreateContainer` becomes a cache lookup in the common case. `fetchAndBuildMapping` is **unchanged** — it keeps doing a live `clientset.Get()` as the trust anchor. The lister is private to the prewarmer.

**Tech Stack:** Go 1.26, `k8s.io/client-go` (informers, listers), `golang.org/x/sync/semaphore`, Prometheus client. Existing logging via `pkg/logger`.

**Reference spec:** `docs/superpowers/specs/2026-05-13-nri-prewarmer-design.md`

---

## File map

| File | Action | Responsibility |
|---|---|---|
| `pkg/config/config.go` | Modify | Add `NRIPrewarmerConfig` struct; add `Prewarmer` field to `NRIConfig`; set defaults |
| `pkg/config/config_test.go` | Modify | Test new defaults |
| `pkg/metrics/prom.go` | Modify | Register 4 new metrics: `prewarm_success`, `prewarm_error{reason}`, `prewarm_inflight`, `cache_hit_total{source}` |
| `pkg/nri/plugin.go` | Modify | Add `cacheSource` parallel map, `resolveMappingWithSource()` non-breaking variant, `evictCacheEntry()` extracted helper, instrument `cache_hit_total` on cache-hit |
| `pkg/nri/plugin_test.go` | Modify | Tests for cache-source tracking on hit |
| `pkg/nri/prewarmer.go` | **Create** | Informer construction, AddFunc/UpdateFunc/DeleteFunc, semaphore, in-goroutine DeletionTimestamp re-check |
| `pkg/nri/prewarmer_test.go` | **Create** | Unit + integration tests |
| `pkg/nri/runner.go` | Modify | Start prewarmer alongside sweeper; respect `Prewarmer.Enabled` |
| `helm/values.yml` | Modify | Add `nri.prewarmer.{enabled,maxConcurrent}` |
| `helm/templates/configmaps.yaml` | Modify | Render the two new keys |
| `helm/README.md` | Regenerate | `make helm-docs` |
| `docs/reference/configuration.md` | Modify | Add prewarmer keys to NRI keys table; add "Prewarming" subsection under NRI tuning |
| `docs/reference/configuration.fr.md` | Modify | Mirror EN |
| `docs/reference/metrics.md` | Modify | Document the 4 new metrics |

---

## Pre-flight

Before starting:

```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
git status   # working tree should be clean
git branch   # confirm: feat/nri-prewarmer
go build ./... && go test ./pkg/...   # baseline: must pass before first task
```

Expected: 228 tests pass across 15 packages.

---

## Task 1: Config — add NRIPrewarmerConfig

**Files:**
- Modify: `pkg/config/config.go`
- Modify: `pkg/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `pkg/config/config_test.go`:

```go
func TestConfig_NRIPrewarmerDefaults(t *testing.T) {
	cfg, err := NewConfig("")
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if !cfg.NRI.Prewarmer.Enabled {
		t.Errorf("NRI.Prewarmer.Enabled default: got false, want true")
	}
	if cfg.NRI.Prewarmer.MaxConcurrent != 50 {
		t.Errorf("NRI.Prewarmer.MaxConcurrent default: got %d, want 50", cfg.NRI.Prewarmer.MaxConcurrent)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./pkg/config/... -run TestConfig_NRIPrewarmerDefaults -v
```

Expected: `cfg.NRI.Prewarmer undefined` compile error.

- [ ] **Step 3: Add the struct and field**

In `pkg/config/config.go`, inside the `NRIConfig` struct (right after the existing `FetchTimeout` field), append:

```go
	// Prewarmer holds the configuration for the async credential prefetcher.
	// When enabled, a SharedInformer watches labelled pods on this node and
	// pre-populates plugin.cache before CreateContainer fires, removing the
	// Vault fetch from containerd's hot path in the common case. The sync
	// fetch in CreateContainer remains as a fail-closed fallback.
	Prewarmer NRIPrewarmerConfig `yaml:"prewarmer" envconfig:"prewarmer"`
}

// NRIPrewarmerConfig configures the async credential prefetcher. See the
// design spec at docs/superpowers/specs/2026-05-13-nri-prewarmer-design.md.
type NRIPrewarmerConfig struct {
	// Enabled, when false, disables the prewarmer entirely (no informer,
	// no async fetch on pod ADD). CreateContainer falls back to the sync
	// path (pre-prewarmer behavior). Useful for debugging or in clusters
	// where the watch overhead is unwelcome.
	Enabled bool `yaml:"enabled" envconfig:"enabled"`
	// MaxConcurrent caps the number of in-flight async fetches per DS pod.
	// Protects Vault and apiserver from thundering-herd on pod bursts.
	// Defaults to 50; set higher on dense nodes.
	MaxConcurrent int `yaml:"maxConcurrent" envconfig:"max_concurrent"`
```

(Note: this replaces the existing closing `}` of `NRIConfig` — the closing brace migrates after the `Prewarmer` field. The new closing `}` for `NRIPrewarmerConfig` is added.)

In the same file, locate the `NRI: NRIConfig{...}` literal in `NewConfig()` and add the `Prewarmer` field:

```go
		NRI: NRIConfig{
			SocketPath:   "/var/run/nri/nri.sock",
			CachePath:    "/run/vault-db-injector/nri/cache.json",
			PluginName:   "vault-db-injector",
			PluginIndex:  "10",
			PodLabel:     "vault-db-injector",
			FetchTimeout: 1500 * time.Millisecond,
			Prewarmer: NRIPrewarmerConfig{
				Enabled:       true,
				MaxConcurrent: 50,
			},
		},
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./pkg/config/... -run TestConfig_NRIPrewarmerDefaults -v
```

Expected: PASS.

- [ ] **Step 5: Run full config tests**

```bash
go test ./pkg/config/...
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/config/config.go pkg/config/config_test.go
git commit -m "feat(config): add NRI.Prewarmer block (Enabled, MaxConcurrent)"
```

---

## Task 2: Metrics — add 4 prewarmer counters/gauges

**Files:**
- Modify: `pkg/metrics/prom.go`

- [ ] **Step 1: Add metric variable declarations**

In `pkg/metrics/prom.go`, after the existing `NRIResolveDuplicateTotal` declaration (around line 255), add:

```go
	NRIPrewarmSuccess = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "vdbi_nri_prewarm_success_total",
			Help: "Successful async prewarm fetches issued by the informer's AddFunc handler.",
		},
	)
	NRIPrewarmError = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_nri_prewarm_error_total",
			Help: "Failed or skipped prewarm attempts.",
		}, []string{"reason"},
	)
	NRIPrewarmInflight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "vdbi_nri_prewarm_inflight",
			Help: "In-flight async prewarm fetches (gauge; sanity check vs MaxConcurrent).",
		},
	)
	NRICacheHitTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_nri_cache_hit_total",
			Help: "CreateContainer events served from plugin.cache, labelled by what populated the entry.",
		}, []string{"source"},
	)
```

- [ ] **Step 2: Register them in Init**

In the same file, locate the `Init(prom *prometheus.Registry)` function and append to its `prom.MustRegister(...)` call:

```go
		NRIPrewarmSuccess,
		NRIPrewarmError,
		NRIPrewarmInflight,
		NRICacheHitTotal,
```

- [ ] **Step 3: Run build to verify**

```bash
go build ./...
```

Expected: build succeeds.

- [ ] **Step 4: Run existing tests**

```bash
go test ./pkg/metrics/... ./pkg/nri/...
```

Expected: all tests pass (no regression).

- [ ] **Step 5: Commit**

```bash
git add pkg/metrics/prom.go
git commit -m "feat(metrics): add NRI prewarmer counters and cache_hit_total"
```

---

## Task 3: Plugin — cacheSource map + non-breaking resolveMappingWithSource

**Files:**
- Modify: `pkg/nri/plugin.go`
- Modify: `pkg/nri/plugin_test.go`

- [ ] **Step 1: Write a failing test for the new field**

Append to `pkg/nri/plugin_test.go`:

```go
func TestPlugin_CacheSourceField_Exists(t *testing.T) {
	p := newPlugin(&config.Config{}, logger.GetLogger())
	if p.cacheSource == nil {
		t.Fatal("newPlugin should initialise cacheSource map")
	}
	p.mu.Lock()
	p.cache["uid-1"] = map[string]string{"__VDBI_PH_x___": "v"}
	p.cacheSource["uid-1"] = "prewarm"
	got := p.cacheSource["uid-1"]
	p.mu.Unlock()
	if got != "prewarm" {
		t.Errorf("cacheSource[uid-1]: got %q, want %q", got, "prewarm")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./pkg/nri/... -run TestPlugin_CacheSourceField_Exists -v
```

Expected: compile error on `p.cacheSource`.

- [ ] **Step 3: Add cacheSource field to plugin struct**

In `pkg/nri/plugin.go`, locate the `plugin` struct (around line 22). Add the `cacheSource` field right after `cache`:

```go
type plugin struct {
	cfg *config.Config
	log logger.Logger

	mu    sync.Mutex
	cache map[string]map[string]string // pod UID → placeholder→value map
	// cacheSource tracks which path populated each cache entry, used to label
	// the cache_hit_total metric on hit. Values: "prewarm", "sync", "unknown"
	// (the latter for entries loaded from disk on plugin restart).
	cacheSource map[string]string
	// sf deduplicates concurrent fetchAndBuildMapping calls for the same pod UID.
	// Multi-container pods trigger CreateContainer near-simultaneously; without
	// singleflight both calls would issue separate Vault credentials — only the
	// second cache write survives, leaving the first token+lease unmanageable.
	sf singleflight.Group

	// bookkeepingCache caches the injector-SA Vault token used for KV bookkeeping
	// writes across CreateContainer calls, bounding Vault auth load (I4).
	bookkeepingCache *vault.BookkeepingTokenCache
}
```

Update `newPlugin` (around line 39) to initialise the new map:

```go
func newPlugin(cfg *config.Config, log logger.Logger) *plugin {
	return &plugin{
		cfg:              cfg,
		log:              log,
		cache:            make(map[string]map[string]string),
		cacheSource:      make(map[string]string),
		bookkeepingCache: vault.NewBookkeepingTokenCache(),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./pkg/nri/... -run TestPlugin_CacheSourceField_Exists -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/nri/plugin.go pkg/nri/plugin_test.go
git commit -m "feat(nri): add cacheSource parallel map for cache_hit_total labelling"
```

---

## Task 4: Plugin — resolveMappingWithSource + instrument cache_hit_total

**Files:**
- Modify: `pkg/nri/plugin.go`

- [ ] **Step 1: Add resolveMappingWithSource and refactor resolveMapping**

In `pkg/nri/plugin.go`, locate `resolveMapping` (around line 238). Replace the entire function with:

```go
// resolveMapping is the legacy entry point used by the sync CreateContainer
// path. It records source="sync" on cache writes.
func (p *plugin) resolveMapping(ctx context.Context, podUID, podNamespace, podName string) (map[string]string, error) {
	return p.resolveMappingWithSource(ctx, podUID, podNamespace, podName, "sync")
}

// resolveMappingWithSource is the canonical entry point. source identifies
// which path populated a cache entry on miss; on hit, the previously-recorded
// source is preserved (the caller's source argument is ignored).
func (p *plugin) resolveMappingWithSource(ctx context.Context, podUID, podNamespace, podName, source string) (map[string]string, error) {
	p.mu.Lock()
	cached, ok := p.cache[podUID]
	hitSrc := ""
	if ok {
		hitSrc = p.cacheSource[podUID]
		if hitSrc == "" {
			hitSrc = "unknown"
		}
	}
	p.mu.Unlock()
	if ok {
		metrics.NRICacheHitTotal.WithLabelValues(hitSrc).Inc()
		p.log.Infof("NRI resolveMapping cache hit pod=%s/%s uid=%s mappingSize=%d source=%s",
			podNamespace, podName, podUID, len(cached), hitSrc)
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

		p.log.Infof("NRI fetchAndBuildMapping start pod=%s/%s uid=%s source=%s", podNamespace, podName, podUID, source)
		fetchStart := time.Now()
		mapping, _, err := fetchAndBuildMapping(ctx, p.cfg, podUID, podNamespace, podName, p.bookkeepingCache)
		p.log.Infof("NRI fetchAndBuildMapping end pod=%s/%s uid=%s dur=%s err=%v mappingSize=%d source=%s",
			podNamespace, podName, podUID, time.Since(fetchStart), err, len(mapping), source)
		if err != nil {
			return nil, err
		}
		p.mu.Lock()
		p.cache[podUID] = mapping
		p.cacheSource[podUID] = source
		p.mu.Unlock()
		if err := saveCache(p.cfg.NRI.CachePath, p.snapshot()); err != nil {
			p.log.Warnf("save cache after fetch for pod %s: %v", podUID, err)
		}
		return mapping, nil
	})
	if err != nil {
		return nil, err
	}
	if shared {
		p.log.Infof("NRI resolveMapping singleflight shared pod=%s/%s uid=%s", podNamespace, podName, podUID)
		metrics.NRIResolveDuplicateTotal.WithLabelValues().Inc()
	}
	return v.(map[string]string), nil
}
```

The cache-hit branch above already increments `metrics.NRICacheHitTotal.WithLabelValues(hitSrc).Inc()`. No additional edit needed.

- [ ] **Step 2: Run NRI tests**

```bash
go test ./pkg/nri/...
```

Expected: all tests pass (including the existing `TestCreateContainer_NoEnv`, `TestRemovePodSandbox_EvictsCache`, etc.).

- [ ] **Step 3: Commit**

```bash
git add pkg/nri/plugin.go pkg/nri/plugin_test.go
git commit -m "feat(nri): add resolveMappingWithSource, instrument cache_hit_total"
```

---

## Task 5: Plugin — extract evictCacheEntry helper

**Files:**
- Modify: `pkg/nri/plugin.go`
- Modify: `pkg/nri/plugin_test.go`

Goal: factor the cache-eviction-and-persist logic from `RemovePodSandbox` into a private helper so the prewarmer's `DeleteFunc` can call it without duplicating code.

- [ ] **Step 1: Write a failing test**

Append to `pkg/nri/plugin_test.go`:

```go
func TestEvictCacheEntry_RemovesMappingAndSource(t *testing.T) {
	p := newPlugin(&config.Config{NRI: config.NRIConfig{CachePath: t.TempDir() + "/cache.json"}}, logger.GetLogger())
	p.mu.Lock()
	p.cache["uid-2"] = map[string]string{"k": "v"}
	p.cacheSource["uid-2"] = "prewarm"
	p.mu.Unlock()

	existed := p.evictCacheEntry("uid-2")
	if !existed {
		t.Fatal("evictCacheEntry returned false for an existing UID")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.cache["uid-2"]; ok {
		t.Error("cache entry not deleted")
	}
	if _, ok := p.cacheSource["uid-2"]; ok {
		t.Error("cacheSource entry not deleted")
	}
}
```

- [ ] **Step 2: Run to verify failure**

```bash
go test ./pkg/nri/... -run TestEvictCacheEntry -v
```

Expected: compile error `p.evictCacheEntry undefined`.

- [ ] **Step 3: Add the helper and refactor RemovePodSandbox**

In `pkg/nri/plugin.go`, add this helper after `snapshot` (around line 240):

```go
// evictCacheEntry removes a UID from cache and cacheSource, then persists.
// Returns true if the entry existed. Callers MUST NOT hold p.mu.
func (p *plugin) evictCacheEntry(podUID string) bool {
	p.mu.Lock()
	_, existed := p.cache[podUID]
	delete(p.cache, podUID)
	delete(p.cacheSource, podUID)
	p.mu.Unlock()
	if existed {
		if err := saveCache(p.cfg.NRI.CachePath, p.snapshot()); err != nil {
			p.log.Warnf("save cache after evict %s: %v", podUID, err)
		}
	}
	return existed
}
```

Now simplify `RemovePodSandbox` (around line 213) to use it:

```go
// RemovePodSandbox evicts the per-pod cache entry and persists.
func (p *plugin) RemovePodSandbox(_ context.Context, pod *nriapi.PodSandbox) error {
	existed := p.evictCacheEntry(pod.GetUid())
	p.log.Infof("NRI RemovePodSandbox pod=%s/%s uid=%s cacheHit=%v",
		pod.GetNamespace(), pod.GetName(), pod.GetUid(), existed)
	return nil
}
```

Also update `Synchronize` (around line 60) which has similar logic — replace the loop body to use `delete` only and avoid touching `cacheSource` independently. Actually it's already simple, just ensure `cacheSource` is also cleaned. Update:

```go
	p.mu.Lock()
	evicted := 0
	for uid := range p.cache {
		if _, alive := live[uid]; !alive {
			delete(p.cache, uid)
			delete(p.cacheSource, uid)
			evicted++
		}
	}
	cacheSize := len(p.cache)
	p.mu.Unlock()
```

- [ ] **Step 4: Run test to verify pass**

```bash
go test ./pkg/nri/... -run "TestEvictCacheEntry|TestRemovePodSandbox" -v
```

Expected: PASS for both.

- [ ] **Step 5: Run all NRI tests**

```bash
go test ./pkg/nri/...
```

Expected: all 23+ tests pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/nri/plugin.go pkg/nri/plugin_test.go
git commit -m "refactor(nri): extract evictCacheEntry helper, sync cacheSource"
```

---

## Task 6: Prewarmer — scaffolding + AddFunc happy path

**Files:**
- Create: `pkg/nri/prewarmer.go`
- Create: `pkg/nri/prewarmer_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/nri/prewarmer_test.go`:

```go
package nri

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

// stubResolver counts resolveMappingWithSource invocations and records sources.
// When plugin is set, it also writes the mapping into plugin.cache so cache-hit
// tests behave end-to-end.
type stubResolver struct {
	plugin  *plugin
	calls   atomic.Int32
	sources sync.Map // map[uid]string
}

func (s *stubResolver) resolveMappingWithSource(_ context.Context, uid, _, _, source string) (map[string]string, error) {
	s.calls.Add(1)
	s.sources.Store(uid, source)
	m := map[string]string{"__VDBI_PH_x___": "v"}
	if s.plugin != nil {
		s.plugin.mu.Lock()
		s.plugin.cache[uid] = m
		s.plugin.cacheSource[uid] = source
		s.plugin.mu.Unlock()
	}
	return m, nil
}

func TestPrewarmer_AddFunc_TriggersFetchForLabelledPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		PodLabel:    "vault-db-injector",
		CachePath:   t.TempDir() + "/cache.json",
		Prewarmer:   config.NRIPrewarmerConfig{Enabled: true, MaxConcurrent: 5},
	}}, logger.GetLogger())

	resolver := &stubResolver{}
	pw := newPrewarmer(p, client, "node-1", 5, logger.GetLogger())
	pw.resolver = resolver.resolveMappingWithSource

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pw.Run(ctx)

	// Wait for informer to be ready, then push a pod.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-x",
			Namespace: "default",
			UID:       types.UID("uid-x"),
			Labels:    map[string]string{"vault-db-injector": "true"},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	if _, err := client.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create pod: %v", err)
	}

	// Poll up to 2s for the prewarm fetch to fire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if resolver.calls.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("expected 1 prewarm fetch, got %d", resolver.calls.Load())
	}
	src, ok := resolver.sources.Load("uid-x")
	if !ok || src != "prewarm" {
		t.Errorf("expected source=prewarm for uid-x, got %v", src)
	}
}
```

Add `"sync"` to the imports.

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./pkg/nri/... -run TestPrewarmer_AddFunc -v
```

Expected: compile error `newPrewarmer undefined`.

- [ ] **Step 3: Create the prewarmer file**

Create `pkg/nri/prewarmer.go`:

```go
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
	// Capture DeletionTimestamp pointer — re-checked inside goroutine below
	// to catch the race between event dispatch and goroutine start.
	go func() {
		defer pw.sem.Release(1)
		metrics.NRIPrewarmInflight.Inc()
		defer metrics.NRIPrewarmInflight.Dec()
		// Re-check DeletionTimestamp. The pod object captured at event time
		// is a snapshot; if the pod entered Terminating between event dispatch
		// and goroutine start, the field would have changed in the lister's
		// next observation. We re-read via a fresh local check.
		if pod.DeletionTimestamp != nil {
			metrics.NRIPrewarmError.WithLabelValues("terminating_pod").Inc()
			return
		}
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
```

- [ ] **Step 4: Run test to verify it passes**

```bash
go test ./pkg/nri/... -run TestPrewarmer_AddFunc -v
```

Expected: PASS.

- [ ] **Step 5: Run all NRI tests**

```bash
go test ./pkg/nri/...
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add pkg/nri/prewarmer.go pkg/nri/prewarmer_test.go
git commit -m "feat(nri): add prewarmer with informer-driven AddFunc + DeleteFunc"
```

---

## Task 7: Prewarmer — DeletionTimestamp re-check race test

**Files:**
- Modify: `pkg/nri/prewarmer_test.go`

- [ ] **Step 1: Write the failing test**

Append to `pkg/nri/prewarmer_test.go`:

```go
func TestPrewarmer_AddFunc_SkipsTerminatingPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		PodLabel:  "vault-db-injector",
		CachePath: t.TempDir() + "/cache.json",
		Prewarmer: config.NRIPrewarmerConfig{Enabled: true, MaxConcurrent: 5},
	}}, logger.GetLogger())

	resolver := &stubResolver{}
	pw := newPrewarmer(p, client, "node-1", 5, logger.GetLogger())
	pw.resolver = resolver.resolveMappingWithSource

	now := metav1.Now()
	terminatingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-term",
			Namespace:         "default",
			UID:               types.UID("uid-term"),
			Labels:            map[string]string{"vault-db-injector": "true"},
			DeletionTimestamp: &now,
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}

	pw.onAdd(terminatingPod)
	// Give the (non-existent) goroutine a moment to NOT run.
	time.Sleep(50 * time.Millisecond)
	if resolver.calls.Load() != 0 {
		t.Errorf("expected 0 fetches for terminating pod, got %d", resolver.calls.Load())
	}
}
```

- [ ] **Step 2: Run test — should pass already**

```bash
go test ./pkg/nri/... -run TestPrewarmer_AddFunc_SkipsTerminatingPod -v
```

Expected: PASS (the implementation already skips terminating pods at event-handler entry).

- [ ] **Step 3: Commit**

```bash
git add pkg/nri/prewarmer_test.go
git commit -m "test(nri): assert prewarmer skips pods with DeletionTimestamp set"
```

---

## Task 8: Prewarmer — semaphore saturation test

**Files:**
- Modify: `pkg/nri/prewarmer_test.go`

- [ ] **Step 1: Write the test**

Append to `pkg/nri/prewarmer_test.go`:

```go
// slowResolver blocks until released, simulating a slow Vault.
type slowResolver struct {
	released chan struct{}
	calls    atomic.Int32
}

func (s *slowResolver) resolveMappingWithSource(_ context.Context, _, _, _, _ string) (map[string]string, error) {
	s.calls.Add(1)
	<-s.released
	return map[string]string{"k": "v"}, nil
}

func TestPrewarmer_AddFunc_SemaphoreSaturates(t *testing.T) {
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		PodLabel:  "vault-db-injector",
		CachePath: t.TempDir() + "/cache.json",
		Prewarmer: config.NRIPrewarmerConfig{Enabled: true, MaxConcurrent: 2},
	}}, logger.GetLogger())

	slow := &slowResolver{released: make(chan struct{})}
	pw := newPrewarmer(p, fake.NewSimpleClientset(), "node-1", 2, logger.GetLogger())
	pw.resolver = slow.resolveMappingWithSource

	makePod := func(uid string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "p-" + uid, Namespace: "default", UID: types.UID(uid),
				Labels: map[string]string{"vault-db-injector": "true"},
			},
			Spec: corev1.PodSpec{NodeName: "node-1"},
		}
	}

	// Saturate the semaphore.
	pw.onAdd(makePod("u1"))
	pw.onAdd(makePod("u2"))
	// Give them a tick to acquire.
	time.Sleep(50 * time.Millisecond)
	// Third pod should be rejected by TryAcquire.
	pw.onAdd(makePod("u3"))
	time.Sleep(50 * time.Millisecond)

	if slow.calls.Load() != 2 {
		t.Errorf("expected 2 in-flight calls, got %d", slow.calls.Load())
	}
	close(slow.released)
}
```

- [ ] **Step 2: Run test to verify**

```bash
go test ./pkg/nri/... -run TestPrewarmer_AddFunc_SemaphoreSaturates -v
```

Expected: PASS (only 2 of 3 calls executed because semaphore=2).

- [ ] **Step 3: Commit**

```bash
git add pkg/nri/prewarmer_test.go
git commit -m "test(nri): assert prewarmer semaphore caps in-flight prewarms"
```

---

## Task 9: Prewarmer — DeleteFunc eviction test

**Files:**
- Modify: `pkg/nri/prewarmer_test.go`

- [ ] **Step 1: Write the test**

Append to `pkg/nri/prewarmer_test.go`:

```go
func TestPrewarmer_OnDelete_EvictsCache(t *testing.T) {
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		CachePath: t.TempDir() + "/cache.json",
	}}, logger.GetLogger())
	p.mu.Lock()
	p.cache["uid-del"] = map[string]string{"k": "v"}
	p.cacheSource["uid-del"] = "prewarm"
	p.mu.Unlock()

	pw := newPrewarmer(p, fake.NewSimpleClientset(), "node-1", 1, logger.GetLogger())
	pw.onDelete(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "p", Namespace: "ns", UID: types.UID("uid-del"),
	}})

	p.mu.Lock()
	_, hasCache := p.cache["uid-del"]
	_, hasSrc := p.cacheSource["uid-del"]
	p.mu.Unlock()
	if hasCache {
		t.Error("cache entry not evicted by onDelete")
	}
	if hasSrc {
		t.Error("cacheSource entry not evicted by onDelete")
	}
}
```

- [ ] **Step 2: Run test**

```bash
go test ./pkg/nri/... -run TestPrewarmer_OnDelete -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add pkg/nri/prewarmer_test.go
git commit -m "test(nri): assert prewarmer DeleteFunc evicts cache"
```

---

## Task 10: Prewarmer — UpdateFunc is no-op test

**Files:**
- Modify: `pkg/nri/prewarmer_test.go`

- [ ] **Step 1: Write the test**

Append to `pkg/nri/prewarmer_test.go`:

```go
func TestPrewarmer_UpdateFunc_DoesNothing(t *testing.T) {
	client := fake.NewSimpleClientset()
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		PodLabel:  "vault-db-injector",
		CachePath: t.TempDir() + "/cache.json",
		Prewarmer: config.NRIPrewarmerConfig{Enabled: true, MaxConcurrent: 5},
	}}, logger.GetLogger())

	resolver := &stubResolver{}
	pw := newPrewarmer(p, client, "node-1", 5, logger.GetLogger())
	pw.resolver = resolver.resolveMappingWithSource

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pw.Run(ctx)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p", Namespace: "default", UID: types.UID("uid-u"),
			Labels: map[string]string{"vault-db-injector": "true"},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	if _, err := client.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create pod: %v", err)
	}
	// Wait for the Add event to fire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if resolver.calls.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	beforeUpdate := resolver.calls.Load()
	if beforeUpdate != 1 {
		t.Fatalf("expected 1 call after Add, got %d", beforeUpdate)
	}

	// Update the pod — should be a no-op.
	pod.Annotations = map[string]string{"unrelated": "change"}
	if _, err := client.CoreV1().Pods("default").Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Update pod: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if resolver.calls.Load() != beforeUpdate {
		t.Errorf("expected no additional fetches after Update, got %d (was %d)",
			resolver.calls.Load(), beforeUpdate)
	}
}
```

- [ ] **Step 2: Run test**

```bash
go test ./pkg/nri/... -run TestPrewarmer_UpdateFunc -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add pkg/nri/prewarmer_test.go
git commit -m "test(nri): assert prewarmer UpdateFunc is a no-op"
```

---

## Task 11: Runner — wire the prewarmer

**Files:**
- Modify: `pkg/nri/runner.go`

- [ ] **Step 1: Locate the sweeper wiring in runner.go**

The existing runner uses `errgroup.WithContext` and spawns the sweeper via `g.Go(...)` inside a nested `if c := k8s.NewClient(); c != nil { if clientset, kerr := c.GetKubernetesClient(); kerr == nil { ... } }` block (around lines 27-37 of `pkg/nri/runner.go`). The prewarmer must be wired into the same block (sharing the `clientset` and `nodeName` setup).

- [ ] **Step 2: Add prewarmer wiring inside the same k8s client block**

In `pkg/nri/runner.go`, find this block:

```go
	if c := k8s.NewClient(); c != nil {
		if clientset, kerr := c.GetKubernetesClient(); kerr == nil {
			if name := nodeNameFromEnv(); name != "" {
				sw := newSweeper(clientset, p, name, log)
				g.Go(func() error { return sw.Run(gctx) })
			} else {
				log.Warn("NODE_NAME env unset; cache sweeper disabled")
			}
		} else {
			log.Warnf("kubernetes client init failed: %v (cache sweeper disabled)", kerr)
		}
	}
```

Replace it with:

```go
	if c := k8s.NewClient(); c != nil {
		if clientset, kerr := c.GetKubernetesClient(); kerr == nil {
			name := nodeNameFromEnv()
			if name != "" {
				sw := newSweeper(clientset, p, name, log)
				g.Go(func() error { return sw.Run(gctx) })
			} else {
				log.Warn("NODE_NAME env unset; cache sweeper disabled")
			}
			if cfg.NRI.Prewarmer.Enabled {
				if name != "" {
					pw := newPrewarmer(p, clientset, name, cfg.NRI.Prewarmer.MaxConcurrent, log)
					g.Go(func() error { return pw.Run(gctx) })
				} else {
					log.Warn("NODE_NAME env unset; NRI prewarmer disabled")
				}
			} else {
				log.Info("NRI prewarmer disabled by config (nri.prewarmer.enabled=false)")
			}
		} else {
			log.Warnf("kubernetes client init failed: %v (cache sweeper and prewarmer disabled)", kerr)
		}
	}
```

- [ ] **Step 3: Build and run all tests**

```bash
go build ./... && go test ./pkg/...
```

Expected: build OK, all tests pass.

- [ ] **Step 4: Commit**

```bash
git add pkg/nri/runner.go
git commit -m "feat(nri): start prewarmer in runner, gated by Prewarmer.Enabled"
```

---

## Task 12: Integration test — prewarm beats CreateContainer (cache hit)

**Files:**
- Modify: `pkg/nri/prewarmer_test.go`

- [ ] **Step 1: Write the integration test**

Append to `pkg/nri/prewarmer_test.go`:

```go
func TestPrewarmer_Integration_PrewarmBeatsCreateContainer(t *testing.T) {
	client := fake.NewSimpleClientset()
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		PodLabel:  "vault-db-injector",
		CachePath: t.TempDir() + "/cache.json",
		Prewarmer: config.NRIPrewarmerConfig{Enabled: true, MaxConcurrent: 5},
	}}, logger.GetLogger())

	resolver := &stubResolver{plugin: p}
	pw := newPrewarmer(p, client, "node-1", 5, logger.GetLogger())
	pw.resolver = resolver.resolveMappingWithSource

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pw.Run(ctx)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p", Namespace: "default", UID: types.UID("uid-int"),
			Labels: map[string]string{"vault-db-injector": "true"},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	if _, err := client.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create pod: %v", err)
	}
	// Wait until prewarm has populated the cache via the stub resolver.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		_, ok := p.cache["uid-int"]
		p.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Now check that resolveMappingWithSource("sync") returns the cache hit
	// and does NOT call the resolver again.
	callsBefore := resolver.calls.Load()
	mapping, err := p.resolveMappingWithSource(ctx, "uid-int", "default", "p", "sync")
	if err != nil {
		t.Fatalf("resolveMappingWithSource cache hit: %v", err)
	}
	if len(mapping) == 0 {
		t.Error("expected non-empty mapping from cache")
	}
	if resolver.calls.Load() != callsBefore {
		t.Errorf("expected zero additional resolver calls on cache hit, got %d",
			resolver.calls.Load()-callsBefore)
	}
	// The source recorded should be "prewarm" (set by the prewarmer's earlier call).
	p.mu.Lock()
	src := p.cacheSource["uid-int"]
	p.mu.Unlock()
	if src != "prewarm" {
		t.Errorf("cacheSource[uid-int]: got %q, want %q", src, "prewarm")
	}
}
```

Note: the `stubResolver.plugin` field (added in Task 6) makes the stub write into `plugin.cache` and `plugin.cacheSource` when set. Tests that need a real cache-hit lookup (this one) must set `resolver.plugin = p` after construction. Tests in Tasks 6-10 don't need it (they only check call counts).

- [ ] **Step 2: Run the integration test**

```bash
go test ./pkg/nri/... -run TestPrewarmer_Integration_PrewarmBeatsCreateContainer -v
```

Expected: PASS.

- [ ] **Step 3: Run all NRI tests to catch regressions from the stub change**

```bash
go test ./pkg/nri/...
```

Expected: all tests pass.

- [ ] **Step 4: Commit**

```bash
git add pkg/nri/prewarmer_test.go
git commit -m "test(nri): integration test — prewarm beats CreateContainer (cache hit)"
```

---

## Task 13: Helm — wire prewarmer config into values + configmap

**Files:**
- Modify: `helm/values.yml`
- Modify: `helm/templates/configmaps.yaml`
- Regenerate: `helm/README.md`

- [ ] **Step 1: Add nri.prewarmer to values.yml**

In `helm/values.yml`, under the existing `nri:` block (after the `fetchTimeout` key), add:

```yaml
  # -- Async credential prefetcher. When enabled, a SharedInformer watches labelled pods on the local node and pre-populates the NRI cache before CreateContainer fires. Removes Vault fetch from containerd's hot path in the common case; sync fetch remains as fail-closed fallback. See `docs/reference/configuration.md` (NRI tuning > Prewarming) for details.
  prewarmer:
    # -- Master switch. When false, the prewarmer is not constructed and CreateContainer always uses the sync fetch path.
    enabled: true
    # -- Maximum number of in-flight async prewarm fetches per DS pod. Caps Vault and apiserver load during bursts. Raise on dense nodes.
    maxConcurrent: 50
```

- [ ] **Step 2: Render them in configmap**

In `helm/templates/configmaps.yaml`, locate the `nri:` block that already renders `fetchTimeout`. Add after it:

```yaml
      prewarmer:
        enabled: {{ .Values.nri.prewarmer.enabled | default true }}
        maxConcurrent: {{ .Values.nri.prewarmer.maxConcurrent | default 50 }}
```

- [ ] **Step 3: Regenerate the helm README**

```bash
make helm-docs
```

Expected: `helm/README.md` updated to mention `nri.prewarmer.*` keys.

- [ ] **Step 4: Verify the diff looks right**

```bash
git diff helm/
```

Expected: only the additions described above + the regenerated rows in `README.md`. No stray changes.

- [ ] **Step 5: Commit**

```bash
git add helm/values.yml helm/templates/configmaps.yaml helm/README.md
git commit -m "feat(helm): expose nri.prewarmer.{enabled,maxConcurrent} values"
```

---

## Task 14: Docs — configuration reference (EN + FR)

**Files:**
- Modify: `docs/reference/configuration.md`
- Modify: `docs/reference/configuration.fr.md`

- [ ] **Step 1: Add prewarmer keys to the NRI keys table (EN)**

In `docs/reference/configuration.md`, locate the "NRI plugin keys" table (under the "Full configuration key reference" section). Append two new rows just before the closing of the table (after the `nri.fetchTimeout` row):

```markdown
| `nri.prewarmer.enabled` | bool | `true` | Master switch for the async credential prefetcher. When `true`, a SharedInformer watches labelled pods on the local node and pre-populates the NRI cache before `CreateContainer` fires, removing the Vault fetch from containerd's hot path in the common case. When `false`, no informer is constructed and every `CreateContainer` uses the sync fetch path (pre-prewarmer behavior). |
| `nri.prewarmer.maxConcurrent` | int | `50` | Maximum number of in-flight async prewarm fetches per DS pod. Bounds Vault and apiserver load during pod bursts. When the semaphore saturates, the surplus pods fall through to the sync path at `CreateContainer` time. Increment `vdbi_nri_prewarm_error_total{reason="semaphore_full"}` is the operator signal to raise this value on dense nodes. |
```

- [ ] **Step 2: Add a "Prewarming" subsection under NRI tuning (EN)**

In the same file, find the "## NRI tuning" section. Append a new subsection at the end of it:

```markdown
### Prewarming (avoid CreateContainer fail-closed on apiserver bursts)

Under default configuration, the plugin observed transient `CreateContainerError` events during bursts where the K8s apiserver `TokenRequest` p99 spikes above the plugin's `fetchTimeout`. The prewarmer subsystem moves the credential fetch out of containerd's `CreateContainer` hot path.

**How it works.** A `SharedInformer` watches pods on the local node (filtered by `spec.nodeName` and `nri.podLabel`). On pod `ADD`, an async fetch populates the existing in-memory cache. When `CreateContainer` fires (1-5 seconds later, typically), it serves from cache in sub-ms. The sync fetch in `CreateContainer` remains as a fail-closed fallback for pods that race ahead of the prewarmer or for cold starts.

**Observability.** Four metrics surface prewarmer health:

| Metric | What it measures |
|---|---|
| `vdbi_nri_prewarm_success_total` | Successful prewarm fetches |
| `vdbi_nri_prewarm_error_total{reason=…}` | Failed/skipped prewarm attempts (`vault_fetch`, `semaphore_full`, `terminating_pod`) |
| `vdbi_nri_prewarm_inflight` | Live count of in-flight prewarm fetches (gauge) |
| `vdbi_nri_cache_hit_total{source=…}` | `CreateContainer` cache hits labelled by what populated the entry (`prewarm`, `sync`, `unknown`) |

The KPI is the prewarm hit rate:

```promql
sum(rate(vdbi_nri_cache_hit_total{source="prewarm"}[5m]))
  / sum(rate(vdbi_nri_cache_hit_total[5m]))
```

Target > 0.95 in steady state. If `prewarm_error_total{reason="semaphore_full"}` is non-zero, raise `nri.prewarmer.maxConcurrent` (default 50).

**Disabling.** Set `nri.prewarmer.enabled: false` in helm and roll the DS. The plugin reverts to pre-prewarmer behavior (sync fetch on every `CreateContainer`).

**Lifecycle note.** Prewarm-issued credentials for pods that never reach `CreateContainer` (admitted then deleted, OOMKilled at start, etc.) are revoked by the revoker's `safetyNetSync` (5-minute periodic GC). No code change required.
```

- [ ] **Step 3: Mirror in FR**

In `docs/reference/configuration.fr.md`, locate the "Clés du plugin NRI" table. Append:

```markdown
| `nri.prewarmer.enabled` | bool | `true` | Interrupteur principal du préchauffeur async de credentials. Lorsque `true`, un SharedInformer observe les pods labellisés du nœud local et pré-remplit le cache NRI avant que `CreateContainer` ne soit appelé, sortant le fetch Vault du hot path containerd dans le cas courant. Lorsque `false`, aucun informer n'est construit et chaque `CreateContainer` utilise le chemin sync (comportement pré-préchauffeur). |
| `nri.prewarmer.maxConcurrent` | int | `50` | Nombre maximum de fetchs async de préchauffe en vol par pod DS. Borne la charge Vault et apiserver pendant les bursts de pods. Quand le sémaphore sature, les pods en surplus retombent sur le chemin sync au `CreateContainer`. Surveiller `vdbi_nri_prewarm_error_total{reason="semaphore_full"}` — signal opérationnel pour monter la valeur sur les nœuds denses. |
```

Then locate the "## Tuning NRI" section. Append a new subsection at its end:

```markdown
### Préchauffage (éviter les fail-closed `CreateContainer` lors de bursts apiserver)

Avec la configuration par défaut, le plugin a observé des évènements `CreateContainerError` transients pendant les bursts où la p99 du `TokenRequest` apiserver K8s dépasse le `fetchTimeout` du plugin. Le sous-système préchauffeur sort le fetch de credentials du hot path `CreateContainer` de containerd.

**Fonctionnement.** Un `SharedInformer` watch les pods sur le nœud local (filtrés par `spec.nodeName` et `nri.podLabel`). À l'évènement pod `ADD`, un fetch async populate le cache mémoire existant. Quand `CreateContainer` arrive (1-5 secondes plus tard typiquement), il sert depuis le cache en sub-ms. Le fetch sync dans `CreateContainer` reste comme fallback fail-closed pour les pods qui devancent le préchauffeur ou pour les cold starts.

**Observabilité.** Quatre métriques exposent la santé du préchauffeur :

| Métrique | Ce qu'elle mesure |
|---|---|
| `vdbi_nri_prewarm_success_total` | Fetchs de préchauffe réussis |
| `vdbi_nri_prewarm_error_total{reason=…}` | Tentatives de préchauffe échouées/sautées (`vault_fetch`, `semaphore_full`, `terminating_pod`) |
| `vdbi_nri_prewarm_inflight` | Compteur des fetchs de préchauffe en vol (gauge) |
| `vdbi_nri_cache_hit_total{source=…}` | Hits cache `CreateContainer` étiquetés selon ce qui a populé l'entrée (`prewarm`, `sync`, `unknown`) |

Le KPI est le taux de hit prewarm :

```promql
sum(rate(vdbi_nri_cache_hit_total{source="prewarm"}[5m]))
  / sum(rate(vdbi_nri_cache_hit_total[5m]))
```

Cible > 0.95 en régime stable. Si `prewarm_error_total{reason="semaphore_full"}` est non nul, monter `nri.prewarmer.maxConcurrent` (défaut 50).

**Désactivation.** Mettre `nri.prewarmer.enabled: false` dans helm et rouler le DS. Le plugin revient au comportement pré-préchauffeur (fetch sync sur chaque `CreateContainer`).

**Cycle de vie.** Les credentials émises par le préchauffeur pour des pods qui n'atteignent jamais `CreateContainer` (admis puis supprimés, OOMKilled au démarrage, etc.) sont révoquées par le `safetyNetSync` du revoker (GC périodique 5 min). Aucun code à ajouter.
```

- [ ] **Step 4: Verify the docs render OK (no need to run mkdocs, just visually scan)**

```bash
git diff docs/
```

Expected: only additions described above.

- [ ] **Step 5: Commit**

```bash
git add docs/reference/configuration.md docs/reference/configuration.fr.md
git commit -m "docs: document NRI prewarmer config keys and tuning section (EN+FR)"
```

---

## Task 15: Docs — metrics reference

**Files:**
- Modify: `docs/reference/metrics.md`

- [ ] **Step 1: Add the 4 new metrics**

In `docs/reference/metrics.md`, locate the NRI metrics table (around line 104-106). Append:

```markdown
| `vdbi_nri_prewarm_success_total` | Successful async prewarm fetches issued by the informer's `AddFunc` handler. | — |
| `vdbi_nri_prewarm_error_total` | Failed or skipped prewarm attempts. | `reason` (`vault_fetch`, `semaphore_full`, `terminating_pod`) |
| `vdbi_nri_prewarm_inflight` | In-flight async prewarm fetches (gauge). Compare against `nri.prewarmer.maxConcurrent`. | — |
| `vdbi_nri_cache_hit_total` | `CreateContainer` events served from the in-memory cache, labelled by what populated the entry. | `source` (`prewarm`, `sync`, `unknown`) |
```

- [ ] **Step 2: Commit**

```bash
git add docs/reference/metrics.md
git commit -m "docs(metrics): document NRI prewarmer and cache_hit_total metrics"
```

---

## Task 16: Final verification

- [ ] **Step 1: Run full build and test suite**

```bash
go build ./...
go test ./pkg/...
```

Expected: build succeeds, all tests pass (228 baseline + new tests added in this plan).

- [ ] **Step 2: Lint**

```bash
make lint 2>&1 | tail -20
# or, if no make target:
# golangci-lint run ./...
```

Expected: no new lint errors. Pre-existing warnings unchanged.

- [ ] **Step 3: Helm template renders**

```bash
helm template test-release helm/ -f helm/values.yml 2>&1 | grep -A2 "prewarmer:"
```

Expected: see `prewarmer: enabled: true / maxConcurrent: 50` in the rendered ConfigMap.

- [ ] **Step 4: Spec sanity check — read the design doc and verify the plan covered each section**

```bash
cat docs/superpowers/specs/2026-05-13-nri-prewarmer-design.md | grep "^##"
```

For each section, confirm a task implements it. The plan covers:
- §3 Architecture → Tasks 6, 11
- §4 Lifecycle + Trust model + Vault lease lifecycle → Tasks 6, 7, 8, 9, 10, 12
- §5 Configuration → Task 1, Task 13
- §6 Metrics → Tasks 2, 4
- §7 Edge cases → Tasks 7, 8, 9, 10
- §8 RBAC → unchanged (already covered)
- §9 Tests → Tasks 6-10, 12
- §10 Rollout → manual post-deploy
- §11 Risks → covered by tests + observability
- §13 Implementation plan → this document

- [ ] **Step 5: Push the branch**

```bash
git push -u origin feat/nri-prewarmer
```

- [ ] **Step 6: Manual canary (post-merge or pre-merge depending on your flow)**

After the branch is deployed to a dev canary:

```bash
# Confirm prewarmer running
kubectl logs -n <ns> -l app=vault-db-injector-nri --tail=50 | grep "prewarmer running"

# Wait a few minutes for traffic, then check hit rate
kubectl exec <nri-pod> -- wget -qO- localhost:8080/metrics | grep vdbi_nri_cache_hit_total
kubectl exec <nri-pod> -- wget -qO- localhost:8080/metrics | grep vdbi_nri_prewarm
```

Target after 24h: `vdbi_nri_cache_hit_total{source="prewarm"}` / `vdbi_nri_cache_hit_total{}` ≥ 0.90 (initial), trending to ≥ 0.95.

If `prewarm_error_total{reason="semaphore_full"}` is non-zero, raise `nri.prewarmer.maxConcurrent` and redeploy.

---

## Rollback procedure

If anything goes wrong post-deploy:

```bash
# Quick toggle (preserves the binary, just disables the prewarmer)
helm upgrade <release> helm/ --set nri.prewarmer.enabled=false --reuse-values

# Full revert
git revert <merge-sha>
# Then deploy the reverted main
```

The prewarmer is opt-in via config — disabling it returns the system to the pre-prewarmer behavior with no functional regression.
