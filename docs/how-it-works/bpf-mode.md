# BPF Mode

## Overview

BPF mode is an optional credential protection layer that makes database
credentials **invisible at the Kubernetes API layer**. When enabled, the
webhook injects fixed-length placeholder strings into the PodSpec instead
of real credentials. A node-local DaemonSet substitutes the placeholders
with real values at `execve` time using a BPF LSM program attached to the
kernel, so the running process sees the real credentials, but:

- `kubectl get pod -o yaml` shows only placeholders
- etcd dumps and Velero backups contain only placeholders (or expired wrap tokens)
- Kubernetes audit logs never record the real values
- GitOps tooling that captures live cluster state sees no secrets

The application code requires **zero changes**. `os.Getenv("DB_PASSWORD")`
returns the real password as always.

BPF mode wraps the existing `classic` and `uri` injection shapes
transparently. The `<db>.mode` annotation continues to control the env
variable shape; BPF substitution is applied on top of whichever shape is
active.

For deep technical details, see the
[design spec](../superpowers/specs/2026-05-02-ebpf-injection-mode-design.md).

---

## Architecture

The overall flow involves three actors: the **Webhook** (a Deployment), the
**BPF DaemonSet**, and **Vault**.

### Admission phase (Webhook)

When a pod is admitted with a `<db>.mode` annotation and `bpf.enabled: true`:

1. The webhook generates real credentials from the Vault Database Engine
   (existing flow, unchanged).
2. The webhook wraps those credentials in Vault using `sys/wrapping/wrap`
   with a short TTL (default 5 minutes). This produces a single-use,
   opaque wrap token.
3. The webhook generates one placeholder per credential field. Each
   placeholder is a fixed-length string (`__VDBI_PH_<32-hex-chars>___`,
   77 bytes). The fixed length allows the BPF program to substitute values
   in-place without resizing the `execve` argument area.
4. The webhook attaches a `db-creds-injector.numberly.io/bpf-mapping`
   annotation to the pod containing the wrap token and the mapping from
   placeholder strings to field names.
5. The webhook writes the placeholder strings into `env:` — never the real
   credentials. etcd stores only these placeholders.

### Node phase (DaemonSet)

The BPF DaemonSet runs in `mode=bpf`, one pod per node:

1. A Kubernetes informer filtered by `spec.nodeName` detects pod creation.
2. The DS reads the `bpf-mapping` annotation, unwraps the token from Vault
   (consuming it — it cannot be replayed), and retrieves the real credentials.
3. The DS resolves the cgroup ID of **every container** (regular, init, and
   ephemeral) from the kubelet cgroup hierarchy at `/sys/fs/cgroup`.
4. The DS writes one `(cgroup_id → placeholder/value pairs)` entry per
   container into the BPF hash map. All containers in the pod share the same
   credential substitution.
5. The DS persists the mapping to tmpfs at
   `/run/vault-db-injector/bpf/<podUID>.json` (JSON format with both the
   placeholder mappings and the list of programmed cgroup IDs) so it can
   recover after a DS restart without contacting Vault again.

### execve phase (BPF LSM hook)

The BPF program is attached to the `bprm_check_security` LSM hook:

1. On every `execve`, the kernel calls the hook with the new binary's
   environment pointer.
2. The BPF program reads the current cgroup ID and looks it up in the hash map.
3. If found, it scans `envp` for placeholder patterns and writes the real
   values in-place using `bpf_probe_write_user`. Values are NUL-padded to
   the fixed placeholder length.
4. If the cgroup ID is not found (the DS hasn't processed this pod yet),
   the hook returns 0 (allow), leaving `envp` unchanged with the literal
   placeholder strings. The application sees the placeholder as the env var
   value, which typically causes a connection failure → CrashLoopBackoff.
   The container self-resolves once the DS catches up and populates the BPF
   map. This is the **fail-safe** behavior (no exec is blocked; bad creds
   cause application-level failure rather than kernel retry).

```
Webhook                          DaemonSet              Vault
  │                                  │                    │
  │─── database/creds/<role> ────────────────────────────►│
  │◄── {username, password} ─────────────────────────────-│
  │─── sys/wrapping/wrap ────────────────────────────────►│
  │◄── wrapToken ────────────────────────────────────────-│
  │
  │ inject placeholders into env: + attach bpf-mapping annotation
  │
  ▼ (pod admitted to apiserver / etcd — no real creds)
  
DaemonSet (node-local)
  │─── watch pods on this node ─────────────────────────-
  │    read bpf-mapping annotation
  │─── sys/wrapping/unwrap(wrapToken) ──────────────────►│
  │◄── {username, password} ─────────────────────────────│
  │
  │ resolve cgroup_id → write BPF map → persist to tmpfs
  
execve (container process)
  │
  └── kernel LSM hook: bprm_check_security
        bpf_get_current_cgroup_id()
        lookup BPF map
        bpf_probe_write_user(placeholder_addr, real_value)
        process env now has real credentials
```

---

## Activation

BPF mode is controlled by a **single Helm value**:

```yaml
bpf:
  enabled: true
```

Setting this to `true`:

- Causes the Helm chart to deploy the BPF DaemonSet on every node.
- Passes `--bpf-enabled` to the webhook Deployment so every admitted pod
  gets its credentials wrapped.

Both pieces are tied to the same switch — there is no intermediate state
where the webhook produces placeholders but no DaemonSet is present to
resolve them.

There is no per-pod, per-namespace, or per-DbConfiguration opt-in. When
`bpf.enabled: true`, every pod going through the webhook is protected.

**Turning it off:** set `bpf.enabled: false` and run `helm upgrade`.
Pods admitted while the feature was on continue to run with the substituted
values already in their memory. New admissions fall back to literal env
injection.

Additional Helm values:

| Value | Default | Description |
|-------|---------|-------------|
| `bpf.wrapTokenTTL` | `5m` | Vault wrap token TTL. Raise on clusters with slow image pulls. |
| `bpf.resources` | — | Resource requests/limits for the DaemonSet pods. |
| `bpf.tolerations` | `[]` | Toleration rules; extend if running on tainted nodes. |
| `bpf.nodeSelector` | `{}` | Restrict DaemonSet to specific nodes. |

---

## Threat model

BPF mode targets leaks through the **Kubernetes control plane**. It does
not protect against in-pod or node-level attackers.

| Attack vector | classic / uri | bpf mode |
|---------------|--------------|----------|
| `kubectl get pod -o yaml` | **leak** | safe |
| etcd dump / Velero backup | **leak** | safe† |
| Kubernetes audit logs | **leak** | safe |
| GitOps tooling capturing live state | **leak** | safe |
| `kubectl exec` with RBAC shell access | **leak** | **leak** |
| Sidecar in shared PID namespace (`/proc/<pid>/environ`) | **leak** | **leak** |
| Node compromise (root on host) | **leak** | **leak** |

† The PodSpec contains the wrap token, not the real credential. The token
is single-use and expires after 5 minutes (configurable). A backup older
than the TTL contains nothing useful. A backup taken within the TTL window
contains a token that can be unwrapped once — the DaemonSet will have
already consumed it.

**DaemonSet blast radius:** Compromise of a single DS pod exposes only the
credentials of pods on that node. There is no lateral path to other nodes.

---

## Failure modes

| Failure | Behavior |
|---------|----------|
| Webhook cannot reach Vault | Admission fails. Pod does not start (same as today). |
| Webhook `sys/wrapping/wrap` call fails | Admission fails. No pod with unresolvable placeholders is ever admitted. |
| DaemonSet not running on the node | Container `execve` sees placeholder; application crashes; CrashLoopBackoff. Operator alerted by `vault_injector_bpf_mappings_loaded == 0`. |
| DaemonSet cannot reach Vault for unwrap | Pod starts but enters CrashLoopBackoff. Metric: `vault_injector_bpf_unwrap_errors_total{reason="vault_unreachable"}`. |
| Wrap token expired before DaemonSet unwraps | Same as above. Metric: `reason="token_expired"`. Raise `bpf.wrapTokenTTL` on slow clusters. |
| BPF map full | DaemonSet rejects new pod entries, logs error. Bump `bpf.maxMappingsPerNode`. |
| BPF program fails to load at DS startup | DS exits non-zero; Kubernetes reschedules (DaemonSet behavior). Operator alerted by pod restart count. |
| Kernel does not support BPF LSM | DS exits with an explicit error message; never silently falls back to classic mode. |
| DaemonSet restart | DS preloads the processed set from tmpfs, starts the informer, waits for cache sync, then re-programs the BPF map for every pod still running on the node using the cgroup IDs stored in tmpfs. Pods rescheduled to another node during DS downtime have their stale tmpfs entries removed. No Vault contact needed. |
| Node reboot | tmpfs is wiped; pods on the node are also rescheduled. Admission flow issues fresh wrap tokens. No recovery needed. |
| Race: container execves before DS has unwrapped | LSM hook returns 0 (allow); envp retains placeholder strings; application sees placeholder as env value → connection failure → CrashLoopBackoff. Self-resolves once DS catches up. |

---

## Observability

The DaemonSet exposes the following Prometheus metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `vault_injector_bpf_mappings_loaded` | Gauge | Number of pods whose credentials are currently programmed in the BPF map. `0` on a node means no pods are protected. |
| `vault_injector_bpf_map_size` | Gauge | Number of individual cgroup entries in the BPF map. Equals `mappings_loaded` for single-container pods; higher for multi-container pods. Alert when this approaches `bpf.maxMappingsPerNode`. |
| `vault_injector_bpf_unwrap_errors_total` | CounterVec | Vault unwrap failures, labelled by `reason`. |

---

## Limitations

BPF mode intentionally does not protect against:

- **`kubectl exec` with shell access.** A user who can exec into the container
  and run `env` or read `/proc/<pid>/environ` sees the real credentials after
  substitution. A future lineage-detection extension (not in scope) would
  address this. To reduce exposure, restrict `kubectl exec` via Kubernetes
  RBAC and enforce Pod Security Admission's `restricted` profile on namespaces
  that hold high-sensitivity credentials (see
  [bpf-requirements.md](../getting-started/bpf-requirements.md#recommended-pod-hardening)).
- **Sidecars sharing the PID namespace.** Any container with `shareProcessNamespace: true`
  that can read `/proc` of the application container can observe the substituted
  environment.
- **Node compromise.** An attacker with root on the node can read BPF maps or
  the tmpfs persist files. BPF mode is a control-plane protection, not a
  host-security control.
- **Credentials longer than 73 bytes.** The fixed placeholder length (77 bytes)
  constrains the maximum real credential value length to 73 bytes (the BPF
  buffer reserves space for a NUL terminator and alignment padding). The
  webhook validates this at admission time and rejects pods whose Vault role
  generates credentials that exceed the limit. Configure the Vault
  `password_policy` for the role to enforce an appropriate maximum length.
- **Windows nodes.** No BPF equivalent exists on Windows.
- **`kind` and `minikube`.** These local environments do not enable BPF LSM
  kernel options by default. See
  [bpf-requirements.md](../getting-started/bpf-requirements.md) for compatible
  environments.

---

## Security notes

- **Kernel taint.** The BPF program uses `bpf_probe_write_user`, which produces
  a one-time kernel taint message in `dmesg` on first use. This is cosmetic and
  does not indicate a security issue.
- **DaemonSet privileges.** The DS requires `CAP_BPF`, `CAP_PERFMON`, and
  `CAP_SYS_RESOURCE`. It does not require full `CAP_SYS_ADMIN`. Treat DS
  pods as high-trust; use minimal image, no shell, read-only root filesystem.
- **Vault policies.** The webhook additionally needs `sys/wrapping/wrap`; the
  DS only needs `sys/wrapping/unwrap`. Neither gains new KV read or write
  rights. See [bpf-requirements.md](../getting-started/bpf-requirements.md).
