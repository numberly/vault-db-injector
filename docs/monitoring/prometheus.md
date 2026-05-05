#  1. <a name='PrometheusMetrics'></a>Prometheus Metrics

Our application exports several Prometheus metrics for monitoring and observability purposes. Below are the details of the available metrics:


| Metric Name                                          | Description                                                               | Labels                                |
|--------------------------------------------------    |---------------------------------------------------------------------------|---------------------------------------|
| `vdbi_renew_token_count_success`           | Vault injector token renewed with success count                           | `uuid`, `namespace`                   |
| `vdbi_renew_token_count_error`             | Vault injector token renewed with error count                             | `uuid`, `namespace`                   |
| `vdbi_renew_lease_count_success`           | Vault injector lease renewed with success count                           | `uuid`, `namespace`                   |
| `vdbi_renew_lease_count_error`             | Vault injector lease renewed with error count                             | `uuid`, `namespace`                   |
| `vdbi_revoke_token_count_success`          | Vault injector token revoked with success count                           | `uuid`, `namespace`                   |
| `vdbi_revoke_token_count_error`            | Vault injector token revoked with error count                             | `uuid`, `namespace`                   |
| `vdbi_token_expiration`                    | Vault injector expiration time for tokens                                 | `uuid`, `namespace`                   |
| `vdbi_lease_expiration`                    | Vault injector expiration time for leases                                 | `uuid`, `namespace`                   |
| `vdbi_token_last_renewed`                  | Last vault token successful renewal                                       | `uuid`, `namespace`                   |
| `vdbi_synchronization_count_success`       | Vault injector synchronization with success                               |                                       |
| `vdbi_synchronization_count_error`         | Vault injector synchronization with error                                 |                                       |
| `vdbi_pod_cleanup_count_success`           | Vault injector PodCleanup with success                                    |                                       |
| `vdbi_pod_cleanup_count_error`             | Vault injector PodCleanup with error                                      |                                       |
| `vdbi_last_synchronization_success`        | Last vault token successful renewal                                       |                                       |
| `vdbi_orphan_ticket_created_count_success` | Vault injector orphan ticket created with success                         |                                       |
| `vdbi_orphan_ticket_created_count_error`   | Vault injector orphan ticket created with error                           |                                       |
| `vdbi_store_data_count_success`            | Vault injector data stored with success                                   | `uuid`, `namespace`                   |
| `vdbi_store_data_count_error`              | Vault injector data stored with error                                     | `uuid`, `namespace`                   |
| `vdbi_delete_data_count_success`           | Vault injector data delete with success                                   | `uuid`, `namespace`                   |
| `vdbi_delete_data_count_error`             | Vault injector data deleted with error                                    | `uuid`, `namespace`                   |
| `vdbi_connect_vault_count_success`         | Vault injector connect to vault with success                              |                                       |
| `vdbi_connect_vault_count_error`           | Vault injector connect to vault with error                                |                                       |
| `vdbi_service_account_authorized_count`    | Vault injector service account is authorized to assume dbRole             |                                       |
| `vdbi_service_account_denied_count`        | Vault injector service account is not authorized to assume dbRole         | `service_account_name`, `namespace`, `db_role`, `cause` |
| `vdbi_last_synchronization_duration`       | Vault injector last duration of synchronization                           |                                       |
| `vdbi_is_leader`                           | Return 1 if the vault injector is leader, else 0                          | `lease_name`                          |
| `vdbi_leader_election_attempts_total`      | Total number of attempts to acquire leadership                            | `lease_name`                          |
| `vdbi_leader_election_duration_seconds`    | Duration in seconds that this instance has been the leader                | `lease_name`, `leader_name`, `mode`   |
| `vdbi_fetch_pods_success_count`            | Count that increase when their is no error retrieving pods                |                                       |
| `vdbi_fetch_pods_error_count`              | Count that increase when their is an error retrieving pods                |                                       |
| `vdbi_mutated_pods_success_count`          | Count that increase when a pod is successfully mutated                    |                                       |
| `vdbi_mutated_pods_error_count`            | Count that increase when their is an error mutating pods                  |                                       |

## v3.0 metrics — NRI mode

| Metric Name                                | Description                                                               | Labels                                |
|--------------------------------------------|---------------------------------------------------------------------------|---------------------------------------|
| `vdbi_nri_substitutions_total`             | Number of CreateContainer events where the NRI plugin emitted an env adjustment | |
| `vdbi_nri_unwrap_failures_total`           | Number of NRI plugin failures resolving credentials at CreateContainer    | `reason`                              |
| `vdbi_nri_resolve_duplicate_total`         | Number of `resolveMapping` calls that hit a concurrent in-flight call (singleflight share). Should stay near 0 in normal operation; spikes indicate concurrent CreateContainer races. | |

## v3.0 metrics — projected-SA mode

| Metric Name                                | Description                                                               | Labels                                |
|--------------------------------------------|---------------------------------------------------------------------------|---------------------------------------|
| `vdbi_token_request_errors_total`          | Number of failed Kubernetes TokenRequest calls (per pod's SA, projected-SA mode) | `reason` (`rbac_denied`, `sa_not_found`, `unauthorized`, `other`) |
| `vdbi_vault_login_errors_total`            | Number of failed Vault logins, classified for triage                      | `reason` (`audience_mismatch`, `sa_not_bound`, `role_not_found`, `vault_sealed`, `permission_denied`, `other`), `auth_mode` (`legacy`, `projected`, `projected_bookkeeping`) |
| `vdbi_projected_role_misconfigured_total`  | Number of times a Vault role used in projected-SA mode was found without `token_period > 0` (pod-token will die at `token_max_ttl`) | `role` |
