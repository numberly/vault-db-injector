# Projected ServiceAccount Vault Authentication

**Date**: 2026-05-04
**Status**: Design — pending implementation plan
**Branch (target)**: feat/projected-sa-vault-auth (current branch `feat/ebpf-injection-mode` to be renamed; it actually contains only the NRI plugin work)

## Problem

Today, vault-db-injector authenticates to Vault with its own ServiceAccount and validates pod authorization with an in-process `CanIGetRoles` function:

1. Injector logs in to Vault with its own SA → "parent" Vault token with broad policy.
2. `CanIGetRoles` reads the Vault role config (`bound_service_account_names`, `bound_service_account_namespaces`, `token_policies`) and checks **in application code** whether the pod's SA is allowed.
3. Injector creates a Vault **orphan token** holding only the role's policy, then issues `database/creds/<role>`. The orphan keeps the lease alive independently of the injector's parent token.

Two structural weaknesses:

- **No cryptographic attestation by Vault.** Vault sees the injector, not the pod. Authorization is an application-side check.
- **No real least-privilege for the injector.** Its policy must permit `auth/token/create-orphan` for any DB role policy, so a compromised injector pod can mint credentials for every database the injector knows about.

## Goals

- **A) Native Vault attestation of pod identity.** Vault validates the pod's SA cryptographically (OIDC signature + `bound_service_account_names`). Authorization is enforced by Vault, not by the injector.
- **B) True least-privilege for the injector.** The injector holds no DB-related Vault policy. Credential issuance happens under the pod's identity. A compromised injector cannot issue credentials.
- Preserve the lease lifecycle: pods that already received credentials before the switch must continue to be renewed and revoked normally.

## Non-Goals

- Removing the injector entirely (it still does the orchestration: TokenRequest, Vault login, credential storage, injection).
- Sidecar Vault Agent inside the pod (different model).
- Changing the storage schema of `keysInformation`.

## Approach: TokenRequest API + token_period Vault roles

The injector calls the Kubernetes `TokenRequest` API to obtain a short-lived JWT signed for the pod's ServiceAccount, then logs in to Vault with that JWT. The Vault role is configured with `token_period > 0`, producing a periodic Vault token that survives indefinitely (renewable forever). All credentials issued during the pod's lifetime use this pod-scoped token.

### Why `token_period` and not `create-orphan`

Tokens returned by an auth method login (`auth/kubernetes/login`) are roots of the token tree — no parent, no cascade revocation. The historical need for `CreateOrphanToken` in this codebase comes from the fact that the injector starts from its own (child) login token, not from a fresh per-pod login. By making each pod produce its own login token, the orphan step becomes unnecessary; we only need the token to be **renewable indefinitely**, which is exactly what `token_period` provides.

### Flow (when `useProjectedSA=true`)

```
1. Pod admitted (webhook) or container creating (NRI)
2. Lookup pod info → (podName, namespace, serviceAccountName)
3. k8sClient.RequestSAToken(ns, sa, audiences, ttl) → podJWT
4. NewConnector(addr, authPath, kubeRole=<role>, ..., token=podJWT) + Login
   ↳ Vault validates OIDC signature + bound_service_account_names
   ↳ Vault returns a periodic pod-token (token_period from role config)
5. CanIGetRoles is SKIPPED — Vault has already attested
6. pod-token issues database/creds/<role> → creds + leaseID
7. Store (vaultTokenID=pod-token, leaseID, podUID, namespace, dbRole)
8. Inject creds into the pod (unchanged)
```

When `useProjectedSA=false`: the existing flow is preserved untouched.

### Cohabitation: zero changes for renewer/revoker

The renewer/revoker only need the stored `tokenID`. Both legacy orphan tokens and new pod-tokens are revoked/renewed via `auth/token/revoke-orphan` and `auth/token/renew`, which work uniformly. **No data migration**, no schema change, no conditional code in the renewer/revoker logic.

The renewer/revoker policies, however, can and should be tightened — see Cleanup section.

## Configuration

### Injector config (envconfig + Helm values)

```yaml
vault:
  useProjectedSA: false           # feature flag, default false
  tokenRequest:
    audiences: []                 # empty = cluster-default audience (legacy compat)
                                  # ["vault"] = explicit, recommended for new setups
    expirationSeconds: 60         # short JWT TTL — only used to log in to Vault
```

### Vault role configuration (per DB role)

```hcl
# Policy granted to the pod via the k8s-auth role (NOT to the injector)
path "database/creds/<role>" {
  capabilities = ["read"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}
```

```bash
vault write auth/kubernetes/role/<role> \
    bound_service_account_names="<sa>" \
    bound_service_account_namespaces="<ns>" \
    audience="vault" \
    token_policies="<role-policy>" \
    token_type="service" \
    token_period="24h"
# token_period > 0 ⇒ renewable indefinitely; do NOT set token_max_ttl with period.
```

### Renewer / Revoker (cleanup chantier, included in this work)

Dedicated SAs and policies replace today's broad injector policy:

```hcl
# vault-db-renewer policy
path "auth/token/renew"  { capabilities = ["update"] }
path "sys/leases/renew"  { capabilities = ["update"] }
```

```hcl
# vault-db-revoker policy
path "auth/token/revoke-orphan" { capabilities = ["update"] }
path "sys/leases/revoke"        { capabilities = ["update"] }
```

```bash
vault write auth/kubernetes/role/vault-db-renewer \
    bound_service_account_names="vault-db-renewer" \
    bound_service_account_namespaces="<injector-ns>" \
    token_policies="vault-db-renewer" \
    token_ttl="1h" token_max_ttl="24h"

vault write auth/kubernetes/role/vault-db-revoker \
    bound_service_account_names="vault-db-revoker" \
    bound_service_account_namespaces="<injector-ns>" \
    token_policies="vault-db-revoker" \
    token_ttl="1h" token_max_ttl="24h"
```

### Injector policy when `useProjectedSA=true`

Effectively empty. Health-check only:

```hcl
path "sys/health" { capabilities = ["read"] }
```

### Kubernetes RBAC

Conditionally rendered in the Helm chart when `useProjectedSA=true`:

```yaml
# ClusterRole for vault-db-injector SA
rules:
  - apiGroups: [""]
    resources: ["serviceaccounts/token"]
    verbs: ["create"]
```

`create serviceaccounts/token` is a powerful capability (impersonation of any SA). It is the price of unification across webhook + NRI. Audience constraint at the Vault role level (`audience="vault"`) limits the practical use of those tokens to Vault.

## Component changes

### `pkg/k8s`
- Add `RequestSAToken(ctx, namespace, saName, audiences []string, ttl int64) (string, error)` on `KubernetesClient` interface and implementations. Wraps `clientset.CoreV1().ServiceAccounts(ns).CreateToken(...)`.

### `pkg/config`
- New fields on `VaultConfig`:
  - `UseProjectedSA bool` (envconfig `VAULT_USE_PROJECTED_SA`, default `false`)
  - `TokenRequestAudiences []string` (envconfig `VAULT_TOKEN_REQUEST_AUDIENCES`)
  - `TokenRequestExpirationSeconds int64` (envconfig `VAULT_TOKEN_REQUEST_EXPIRATION_SECONDS`, default `60`)

### `pkg/vault`
- `DbCredentialsRequest` gains `SkipOrphanCreation bool`.
- `GetDbCredentials`: when `SkipOrphanCreation=true`, do not call `CreateOrphanToken`; use the connector's current token (the pod-token from login) directly to call `database/creds/<role>`. The `tokenID` stored is the pod-token.
- No new Vault auth function: existing `Login` already takes `c.k8sSaToken` — pass the pod JWT to `NewConnector` and `Login` works as-is.
- Optional health check: after login in projected mode, lookup-self to verify `period > 0`; warn (do not fail) if 0.

### `pkg/k8smutator/k8smutator.go` (webhook)
Branch on `cfg.UseProjectedSA`:
- `true`:
  - `podJWT := k8sClient.RequestSAToken(...)`
  - `vaultConn := NewConnector(..., token=podJWT)` + `Login`
  - Skip `CanIGetRoles`
  - `GetDbCredentials(req with SkipOrphanCreation=true)`
- `false`: current code path, intact.

### `pkg/nri/vault.go`
Symmetric to webhook in `fetchAndBuildMapping`.

### `pkg/renewer` / `pkg/revoker`
- No logic changes.
- Helm chart change: when cleanup is rolled out, these CronJobs use dedicated SAs (`vault-db-renewer`, `vault-db-revoker`) and Vault roles. Existing `tokenFilePath` lookup is unchanged.

### Helm chart
- Conditional `ClusterRole` for `serviceaccounts/token` (only when `useProjectedSA=true`).
- New SAs and RoleBindings for renewer/revoker (cleanup chantier).
- Documentation snippet inlined in `values.yaml`.

## Error handling

| Failure | Behavior |
|---|---|
| `TokenRequest` fails (apiserver, RBAC, missing SA) | Webhook: deny admission. NRI: fail `CreateContainer`. Metric `injector_token_request_errors_total{reason}`. No silent fallback to legacy. |
| Vault login fails with pod JWT (audience mismatch, SA not bound, role missing) | Return error to caller, metric `injector_vault_login_errors_total{reason,auth_mode}`. |
| `token_period == 0` on Vault role | Warning log + metric `injector_projected_role_misconfigured_total{role}`. Continue (lease will live for a few hours, gives operators time to fix). |
| Lease expires due to renewer not catching up | Pre-existing concern, unchanged. Document `RenewalInterval ≤ token_period / 2`. |

**Boot-time fail-fast**: when `useProjectedSA=true`, on startup, perform a self `RequestSAToken` + Vault login to surface RBAC/Vault misconfiguration before traffic.

## Testing

**Unit**:
- `pkg/k8s`: mock clientset, verify `RequestSAToken` passes correct audiences/TTL.
- `pkg/vault`: `GetDbCredentials` with `SkipOrphanCreation=true` does not call `CreateOrphanToken`.
- `pkg/k8smutator` and `pkg/nri`: table-driven on `useProjectedSA × audiences × CanIGetRoles invocation`.

**Integration**:
- Extend `pkg/vault/handle_token_integration_test.go` with a projected-SA scenario.

**Manual procedure** documented in `docs/how-it-works/projected-sa.md`:
- Configure Vault role with `token_period`.
- Deploy a pod with annotations.
- Verify `vault token lookup <stored-tokenID>` shows policies = `[<dbRole>]` only — not the injector policy.
- Kill the pod, verify revoker tears the lease down.

## Rollout

1. **Vault prep** (no code change): set `token_period` on all roles that will support projected; create renewer/revoker policies + roles.
2. **Code deploy with flag false**: validate no regression. Helm chart does not create the `serviceaccounts/token` ClusterRole.
3. **Per-cluster activation**: flip `useProjectedSA=true` on a non-prod cluster. Verify `vault token lookup` shows only the role policy. Verify pre-existing pods continue to renew/revoke. Roll out across prod clusters progressively.
4. **Injector policy cleanup**: once all clusters run with the flag, drop DB policy from the injector SA. (May land as a separate PR for caution.)

**Success criteria**:
- `vault-db-injector` SA has no `read database/creds/*` and no `update auth/token/create-orphan`.
- Renewer/revoker run with their own SAs and minimal policies.
- Zero regression in renew/revoke success metrics across the rollout.

## Documentation deliverables

- `docs/how-it-works/projected-sa.md`: prerequisites, security model, full Vault config example (role, policy, audience), Helm values, manual verification procedure, troubleshooting (common errors and what they mean).
- `docs/how-it-works/security-model.md` (or update existing): explain the difference between attestation by injector (`CanIGetRoles`) vs by Vault, and the least-privilege guarantee in projected mode.
- `values.yaml` comments referencing the doc.

## Open questions

- Confirm `min-token-expiration-seconds` floor on the target clusters (apiserver flag). Default is 600s; if unchanged, an `expirationSeconds: 60` request will be clamped to 600 by apiserver. Functional but worth knowing for documentation accuracy.
- Whether to keep `CanIGetRoles` for the legacy (`useProjectedSA=false`) flow only — yes, kept untouched there. Removed only on the projected path.
- Branch rename: current branch `feat/ebpf-injection-mode` actually contains only NRI work. Rename to `feat/nri-plugin` (or similar) before this work continues, so the new branch can be `feat/projected-sa-vault-auth` cleanly.
