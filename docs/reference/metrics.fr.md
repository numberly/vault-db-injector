# Référence des métriques

**Audience:** Opérateur de plateforme

Toutes les métriques utilisent le préfixe `vdbi_` (introduit en v3.0). La v2.x utilisait
`vault_injector_*` — voir [migration §B1](../operators/migration-v2-to-v3.md#b1-noms-de-metriques-vault_injector_-vdbi_)
pour la correspondance complète des renommages.

!!! note
    Utilisateurs v2.x : consultez le guide de migration avant de mettre à jour. Les tableaux
    de bord et les règles d'alerte référençant `vault_injector_*` doivent être mis à jour
    ou vous perdrez immédiatement la visibilité lors de la mise à jour.

---

## Cycle de vie des tokens et leases

| Métrique | Description | Labels |
|---|---|---|
| `vdbi_renew_token_count_success` | Token renouvelé avec succès | `uuid`, `namespace` |
| `vdbi_renew_token_count_error` | Échec du renouvellement du token | `uuid`, `namespace` |
| `vdbi_renew_lease_count_success` | Lease renouvelé avec succès | `uuid`, `namespace` |
| `vdbi_renew_lease_count_error` | Échec du renouvellement du lease | `uuid`, `namespace` |
| `vdbi_revoke_token_count_success` | Token révoqué avec succès | `uuid`, `namespace` |
| `vdbi_revoke_token_count_error` | Échec de la révocation du token | `uuid`, `namespace` |
| `vdbi_token_expiration` | Horodatage d'expiration d'un token | `uuid`, `namespace` |
| `vdbi_lease_expiration` | Horodatage d'expiration d'un lease | `uuid`, `namespace` |
| `vdbi_token_last_renewed` | Horodatage du dernier renouvellement de token réussi | `uuid`, `namespace` |

---

## Admission des pods

| Métrique | Description | Labels |
|---|---|---|
| `vdbi_mutated_pods_success_count` | Pod admis et muté avec succès | — |
| `vdbi_mutated_pods_error_count` | Échec de la mutation lors de l'admission du pod | — |
| `vdbi_fetch_pods_success_count` | Liste des pods récupérée sans erreur | — |
| `vdbi_fetch_pods_error_count` | Échec de la récupération de la liste des pods | — |
| `vdbi_orphan_ticket_created_count_success` | Token orphan créé avec succès (mode legacy) | — |
| `vdbi_orphan_ticket_created_count_error` | Échec de la création du token orphan (mode legacy) | — |

---

## Métadonnées KV

| Métrique | Description | Labels |
|---|---|---|
| `vdbi_store_data_count_success` | Entrée KV écrite avec succès | `uuid`, `namespace` |
| `vdbi_store_data_count_error` | Échec de l'écriture KV | `uuid`, `namespace` |
| `vdbi_delete_data_count_success` | Entrée KV supprimée avec succès | `uuid`, `namespace` |
| `vdbi_delete_data_count_error` | Échec de la suppression KV | `uuid`, `namespace` |

---

## Autorisation

| Métrique | Description | Labels |
|---|---|---|
| `vdbi_service_account_authorized_count` | ServiceAccount autorisé à assumer le rôle de base de données | — |
| `vdbi_service_account_denied_count` | ServiceAccount refusé pour le rôle de base de données | `service_account_name`, `namespace`, `db_role`, `cause` |

---

## Synchronisation

| Métrique | Description | Labels |
|---|---|---|
| `vdbi_synchronization_count_success` | Passe de synchronisation du renewer terminée sans erreur | — |
| `vdbi_synchronization_count_error` | Échec de la passe de synchronisation du renewer | — |
| `vdbi_pod_cleanup_count_success` | Balayage de nettoyage des pods terminé sans erreur | — |
| `vdbi_pod_cleanup_count_error` | Échec du balayage de nettoyage des pods | — |
| `vdbi_last_synchronization_success` | Horodatage de la dernière synchronisation réussie | — |
| `vdbi_last_synchronization_duration` | Durée en secondes de la dernière passe de synchronisation | — |

---

## Connectivité

| Métrique | Description | Labels |
|---|---|---|
| `vdbi_connect_vault_count_success` | Connexion Vault ou renouvellement de token réussi | — |
| `vdbi_connect_vault_count_error` | Échec de la connexion Vault ou du renouvellement de token | — |

---

## Élection de leader

| Métrique | Description | Labels |
|---|---|---|
| `vdbi_is_leader` | `1` si cette réplique est le leader actuel, `0` sinon | `lease_name` |
| `vdbi_leader_election_attempts_total` | Nombre total de tentatives d'acquisition du leadership | `lease_name` |
| `vdbi_leader_election_duration_seconds` | Secondes pendant lesquelles cette instance a détenu le lease de leader | `lease_name`, `leader_name`, `mode` |

---

## Mode NRI

Ces métriques sont émises par le DaemonSet NRI. Elles sont absentes lorsque
`nri.enabled=false`.

| Métrique | Description | Labels |
|---|---|---|
| `vdbi_nri_substitutions_total` | Événements `CreateContainer` où le plugin NRI a émis un ajustement d'env | — |
| `vdbi_nri_unwrap_failures_total` | Échecs du plugin NRI lors de la résolution des identifiants à `CreateContainer` | `reason` |
| `vdbi_nri_resolve_duplicate_total` | Appels `resolveMapping` qui ont touché un appel concurrent en cours (déduplication singleflight). Devrait rester proche de 0 en fonctionnement normal ; les pics indiquent des concurrences de `CreateContainer`. | — |

---

## Mode Projected-SA

Ces métriques sont émises uniquement lorsque `useProjectedSA: true`.

| Métrique | Description | Labels |
|---|---|---|
| `vdbi_token_request_errors_total` | Appels Kubernetes TokenRequest échoués pour le ServiceAccount d'un pod | `reason` (`rbac_denied`, `sa_not_found`, `unauthorized`, `other`) |
| `vdbi_vault_login_errors_total` | Connexions Vault échouées, classifiées pour le triage | `reason` (`audience_mismatch`, `sa_not_bound`, `role_not_found`, `vault_sealed`, `permission_denied`, `other`), `auth_mode` (`legacy`, `projected`, `projected_bookkeeping`) |
| `vdbi_projected_role_misconfigured_total` | Nombre de fois où un rôle Vault en mode projected-SA a été trouvé sans `token_period > 0`. Lorsque cette métrique se déclenche, le pod-token mourra à `token_max_ttl` et les identifiants ne pourront plus être renouvelés. | `role` |
