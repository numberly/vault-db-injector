<!-- vscode-markdown-toc -->
* 1. [Alerts Configuration](#AlertsConfiguration)
	* 1.1. [Service Account Denied](#ServiceAccountDenied)
	* 1.2. [Token Renewal Failure](#TokenRenewalFailure)
	* 1.3. [Lease Renewal Failure](#LeaseRenewalFailure)
	* 1.4. [Token Expiration Warnings](#TokenExpirationWarnings)
	* 1.5. [Lease Expiration Warnings](#LeaseExpirationWarnings)
* 2. [Conclusion](#Conclusion)

<!-- vscode-markdown-toc-config
	numbering=true
	autoSave=true
	/vscode-markdown-toc-config -->
<!-- /vscode-markdown-toc --># Alertmanager Configuration for VaultDb Injector

This configuration defines a set of alerts for monitoring the VaultDb Injector within a Kubernetes environment. Each alert is designed to notify the team of potential issues that could impact the availability, security, or functionality of the services relying on Vault for secret management.

##  1. <a name='AlertsConfiguration'></a>Alerts Configuration

###  1.1. <a name='ServiceAccountDenied'></a>Service Account Denied

```yaml
- alert: VaultDbInjectorServiceAccountDenied
  annotations:
    description: "Service Account (SA) `{{ $labels.service_account_name }}` in namespace `{{ $labels.exported_namespace }}` was denied access to db_role `{{ $labels.db_role }}` due to `{{ $labels.cause }}` on cluster `{{ $labels.k8s_cluster }}`. Immediate investigation is recommended to ensure proper access controls and service configurations."
    summary: "Service Account `{{ $labels.service_account_name }}` in namespace `{{ $labels.exported_namespace }}` was denied by the injector."
  expr: increase(vault_injector_service_account_denied_count{}[2m]) > 0
  for: 1m
  labels:
    severity: critical
```

**Response Actions:**
- Verify the service account permissions and roles.
- Check the db_role configurations to ensure they are correctly set up.
- Investigate the cause for denial to prevent future occurrences.

###  1.2. <a name='TokenRenewalFailure'></a>Token Renewal Failure

```yaml
- alert: VaultDbInjectorFailToRenewToken
  annotations:
    description: "VaultDbInjector encountered an error while attempting to renew a token. This might affect the continuous operation of dependent services. Check for errors and ensure the token renewal process is configured correctly."
    summary: "VaultDbInjector token renewal failure for namespace `{{ $labels.exported_namespace }}` on cluster `{{ $labels.k8s_cluster }}`."
  expr: increase(vault_injector_renew_token_count_error{}[2m]) > 0
  for: 1m
  labels:
    severity: warning
```

**Response Actions:**
- Review the injector logs for errors related to token renewal.
- Ensure the Vault policies allow for token renewal by the injector.
- Check for network issues that might prevent the injector from communicating with Vault.

###  1.3. <a name='LeaseRenewalFailure'></a>Lease Renewal Failure

```yaml
- alert: VaultDbInjectorFailToRenewLease
  annotations:
    description: "VaultDbInjector encountered an error while attempting to renew a lease. Similar to token renewal failures, this can disrupt service operations if not addressed."
    summary: "VaultDbInjector lease renewal failure for namespace `{{ $labels.exported_namespace }}` on cluster `{{ $labels.k8s_cluster }}`."
  expr: increase(vault_injector_renew_lease_count_error{}[2m]) > 0
  for: 1m
  labels:
    severity: warning
```

**Response Actions:**
- Inspect the injector logs for specific errors related to lease renewal.
- Confirm that the Vault configuration allows the injector to renew leases.
- Investigate any network or configuration issues that might affect communication with Vault.

###  1.4. <a name='TokenExpirationWarnings'></a>Token Expiration Warnings

```yaml
- alert: VaultDbInjectorTokenExpirationLessThan14Days
  annotations:
    description: "A token is nearing expiration (less than 2 weeks). Renewing or rotating the token promptly ensures continuous service operation without interruption."
    summary: "Token nearing expiration in namespace `{{ $labels.exported_namespace }}` on cluster `{{ $labels.k8s_cluster }}`."
  expr: vault_injector_token_expiration - time() < 1209600
  for: 90m
  labels:
    severity: warning

- alert: VaultDbInjectorTokenExpirationLessThan7Days
  annotations:
    description: "A token will expire in less than 7 days. Immediate action is required to renew or rotate the token to avoid service disruption."
    summary: "Urgent: Token expiration warning for namespace `{{ $labels.exported_namespace }}`."
  expr: vault_injector_token_expiration - time() < 604800
  for: 5m
  labels:
    severity: critical
```

**Response Actions:**
- For both alerts, identify the service or application using the token.
- Initiate the token renewal or rotation process.
- Review token policies to ensure they're aligned with security and operational requirements.

###  1.5. <a name='LeaseExpirationWarnings'></a>Lease Expiration Warnings

```yaml
- alert: VaultDbInjectorLeaseExpirationLessThan4Days
  annotations:
    description: "A lease is nearing expiration (less than 4 days). Addressing this promptly can prevent potential access issues for services relying on leased credentials or secrets."
    summary: "Lease nearing expiration for namespace `{{ $labels.namespace }}` on cluster `{{ $labels.k8s_cluster }}`."
  expr: vault_injector_lease_expiration - time() < 345600
  for: 3m
  labels:
    severity: warning

- alert: VaultDbInjectorLeaseExpirationLessThan1Day
  annotations:
    description: "A lease will expire in less than 1 day. Immediate renewal is critical to maintaining access for the dependent services."
    summary: "Critical: Lease expiration imminent for namespace `{{ $labels.namespace }}`."
  expr: vault_injector_lease_expiration - time() < 86400
  for: 3m
  labels:
    severity: critical
```

**Response Actions:**
- Quickly identify and renew the leases for the affected services or credentials.
- Review the lease durations and renewal policies to prevent future alerts.

##  2. <a name='Conclusion'></a>Conclusion

Monitoring VaultDb Injector with these alerts helps ensure the reliability and security of services depending on Vault for secret management and access control. Each alert is designed to provide actionable insights for maintaining operational efficiency and security compliance. Responding promptly to these alerts will mitigate potential risks and disruptions to your services.