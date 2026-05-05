# Components

**Audience:** Platform operator

vault-db-injector runs as four cooperating components. They share the
same Go binary and select their role at startup via the `mode` config
key.

## Injector (webhook)

**File:** `pkg/injector/injector.go`

The injector runs as a Deployment and serves a Kubernetes Mutating
Admission Webhook on TLS. When a pod carrying the
`vault-db-injector: "true"` label is admitted, the webhook reads the
`db-creds-injector.numberly.io/*` annotations and decides what to put
into the pod's env.

In **legacy mode**, the webhook calls `CanIGetRoles` against Vault to
verify that the pod's ServiceAccount is bound to the requested DB
role, issues a Vault orphan token holding the role's policy, fetches
dynamic credentials, and writes them as plaintext env vars onto the
container spec.

In **NRI mode** (projected auth), the webhook does not fetch
credentials. It writes opaque `__VDBI_PH_<64hex>___` placeholders into
the env — one pair per dbConfig — and stamps a per-dbConfig UUID into
the `db-creds-injector.numberly.io/uuid` annotation for later
correlation. `CanIGetRoles` is skipped because Vault attests pod
identity natively at pod-token time.

## NRI plugin (DaemonSet)

**Files:** `pkg/nri/...`

The NRI plugin runs as a DaemonSet on every node, mounting
`/var/run/nri/nri.sock` from the host. It registers as an NRI plugin
with containerd or CRI-O. On every `CreateContainer` event it filters
by pod label, scans env for placeholders, and on a match fetches the
pod's identity from the kube-apiserver, logs into Vault as that pod
(projected mode) or as itself (legacy mode), issues the credentials,
and emits a `ContainerAdjustment` so runc starts the container with
the real env.

A per-node tmpfs cache at `/run/<release-fullname>/nri/cache.json`
holds unwrapped credentials between plugin restarts so a CrashLoop
does not require re-issuing creds on every retry. The cache is wiped
on node reboot.

## Renewer (Deployment)

**File:** `pkg/renewer/renewer.go`

The renewer runs as a Deployment with leader election. Every 5 minutes
(configurable via `SyncTTLSecond`) the leader walks the KV bookkeeping
mount, calls `auth/token/renew` on each stored token, and
`sys/leases/renew` on each stored lease. In projected-SA mode the
renewer holds a minimal Vault policy: renew-only, no revoke, no KV
delete. Revocation is owned by the revoker exclusively.

## Revoker (Deployment)

**File:** `pkg/revoker/revoker.go`

The revoker runs as a Deployment with leader election. The leader
watches the Kubernetes API for pod `DELETE` events filtered by the
`vault-db-injector: "true"` label. On a delete it revokes the pod's
token and lease, then wipes the KV entry. A 5-minute periodic
safety-net sweep (`safetyNetSync`) catches pods that died while the
watch was disconnected or the revoker was down.

In projected-SA mode the revoker owns **all** revocation: the renewer
no longer touches `auth/token/revoke-orphan` or KV `delete`.

## Leader election

The renewer and revoker run multi-replica for high availability. Only
the elected leader does work; the others stand by. The webhook is
stateless and runs all replicas active. See [operations](operations.md)
for the mechanics and the `vdbi_is_leader` metric.
