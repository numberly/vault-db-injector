# Vault policies and roles

**Audience:** Platform operator

!!! note
    This page covers NRI + Projected-SA mode — the recommended path for new deployments as of v3.0. Legacy webhook policies are documented at [operators/legacy-webhook-mode](../operators/legacy-webhook-mode.md).

## What you will create

- Database connection and DB role under the `database` engine
- Injector policy and Vault auth role
- Renewer policy and Vault auth role
- Revoker policy and Vault auth role
- One per-application policy and Vault auth role (repeated for each app)

## Database backend

### Database connection

```bash
vault write database/config/myapp-postgres \
    plugin_name="postgresql-database-plugin" \
    connection_url="postgresql://{{username}}:{{password}}@db:5432/myapp" \
    allowed_roles="myapp-prod" \
    username="vaultadmin" \
    password="<strong-random>"
```

### Database role

```bash
vault write database/roles/myapp-prod \
    db_name="myapp-postgres" \
    creation_statements="CREATE ROLE \"{{name}}\" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}'; GRANT myapp_owner TO \"{{name}}\";" \
    revocation_statements="REASSIGN OWNED BY \"{{name}}\" TO myapp_owner; DROP OWNED BY \"{{name}}\"; DROP ROLE \"{{name}}\";" \
    default_ttl="1h" \
    max_ttl="24h"
```

`revocation_statements` is mandatory in production. Without it, Vault falls back
to a default `DROP ROLE` that fails as soon as the dynamic role owns objects or
has been granted privileges — leases pile up and revocation queues stall. The
`REASSIGN OWNED` + `DROP OWNED` pair handles both cases idempotently.

## Injector policy

Create the policy file `vault-db-injector.hcl`:

!!! note "Tighten the KV path scope to your cluster"
    The examples below use `vault-db-injector/data/+/+` for readability. In
    production you should pin the first path segment to your cluster name
    (matches `vaultSecretPrefix` in the chart) — for example
    `vault-db-injector/data/kubernetes1-prod-par5/+`. This prevents the injector
    in cluster A from reading or writing bookkeeping owned by cluster B.

```hcl
# KV-v2 bookkeeping (replace vault-db-injector with your vaultSecretName,
# and pin the cluster segment in production — see note above)
path "vault-db-injector/data/+/+" {
  capabilities = ["create", "update"]
}
path "vault-db-injector/metadata/+/+" {
  capabilities = ["create", "update"]
}

# Read app role config (needed at admission to validate the bound SA)
path "auth/kubernetes/role/*" {
  capabilities = ["read"]
}

# Lease lookup (admission-time validation of inherited leases)
path "sys/leases/lookup" {
  capabilities = ["update"]
}

# Health probes
path "sys/health" {
  capabilities = ["read"]
}
```

Apply it:

```bash
vault policy write vault-db-injector vault-db-injector.hcl
```

## Renewer policy

Create `vault-db-renewer.hcl`. The renewer also revokes orphan tokens and leases
during sync passes (when a pod no longer exists), so it needs revoke
capabilities in addition to renew:

```hcl
# KV-v2 bookkeeping — full lifecycle: read pending work, write outcome,
# delete entries for pods that no longer exist
path "vault-db-injector/data/+/+" {
  capabilities = ["create", "read", "update", "delete"]
}
path "vault-db-injector/metadata/+/+" {
  capabilities = ["read", "list", "delete"]
}

# Token renew
path "auth/token/renew" {
  capabilities = ["update"]
}
path "auth/token/lookup" {
  capabilities = ["update"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}

# Lease renew + lookup
path "sys/leases/renew" {
  capabilities = ["update"]
}
path "sys/leases/lookup" {
  capabilities = ["update"]
}

# Revoke orphan tokens and leases for pods that disappeared between
# the admission webhook and the renewer's next sync pass
path "auth/token/revoke-orphan" {
  capabilities = ["update", "sudo"]
}
path "sys/leases/revoke" {
  capabilities = ["update"]
}

path "sys/health" {
  capabilities = ["read"]
}
```

Apply it:

```bash
vault policy write vault-db-renewer vault-db-renewer.hcl
```

## Revoker policy

Create `vault-db-revoker.hcl`:

```hcl
# KV-v2 bookkeeping (read + delete)
path "vault-db-injector/data/+/+" {
  capabilities = ["read", "delete"]
}
path "vault-db-injector/metadata/+/+" {
  capabilities = ["read", "list", "delete"]
}

# Token revoke
path "auth/token/revoke" {
  capabilities = ["update"]
}
path "auth/token/revoke-orphan" {
  capabilities = ["update", "sudo"]
}

# Lease revocation and lookup (safety-net sync)
path "sys/leases/revoke" {
  capabilities = ["update"]
}
path "sys/leases/lookup" {
  capabilities = ["update"]
}

# Self-token renewal
path "auth/token/renew-self" {
  capabilities = ["update"]
}

path "sys/health" {
  capabilities = ["read"]
}
```

Apply it:

```bash
vault policy write vault-db-revoker vault-db-revoker.hcl
```

## NRI plugin policy (optional — only when separating identities)

By default the NRI DaemonSet reuses the **injector** ServiceAccount and the
**injector** Vault auth role — no extra policy is needed. The same
`vault-db-injector.hcl` already covers it.

You only need a separate policy when you set both:

- `nri.serviceAccountName` to a distinct value (e.g. `vault-db-injector-nri`), and
- `vaultDbInjector.configuration.kubeRoleNri` to a matching distinct Vault role

This privilege-separation pattern is useful when you want the webhook (running
as a Deployment, low blast radius) and the NRI plugin (running as a host-level
DaemonSet on every node, larger blast radius) to hold different Vault tokens —
so a host compromise does not yield the webhook's token, and vice-versa.

The NRI plugin's policy is a strict subset of the injector's: it only writes
KV bookkeeping after a credential resolve. It does **not** perform admission,
so it does not need `auth/kubernetes/role/*` or `sys/leases/lookup`.

Create `vault-db-injector-nri.hcl`:

```hcl
# KV-v2 bookkeeping write (NRI plugin records the substitution outcome)
path "vault-db-injector/data/+/+" {
  capabilities = ["create", "update"]
}
path "vault-db-injector/metadata/+/+" {
  capabilities = ["create", "update"]
}

# Self-token renewal (the NRI process keeps its login token alive between
# CreateContainer events)
path "auth/token/renew-self" {
  capabilities = ["update"]
}

path "sys/health" {
  capabilities = ["read"]
}
```

Apply it:

```bash
vault policy write vault-db-injector-nri vault-db-injector-nri.hcl
```

## Roles for the injector-tier ServiceAccounts

The chart provisions three Kubernetes ServiceAccounts when `useProjectedSA: true`:

- `vault-db-injector` — the webhook (and the NRI plugin, by default)
- `vault-db-injector-renewer` — the renewer Deployment
- `vault-db-injector-revoker` — the revoker Deployment

A fourth SA (`vault-db-injector-nri` or any name you set in
`nri.serviceAccountName`) appears only when you opt into NRI privilege
separation as described above.

Create one Vault auth role per ServiceAccount:

```bash
# Injector (also covers the NRI plugin in the default shared-SA setup)
vault write auth/kubernetes/role/vault-db-injector \
    bound_service_account_names="vault-db-injector" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-injector" \
    token_ttl="1h" \
    token_max_ttl="24h"

# Renewer
vault write auth/kubernetes/role/vault-db-injector-renewer \
    bound_service_account_names="vault-db-injector-renewer" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-renewer" \
    token_ttl="1h" \
    token_max_ttl="24h"

# Revoker
vault write auth/kubernetes/role/vault-db-injector-revoker \
    bound_service_account_names="vault-db-injector-revoker" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-revoker" \
    token_ttl="1h" \
    token_max_ttl="24h"

# NRI (only if nri.serviceAccountName + kubeRoleNri are set to distinct values)
vault write auth/kubernetes/role/vault-db-injector-nri \
    bound_service_account_names="vault-db-injector-nri" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-injector-nri" \
    token_ttl="1h" \
    token_max_ttl="24h"
```

## Per-application setup

Repeat this block for each application that needs dynamic database credentials.

### App policy

Create `myapp-prod.hcl`:

```hcl
path "database/creds/myapp-prod" {
  capabilities = ["read"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}
```

Apply it:

```bash
vault policy write myapp-prod myapp-prod.hcl
```

### App Vault auth role

```bash
vault write auth/kubernetes/role/myapp-prod \
    bound_service_account_names="myapp" \
    bound_service_account_namespaces="team-myapp" \
    audience="vault" \
    token_policies="myapp-prod" \
    token_type="service" \
    token_period="24h"
```

!!! warning "token_period is mandatory in projected-SA mode"
    Without `token_period`, the pod-token expires at `token_max_ttl` and the database lease goes with it. The application loses credentials in the middle of a session. The metric `vdbi_projected_role_misconfigured_total{role}` increments when the injector detects a role missing this field.

## Verify

```bash
vault policy read vault-db-injector
vault read auth/kubernetes/role/vault-db-injector
vault read database/roles/myapp-prod
```

All three should return the configuration you wrote above. If any returns a 403 or empty response, check your Vault token's own policy.

## Next

[Install the injector](install-injector.md)
