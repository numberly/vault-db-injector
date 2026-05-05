# Metrics reference

**Audience:** Platform operator

All metrics use the `vdbi_` prefix (introduced in v3.0). v2.x used
`vault_injector_*` — see [migration §B1](../operators/migration-v2-to-v3.md#b1-metric-names-vault_injector_-vdbi_)
for the full rename mapping.

!!! note
    v2.x users: check the migration guide before upgrading. Dashboards and
    alert rules referencing `vault_injector_*` must be updated or you will
    lose visibility immediately on upgrade.

---

## Token and lease lifecycle

| Metric | Description | Labels |
|---|---|---|
| `vdbi_renew_token_count_success` | Token renewed successfully | `uuid`, `namespace` |
| `vdbi_renew_token_count_error` | Token renewal failed | `uuid`, `namespace` |
| `vdbi_renew_lease_count_success` | Lease renewed successfully | `uuid`, `namespace` |
| `vdbi_renew_lease_count_error` | Lease renewal failed | `uuid`, `namespace` |
| `vdbi_revoke_token_count_success` | Token revoked successfully | `uuid`, `namespace` |
| `vdbi_revoke_token_count_error` | Token revocation failed | `uuid`, `namespace` |
| `vdbi_token_expiration` | Expiration timestamp for a token | `uuid`, `namespace` |
| `vdbi_lease_expiration` | Expiration timestamp for a lease | `uuid`, `namespace` |
| `vdbi_token_last_renewed` | Timestamp of last successful token renewal | `uuid`, `namespace` |

---

## Pod admission

| Metric | Description | Labels |
|---|---|---|
| `vdbi_mutated_pods_success_count` | Pod admitted and mutated successfully | — |
| `vdbi_mutated_pods_error_count` | Pod admission mutation failed | — |
| `vdbi_fetch_pods_success_count` | Pod list retrieved without error | — |
| `vdbi_fetch_pods_error_count` | Pod list retrieval failed | — |
| `vdbi_orphan_ticket_created_count_success` | Orphan token created successfully (legacy mode) | — |
| `vdbi_orphan_ticket_created_count_error` | Orphan token creation failed (legacy mode) | — |

---

## KV bookkeeping

| Metric | Description | Labels |
|---|---|---|
| `vdbi_store_data_count_success` | KV entry written successfully | `uuid`, `namespace` |
| `vdbi_store_data_count_error` | KV write failed | `uuid`, `namespace` |
| `vdbi_delete_data_count_success` | KV entry deleted successfully | `uuid`, `namespace` |
| `vdbi_delete_data_count_error` | KV delete failed | `uuid`, `namespace` |

---

## Authorization

| Metric | Description | Labels |
|---|---|---|
| `vdbi_service_account_authorized_count` | ServiceAccount authorized to assume DB role | — |
| `vdbi_service_account_denied_count` | ServiceAccount denied for DB role | `service_account_name`, `namespace`, `db_role`, `cause` |

---

## Synchronization

| Metric | Description | Labels |
|---|---|---|
| `vdbi_synchronization_count_success` | Renewer synchronization pass completed without error | — |
| `vdbi_synchronization_count_error` | Renewer synchronization pass failed | — |
| `vdbi_pod_cleanup_count_success` | Pod cleanup sweep completed without error | — |
| `vdbi_pod_cleanup_count_error` | Pod cleanup sweep failed | — |
| `vdbi_last_synchronization_success` | Timestamp of last successful synchronization | — |
| `vdbi_last_synchronization_duration` | Duration in seconds of the last synchronization pass | — |

---

## Connectivity

| Metric | Description | Labels |
|---|---|---|
| `vdbi_connect_vault_count_success` | Vault login or token renewal succeeded | — |
| `vdbi_connect_vault_count_error` | Vault login or token renewal failed | — |

---

## Leader election

| Metric | Description | Labels |
|---|---|---|
| `vdbi_is_leader` | `1` if this replica is the current leader, `0` otherwise | `lease_name` |
| `vdbi_leader_election_attempts_total` | Total attempts to acquire leadership | `lease_name` |
| `vdbi_leader_election_duration_seconds` | Seconds this instance has held the leader lease | `lease_name`, `leader_name`, `mode` |

---

## NRI mode

These metrics are emitted by the NRI DaemonSet. They are absent when
`nri.enabled=false`.

| Metric | Description | Labels |
|---|---|---|
| `vdbi_nri_substitutions_total` | `CreateContainer` events where the NRI plugin emitted an env adjustment | — |
| `vdbi_nri_unwrap_failures_total` | NRI plugin failures resolving credentials at `CreateContainer` | `reason` |
| `vdbi_nri_resolve_duplicate_total` | `resolveMapping` calls that hit a concurrent in-flight call (singleflight dedup). Should stay near 0 in normal operation; spikes indicate concurrent `CreateContainer` races. | — |

---

## Projected-SA mode

These metrics are emitted only when `useProjectedSA: true`.

| Metric | Description | Labels |
|---|---|---|
| `vdbi_token_request_errors_total` | Failed Kubernetes TokenRequest calls for a pod's ServiceAccount | `reason` (`rbac_denied`, `sa_not_found`, `unauthorized`, `other`) |
| `vdbi_vault_login_errors_total` | Failed Vault logins, classified for triage | `reason` (`audience_mismatch`, `sa_not_bound`, `role_not_found`, `vault_sealed`, `permission_denied`, `other`), `auth_mode` (`legacy`, `projected`, `projected_bookkeeping`) |
| `vdbi_projected_role_misconfigured_total` | Times a Vault role in projected-SA mode was found without `token_period > 0`. When this fires, the pod-token will die at `token_max_ttl` and credentials cannot be renewed. | `role` |
