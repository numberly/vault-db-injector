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
    revocation_statements="REASSIGN OWNED BY \"{{name}}\" TO myapp_owner; DROP OWNED BY \"{{name}}\"; DROP ROLE \"{{name}}\";" \
    default_ttl="1h" \
    max_ttl="24h"
```

`revocation_statements` est obligatoire en production. Sans cela, Vault retombe
sur un `DROP ROLE` par défaut qui échoue dès que le rôle dynamique possède des
objets ou s'est vu accorder des privilèges — les leases s'accumulent et la file
de révocation se bloque. Le couple `REASSIGN OWNED` + `DROP OWNED` traite les
deux cas de façon idempotente.

## Politique de l'injector

Créez le fichier de politique `vault-db-injector.hcl` :

!!! note "Restreignez le scope du chemin KV à votre cluster"
    Les exemples ci-dessous utilisent `vault-db-injector/data/+/+` pour la
    lisibilité. En production vous devriez figer le premier segment du chemin
    sur le nom de votre cluster (correspond à `vaultSecretPrefix` dans le
    chart) — par exemple `vault-db-injector/data/kubernetes1-prod-par5/+`. Cela
    empêche l'injector du cluster A de lire ou écrire le bookkeeping appartenant
    au cluster B.

```hcl
# KV-v2 bookkeeping (remplacer vault-db-injector par votre vaultSecretName,
# et figer le segment cluster en production — voir la note ci-dessus)
path "vault-db-injector/data/+/+" {
  capabilities = ["create", "update"]
}
path "vault-db-injector/metadata/+/+" {
  capabilities = ["create", "update"]
}

# Lecture de la config du rôle de l'application (nécessaire à l'admission pour
# valider le ServiceAccount lié)
path "auth/kubernetes/role/*" {
  capabilities = ["read"]
}

# Lookup de lease (validation des leases hérités à l'admission)
path "sys/leases/lookup" {
  capabilities = ["update"]
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

Créez `vault-db-renewer.hcl`. Le renewer révoque aussi les tokens orphan et les
leases pendant les passes de synchronisation (quand un pod n'existe plus), il a
donc besoin des capabilities revoke en plus de renew :

```hcl
# KV-v2 bookkeeping — cycle de vie complet : lire le travail à faire, écrire
# le résultat, supprimer les entrées des pods qui n'existent plus
path "vault-db-injector/data/+/+" {
  capabilities = ["create", "read", "update", "delete"]
}
path "vault-db-injector/metadata/+/+" {
  capabilities = ["read", "list", "delete"]
}

# Renouvellement de token
path "auth/token/renew" {
  capabilities = ["update"]
}
path "auth/token/lookup" {
  capabilities = ["update"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}

# Renouvellement et lookup de lease
path "sys/leases/renew" {
  capabilities = ["update"]
}
path "sys/leases/lookup" {
  capabilities = ["update"]
}

# Révocation des tokens orphan et des leases pour les pods qui ont disparu
# entre l'admission par le webhook et la passe de sync suivante du renewer
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

Appliquez-la :

```bash
vault policy write vault-db-renewer vault-db-renewer.hcl
```

## Politique du revoker

Créez `vault-db-revoker.hcl` :

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

Appliquez-la :

```bash
vault policy write vault-db-revoker vault-db-revoker.hcl
```

## Politique du plugin NRI (optionnelle — uniquement en cas de séparation d'identités)

Par défaut, le DaemonSet NRI réutilise le ServiceAccount **injector** et le rôle
d'authentification Vault **injector** — aucune politique supplémentaire n'est
nécessaire. Le même `vault-db-injector.hcl` le couvre déjà.

Vous n'avez besoin d'une politique séparée que si vous définissez les deux à
la fois :

- `nri.serviceAccountName` à une valeur distincte (ex. `vault-db-injector-nri`), et
- `vaultDbInjector.configuration.kubeRoleNri` à un rôle Vault distinct correspondant

Ce pattern de séparation des privilèges est utile lorsque vous voulez que le
webhook (qui tourne en Deployment, surface d'attaque réduite) et le plugin NRI
(qui tourne en DaemonSet host-level sur chaque nœud, surface d'attaque plus
large) détiennent des tokens Vault différents — ainsi une compromission de
nœud ne donne pas accès au token du webhook, et inversement.

La politique du plugin NRI est un sous-ensemble strict de celle de l'injector :
elle écrit uniquement le bookkeeping KV après la résolution d'identifiants.
Elle ne gère **pas** l'admission, donc elle n'a pas besoin de
`auth/kubernetes/role/*` ni de `sys/leases/lookup`.

Créez `vault-db-injector-nri.hcl` :

```hcl
# Écriture du bookkeeping KV-v2 (le plugin NRI enregistre le résultat de la substitution)
path "vault-db-injector/data/+/+" {
  capabilities = ["create", "update"]
}
path "vault-db-injector/metadata/+/+" {
  capabilities = ["create", "update"]
}

# Renouvellement du token (le processus NRI maintient son token de login en vie
# entre les événements CreateContainer)
path "auth/token/renew-self" {
  capabilities = ["update"]
}

path "sys/health" {
  capabilities = ["read"]
}
```

Appliquez-la :

```bash
vault policy write vault-db-injector-nri vault-db-injector-nri.hcl
```

## Rôles pour les ServiceAccounts de la couche injector

Le chart provisionne trois Kubernetes ServiceAccounts quand `useProjectedSA: true` :

- `vault-db-injector` — le webhook (et le plugin NRI, par défaut)
- `vault-db-injector-renewer` — le Deployment renewer
- `vault-db-injector-revoker` — le Deployment revoker

Un quatrième SA (`vault-db-injector-nri` ou tout nom défini dans
`nri.serviceAccountName`) n'apparaît que si vous activez la séparation de
privilèges NRI décrite ci-dessus.

Créez un rôle d'authentification Vault par ServiceAccount :

```bash
# Injector (couvre aussi le plugin NRI dans la configuration partagée par défaut)
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

# NRI (uniquement si nri.serviceAccountName + kubeRoleNri sont définis à des valeurs distinctes)
vault write auth/kubernetes/role/vault-db-injector-nri \
    bound_service_account_names="vault-db-injector-nri" \
    bound_service_account_namespaces="vault-db-injector" \
    audience="vault" \
    token_policies="vault-db-injector-nri" \
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
