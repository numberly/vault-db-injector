# NRI mode

NRI mode replaces credentials with opaque placeholders in the PodSpec
and substitutes them at container creation time via a node-local
DaemonSet plugin. Credentials never appear in any persisted Kubernetes
resource (PodSpec, etcd, audit logs, GitOps captures).

The design is **transparent**: the user only adds the existing
`db-creds-injector.numberly.io/*` annotations to opt their pod into
credential injection (same as classic mode). Nothing about NRI is
visible from the pod spec — no extra annotation, no extra env. The
webhook just substitutes placeholders for credentials at admission;
the plugin substitutes the real values at container creation. The user
doesn't need to know NRI exists.

## Architecture

```
kube-apiserver
     ↓ pod admitted with the user's standard annotations:
     ↓   db-creds-injector.numberly.io/role: postgres-readonly
     ↓   db-creds-injector.numberly.io/cluster: databases
     ↓   db-creds-injector.numberly.io/main.mode: classic
     ↓   db-creds-injector.numberly.io/main.env-key-dbuser: DB_USER
     ↓   db-creds-injector.numberly.io/main.env-key-dbpassword: DB_PASS
     ↓ + label vault-db-injector="true" (matches webhook objectSelector)
     ↓
[vault-db-injector webhook]
     ↓ in NRI mode:
     ↓   1. CanIGetRoles → admission fail-fast on Vault RBAC mismatch
     ↓   2. generate two placeholders __VDBI_PH_<64hex>___
     ↓   3. put them in env (DB_USER, DB_PASS) instead of cleartext
     ↓   4. NO new annotation. NO env beyond placeholders.
kubelet
     ↓
containerd (NRI enabled)
     ↓ /var/run/nri/nri.sock
[vault-db-injector NRI plugin DaemonSet]
     ↓ on CreateContainer:
     ↓   1. filter by pod label (cfg.NRI.PodLabel) — skip if no match
     ↓   2. fast-path: scan env for placeholder shape — skip if none
     ↓   3. GET pod from kube-apiserver (UID, namespace, SA)
     ↓   4. parse the pod's standard db-creds-injector.numberly.io/*
     ↓      annotations to learn (vault path, db role, env-key map)
     ↓   5. authenticate to Vault as the plugin's OWN SA token
     ↓   6. CanIGetRoles for the K8s-attested pod identity
     ↓   7. for each dbConfig: GetDbCredentials — dynamic credential
     ↓      issued, lease tagged with per-dbConfig UUID (from the
     ↓      db-creds-injector.numberly.io/uuid annotation) for
     ↓      renewer/revoker correlation
     ↓   8. map env-key → placeholder → cred field, merge all mappings
     ↓   9. emit ContainerAdjustment{env: substituted}
runc
     ↓ execve with real envp
app
```

## Components

- **Webhook** — generates placeholders, puts them in env. Calls
  `CanIGetRoles` at admission so an unauthorised pod is rejected
  immediately. Does **not** call Vault sys/wrapping/wrap, does **not**
  fetch credentials, does **not** add any annotation. Pod spec contains
  only what the user wrote plus the placeholder strings.
- **DaemonSet (NRI plugin)** — node-local. Filters pods by the
  configured label (defaults to `vault-db-injector`). Reads the user's
  existing annotations to know the Vault role/path/env-key mapping.
  Authenticates to Vault using its own SA token. Creates the dynamic
  credential. Substitutes placeholders in the container env at
  `CreateContainer` (before runc).
- **Cache** — per-node tmpfs at `/run/<release-fullname>/nri/cache.json`
  persists unwrapped credentials so they survive plugin pod restart
  but not node reboot. A pod whose plugin DS restarts mid-CrashLoop
  continues to receive the substituted env on retry instead of the
  placeholder.

## Multiple injector releases on one cluster

Two helm releases (e.g. prod + dev) running side by side require
distinct values to avoid colliding on the containerd NRI registration
and the cache file:

| value | prod release | dev release |
|---|---|---|
| `nri.pluginIndex` | `"10"` (default) | `"11"` |
| `vaultDbInjector.configuration.webhookMatchLabels` | `vault-db-injector` | `vault-db-injector-dev` |

The chart auto-generates these per-release:
- `pluginName` = the helm release fullname (unique per release)
- `cachePath` = `/run/<release-fullname>/nri/cache.json` (unique per release)
- `podLabel` = `webhookMatchLabels` value (already release-specific)

Override `nri.pluginIndex` in dev so both indices coexist on
containerd.

## Failure modes and detection

The plugin emits Prometheus metrics:

- `vdbi_nri_substitutions_total` — successful adjustments emitted
- `vdbi_nri_unwrap_failures_total{reason}` — labels:
  `fetch_error` (Vault auth failure, identity mismatch, missing pod,
  CanIGetRoles deny, etc.)

### What can still go wrong

1. **No NRI plugin on the target node.** If the DS pod is missing on a
   node (image pull, broken DS, post-install delay), labelled pods
   scheduled there start with the literal placeholder string in env.
   The app fails to connect to the database with the placeholder as
   password and crashes visibly. The plugin emits no metric for this
   case (it is not running on that node).

   **Mitigation** — the DS defaults to `tolerations: [{operator:
   Exists}]` so it runs on every node regardless of taints.

   **Detection** — alert when a pod with a release-specific label has
   no ready plugin pod on its node:
   ```yaml
   - alert: NRIPluginMissingOnNode
     expr: |
       count by (node) (kube_pod_labels{label_vault_db_injector="true"})
       and on (node) (
         count by (node) (
           kube_pod_status_ready{condition="true",pod=~".*-vault-db-injector-nri-.*"}
         ) == 0
       )
     for: 1m
   ```

2. **Pod identity forgery via `pods.update`** — closed by Hunter
   finding #H6. The plugin queries kube-apiserver for the pod (by NRI
   sandbox UID) to get `spec.serviceAccountName` directly. The
   annotation does not carry pod identity, so there's nothing to
   forge.

3. **Pod name reuse race** — closed by Hunter finding #CRIT-1. NRI
   sandbox UID must equal kube-apiserver `pod.UID`.

4. **Plugin DS pod and main container restart in the wrong order** —
   the on-disk cache covers this: the second CreateContainer attempt
   for the same pod UID reuses the stored credential, no second Vault
   round-trip.

5. **Force-deleted pod cache leak** — a pod deleted with
   `--grace-period=0 --force` does not fire NRI's `RemovePodSandbox`
   event. The plugin runs a periodic 5-minute sweep that lists pods
   on its node via the K8s API and evicts cache entries whose UIDs
   no longer exist.

## Security considerations

The NRI DaemonSet runs as `root` on every node to read the containerd NRI socket at `/var/run/nri/nri.sock`. When `useProjectedSA=true`, the plugin obtains a Kubernetes ServiceAccount token via TokenRequest (API-attested) and logs into Vault natively, gaining credentials that inherit the `create serviceaccounts/token` cluster-wide permission granted to its RBAC role. A container escape from the plugin pod is therefore equivalent to full cluster Vault access. **Recommendation**: Deploy NRI mode only on dedicated or hardened nodes, apply Pod Security Admission policies to restrict privileged containers, and review node images regularly for compromise.

## Hardening checklist

- Set resource requests on the DS so it is not OOM-killed on memory
  pressure
- Use `priorityClass: system-node-critical` (or
  `k8s-numberly-critical` per Numberly conventions) to make eviction
  less likely
- Monitor `NRIPluginMissingOnNode` (above) and
  `vdbi_nri_unwrap_failures_total{reason="fetch_error"}`
- Apply the Kyverno policy at
  [helm/policies/kyverno-restrict-nri-socket.yaml](../../helm/policies/kyverno-restrict-nri-socket.yaml)
  to block hostPath mounts of `/var/run/nri`, `/opt/nri`, and
  `/run/<release-fullname>` outside the plugin's namespace
- On RHEL/CoreOS leave SELinux enforcing; do not run user pods with
  `seLinuxOptions.type: spc_t`

## Trust posture

The cache file at `/run/<release-fullname>/nri/cache.json` contains
unwrapped credentials in cleartext, perms `0600 root:root`, on tmpfs.
The same posture applies to:

- kubelet's projected service-account tokens at
  `/var/lib/kubelet/pods/<UID>/volumes/kubernetes.io~projected/...`
- Any Secret mounted as a volume

A root-on-node attacker can already read `/proc/<pid>/environ` of every
container, so the cache adds no new attack surface beyond what root
already has. The cache is **never on persistent disk** (tmpfs) and
**never in backups** (`/run` is excluded by every node backup tool).

A pod that mounts hostPath `/run` AND runs as UID 0 (root) can read
the cache. PSA `restricted` and `baseline` profiles forbid hostPath
mounts entirely, which is the recommended baseline for user
namespaces. The Kyverno policy referenced above blocks hostPath
`/run/<release-fullname>` for user pods as defense in depth.
