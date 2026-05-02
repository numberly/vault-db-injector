package renewer

import (
	"context"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"github.com/numberly/vault-db-injector/pkg/vault"
)

var _ TokenRenewer = (*tokenRenewerImpl)(nil)

type TokenRenewer interface {
	RenewTokenJob(ctx context.Context)
}

type tokenRenewerImpl struct {
	cfg       *config.Config
	stopChan  <-chan struct{}
	clientset k8s.KubernetesClient
	log       logger.Logger
}

func NewTokenRenewer(cfg *config.Config, clientset k8s.KubernetesClient, stopchan <-chan struct{}) TokenRenewer {
	return &tokenRenewerImpl{
		cfg:       cfg,
		stopChan:  stopchan,
		clientset: clientset,
		log:       logger.GetLogger(),
	}
}

func (r *tokenRenewerImpl) RenewTokenJob(ctx context.Context) {
	saToken, err := r.clientset.GetServiceAccountToken()
	if err != nil {
		r.log.Fatalf("Error getting ServiceAccount token: %v", err)
	}
	vaultConn, err := vault.ConnectAndRenew(ctx, r.cfg, saToken)
	if err != nil {
		r.log.Fatalf("Error connecting to Vault: %v", err)
	}
	r.log.Debugf("authenticated to vault using role %s", r.cfg.VaultAuthPath)

	syncToken := func(vaultConn *vault.Connector) bool {

		keyInfos, err := vaultConn.ListKeyInfo(ctx, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix)
		if err != nil {
			// ListKeyInfo may return partial results alongside a non-nil error;
			// honor that contract so a single transient KV failure does not
			// stall renewal of every other token until the next tick.
			r.log.Warnf("Partial error while retrieving key info, continuing with available keys: %v", err)
			metrics.SynchronizationErrorCount.WithLabelValues().Inc()
		}
		if len(keyInfos) == 0 {
			return false
		}

		podService := k8s.NewPodService(r.clientset, r.cfg)
		ok := vaultConn.SyncAndCleanupTokens(ctx, r.cfg, keyInfos, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix, podService, r.cfg.SyncTTLSecond)
		if !ok {
			metrics.SynchronizationErrorCount.WithLabelValues().Inc()
			return false
		}

		return true
	}

	ticker := time.NewTicker(time.Duration(r.cfg.SyncTTLSecond) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.log.Info("Token Synchronization has been started!")
			startTime := time.Now()
			if !syncToken(vaultConn) {
				r.log.Error("Token Synchronization Error!")
			} else {
				r.log.Info("Token Synchronization Successful!")
				metrics.LastTokenSynchronizationSuccess.WithLabelValues().Set(float64(time.Now().Unix()))
			}
			metrics.SynchronizationCount.WithLabelValues().Inc()
			duration := time.Since(startTime).Seconds()
			r.log.Debugf("The token synchronization has taken : %vs", time.Since(startTime).Seconds())
			metrics.LastSynchronizationDuration.Observe(duration)

		case <-r.stopChan:
			if err := vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken); err != nil {
				r.log.Errorf("RevokeSelfToken failed: %v", err)
			}
			r.log.Warn("Stopping TokenSync1Hours due to lost leadership")
			return
		}
	}
}
