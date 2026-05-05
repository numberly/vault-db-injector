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
    default_ttl="1h" \
    max_ttl="24h"
```

## Injector policy

Create the policy file `vault-db-injector.hcl`:

```hcl
# KV-v2 bookkeeping (replace vault-injector with your vaultSecretName)
path "vault-injector/data/+/+" {
  capabilities = ["create", "update"]
}
path "vault-injector/metadata/+/+" {
  capabilities = ["read"]
}

# Read app role config (needed at admission)
path "auth/kubernetes/role/*" {
  capabilities = ["read"]
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

Create `vault-db-renewer.hcl`:

```hcl
# KV-v2 bookkeeping (read-only — discover work)
path "vault-injector/data/+/+" {
  capabilities = ["read"]
}
path "vault-injector/metadata/+/+" {
  capabilities = ["read", "list"]
}

# Token renew
path "auth/token/renew" {
  capabilities = ["update"]
}
path "auth/token/lookup" {
  capabilities = ["update"]
}

# Direct lease renewal
path "sys/leases/renew" {
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
vault policy write vault-db-renewer vault-db-renewer.hcl
```

## Revoker policy

Create `vault-db-revoker.hcl`:

```hcl
# KV-v2 bookkeeping (read + delete)
path "vault-injector/data/+/+" {
  capabilities = ["read", "delete"]
}
path "vault-injector/metadata/+/+" {
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

## Roles for the three injector-tier ServiceAccounts

The chart provisions three Kubernetes ServiceAccounts when `useProjectedSA: true`:

- `vault-db-injector` — the webhook and NRI plugin
- `vault-db-injector-renewer` — the renewer Deployment
- `vault-db-injector-revoker` — the revoker Deployment

Create one Vault auth role per ServiceAccount:

```bash
# Injector
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
