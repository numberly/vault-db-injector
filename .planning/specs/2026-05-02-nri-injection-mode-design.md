# NRI-based credential injection mode

**Status:** draft
**Date:** 2026-05-02
**Owner:** @SoulKyu
**Supersedes:** [2026-05-02-ebpf-injection-mode-design.md](./2026-05-02-ebpf-injection-mode-design.md)

## Summary

Replace the eBPF `probe_write_user`-based credential substitution layer with a
**Node Resource Interface (NRI)** plugin. Same goal as the BPF design:
credentials never appear in any persisted Kubernetes resource. Same webhook +
placeholder convention. The substitution mechanism moves from kernel-level
userspace memory mutation to a containerd/CRI-O plugin that mutates the
container spec **before runc receives it**, so the new process is born with
the real envp.

This pivot fixes the structural URI-mode bug of the BPF approach
(NUL-padding truncates env strings when a placeholder is embedded inside a
DSN like `postgres://user:__PH__@host/db`) and removes the entire
`probe_write_user`/cgroup/tracepoint stack.

## Motivation

The BPF design works for env values where the placeholder **is** the entire
value (e.g. `DB_PASSWORD=__VDBI_PH_xxx___`) but breaks when the placeholder
is embedded in a longer string (e.g. `DB_URI=postgres://alice:__VDBI_PH_xxx___@host/db`).
Root cause: `bpf_probe_write_user` overwrites the 77-byte placeholder with
the substituted value NUL-padded to 77 bytes, and `getenv()` stops at the
first NUL — everything after the placeholder is lost.

URI/DSN-mode env vars are a heavily-used pattern in production
(Postgres/MongoDB/Redis/etc.). Variable-length writes via BPF are
implementable in theory but: (a) no public BPF program does this, (b) require
kernel ≥ 5.17 for `bpf_loop`, (c) introduce page-residency failure modes
that cannot be handled gracefully from a tracepoint, (d) trip kernel taint
warnings, (e) require `CAP_SYS_ADMIN` and lockdown-incompatible kernels.

The NRI plugin pattern is the modern, supported extension point for runtime
spec mutation. containerd ≥ 1.7 and CRI-O ≥ 1.26 ship NRI; containerd 2.0
enables it by default. The substitution becomes a pure-Go string operation
in userspace, with arbitrary length, multi-placeholder, URI-mode, and binary-
safe support.

## Goals

1. Credentials never appear in any persisted Kubernetes resource (same as
   BPF design).
2. **URI-mode envs work** (the BPF design's structural failure case).
3. Multi-placeholder envs work (e.g. `__USER__:__PASS__@__HOST__`).
4. Application code requires zero changes; standard `os.Getenv` / `process.env`
   continue to work.
5. The new NRI agent replaces the BPF agent as the fourth runtime mode of
   the existing binary (`injector` / `renewer` / `revoker` / `nri`).
6. Activation is a single cluster-wide switch via a Helm value. When off,
   behavior is byte-identical to today's `classic` / `uri` modes (cleartext
   in PodSpec). When on, every credential issued by the webhook is wrapped.
7. Single PR delivery on the same branch (`feat/ebpf-injection-mode`).
8. **All `pkg/bpf/` code, BPF C sources, BPF object embedding, and BPF-related
   helm/CI plumbing are removed.** No coexistence with the BPF mode.

## Non-goals

- Defending against in-pod attackers (sidecar in same PID ns reading
  `/proc/<pid>/environ`). Same threat model as BPF design.
- Defending against `kubectl exec` users with shell access.
- Renewal-aware substitution; rotation continues to follow the existing
  rolling-restart model.
- Supporting runtimes that do not implement NRI (Docker dockershim — dead
  in K8s ≥ 1.24; gVisor; older containerd ≤ 1.6). Document the requirement
  and fail closed at startup if the NRI socket is missing.
- Backwards compatibility with the BPF `bpf-mapping` annotation. The
  annotation is renamed to `nri-mapping` in a single cut; no dual-key reader.

## Threat model

Identical to the BPF design — kernel control plane vs in-pod attacker. Same
table of leak paths. The wrap-token TTL (5 min) still bounds etcd-backup
exposure. Renaming the mechanism does not change the security posture beyond
the URI-mode fix.

## Architecture

```
kube-apiserver
     ↓ pod spec contains placeholders + annotation `nri-mapping={wrap_token, placeholders}`
kubelet
     ↓
containerd (≥ 1.7, NRI enabled)
     ↓ /var/run/nri/nri.sock
[vault-db-injector-nri DaemonSet]
     ↓ on CreateContainer:
     ↓   - read pod sandbox annotations
     ↓   - unwrap wrap-token via Vault (one-shot, 5min TTL)
     ↓   - for each container env: replace placeholder substrings
     ↓   - return ContainerAdjustment{env: substituted}
runc
     ↓ execve with real envp
app
```

### Components

**Webhook (mostly unchanged)**

- Continues to mint placeholders (`__VDBI_PH_<64hex>___`, 77 bytes — kept for
  ergonomic continuity, no length constraint required anymore).
- Wraps the credential value list into a Vault response-wrap token.
- Annotates the pod with `db-creds-injector.numberly.io/nri-mapping`
  containing `{wrap_token, placeholders: {ph → vault_key_name}}`.
- **Removed**: the per-env-var length validation that rejected
  `len(value) != PlaceholderLen`. URI mode is now allowed (`DB_URI=postgres://user:__VDBI_PH_xxx___@host/db`
  passes through).
- The mode flag `bpf.enabled` becomes `nri.enabled`. When off, the webhook
  emits cleartext as before. When on, every secret env is wrapped.

**`pkg/nri/` package (new)**

- `pkg/nri/plugin.go` — implements `nri.Plugin` interface (Synchronize,
  RunPodSandbox, CreateContainer, StopContainer, RemovePodSandbox).
- `pkg/nri/substitute.go` — pure-Go substitution logic (string replacement
  over `[]string` envp). No goroutines, no maps, no I/O.
- `pkg/nri/vault.go` — wrap-token unwrap helper (uses existing
  `pkg/vault` package).
- `pkg/nri/runner.go` — `Run(ctx)` entrypoint, registers plugin with
  containerd over `/var/run/nri/nri.sock`, blocks until ctx cancelled.

**Binary mode (`cmd/.../main.go`)**

- Adds `nri` to the mode switch (alongside `injector` / `renewer` / `revoker`).
- Removes the `bpf` mode and the `runBPFAgent` call.

**Helm chart**

- Renames `daemonset-bpf.yaml` → `daemonset-nri.yaml`.
- Drops capabilities `BPF` / `PERFMON` / `SYS_RESOURCE` / `SYS_ADMIN`.
- Drops mounts `/sys/fs/bpf`, `/sys/kernel/tracing`, `/sys/kernel/security`,
  `/sys/fs/cgroup`, `/run/vault-db-injector/bpf`.
- Adds mount `/var/run/nri/nri.sock` (RW hostPath).
- Runs as non-root (any UID ≥ 1000 works — only socket access needed).
- `readOnlyRootFilesystem: true` retained.
- `values.yaml`: `bpf.*` keys → `nri.*` (image, resources, tolerations,
  nodeSelector). `bpf.enabled` → `nri.enabled`.

**Removed code (single PR, same branch)**

- `pkg/bpf/` (entire directory: runner, loader, cgroup, embed.go, c/,
  bpfobj_*.o, runner_test.go, loader_test.go, cgroup_test.go).
- `Makefile` BPF compile targets.
- `.github/workflows/*` BPF object compile and verify jobs.
- `helm/templates/daemonset-bpf.yaml`, `helm/templates/configmap-bpf.yaml`.
- `pkg/placeholder/` is **kept** — still useful to the webhook for
  generating placeholders, even though length-strictness is dropped.

## Data flow

### Steady-state injection

1. Pod created with annotation `db-creds-injector.numberly.io/inject: 'true'`.
2. Webhook resolves Vault credential, generates placeholders, wraps the
   value list into a single-use 5-min token via `sys/wrapping/wrap`.
3. Webhook mutates the PodSpec: env vars get placeholder values; pod
   gets annotation `db-creds-injector.numberly.io/nri-mapping={...}`.
4. API server stores the (placeholder-only) spec in etcd.
5. Kubelet pulls spec, hands to containerd.
6. Containerd fires `CreateContainer` NRI event → DaemonSet plugin receives.
7. Plugin reads pod-sandbox annotation, unwraps token via
   `sys/wrapping/unwrap`, builds a `placeholder → real value` map.
8. Plugin scans container envs; for each env value, runs
   `strings.ReplaceAll(value, placeholder, real)` for every mapping.
9. Plugin returns `ContainerAdjustment{env: [substituted entries]}`.
10. Containerd applies adjustment, hands final spec to runc.
11. runc `execve`s the app with real envp. App reads `os.Getenv(...)`.

### Failure paths

| Failure | Behavior |
|---------|----------|
| Wrap-token expired (> 5 min between webhook and `CreateContainer`) | Plugin logs error, returns no adjustment → container starts with placeholder env → app fails to connect → pod CrashLoops with visible error. |
| Vault unreachable from node | Same as above. Plugin emits Prometheus metric `vdbi_nri_unwrap_failures_total{reason="vault_error"}`. |
| Annotation malformed / missing | Plugin returns no adjustment. Logged as warning, not error. |
| NRI socket missing on host | DaemonSet pod fails liveness probe and CrashLoops with clear error message. |
| Plugin panic | NRI client library auto-reconnects after panic recovery; in worst case containerd continues without the plugin (containers start with placeholder env, fail visibly). |

### Multi-container pod

Plugin sees one `CreateContainer` event per container. Each event includes
the pod sandbox annotations. The wrap-token annotation is unwrapped **once
per pod sandbox** (cached in plugin memory keyed by pod UID, evicted on
`RemovePodSandbox`). The same placeholder→value map is applied to every
container in the pod.

### Restart resilience

- **Plugin restart between webhook annotation and `CreateContainer`**: NRI
  re-fires `Synchronize` on plugin reconnect, listing all running pods.
  Plugin doesn't need to act on already-running containers (envp is
  immutable post-execve). For pending sandboxes/containers that have not
  yet entered `CreateContainer`, NRI replays the events. Token TTL of 5 min
  bounds the recovery window.
- **Plugin restart after `CreateContainer`**: nothing to do. Substitution
  is one-shot at container creation; app is already running with real env.
- **Vault reachability flap**: plugin returns no adjustment, container
  fails to start. Kubelet retries the container per `restartPolicy`. Each
  retry triggers a fresh `CreateContainer` event → plugin gets a new
  attempt to unwrap. As long as the wrap token has not expired, eventual
  success. After 5 min expiry → permanent failure → ops triage required.

## Configuration

### Helm values (delta vs current `bpf.*`)

```yaml
nri:
  enabled: false                     # was: bpf.enabled
  image:
    repository: ""                   # falls back to vaultDbInjector.injector.image.repository
    tag: ""                          # falls back to .Chart.AppVersion
  imagePullPolicy: IfNotPresent
  socketPath: /var/run/nri/nri.sock  # configurable for non-default containerd setups
  resources: {}
  tolerations: []
  nodeSelector: {}

vaultDbInjector:
  webhook:
    nri:
      enabled: false                 # was: bpf.enabled (mirrors top-level)
```

### Runtime requirements

- containerd ≥ 1.7 with NRI enabled in `/etc/containerd/config.toml`:
  ```
  [plugins."io.containerd.nri.v1.nri"]
    disable = false
  ```
  containerd 2.0+ has it on by default.
- OR CRI-O ≥ 1.26 with NRI enabled.
- Kernel: any version that runs the chosen runtime (no eBPF-specific
  requirement; no `CAP_SYS_ADMIN`, no lockdown concerns).
- The DS is documented as **incompatible** with: dockershim, gVisor, Kata
  Containers configurations that do not enable NRI on their containerd shim,
  and any containerd ≤ 1.6.

## Security posture vs BPF design

| Concern | BPF | NRI |
|---|---|---|
| etcd cleartext leak | safe | safe |
| audit log cleartext leak | safe | safe |
| Kernel taint warning | yes (one-time per probe_write_user load) | no |
| Required capabilities | BPF, PERFMON, SYS_RESOURCE, SYS_ADMIN | none (socket access via hostPath) |
| Lockdown kernel compat | broken | works |
| Run-as-root requirement | yes | no |
| Required mounts | /sys/fs/bpf, /sys/kernel/tracing, /sys/kernel/security, /sys/fs/cgroup, /run/vault-db-injector/bpf | /var/run/nri/nri.sock |
| Trust boundary | DS holds wrap-token unwrap → secret in DS memory briefly | identical |
| Failure mode | substitution silently truncates URI mode | substitution always faithful or visibly fails |

The NRI plugin runs with **strictly fewer privileges** than the BPF DS.

## Testing

### Unit tests

- `pkg/nri/substitute_test.go`:
  - Single placeholder, full-value (legacy BPF test parity).
  - Single placeholder, embedded in URI (the BPF failure case).
  - Multi-placeholder same env (`postgres://__USER__:__PASS__@host`).
  - Multi-placeholder across multiple envs.
  - Placeholder absent in env → no-op.
  - Empty env list → no-op.
  - Binary-safe value (high bytes, NULs in value rejected by webhook → not
    expected here, but assert plugin doesn't corrupt on weird input).

- `pkg/nri/plugin_test.go`:
  - CreateContainer with valid annotation → returns expected adjustment.
  - CreateContainer with no annotation → no adjustment.
  - CreateContainer with malformed annotation → no adjustment + warning log.
  - Pod sandbox cache eviction on RemovePodSandbox.

- `pkg/k8smutator` tests updated:
  - Annotation key changes from `bpf-mapping` to `nri-mapping`.
  - Length validation removed.
  - URI-mode envs now produce wrapped output when `nri.enabled=true`.

### Integration tests on k3d (existing `vault-db-test` cluster)

The cluster needs containerd NRI enabled. k3d's bundled containerd is
≥ 2.0 in recent k3d versions; if NRI is not on by default, enable via k3d
config patch.

Test matrix (must all pass before user is notified):

1. **Single-container, single placeholder, full value** — `DB_PASSWORD`
   gets substituted. Equivalent of legacy BPF `classic` mode.
2. **URI mode** — `DB_URI=postgres://alice:__PH__@db.example.com:5432/mydb?sslmode=require`
   resolves correctly with the tail intact. **This is the BPF failure case.**
3. **Multi-container pod** — both containers receive substitutions.
4. **Init container** — init runs with substituted env, completes, main
   container also gets substituted.
5. **CrashLoopBackoff retry** — kill an app that crashes; on each kubelet
   restart, env is re-substituted.
6. **Multi-placeholder in single env** — `postgres://__USER__:__PASS__@__HOST__`.
7. **DaemonSet restart between annotation and pod start** — plugin
   reconnects, NRI replays sandbox state, late-arriving pods still work.
8. **Wrap-token expired** — pod created from a webhook event 6 min before
   `CreateContainer` reaches the plugin → container starts with
   placeholders → CrashLoop visible (fail-safe).
9. **NRI not enabled on containerd** — DS fails liveness with clear error.
10. **Vault unreachable from node** — `vdbi_nri_unwrap_failures_total`
    increments; pod CrashLoops; recovers when Vault is back (within token TTL).

### Manual smoke

- `helm install` with `nri.enabled=true` on the k3d cluster.
- Deploy a sample pod with both `classic` and `uri` annotations.
- `kubectl exec ... env` shows real values; `kubectl get pod -o yaml`
  shows placeholders.

## Migration / rollout

This is a single PR on the existing branch. There is no in-cluster migration
because the BPF mode never shipped. The branch was the staging ground for
the feature; it pivots before merge.

For users following the branch:

- BPF DS will be removed; redeploying with this PR removes the BPF DS pods
  automatically (helm prunes resources by name match).
- Annotation rename is invisible — webhook and DS are upgraded together.

## Risks and unknowns

1. **NRI socket mount race on node bring-up.** If the DS pod starts before
   containerd has created `/var/run/nri/nri.sock`, the plugin fails to
   register. Mitigation: liveness probe restarts until socket appears;
   add an init container that waits for the socket (5s poll, 60s timeout).
2. **k3d containerd NRI default.** Need to verify NRI is on by default on
   the k3d image we use; if not, document the patch in test setup.
3. **Sandbox annotation propagation.** NRI passes pod sandbox annotations to
   `CreateContainer` events via `Pod.Annotations`. Confirmed by
   [containerd NRI api.proto](https://github.com/containerd/nri); validate
   on first integration test run.
4. **Large env values.** NRI message size limits should accommodate any
   reasonable env (no documented hard cap below 4 MiB).
5. **Plugin auth/identity.** NRI socket access is per-host; any process
   that can mount the socket can register a plugin. Same trust posture as
   the BPF DS (both run privileged on the host). No regression.

## References

- [containerd/nri](https://github.com/containerd/nri)
- [containerd NRI docs](https://github.com/containerd/containerd/blob/main/docs/NRI.md)
- [containers/nri-plugins (community plugins)](https://github.com/containers/nri-plugins)
- [Bank-Vaults secret webhook](https://bank-vaults.dev/docs/mutating-webhook/) (architectural sibling, env-var pattern)
- Previous spec: [2026-05-02-ebpf-injection-mode-design.md](./2026-05-02-ebpf-injection-mode-design.md)
