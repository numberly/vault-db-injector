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

// flushVanished drops the per-pod metric series of every uuid present in prev
// but absent from the current KV listing, and returns the new tracked set.
func flushVanished(prev map[string]string, current []*vault.KeyInfo) map[string]string {
	next := make(map[string]string, len(current))
	for _, ki := range current {
		next[ki.PodNameUID] = ki.Namespace
	}
	for uuid, namespace := range prev {
		if _, still := next[uuid]; !still {
			metrics.FlushPodSeries(uuid, namespace)
		}
	}
	return next
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

	// uuid -> namespace of every KV entry seen on the previous cycle. The
	// revoker usually deletes the KV entry before this process notices the
	// pod is gone, so the "pod missing in k8s" branch in SyncAndCleanupTokens
	// rarely runs; diffing consecutive KV listings is what actually catches
	// vanished pods and lets us drop their frozen metric series.
	tracked := map[string]string{}

	syncToken := func(vaultConn *vault.Connector) bool {

		keyInfos, err := vaultConn.ListKeyInfo(ctx, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix)
		if err != nil {
			// ListKeyInfo may return partial results alongside a non-nil error;
			// honor that contract so a single transient KV failure does not
			// stall renewal of every other token until the next tick.
			r.log.Warnf("Partial error while retrieving key info, continuing with available keys: %v", err)
			metrics.SynchronizationErrorCount.WithLabelValues().Inc()
		} else {
			// Only trust a complete listing: a partial one would flush series
			// of pods that are still alive (they would come back on the next
			// successful renew anyway, but there is no reason to flap).
			tracked = flushVanished(tracked, keyInfos)
		}
		if len(keyInfos) == 0 {
			return false
		}

		podService := k8s.NewPodService(r.clientset, r.cfg)
		// Renew increment is the desired token lifetime (TokenTTL), NOT the sync
		// interval. Otherwise the token TTL collapses to the sync period.
		renewIncrementSeconds, err := r.cfg.TokenTTLSeconds()
		if err != nil {
			r.log.Errorf("invalid tokenTTL, skipping sync: %v", err)
			metrics.SynchronizationErrorCount.WithLabelValues().Inc()
			return false
		}
		ok := vaultConn.SyncAndCleanupTokens(ctx, r.cfg, keyInfos, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix, podService, renewIncrementSeconds)
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
			// Use a fresh context: on SIGTERM the inherited ctx is already
			// cancelled, which would make RevokeSelfToken fail immediately and
			// leak the renewer's own login token. Mirrors revoker.go shutdown.
			cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
			if err := vaultConn.RevokeSelfToken(cleanupCtx, vaultConn.K8sSaVaultToken); err != nil {
				r.log.Errorf("RevokeSelfToken failed: %v", err)
			}
			cancelCleanup()
			r.log.Warn("Stopping TokenSync1Hours due to lost leadership")
			return
		}
	}
}
