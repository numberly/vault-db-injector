package revoker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"github.com/numberly/vault-db-injector/pkg/vault"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

var _ TokenRevoker = (*tokenRevokerImpl)(nil)

type TokenRevoker interface {
	RevokeTokenJob(ctx context.Context)
}

type tokenRevokerImpl struct {
	cfg       *config.Config
	stopChan  <-chan struct{}
	clientset k8s.KubernetesClient
	log       logger.Logger
}

func NewTokenRevoker(cfg *config.Config, clientset k8s.KubernetesClient, stopchan <-chan struct{}) TokenRevoker {
	return &tokenRevokerImpl{
		cfg:       cfg,
		stopChan:  stopchan,
		clientset: clientset,
		log:       logger.GetLogger(),
	}
}

func (r *tokenRevokerImpl) safetyNetSync(ctx context.Context, vaultConn *vault.Connector) {
	keyInfos, err := vaultConn.ListKeyInfo(ctx, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix)
	if err != nil {
		// ListKeyInfo may return partial results; log but continue with what we have.
		r.log.Warnf("safetyNetSync ListKeyInfo partial error: %v", err)
	}
	if len(keyInfos) == 0 {
		return
	}

	podSvc := k8s.NewPodService(r.clientset, r.cfg)
	pods, err := podSvc.GetAllPodAndNamespace(ctx)
	if err != nil {
		r.log.Errorf("safetyNetSync GetAllPodAndNamespace failed: %v", err)
		return
	}

	podUUIDs := make(map[string]bool)
	for _, pi := range pods {
		for _, uuid := range pi.PodNameUUIDs {
			podUUIDs[uuid] = true
		}
	}

	for _, ki := range keyInfos {
		if podUUIDs[ki.PodNameUID] {
			continue
		}
		// Pod for this UUID is gone — revoke + purge the KV entry.
		if err := vaultConn.HandlePodDeletionToken(ctx, ki, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix); err != nil {
			r.log.Warnf("safetyNetSync HandlePodDeletionToken for uuid %s: %v", ki.PodNameUID, err)
			continue
		}
		r.log.Infof("safetyNetSync: revoked + purged orphan KV entry for uuid %s", ki.PodNameUID)
	}
}

func (r *tokenRevokerImpl) RevokeTokenJob(ctx context.Context) {
	saToken, err := r.clientset.GetServiceAccountToken()
	if err != nil {
		r.log.Fatalf("Error getting ServiceAccount token: %v", err)
	}
	vaultConn, err := vault.ConnectAndRenew(ctx, r.cfg, saToken)
	if err != nil {
		r.log.Fatalf("Error connecting to Vault: %v", err)
	}
	r.log.Debugf("authenticated to vault using role %s", r.cfg.VaultAuthPath)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Periodic safety-net sync: catch pods that died while the revoker
	// was down or the watch was disconnected.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.log.Debug("safetyNetSync: starting periodic orphan cleanup")
				r.safetyNetSync(ctx, vaultConn)
			}
		}
	}()

	go func() {
		var watcher watch.Interface
		var err error

		watcher, err = r.startWatchingPods(ctx)
		if err != nil {
			r.log.Errorf("Failed to start watching pods: %v", err)
			return
		}
		for {
			select {
			case <-r.stopChan:
				r.log.Info("Received stop signal, stopping RevokeTokenJob.")
				cancel()
				return
			case <-ctx.Done():
				r.log.Info("Context cancelled, stopping pod watcher.")
				return
			case event, ok := <-watcher.ResultChan():
				if !ok {
					r.log.Warn("Pod watch channel closed, attempting to restart...")
					watcher, err = r.startWatchingPods(ctx)
					if err != nil {
						r.log.Errorf("Failed to restart watching pods: %v", err)
						cancel()
						return
					}
					continue
				}
				if event.Type == watch.Deleted {
					pod, ok := event.Object.(*corev1.Pod)
					if !ok {
						r.log.Infof("Unexpected type\n")
						continue
					}
					if uuidsString, exists := pod.GetAnnotations()[k8s.ANNOTATION_VAULT_POD_UUID]; exists {
						uuids := strings.Split(uuidsString, ",")
						for _, uuid := range uuids {
							keyInformation, err := vaultConn.GetKeyInfo(ctx, pod.Name, uuid, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix)
							if err != nil {
								if errors.Is(err, vault.ErrKeyNotFound) {
									r.log.Debugf("No vault data found for uuid %s (pod %s), skipping revocation", uuid, pod.Name)
								} else {
									r.log.Errorf("Error while retrieving key information for uuid %s: %v", uuid, err)
									metrics.PodCleanupErrorCount.WithLabelValues().Inc()
								}
								continue
							}
							if keyInformation == nil {
								r.log.Errorf("keyInformation unexpectedly nil for uuid %s", uuid)
								metrics.PodCleanupErrorCount.WithLabelValues().Inc()
								continue
							}
							err = vaultConn.HandlePodDeletionToken(ctx, keyInformation, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix)
							if err != nil {
								r.log.Errorf("Error in HandlePodDeletionToken: %v", err)
								metrics.PodCleanupErrorCount.WithLabelValues().Inc()
								continue
							}
						}
						metrics.PodCleanupSuccessCount.WithLabelValues().Inc()
					}
				}
			}
		}
	}()

	// Keep the main goroutine alive until context is cancelled
	<-ctx.Done()
	// ctx is already cancelled here, so any Vault call using it would fail
	// immediately. Use a fresh short-lived context for the cleanup so the
	// self token still gets revoked on shutdown.
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelCleanup()
	if err := vaultConn.RevokeSelfToken(cleanupCtx, vaultConn.K8sSaVaultToken); err != nil {
		r.log.Errorf("RevokeSelfToken failed: %v", err)
	}
	r.log.Info("RevokeTokenJob stopped.")
}

func (r *tokenRevokerImpl) startWatchingPods(ctx context.Context) (watch.Interface, error) {
	listOptions := v1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", r.cfg.InjectorLabel),
	}
	watch, err := r.clientset.CoreV1().Pods("").Watch(ctx, listOptions)
	if err != nil {
		return nil, err
	}
	r.log.Infof("Watching pod deletions...")
	return watch, nil
}
