package renewer

import (
	"context"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	promInjector "github.com/numberly/vault-db-injector/pkg/prometheus"
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
	vaultConn, err := vault.ConnectToVault(ctx, r.cfg)
	if err != nil {
		r.log.Fatalf("Error connecting to Vault: %v", err)
	}
	r.log.Debugf("authenticated to vault using role %s", r.cfg.VaultAuthPath)
	vaultConn.RenewalInterval = time.Duration(600) * time.Second
	vaultConn.StartTokenRenewal(ctx, r.cfg)

	syncToken := func(vaultConn *vault.Connector) bool {

		keyInfos, err := vaultConn.ListKeyInfo(ctx, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix)
		if err != nil {
			r.log.Errorf("Error while retrieving informations: %v", err)
			promInjector.SynchronizationErrorCount.WithLabelValues().Inc()
			return false
		}

		ok := vaultConn.SyncAndCleanupTokens(ctx, r.cfg, keyInfos, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix, r.clientset, r.cfg.SyncTTLSecond)
		if !ok {
			promInjector.SynchronizationErrorCount.WithLabelValues().Inc()
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
				promInjector.LastTokenSynchronizationSuccess.WithLabelValues().Set(float64(time.Now().Unix()))
			}
			promInjector.SynchronizationCount.WithLabelValues().Inc()
			duration := time.Since(startTime).Seconds()
			r.log.Debugf("The token synchronization has taken : %vs", time.Since(startTime).Seconds())
			promInjector.LastSynchronizationDuration.Observe(duration)

		case <-r.stopChan:
			vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken, "", "")
			r.log.Warn("Stopping TokenSync1Hours due to lost leadership")
			return
		}
	}
}
