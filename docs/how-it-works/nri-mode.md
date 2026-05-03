# NRI mode

NRI mode replaces credentials with opaque placeholders in the PodSpec and
substitutes them at container creation time via a node-local DaemonSet
plugin. Credentials never appear in any persisted Kubernetes resource
(PodSpec, etcd, audit logs, GitOps captures).

## Components

- **Webhook** — generates placeholders, wraps the credential payload as a
  single-use Vault wrap token (5min TTL by default), attaches both as a
  pod annotation `db-creds-injector.numberly.io/nri-mapping`.
- **DaemonSet** — node-local NRI plugin. On `CreateContainer`: reads the
  annotation, unwraps the token, substitutes placeholders in env vars,
  returns a `ContainerAdjustment` to containerd. Runs **before runc**, so
  the new process is born with the real envp.
- **Cache** — per-node tmpfs at `/run/vault-db-injector/nri/cache.json`
  persists unwrapped credentials so they survive plugin pod restart but
  not node reboot. Without persistence, a CrashLoop pod whose plugin DS
  restarts in the meantime would see the literal placeholder string in
  env (the wrap token is single-use).

## Failure modes and detection

The plugin emits Prometheus metrics:

- `vdbi_nri_substitutions_total` — successful adjustments emitted
- `vdbi_nri_unwrap_failures_total{reason}` — labels: `malformed_annotation`,
  `unwrap_error`

### What can still go wrong

1. **No NRI plugin on the target node.** If the DS pod is missing on a
   node (image pull, broken DS, post-install delay), pods scheduled there
   start with the literal placeholder string in env. The app fails to
   connect to the database with the placeholder as password and crashes
   visibly. The plugin emits no metric for this case (it is not running
   on that node).

   **Detection** — alert when a pod has the `nri-mapping` annotation but
   its node has no ready NRI plugin pod:

   ```yaml
   - alert: NRIPluginMissingOnNode
     expr: |
       count by (node) (
         kube_pod_annotations{annotation_db_creds_injector_numberly_io_nri_mapping!=""}
       )
       and on (node) (
         count by (node) (
           kube_pod_status_ready{condition="true",pod=~"vault-db-injector-nri-.*"}
         ) == 0
       )
     for: 1m
     annotations:
       summary: NRI plugin not ready on node {{ $labels.node }} — pods with credentials are starting unsubstituted
   ```

2. **Wrap token expired before CreateContainer.** Slow image pull, long
   pod scheduling delay (> 5 min) → unwrap fails. Plugin logs the error
   and increments `vdbi_nri_unwrap_failures_total{reason="unwrap_error"}`.
   Container starts unsubstituted, app crashes with bad credentials,
   visible. Increase `nri.wrapTokenTTL` if this is recurring on a slow
   cluster.

3. **Plugin DS pod and main container restart in the wrong order.** The
   on-disk cache covers this: the second CreateContainer attempt for the
   same pod UID reuses the stored mapping.

## Hardening checklist

- Set resource requests on the DS so it is not OOM-killed on memory pressure
- Use `priorityClass: system-node-critical` to make eviction less likely
- Monitor the alert above
- Set `nri.wrapTokenTTL` higher than the worst-case scheduling + image pull
  time on your cluster (default 5min is fine for most clusters)

## Trust posture

The cache file at `/run/vault-db-injector/nri/cache.json` contains
unwrapped credentials in cleartext, perms `0600 root:root`, on tmpfs.
The same posture applies to:

- kubelet's projected service-account tokens at
  `/var/lib/kubelet/pods/<UID>/volumes/kubernetes.io~projected/...`
- Any Secret mounted as a volume

A root-on-node attacker can already read `/proc/<pid>/environ` of every
container, so the cache adds no new attack surface beyond what root already
has. The cache is **never on persistent disk** (tmpfs) and **never in
backups** (`/run` is excluded by every node backup tool).
