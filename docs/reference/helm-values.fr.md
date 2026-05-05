# Référence des valeurs Helm

**Audience:** Opérateur de plateforme

Cette page documente chaque clé dans `helm/values.yml`. Le chart Helm provisionne
les trois Deployments (injector, renewer, revoker) et, optionnellement, le DaemonSet NRI
depuis un seul fichier `values.yml`.

## Valeurs minimales pour l'installation canonique NRI + projected-SA

```yaml
vaultDbInjector:
  configuration:
    vaultAddress: "https://vault.example.com:8200"
    vaultAuthPath: "kubernetes"
    kubeRole: "vault-db-injector"
    useProjectedSA: true
    tokenRequestAudiences:
      - vault

nri:
  enabled: true
  pluginIndex: "10"
```

Toutes les autres clés utilisent les valeurs par défaut documentées ci-dessous.

---

## `vaultDbInjector.configuration.*`

Ces clés correspondent directement au fichier de configuration des binaires. Le chart les
rend dans une ConfigMap consommée par les trois Deployments.

| Clé | Défaut | Rôle |
|---|---|---|
| `vaultAddress` | `https://vault1.numberly.in:8200` | URL de base de Vault ou OpenBao |
| `vaultAuthPath` | `kubernetes` | Chemin de mount de la méthode d'authentification Kubernetes |
| `logLevel` | `info` | Niveau de log pour les trois binaires |
| `kubeRole` | `all-rw` | Rôle Vault par défaut pour la connexion des binaires |
| `kubeRoleNri` | `""` | Rôle de substitution pour le plugin NRI. Repli sur `kubeRole` si vide. Utile en mode projected-SA où le plugin NRI dispose d'une politique Vault dédiée. |
| `kubeRoleRenewer` | `""` | Rôle de substitution pour le renewer. Repli sur `kubeRole` si vide. |
| `kubeRoleRevoker` | `""` | Rôle de substitution pour le revoker. Repli sur `kubeRole` si vide. |
| `tokenTTL` | `8766h` | TTL du token périodique demandé à la connexion (environ 1 an) |
| `vaultSecretName` | `vault-injector` | Nom du mount KV-v2 pour les métadonnées par pod |
| `vaultSecretPrefix` | `kubernetes` | Préfixe de chemin à l'intérieur du mount KV |
| `sentry` | `true` | Activer le rapport d'erreurs Sentry |
| `sentryDsn` | `https://your-sentry@sentry/660` | DSN Sentry |
| `webhookFqdn` | `vault-db-injector.numberly.io` | FQDN utilisé dans le nom de service du webhook |
| `webhookMatchLabels` | `vault-db-injector` | Valeur du label `objectSelector` sur la MutatingWebhookConfiguration |
| `injectorLabel` | `vault-db-injector` | Valeur du label pod utilisée pour sélectionner les pods injectés |
| `useProjectedSA` | `false` | Lorsque `true`, l'injector s'authentifie auprès de Vault par pod en utilisant un JWT Kubernetes TokenRequest pour le ServiceAccount du pod admis. Chaque rôle auth/kubernetes de Vault doit avoir `token_period > 0`. Le chart provisionne un ClusterRole accordant `create` sur `serviceaccounts/token`. |
| `tokenRequestAudiences` | `[]` | Audiences définies sur le JWT TokenRequest. Vide = audience par défaut du cluster (compatibilité legacy). Recommandé pour les nouveaux déploiements : `["vault"]` avec une `audience` correspondante sur chaque rôle k8s-auth de Vault. |
| `tokenRequestExpirationSeconds` | `600` | Durée de vie demandée du JWT TokenRequest en secondes. Le kube-apiserver impose un plancher de 600s. |

---

## `vaultDbInjector.injector.*`

Configuration spécifique au Deployment de l'injector.

| Clé | Défaut | Rôle |
|---|---|---|
| `serviceAccountName` | `""` | Remplace le ServiceAccount utilisé par le Deployment de l'injector. Vide = défaut géré par le chart (nom de la release). Définir pour apporter votre propre SA provisionné hors du chart. |
| `args` | `["--config=/injector/config.yaml"]` | Arguments passés au binaire de l'injector |
| `image.repository` | `numberly/vault-db-injector` | Dépôt d'images de conteneur |
| `image.tag` | `2.0.12` | Tag de l'image de conteneur |
| `imagePullPolicy` | `Always` | Politique de récupération d'image |
| `replicas` | `2` | Nombre de répliques du Deployment de l'injector |
| `ports[0].port` | `8443` | Port HTTPS du webhook |
| `ports[0].targetPort` | `8443` | Port cible du conteneur |
| `type` | `ClusterIP` | Type de Service |
| `serviceAccount.annotations` | `{}` | Annotations ajoutées au ServiceAccount de l'injector (ex. pour IRSA/Workload Identity) |
| `containerSecurityContext.allowPrivilegeEscalation` | `false` | Contexte de sécurité |
| `containerSecurityContext.readOnlyRootFilesystem` | `true` | Contexte de sécurité |
| `containerSecurityContext.runAsNonRoot` | `true` | Contexte de sécurité |
| `containerSecurityContext.runAsUser` | `65534` | UID pour le processus de l'injector |
| `containerSecurityContext.runAsGroup` | `65534` | GID pour le processus de l'injector |

---

## `vaultDbInjector.renewer.*`

Configuration spécifique au Deployment du renewer.

| Clé | Défaut | Rôle |
|---|---|---|
| `serviceAccountName` | `""` | Remplace le ServiceAccount. Vide = `<release>` en mode legacy, `<release>-renewer` lorsque `useProjectedSA: true`. |
| `args` | `["--config=/renewer/config.yaml"]` | Arguments passés au binaire du renewer |
| `image.repository` | `numberly/vault-db-injector` | Dépôt d'images de conteneur |
| `image.tag` | `2.0.12` | Tag de l'image de conteneur |
| `imagePullPolicy` | `Always` | Politique de récupération d'image |
| `replicas` | `4` | Nombre de répliques du renewer (l'élection de leader sélectionne l'actif) |
| `containerSecurityContext` | identique à l'injector | Privilèges réduits, non-root |

---

## `vaultDbInjector.revoker.*`

Configuration spécifique au Deployment du revoker.

| Clé | Défaut | Rôle |
|---|---|---|
| `serviceAccountName` | `""` | Remplace le ServiceAccount. Vide = `<release>` en mode legacy, `<release>-revoker` lorsque `useProjectedSA: true`. |
| `args` | `["--config=/revoker/config.yaml"]` | Arguments passés au binaire du revoker |
| `image.repository` | `numberly/vault-db-injector` | Dépôt d'images de conteneur |
| `image.tag` | `2.0.12` | Tag de l'image de conteneur |
| `imagePullPolicy` | `Always` | Politique de récupération d'image |
| `replicas` | `4` | Nombre de répliques du revoker (l'élection de leader sélectionne l'actif) |
| `containerSecurityContext` | identique à l'injector | Privilèges réduits, non-root |

---

## `nri.*`

Configuration pour le DaemonSet NRI (v3.0+).

| Clé | Défaut | Rôle |
|---|---|---|
| `enabled` | `false` | Lorsque `true`, déploie le DaemonSet NRI et indique à l'injector d'encapsuler les identifiants avec des placeholders. Les deux sont liés à ce seul interrupteur pour éviter qu'un cluster se retrouve dans un état où le webhook produit des placeholders mais rien ne les substitue. Nécessite containerd ≥ 1.7 avec NRI activé, ou CRI-O ≥ 1.26. |
| `serviceAccountName` | `""` | Remplace le ServiceAccount pour le DaemonSet NRI. Vide = `<release>`. |
| `image.repository` | `""` | Par défaut : `vaultDbInjector.injector.image.repository` |
| `image.tag` | `""` | Par défaut : `vaultDbInjector.injector.image.tag` |
| `imagePullPolicy` | `Always` | Politique de récupération d'image |
| `resources.requests.cpu` | `50m` | Demande CPU pour chaque pod du plugin NRI |
| `resources.requests.memory` | `64Mi` | Demande mémoire pour chaque pod du plugin NRI |
| `resources.limits.cpu` | `200m` | Limite CPU |
| `resources.limits.memory` | `256Mi` | Limite mémoire |
| `pluginIndex` | `"10"` | Index de priorité du plugin NRI. Doit être unique par instance containerd. L'exécution de plusieurs releases de l'injector sur le même cluster nécessite des valeurs distinctes (ex. `"10"` pour prod, `"11"` pour dev). Le nom du plugin prend automatiquement par défaut le fullname de la release Helm. |
| `tolerations` | `[{operator: Exists}]` | Tolerations pour le DaemonSet NRI. La valeur par défaut tolère toutes les teintes pour que le plugin s'exécute sur chaque nœud. Si un nœud teinté exécute des pods labellisés mais que le plugin est absent, ces pods démarrent avec la chaîne placeholder brute dans l'env — remplacez ceci si vous souhaitez restreindre les nœuds qui exécutent le plugin. |
| `nodeSelector` | `{}` | Sélecteur de nœud pour le DaemonSet NRI |

---

## Projected-SA : rôles Vault que l'opérateur doit créer

Lorsque `useProjectedSA: true`, le chart provisionne des ServiceAccounts dédiés
pour le renewer et le revoker. L'opérateur Vault doit créer les entrées `auth/kubernetes/role` correspondantes :

```
auth/kubernetes/role/<release>-renewer:
  bound_service_account_names = <release>-renewer
  bound_service_account_namespaces = <release-namespace>
  token_policies = <release>-renewer
  # policy: update on auth/token/renew + sys/leases/renew

auth/kubernetes/role/<release>-revoker:
  bound_service_account_names = <release>-revoker
  bound_service_account_namespaces = <release-namespace>
  token_policies = <release>-revoker
  # policy: update on auth/token/revoke-orphan + sys/leases/revoke
```

Voir [getting-started/vault-policies](../getting-started/vault-policies.md) pour
le HCL complet des politiques et les commandes `vault write`.
