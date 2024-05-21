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

var _ TokenRenewor = (*tokenReneworImpl)(nil)

type TokenRenewor interface {
	RenewTokenJob(ctx context.Context)
}

type tokenReneworImpl struct {
	cfg       *config.Config
	stopChan  <-chan struct{}
	clientset k8s.KubernetesClient
	log       logger.Logger
}

func NewTokenRenewor(cfg *config.Config, clientset k8s.KubernetesClient, stopchan <-chan struct{}) TokenRenewor {
	return &tokenReneworImpl{
		cfg:       cfg,
		stopChan:  stopchan,
		clientset: clientset,
		log:       logger.GetLogger(),
	}
}

func (tri *tokenReneworImpl) RenewTokenJob(ctx context.Context) {
	vaultConn, err := vault.ConnectToVault(ctx, tri.cfg)
	if err != nil {
		tri.log.Fatalf("Error connecting to Vault: %v", err)
	}
	tri.log.Debugf("authenticated to vault using role %s", tri.cfg.VaultAuthPath)
	vaultConn.RenewalInterval = time.Duration(600) * time.Second
	vaultConn.StartTokenRenewal(ctx, tri.cfg)

	syncToken := func(vaultConn *vault.Connector) bool {

		keyInfos, err := vaultConn.ListKeyInformations(ctx, tri.cfg.VaultSecretName, tri.cfg.VaultSecretPrefix)
		if err != nil {
			tri.log.Errorf("Error while retrieving informations: %v", err)
			promInjector.SynchronizationErrorCount.WithLabelValues().Inc()
			return false
		}

		ok := vaultConn.HandleTokens(ctx, tri.cfg, keyInfos, tri.cfg.VaultSecretName, tri.cfg.VaultSecretPrefix, tri.clientset, tri.cfg.SyncTTLSecond)
		if !ok {
			promInjector.SynchronizationErrorCount.WithLabelValues().Inc()
			return false
		}

		return true
	}

	ticker := time.NewTicker(time.Duration(tri.cfg.SyncTTLSecond) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tri.log.Info("Token Synchronization has been started!")
			startTime := time.Now()
			if !syncToken(vaultConn) {
				tri.log.Error("Token Synchronization Error!")
			} else {
				tri.log.Info("Token Synchronization Successful!")
				promInjector.LastTokenSynchronisationSuccess.WithLabelValues().Set(float64(time.Now().Unix()))
			}
			promInjector.SynchronizationCount.WithLabelValues().Inc()
			duration := time.Since(startTime).Seconds()
			tri.log.Debugf("The token synchronization has taken : %vs", time.Since(startTime).Seconds())
			promInjector.LastSynchronizationDuration.Observe(duration)

		case <-tri.stopChan:
			vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken, "", "")
			tri.log.Warn("Stopping TokenSync1Hours due to lost leadership")
			return
		}
	}
}
