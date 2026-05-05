# Politiques et rôles Vault

**Audience:** Opérateur de plateforme

!!! note
    Cette page couvre le mode NRI + Projected-SA — le chemin recommandé pour les nouveaux déploiements depuis la v3.0. Les politiques du webhook legacy sont documentées dans [operators/legacy-webhook-mode](../operators/legacy-webhook-mode.md).

## Ce que vous allez créer

- Connexion à la base de données et rôle DB sous le moteur `database`
- Politique de l'injector et rôle d'authentification Vault
- Politique du renewer et rôle d'authentification Vault
- Politique du revoker et rôle d'authentification Vault
- Une politique par application et un rôle d'authentification Vault (répétés pour chaque application)

## Backend de base de données

### Connexion à la base de données

```bash
vault write database/config/myapp-postgres \
    plugin_name="postgresql-database-plugin" \
    connection_url="postgresql://{{username}}:{{password}}@db:5432/myapp" \
    allowed_roles="myapp-prod" \
    username="vaultadmin" \
    password="<strong-random>"
```

### Rôle de base de données

```bash
vault write database/roles/myapp-prod \
    db_name="myapp-postgres" \
    creation_statements="CREATE ROLE \"{{name}}\" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}'; GRANT myapp_owner TO \"{{name}}\";" \
    default_ttl="1h" \
    max_ttl="24h"
```

## Politique de l'injector

Créez le fichier de politique `vault-db-injector.hcl` :

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

Appliquez-la :

```bash
vault policy write vault-db-injector vault-db-injector.hcl
```

## Politique du renewer

Créez `vault-db-renewer.hcl` :

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

Appliquez-la :

```bash
vault policy write vault-db-renewer vault-db-renewer.hcl
```

## Politique du revoker

Créez `vault-db-revoker.hcl` :

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

Appliquez-la :

```bash
vault policy write vault-db-revoker vault-db-revoker.hcl
```

## Rôles pour les trois ServiceAccounts de la couche injector

Le chart provisionne trois Kubernetes ServiceAccounts quand `useProjectedSA: true` :

- `vault-db-injector` — le webhook et le plugin NRI
- `vault-db-injector-renewer` — le Deployment renewer
- `vault-db-injector-revoker` — le Deployment revoker

Créez un rôle d'authentification Vault par ServiceAccount :

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

## Configuration par application

Répétez ce bloc pour chaque application nécessitant des identifiants de base de données dynamiques.

### Politique de l'application

Créez `myapp-prod.hcl` :

```hcl
path "database/creds/myapp-prod" {
  capabilities = ["read"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}
```

Appliquez-la :

```bash
vault policy write myapp-prod myapp-prod.hcl
```

### Rôle d'authentification Vault de l'application

```bash
vault write auth/kubernetes/role/myapp-prod \
    bound_service_account_names="myapp" \
    bound_service_account_namespaces="team-myapp" \
    audience="vault" \
    token_policies="myapp-prod" \
    token_type="service" \
    token_period="24h"
```

!!! warning "token_period est obligatoire en mode projected-SA"
    Sans `token_period`, le token du pod expire à `token_max_ttl` et le bail de base de données expire avec lui. L'application perd ses identifiants en pleine session. La métrique `vdbi_projected_role_misconfigured_total{role}` s'incrémente lorsque l'injector détecte un rôle manquant ce champ.

## Vérifier

```bash
vault policy read vault-db-injector
vault read auth/kubernetes/role/vault-db-injector
vault read database/roles/myapp-prod
```

Les trois commandes doivent retourner la configuration que vous avez écrite ci-dessus. Si l'une d'elles retourne une erreur 403 ou une réponse vide, vérifiez la politique de votre propre token Vault.

## Suivant

[Installer l'injector](install-injector.md)
