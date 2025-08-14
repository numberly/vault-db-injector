package prometheus

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

var (
	RenewTokenCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_renew_token_count_success",
			Help: "Vault injoctor token renewed with success count",
		},
		[]string{"uuid", "namespace"},
	)
	RenewTokenErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_renew_token_count_error",
			Help: "Vault injector token renewed with error count",
		},
		[]string{"uuid", "namespace"},
	)
	RenewLeaseCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_renew_lease_count_success",
			Help: "Vault injoctor lease renewed with success count",
		},
		[]string{"uuid", "namespace"},
	)
	RenewLeaseErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_renew_lease_count_error",
			Help: "Vault injector lease renewed with error count",
		},
		[]string{"uuid", "namespace"},
	)
	RevokeTokenCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_revoke_token_count_success",
			Help: "Vault injector token revoked with success count",
		},
		[]string{"namespace"},
	)
	RevokeTokenErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_revoke_token_count_error",
			Help: "Vault injector token revoked with error count",
		},
		[]string{"uuid", "namespace"},
	)
	TokenExpirationInTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vault_injector_token_expiration",
			Help: "Vault injector expiration time",
		},
		[]string{"uuid", "namespace"},
	)
	LeaseExpirationInTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vault_injector_lease_expiration",
			Help: "Vault injector expiration time",
		},
		[]string{"uuid", "namespace"},
	)
	// Not used
	LastTokenRenewSuccessInTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vault_injector_token_last_renewed",
			Help: "Last vault token successful renewal",
		},
		[]string{"uuid", "namespace"},
	)
	SynchronizationCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_synchronization_count_success",
			Help: "Vault injector synchronization with success",
		},
		[]string{},
	)
	SynchronizationErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_synchronization_count_error",
			Help: "Vault injector synchronization with error",
		},
		[]string{},
	)
	PodCleanupSuccessCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_pod_cleanup_count_success",
			Help: "Vault injector PodCleanup with success",
		},
		[]string{},
	)
	PodCleanupErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_pod_cleanup_count_error",
			Help: "Vault injector PodCleanup with error",
		},
		[]string{},
	)
	LastTokenSynchronisationSuccess = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vault_injector_last_synchronization_success",
			Help: "Last vault token successful renewal",
		},
		[]string{},
	)
	OrphanTicketCreatedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_orphan_ticket_created_count_success",
			Help: "Vault injector orphan ticket created with success",
		},
		[]string{},
	)
	OrphanErrorTicketCreatedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_orphan_ticket_created_count_error",
			Help: "Vault injector orphan ticket created with error",
		},
		[]string{},
	)

	DataStoredCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_store_data_count_success",
			Help: "Vault injector data stored with success",
		},
		[]string{},
	)
	DataErrorStoredCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_store_data_count_error",
			Help: "Vault injector data stored with error",
		},
		[]string{"uuid", "namespace"},
	)
	DataDeletedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_delete_data_count_success",
			Help: "Vault injector data delete with success",
		},
		[]string{"uuid", "namespace"},
	)
	DataErrorDeletedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_delete_data_count_error",
			Help: "Vault injector data deleted with error",
		},
		[]string{"uuid", "namespace"},
	)
	ConnectVault = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_connect_vault_count_success",
			Help: "Vault injector connect to vault with success",
		},
		[]string{},
	)
	ConnectVaultError = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_connect_vault_count_error",
			Help: "Vault injector connect to vault with error",
		},
		[]string{},
	)
	ServiceAccountAuthorized = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_service_account_authorized_count",
			Help: "Vault injector service account is authorized to assume dbRole",
		},
		[]string{},
	)
	ServiceAccountDenied = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_service_account_denied_count",
			Help: "Vault injector service account is no authorized to assume dbRole",
		},
		[]string{"service_account_name", "namespace", "db_role", "cause"},
	)
	LastSynchronizationDuration = prometheus.NewSummary(
		prometheus.SummaryOpts{
			Name: "vault_injector_last_synchronization_duration",
			Help: "Vault injector last duration of synchronization",
		},
	)
	IsLeader = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vault_injector_is_leader",
			Help: "Return 1 if the vault injector is leader, else 0",
		},
		[]string{"lease_name"},
	)
	LeaseElectionAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_leader_election_attempts_total",
			Help: "Total number of attempts to acquire leadership.",
		},
		[]string{"lease_name"},
	)
	LeaseDuration = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vault_injector_leader_election_duration_seconds",
			Help: "Duration in seconds that this instance has been the leader.",
		}, []string{"lease_name", "leader_name", "mode"},
	)
	GetAllPodSuccessCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_fetch_pods_success_count",
			Help: "Count that increase when their is no error retrieving pods",
		}, []string{},
	)
	GetAllPodErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_fetch_pods_error_count",
			Help: "Count that increase when their is an error retrieving pods",
		}, []string{},
	)
	MutatedPodWithSucessCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_mutated_pods_success_count",
			Help: "Count that increase when their is an error mutating pods",
		}, []string{},
	)
	MutatedPodWithErrorCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "vault_injector_mutated_pods_error_count",
			Help: "Count that increase when their is an error mutating pods",
		}, []string{},
	)
)

func Init(prom *prometheus.Registry) {
	prom.MustRegister(
		MutatedPodWithErrorCount,
		MutatedPodWithSucessCount,
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
		LastTokenRenewSuccessInTime,
		SynchronizationCount,
		SynchronizationErrorCount,
		LastTokenSynchronisationSuccess,
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
	)
}
