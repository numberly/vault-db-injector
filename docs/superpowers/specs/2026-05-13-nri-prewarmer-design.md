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

### Non-goals

- Webhook-side credential pre-resolution (Option 3 from the brainstorm). Bigger refactor, separate spec if pursued.
- Removing the existing `Synchronize` / `RemovePodSandbox` / sweeper logic. Those remain unchanged.
- Changing the fail-closed contract or `fetchTimeout` value.
- Multi-node coordination (each DS pod handles its own node, independent).
- **Replacing the live `clientset.Get()` inside `fetchAndBuildMapping` with a lister read.** The linearizable read from kube-apiserver is the trust anchor for the UID-match check and annotation parsing (see *Trust model* section). The lister is used only by the prewarmer for enqueueing decisions, never for credential fetch.

## 3. Architecture

A single new file `pkg/nri/prewarmer.go` introduces a `SharedInformer`-driven prewarmer that observes labelled pods on the local node and asynchronously populates the existing `plugin.cache` before `CreateContainer` arrives.

```
┌──────────────────────────────────────────────────────────────┐
│ DaemonSet pod NRI (1 per node)                               │
│                                                              │
│  ┌──────────────────────┐    ┌──────────────────────┐        │
│  │ SharedInformerFactory│───▶│ PodLister            │        │
│  │  filter:             │    │ (private to         │        │
│  │   labelSelector=...  │    │  prewarmer)         │        │
│  │   fieldSelector=     │    └──────────────────────┘        │
│  │    spec.nodeName=… │             │                      │
│  └──────────────────────┘             │ DeletionTimestamp   │
│           │                            │ re-check            │
│           │ AddFunc / DeleteFunc        ▼                     │
│           ▼                   ┌──────────────────────┐       │
│  ┌──────────────────────┐     │ prewarmer            │       │
│  │ informer events      │────▶│  async fetch via     │       │
│  └──────────────────────┘     │  resolveMapping()    │       │
│                               │  bounded by semaphore│       │
│                               └──────────────────────┘       │
│                                        │                     │
│                                        ▼                     │
│  ┌──────────────────────────────────────────────────────┐    │
│  │ plugin.cache (existing) + singleflight (existing)    │    │
│  └──────────────────────────────────────────────────────┘    │
│           ▲                              ▲                   │
│           │ cache lookup                 │                   │
│           │                              │                   │
│  ┌──────────────────────┐    ┌──────────────────────┐        │
│  │ CreateContainer      │    │ fetchAndBuildMapping │        │
│  │   if cache miss:     │───▶│  live apiserver GET  │        │
│  │     sync fetch       │    │  + UID-match check   │        │
│  │  (fail-closed)       │    │  (TRUST ANCHOR)      │        │
│  └──────────────────────┘    └──────────────────────┘        │
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
| `corev1listers.PodLister` | Local in-memory pod reader, used **only** by the prewarmer to drive its own logic (event handlers, DeletionTimestamp re-checks). NOT used by `fetchAndBuildMapping`. | constructed in `pkg/nri/prewarmer.go`, kept private to that file |
| `prewarmer` | Event handler that triggers async `resolveMapping` calls on pod ADD | `pkg/nri/prewarmer.go` |

### Components modified

| Component | Change |
|---|---|
| `pkg/nri/vault.go::fetchAndBuildMapping` | **Unchanged**: still does a live `clientset.CoreV1().Pods(ns).Get(ctx, name, …)`. The linearizable read is the trust anchor — the existing UID-match check (`vault.go:91-100`) and annotation parsing (`vault.go:115`, `vault.go:128`) MUST observe the apiserver's authoritative state, not the lister's possibly-stale view. |
| `pkg/nri/runner.go` | Construct the `SharedInformerFactory`, start it, **do NOT** block on `WaitForCacheSync`. Inject the prewarmer into the plugin lifecycle so it can call `p.resolveMapping`. |
| `pkg/nri/plugin.go` | No structural change to `CreateContainer`. The plugin may need to expose `resolveMapping` for the prewarmer to call (existing visibility allows it within the same package). |
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
| `AddFunc(pod)` | If `pod.DeletionTimestamp == nil`, attempt to acquire a semaphore slot (non-blocking — on saturation, increment `prewarm_error_total{reason="semaphore_full"}` and return). Spawn `go func()` which: (1) **re-checks** `pod.DeletionTimestamp == nil` via the lister (a pod can acquire `DeletionTimestamp` between event dispatch and goroutine start); (2) calls `p.resolveMapping(ctx, pod.UID, pod.Namespace, pod.Name)`; (3) releases the semaphore slot. Singleflight (`plugin.go`) dedupes if `CreateContainer` raced ahead. Note: `resolveMapping` will internally call `fetchAndBuildMapping` which does the live apiserver GET — the lister `pod.UID` passed here is just an enqueue key, not the trust anchor. |
| `UpdateFunc(_, new)` | No-op. The mapping is keyed on `pod.UID` and uses pod identity fields that are immutable after admission (UID, namespace, name, ServiceAccountName). **Annotations are technically mutable** via `kubectl annotate`, but in practice `db-creds-injector.numberly.io/*` annotations are stamped by the webhook and not modified by users. If they are, the cached mapping becomes stale; this is bounded by the next pod recreation. We accept this as a known limitation rather than re-fetching on every annotation patch (would defeat the cache). |
| `DeleteFunc(pod)` | Evict `plugin.cache[pod.UID]` and persist cache to disk. Mirrors `RemovePodSandbox` for cases where the NRI `Remove` event was missed. **Does NOT revoke Vault credentials** — that responsibility belongs to the revoker's existing pipeline (see *Vault lease lifecycle* below). |

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

**Decision: Option 2.** The sync path is functionally correct (it was the only path before this change). Once the informer syncs, both paths benefit. The bootstrap window has no regression versus current behavior — `fetchAndBuildMapping` always reads pod state live from kube-apiserver regardless of informer sync state.

### Concurrency bound

A `golang.org/x/sync/semaphore.Weighted` (`MaxConcurrent`, default 20) caps the number of in-flight async prewarm fetches. Protects Vault from a thundering herd when a node receives a burst of pods (Airflow DAG, scale-out event).

If the semaphore is full, `AddFunc` does NOT block — it falls through. The pod will get its credentials via the sync path at `CreateContainer` time (or via a later informer resync). Bounded backpressure, no deadlock.

### Trust model (security review outcome)

The prewarmer runs ahead of NRI's `CreateContainer` hook, which means it lacks the NRI-attested sandbox UID that the existing code uses as a cross-check (`vault.go:91-100`). Two consequences:

1. **The UID-match check inside `fetchAndBuildMapping` becomes a no-op for prewarm paths**, because both sides of the comparison come from the same source (`contextID = pod.UID` from the informer, and `pod.UID` from the live apiserver GET inside `fetchAndBuildMapping` is the same identity — different reads, but globally-unique UIDs cannot collide). The check still functions normally on sync (`CreateContainer`-triggered) paths where `contextID` comes from containerd.
2. **Prewarm safety derives from apiserver UID uniqueness**, not from the UID-match check. Kubernetes UIDs are 128-bit random and never reused, so caching credentials keyed on UID cannot be confused with a future pod's UID. A force-delete-and-recreate of a pod with the same name produces a new UID; NRI's `CreateContainer` for the new pod arrives with the new UID, missing the cache (stale entry for old UID is later swept).

This is documented here so reviewers and future maintainers don't assume the UID-match check protects prewarm-path fetches.

Additionally: `fetchAndBuildMapping` continues to read pod state via a live `clientset.CoreV1().Pods().Get()`. The lister is NOT used for this read. Rationale: pod annotations are mutable post-admission (rare but possible via `kubectl annotate`); only a linearizable apiserver read guarantees `fetchAndBuildMapping` parses the current annotation set. The lister's eventual-consistency view is acceptable for enqueueing decisions but not for trust-establishing reads.

### Vault lease lifecycle

The prewarmer issues real Vault credentials with leases via `GetDbCredentials`. If a pod is prewarmed but never reaches `CreateContainer` (force-deleted before scheduling, OOMKilled at start, etc.), three cleanup paths exist:

1. **`DeleteFunc` of the informer** evicts the cache entry (local). Does not call Vault.
2. **`RemovePodSandbox` of NRI** evicts the cache entry. Triggered when the pod sandbox ever existed.
3. **Revoker `safetyNetSync`** (`pkg/revoker/revoker.go:42`) runs every 5 minutes. It lists all KV bookkeeping entries (`vaultConn.ListKeyInfo`) and all pods cluster-wide (`GetAllPodAndNamespace`), then revokes + purges any KV entry whose `PodNameUID` no longer corresponds to a live pod.

Path 3 is the load-bearing cleanup for prewarm-only credentials: even if a pod never gets a sandbox (so `RemovePodSandbox` never fires), the KV bookkeeping entry is orphaned, picked up by the safety net within ≤ 5 min, and revoked. **No code change required for this case** — it's pre-existing behavior that happens to handle the new lifecycle correctly.

Implication: prewarm-issued credentials for pods that never start are leaked for **up to 5 minutes** (KV-tracked but DB-active) before revocation. Acceptable for credentials that have multi-hour TTLs and are rate-limited by `MaxConcurrent`.

## 5. Configuration

```go
// pkg/config/config.go
type NRIConfig struct {
    // ... existing fields ...
    Prewarmer NRIPrewarmerConfig `yaml:"prewarmer" envconfig:"prewarmer"`
}

type NRIPrewarmerConfig struct {
    // Enabled, when false, disables the prewarmer entirely (no informer,
    // no async fetch on pod ADD). CreateContainer falls back to the sync
    // path (current behavior pre-prewarmer). Useful for debugging or in
    // clusters where the watch overhead is unwelcome.
    Enabled bool `yaml:"enabled" envconfig:"enabled"`
    // MaxConcurrent caps the number of in-flight async fetches per DS pod.
    // Protects Vault and apiserver from thundering-herd on pod bursts.
    MaxConcurrent int `yaml:"maxConcurrent" envconfig:"max_concurrent"` // default 50
}
```

Defaults in `NewConfig()`:

```go
Prewarmer: NRIPrewarmerConfig{
    Enabled:       true,
    MaxConcurrent: 50,
},
```

Helm values (`helm/values.yml`):

```yaml
nri:
  prewarmer:
    enabled: true
    maxConcurrent: 50
```

Rationale for `MaxConcurrent=50`: under burst load (the primary use case), 20 was identified by review as too low — saturating the semaphore would disable the prewarmer precisely when it is most needed (everyone falls through to sync, which is the very pattern this spec exists to prevent). 50 is high enough for typical Airflow burst sizes per node while still bounding Vault load. Operators on dense nodes should tune up; alert when `prewarm_error_total{reason="semaphore_full"}` is non-zero (see *Metrics*).

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
| Pod created and deleted within 1s (transient) | Prewarm goroutine completes anyway (singleflight cannot be cancelled). `DeleteFunc` then evicts the cache entry. The orphaned Vault lease is picked up by the revoker `safetyNetSync` within ≤ 5 min (see *Vault lease lifecycle*). Bounded waste in Vault TTL terms. |
| Adversarial create-delete churn by a user with pod-edit rights | Prewarms get issued, semaphore bounds concurrency. Orphans accumulate until next `safetyNetSync`. Worst case: `MaxConcurrent × ratio` of churn rate over 5 min = a few hundred orphan DB users until cleanup. Mitigation already exists (safety net). Operator-level fix if abused: K8s RBAC restricts who can create pods with the injector label. |
| Prewarm-only pod (admitted but never starts a container) | Cache eviction via `DeleteFunc`. Vault revocation via revoker `safetyNetSync` within ≤ 5 min. No code change. |
| Pod admitted, `CreateContainer` fires before the informer event reaches the prewarmer's node | Sync path serves the request (live apiserver GET in `fetchAndBuildMapping`). `cache_hit_total{source="sync"}` increments. This is expected during informer bootstrap and possible in steady state for pods scheduled very quickly. Quantify post-rollout: if `source="sync"` rate stays > 10% in steady state, investigate watch propagation latency. |
| `MaxConcurrent` reached at pod burst | `AddFunc` does not block; metric `prewarm_error_total{reason="semaphore_full"}` increments. Pod falls through to sync path at `CreateContainer`. |
| Informer disconnect / re-list | Standard client-go behavior: re-LIST and resync. `AddFunc` fires again for all pods; cache hits ignored, true misses re-fetch. No correctness issue. |
| DS pod rescheduled to a different node | New `NODE_NAME`, new informer scope. Cache rebuilt via resync. Same as cold start. |
| `Synchronize` hook fires after restart with N running containers | Existing behavior unchanged. Informer is started in parallel; the two paths converge on `plugin.cache`. |
| Cross-namespace pod GET in `fetchAndBuildMapping` | Unchanged — `fetchAndBuildMapping` does a live cluster-scoped `clientset.Get()` regardless of namespace. |
| Pod admitted before informer synced (bootstrap) | The prewarmer hasn't observed it; `CreateContainer` arrives and the sync path handles it normally. No special fallback needed because `fetchAndBuildMapping` was never switched to the lister. |

## 8. RBAC

`helm/templates/rbac.yaml` already grants `["get", "list", "watch"]` on `pods`. **No helm change required.**

## 9. Tests

| Layer | Test | Mechanism |
|---|---|---|
| Unit | `prewarmer.AddFunc` triggers async fetch for labelled, non-terminating pods | `kubernetes/fake` client, push a pod, assert `resolveMapping` invocation count via injected stub |
| Unit | `prewarmer.AddFunc` skips terminating pods at event dispatch | `kubernetes/fake` + pod with `DeletionTimestamp != nil` |
| Unit | `prewarmer.AddFunc` re-checks `DeletionTimestamp` inside the goroutine | Race scenario: schedule a `DeletionTimestamp` patch between event and goroutine; assert no fetch is issued |
| Unit | `prewarmer.DeleteFunc` evicts cache, does NOT call Vault | Seed cache, push delete event, assert eviction + zero Vault calls |
| Unit | Semaphore enforces `MaxConcurrent`, fail-open on saturation | Launch N+5 simulated fetches; assert only N concurrent + `prewarm_error_total{reason="semaphore_full"}` increments for the surplus |
| Unit | Race: `RemovePodSandbox` then `DeleteFunc` (and inverse) | Both orderings; assert idempotent eviction, at most one disk persistence write |
| Unit | `UpdateFunc` does NOT trigger a fetch | Push update with changed annotations; assert no Vault call |
| Integration | Prewarm beats `CreateContainer` → cache hit | End-to-end with `kubernetes/fake` + stub Vault; push pod, wait for resync, fire `CreateContainer`, assert `vdbi_nri_cache_hit_total{source="prewarm"}` incremented |
| Integration | `CreateContainer` beats prewarm → singleflight dedup | Fire `CreateContainer` immediately after pod ADD, assert exactly one Vault fetch and `cache_hit_total{source="sync"}` |
| Integration | UID-match check still triggers fail-closed when contextID ≠ apiserver UID | Construct a `CreateContainer` with a fabricated sandbox UID; assert error returned, no creds leaked |
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
- No regression in `vdbi_nri_substitutions_total`: post-rollout daily rate within ±5% of pre-rollout baseline.
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
| Stale cache after `UpdateFunc` no-op | Pod identity fields (UID, namespace, name, SA) are immutable. Annotations are technically mutable but in practice not edited post-admission; if they are, the cached mapping is stale until the next pod recreation. Documented limitation, not a correctness bug. |
| Orphan Vault leases from prewarm-only pods | Revoker `safetyNetSync` (5-min ticker) revokes orphans. Window ≤ 5 min + lease TTL. |
| Adversarial pod churn inflating Vault DB user count | `MaxConcurrent` bounds rate; `safetyNetSync` bounds steady-state count. RBAC on pod creation is the upstream control. |

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
4. Inject the prewarmer into the plugin lifecycle. `fetchAndBuildMapping` is **not** modified — continues to do a live `clientset.Get()`. The lister is constructed inside `prewarmer.go` and stays private to that file.
5. Modify `runner.go` to start the informer alongside the existing sweeper.
6. Add `cache_hit_total{source}` instrumentation in `resolveMapping` (sync path) and prewarmer.
7. Tests (unit + integration).
8. Update `docs/reference/configuration.md` (EN + FR) — add `nri.prewarmer.*` keys and a "Prewarming" subsection under NRI tuning.
9. Regenerate helm-docs (`make helm-docs`).
10. Manual canary on dev. Validate hit rate metric. Iterate `MaxConcurrent` if needed.
