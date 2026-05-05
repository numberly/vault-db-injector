# eBPF-based credential injection mode

**Status:** draft
**Date:** 2026-05-02
**Owner:** @SoulKyu

## Summary

Add an eBPF-based credential protection layer that makes database credentials
invisible at the Kubernetes API layer. The webhook injects fixed-length
placeholders into the PodSpec instead of real values; a node-local DaemonSet
substitutes the placeholders at `execve` time via a BPF LSM program, so the
running process sees the real credentials but `kubectl get pod -o yaml`,
etcd dumps, and audit logs only ever see the placeholders.

The protection is **cluster-wide and automatic** when both:
- the webhook is configured with `bpf.enabled: true`
- the BPF DaemonSet is deployed on the nodes

The existing `classic` / `uri` annotation modes continue to control the env
**shape** (`DB_USER`+`DB_PASSWORD` vs `DB_URI`); the BPF layer wraps either
shape transparently. There is no per-pod or per-namespace opt-in.

## Motivation

Current modes (`classic`, `uri`) materialize credentials as literal `env:`
entries on the PodSpec. Anyone with `get pod` permission, access to etcd
backups, audit logs, or GitOps captures of live state can read them. Security
audits (SOC2, ISO 27001, PCI-DSS) flag this recurringly.

The `bpf` mode closes those leak paths without changing the application code:
`os.Getenv("DB_PASSWORD")` continues to return the real password.

## Goals

1. Credentials never appear in any persisted Kubernetes resource (PodSpec,
   Secrets, Events, audit log entries, etcd backups).
2. Application code requires zero changes; standard `os.Getenv` and
   `process.env` work.
3. The new BPF agent is one of the four runtime modes of the existing
   binary (`injector` / `renewer` / `revoker` / `bpf`), not a separate
   project.
4. Activation is a **single cluster-wide switch** via a Helm value. When
   off, behavior is byte-identical to today. When on, the webhook
   automatically wraps every credential it issues — no per-pod annotation,
   no namespace allowlist.
5. Single PR delivery: nothing half-shipped.

## Non-goals

- **Defending against in-pod attackers.** A sidecar in the same PID namespace
  reading `/proc/<pid>/environ` still sees the substituted value. The
  threat model targets the K8s control plane, not host or in-pod compromise.
- **Defending against `kubectl exec` users with shell access.** A future
  hardening hook (lineage detection) is mentioned in the threat-model section
  but is not part of this design.
- **Renewal-aware substitution.** Credentials live in the process's memory
  after `execve`; rotation continues to follow the existing
  rolling-restart / SIGHUP model.
- **Supporting kernels older than 5.7.** Document the requirement and fail
  closed at DaemonSet startup if `CONFIG_BPF_LSM` is missing.

## Threat model

| Attack vector                        | classic / uri | bpf       |
|--------------------------------------|---------------|-----------|
| `kubectl get pod -o yaml`            | leak          | safe      |
| etcd dump / Velero backup            | leak          | safe†     |
| Kubernetes audit logs                | leak          | safe      |
| GitOps tooling capturing live state  | leak          | safe      |
| `kubectl exec` with RBAC             | leak          | leak      |
| Sidecar in shared PID namespace      | leak          | leak      |
| Node compromise (root on host)       | leak          | leak      |

† The PodSpec contains a Vault wrapping token (5 min TTL, single-use). After
unwrap, the token is dead. A backup older than the TTL contains nothing
useful.

## Architecture

### Modes alignment

The `vault-db-injector` binary already runs in four modes coordinated by
`pkg/controller`:

```
config.Mode = injector | renewer | revoker | all
```

This design adds:

```
config.Mode = injector | renewer | revoker | bpf | all
```

`RunBPF(ctx context.Context) error` is added to `*Controller`, on the same
shape as `RunInjector` / `RunRenewer` / `RunRevoker`. `ModeAll` includes
`RunBPF` in its `errgroup` (cluster operators can also run `bpf` as its own
DaemonSet, which is the default Helm chart layout).

### High-level flow

```
                        ┌──────────────────────┐
                        │       Vault          │
                        │ Database Engine + KV │
                        │   sys/wrapping       │
                        └──────────────────────┘
                            ▲    ▲          ▲
                            │1   │5         │2 wrap
                            │    │ unwrap   │
                            │    │          │
┌───────────────────────────┴────┴──────────┴────────────────────────────┐
│                                                                        │
│  Webhook (mode=injector, Deployment)         DaemonSet (mode=bpf)      │
│  ┌─────────────────────────────────┐         ┌──────────────────────┐  │
│  │ pkg/k8smutator                  │         │ pkg/bpf              │  │
│  │   applyEnvToContainers          │         │   loader (cilium/    │  │
│  │     when cfg.BPF.Enabled:       │         │     ebpf)            │  │
│  │       1. fetch creds (existing) │         │   pod informer       │  │
│  │       2. wrap → wrapToken       │         │   tmpfs persister    │  │
│  │       3. placeholder per field  │         │   LSM program (.bpf  │  │
│  │       4. annotate pod with      │         │     .c, embedded)    │  │
│  │          {wrapToken, mapping}   │         │                      │  │
│  │       5. inject placeholders    │         │   3. watch local     │  │
│  │          in env                 │         │      pods            │  │
│  └─────────────────────────────────┘         │   4. read annotation │  │
│                  │                           │   5. unwrap (Vault)  │  │
│                  │ admission                 │   6. cgroup resolve  │  │
│                  ▼                           │   7. BPF map update  │  │
│           ┌────────────┐                     │   8. tmpfs write     │  │
│           │  apiserver │                     └──────────────────────┘  │
│           │  → etcd    │                              │                │
│           └────────────┘                              │                │
│                  │                                    │                │
│                  ▼                                    ▼                │
│           ┌──────────────────────────────────────────────────────┐     │
│           │                       Node N                         │     │
│           │   Pod scheduled here                                 │     │
│           │   ┌──────────────┐                                   │     │
│           │   │  container   │                                   │     │
│           │   │              │                                   │     │
│           │   │  execve()    │── kernel ─┐                       │     │
│           │   │              │           │ LSM bprm_check_       │     │
│           │   │              │           │ security              │     │
│           │   │              │           ▼                       │     │
│           │   │              │   ┌────────────────┐              │     │
│           │   │              │   │ BPF program    │              │     │
│           │   │              │   │  - lookup map  │              │     │
│           │   │              │   │    by cgroup   │              │     │
│           │   │              │   │  - scan envp   │              │     │
│           │   │              │   │  - probe_write │              │     │
│           │   │              │   │    _user       │              │     │
│           │   │              │   └────────────────┘              │     │
│           │   │              │           │                       │     │
│           │   │  envp now    │◄──────────┘                       │     │
│           │   │  has real    │                                   │     │
│           │   │  values      │                                   │     │
│           │   └──────────────┘                                   │     │
│           └──────────────────────────────────────────────────────┘     │
└────────────────────────────────────────────────────────────────────────┘
```

### Data flow

**Admission (when `cfg.BPF.Enabled == true` on the webhook):**

The flow runs for every pod that already triggers the webhook today —
the existing annotation-driven selection (`<db>.mode = classic | uri`) is
preserved and continues to control env shape. The BPF layer wraps the
result.

1. Webhook authenticates to Vault using the pod's ServiceAccount token
   (existing flow, unchanged).
2. Webhook calls `database/creds/<role>` → `{username, password}` (existing).
3. **New**: Webhook calls `sys/wrapping/wrap?wrap-ttl=5m` with the credential
   payload → receives `wrapToken` (opaque, single-use, 5 min TTL).
4. **New**: Webhook generates one placeholder per credential field. Each
   placeholder is a fixed-length string `__VDBI_PH_<32-byte-hex>___`
   (74 bytes). Length is fixed to allow in-place substitution by the BPF
   program without resizing the envp area.
5. Webhook builds a JSON payload mapping placeholder → field name in the
   wrapped data:
   ```json
   {
     "wrap_token": "hvs.CAESIJxK2nT...",
     "placeholders": {
       "__VDBI_PH_a1b2c3d4...___": "username",
       "__VDBI_PH_e5f6a7b8...___": "password"
     }
   }
   ```
6. Webhook attaches that payload as a single annotation:
   `db-creds-injector.numberly.io/bpf-mapping`.
7. Webhook injects placeholders into containers' `env:` (same locations as
   `classic` mode, just with placeholder strings instead of real values).
8. Existing Vault KV entry for `secretPrefix/<podUUID>` is still written —
   the renewer/revoker need it. KV format unchanged.

**On node N (DaemonSet running in `bpf` mode):**

1. K8s informer filtered by `spec.nodeName == N` triggers on pod creation.
2. DS reads `bpf-mapping` annotation, parses payload.
3. DS calls `Logical().Unwrap(wrapToken)` against Vault → real credentials
   (single-use, this consumes the token).
4. DS resolves the pod's cgroup ID via the kubelet's pod-cgroup naming
   convention (`/sys/fs/cgroup/.../kubepods.slice/...<podUID>...`).
5. DS writes `(cgroup_id, []{placeholder, real_value})` into a BPF
   `BPF_MAP_TYPE_HASH`.
6. DS persists the same mapping to tmpfs at
   `/run/vault-db-injector/bpf/<podUID>.json` for self-restart recovery.

**On container `execve`:**

1. Kernel resolves the binary, copies argv/envp to the new task's stack.
2. Kernel calls LSM hook `bprm_check_security`.
3. BPF program reads `bpf_get_current_cgroup_id()`, looks up the mapping
   table.
4. If found: scans envp for placeholder pattern, calls
   `bpf_probe_write_user(envp_addr, real_value, placeholder_len)` for each
   match. Real values are right-padded with NUL to placeholder length.
5. If not found (DS hasn't unwrapped yet, race): returns `EAGAIN`. Kernel
   retries the LSM hook on next exec attempt. After ~30 s of retries, the
   container falls into `CrashLoopBackoff`; the DS catches up; next restart
   succeeds. **Fail-closed.**

   Real-value length constraint: real values must fit within the placeholder
   (74 bytes minus the trailing NUL). The Vault Database Engine generates
   passwords bounded by `password_policy` configuration; operators must
   configure roles to keep passwords ≤ 73 bytes. Webhook validates this at
   admission time and refuses to admit a pod if the generated value exceeds
   the limit (with a clear error pointing at the Vault role configuration).
6. Hook completes; kernel resumes execve.

**On DS restart:**

1. DS reloads tmpfs mappings into in-memory state.
2. DS rebuilds BPF maps from in-memory state.
3. DS resumes informer; new pods follow the normal flow.

**On node reboot:**

tmpfs is wiped. Pods on the node are also gone. As they're rescheduled, the
admission flow re-issues new wrap tokens and the cycle restarts. No
recovery needed.

## Components

### `pkg/controller/controller.go`

- New method `RunBPF(ctx context.Context) error`. Symmetric with the three
  existing `Run*` methods.
- ModeAll's errgroup spawns `RunBPF` alongside the others. **Note:** in
  practice the Helm chart deploys four separate workloads (one Deployment
  per `injector`/`renewer`/`revoker`, one DaemonSet for `bpf`). ModeAll
  remains useful for local development, integration tests, and operators
  who prefer a single binary deployment, but the recommended production
  layout is one process per mode.

### `pkg/config/config.go`

- New `Mode` constant: `ModeBPF Mode = "bpf"`.
- New config block:
  ```go
  type BPFConfig struct {
      Enabled            bool          // single cluster-wide switch
      WrapTokenTTL      time.Duration // default 5m
      TmpfsPath         string        // default /run/vault-db-injector/bpf
      MaxMappingsPerNode int          // BPF map size hint, default 4096
  }
  ```
- Existing `Mode.Validate()` adds `bpf` to the allowed values for the
  binary runtime mode.

### `pkg/k8s/parse_annotations.go`

- New annotation key constant
  `ANNOTATION_BPF_MAPPING = "db-creds-injector.numberly.io/bpf-mapping"`.
- **No new `DbMode` constant.** The `<db>.mode` annotation continues to
  accept `classic` / `uri` only, controlling env shape. BPF wrapping is
  applied on top regardless of shape.

### `pkg/placeholder/placeholder.go` (new package)

- `Generate() string` returning `__VDBI_PH_<32-hex-chars>___` (74 bytes).
- `IsPlaceholder(s string) bool` for tests and BPF-side parity check (same
  matcher logic compiled to both Go and BPF C; the BPF version is hand-coded
  but tested for parity against the Go version).
- Exported byte length constant.

### `pkg/k8smutator/k8smutator.go`

- The existing `case "", k8s.DbModeClassic:` and `case k8s.DbModeURI:`
  branches are wrapped: when `cfg.BPF.Enabled` is true, the values pushed
  into `corev1.EnvVar.Value` are placeholders instead of real credentials,
  AND a `prepareBPFAnnotation(creds, placeholders)` helper attaches the
  `bpf-mapping` annotation to the pod with the wrap token + placeholder
  map.
- A small helper `wrapAndPlaceholder(ctx, creds, fields []string)` does
  the work shared by both shapes:
  1. Generates one placeholder per field requested.
  2. Calls `vault.Connector.WrapValues(ctx, payload, ttl)` and returns
     the wrap token.
  3. Returns `(placeholders map[string]string, wrapToken string)`.
- Classic shape calls it with fields `["username", "password"]`; URI
  shape calls it with `["username", "password"]` then builds the DSN
  string by substituting the placeholder values into the URL template
  (the BPF substitution will replace those placeholder bytes inside the
  full DSN string at execve time, since placeholders are literal byte
  sequences and DSN encoding never escapes the placeholder pattern).
- All code paths are gated by `cfg.BPF.Enabled`; when false, behavior is
  byte-identical to the current `applyEnvToContainers`.

### `pkg/vault/auth.go` / `pkg/vault/vault.go`

- New method `(c *Connector) WrapValues(ctx, payload map[string]string,
  ttl time.Duration) (string, error)` returning the wrap token.
- New method `(c *Connector) UnwrapValues(ctx, token string)
  (map[string]string, error)`.

### `pkg/bpf/` (new package, only built on linux/amd64 and linux/arm64)

- `loader.go` — uses `cilium/ebpf`:
  - Loads compiled `.bpf.o` from `go:embed`.
  - Attaches `lsm/bprm_check_security` link.
  - Exposes `Map.Put(cgroupID, []Mapping) error` and `Map.Delete(cgroupID)`.
  - Refuses to start if `/sys/kernel/security/lsm` doesn't contain `bpf`,
    or if BTF is unavailable.
- `substitute.bpf.c` — BPF program:
  - Reads `bpf_get_current_cgroup_id()`.
  - Lookup in `BPF_MAP_TYPE_HASH<u64 cgroup_id, struct mappings>`.
  - Scans envp via `bprm->p` and `bpf_probe_read_user_str`.
  - On placeholder match: `bpf_probe_write_user(addr, real, len)`.
  - Filters: only acts when cgroup is found in map; never modifies anything
    outside the placeholder pattern.
- `cgroup.go` — resolves `/sys/fs/cgroup/.../<podUID>/<containerID>` to a
  `cgroup_id` (u64), using kubelet's well-known cgroup naming.
- `embed.go` — `go:embed substitute.bpf.o` (compiled in CI by clang+
  bpftool; Go build doesn't require clang installed).

### `pkg/bpf/runner.go`

- Owned by `controller.RunBPF`. Lifecycle:
  - Setup: kernel sanity checks, load BPF program, restore from tmpfs.
  - Start: K8s informer filtered by `spec.nodeName == os.Getenv("NODE_NAME")`.
  - On pod added/updated:
    1. Read `bpf-mapping` annotation; skip if absent.
    2. Idempotency check: if `<tmpfs>/<podUID>.json` exists, skip — already
       processed.
    3. Unwrap the wrapToken via existing `vault.Connector` (the DS
       authenticates to Vault using its own SA token, identical to the
       webhook's auth flow).
    4. Resolve cgroup_id of each container.
    5. Write BPF map entries.
    6. Persist tmpfs.
  - On pod deleted: clear BPF map entries for that cgroup, remove tmpfs file.
  - Healthcheck: `/live` reports BPF program loaded and informer synced;
    `/ready` reports BPF map within size budget.
- Reuses `pkg/healthcheck.Service` (same private mux pattern).
- Exposes Prometheus metrics:
  - `vault_injector_bpf_mappings_loaded` (gauge)
  - `vault_injector_bpf_unwrap_errors_total` (counter, label: reason)
  - `vault_injector_bpf_substitutions_total` (counter, fed from BPF
    perf-event ring buffer)
  - `vault_injector_bpf_map_size` (gauge)

### Helm chart

- New `helm/templates/daemonset-bpf.yaml` based on
  `helm/templates/deployment-revoker.yaml`. Differences:
  - Kind: `DaemonSet`.
  - `securityContext`: `privileged: false`,
    `capabilities.add: [BPF, PERFMON, SYS_RESOURCE]`.
  - `hostPID: true` (needed for cgroup resolution).
  - VolumeMount tmpfs at `/run/vault-db-injector/bpf` (memory-backed
    `emptyDir` with `medium: Memory`).
  - VolumeMount `/sys/fs/cgroup` read-only.
  - VolumeMount `/sys/fs/bpf` for pinned maps (optional).
  - Env: `NODE_NAME` from `fieldRef: spec.nodeName`.
  - Tolerations to run on every node, including masters if applicable.
- New `helm/values.yml` keys under `bpf:`:
  ```yaml
  bpf:
    enabled: false                    # the single switch
    image: same as injector image
    resources: {requests/limits}
    tolerations: []
    nodeSelector: {}
    wrapTokenTTL: 5m
  ```
  When `bpf.enabled: false`:
  - The DaemonSet template is skipped (no DS deployed).
  - The webhook Deployment does NOT pass `--bpf-enabled` to the binary.
  - Behavior is byte-identical to today.

  When `bpf.enabled: true`:
  - The DaemonSet template is rendered.
  - The webhook Deployment passes `--bpf-enabled` (or sets the corresponding
    env var) so the running webhook starts wrapping every credential.
  - Both pieces are tied together by the same Helm switch, eliminating any
    "webhook produces placeholders but no DS substitutes" state.

## Activation

A single Helm value (`bpf.enabled`) controls the feature cluster-wide. There
is no per-pod, per-namespace, or per-DbConfiguration opt-in. When enabled,
every pod going through the webhook has its credentials wrapped.

Operationally:
- Turning the feature on is a deliberate cluster-wide commitment. The
  operator must be confident that all nodes have BPF LSM available before
  flipping the switch.
- Disabling it is just `bpf.enabled: false` + Helm upgrade. Pods admitted
  while the feature was on continue to run with the substituted credentials
  in their memory until they restart; new admissions fall back to literal
  env injection.

The webhook itself fails closed if `cfg.BPF.Enabled` is true but Vault
wrapping calls fail — admissions are rejected. This guarantees that no pod
is ever admitted with placeholders that nothing on the cluster can resolve.

## Error handling and failure modes

| Failure                                      | Result                                                     |
|----------------------------------------------|------------------------------------------------------------|
| Webhook can't reach Vault                    | Existing behavior: admission fails. Pod doesn't start.     |
| Webhook can't wrap                           | Admission fails. Pod doesn't start.                        |
| DS not running on node                       | Pod starts, container execve sees placeholder, app crashes, CrashLoopBackoff. Operator alerted by `vault_injector_bpf_mappings_loaded == 0`. |
| DS can't reach Vault for unwrap              | Pod blocks in pending → CrashLoopBackoff. Metric: `vault_injector_bpf_unwrap_errors_total{reason="vault_unreachable"}`. |
| Wrap token expired before DS unwrap          | Same as above. Metric: `reason="token_expired"`.           |
| BPF map full                                 | DS rejects new pods, logs error. Metric: `vault_injector_bpf_map_size` saturates. Operator should bump `MaxMappingsPerNode`. |
| BPF program load fails on DS startup         | DS exits non-zero; K8s reschedules forever (DaemonSet behavior). Operator alerted by pod restart count. |
| Kernel doesn't support BPF LSM               | DS exits with explicit error message at startup; doesn't quietly fall back. |
| DS restart                                   | tmpfs reload, BPF map repopulation. New pods queued via informer. |
| Node reboot                                  | tmpfs gone, pods gone, no recovery needed.                 |
| Race: pod execve before DS unwrap            | LSM hook returns EAGAIN; kernel retries; if too slow, CrashLoopBackoff catches up. |

## Testing strategy

### Unit tests (no kernel)

- `pkg/placeholder`: generation, length, uniqueness, detection.
- `pkg/k8smutator`: existing `classic` and `uri` cases tested both with
  `cfg.BPF.Enabled=false` (expect literal creds, current behavior) and
  `cfg.BPF.Enabled=true` (expect placeholders + `bpf-mapping` annotation).
  Asserts annotation content and env shape.
- `pkg/vault.WrapValues` / `UnwrapValues`: against a stub Vault HTTP server.
- `pkg/bpf/cgroup.go`: cgroup-id resolution against synthetic
  `/sys/fs/cgroup` filesystem in tmpfs.
- `pkg/bpf/runner.go`: pod informer behavior with fake clientset; verifies
  unwrap call shape and tmpfs persistence.

### Integration tests (`-tags=integration`, real kernel)

- BPF program loads on a kernel ≥5.7 with `CONFIG_BPF_LSM=y`.
- LSM hook attaches and detaches cleanly.
- Synthetic execve through a child Go test process; verify substitution
  happens.
- Run on a CI runner with the requirements (matrix: Bottlerocket, Talos,
  recent Ubuntu — pick one for CI, others smoke-tested manually).
- kind/minikube don't support BPF LSM by default → integration tests
  documented as requiring a real cluster.

### End-to-end (manual, staging)

- Deploy chart with `bpf.enabled: true` on a Bottlerocket/Talos cluster.
- Apply any Pod manifest already using `<db>.mode: classic` or `uri` —
  no annotation change needed.
- Verify:
  - `kubectl get pod -o yaml` shows placeholders only.
  - Application logs prove DB connection succeeded → real password was seen.
  - `etcdctl get` on the pod object shows placeholders only.
  - `vault_injector_bpf_substitutions_total` increments.
- Restart the DS pod on the node; verify still-running pod's app keeps
  working (substitution already happened, BPF map for live pods restored
  from tmpfs).
- Reboot the node; verify rescheduled pods get fresh wrap tokens and work.
- `kubectl exec` into the container, run `env` → leak (documented expected).

## Kernel and runtime requirements

- **Linux kernel ≥5.7**, recommended ≥5.11. Required configs:
  - `CONFIG_BPF_LSM=y`
  - `lsm=...,bpf` in `/proc/cmdline` (kernel boot parameter)
  - `CONFIG_DEBUG_INFO_BTF=y` (for CO-RE)
- **Container runtime:** containerd or cri-o (cgroup-v2 path resolution
  assumes systemd-managed cgroups).
- **CAP_BPF + CAP_PERFMON + CAP_SYS_RESOURCE** on the DS container. Avoids
  requiring full `CAP_SYS_ADMIN`.
- Tested distros (CI matrix to be picked): Bottlerocket (EKS), Talos,
  Ubuntu 22.04+. Documented as compatible: GKE COS, AKS Ubuntu, Flatcar.
- **Documented as incompatible:** kind, minikube (kernel options not
  enabled), older managed offerings.

## Decided behaviors (previously open)

- **Wrap token TTL:** configurable via `bpf.wrapTokenTTL` Helm value,
  default 5 min. Sufficient for normal scheduling; large clusters with slow
  image pulls can raise it.
- **No namespace allowlist.** Activation is cluster-wide via Helm value;
  if an operator wants the protection on, it applies to every pod the
  webhook touches.
- **Annotation cleanup:** the DS leaves the `bpf-mapping` annotation in
  place after unwrap. The token is single-use and time-bounded, so it's
  harmless. Stripping would require mutating the pod from outside an
  admission webhook context (allowed but adds complexity for no security
  gain).
- **Sidecar / ephemeral containers:** all hit the same LSM hook. Behavior
  is identical: substitution happens if the cgroup matches a mapping. The
  DS treats every container in the pod identically when populating
  cgroup→mapping.

## Build and CI

**Multi-arch BPF compilation:** a single multi-arch container image with
both `amd64` and `arm64` `.bpf.o` artifacts embedded. The Go binary
selects the right one at startup based on `runtime.GOARCH`.

- The `Dockerfile` adds a build stage with clang + libbpf + bpftool that
  compiles `pkg/bpf/substitute.bpf.c` twice (once with
  `-target bpf -D__TARGET_ARCH_x86`, once with `-D__TARGET_ARCH_arm64`),
  producing `substitute.amd64.bpf.o` and `substitute.arm64.bpf.o`.
- `pkg/bpf/embed.go` uses `go:embed` to ship both objects in the binary.
- The Go runtime selects the matching object at `RunBPF` startup.
- CI publishes a single OCI manifest containing both arch images, built
  via `docker buildx build --platform linux/amd64,linux/arm64`. The clang
  compilation runs once per arch in matching builder stages.

**CI for BPF integration tests:** GitHub Actions on `ubuntu-24.04` runner
(kernel 6.8+, has BPF LSM compiled in but not enabled by default).

- New workflow `.github/workflows/bpf-integration.yml`:
  1. Checks `/sys/kernel/security/lsm` — if BPF LSM isn't enabled, the
     workflow attempts to enable it via `lsm=...,bpf` kernel cmdline
     update + reboot. If the runner image doesn't allow this (most
     hosted GitHub runners don't), the workflow fails fast with a clear
     error pointing operators at self-hosted runners.
  2. Compiles the BPF objects.
  3. Runs `go test -tags=integration_bpf ./pkg/bpf/...` which loads the
     program and exercises the substitution path against a synthetic
     execve.
- If GitHub-hosted runners turn out not to support BPF LSM enablement at
  runtime (likely), the workflow runs as a no-op success in CI and the
  test gate is enforced by a self-hosted runner step. The fallback is
  documented in `CONTRIBUTING.md` so contributors know how to validate
  locally on a kernel with BPF LSM enabled (Ubuntu 22.04+ with cmdline
  tweak, or Bottlerocket/Talos VM).
- Unit tests (no kernel) and webhook-side integration tests stay on the
  default CI workflow, run on every PR.

## Security considerations

- The wrap token in the PodSpec is opaque, single-use, and time-bounded.
  It cannot be replayed.
- Compromise of one DS gives access only to credentials of pods on that
  node. No lateral spread.
- The BPF program uses `bpf_probe_write_user`, which kernel-taints the
  system on first use. Document this loudly in operator-facing docs; it's a
  one-time taint message in dmesg, not an actual security issue.
- The DS runs with `CAP_BPF`, which lets it load arbitrary BPF programs.
  Compromise of the DS is high-impact. Mitigations: minimal image, no
  shell, read-only root filesystem, regular Vault token rotation.
- Vault policy required for the DS:
  - `path "sys/wrapping/unwrap" { capabilities = ["update"] }` (only).
  - No KV read or write rights. The DS only consumes wrap tokens; it
    cannot fetch credentials directly.
- Vault policy required for the webhook (in addition to existing):
  - `path "sys/wrapping/wrap" { capabilities = ["update"] }` (only).

## Documentation deltas

- `docs/getting-started/comparison.md`: add a row for "credential invisibility
  at K8s API layer" and check whether competitors offer it. (Quick survey:
  vault-secrets-operator, external-secrets, secrets-store-csi-driver — none
  do BPF-substitution; this is a real differentiator.)
- `docs/how-it-works/`: new page `bpf-mode.md` covering architecture,
  flow, kernel requirements, and threat model.
- `docs/getting-started/`: new page `bpf-requirements.md` listing kernel
  configs and tested distros.
- `README.md`: mention BPF mode in the feature list.

## Out of scope (future work)

- **kubectl-exec lineage hardening:** detect that the parent of the new
  process is `kubelet → containerd-shim → runc → kubectl-exec target` and
  skip substitution for that path. Requires extending the BPF program to
  walk `task->parent`. Tracked separately.
- **Renewal-aware live updates:** updating credentials in already-running
  processes' memory. Out of scope; use the existing rolling-restart flow.
- **Windows nodes:** out of scope; Windows containers don't have a BPF
  equivalent.
