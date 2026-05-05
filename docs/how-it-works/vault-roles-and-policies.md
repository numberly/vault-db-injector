# Vault roles and policies required by vault-db-injector

This page is the reference for everything that must exist on the Vault
side for vault-db-injector to operate. Configuration scales with the
features you enable: legacy mode needs only the injector role; NRI mode
adds nothing on the Vault side; projected-SA mode adds dedicated roles
and policies for the renewer and revoker, and per-app `token_period`
on every k8s-auth role used by injected pods.

The examples assume:
- Helm release name: `vault-db-injector`
- Release namespace: `vault-db-injector`
- Vault k8s-auth mount: `kubernetes`

Adjust to your environment.

---

## 1. Always required — the injector role and its policy

The webhook (and the NRI plugin) authenticates to Vault as the injector
ServiceAccount in legacy mode. In projected-SA mode it still does so for
non-projected paths and for the admission-time role lookup. So the
injector role is **always required**.

### Policy: `vault-db-injector`

```hcl
# Read every database role's config (used by CanIGetRoles in legacy mode).
path "auth/kubernetes/role/*" {
  capabilities = ["read"]
}

# Issue and revoke orphan tokens used to carry per-pod DB credentials
# in legacy mode.
path "auth/token/create-orphan" {
  capabilities = ["update", "sudo"]
}
path "auth/token/revoke-orphan" {
  capabilities = ["update", "sudo"]
}
path "auth/token/revoke" {
  capabilities = ["update"]
}
path "auth/token/renew" {
  capabilities = ["update"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}
path "auth/token/lookup-self" {
  capabilities = ["read"]
}

# Issue dynamic database credentials for any role the injector can use.
# Scope this down to specific paths in production if you can enumerate
# them; the wildcard is shown for clarity.
path "database/creds/*" {
  capabilities = ["read"]
}

# KV writes for lease/token bookkeeping (the renewer/revoker rely on
# this to discover what to renew/revoke). Path follows
# vaultSecretName / vaultSecretPrefix from Helm values.
path "kubernetes/data/+/+" {
  capabilities = ["create", "read", "update", "delete"]
}
path "kubernetes/metadata/+/+" {
  capabilities = ["read", "delete", "list"]
}

# Self health check used by liveness/readiness probes.
path "sys/health" {
  capabilities = ["read"]
}
```

> ⚠️ When you enable projected-SA mode AND complete the cleanup phase
> (drop DB-issuing privileges from the injector), this policy reduces
> drastically. See section 4 below.

### Role: `vault-db-injector`

```bash
vault write auth/kubernetes/role/vault-db-injector \
    bound_service_account_names="vault-db-injector" \
    bound_service_account_namespaces="vault-db-injector" \
    token_policies="vault-db-injector" \
    token_ttl="1h" \
    token_max_ttl="24h"
```

The Helm value `vaultDbInjector.configuration.kubeRole` must equal the
role name (default: `vault-db-injector`).

---

## 2. Per-application role — what user pods consume

For each application that wants dynamic DB credentials, you need:
1. A Vault DB connection + role under `database/`
2. A k8s-auth role telling Vault which pod SA can use it

### Per-app DB role

```bash
# One-time per database backend (Postgres, MySQL, ...)
vault write database/config/myapp-postgres \
    plugin_name="postgresql-database-plugin" \
    connection_url="postgresql://{{username}}:{{password}}@db:5432/myapp" \
    allowed_roles="myapp-prod" \
    username="vaultadmin" \
    password="..."

# One DB role per (app × env)
vault write database/roles/myapp-prod \
    db_name="myapp-postgres" \
    creation_statements="CREATE ROLE \"{{name}}\" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}'; GRANT readonly TO \"{{name}}\";" \
    default_ttl="1h" \
    max_ttl="24h"
```

### Per-app policy

```hcl
# vault-policy: myapp-prod
path "database/creds/myapp-prod" {
  capabilities = ["read"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}
```

### Per-app k8s-auth role

```bash
vault write auth/kubernetes/role/myapp-prod \
    bound_service_account_names="myapp" \
    bound_service_account_namespaces="team-myapp" \
    audience="vault" \
    token_policies="myapp-prod" \
    token_type="service" \
    token_period="24h"
```

> **Mandatory in projected-SA mode**: `token_period > 0`. Without it
> the pod-token expires at `token_max_ttl` and the lease falls with
> it. Use `scripts/vault-set-audience.sh` to bulk-set the audience on
> every existing role at migration time.

---

## 3. Projected-SA mode additions — renewer & revoker

When `useProjectedSA: true`, the chart provisions dedicated Kubernetes
ServiceAccounts (`<release>-renewer`, `<release>-revoker`). You MUST
create matching Vault roles + minimal policies before flipping the flag.

### Renewer policy: `vault-db-renewer`

```hcl
# Renew per-pod tokens issued in projected-SA mode.
path "auth/token/renew" {
  capabilities = ["update"]
}
path "auth/token/lookup" {
  capabilities = ["update"]
}

# Renew underlying DB leases.
path "sys/leases/renew" {
  capabilities = ["update"]
}

# Read KV bookkeeping to find what to renew.
path "kubernetes/data/+/+" {
  capabilities = ["read"]
}
path "kubernetes/metadata/+/+" {
  capabilities = ["read", "list"]
}

path "sys/health" {
  capabilities = ["read"]
}
```

### Revoker policy: `vault-db-revoker`

```hcl
# Revoke per-pod tokens (cascades to their DB leases).
path "auth/token/revoke" {
  capabilities = ["update"]
}
path "auth/token/revoke-orphan" {
  capabilities = ["update", "sudo"]
}

# Direct lease revocation as a fallback when only the lease ID is known.
path "sys/leases/revoke" {
  capabilities = ["update"]
}

# KV bookkeeping: read to discover, delete to clean up.
path "kubernetes/data/+/+" {
  capabilities = ["read", "delete"]
}
path "kubernetes/metadata/+/+" {
  capabilities = ["read", "list", "delete"]
}

path "sys/health" {
  capabilities = ["read"]
}
```

### Vault roles for the renewer/revoker SAs

```bash
vault write auth/kubernetes/role/vault-db-injector-renewer \
    bound_service_account_names="vault-db-injector-renewer" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-renewer" \
    token_ttl="1h" \
    token_max_ttl="24h"

vault write auth/kubernetes/role/vault-db-injector-revoker \
    bound_service_account_names="vault-db-injector-revoker" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-revoker" \
    token_ttl="1h" \
    token_max_ttl="24h"
```

The role names follow the pattern `<helm-release-fullname>-renewer` and
`<helm-release-fullname>-revoker`. Adjust if your release name differs.

---

## 4. Projected-SA cleanup — minimal injector policy

After every cluster runs with `useProjectedSA: true` and you are
confident no rollback is needed, drop DB-issuing privileges from the
injector policy. The minimum it needs in projected-only mode is:

```hcl
# Health probes
path "sys/health" {
  capabilities = ["read"]
}

# Read role config — still needed by the admission-time pre-check
# (CanIGetRoles is skipped in projected mode, but the chart's
#  startup sequence still issues a Vault login)
path "auth/kubernetes/role/*" {
  capabilities = ["read"]
}

# KV bookkeeping the injector itself writes lease metadata to
path "kubernetes/data/+/+" {
  capabilities = ["create", "update"]
}
path "kubernetes/metadata/+/+" {
  capabilities = ["read"]
}
```

Notably absent from this minimal policy:
- `database/creds/*` — the pod-token issues credentials, not the injector
- `auth/token/create-orphan` — no orphan creation in projected mode
- `auth/token/revoke*` — the revoker has its own policy now
- `auth/token/renew*` — same, renewer has its own

Apply this only after the rollout is stable. There is no automation —
flip the policy in your Terraform module / Vault setup script.

---

## 5. Audience migration

When introducing a new audience requirement (recommended for new
deployments: `audience="vault"`), use the helper script to update every
existing k8s-auth role at once:

```bash
export VAULT_ADDR=https://vault.example.com:8200
export VAULT_TOKEN="<a token with list+read+update on auth/kubernetes/role/*>"

# Preview
./scripts/vault-set-audience.sh kubernetes vault --dry-run

# Apply
./scripts/vault-set-audience.sh kubernetes vault
```

The script:
- Lists every role under `auth/<mount>/role/`
- Reads each role's full config
- Re-writes it with the new audience while preserving every other
  field (`bound_service_account_names`, `token_policies`, `token_period`,
  etc.) — `vault write` is CREATE-or-REPLACE, NOT a partial update,
  so the read-then-write flow is mandatory
- Skips roles that already have the desired audience (idempotent)

After applying, set `tokenRequestAudiences: ["vault"]` in your Helm
values to start emitting JWTs that match.

---

## 6. Capability summary by mode

| Capability | Legacy | NRI mode | Projected-SA mode | Projected + cleanup |
|---|---|---|---|---|
| Injector reads `auth/kubernetes/role/*` | ✓ | ✓ | ✓ | ✓ |
| Injector creates orphan tokens | ✓ | ✓ | — | — |
| Injector reads `database/creds/*` | ✓ | ✓ | — | — |
| Injector revokes tokens | ✓ | ✓ | (renewer/revoker only) | — |
| Renewer/revoker have own policies | — | — | ✓ | ✓ |
| Per-app role has `token_period > 0` | optional | optional | **required** | **required** |
| Per-app role has `audience` | optional | optional | recommended | recommended |
