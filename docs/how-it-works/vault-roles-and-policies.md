# Vault roles and policies required by vault-db-injector

This page is the canonical reference for everything that must exist on
the Vault side. Follow the section that matches your operating mode:
**legacy** (the only mode in v2.x) or **projected-SA** (recommended in v3.0+).

---

## 0. Vocabulary — two Vault mounts to keep distinct

vault-db-injector touches **two different Vault mounts**. They are easy
to confuse because both are configured by Helm values.

| Vault concept | Helm value | Default | Example |
|---|---|---|---|
| Auth method mount (k8s OIDC login) | `vaultAuthPath` | `kubernetes` | `auth/kubernetes/` |
| KV-v2 secrets engine (lease/token bookkeeping) | `vaultSecretName` | `vault-injector` | `vault-injector/` |
| Path prefix inside the KV mount | `vaultSecretPrefix` | (empty) | `kubernetes1-dv-par5` |
| Auth role used by the injector itself | `kubeRole` | (empty) | `vault-db-injector` |

**Per-mode role overrides**: by default the injector (webhook), NRI
plugin, renewer, and revoker all log in with the same `kubeRole`. Each
can be overridden via Helm values `kubeRoleNri`, `kubeRoleRenewer`,
`kubeRoleRevoker` — recommended in projected-SA mode where the renewer
and revoker have dedicated Vault roles tied to their dedicated
ServiceAccounts.

Throughout this doc, paths are written using these placeholders so you
can mentally substitute the values from your Helm `values.yaml`:

- `auth/<authPath>/` — the k8s auth mount, e.g. `auth/kubernetes/`
- `<kvMount>/` — the KV-v2 mount, e.g. `vault-injector/`

Bookkeeping objects live at:
```
<kvMount>/data/<vaultSecretPrefix>/<podUID>          # KV-v2 data API
<kvMount>/metadata/<vaultSecretPrefix>/<podUID>      # KV-v2 metadata API
```

---

## 1. Legacy mode (`useProjectedSA: false`)

The original v2.x flow. The injector authenticates to Vault with **its
own** ServiceAccount, validates the pod's authorization in-process via
`CanIGetRoles`, then issues a Vault orphan token holding the role's
policy and uses it to call `database/creds/<role>`. The renewer and
revoker share the injector's SA and policy.

### What you need

1. **One Vault policy** for the injector — `vault-db-injector`
2. **One Vault role** under `auth/<authPath>/role/` bound to the injector SA
3. **Per-application** role + policy + DB role (see section 3)

### Policy `vault-db-injector` (legacy mode)

```hcl
# --- KV-v2 bookkeeping (replace <kvMount> with vaultSecretName) ---
# Read/write per-pod metadata: which lease, which token, which pod.
path "<kvMount>/data/+/+" {
  capabilities = ["create", "read", "update", "delete"]
}
path "<kvMount>/metadata/+/+" {
  capabilities = ["read", "delete", "list"]
}

# --- Vault token operations ---
# Create the orphan token that carries the per-pod DB role policy.
path "auth/token/create-orphan" {
  capabilities = ["update", "sudo"]
}
# Revoke that orphan when the pod dies (revoker job, in-flight cleanup).
path "auth/token/revoke-orphan" {
  capabilities = ["update", "sudo"]
}
path "auth/token/revoke" {
  capabilities = ["update"]
}
# Renew the orphan periodically (renewer job).
path "auth/token/renew" {
  capabilities = ["update"]
}
# Renew the injector's own login token in long-running modes.
path "auth/token/renew-self" {
  capabilities = ["update"]
}
path "auth/token/lookup-self" {
  capabilities = ["read"]
}

# --- Authorization check ---
# CanIGetRoles reads each app role's config to verify the pod's SA
# is in bound_service_account_names BEFORE issuing creds.
path "auth/<authPath>/role/*" {
  capabilities = ["read"]
}

# --- Database credential issuance ---
# Issue dynamic credentials. Scope to specific role names in
# production if you can enumerate them.
path "database/creds/*" {
  capabilities = ["read"]
}

# --- Probes ---
path "sys/health" {
  capabilities = ["read"]
}
```

### Role `vault-db-injector` (legacy mode)

```bash
vault write auth/<authPath>/role/vault-db-injector \
    bound_service_account_names="vault-db-injector" \
    bound_service_account_namespaces="vault-db-injector" \
    token_policies="vault-db-injector" \
    token_ttl="1h" \
    token_max_ttl="24h"
```

The Helm value `vaultDbInjector.configuration.kubeRole` must equal this
role name (`vault-db-injector` here).

### Renewer & revoker in legacy mode

They **share** the injector SA and the same `vault-db-injector` policy
above. No additional Vault objects are required.

---

## 2. Projected-SA mode (`useProjectedSA: true`) — full config

Every pod authenticates as itself via Kubernetes TokenRequest, so:
- Vault attests pod identity natively (`bound_service_account_names`)
- The injector no longer needs `database/creds/*` or `create-orphan`
- The renewer and revoker get **dedicated SAs** (provisioned by the chart)
  with **dedicated minimal policies**

You need **four Vault policies** and **three Vault roles** for the
injector tier, plus the per-app roles (section 3).

### 2a. Injector policy — `vault-db-injector` (projected mode, minimal)

The injector itself only needs to:
- Read app roles to know they exist (still used by `authorizeDbAccess`)
- Write KV bookkeeping (the renewer/revoker discover work via this)
- Probe its own health

```hcl
# --- KV-v2 bookkeeping ---
path "<kvMount>/data/+/+" {
  capabilities = ["create", "update"]
}
path "<kvMount>/metadata/+/+" {
  capabilities = ["read"]
}

# --- Read app role config (still needed at admission) ---
path "auth/<authPath>/role/*" {
  capabilities = ["read"]
}

# --- Probes ---
path "sys/health" {
  capabilities = ["read"]
}
```

> Notably **absent** vs. legacy mode: `database/creds/*`,
> `auth/token/create-orphan`, `auth/token/revoke*`, `auth/token/renew*`.
> All credential issuance happens under the per-pod token; renew/revoke
> are owned by the dedicated jobs below.

### 2b. Renewer policy — `vault-db-renewer` (projected mode)

The renewer reads the KV bookkeeping to find pod tokens, then renews
each one (which automatically renews their attached DB lease).

```hcl
# --- KV-v2 bookkeeping (read-only) ---
path "<kvMount>/data/+/+" {
  capabilities = ["read"]
}
path "<kvMount>/metadata/+/+" {
  capabilities = ["read", "list"]
}

# --- Token operations ---
path "auth/token/renew" {
  capabilities = ["update"]
}
path "auth/token/lookup" {
  capabilities = ["update"]
}

# --- Direct lease renewal (fallback when only the lease ID is known) ---
path "sys/leases/renew" {
  capabilities = ["update"]
}

# --- Self-token renewal (long-running CronJob) ---
path "auth/token/renew-self" {
  capabilities = ["update"]
}

path "sys/health" {
  capabilities = ["read"]
}
```

### 2c. Revoker policy — `vault-db-revoker` (projected mode)

The revoker discovers gone pods via KV + Kubernetes API, revokes their
tokens (which cascades to leases), and cleans up the KV entries.

```hcl
# --- KV-v2 bookkeeping (read + delete) ---
path "<kvMount>/data/+/+" {
  capabilities = ["read", "delete"]
}
path "<kvMount>/metadata/+/+" {
  capabilities = ["read", "list", "delete"]
}

# --- Token operations ---
path "auth/token/revoke" {
  capabilities = ["update"]
}
path "auth/token/revoke-orphan" {
  capabilities = ["update", "sudo"]
}

# --- Direct lease revocation (fallback) ---
path "sys/leases/revoke" {
  capabilities = ["update"]
}

# --- Self-token renewal ---
path "auth/token/renew-self" {
  capabilities = ["update"]
}

path "sys/health" {
  capabilities = ["read"]
}
```

### 2d. Roles for the three injector-tier SAs

The chart provisions three Kubernetes ServiceAccounts when
`useProjectedSA: true`:
- `<release>` — the webhook + NRI plugin
- `<release>-renewer` — the renewer Deployment
- `<release>-revoker` — the revoker Deployment

Create one Vault auth role per SA, each bound to its own policy:

```bash
# Injector
vault write auth/<authPath>/role/vault-db-injector \
    bound_service_account_names="vault-db-injector" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-injector" \
    token_ttl="1h" token_max_ttl="24h"

# Renewer
vault write auth/<authPath>/role/vault-db-injector-renewer \
    bound_service_account_names="vault-db-injector-renewer" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-renewer" \
    token_ttl="1h" token_max_ttl="24h"

# Revoker
vault write auth/<authPath>/role/vault-db-injector-revoker \
    bound_service_account_names="vault-db-injector-revoker" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-revoker" \
    token_ttl="1h" token_max_ttl="24h"
```

> The role names follow the pattern `<helm-fullname>` /
> `<helm-fullname>-renewer` / `<helm-fullname>-revoker`. If your Helm
> release name differs, adjust accordingly.
>
> The `audience="vault"` line is recommended (matches Helm
> `tokenRequestAudiences: ["vault"]`). Omit it if you intentionally
> want the apiserver-default audience for legacy compatibility.

---

## 3. Per-application setup (both modes)

For each app that wants dynamic DB credentials you create:
1. A DB connection + role under `database/`
2. A Vault policy granting `read` on that DB role's creds path
3. A k8s-auth role binding the policy to the app's SA

The shape is the same in legacy and projected modes — **only**
`token_period` is mandatory in projected mode (so the pod-token lives
beyond `token_max_ttl`).

### DB connection + role

```bash
# Once per database backend (Postgres, MySQL, ...)
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

### App policy `myapp-prod`

```hcl
path "database/creds/myapp-prod" {
  capabilities = ["read"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}
```

### App k8s-auth role

```bash
vault write auth/<authPath>/role/myapp-prod \
    bound_service_account_names="myapp" \
    bound_service_account_namespaces="team-myapp" \
    audience="vault" \
    token_policies="myapp-prod" \
    token_type="service" \
    token_period="24h"     # MANDATORY in projected-SA mode
```

> **About `token_period`**: in projected mode the same Vault token
> issues the lease and remains the handle for renewal/revocation. If
> the role lacks `token_period`, the token expires at `token_max_ttl`
> and the lease falls with it — apps lose creds in the middle of a
> shift. The metric `vdbi_projected_role_misconfigured_total{role}`
> increments when the injector sees a role without it.

---

## 4. Audience migration helper

If you are switching to projected-SA mode and want to enforce a
specific JWT audience (recommended), use the bundled script to update
every existing k8s-auth role at once:

```bash
export VAULT_ADDR=https://vault.example.com:8200
export VAULT_TOKEN="<a token with list+read+update on auth/<authPath>/role/*>"

# Preview
./scripts/vault-set-audience.sh <authPath> vault --dry-run

# Apply
./scripts/vault-set-audience.sh <authPath> vault
```

The script reads each role's full config and re-writes it with the new
audience while preserving every other field. `vault write` on a role
is CREATE-or-REPLACE, NOT a partial update — the read-then-write flow
is mandatory.

After applying, set `tokenRequestAudiences: ["vault"]` in your Helm
values so the injector emits matching JWTs.

---

## 5. What goes where — quick lookup

| You want to … | Vault path | Capability |
|---|---|---|
| Inject (legacy): create the per-pod orphan token | `auth/token/create-orphan` | `update`, `sudo` |
| Inject (legacy): issue DB creds | `database/creds/*` | `read` |
| Inject (both modes): write KV bookkeeping | `<kvMount>/data/+/+` | `create`, `update` |
| Inject (both modes): read app role config | `auth/<authPath>/role/*` | `read` |
| Renew (legacy or projected): renew a token | `auth/token/renew` | `update` |
| Renew (both modes): read KV to find work | `<kvMount>/data/+/+` | `read` |
| Revoke (legacy or projected): revoke a token | `auth/token/revoke-orphan` | `update`, `sudo` |
| Revoke (both modes): clean up KV | `<kvMount>/data/+/+` | `delete` |
| Pod-side (projected): issue DB creds | `database/creds/<role>` | `read` (in app policy) |
| Pod-side (projected): renew its own token | `auth/token/renew-self` | `update` (in app policy) |

## 6. Mode comparison

| Capability owner | Legacy | Projected-SA |
|---|---|---|
| `auth/token/create-orphan` | injector | — |
| `database/creds/*` (inject path) | injector | per-app (the pod) |
| `auth/token/revoke-orphan` | injector + revoker | revoker only |
| `auth/token/renew` | injector + renewer | renewer only |
| `auth/<authPath>/role/*` read | injector | injector |
| `<kvMount>/data/+/+` write | injector | injector |
| `<kvMount>/data/+/+` read | renewer + revoker | renewer + revoker |
| `<kvMount>/data/+/+` delete | revoker | revoker |
| Per-app role `token_period > 0` | optional | **required** |
| Per-app role `audience` | optional | recommended |
