# Référence de configuration

**Audience:** Opérateur de plateforme, Contributeur

## Modes binaires

vault-db-injector fournit un binaire unique qui s'exécute dans l'un des trois modes,
sélectionné par la clé `mode` dans le fichier de configuration passé via `--config` :

| Mode | Ce qu'il fait |
|---|---|
| `injector` | Exécute le webhook d'admission mutant. Modifie les PodSpecs au moment de l'admission. |
| `renewer` | Itère périodiquement les entrées KV et renouvelle les tokens et leases avant expiration. |
| `revoker` | Surveille les événements `DELETE` des pods et révoque les tokens et leases Vault. |

Le plugin NRI est intégré dans le binaire `injector` et s'active lorsque
`nri.enabled=true` dans Helm (ce qui définit l'indicateur de configuration approprié).

Chaque binaire lit un fichier de configuration YAML. Les trois partagent le même schéma
de clés ; les clés non pertinentes pour un mode donné sont silencieusement ignorées.

## Référence complète des clés de configuration

| Clé | Type | Défaut | Utilisé par | Rôle |
|---|---|---|---|---|
| `vaultAddress` | string | — | all | URL de base de Vault ou OpenBao (ex. `https://vault.example.com:8200`) |
| `vaultAuthPath` | string | `kubernetes` | all | Chemin de mount de la méthode d'authentification Kubernetes sur Vault |
| `kubeRole` | string | — | all | Rôle auth/kubernetes Vault par défaut pour la connexion du binaire |
| `kubeRoleNri` | string | repli sur `kubeRole` | plugin NRI | Rôle Vault de substitution pour la connexion du plugin NRI à Vault |
| `kubeRoleRenewer` | string | repli sur `kubeRole` | renewer | Rôle Vault de substitution pour la connexion du renewer à Vault |
| `kubeRoleRevoker` | string | repli sur `kubeRole` | revoker | Rôle Vault de substitution pour la connexion du revoker à Vault |
| `tokenTTL` | duration | `8766h` | injector | TTL du token périodique demandé à la connexion |
| `vaultSecretName` | string | `vault-db-injector` | all | Nom du mount KV-v2 utilisé pour les métadonnées par pod |
| `vaultSecretPrefix` | string | `kubernetes` | all | Préfixe de chemin à l'intérieur du mount KV |
| `useProjectedSA` | bool | `false` | injector, NRI | Lorsque `true`, émet un Kubernetes TokenRequest par pod admis et l'utilise pour se connecter à Vault au nom du pod |
| `tokenRequestAudiences` | []string | `[]` | injector, NRI | Audiences définies sur le JWT TokenRequest. Doit être non vide lorsque `useProjectedSA: true` |
| `tokenRequestExpirationSeconds` | int | `600` | injector, NRI | Durée de vie demandée du JWT TokenRequest en secondes (plancher kube-apiserver : 600s) |
| `injectorLabel` | string | `vault-db-injector` | injector, revoker | Valeur du label pod utilisée pour sélectionner les pods injectés |
| `webhookMatchLabels` | string | `vault-db-injector` | injector | Valeur du label `objectSelector` sur la MutatingWebhookConfiguration |
| `mode` | string | — | all | `injector`, `renewer` ou `revoker` |
| `sentry` | bool | `false` | all | Activer le rapport d'erreurs Sentry |
| `sentryDsn` | string | — | all | DSN Sentry |
| `logLevel` | string | `info` | all | Niveau de log (`debug`, `info`, `warn`, `error`) — transmis à logrus |
| `SyncTTLSecond` | int | `300` | renewer | Intervalle en secondes entre les balayages de synchronisation du renewer |
| `defaultEngine` | string | `databases` | injector | Nom du mount du moteur de secrets de base de données Vault par défaut |
| `certFile` | string | `/tls/tls.crt` | injector | Certificat TLS pour le serveur HTTPS du webhook |
| `keyFile` | string | `/tls/tls.key` | injector | Clé privée TLS pour le serveur HTTPS du webhook |

## Exemple : configuration de l'injector

```yaml
certFile: /tls/tls.crt
keyFile: /tls/tls.key
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector
tokenTTL: 8766h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: injector
useProjectedSA: true
tokenRequestAudiences:
  - vault
tokenRequestExpirationSeconds: 600
injectorLabel: vault-db-injector
webhookMatchLabels: vault-db-injector
logLevel: info
sentry: false
```

## Exemple : configuration du renewer

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector-renewer
tokenTTL: 8766h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: renewer
SyncTTLSecond: 300
logLevel: info
sentry: false
```

## Exemple : configuration du revoker

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector-revoker
tokenTTL: 8766h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: revoker
injectorLabel: vault-db-injector
logLevel: info
sentry: false
```

!!! warning
    Lorsque `useProjectedSA: true`, `tokenRequestAudiences` doit être non vide.
    Le binaire refuse de démarrer et consigne une erreur fatale si cette contrainte
    est violée. Définissez au minimum `["vault"]` et configurez une `audience`
    correspondante sur chaque rôle `auth/kubernetes` de Vault.
