package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metric naming convention: all metrics emitted by this binary use
// the `vdbi_` prefix. The legacy `vdbi_` prefix was
// retired in v3.0.0 (see docs/how-it-works/migration-v2-to-v3.md
// for dashboards/alerts migration).
var (
	RenewTokenCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_renew_token_count_success",
			Help: "Vault injector token renewed with success count",
		},
		[]string{"uuid", "namespace"},
	)
	RenewTokenErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_renew_token_count_error",
			Help: "Vault injector token renewed with error count",
		},
		[]string{"uuid", "namespace"},
	)
	RenewLeaseCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_renew_lease_count_success",
			Help: "Vault injector lease renewed with success count",
		},
		[]string{"uuid", "namespace"},
	)
	RenewLeaseErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_renew_lease_count_error",
			Help: "Vault injector lease renewed with error count",
		},
		[]string{"uuid", "namespace"},
	)
	RevokeTokenCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_revoke_token_count_success",
			Help: "Vault injector token revoked with success count",
		},
		[]string{"namespace"},
	)
	RevokeTokenErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_revoke_token_count_error",
			Help: "Vault injector token revoked with error count",
		},
		[]string{"uuid", "namespace"},
	)
	TokenExpirationInTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vdbi_token_expiration",
			Help: "Vault injector expiration time",
		},
		[]string{"uuid", "namespace"},
	)
	LeaseExpirationInTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vdbi_lease_expiration",
			Help: "Vault injector expiration time",
		},
		[]string{"uuid", "namespace"},
	)
	SynchronizationCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_synchronization_count_success",
			Help: "Vault injector synchronization with success",
		},
		[]string{},
	)
	SynchronizationErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_synchronization_count_error",
			Help: "Vault injector synchronization with error",
		},
		[]string{},
	)
	PodCleanupSuccessCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_pod_cleanup_count_success",
			Help: "Vault injector PodCleanup with success",
		},
		[]string{},
	)
	PodCleanupErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_pod_cleanup_count_error",
			Help: "Vault injector PodCleanup with error",
		},
		[]string{},
	)
	LastTokenSynchronizationSuccess = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vdbi_last_synchronization_success",
			Help: "Last vault token successful renewal",
		},
		[]string{},
	)
	OrphanTicketCreatedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_orphan_ticket_created_count_success",
			Help: "Vault injector orphan ticket created with success",
		},
		[]string{},
	)
	OrphanErrorTicketCreatedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_orphan_ticket_created_count_error",
			Help: "Vault injector orphan ticket created with error",
		},
		[]string{},
	)

	DataStoredCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_store_data_count_success",
			Help: "Vault injector data stored with success",
		},
		[]string{},
	)
	DataErrorStoredCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_store_data_count_error",
			Help: "Vault injector data stored with error",
		},
		[]string{"uuid", "namespace"},
	)
	DataDeletedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_delete_data_count_success",
			Help: "Vault injector data delete with success",
		},
		[]string{"uuid", "namespace"},
	)
	DataErrorDeletedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_delete_data_count_error",
			Help: "Vault injector data deleted with error",
		},
		[]string{"uuid", "namespace"},
	)
	ConnectVault = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_connect_vault_count_success",
			Help: "Vault injector connect to vault with success",
		},
		[]string{},
	)
	ConnectVaultError = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_connect_vault_count_error",
			Help: "Vault injector connect to vault with error",
		},
		[]string{},
	)
	ServiceAccountAuthorized = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_service_account_authorized_count",
			Help: "Vault injector service account is authorized to assume dbRole",
		},
		[]string{},
	)
	ServiceAccountDenied = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_service_account_denied_count",
			Help: "Vault injector service account is no authorized to assume dbRole",
		},
		[]string{"service_account_name", "namespace", "db_role", "cause"},
	)
	LastSynchronizationDuration = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Name: "vdbi_last_synchronization_duration",
			Help: "Vault injector last duration of synchronization",
		},
	)
	IsLeader = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vdbi_is_leader",
			Help: "Return 1 if the vault injector is leader, else 0",
		},
		[]string{"lease_name"},
	)
	LeaseElectionAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_leader_election_attempts_total",
			Help: "Total number of attempts to acquire leadership.",
		},
		[]string{"lease_name"},
	)
	LeaseDuration = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vdbi_leader_election_duration_seconds",
			Help: "Duration in seconds that this instance has been the leader.",
		}, []string{"lease_name", "leader_name", "mode"},
	)
	GetAllPodSuccessCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_fetch_pods_success_count",
			Help: "Count that increase when their is no error retrieving pods",
		}, []string{},
	)
	GetAllPodErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_fetch_pods_error_count",
			Help: "Count that increase when their is an error retrieving pods",
		}, []string{},
	)
	MutatedPodWithSuccessCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_mutated_pods_success_count",
			Help: "Count that increase when their is an error mutating pods",
		}, []string{},
	)
	MutatedPodWithErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_mutated_pods_error_count",
			Help: "Count that increase when their is an error mutating pods",
		}, []string{},
	)
	NRISubstitutionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_nri_substitutions_total",
			Help: "Number of CreateContainer events where the NRI plugin emitted an env adjustment.",
		}, []string{},
	)
	NRIUnwrapFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_nri_unwrap_failures_total",
			Help: "Number of NRI plugin unwrap failures by reason.",
		}, []string{"reason"},
	)
	TokenRequestErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_token_request_errors_total",
			Help: "Number of failed Kubernetes TokenRequest calls for projected-SA Vault login.",
		}, []string{"reason"},
	)
	VaultLoginErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_vault_login_errors_total",
			Help: "Number of failed Vault logins, labeled by auth_mode (legacy/projected).",
		}, []string{"reason", "auth_mode"},
	)
	ProjectedRoleMisconfigured = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_projected_role_misconfigured_total",
			Help: "Number of times a Vault role used in projected-SA mode was found without token_period > 0.",
		}, []string{"role"},
	)
	NRIResolveDuplicateTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vdbi_nri_resolve_duplicate_total",
			Help: "Number of resolveMapping calls that hit a concurrent in-flight call (singleflight share). Indicates multi-container pods triggering simultaneous CreateContainer.",
		}, []string{},
	)
)

func Init(prom *prometheus.Registry) {
	prom.MustRegister(
		MutatedPodWithErrorCount,
		MutatedPodWithSuccessCount,
		NRISubstitutionsTotal,
		NRIUnwrapFailures,
		TokenRequestErrors,
		VaultLoginErrors,
		ProjectedRoleMisconfigured,
		GetAllPodErrorCount,
		GetAllPodSuccessCount,
		RenewTokenCount,
		RenewLeaseCount,
		RenewLeaseErrorCount,
		LeaseExpirationInTime,
		PodCleanupSuccessCount,
		PodCleanupErrorCount,
		RevokeTokenCount,
		RevokeTokenErrorCount,
		TokenExpirationInTime,
		SynchronizationCount,
		SynchronizationErrorCount,
		LastTokenSynchronizationSuccess,
		OrphanTicketCreatedCount,
		OrphanErrorTicketCreatedCount,
		DataStoredCount,
		DataErrorStoredCount,
		DataDeletedCount,
		DataErrorDeletedCount,
		ConnectVault,
		ConnectVaultError,
		ServiceAccountAuthorized,
		ServiceAccountDenied,
		LastSynchronizationDuration,
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collectors.NewGoCollector(),
		IsLeader,
		LeaseElectionAttempts,
		LeaseDuration,
		NRIResolveDuplicateTotal,
	)
}
