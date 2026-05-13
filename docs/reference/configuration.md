# Configuration reference

**Audience:** Platform operator, Contributor

## Binary modes

vault-db-injector ships a single binary that runs in one of three modes,
selected by the `mode` key in the config file passed via `--config`:

| Mode | What it does |
|---|---|
| `injector` | Runs the mutating admission webhook. Mutates PodSpecs at admission time. |
| `renewer` | Periodically iterates KV entries and renews tokens and leases before expiry. |
| `revoker` | Watches for pod `DELETE` events and revokes Vault tokens and leases. |

The NRI plugin is embedded in the `injector` binary and activates when
`nri.enabled=true` in Helm (which sets the appropriate config flag).

Each binary reads a YAML config file. All three share the same key schema;
keys that are irrelevant to a given mode are silently ignored.

## Full configuration key reference

| Key | Type | Default | Used by | Purpose |
|---|---|---|---|---|
| `vaultAddress` | string | — | all | Vault or OpenBao base URL (e.g. `https://vault.example.com:8200`) |
| `vaultAuthPath` | string | `kubernetes` | all | Kubernetes auth method mount path on Vault |
| `kubeRole` | string | — | all | Default Vault auth/kubernetes role for binary login |
| `kubeRoleNri` | string | falls back to `kubeRole` | NRI plugin | Override Vault role for the NRI plugin's Vault login |
| `kubeRoleRenewer` | string | falls back to `kubeRole` | renewer | Override Vault role for the renewer's Vault login |
| `kubeRoleRevoker` | string | falls back to `kubeRole` | revoker | Override Vault role for the revoker's Vault login |
| `tokenTTL` | duration | `8766h` | injector | Periodic token TTL requested at login |
| `vaultSecretName` | string | `vault-db-injector` | all | Name of the KV-v2 mount used for per-pod bookkeeping |
| `vaultSecretPrefix` | string | `kubernetes` | all | Path prefix inside the KV mount |
| `useProjectedSA` | bool | `false` | injector, NRI | When `true`, issues a Kubernetes TokenRequest per admitted pod and uses it to log into Vault on the pod's behalf |
| `tokenRequestAudiences` | []string | `[]` | injector, NRI | Audiences set on the TokenRequest JWT. Must be non-empty when `useProjectedSA: true` |
| `tokenRequestExpirationSeconds` | int | `600` | injector, NRI | Requested lifetime of the TokenRequest JWT in seconds (kube-apiserver floor: 600s) |
| `injectorLabel` | string | `vault-db-injector` | injector, revoker | Pod label value used to select injected pods |
| `webhookMatchLabels` | string | `vault-db-injector` | injector | Value of the `objectSelector` label on the MutatingWebhookConfiguration |
| `mode` | string | — | all | `injector`, `renewer`, or `revoker` |
| `sentry` | bool | `false` | all | Enable Sentry error reporting |
| `sentryDsn` | string | — | all | Sentry DSN |
| `logLevel` | string | `info` | all | Log level (`debug`, `info`, `warn`, `error`) — passed to logrus |
| `SyncTTLSecond` | int | `300` | renewer | Interval in seconds between renewer synchronization sweeps |
| `defaultEngine` | string | `databases` | injector | Default Vault database secrets engine mount name |
| `certFile` | string | `/tls/tls.crt` | injector | TLS certificate for the webhook HTTPS server |
| `keyFile` | string | `/tls/tls.key` | injector | TLS private key for the webhook HTTPS server |

### NRI plugin keys

The NRI DaemonSet reads its config from the same YAML schema, under the `nri:` top-level key.

| Key | Type | Default | Purpose |
|---|---|---|---|
| `nri.enabled` | bool | `false` | Activates the NRI plugin code path. Set by Helm. |
| `nri.socketPath` | string | `/var/run/nri/nri.sock` | UNIX socket the plugin uses to register with containerd. Must match the host's NRI socket. |
| `nri.cachePath` | string | `/run/vault-db-injector/nri/cache.json` | On-disk JSON cache of unwrapped credentials. HostPath tmpfs — survives DS pod restart, cleared on node reboot. |
| `nri.pluginName` | string | `vault-db-injector` (Helm release fullname) | NRI plugin name at registration. Must be unique per containerd instance — running multiple releases (prod + dev) on the same cluster requires distinct values. |
| `nri.pluginIndex` | string | `"10"` | NRI plugin priority (`stub.WithPluginIdx`). Must also be unique per containerd instance when multiple plugins coexist (e.g. `"10"` prod, `"11"` dev). |
| `nri.podLabel` | string | `vault-db-injector` | Pod label key the plugin filters on. Pods missing this label (or value `!= "true"`) are ignored. With multiple releases, set this to the release-specific label the matching webhook's `objectSelector` uses. Empty disables the filter. |
| `nri.fetchTimeout` | duration | `1500ms` | Vault credential fetch timeout per `CreateContainer` event. **MUST be strictly less than containerd's `plugin_request_timeout`** (containerd default: `2s`). See [NRI tuning](#nri-tuning) below. |
| `nri.prewarmer.enabled` | bool | `true` | Master switch for the async credential prefetcher. When `true`, a SharedInformer watches labelled pods on the local node and pre-populates the NRI cache before `CreateContainer` fires, removing the Vault fetch from containerd's hot path in the common case. When `false`, no informer is constructed and every `CreateContainer` uses the sync fetch path (pre-prewarmer behavior). |
| `nri.prewarmer.maxConcurrent` | int | `50` | Maximum number of in-flight async prewarm fetches per DS pod. Bounds Vault and apiserver load during pod bursts. When the semaphore saturates, the surplus pods fall through to the sync path at `CreateContainer` time. Increment `vdbi_nri_prewarm_error_total{reason="semaphore_full"}` is the operator signal to raise this value on dense nodes. |

## Example: injector config

```yaml
certFile: /tls/tls.crt
keyFile: /tls/tls.key
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector
tokenTTL: 8766h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: injector
useProjectedSA: true
tokenRequestAudiences:
  - vault
tokenRequestExpirationSeconds: 600
injectorLabel: vault-db-injector
webhookMatchLabels: vault-db-injector
logLevel: info
sentry: false
```

## Example: renewer config

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector-renewer
tokenTTL: 8766h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: renewer
SyncTTLSecond: 300
logLevel: info
sentry: false
```

## Example: revoker config

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector-revoker
tokenTTL: 8766h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: revoker
injectorLabel: vault-db-injector
logLevel: info
sentry: false
```

!!! warning
    When `useProjectedSA: true`, `tokenRequestAudiences` must be non-empty.
    The binary refuses to start and logs a fatal error if this constraint
    is violated. Set at least `["vault"]` and configure a matching `audience`
    on each Vault `auth/kubernetes` role.

## NRI tuning

The NRI plugin runs as a DaemonSet and intercepts every `CreateContainer`
event on its node. For each labelled pod with placeholders in env, it
synchronously fetches credentials from Vault and returns the substituted
env to containerd. Two timeouts interact here:

| Layer | Setting | Default | Behavior on timeout |
|---|---|---|---|
| containerd | `plugin_request_timeout` (in `/etc/containerd/config.toml`) | `2s` | **Fail-open**: containerd abandons the NRI call and starts the container with the unmodified env (placeholders leak). |
| vault-db-injector | `nri.fetchTimeout` | `1500ms` | **Fail-closed**: the plugin returns an error before containerd's own timeout fires. Containerd propagates the error to kubelet, the pod enters `CreateContainerError`, kubelet retries with backoff. |

The hard invariant: `nri.fetchTimeout < plugin_request_timeout` (with a few
hundred milliseconds of margin so containerd has time to propagate our
error). Otherwise containerd times out first and silently leaks placeholders.

### Default profile (vanilla containerd)

Out of the box, containerd ships with `plugin_request_timeout = 2s`. The
default `nri.fetchTimeout = 1500ms` is sized for this setting and works
without any node-side configuration. Trade-off: any Vault fetch slower
than 1.5s (e.g. during a burst that saturates Vault's `auth/kubernetes/login`)
fails-closed. Kubelet retries with backoff (10s → 20s → 40s → … → 5min cap).

### High-throughput profile (Vault bursts expected)

When your workload schedules many labelled pods simultaneously (Airflow
DAG runs, cronjobs at the top of the hour, scale-out events), Vault's
`auth/kubernetes/login` can spike to several seconds. To absorb the burst
without `CreateContainerError` events, raise both timeouts in lockstep:

**On each node, in `/etc/containerd/config.toml`:**

```toml
[plugins."io.containerd.nri.v1.nri"]
  disable = false
  disable_connections = false
  plugin_registration_timeout = "15s"
  plugin_request_timeout = "30s"   # vs default 2s
  socket_path = "/var/run/nri/nri.sock"
```

Then `systemctl reload containerd` (or `restart` if reload is not
supported on your distribution).

**In Helm values:**

```yaml
nri:
  fetchTimeout: "25s"   # < containerd plugin_request_timeout (30s), with 5s margin
```

Trade-off: when Vault is genuinely unavailable, pods will hang up to 25s
per attempt before kubelet retries. Acceptable in most cases — the
alternative (placeholder-leaking pod that crashes the app) is worse.

### Diagnosing fail-closed events

Each fail-closed path increments `vdbi_nri_unwrap_failures_total{reason=...}`
and produces a Kubernetes Warning event on the pod with a `vault-db-injector:`
prefix. The reasons:

| `reason` label | Cause |
|---|---|
| `fetch_error` | Vault fetch returned an error or timed out (most common — increase `fetchTimeout` if it correlates with Vault bursts). |
| `empty_mapping` | Pod has placeholders in env but no `db-creds-injector.numberly.io/*.env-key-*` annotation matched any container env var name. User config error. |
| `no_change` | Mapping resolved, but `Substitute()` produced an identical env. Indicates env-key annotation refers to a key that does not exist on this specific container. |
| `residual_placeholder` | A `__VDBI_PH_…___` token remained in env after substitution (e.g. only password was resolved, username placeholder leaked). Indicates a partial mapping bug. |

Useful queries:

```promql
# Fail-closed rate, by reason
sum by (reason) (rate(vdbi_nri_unwrap_failures_total[5m]))

# Successful substitutions vs failures
sum(rate(vdbi_nri_substitutions_total[5m]))
  / (sum(rate(vdbi_nri_substitutions_total[5m]))
     + sum(rate(vdbi_nri_unwrap_failures_total[5m])))
```

Per-step latency is also logged at `info` level under the `[timing]` tag,
visible in `kubectl logs -l app=vault-db-injector-nri`. The total of
`fetchAndBuildMapping TOTAL` against `nri.fetchTimeout` tells you how
close you are to fail-closing under load.

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

### Reconnect handling (containerd reloads, ttrpc disconnects)

containerd may close NRI plugin ttrpc connections without restarting itself — typical triggers are logrotate-driven SIGHUP, in-place config reloads, or shim version upgrades. Without active handling, the plugin stays alive but disconnected: the DS pod is `Running` from Kubernetes' POV while the node is effectively NRI-dead.

The plugin runs the ttrpc stub under a bounded reconnect loop:

1. On unexpected disconnect, increment `vdbi_nri_reconnect_total{result="attempted"}`.
2. Backoff `1s → 2s → 5s → 10s → 30s` (≈ 48s total recovery window).
3. Rebuild a fresh `stub.Stub` and re-register with containerd. NRI's `Synchronize` hook fires on connect and re-establishes visibility of running containers.
4. On successful reconnect, increment `vdbi_nri_reconnect_total{result="succeeded"}`.
5. After the backoff schedule is exhausted, increment `vdbi_nri_reconnect_total{result="exhausted"}` and return a fatal error → main exits non-zero → kubelet restarts the DS pod.

In-memory state (`plugin.cache`, `cacheSource`, prewarmer informer, sweeper) survives across reconnects, so the recovery is cheap.

**Operator signals.** Each reconnect is logged at `Warn`. Recommended alert:

```promql
increase(vdbi_nri_reconnect_total{result="exhausted"}[1h]) > 0
```

— fires when the plugin gave up and the pod is being restarted by kubelet. A non-zero `attempted` rate without `exhausted` indicates transient disconnects that the lifecycle absorbed (informational, not actionable).
