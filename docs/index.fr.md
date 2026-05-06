# Vault DB Injector

**Audience:** Tout le monde

vault-db-injector émet des identifiants de base de données éphémères depuis HashiCorp Vault (ou OpenBao) et les délivre aux workloads Kubernetes à l'exécution. Il gère le cycle de vie complet : émission, renouvellement, et révocation à la mort du pod.

## Pourquoi ce projet existe

Les identifiants de base de données statiques stockés dans des Kubernetes Secrets constituent un point faible connu : ils ne expirent jamais, fuient via GitOps, et sont lisibles par quiconque peut exécuter `kubectl get secret` dans le namespace. vault-db-injector les remplace par des identifiants qui n'existent que pour la durée de vie du pod, sont rotés par Vault plutôt que par l'application, et apparaissent dans le journal d'audit Vault associés à l'identité du pod.

## Deux modes de livraison

| Mode | Où vivent les identifiants dans Kubernetes | Recommandé pour |
|---|---|---|
| **NRI + Projected-SA** (canonique) | Nulle part — substitués au démarrage du conteneur par un plugin NRI local au nœud. Le PodSpec, etcd, et les journaux d'audit ne voient que des placeholders opaques | Nouveaux déploiements |
| **Webhook + injector-SA** (legacy) | Variables d'environnement en clair dans le PodSpec | Clusters v2.x existants qui n'ont pas encore migré |

Le mode legacy est maintenu pour la compatibilité ascendante. Les nouveaux déploiements doivent suivre le guide [Démarrage](getting-started/overview.md), qui présente le chemin canonique NRI + Projected-SA de bout en bout.

## Choisir votre point d'entrée

- [**Démarrage**](getting-started/overview.md) — installer depuis zéro, dans l'ordre
- [**Pour les développeurs d'application**](developers/annotations.md) — annoter vos pods pour consommer les identifiants injectés
- [**Pour les opérateurs de plateforme**](operators/architecture.md) — opérer, sécuriser, surveiller et migrer l'injector
- [**Pour les contributeurs**](contributors/build-from-source.md) — compiler, tester et contribuer du code

## Compatibilité OpenBao

Toutes les API Vault utilisées par ce projet fonctionnent avec OpenBao sans modification. Pointez `vaultAddress` vers votre instance OpenBao et suivez les mêmes étapes de configuration. Consultez la note OpenBao dans [setup-vault](getting-started/setup-vault.md).

## Licence

Apache-2.0. Voir [`LICENSE`](https://github.com/numberly/vault-db-injector/blob/main/LICENSE).
