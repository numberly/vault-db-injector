# Migration de v2.x vers v3.0

**Audience:** Opérateur de plateforme

> ⚠️ **v3.0 est une release avec changements incompatibles.** Lisez l'intégralité
> de ce document avant de mettre à jour. Prévoyez une fenêtre de maintenance si
> vous avez des tableaux de bord ou des alertes qui référencent les anciens noms
> de métriques.

## TL;DR

Trois choses changent entre la v2.x (`main` actuel) et la v3.0 :

1. **Deux nouveaux modes d'injection** s'ajoutent au webhook mutant legacy :
   le **mode plugin NRI** (identifiants résolus à la création du conteneur,
   pas de clair dans le PodSpec), et le **mode auth Vault projected-SA**
   (identité par pod, attestation Vault native, injector au moindre privilège).
   Les deux sont des feature flags opt-in. Le flux legacy du webhook reste
   inchangé quand les flags sont désactivés.
2. **Tous les noms de métriques Prometheus** ont été renommés du préfixe
   `vault_injector_*` vers `vdbi_*`. Les tableaux de bord, règles d'alerte et
   règles d'enregistrement DOIVENT être mis à jour avant la mise à niveau, sans
   quoi vous perdez toute visibilité.
3. **Les valeurs Helm intègrent de nouvelles clés** (`useProjectedSA`,
   `nri.enabled`, etc.) et **provisionnent conditionnellement** de nouveaux RBAC
   et ServiceAccounts. Les valeurs par défaut préservent un comportement
   bit-identique à la v2.x.

---

## Pourquoi migrer

| Capacité | v2.x | v3.0 |
|---|---|---|
| Identité du pod attestée par Vault | ❌ vérification en interne (`CanIGetRoles`) | ✅ native via `bound_service_account_names` |
| Rayon d'impact de l'injector si compromis | large (peut émettre des identifiants pour tout rôle DB) | limité au rôle du pod uniquement (en mode projected) |
| Identifiants au repos dans le PodSpec | variables d'environnement en clair | mode NRI optionnel résout à la création du conteneur, sans clair |
| Politiques renewer / revoker | partagées avec l'injector (larges) | ServiceAccounts dédiés + politiques Vault minimales (en mode projected) |
| Contrainte d'audience sur TokenRequest | n/a | configurable par rôle Vault |
| Observabilité | compteurs + jauge leader | + 4 nouvelles métriques (erreurs TokenRequest, raisons d'erreur de login Vault, mauvaise config de rôle, jauge audience non contrainte) |

**Vous DEVRIEZ migrer si** l'un des cas suivants s'applique :
- Votre modèle de menace traite l'injector comme un point unique de compromission privilégié
- Vous souhaitez que les journaux d'audit Vault attribuent l'émission des identifiants au
  ServiceAccount du pod réel, et non à celui de l'injector
- Vous souhaitez sortir les identifiants du PodSpec en clair (mode NRI)

**Vous pouvez rester en v2.x si** vous êtes satisfait du modèle de confiance actuel
et n'avez pas le temps de mettre à jour tableaux de bord et alertes.

---

## Changements incompatibles (liste complète)

### B1. Noms de métriques — `vault_injector_*` → `vdbi_*`

Chaque métrique a été renommée par substitution de préfixe. Même comportement,
mêmes labels, nouveau nom. Renommage à la volée.

Le tableau de correspondance complet (39 noms de métriques) :

| Nom v2.x | Nom v3.0 |
|---|---|
| `vault_injector_renew_token_count_success` | `vdbi_renew_token_count_success` |
| `vault_injector_renew_token_count_error` | `vdbi_renew_token_count_error` |
| `vault_injector_renew_lease_count_success` | `vdbi_renew_lease_count_success` |
| `vault_injector_renew_lease_count_error` | `vdbi_renew_lease_count_error` |
| `vault_injector_revoke_token_count_success` | `vdbi_revoke_token_count_success` |
| `vault_injector_revoke_token_count_error` | `vdbi_revoke_token_count_error` |
| `vault_injector_token_expiration` | `vdbi_token_expiration` |
| `vault_injector_lease_expiration` | `vdbi_lease_expiration` |
| `vault_injector_synchronization_count_success` | `vdbi_synchronization_count_success` |
| `vault_injector_synchronization_count_error` | `vdbi_synchronization_count_error` |
| `vault_injector_pod_cleanup_count_success` | `vdbi_pod_cleanup_count_success` |
| `vault_injector_pod_cleanup_count_error` | `vdbi_pod_cleanup_count_error` |
| `vault_injector_last_synchronization_success` | `vdbi_last_synchronization_success` |
| `vault_injector_orphan_ticket_created_count_success` | `vdbi_orphan_ticket_created_count_success` |
| `vault_injector_orphan_ticket_created_count_error` | `vdbi_orphan_ticket_created_count_error` |
| `vault_injector_store_data_count_success` | `vdbi_store_data_count_success` |
| `vault_injector_store_data_count_error` | `vdbi_store_data_count_error` |
| `vault_injector_delete_data_count_success` | `vdbi_delete_data_count_success` |
| `vault_injector_delete_data_count_error` | `vdbi_delete_data_count_error` |
| `vault_injector_connect_vault_count_success` | `vdbi_connect_vault_count_success` |
| `vault_injector_connect_vault_count_error` | `vdbi_connect_vault_count_error` |
| `vault_injector_service_account_authorized_count` | `vdbi_service_account_authorized_count` |
| `vault_injector_service_account_denied_count` | `vdbi_service_account_denied_count` |
| `vault_injector_last_synchronization_duration` | `vdbi_last_synchronization_duration` |
| `vault_injector_is_leader` | `vdbi_is_leader` |
| `vault_injector_leader_election_attempts_total` | `vdbi_leader_election_attempts_total` |
| `vault_injector_leader_election_duration_seconds` | `vdbi_leader_election_duration_seconds` |
| `vault_injector_fetch_pods_success_count` | `vdbi_fetch_pods_success_count` |
| `vault_injector_fetch_pods_error_count` | `vdbi_fetch_pods_error_count` |
| `vault_injector_mutated_pods_success_count` | `vdbi_mutated_pods_success_count` |
| `vault_injector_mutated_pods_error_count` | `vdbi_mutated_pods_error_count` |

Nouvelles métriques en v3.0 (sans équivalent v2.x) :

| Nouvelle métrique | Type | Objet |
|---|---|---|
| `vdbi_nri_substitutions_total` | counter | Plugin NRI a substitué l'environnement à CreateContainer |
| `vdbi_nri_unwrap_failures_total{reason}` | counter | Plugin NRI n'a pas pu récupérer un identifiant |
| `vdbi_token_request_errors_total{reason}` | counter | TokenRequest Kubernetes a échoué (mode projected) |
| `vdbi_vault_login_errors_total{reason,auth_mode}` | counter | Login Vault échoué ; `auth_mode` est `legacy` ou `projected` |
| `vdbi_projected_role_misconfigured_total{role}` | counter | Un rôle Vault utilisé en mode projected est sans `token_period > 0` |
| `vdbi_nri_resolve_duplicate_total` | counter | Appels `resolveMapping` concurrents fusionnés via singleflight. Doit rester proche de 0 ; les pics indiquent une race sidecar/main correctement évitée. |

**Migration** : voir "Mise à jour des tableaux de bord et alertes" ci-dessous pour une recette `sed` automatisée.

### B2. Modifications du chart Helm

Le chart crée **toujours** les ServiceAccounts `<release>-renewer` et
`<release>-revoker` et lie les pods renewer/revoker existants à ceux-ci
quand `useProjectedSA: true`. Quand `useProjectedSA: false` (défaut), ces
ServiceAccounts ne sont pas créés et la topologie mono-SA existante est
préservée — bit-identique à la v2.x.

### B3. Ajouts au schéma de configuration

Nouvelles clés sous `vaultDbInjector.configuration` :

```yaml
useProjectedSA: false                    # default false
tokenRequestAudiences: []                # default empty
tokenRequestExpirationSeconds: 600       # default 600s (apiserver minimum)
kubeRoleNri: ""                          # optional override; falls back to kubeRole
kubeRoleRenewer: ""                      # optional override; falls back to kubeRole
kubeRoleRevoker: ""                      # optional override; falls back to kubeRole
```

Plus l'ensemble du bloc `nri:` de niveau supérieur (voir [modèle de sécurité NRI](security.md)).
Les valeurs par défaut gardent toutes les nouvelles fonctionnalités DÉSACTIVÉES.

> ⚠️ **Validation bloquante** : lorsque `useProjectedSA: true` est défini, le
> binaire refuse désormais de démarrer si `tokenRequestAudiences` est vide.
> Une audience vide désactive la contrainte d'usurpation d'identité SA
> cryptographique (tout porteur JWT peut présenter le token de n'importe quel
> ServiceAccount à n'importe quel service qui ne vérifie pas strictement
> l'audience), annulant l'objectif de sécurité du mode projected. Définissez
> `tokenRequestAudiences: ["vault"]` (ou le nom d'audience correspondant)
> avant d'activer le flag.

### B4. CanIGetRoles ignoré en mode projected

Lorsque `useProjectedSA: true`, la vérification `CanIGetRoles` en interne
**n'est pas** invoquée car Vault effectue l'attestation équivalente nativement
au moment du login. En mode legacy (`useProjectedSA: false`), `CanIGetRoles`
est inchangé.

### B5. Double identité Vault en mode projected-SA

En mode projected-SA, l'injector détient deux tokens Vault distincts par
récupération d'identifiants de pod : le pod-token (émis via le TokenRequest du
ServiceAccount projected du pod, utilisé pour `database/creds`) et le token de
bookkeeping (`K8sSaVaultToken`, émis via le ServiceAccount propre de l'injector,
utilisé pour les écritures KV et la gestion des baux). Les chemins de nettoyage
utilisent `conn.GetToken()` pour le pod-token et `conn.K8sSaVaultToken` pour le
token de bookkeeping. Les opérateurs externes et les importateurs hors-arbre de
`pkg/vault` doivent utiliser ces accesseurs ; le champ déprécié `PodVaultToken`
a été supprimé.

### B6. Multi-dbConfiguration en mode NRI désormais fonctionnel

Auparavant, les pods avec plusieurs annotations `db-creds-injector.numberly.io/role-N`
en mode NRI ne voyaient résolue que leur **première** paire d'identifiants
dbConfig ; toutes les autres paires de placeholders restaient non substituées
(l'application plantait avec un placeholder littéral comme mot de passe).

C'est corrigé : le webhook inscrit désormais un UUID par dbConfig dans
l'annotation `db-creds-injector.numberly.io/uuid`, et le plugin NRI itère
tous les dbConfigs en utilisant ces UUIDs comme clés KV distinctes.

**Comportement à la mise à niveau** : les pods admis avant cette mise à niveau
ne portent pas l'annotation UUID. Le plugin NRI retombe sur l'UID du pod pour
le premier dbConfig uniquement (préservant le comportement mono-dbConfig). Les
pods avec plusieurs dbConfigs doivent être relancés après la mise à niveau pour
que l'annotation UUID soit inscrite pour tous les dbConfigs.

### B7. Séparation des responsabilités renewer / revoker (mode projected-SA)

La logique de nettoyage filet-de-sécurité du renewer (révocation + suppression
KV des entrées orphelines pour les pods qui n'existent plus) a été déplacée vers
le revoker sous la forme d'un ticker périodique (intervalle de 5 minutes). Deux
conséquences :

1. La **politique Vault du renewer** est désormais strictement minimale : lecture
   sur KV + `auth/token/renew` + `sys/leases/renew` + `auth/token/renew-self` +
   `sys/health`. Elle n'a notamment plus besoin de `auth/token/revoke-orphan`
   ni de `delete` KV. Si vous avez précédemment accordé la politique plus large
   en suivant une version antérieure de la documentation, il est sûr (et
   recommandé) de révoquer les capacités supplémentaires.

2. La **politique Vault du revoker** nécessite désormais `sys/leases/lookup`
   (utilisé pour récupérer les métadonnées de bail lors du balayage de filet de
   sécurité). Ajoutez cette capacité à votre politique `vault-db-revoker` avant
   la mise à niveau.

Voir [Vault policies](../getting-started/vault-policies.md) §2b (renewer) et §2c (revoker)
pour les blocs de politique exacts.

### B8. Le défaut du chart `vaultSecretName` passe à `vault-db-injector`

Le défaut Helm pour `vaultDbInjector.configuration.vaultSecretName` est passé
de `vault-injector` à `vault-db-injector` pour s'aligner sur le nom de l'outil
lui-même. **Ceci est breaking** pour tout déploiement qui n'overridait pas la
valeur : le chemin du mount KV que l'injector lit et écrit change de
`vault-injector/data/<cluster>/<uuid>` à `vault-db-injector/data/<cluster>/<uuid>`,
et les entrées de bookkeeping existantes deviennent invisibles pour la nouvelle
release.

Deux façons de gérer :

1. **Figer l'ancienne valeur (sans interruption, sans changement Vault)** —
   définissez `vaultDbInjector.configuration.vaultSecretName: vault-injector`
   dans votre fichier de values avant la mise à niveau. Les données existantes
   restent là où elles sont, aucun changement de chemin de policy nécessaire.

2. **Migrer vers le nouveau défaut (opération Vault unique)** — drainez le
   travail en cours, puis déplacez le mount KV avec
   `vault secrets move vault-injector vault-db-injector` et mettez à jour
   chaque politique Vault qui référence `vault-injector/{data,metadata}/...`
   pour pointer vers le nouveau chemin. Prévoyez une brève fenêtre de
   maintenance — les pods admis pendant le move échoueront à écrire le
   bookkeeping.

L'option 1 est le chemin recommandé pour une mise à niveau in-place. L'option 2
s'adresse aux opérateurs qui veulent explicitement la cohérence de nommage
entre l'outil, la release du chart, et le mount KV.

---

## Ce qui ne change PAS

- Toutes les annotations sur les pods utilisateurs (`db-creds-injector.numberly.io/*`).
- La structure KV Vault pour les informations de bail/token stockées.
- Le comportement du renewer / revoker sur les baux existants.
- L'URL du webhook mutant, le bootstrap des certificats, la NetworkPolicy.
- Les valeurs Helm par défaut (webhook legacy + variables d'environnement en clair, sauf si les flags sont activés).

Un cluster v2.x mis à niveau vers la v3.0 **sans modification de valeurs** exécute
exactement le même flux qu'auparavant, avec le même comportement observable
hormis les noms de métriques.

---

## Checklist pré-migration

Avant `helm upgrade` :

- [ ] **Inventaire des tableaux de bord** : lister tous les panneaux Grafana et
  règles Prometheus référençant les noms de métriques `vault_injector_*`.
- [ ] **Inventaire des alertes** : idem pour les règles Alertmanager.
- [ ] **Décider de la topologie cible** pour la v3.0 :
  - Rester sur le webhook legacy ? Vous avez terminé — mettez simplement à jour
    les noms de métriques.
  - Passer au mode NRI ? Lire le [modèle de sécurité NRI](security.md).
    Prérequis cluster : containerd ≥ 1.7 avec NRI activé, ou CRI-O ≥ 1.26.
  - Passer au mode projected-SA ? Lire [Vault policies](../getting-started/vault-policies.md).
    Prérequis Vault : chaque rôle k8s-auth utilisé par les pods injectés doit
    avoir `token_period > 0`, et les rôles Vault `<release>-renewer` /
    `<release>-revoker` dédiés doivent exister avant la mise à niveau du chart.
- [ ] **Prévoir une fenêtre de Rollback** : conserver le chart v2.x et le tag
  d'image épinglés en cas de besoin de revenir en arrière.

---

## Étapes de migration

### Étape 1 — Mettre à jour les tableaux de bord et alertes (AVANT la mise à niveau du chart)

Utilisez l'une des commandes suivantes :

```bash
# Tableaux de bord JSON Grafana
sed -i 's/vault_injector_/vdbi_/g' grafana-dashboards/*.json

# Fichiers de règles Prometheus
sed -i 's/vault_injector_/vdbi_/g' prometheus-rules/*.yml

# Fichiers de règles Alertmanager (quand les alertes incluent la métrique dans l'expr)
sed -i 's/vault_injector_/vdbi_/g' alertmanager-rules/*.yml
```

Rechargez Prometheus et Alertmanager. Vérifiez que les requêtes PromQL dans
Grafana se résolvent toujours (elles retourneront **aucune donnée** jusqu'au
déploiement de la v3.0 ; c'est attendu).

> Note : les séries legacy `vault_injector_*` et les nouvelles `vdbi_*` ne sont
> **pas** émises simultanément dans la v3.0. Il n'y a pas de fenêtre de
> chevauchement. Prévoyez un bref trou d'observabilité pendant le déploiement,
> ou exécutez temporairement les tableaux de bord avec les deux noms
> (`vault_injector_X OR vdbi_X`) pendant la transition.

### Étape 2 — Mettre à niveau le chart avec les flags DÉSACTIVÉS (pas de changement de comportement)

```bash
helm upgrade <release> ./helm/ \
  --reuse-values \
  --version 3.0.0
```

Les valeurs par défaut conservent :
- `vaultDbInjector.configuration.useProjectedSA: false`
- `nri.enabled: false`

Validation :
- Tous les pods atteignent l'état `Ready`.
- Le renewer et le revoker continuent de renouveler/révoquer les baux existants.
- Les nouvelles métriques `vdbi_*` commencent à se peupler.
- Aucun pod existant n'est refusé ni perturbé.

Cette étape est le point le plus sûr pour valider la mise à niveau. En cas de
problème, voir "Rollback".

### Étape 3 (optionnel) — Activer le mode NRI

Si votre cluster remplit les prérequis et que vous souhaitez sortir les
identifiants du PodSpec en clair :

```yaml
nri:
  enabled: true
  pluginIndex: "10"     # must be unique per containerd instance
```

Voir le [modèle de sécurité NRI](security.md) pour la liste complète des
prérequis, le chemin du socket NRI, et les mises en garde par runtime.
Déployez sur un cluster à la fois et validez au moins une injection
d'identifiants de bout en bout avant de continuer.

### Étape 4 (optionnel) — Activer le mode projected-SA

C'est le changement le plus important et il nécessite une préparation côté Vault.
Suivez [Vault policies](../getting-started/vault-policies.md) pas à pas.

En résumé :

1. Pré-Vault : configurer `token_period > 0` sur chaque rôle k8s-auth utilisé
   par les pods injectés. Créer les rôles et politiques Vault `<release>-renewer`
   et `<release>-revoker`.
2. Par cluster, définir `vaultDbInjector.configuration.useProjectedSA: true`
   dans les valeurs. Le chart provisionne alors :
   - Le ClusterRole `serviceaccounts/token` pour le ServiceAccount de l'injector
   - Les ServiceAccounts dédiés `-renewer` et `-revoker` et leurs bindings
   - Les Deployments renewer/revoker basculent vers les ServiceAccounts dédiés
3. Validation : les pods existants continuent de se renouveler normalement ;
   les pods nouvellement admis reçoivent un token Vault dont la liste `policies`
   ne contient que la politique de leur rôle (vérifiable via
   `vault token lookup <stored-tokenID>`).

> ⚠️ **Important** : quand vous activez `useProjectedSA: true`, le chart fait
> immédiatement basculer les Deployments renewer et revoker vers des
> ServiceAccounts dédiés (`<release>-renewer`, `<release>-revoker`).
> Les rôles Vault auth/kubernetes liés à ces ServiceAccounts DOIVENT exister
> AVANT la mise à niveau du chart, sinon les pods renewer/revoker entrent en
> crash-loop au login Vault et les baux existants expirent silencieusement au TTL.
>
> Ordre recommandé :
> 1. Créer les nouveaux rôles et politiques Vault (voir [Vault policies](../getting-started/vault-policies.md) (sections renewer + revoker))
> 2. `helm upgrade` avec `useProjectedSA: true`
> 3. Vérifier que les pods renewer/revoker sont en état Ready
> 4. Optionnel : resserrer la politique legacy de l'injector (voir §4 de ce document)

---

## Rollback

Le chemin legacy est préservé inconditionnellement — le Rollback est simplement un
`helm rollback` :

```bash
helm rollback <release> <previous-revision>
```

Mises en garde :
- Si vous avez renommé les tableaux de bord/alertes (Étape 1) avant de revenir
  en arrière, ils ne verront aucune donnée jusqu'à ce que vous revertiez le
  renommage ou utilisiez des requêtes duales.
- Si vous aviez activé `useProjectedSA: true` (Étape 4) et que les rôles
  Vault-side attendent toujours les ServiceAccounts dédiés renewer/revoker,
  la rétrogradation laisse ces rôles Vault orphelins mais inoffensifs. Nettoyez-les
  à votre convenance.
- Si vous aviez activé le mode NRI (Étape 3) et que les identifiants étaient
  injectés via NRI, ces pods continuent à avoir des identifiants valides (NRI
  n'a pas modifié l'état Vault) — mais ils devront être relancés pour revenir
  au mode variables d'environnement en clair si vous souhaitez le comportement
  legacy.

---

## Dépannage

| Symptôme après mise à niveau | Cause probable |
|---|---|
| Panneau Grafana "no data" | Étape 1 (renommage) omise — les tableaux de bord interrogent toujours `vault_injector_*` |
| Pods renewer en CrashLoop avec `permission denied` Vault | `useProjectedSA: true` mais le rôle Vault `<release>-renewer` n'a pas encore été créé avec `bound_service_account_names: <release>-renewer` |
| `vdbi_token_request_errors_total{reason="rbac_denied"}` augmente | `useProjectedSA: true` mais le ClusterRoleBinding pour `serviceaccounts/token` n'est pas encore appliqué |
| `vdbi_vault_login_errors_total{reason="audience_mismatch"}` | Le rôle Vault a `audience="vault"` mais `tokenRequestAudiences: []` (ou inversement) |
| `vdbi_projected_role_misconfigured_total{role=…} > 0` | Le rôle Vault nommé n'a pas `token_period` ; le pod-token mourra à `token_max_ttl` |
| Le pod injector ne démarre pas avec `tokenRequestAudiences must be set` | `useProjectedSA: true` mais `tokenRequestAudiences: []`. Le chart échoue désormais au démarrage pour éviter une dégradation silencieuse de la sécurité. Définir `tokenRequestAudiences: ["vault"]` (ou votre nom d'audience) dans les valeurs. |
| `vdbi_nri_resolve_duplicate_total > 0` | Des sidecars ou pods multi-conteneurs ont déclenché des `CreateContainer` concurrents. Le plugin déduplique correctement via singleflight, c'est donc informatif uniquement — mais des valeurs persistamment élevées peuvent indiquer un pattern de création de pod intense méritant investigation. |
| Pods du plugin NRI en CrashLoop | Le cluster ne remplit pas les prérequis NRI (containerd < 1.7 sans plugin NRI activé). Voir le [modèle de sécurité NRI](security.md) |

---

## Référence

- [`getting-started/vault-policies.md`](../getting-started/vault-policies.md) — exploration approfondie de l'auth Vault projected-SA
- [`operators/security.md`](security.md) — exploration approfondie du plugin NRI
- [`operators/monitoring.md`](monitoring.md) — référence complète des métriques (noms v3.0)
- [`operators/monitoring.md`](monitoring.md) — exemples de règles d'alerte (noms v3.0)
