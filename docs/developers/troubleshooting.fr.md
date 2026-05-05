# Dépannage

**Audience:** Développeur d'application

Symptômes diagnosticables depuis le côté pod. Pour les problèmes de politique Vault,
la configuration du cluster ou les requêtes de métriques, voir
[operators/monitoring](../operators/monitoring.md).

## Le pod démarre mais ne peut pas se connecter à la base de données

| Symptôme | Cause probable | Quoi vérifier |
|---|---|---|
| `authentication failed for user "DBUSER"` | Valeurs par défaut du mode d'annotation utilisées — le nom de la variable d'environnement est `DBUSER`, pas ce que l'application lit | Définir `<dbname>.env-key-dbuser` et `<dbname>.env-key-dbpassword` explicitement |
| `role "DBUSER" does not exist` | Identique au précédent, l'application lit le nom de variable d'environnement par défaut | Vérifier que les noms de variables correspondent à ceux attendus par l'application |
| `connection refused` | Hôte ou port incorrect dans le modèle URI | Vérifier `<dbname>.template` ; s'assurer que le nom d'hôte DB est résolvable depuis l'intérieur du pod |
| `SSL connection required` | L'application se connecte sans TLS, la DB l'exige | Ajouter `?sslmode=require` (ou `verify-full`) à votre modèle URI |
| Les identifiants fonctionnent mais expirent après quelques heures | `token_period` non défini sur le rôle Vault | Demander à votre opérateur de vérifier `vault read auth/kubernetes/role/<role>` — `token_period` doit être non nul |

## L'environnement du pod contient `__VDBI_PH_...`

Ce placeholder est défini par le webhook en mode NRI et doit être
remplacé par le plugin NRI avant le démarrage du processus conteneur.

Si `kubectl exec -- env` affiche toujours la chaîne placeholder plutôt que
des identifiants réels, le plugin NRI n'a pas effectué la substitution.

Causes fréquentes :

- **Pod du DaemonSet du plugin NRI non actif sur le nœud.** Vérifier que le
  DaemonSet dispose d'un pod prêt sur le même nœud que votre pod :
  ```bash
  kubectl -n vault-db-injector get pods -o wide | grep nri
  kubectl get pod myapp -n team-myapp -o wide   # note the NODE
  ```
- **Pod planifié sur un nœud où containerd NRI n'est pas activé.**
  L'opérateur doit activer NRI dans la configuration de containerd sur chaque
  nœud exécutant des charges de travail injectées.
- **Le pod ne porte pas le label correct.** Le plugin NRI filtre les pods
  par le label `vault-db-injector: "true"` (ou l'équivalent configuré par l'opérateur).
  Un pod admis sans ce label reçoit des placeholders qui ne sont jamais substitués.

L'opérateur peut vérifier `vdbi_nri_unwrap_failures_total{reason}` pour voir
pourquoi la substitution a échoué. Voir [operators/monitoring](../operators/monitoring.md)
pour la référence des métriques.

## Pod bloqué en `ContainerCreating` après l'admission

Le plugin NRI s'exécute de façon synchrone lors de la création du conteneur. Si le plugin
échoue à résoudre les identifiants, containerd peut bloquer le démarrage du conteneur.

Causes probables :

- **Échec du login Vault lors de la substitution NRI.** Le plugin s'authentifie
  auprès de Vault en utilisant le ServiceAccount du pod. Si le SA n'est lié à
  aucun rôle Vault, le login est rejeté.
- **ServiceAccount du pod absent de `bound_service_account_names` du rôle Vault.**
  Demander à votre opérateur de vérifier le rôle Vault :
  ```bash
  vault read auth/kubernetes/role/<role-name>
  ```
  Le nom du SA et le namespace doivent apparaître dans la sortie.
- **Pod admis en dehors du sélecteur de namespace du webhook.** Si le pod
  a été créé dans un namespace non couvert par le webhook, le webhook n'a pas
  été appelé, aucun placeholder n'a été défini, et le plugin NRI ignore le
  pod (aucun placeholder à substituer). Le pod devrait démarrer normalement dans
  ce cas — s'il se bloque, cela n'est pas lié à l'injector.

La métrique `vdbi_nri_unwrap_failures_total{reason}` est le signal principal pour
les échecs de substitution NRI. Voir
[operators/monitoring](../operators/monitoring.md) pour le catalogue complet des métriques
et les règles d'alerte suggérées.

## Vérification des annotations définies par le webhook

Après l'admission du pod, inspectez les annotations écrites par le webhook :

```bash
kubectl -n team-myapp get pod myapp -o yaml | grep db-creds-injector
```

Vérifier que :

- Chaque `<dbname>.role` est présent et pointe vers le rôle Vault attendu.
- Chaque `<dbname>.uuid` est défini (écrit par le webhook lors de l'admission). S'il
  est absent, le webhook n'a pas traité le pod.
- Aucune clé d'annotation obsolète ou mal orthographiée n'est présente.

## Messages de rejet du webhook

Les événements du pod contiennent souvent le motif de rejet émis par le webhook :

```bash
kubectl -n team-myapp describe pod myapp | grep -A10 Events
```

Un événement `FailedCreate` ou lié au webhook accompagné d'un message d'erreur Vault
indique que l'injector a retourné une réponse non-200 lors de l'admission.
Transmettez le texte complet de l'événement à votre opérateur.
