#  1. <a name='PrometheusMetrics'></a>Prometheus Metrics

Our application exports several Prometheus metrics for monitoring and observability purposes. Below are the details of the available metrics:


| Metric Name                                          | Description                                                               | Labels                                |
|--------------------------------------------------    |---------------------------------------------------------------------------|---------------------------------------|
| `vault_injector_renew_token_count_success`           | Vault injector token renewed with success count                           | `uuid`, `namespace`                   |
| `vault_injector_renew_token_count_error`             | Vault injector token renewed with error count                             | `uuid`, `namespace`                   |
| `vault_injector_renew_lease_count_success`           | Vault injector lease renewed with success count                           | `uuid`, `namespace`                   |
| `vault_injector_renew_lease_count_error`             | Vault injector lease renewed with error count                             | `uuid`, `namespace`                   |
| `vault_injector_revoke_token_count_success`          | Vault injector token revoked with success count                           | `uuid`, `namespace`                   |
| `vault_injector_revoke_token_count_error`            | Vault injector token revoked with error count                             | `uuid`, `namespace`                   |
| `vault_injector_token_expiration`                    | Vault injector expiration time for tokens                                 | `uuid`, `namespace`                   |
| `vault_injector_lease_expiration`                    | Vault injector expiration time for leases                                 | `uuid`, `namespace`                   |
| `vault_injector_token_last_renewed`                  | Last vault token successful renewal                                       | `uuid`, `namespace`                   |
| `vault_injector_synchronization_count_success`       | Vault injector synchronization with success                               |                                       |
| `vault_injector_synchronization_count_error`         | Vault injector synchronization with error                                 |                                       |
| `vault_injector_pod_cleanup_count_success`           | Vault injector PodCleanup with success                                    |                                       |
| `vault_injector_pod_cleanup_count_error`             | Vault injector PodCleanup with error                                      |                                       |
| `vault_injector_last_synchronization_success`        | Last vault token successful renewal                                       |                                       |
| `vault_injector_orphan_ticket_created_count_success` | Vault injector orphan ticket created with success                         |                                       |
| `vault_injector_orphan_ticket_created_count_error`   | Vault injector orphan ticket created with error                           |                                       |
| `vault_injector_store_data_count_success`            | Vault injector data stored with success                                   | `uuid`, `namespace`                   |
| `vault_injector_store_data_count_error`              | Vault injector data stored with error                                     | `uuid`, `namespace`                   |
| `vault_injector_delete_data_count_success`           | Vault injector data delete with success                                   | `uuid`, `namespace`                   |
| `vault_injector_delete_data_count_error`             | Vault injector data deleted with error                                    | `uuid`, `namespace`                   |
| `vault_injector_connect_vault_count_success`         | Vault injector connect to vault with success                              |                                       |
| `vault_injector_connect_vault_count_error`           | Vault injector connect to vault with error                                |                                       |
| `vault_injector_service_account_authorized_count`    | Vault injector service account is authorized to assume dbRole             |                                       |
| `vault_injector_service_account_denied_count`        | Vault injector service account is not authorized to assume dbRole         | `service_account_name`, `namespace`, `db_role`, `cause` |
| `vault_injector_last_synchronization_duration`       | Vault injector last duration of synchronization                           |                                       |
| `vault_injector_is_leader`                           | Return 1 if the vault injector is leader, else 0                          | `lease_name`                          |
| `vault_injector_leader_election_attempts_total`      | Total number of attempts to acquire leadership                            | `lease_name`                          |
| `vault_injector_leader_election_duration_seconds`    | Duration in seconds that this instance has been the leader                | `lease_name`, `leader_name`, `mode`   |
| `vault_injector_fetch_pods_success_count`            | Count that increase when their is no error retrieving pods                |                                       |
| `vault_injector_fetch_pods_error_count`              | Count that increase when their is an error retrieving pods                |                                       |
| `vault_injector_mutated_pods_error_count`            | Count that increase when their is an error mutating pods                  |                                       |
| `vault_injector_mutated_pods_error_count`            | Count that increase when their is an error mutating pods                  |                                       |
