# Comparaison du projet

**Audience:** Tout le monde

Cette page compare vault-db-injector avec d'autres outils qui récupèrent des secrets
depuis Vault et les livrent aux workloads Kubernetes. Elle est placée sous Contributeurs
plutôt que dans Démarrage car la comparaison est surtout utile aux personnes qui ont
déjà décidé d'approfondir — qu'elles évaluent sérieusement le projet ou qu'elles
prévoient de contribuer. Si vous êtes encore en phase d'évaluation préliminaire, cette
page vous donne une vue complète.

## Pourquoi vault-db-injector existe

Nous avons étudié les solutions d'injection Vault existantes avant de construire celle-ci.
Aucune ne correspondait à nos exigences : la plupart se concentraient sur la livraison
générique de secrets plutôt que sur le moteur de base de données Vault spécifiquement,
et plusieurs étaient difficiles à étendre. vault-db-injector n'est pas un remplacement
drop-in d'un outil existant. C'est un outil focalisé, construit autour du moteur de base
de données, du renouvellement automatique des leases, de la révocation des leases à la
suppression des pods, et — en v3.0 — d'une livraison des identifiants qui ne laisse
aucun texte en clair dans le PodSpec.

## Outils comparés

- [Vault Agent Injector](https://developer.hashicorp.com/vault/docs/platform/k8s/injector)
- [Bank Vaults](https://github.com/bank-vaults/bank-vaults)
- [Vals Operator](https://github.com/digitalis-io/vals-operator)
- [Vault CSI Provider](https://developer.hashicorp.com/vault/docs/platform/k8s/csi)

## Nos exigences (par priorité)

1. Gérer nativement le moteur de base de données Vault
2. Injecter les identifiants via des variables d'environnement
3. Configuration simple pour les développeurs d'applications (annotations uniquement)
4. Journalisation d'audit Vault attribuée à l'identité du pod
5. Renouvellement automatique des leases et révocation liés au cycle de vie du pod
6. État inspectable (pour le débogage et la révocation manuelle)
7. Déploiement unique — pas de conteneurs sidecar

## Tableau comparatif

| Fonctionnalité | Vault-Db-Injector | Vault Agent Injector | Bank Vaults (webhook) | Vals Operator | Vault CSI Provider |
|---|---|---|---|---|---|
| **Source des identifiants** | Vault Database Engine | Plusieurs moteurs | Secret engine | Plusieurs moteurs | K/V |
| **Moteur** | Database | All | K/V | Database et K/V | K/V |
| **Méthode d'injection** | Variables d'env du pod | Sidecar / Init container | Init container (en mémoire) | Kubernetes Secrets | Volume CSI |
| **Rotation dynamique des secrets** | Non nécessaire | Yes | Yes | No | Yes |
| **Contrôle d'accès** | Politiques basées sur les rôles | Politiques basées sur les rôles | Politiques basées sur les rôles | Politiques basées sur les rôles | Politiques basées sur les rôles |
| **Complexité de configuration** | Faible | Très élevée | Faible | Modérée | Modérée |
| **Complexité utilisateur** | Faible | Très élevée | Faible | Modérée | Faible |
| **Mode de fonctionnement** | Deployment | Deployment | Deployment | Operator | Operator |
| **Méthode de configuration** | Annotations | Annotations | Via env | CRDs | CRDs |
| **Injection dans l'environnement** | Yes | No | Yes | Yes | Yes (secretRef) |
| **Chiffrement des secrets** | Yes | Yes | Yes | Yes | Yes |
| **Journalisation d'audit** | Yes | Yes | Yes | Yes | Yes |
| **État accessible** | Yes | No | No | No | No |
| **Renouvellement des leases** | Yes | Yes | — | Avec redémarrage du pod | — |
| **Révocation des leases** | Yes | No | — | No | — |
| **Support communautaire** | En croissance | Établi | Modéré | Modéré | Établi |
| **Identifiants invisibles au niveau de l'API K8s (PodSpec / etcd / logs d'audit / GitOps)** | Yes (avec le mode NRI) | No | No | No | No |

### Légende

| Symbole | Signification |
|---|---|
| Yes | Supporté |
| No | Non supporté |
| — | Non applicable |

## Résumé

vault-db-injector se concentre spécifiquement sur le moteur de base de données Vault,
révoque les leases à la suppression des pods (la plupart des alternatives ne le font pas),
et en mode NRI maintient les identifiants entièrement hors de l'API Kubernetes.
