# Installer l'injector

**Audience:** Opérateur de plateforme

## Installation Helm

```bash
helm upgrade --install vault-db-injector ./helm \
  --namespace vault-db-injector \
  --set vaultDbInjector.configuration.vaultAddress=https://vault.example.com:8200 \
  --set vaultDbInjector.configuration.vaultAuthPath=kubernetes \
  --set vaultDbInjector.configuration.kubeRole=vault-db-injector \
  --set vaultDbInjector.configuration.useProjectedSA=true \
  --set vaultDbInjector.configuration.tokenRequestAudiences='{vault}' \
  --set nri.enabled=true \
  --set nri.pluginIndex=10
```

Remplacez `https://vault.example.com:8200` par votre adresse Vault ou OpenBao. Toutes les autres valeurs correspondent aux noms d'exemple utilisés dans [Politiques et rôles Vault](vault-policies.md).

Pour la liste complète des values du chart, des défauts et la documentation
par clé, consultez la [référence des Helm values](../reference/helm-values.md)
— auto-générée depuis `helm/values.yml`.

!!! warning
    Avec `useProjectedSA: true`, `tokenRequestAudiences` doit être non vide. Le binaire refuse de démarrer si cette valeur est vide — cela prévient une dégradation silencieuse de la sécurité où le token d'un pod pourrait être réutilisé entre services.

## Ce que le chart provisionne

Quand `useProjectedSA: true` et `nri.enabled: true`, le chart crée :

| Objet | Nom | Rôle |
|---|---|---|
| ServiceAccount | `vault-db-injector` | Identité du webhook et du plugin NRI de l'injector |
| ServiceAccount | `vault-db-injector-renewer` | Identité du Deployment renewer |
| ServiceAccount | `vault-db-injector-revoker` | Identité du Deployment revoker |
| ClusterRole + binding | `vault-db-injector-token` | Accorde au ServiceAccount de l'injector `create` sur `serviceaccounts/token` (nécessaire pour émettre des JWT TokenRequest par pod) |
| Deployment | `vault-db-injector` | Serveur webhook (2 réplicas par défaut) |
| Deployment | `vault-db-injector-renewer` | Renouvellement périodique des tokens et baux (4 réplicas) |
| Deployment | `vault-db-injector-revoker` | Revoker à surveillance des pods avec balayage de filet de sécurité (4 réplicas) |
| DaemonSet | `vault-db-injector-nri` | Plugin NRI local au nœud (1 pod par nœud) |
| MutatingWebhookConfiguration | `vault-db-injector` | Intercepte les pods avec le label `vault-db-injector: "true"` |

## Vérifier

```bash
kubectl -n vault-db-injector get pods
```

Résultat attendu : 2 pods injector, 4 pods renewer, 4 pods revoker, et 1 pod NRI par nœud — tous `Ready`.

```bash
kubectl -n vault-db-injector logs deployment/vault-db-injector | grep -E "(starting webhook|vault login)"
```

Lignes de sortie attendues :
```
starting webhook server on :8443
vault login successful role=vault-db-injector
```

Si le plugin NRI échoue à s'enregistrer, vérifiez les logs containerd sur le nœud :

```bash
journalctl -u containerd --since "5 minutes ago" | grep nri
```

## Suivant

[Premier pod injecté](first-injected-pod.md)
