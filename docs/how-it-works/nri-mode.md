# NRI mode

NRI mode replaces credentials with opaque placeholders in the PodSpec and
substitutes them at container creation time via a node-local DaemonSet
plugin. Credentials never appear in any persisted Kubernetes resource
(PodSpec, etcd, audit logs, GitOps captures).

## Architecture (schema v2 — pull-not-push)

```
kube-apiserver
     ↓ pod admitted with annotation
     ↓ db-creds-injector.numberly.io/nri-mapping = {
     ↓   schema:2, db_path, db_role, placeholders, request_id,
     ↓   pod_namespace, pod_service_account
     ↓ }   (NO Vault token, NO bearer credential)
kubelet
     ↓
containerd (NRI enabled)
     ↓ /var/run/nri/nri.sock
[vault-db-injector NRI plugin DaemonSet]
     ↓ on CreateContainer:
     ↓   1. read pod-sandbox annotation, parse NRIMapping
     ↓   2. GET pod from kube-apiserver — verify UID, namespace, SA
     ↓      match annotation (defense vs annotation forgery)
     ↓   3. authenticate to Vault as the plugin's OWN SA token
     ↓      (k8s auth method)
     ↓   4. CanIGetRoles for the actual pod identity → confirms the
     ↓      Vault auth role binds this (namespace, SA)
     ↓   5. GetDbCredentials — dynamic credential issued, lease tagged
     ↓      with pod UID for renewer/revoker correlation
     ↓   6. emit ContainerAdjustment{env: substituted}
runc
     ↓ execve with real envp
app
```

## Components

- **Webhook** — generates placeholders, stamps the
  `db-creds-injector.numberly.io/nri-mapping` annotation. Calls Vault
  CanIGetRoles to fail-fast at admission if the pod's SA isn't bound to
  the requested role. **Does not** call Vault sys/wrapping/wrap; no
  credential or token is placed in the PodSpec.
- **DaemonSet (NRI plugin)** — node-local. Authenticates to Vault using
  its own ServiceAccount token. Verifies pod identity against the K8s
  API. Creates the dynamic credential. Substitutes placeholders in the
  container env at `CreateContainer` (before runc).
- **Cache** — per-node tmpfs at `/run/vault-db-injector/nri/cache.json`
  persists unwrapped credentials so they survive plugin pod restart but
  not node reboot. A pod whose plugin DS restarts mid-CrashLoop continues
  to receive the substituted env on retry instead of the placeholder.

## Failure modes and detection

The plugin emits Prometheus metrics:

- `vdbi_nri_substitutions_total` — successful adjustments emitted
- `vdbi_nri_unwrap_failures_total{reason}` — labels: `malformed_annotation`,
  `fetch_error` (covers identity mismatch, Vault errors, missing pod, etc.)

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

2. **Annotation forgery** (closed by Hunter finding #H6). An attacker
   with `pods.create` or `pods.update` RBAC can craft an annotation
   claiming any `pod_namespace`/`pod_service_account`. The plugin
   defends against this in three layers:
   - The pod's actual UID (from NRI sandbox) must match
     `pod.metadata.uid` recorded by kube-apiserver.
   - The pod's actual namespace and `spec.serviceAccountName` (from
     kube-apiserver) must match the annotation's claim.
   - Vault `CanIGetRoles` is called with the K8s-attested identity, not
     the annotation's, so a mismatched claim fails authorization.

3. **Plugin DS pod and main container restart in the wrong order.** The
   on-disk cache covers this: the second CreateContainer attempt for the
   same pod UID reuses the stored credential, no second Vault round-trip.

4. **Force-deleted pod cache leak** — a pod deleted with
   `--grace-period=0 --force` does not fire NRI's `RemovePodSandbox`
   event. The plugin runs a periodic 5-minute sweep that lists pods on
   its node via the K8s API and evicts cache entries whose UIDs no
   longer exist.

## Schema versioning

The plugin only accepts annotations with `"schema":2`. Schema 1 (the
legacy `wrap_token` design) is rejected with a clear error so an
operator never silently runs in an inconsistent state during upgrade.

**Upgrade path** — when moving from a v1 webhook + v1 plugin
deployment to v2:

1. Set `nri.enabled: false` in helm values and apply. New pods now
   inject literal credentials in PodSpec (legacy mode, byte-identical
   to pre-NRI behavior).
2. Upgrade the webhook and plugin Deployment/DaemonSet images together.
3. Set `nri.enabled: true` and apply.

If you upgrade hot (without disabling NRI), pods admitted by an old
webhook just before the upgrade will hit the new plugin and be rejected
with `unsupported nri-mapping schema version 1`. Container starts with
placeholder, app crashes with bad cred, kubelet restarts it. Within a
few seconds the new webhook is admitting v2 annotations and recovery
is automatic — but expect ~30 seconds of pod CrashLoop noise during the
window. Cleaner to drain.

## Hardening checklist

- Set resource requests on the DS so it is not OOM-killed on memory pressure
- Use `priorityClass: system-node-critical` to make eviction less likely
- Monitor `NRIPluginMissingOnNode` (above) and
  `vdbi_nri_unwrap_failures_total{reason="fetch_error"}`
- Apply the Kyverno policy at
  [helm/policies/kyverno-restrict-nri-socket.yaml](../../helm/policies/kyverno-restrict-nri-socket.yaml)
  to block hostPath mounts of `/var/run/nri` and `/opt/nri` outside the
  plugin's namespace
- On RHEL/CoreOS leave SELinux enforcing; do not run user pods with
  `seLinuxOptions.type: spc_t`

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

A pod that mounts hostPath `/run` AND runs as UID 0 (root) can read the
cache. PSA `restricted` and `baseline` profiles forbid hostPath mounts
entirely, which is the recommended baseline for user namespaces. The
Kyverno policy referenced above does not currently include
`/run/vault-db-injector` because PSA covers it; if you must keep
`baseline` off and root-on-pod allowed, extend the Kyverno policy
manually.
