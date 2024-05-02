package revoker

import (
	"context"
	"fmt"
	"time"

	"strings"

	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/config"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/k8s"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/logger"
	promInjector "gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/prometheus"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/vault"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

var _ TokenRevokor = (*tokenRevokorImpl)(nil)

type TokenRevokor interface {
	RevokeTokenJob(ctx context.Context)
}

type tokenRevokorImpl struct {
	cfg       *config.Config
	stopChan  <-chan struct{}
	clientset k8s.KubernetesClient
	log       logger.Logger
}

func NewTokenRevokor(cfg *config.Config, clientset k8s.KubernetesClient, stopchan <-chan struct{}) TokenRevokor {
	return &tokenRevokorImpl{
		cfg:       cfg,
		stopChan:  stopchan,
		clientset: clientset,
		log:       logger.GetLogger(),
	}
}

func (tri *tokenRevokorImpl) RevokeTokenJob(ctx context.Context) {
	vaultConn, err := vault.ConnectToVault(ctx, tri.cfg)
	if err != nil {
		tri.log.Fatalf("Error connecting to Vault: %v", err)
	}
	tri.log.Debugf("authenticated to vault using role %s", tri.cfg.VaultAuthPath)
	vaultConn.RenewalInterval = time.Duration(600) * time.Second
	vaultConn.StartTokenRenewal(ctx, tri.cfg)

	// Create a child context that will be cancelled on stopChan signal
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start watching for stopChan signals to cancel the context
	go func() {
		<-tri.stopChan
		tri.log.Info("Received stop signal, stopping RevokeTokenJob.")
		cancel() // This cancels the context
	}()

	go func() {
		var watch watch.Interface
		var err error

		// Initial watch setup
		watch, err = tri.startWatchingPods(ctx)
		if err != nil {
			tri.log.Errorf("Failed to start watching pods: %v", err)
			return
		}
		for {
			select {
			case <-ctx.Done():
				// If the context is cancelled, stop the goroutine
				tri.log.Info("Context cancelled, stopping pod watcher.")
				return
			case event, ok := <-watch.ResultChan():
				if !ok {
					tri.log.Warn("Pod watch channel closed, attempting to restart...")
					watch, err = tri.startWatchingPods(ctx)
					if err != nil {
						tri.log.Errorf("Failed to restart watching pods: %v", err)
						cancel()
						return
					}
					continue
				}
				if event.Type == "DELETED" {
					pod, ok := event.Object.(*corev1.Pod)
					if !ok {
						tri.log.Infof("Unexpected type\n")
						continue
					}
					if uuidsString, exists := pod.GetAnnotations()[k8s.ANNOTATION_VAULT_POD_UUID]; exists {
						uuids := strings.Split(uuidsString, ",")
						for _, uuid := range uuids {
							keyInformation, err := vaultConn.GetKeyInformations(ctx, pod.Name, uuid, tri.cfg.VaultSecretName, tri.cfg.VaultSecretPrefix)
							if err != nil || keyInformation == nil {
								tri.log.Errorf("Error while retrieving information or keyInformation is nil: %v", err)
								promInjector.PodCleanupErrorCount.WithLabelValues().Inc()
								continue
							}
							if vaultConn != nil {
								err = vaultConn.HandlePodDeletionToken(ctx, keyInformation, tri.cfg.VaultSecretName, tri.cfg.VaultSecretPrefix)
								if err != nil {
									tri.log.Errorf("Error in HandlePodDeletionToken: %v", err)
									promInjector.PodCleanupErrorCount.WithLabelValues().Inc()
									continue
								}
							} else {
								tri.log.Errorf("vaultConn or keyInformation is nil")
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
	tri.log.Info("RevokeTokenJob stopped.")
}

func (tri *tokenRevokorImpl) startWatchingPods(ctx context.Context) (watch.Interface, error) {
	listOptions := v1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", tri.cfg.InjectorLabel),
	}
	watch, err := tri.clientset.CoreV1().Pods("").Watch(ctx, listOptions)
	if err != nil {
		return nil, err
	}
	tri.log.Infof("Watching pod deletions...")
	return watch, nil
}
