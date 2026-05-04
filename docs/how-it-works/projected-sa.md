# Projected ServiceAccount Authentication

By default, vault-db-injector authenticates to Vault with its own
ServiceAccount and validates pod authorization with an in-process
check. With `useProjectedSA: true`, the injector instead authenticates
**per pod** using a short-lived JWT requested for the pod's own
ServiceAccount, and Vault performs the authorization check natively.

## What it changes

| Aspect | Default | Projected-SA |
|---|---|---|
| Vault sees | Injector SA | Pod SA |
| Authorization | In-process `CanIGetRoles` | Vault `bound_service_account_names` |
| Injector Vault policy | DB-issuing (broad) | None / health only |
| Token lifecycle | Orphan token via injector | Periodic pod-token |
| Renewer/revoker policy | Same as injector | Dedicated, minimal |

## Prerequisites

### 1. Vault role with `token_period`

Each `auth/kubernetes/role/<X>` consumed by an injected pod **must**
have `token_period` set; otherwise the pod-token (and its DB lease)
expires at `token_max_ttl` and credentials become invalid.

```bash
vault write auth/kubernetes/role/<role> \
    bound_service_account_names="<sa>" \
    bound_service_account_namespaces="<ns>" \
    audience="vault" \
    token_policies="<role-policy>" \
    token_type="service" \
    token_period="24h"
```

The policy attached to `<role-policy>`:

```hcl
path "database/creds/<role>" { capabilities = ["read"] }
path "auth/token/renew-self" { capabilities = ["update"] }
```

### 2. Renewer / revoker policies

```hcl
# vault-db-renewer policy
path "auth/token/renew" { capabilities = ["update"] }
path "sys/leases/renew" { capabilities = ["update"] }
```

```hcl
# vault-db-revoker policy
path "auth/token/revoke-orphan" { capabilities = ["update"] }
path "sys/leases/revoke"        { capabilities = ["update"] }
```

```bash
vault write auth/kubernetes/role/<release>-renewer \
    bound_service_account_names="<release>-renewer" \
    bound_service_account_namespaces="<injector-ns>" \
    token_policies="vault-db-renewer" \
    token_ttl="1h" token_max_ttl="24h"

vault write auth/kubernetes/role/<release>-revoker \
    bound_service_account_names="<release>-revoker" \
    bound_service_account_namespaces="<injector-ns>" \
    token_policies="vault-db-revoker" \
    token_ttl="1h" token_max_ttl="24h"
```

### 3. Helm values

```yaml
vaultDbInjector:
  configuration:
    useProjectedSA: true
    tokenRequestAudiences: ["vault"]   # must match the role's `audience`
    tokenRequestExpirationSeconds: 60
```

The chart automatically:

- Grants the injector SA `create` on `serviceaccounts/token`
- Provisions `<release>-renewer` and `<release>-revoker` SAs with
  matching deployments

## Audience handling

| Role `audience` | `tokenRequestAudiences` | Result |
|---|---|---|
| empty | `[]` | Apiserver-default audience accepted by Vault (legacy compat) |
| `"vault"` | `["vault"]` | Strict cryptographic binding to Vault |
| `"vault"` | `[]` | Vault rejects login (audience mismatch) |
| empty | `["vault"]` | Works but defeats the purpose — set the role audience too |

**Recommendation for new deployments**: configure `audience="vault"` on
the role and `tokenRequestAudiences: ["vault"]` on the chart.

## Verification

After enabling on a cluster:

```bash
# Inspect the token stored in Vault KV for a given pod (path follows
# vaultSecretPrefix / vaultSecretName conventions). Read the tokenID
# field, then look it up:
vault token lookup <stored-tokenID>
# Expected: policies = [<role>], period > 0, ttl renewable
```

A token whose `policies` list contains the injector's broad policy
(rather than the pod's role policy) means the pod is still being
served by the legacy path — recheck `useProjectedSA` in the running
config.

## Migration

1. **Before code rollout**: configure `token_period` on every Vault
   role used by injected pods; create renewer/revoker roles + policies.
2. **Code deploy** with `useProjectedSA: false` (no change in behavior).
3. **Per cluster**, flip `useProjectedSA: true`. New pods use the new
   path; pods already injected continue to be renewed/revoked normally
   — no data migration.
4. **Cleanup** (separate PR): drop DB policy from the injector SA.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `permission denied` on TokenRequest | `useProjectedSA` enabled in values but ClusterRoleBinding missing or not yet applied |
| Vault `invalid role` on login | Pod's SA not present in `bound_service_account_names` |
| Pods lose creds after a few hours | `token_period` not set on the Vault role |
| `audience mismatch` on login | Role `audience` differs from `tokenRequestAudiences` |
| Metric `vdbi_projected_role_misconfigured_total > 0` | A role used in projected-SA mode lacks `token_period` — pod-tokens will die at `token_max_ttl` |

## Security gains

- **Native attestation** by Vault: the audit log shows which pod's SA
  acquired which credentials.
- **Compromised injector** can no longer issue arbitrary DB credentials:
  it has no DB-issuing policy and the pod-token bears the role
  constraint cryptographically.
- **Reduced blast radius**: the only k8s capability the injector still
  needs is `serviceaccounts/token`, scoped by audience.
