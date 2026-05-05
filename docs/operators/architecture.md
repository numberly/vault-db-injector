# Architecture

**Audience:** Platform operator

## System overview

vault-db-injector ships four cooperating components: the **injector
webhook**, the **NRI plugin DaemonSet**, the **renewer**, and the
**revoker**. They all build from the same Go binary and switch role at
startup via the `mode` config key. They depend on two external
systems: Vault (or OpenBao) for credential issuance and bookkeeping,
and the Kubernetes API server for admission and pod-watch events. The
webhook is the admission gate. The NRI plugin substitutes credentials
into containers at the last moment before runc. The renewer keeps
tokens and leases alive. The revoker tears them down when the pod
dies.

## Diagram

```
                       ┌──────────────────┐
                       │  Vault / OpenBao │
                       │  auth/kubernetes │
                       │  database/...    │
                       │  KV bookkeeping  │
                       └────────┬─────────┘
                                │
        ┌───────────────────────┼─────────────────────┐
        │                       │                     │
   write/read KV           renew tokens         revoke tokens
   issue pod-token          + leases              + leases
        │                       │                     │
┌───────┴─────────┐    ┌────────┴───────┐    ┌────────┴────────┐
│ Injector        │    │  Renewer       │    │  Revoker        │
│ (Deployment)    │    │  (Deployment)  │    │  (Deployment)   │
│ webhook server  │    │  periodic      │    │  pod-watch +    │
│                 │    │  ticker        │    │  safety-net     │
└───────┬─────────┘    └────────────────┘    └─────────────────┘
        │
        │ admit pod (placeholders only in NRI mode)
        │
┌───────┴─────────┐                          ┌──────────────────┐
│  kube-apiserver │◄────── pod-watch ────────│  K8s API events  │
└───────┬─────────┘                          └──────────────────┘
        │
        │ schedule
        ▼
   ┌──────────┐
   │  kubelet │
   └─────┬────┘
         │
         │ /var/run/nri/nri.sock
         ▼
┌────────────────────────┐    on CreateContainer:
│  NRI plugin            │      substitute placeholders
│  (DaemonSet, root,     │      with real DB credentials
│   per node)            │      from Vault
└────────┬───────────────┘
         │
         ▼
       runc → container starts with real envp
```

## Data flow

1. A user pod with the `vault-db-injector: "true"` label is submitted
   to the API server.
2. The webhook intercepts admission. It validates Vault RBAC via
   `CanIGetRoles` (legacy mode) or relies on Vault's native attestation
   at pod-token time (projected mode), then writes either cleartext
   credentials or `__VDBI_PH_<64hex>___` placeholders into the pod's
   env.
3. The pod is scheduled. In NRI mode, the per-node plugin gets a
   `CreateContainer` event before runc, fetches real credentials from
   Vault using a per-pod TokenRequest JWT, and emits a
   `ContainerAdjustment` substituting the placeholders.
4. The container starts with valid credentials in its env.
5. The renewer ticks every 5 minutes (configurable). On each tick the
   leader walks the KV bookkeeping mount and renews each pod-token plus
   its DB lease.
6. When the pod is deleted, the revoker's pod-watch fires. The token
   and lease are revoked, the KV entry is wiped. A periodic safety-net
   sweep catches anything the watch missed.

## Trust boundaries

- The **injector** holds a Vault token scoped to KV bookkeeping plus
  (legacy mode only) `database/creds/*` and `auth/token/create-orphan`.
- In projected mode, the **NRI plugin** does not hold a long-lived
  Vault identity beyond what is needed to call TokenRequest for the
  pod's SA. It logs into Vault per pod, as the pod.
- The **renewer** and **revoker** in projected mode each have a
  dedicated SA bound to a minimal Vault policy: renew-only for the
  renewer, revoke-only for the revoker.

For the per-component view, see [components](components.md). For the
threat model, see [security](security.md).
