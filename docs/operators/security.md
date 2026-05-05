# Security

**Audience:** Platform operator

## Threat model

The threats worth thinking about fall into three buckets: a compromised
pod, a compromised node, and a compromised injector. A compromised
**pod** can read its own env and `/proc/<pid>/environ`. In legacy mode
that means raw DB credentials in plaintext; in NRI mode that means
opaque placeholders pre-substitution and the same plaintext creds
post-substitution. A compromised **node** with root already owns every
container on it — root can read every `/proc/*/environ` and every
mounted Secret. A compromised **injector** is the one that changes
shape between modes.

In **legacy mode**, the injector holds a broad Vault policy:
`database/creds/*` and `auth/token/create-orphan`. An attacker who
takes over the injector pod can mint credentials for any DB role
configured under `database/`. The blast radius is the union of every
DB engine the injector can reach.

In **projected-SA mode**, the injector does not hold a DB-issuing
policy. The pod-token bears the role constraint cryptographically: a
JWT minted by the injector for pod A cannot pass `bound_service_account_names`
on Vault's role for pod B. A compromised injector can still mint
TokenRequests for any pod's SA — but the resulting Vault token is
scoped to the pod's role only, and the Vault audit log attributes
issuance to the actual pod.

Residual risks: the **NRI plugin DaemonSet runs as root** to read the
containerd socket. A container escape from the plugin pod yields full
node compromise and (via the `serviceaccounts/token` cluster-wide
permission) effectively full Vault access. NRI mode does not mitigate
node-level compromise — it mitigates etcd / GitOps / audit-log leakage
of credentials.

## NRI mode hardening

- **PodSecurityAdmission `restricted`** on every user namespace.
  `restricted` and `baseline` both forbid hostPath volumes, which is
  the only way for a non-injector pod to register as an NRI plugin or
  read the cache file. The plugin's own namespace must be labeled
  `pod-security.kubernetes.io/enforce=privileged`.
- **Kyverno ClusterPolicy** blocking `/var/run/nri`, `/opt/nri`, and
  `/run/<release-fullname>` hostPath mounts outside the trusted
  namespace. Reference policy below.
- **SELinux/AppArmor enforcing** on RHEL/CoreOS. Do not run any pod
  with `seLinuxOptions.type: spc_t`. The default
  `container_runtime_t` socket label prevents user pods from
  connecting even if they bypass the hostPath check.
- **hostPath restrictions** at the admission layer: the only legal
  consumer of `/var/run/nri/nri.sock` is the plugin DS itself.

## Kyverno policy

The repo ships a reference Kyverno ClusterPolicy that blocks NRI
hostPath mounts outside the injector's namespace. It is the cheapest
defense-in-depth layer on top of PSA, and the recommended baseline:

[helm/policies/kyverno-restrict-nri-socket.yaml](https://github.com/numberly/vault-db-injector/blob/main/helm/policies/kyverno-restrict-nri-socket.yaml)

The policy denies any pod that mounts `/var/run/nri`, `/opt/nri`, or
`/run/<release-fullname>` unless it lives in the injector's namespace.
That closes the "user pod registers as an NRI plugin" path that PSA
already blocks at the `restricted` level — useful when you cannot
enforce `restricted` cluster-wide.

## Projected-SA security gains

- **Native attestation by Vault**: the audit log shows which pod's SA
  acquired which credentials. In legacy mode, every issuance is
  attributed to the injector's SA.
- **Compromised injector cannot issue arbitrary DB credentials**: the
  injector has no DB-issuing policy in projected mode, and the
  pod-token bears the role constraint cryptographically.
- **Reduced blast radius**: the only Kubernetes capability the
  injector still needs is `serviceaccounts/token`, scoped by audience.
  An empty `tokenRequestAudiences` is rejected at startup since v3.0
  because an empty audience produces a JWT any service can reuse.

## Cache file posture

The NRI plugin's cache at `/run/<release-fullname>/nri/cache.json`
contains unwrapped credentials in cleartext, perms `0600 root:root`,
on tmpfs. The same posture applies to kubelet's projected
ServiceAccount tokens at
`/var/lib/kubelet/pods/<UID>/volumes/kubernetes.io~projected/...` and
to any Secret mounted as a volume.

A root-on-node attacker can already read `/proc/<pid>/environ` of
every container on the node, so the cache adds no new attack surface
beyond what root already owns. The cache is **never on persistent
disk** (tmpfs) and **never in backups** (`/run` is excluded by every
node backup tool).

The only path that lets a non-root user pod read the cache is mounting
hostPath `/run` and running as UID 0. PSA `restricted` and `baseline`
forbid hostPath mounts outright. The Kyverno policy blocks
`/run/<release-fullname>` for user pods as a second layer.

## Audit trail

In **projected mode**, the Vault audit log shows the pod's SA as the
identity that issued each `database/creds/<role>` call. Correlate by
SA name plus the `db-creds-injector.numberly.io/uuid` annotation
stamped on the pod. In **legacy mode**, every issuance is attributed
to the injector's own SA — the audit log tells you the injector did
it on behalf of someone, but cannot prove who without correlating
against the KV bookkeeping mount.

!!! danger "NRI DaemonSet runs as root"
    NRI mode requires the plugin DaemonSet to mount
    `/var/run/nri/nri.sock` — the same socket containerd uses for
    plugin registration. Any pod that mounts this hostPath can
    register as an NRI plugin and mutate every container created on
    the node (env, mounts, capabilities, args).

    This is **inherent to NRI**, not specific to this project. The
    cluster admin must restrict who can mount these paths via PSA
    `restricted`/`baseline` on user namespaces, the Kyverno policy
    above, and SELinux/AppArmor enforcing. A container escape from
    the NRI plugin pod yields full node compromise and effective full
    Vault access via the `serviceaccounts/token` cluster-wide
    permission.

    Deploy NRI mode only on dedicated or hardened nodes. Review node
    images regularly for compromise.
