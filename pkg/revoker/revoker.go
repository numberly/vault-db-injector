package revoker

import (
	"context"
	"fmt"
	"time"

	"strings"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	promInjector "github.com/numberly/vault-db-injector/pkg/prometheus"
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

func (r *tokenRevokerImpl) RevokeTokenJob(ctx context.Context) {
	vaultConn, err := vault.ConnectToVault(ctx, r.cfg)
	if err != nil {
		r.log.Fatalf("Error connecting to Vault: %v", err)
	}
	r.log.Debugf("authenticated to vault using role %s", r.cfg.VaultAuthPath)
	vaultConn.RenewalInterval = time.Duration(600) * time.Second
	vaultConn.StartTokenRenewal(ctx, r.cfg)

	// Create a child context that will be cancelled on stopChan signal
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start watching for stopChan signals to cancel the context
	go func() {
		<-r.stopChan
		r.log.Info("Received stop signal, stopping RevokeTokenJob.")
		cancel() // This cancels the context
	}()

	go func() {
		var watch watch.Interface
		var err error

		// Initial watch setup
		watch, err = r.startWatchingPods(ctx)
		if err != nil {
			r.log.Errorf("Failed to start watching pods: %v", err)
			return
		}
		for {
			select {
			case <-ctx.Done():
				// If the context is cancelled, stop the goroutine
				r.log.Info("Context cancelled, stopping pod watcher.")
				return
			case event, ok := <-watch.ResultChan():
				if !ok {
					r.log.Warn("Pod watch channel closed, attempting to restart...")
					watch, err = r.startWatchingPods(ctx)
					if err != nil {
						r.log.Errorf("Failed to restart watching pods: %v", err)
						cancel()
						return
					}
					continue
				}
				if event.Type == "DELETED" {
					pod, ok := event.Object.(*corev1.Pod)
					if !ok {
						r.log.Infof("Unexpected type\n")
						continue
					}
					if uuidsString, exists := pod.GetAnnotations()[k8s.ANNOTATION_VAULT_POD_UUID]; exists {
						uuids := strings.Split(uuidsString, ",")
						for _, uuid := range uuids {
							keyInformation, err := vaultConn.GetKeyInfo(ctx, pod.Name, uuid, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix)
							if err != nil || keyInformation == nil {
								r.log.Errorf("Error while retrieving information or keyInformation is nil: %v", err)
								promInjector.PodCleanupErrorCount.WithLabelValues().Inc()
								continue
							}
							if vaultConn != nil {
								err = vaultConn.HandlePodDeletionToken(ctx, keyInformation, r.cfg.VaultSecretName, r.cfg.VaultSecretPrefix)
								if err != nil {
									r.log.Errorf("Error in HandlePodDeletionToken: %v", err)
									promInjector.PodCleanupErrorCount.WithLabelValues().Inc()
									continue
								}
							} else {
								r.log.Errorf("vaultConn or keyInformation is nil")
								promInjector.PodCleanupErrorCount.WithLabelValues().Inc()
								continue
							}
						}
						promInjector.PodCleanupSuccessCount.WithLabelValues().Inc()
					}
				}
			}
		}
	}()

	// Keep the main goroutine alive until context is cancelled
	<-ctx.Done()
	vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken, "", "")
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
