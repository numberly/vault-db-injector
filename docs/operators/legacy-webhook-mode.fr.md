# Mode legacy webhook

**Audience:** Opérateur de plateforme

!!! warning "Mode legacy"
    Cette page documente le comportement v2.x conservé dans la v3.0 par
    compatibilité ascendante. Les nouveaux déploiements doivent suivre
    [Getting Started](../getting-started/overview.md), qui décrit le chemin
    canonique NRI + Projected-SA de bout en bout.

En mode legacy, l'injector s'authentifie auprès de Vault avec **son propre**
ServiceAccount, valide l'autorisation du pod en interne via `CanIGetRoles`,
émet ensuite un token orphelin Vault portant la politique du rôle et l'utilise
pour appeler `database/creds/<role>`. Les identifiants atterrissent dans le
PodSpec sous forme de variables d'environnement en clair. Le renewer et le
revoker partagent le ServiceAccount et la politique de l'injector.

## Quand conserver ce mode

- Votre runtime de conteneurs n'expose pas NRI (containerd < 1.7 sans le
  plugin NRI activé, ou CRI-O < 1.26).
- Vous êtes en cours de migration depuis la v2.x et devez fonctionner avec
  `useProjectedSA: false` jusqu'à la mise à jour de vos rôles Vault.
- Vous opérez dans un environnement contraint où le déploiement d'un DaemonSet
  privilégié (le plugin NRI s'exécute en root) n'est pas envisageable et
  l'exposition en clair dans le PodSpec est acceptable pour votre modèle de
  menace.

Dans tous les autres cas, suivez le chemin canonique. NRI + Projected-SA est
la cible recommandée pour la v3.0.

## Configuration

Le mode legacy est le mode par défaut lorsque `useProjectedSA: false` et
`nri.enabled: false`. Chaque binaire lit une configuration YAML sélectionnée
par le flag `--config`.

**Injector** — `--config=/injector/config.yaml` :

```yaml
certFile: /tls/tls.crt
keyFile: /tls/tls.key
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
logLevel: info
kubeRole: vault-db-injector
tokenTTL: 768h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: injector
sentry: false
injectorLabel: vault-db-injector
defaultEngine: database
```

**Renewer** — `--config=/renewer/config.yaml` :

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
logLevel: info
kubeRole: vault-db-injector
tokenTTL: 768h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: renewer
SyncTTLSecond: 300
injectorLabel: vault-db-injector
defaultEngine: database
```

**Revoker** — `--config=/revoker/config.yaml` :

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
logLevel: info
kubeRole: vault-db-injector
tokenTTL: 768h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: revoker
injectorLabel: vault-db-injector
defaultEngine: database
```

## Politiques Vault

Le mode legacy nécessite **une** politique Vault pour l'injector et **un**
rôle Vault sous `auth/kubernetes/role/` lié au ServiceAccount de l'injector.
Le renewer et le revoker partagent la même politique et le même rôle.

### Politique `vault-db-injector` (legacy)

```hcl
# --- KV-v2 bookkeeping ---
path "vault-db-injector/data/+/+" {
  capabilities = ["create", "read", "update", "delete"]
}
path "vault-db-injector/metadata/+/+" {
  capabilities = ["read", "delete", "list"]
}

# --- Vault token operations ---
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

# --- Authorization check (CanIGetRoles) ---
path "auth/kubernetes/role/*" {
  capabilities = ["read"]
}

# --- Database credential issuance ---
path "database/creds/*" {
  capabilities = ["read"]
}

# --- Probes ---
path "sys/health" {
  capabilities = ["read"]
}
```

### Rôle `vault-db-injector` (legacy)

```bash
vault write auth/kubernetes/role/vault-db-injector \
    bound_service_account_names="vault-db-injector" \
    bound_service_account_namespaces="vault-db-injector" \
    token_policies="vault-db-injector" \
    token_ttl="1h" \
    token_max_ttl="24h"
```

La valeur Helm `vaultDbInjector.configuration.kubeRole` doit correspondre à ce
nom de rôle.

### Renewer et revoker

Ils **partagent** le ServiceAccount de l'injector et la même politique
`vault-db-injector`. Aucun objet Vault supplémentaire n'est requis.

## Annotations

Les annotations sont identiques dans tous les modes. Voir la
[référence des annotations](../developers/annotations.md) pour la liste
complète.

## Limitations

- **Identifiants en clair dans le PodSpec** — visibles dans `kubectl get pod
  -o yaml`, dans etcd, dans les journaux d'audit, et dans toute sauvegarde
  GitOps.
- **Rayon d'impact large de l'injector** — un pod injector compromis peut
  émettre des identifiants pour chaque rôle DB configuré sous `database/`.
- **ServiceAccount partagé entre injector / renewer / revoker** — le moindre
  privilège est impossible sans le découpage projected-SA.
- **Pas d'attestation native du pod** — Vault voit le ServiceAccount de
  l'injector, pas celui du pod. Le journal d'audit Vault ne peut pas associer
  l'émission à un pod spécifique sans corréler avec le mont de bookkeeping KV.

Lorsque vous êtes prêt à quitter ce mode, voir
[migration vers la v3.0 avec projected-SA](migration-v2-to-v3.md).
