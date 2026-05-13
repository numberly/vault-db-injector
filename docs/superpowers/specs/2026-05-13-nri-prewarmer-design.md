# NRI prewarmer — design spec

**Status:** approved (brainstorm)
**Author:** Guillaume LEGRAIN
**Date:** 2026-05-13
**Branch target:** `feat/nri-prewarmer` off `main`
**Related:** [`debug/nri-substitution-logs`](../../../) (commit `03f49c7` — fail-closed + per-step timing)

---

## 1. Context & problem

The NRI plugin's `CreateContainer` hook fetches Vault credentials synchronously under containerd's `plugin_request_timeout` (default 2s, our `nri.fetchTimeout=1500ms`). The fetch chains several network calls:

1. K8s `GET pod` (~5ms nominal)
2. K8s `TokenRequest` API (~6ms nominal — but spikes to 1-2s during apiserver bursts)
3. Vault `auth/kubernetes/login` for the pod (~50ms)
4. Vault `auth/kubernetes/login` for bookkeeping SA (cached, ~µs after first call)
5. Vault `<dbMount>/creds/<role>` (~20ms)

Observed in production (2026-05-12 14:35:31): when K8s apiserver TokenRequest p99 spikes to 1.47s, our 1500ms budget is exhausted before the Vault login can complete. The plugin returns `CreateContainerError`, kubelet retries 2s later, the retry succeeds (apiserver back to nominal).

**Root cause:** the credential fetch runs in containerd's hot path, on the latency budget of `CreateContainer`, while it depends on infrastructure (apiserver TokenRequest, Vault) whose latency is variable and outside our control.

**Symptom users see:** transient `CreateContainerError` events on labelled pods during apiserver bursts. Self-heals via kubelet retry, but generates alert noise and slows pod startup by 2-10s.

## 2. Goals & non-goals

### Goals

- Move credential fetch out of containerd's `CreateContainer` hot path for the common case.
- Target: > 95% of `CreateContainer` events served from cache, sub-millisecond.
- Keep the existing fail-closed path (sync fetch on cache miss) as a fallback for cold starts and prewarm failures.
- Reduce apiserver pressure: replace per-`CreateContainer` `GET pod` calls with a local `PodLister` lookup.

### Non-goals

- Webhook-side credential pre-resolution (Option 3 from the brainstorm). Bigger refactor, separate spec if pursued.
- Removing the existing `Synchronize` / `RemovePodSandbox` / sweeper logic. Those remain unchanged.
- Changing the fail-closed contract or `fetchTimeout` value.
- Multi-node coordination (each DS pod handles its own node, independent).

## 3. Architecture

A single new file `pkg/nri/prewarmer.go` introduces a `SharedInformer`-driven prewarmer that observes labelled pods on the local node and asynchronously populates the existing `plugin.cache` before `CreateContainer` arrives.

```
┌──────────────────────────────────────────────────────────────┐
│ DaemonSet pod NRI (1 per node)                               │
│                                                              │
│  ┌──────────────────────┐    ┌──────────────────────┐        │
│  │ SharedInformerFactory│───▶│ PodLister            │        │
│  │  filter:             │    │ (local, ~10µs reads) │        │
│  │   labelSelector=...  │    └──────────────────────┘        │
│  │   fieldSelector=     │             │                      │
│  │    spec.nodeName=… │             │                      │
│  └──────────────────────┘             │                      │
│           │                            │                     │
│           │ AddFunc / DeleteFunc        │                     │
│           ▼                            │                     │
│  ┌──────────────────────┐              │                     │
│  │ prewarmer            │              │                     │
│  │  async fetch via     │              │                     │
│  │  resolveMapping()    │              │                     │
│  │  bounded by semaphore│              │                     │
│  └──────────────────────┘              │                     │
│           │                            │                     │
│           ▼                            ▼                     │
│  ┌──────────────────────────────────────────────────────┐    │
│  │ plugin.cache (existing) + singleflight (existing)    │    │
│  └──────────────────────────────────────────────────────┘    │
│           ▲                                                  │
│           │ cache lookup                                     │
│           │                                                  │
│  ┌──────────────────────┐                                    │
│  │ CreateContainer      │ ← containerd hook                  │
│  │   if cache miss:     │                                    │
│  │     sync fetch       │   (fallback, fail-closed)          │
│  └──────────────────────┘                                    │
│                                                              │
│  ┌──────────────────────┐                                    │
│  │ sweeper (5min)       │ ← unchanged, safety net            │
│  └──────────────────────┘                                    │
└──────────────────────────────────────────────────────────────┘
```

### Components added

| Component | Role | Location |
|---|---|---|
| `SharedInformerFactory` | Watch labelled pods on the local node | `pkg/nri/prewarmer.go` |
| `PodLister` | Local in-memory pod reader, shared by prewarmer and `fetchAndBuildMapping` | injected into `plugin` |
| `prewarmer` | Event handler that triggers async `resolveMapping` calls on pod ADD | `pkg/nri/prewarmer.go` |

### Components modified

| Component | Change |
|---|---|
| `pkg/nri/vault.go::fetchAndBuildMapping` | Replace `clientset.CoreV1().Pods(ns).Get(ctx, name, …)` with `podLister.Pods(ns).Get(name)` (local cache lookup). Falls back to live `Get` if lister returns `NotFound` — handles the bootstrap race. |
| `pkg/nri/runner.go` | Construct the `SharedInformerFactory`, start it, **do NOT** block on `WaitForCacheSync`. Pass the `PodLister` and `prewarmer` to the plugin. |
| `pkg/nri/plugin.go` | New field `podLister cache.GenericLister` (or typed `PodLister`). Unchanged `CreateContainer` logic. |
| `pkg/config/config.go` | New `NRI.Prewarmer` sub-struct with `Enabled` and `MaxConcurrent` fields. |

### Components unchanged

- `Synchronize`, `RemovePodSandbox`, `CreateContainer` core logic, `Substitute`, `extractPlaceholdersFromEnv`
- `sweeper` (5-minute periodic safety net)
- `resolveMapping` itself — both prewarmer and sync path call it identically. The existing `singleflight.Group` deduplicates concurrent calls per UID.
- `nri.fetchTimeout` (stays at 1500ms)

## 4. Lifecycle & event handling

### Informer filters (apiserver-side, not in-memory)

```go
labelSelector := p.cfg.NRI.PodLabel + "=true"    // e.g. vault-db-injector=true
fieldSelector := "spec.nodeName=" + nodeName     // local node only
```

Each DS pod receives only the pods on its own node that match the release's label. Watch volume is proportional to the workload on the node, not the cluster size.

### Event handlers

| Event | Action |
|---|---|
| `AddFunc(pod)` | If `pod.DeletionTimestamp == nil`, acquire a semaphore slot, call `go p.resolveMapping(ctx, pod.UID, pod.Namespace, pod.Name)`. Release slot on completion. Singleflight dedupes if `CreateContainer` raced ahead. |
| `UpdateFunc(_, _)` | No-op. The mapping is keyed on `pod.UID` and uses fields immutable after admission (UID, namespace, name, SA, annotations). Status changes do not affect the fetch. |
| `DeleteFunc(pod)` | Evict `plugin.cache[pod.UID]`, persist cache to disk. Mirrors `RemovePodSandbox` for cases where the NRI `Remove` event was missed. |

### Interaction with existing paths

- **`resolveMapping`** is the single fetch entry point. Prewarmer and `CreateContainer` both call it. No code duplication.
- **`singleflight.Group`** in the existing implementation already keys on `podUID`. Prewarmer arriving first → cache populated by the time `CreateContainer` fires. `CreateContainer` arriving first → prewarmer joins as a shared waiter, no second Vault call.
- **`plugin.cache`** remains the source of truth. Cache hit ratio (via the new `cache_hit_total` metric) measures prewarm effectiveness.
- **`RemovePodSandbox`** continues to evict on its own NRI event. `DeleteFunc` adds defense-in-depth against missed `Remove` events.
- **`sweeper`** continues to do periodic garbage collection every 5 min. Same rationale as before, idempotent with respect to the informer.

### Bootstrap behavior (DS pod startup)

The informer's initial `List` takes 100ms-2s to populate the cache. During this window, `CreateContainer` events may arrive. Two options were considered:

1. Block `CreateContainer` behind `WaitForCacheSync`. Safe, but delays plugin readiness by up to 2s.
2. Let `CreateContainer` proceed immediately, sync path handles it (no prewarm benefit yet).

**Decision: Option 2.** The sync path is functionally correct (it was the only path before this change). Once the informer syncs, both paths benefit. The bootstrap window has no regression versus current behavior.

`fetchAndBuildMapping` falls back to a live `clientset.CoreV1().Pods().Get()` if the `PodLister` returns `NotFound` (which happens for pods admitted before the informer synced, or for cross-namespace fetches).

### Concurrency bound

A `golang.org/x/sync/semaphore.Weighted` (`MaxConcurrent`, default 20) caps the number of in-flight async prewarm fetches. Protects Vault from a thundering herd when a node receives a burst of pods (Airflow DAG, scale-out event).

If the semaphore is full, `AddFunc` does NOT block — it falls through. The pod will get its credentials via the sync path at `CreateContainer` time (or via a later informer resync). Bounded backpressure, no deadlock.

## 5. Configuration

```go
// pkg/config/config.go
type NRIConfig struct {
    // ... existing fields ...
    Prewarmer NRIPrewarmerConfig `yaml:"prewarmer" envconfig:"prewarmer"`
}

type NRIPrewarmerConfig struct {
    // Enabled, when false, disables the async fetch on pod ADD events.
    // The PodLister still serves GET pod reads (Option 2 still active).
    // Useful for debugging or in clusters with stressed apiservers where
    // the watch overhead is unwelcome.
    Enabled bool `yaml:"enabled" envconfig:"enabled"`
    // MaxConcurrent caps the number of in-flight async fetches per DS pod.
    // Protects Vault and apiserver from thundering-herd on pod bursts.
    MaxConcurrent int `yaml:"maxConcurrent" envconfig:"max_concurrent"`
}
```

Defaults in `NewConfig()`:

```go
Prewarmer: NRIPrewarmerConfig{
    Enabled:       true,
    MaxConcurrent: 20,
},
```

Helm values (`helm/values.yml`):

```yaml
nri:
  prewarmer:
    enabled: true
    maxConcurrent: 20
```

Both surface in `helm/templates/configmaps.yaml` next to the existing `nri.*` keys.

## 6. Metrics

| Metric | Type | Labels | Purpose |
|---|---|---|---|
| `vdbi_nri_prewarm_success_total` | Counter | — | Successful async prewarm fetches |
| `vdbi_nri_prewarm_error_total` | Counter | `reason` (`vault_fetch`, `semaphore_full`, `terminating_pod`) | Failed or skipped prewarm attempts |
| `vdbi_nri_prewarm_inflight` | Gauge | — | In-flight async fetches (sanity check vs `MaxConcurrent`) |
| `vdbi_nri_cache_hit_total` | Counter | `source` (`prewarm`, `sync`) | Whether `CreateContainer` was served from cache and what populated it |

### KPI dashboards / alerts derived from these

- **Prewarm hit rate** (the success criterion of this project):
  ```promql
  sum(rate(vdbi_nri_cache_hit_total{source="prewarm"}[5m]))
    / sum(rate(vdbi_nri_cache_hit_total[5m]))
  ```
  Target > 0.95 in steady state. Below 0.80 → prewarm is not working or pods race their CreateContainer.
- **Prewarm error rate**:
  ```promql
  sum by (reason) (rate(vdbi_nri_prewarm_error_total[5m]))
  ```
  Especially `reason="semaphore_full"` indicates `MaxConcurrent` is too low for the workload.
- **Inflight saturation**:
  ```promql
  max(vdbi_nri_prewarm_inflight) / <maxConcurrent>
  ```
  Sustained > 0.8 → bump `MaxConcurrent`.

No new alerts added in this spec — these are operational signals, observed in Grafana. If the existing `VaultDbInjectorNRISubstitutionFailureDev` alert continues to fire after this rollout, that's the actionable signal.

## 7. Edge cases

| Case | Handling |
|---|---|
| Pod in `Terminating` state when `AddFunc` fires (informer resync) | Check `pod.DeletionTimestamp == nil` before fetching. Skip with metric `prewarm_error_total{reason="terminating_pod"}`. |
| Pod label race: pod appears with the label, then the label is removed mid-life | Prewarm already started, fetch completes, cache is filled. `RemovePodSandbox` or sweeper will evict on actual pod deletion. Acceptable overhead. |
| Pod created and deleted within 1s (transient) | Prewarm goroutine completes anyway (singleflight cannot be cancelled). `DeleteFunc` then evicts. Bounded waste. |
| `MaxConcurrent` reached at pod burst | `AddFunc` does not block; metric `prewarm_error_total{reason="semaphore_full"}` increments. Pod falls through to sync path at `CreateContainer`. |
| Informer disconnect / re-list | Standard client-go behavior: re-LIST and resync. `AddFunc` fires again for all pods; cache hits ignored, true misses re-fetch. No correctness issue. |
| DS pod rescheduled to a different node | New `NODE_NAME`, new informer scope. Cache rebuilt via resync. Same as cold start. |
| `Synchronize` hook fires after restart with N running containers | Existing behavior unchanged. Informer is started in parallel; the two paths converge on `plugin.cache`. |
| Cross-namespace pod GET in `fetchAndBuildMapping` | `PodLister` is cluster-scoped (informer filters by `spec.nodeName`, not namespace). All node-local pods are visible regardless of namespace. |
| Pod admitted before informer synced (bootstrap) | `PodLister.Get()` returns `NotFound`. Fall back to live `clientset` GET. One-time cost during the bootstrap window. |

## 8. RBAC

`helm/templates/rbac.yaml` already grants `["get", "list", "watch"]` on `pods`. **No helm change required.**

## 9. Tests

| Layer | Test | Mechanism |
|---|---|---|
| Unit | `prewarmer.AddFunc` triggers async fetch for labelled, non-terminating pods | `kubernetes/fake` client, push a pod, assert `resolveMapping` invocation count via injected stub |
| Unit | `prewarmer.AddFunc` skips terminating pods | `kubernetes/fake` + pod with `DeletionTimestamp != nil` |
| Unit | `prewarmer.DeleteFunc` evicts cache | Seed cache, push delete event, assert eviction |
| Unit | Semaphore enforces `MaxConcurrent` | Launch N+5 simulated fetches, assert only N concurrent |
| Unit | `PodLister.Get` fallback to live `Get` on `NotFound` | Mock lister returning `NotFound`, verify clientset is called |
| Integration | Prewarm beats `CreateContainer` → cache hit | End-to-end with `kubernetes/fake` + stub Vault; push pod, wait for resync, fire `CreateContainer`, assert `vdbi_nri_cache_hit_total{source="prewarm"}` incremented |
| Integration | `CreateContainer` beats prewarm → singleflight dedup | Fire `CreateContainer` immediately after pod ADD, assert exactly one Vault fetch |
| Regression | All existing `pkg/nri/...` tests still pass | `go test ./pkg/nri/...` |

## 10. Rollout & success criteria

### Phased deployment

1. **Dev cluster canary**: deploy with `nri.prewarmer.enabled=true`, observe `vdbi_nri_cache_hit_total{source="prewarm"}` over 48h. Target: hit rate > 90% on the canary release.
2. **Dev all releases**: roll to all dev NRI DaemonSets. Monitor `VaultDbInjectorNRISubstitutionFailureDev` alert rate — expect ~10x reduction.
3. **Prod canary**: same as step 1 on prod canary.
4. **Prod all releases**: roll to prod.

### Success criteria

- `vdbi_nri_cache_hit_total{source="prewarm"}` / total ≥ 0.95 in steady state on dev after rollout.
- `VaultDbInjectorNRISubstitutionFailureDev` alert firing rate decreases by ≥ 80% over 7 days vs the 7 days before rollout.
- No regression in `vdbi_nri_substitutions_total` (substitutions still happen at the expected rate).
- Memory footprint of the NRI DS pod increases by < 20MB.

### Rollback

Toggle `nri.prewarmer.enabled=false` via helm and re-deploy the DS (the binary reads its config at startup; no in-process reload). PodLister stays active in the new pods (no harm — local cache, no Vault calls). Prewarm goroutines stop. Reverts to current behavior within one DS rolling-restart cycle.

## 11. Risks

| Risk | Mitigation |
|---|---|
| Watch overhead on apiserver (N nodes × 1 watcher) | Modern apiserver watch cache handles this comfortably. Per-node filter keeps payload small. |
| Memory growth from `PodLister` cache | Filtered by label + node → bounded by labelled-pods-on-node. Typical < 50 pods per node × ~10KB → < 500KB. |
| Prewarm thundering herd on Vault during scale-out | `MaxConcurrent` semaphore bounds the herd. |
| Bug in informer code base impacts `CreateContainer` | The sync path is unchanged. Informer issues degrade prewarm hit rate but cannot break correctness. |
| `DeleteFunc` evicts cache while `CreateContainer` is mid-flight for the same UID | Singleflight returns the resolved mapping to the caller. The cache eviction races but the substitution already completed. No correctness issue. |
| Stale cache after `UpdateFunc` no-op | Mapping inputs are immutable post-admission. No stale-data risk for the resolved mapping. |

## 12. Out of scope (deferred)

- Webhook-side credential pre-resolution (Option 3 from brainstorm).
- Replacing the periodic sweeper entirely with informer events.
- Cross-DS-pod cache replication.
- Pre-warming on `Pending`-state pods before scheduling (would require a cluster-wide informer, not node-local).

## 13. Implementation plan (high-level)

Detail will live in the implementation plan produced by `writing-plans`.

1. Add `NRI.Prewarmer` config block + defaults + helm wiring.
2. Add new metrics in `pkg/metrics/prom.go`.
3. Create `pkg/nri/prewarmer.go` with `SharedInformerFactory` setup, event handlers, semaphore.
4. Inject `PodLister` into `plugin`; modify `fetchAndBuildMapping` to use it with live fallback.
5. Modify `runner.go` to start the informer alongside the existing sweeper.
6. Add `cache_hit_total{source}` instrumentation in `resolveMapping` (sync path) and prewarmer.
7. Tests (unit + integration).
8. Update `docs/reference/configuration.md` (EN + FR) — add `nri.prewarmer.*` keys and a "Prewarming" subsection under NRI tuning.
9. Regenerate helm-docs (`make helm-docs`).
10. Manual canary on dev. Validate hit rate metric. Iterate `MaxConcurrent` if needed.
