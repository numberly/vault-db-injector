# Architecture deep-dive

**Audience:** Contributor

This page walks the codebase at the package level. For the operator-level
overview (components, data flow, trust boundaries), see
[operators/architecture](../operators/architecture.md).

## Package layout

| Package | Role |
|---|---|
| `pkg/injector` | Webhook server and admission logic. Mutates PodSpecs: adds env vars (classic), or placeholder strings (NRI mode). Calls `CanIGetRoles` in legacy mode only. |
| `pkg/nri` | NRI plugin. Registers with containerd at startup. Intercepts `CreateContainer` events and substitutes placeholder env vars with real credentials fetched from the per-node cache. |
| `pkg/renewer` | Periodic renewer. Iterates entries in the KV mount and renews tokens and leases before they expire. Does not revoke (that is the revoker's job). |
| `pkg/revoker` | Pod-watch revoker. Watches for `DELETE` events on pods with the injector label and revokes their Vault token and lease. Also runs a 5-minute safety-net sweep to catch pods missed by the watch. |
| `pkg/vault` | Vault client wrapper. Handles all Vault API calls: KV reads/writes, database credential issuance, token and lease renewal/revocation, projected-SA login via TokenRequest JWT. |
| `pkg/k8s` | Kubernetes client initialization, annotation parsing, ServiceAccount token requests. |
| `pkg/k8smutator` | Webhook mutation logic extracted from `pkg/injector`. Contains the per-admission logic and is separately unit-testable with `cfg.NRI.Enabled` toggled. |
| `pkg/leadership` | Leader election via Kubernetes Lease objects. Renewer and revoker run multi-replica; only the leader does active work. |
| `pkg/healthcheck` | HTTP `/healthz` and `/readyz` handlers. |
| `pkg/metrics` | Prometheus registry and all `vdbi_*` metric definitions. |
| `pkg/config` | Config file parsing and validation (YAML → struct). Shared by all three binaries. |
| `pkg/placeholder` | Placeholder string generation and parsing (`__VDBI_PH_<64hex>___` format). |
| `pkg/controller` | Top-level binary entrypoint logic; wires together config, metrics, health, and the mode-specific component. |
| `pkg/logger` | Logrus wrapper with consistent field conventions. |
| `pkg/sentry` | Sentry error reporter initialization. |

## Key flows

### 1. Admission (webhook → injector)

```
kube-apiserver
    │  POST /mutate (AdmissionReview)
    ▼
pkg/injector (HTTPS :8443)
    │  parse annotations
    │  check pod label: vault-db-injector=true
    │
    ├─ NRI mode (useProjectedSA=true, nri.enabled=true)
    │      issue TokenRequest for pod SA  (pkg/k8s)
    │      login to Vault with JWT         (pkg/vault)
    │      fetch DB creds from database/creds/<role>
    │      write KV entry (uuid → token/lease IDs)
    │      replace env values with placeholder strings
    │      return mutated PodSpec
    │
    └─ Legacy mode
           login with injector SA token
           create orphan token for pod
           fetch DB creds
           write KV entry
           inject plaintext creds into env vars
           return mutated PodSpec
```

### 2. NRI substitution (node-local, at container creation)

```
containerd
    │  CreateContainer(containerConfig)
    ▼
pkg/nri (NRI plugin, DaemonSet)
    │  scan env vars for __VDBI_PH_<hex>___
    │
    │  cache hit?
    │  ├─ yes → substitute placeholder → return adjusted config
    │  └─ no  → read KV entry (pkg/vault)
    │            cache result to /run/<release>/nri/cache.json
    │            substitute placeholder → return adjusted config
    │
    └─ on failure: return error → containerd aborts CreateContainer
                   (pod stays in ContainerCreating, metric incremented)
```

### 3. Revocation (revoker)

```
kube-apiserver
    │  WATCH pods (label: vault-db-injector=true)
    │
    ▼  DELETE event
pkg/revoker
    │  read KV entry for pod UUID      (pkg/vault)
    │  revoke Vault lease              (pkg/vault)
    │  revoke orphan token             (pkg/vault)
    │  delete KV entry                 (pkg/vault)
    │  emit vdbi_revoke_token_count_success / _error
    │
    └─ safety-net sweep (every 5 min)
           list KV entries
           for each: check pod still running (pkg/k8s)
           if not → revoke + delete
```

## Adding a new metric

1. Define the metric in `pkg/metrics/metrics.go` using the `vdbi_` prefix.
2. Register it in `init()` in the same file.
3. Increment/observe it in the relevant package.
4. Add it to `docs/reference/metrics.md` and the appropriate category.
