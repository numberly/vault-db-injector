# Configuration : Vault

**Audience:** Opérateur de plateforme

## Vault ou OpenBao

Cette procédure cible HashiCorp Vault ≥ 1.13. Toutes les API utilisées — la méthode d'authentification Kubernetes, KV-v2, et le moteur de secrets de base de données — fonctionnent avec OpenBao ≥ 2.0 sans modification. Pointez `vaultAddress` vers votre instance OpenBao et suivez les mêmes étapes.

## Mounts requis

vault-db-injector utilise trois mounts Vault :

| Mount | Type | Chemin par défaut | Rôle |
|---|---|---|---|
| Moteur de secrets KV-v2 | `kv` (version 2) | `vault-injector` | Comptabilité par pod : IDs de bail, IDs de token, namespace, UUID |
| Moteur de secrets de base de données | `database` | `database` | Émet des identifiants de base de données dynamiques |
| Méthode d'authentification Kubernetes | `kubernetes` | `kubernetes` | Authentifie le ServiceAccount de l'injector et celui de chaque pod |

## Activer les mounts

```bash
vault secrets enable -path=vault-injector -version=2 kv
vault secrets enable database
vault auth enable kubernetes
```

Si un mount existe déjà au chemin cible, Vault retourne `Error enabling: Error making API request` avec le statut 400. Utilisez `vault secrets list` ou `vault auth list` pour vérifier les chemins existants avant d'exécuter ces commandes.

## Configurer la méthode d'authentification Kubernetes

```bash
vault write auth/kubernetes/config \
    kubernetes_host="https://<APISERVER>:6443" \
    kubernetes_ca_cert=@/path/to/ca.crt \
    issuer="https://kubernetes.default.svc.cluster.local"
```

Remplacez `<APISERVER>` par l'adresse que votre instance Vault utilise pour atteindre le serveur API Kubernetes. La valeur `issuer` doit correspondre au flag `--service-account-issuer` du kube-apiserver — `https://kubernetes.default.svc.cluster.local` est la valeur par défaut pour la plupart des distributions.

Pour récupérer le certificat CA depuis le cluster :

```bash
kubectl config view --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' \
    | base64 -d > /tmp/k8s-ca.crt
```

Passez ensuite `/tmp/k8s-ca.crt` comme argument `@` ci-dessus.

## Vérifier

```bash
vault read auth/kubernetes/config
vault secrets list | grep -E '(database|vault-injector)'
```

Résultat attendu : `auth/kubernetes/config` retourne l'hôte et le CA que vous avez configurés. La liste des secrets affiche `database/` et `vault-injector/`.

## Suivant

[Configuration : Base de données](setup-database.md)
