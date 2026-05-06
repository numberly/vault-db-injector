# Monitoring

**Audience:** Platform operator

!!! note
    v2.x users — see [migration](migration-v2-to-v3.md) for the metric
    rename. Every metric was renamed from the `vault_injector_*` prefix
    to `vdbi_*` in v3.0.

## Prometheus

The injector, renewer, and revoker each expose a Prometheus endpoint.
The metrics are grouped by lifecycle stage (token / lease renew, revoke,
admission), bookkeeping (KV store/delete), authorization
(`service_account_*`), and v3.0 additions for NRI and projected-SA mode.

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

### v3.0 metrics — NRI mode

| Metric Name                                | Description                                                               | Labels                                |
|--------------------------------------------|---------------------------------------------------------------------------|---------------------------------------|
| `vdbi_nri_substitutions_total`             | Number of CreateContainer events where the NRI plugin emitted an env adjustment | |
| `vdbi_nri_unwrap_failures_total`           | Number of NRI plugin failures resolving credentials at CreateContainer    | `reason`                              |
| `vdbi_nri_resolve_duplicate_total`         | Number of `resolveMapping` calls that hit a concurrent in-flight call (singleflight share). Should stay near 0 in normal operation; spikes indicate concurrent CreateContainer races. | |

### v3.0 metrics — projected-SA mode

| Metric Name                                | Description                                                               | Labels                                |
|--------------------------------------------|---------------------------------------------------------------------------|---------------------------------------|
| `vdbi_token_request_errors_total`          | Number of failed Kubernetes TokenRequest calls (per pod's SA, projected-SA mode) | `reason` (`rbac_denied`, `sa_not_found`, `unauthorized`, `other`) |
| `vdbi_vault_login_errors_total`            | Number of failed Vault logins, classified for triage                      | `reason` (`audience_mismatch`, `sa_not_bound`, `role_not_found`, `vault_sealed`, `permission_denied`, `other`), `auth_mode` (`legacy`, `projected`, `projected_bookkeeping`) |
| `vdbi_projected_role_misconfigured_total`  | Number of times a Vault role used in projected-SA mode was found without `token_period > 0` (pod-token will die at `token_max_ttl`) | `role` |

## Grafana

A reference dashboard ships in the repo at
[`dashboard.json`](https://github.com/numberly/vault-db-injector/blob/main/docs/operators/dashboard.json).
Import it into Grafana via **Dashboards → Import → Upload JSON file**,
point it at your Prometheus data source, and you get panels for token
and lease lifecycle, admission throughput, leader status, and the v3.0
NRI / projected-SA failure breakdowns.

![Grafana dashboard](images/grafana.png)

## Alertmanager

The rules below cover the failure modes that warrant a page: SA
authorization denied, token or lease renewal failures, and
expiration warnings before TTL runs out. Tune `for:` durations and
severity labels to your on-call posture before deploying.

### Service Account Denied

```yaml
- alert: VaultDbInjectorServiceAccountDenied
  annotations:
    description: "Service Account (SA) `{{ $labels.service_account_name }}` in namespace `{{ $labels.exported_namespace }}` was denied access to db_role `{{ $labels.db_role }}` due to `{{ $labels.cause }}` on cluster `{{ $labels.k8s_cluster }}`. Immediate investigation is recommended to ensure proper access controls and service configurations."
    summary: "Service Account `{{ $labels.service_account_name }}` in namespace `{{ $labels.exported_namespace }}` was denied by the injector."
  expr: increase(vdbi_service_account_denied_count{}[2m]) > 0
  for: 1m
  labels:
    severity: critical
```

**Response actions:**

- Verify the service account permissions and roles.
- Check the db_role configurations.
- Investigate the cause of denial.

### Token renewal failure

```yaml
- alert: VaultDbInjectorFailToRenewToken
  annotations:
    description: "VaultDbInjector encountered an error while attempting to renew a token. This might affect the continuous operation of dependent services."
    summary: "VaultDbInjector token renewal failure for namespace `{{ $labels.exported_namespace }}` on cluster `{{ $labels.k8s_cluster }}`."
  expr: increase(vdbi_renew_token_count_error{}[2m]) > 0
  for: 1m
  labels:
    severity: warning
```

**Response actions:**

- Review the injector logs for token-renewal errors.
- Check the Vault policy still allows `auth/token/renew`.
- Look for network issues between the renewer and Vault.

### Lease renewal failure

```yaml
- alert: VaultDbInjectorFailToRenewLease
  annotations:
    description: "VaultDbInjector encountered an error while attempting to renew a lease. Similar to token renewal failures, this can disrupt service operations if not addressed."
    summary: "VaultDbInjector lease renewal failure for namespace `{{ $labels.exported_namespace }}` on cluster `{{ $labels.k8s_cluster }}`."
  expr: increase(vdbi_renew_lease_count_error{}[2m]) > 0
  for: 1m
  labels:
    severity: warning
```

**Response actions:**

- Inspect the renewer logs for lease-renewal errors.
- Confirm the Vault policy allows `sys/leases/renew`.
- Check connectivity to Vault.

### Token expiration warnings

```yaml
- alert: VaultDbInjectorTokenExpirationLessThan14Days
  annotations:
    description: "A token is nearing expiration (less than 2 weeks). Renewing or rotating the token promptly ensures continuous service operation."
    summary: "Token nearing expiration in namespace `{{ $labels.exported_namespace }}` on cluster `{{ $labels.k8s_cluster }}`."
  expr: vdbi_token_expiration - time() < 1209600
  for: 90m
  labels:
    severity: warning

- alert: VaultDbInjectorTokenExpirationLessThan7Days
  annotations:
    description: "A token will expire in less than 7 days. Immediate action is required to renew or rotate the token to avoid service disruption."
    summary: "Urgent: Token expiration warning for namespace `{{ $labels.exported_namespace }}`."
  expr: vdbi_token_expiration - time() < 604800
  for: 5m
  labels:
    severity: critical
```

**Response actions:**

- Identify the service or application using the token.
- Trigger a renewal or rotation.
- Review token policies for alignment with operational requirements.

### Lease expiration warnings

```yaml
- alert: VaultDbInjectorLeaseExpirationLessThan4Days
  annotations:
    description: "A lease is nearing expiration (less than 4 days). Addressing this promptly can prevent potential access issues for services relying on leased credentials or secrets."
    summary: "Lease nearing expiration for namespace `{{ $labels.namespace }}` on cluster `{{ $labels.k8s_cluster }}`."
  expr: vdbi_lease_expiration - time() < 345600
  for: 3m
  labels:
    severity: warning

- alert: VaultDbInjectorLeaseExpirationLessThan1Day
  annotations:
    description: "A lease will expire in less than 1 day. Immediate renewal is critical to maintaining access for the dependent services."
    summary: "Critical: Lease expiration imminent for namespace `{{ $labels.namespace }}`."
  expr: vdbi_lease_expiration - time() < 86400
  for: 3m
  labels:
    severity: critical
```

**Response actions:**

- Identify and renew the leases for the affected services.
- Review lease durations and renewal policies to prevent recurrences.
