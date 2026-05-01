package controller

import (
	"context"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/healthcheck"
	"github.com/numberly/vault-db-injector/pkg/injector"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/leadership"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"github.com/numberly/vault-db-injector/pkg/renewer"
	"github.com/numberly/vault-db-injector/pkg/revoker"
	"github.com/numberly/vault-db-injector/pkg/sentry"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

type Controller struct {
	Cfg       *config.Config
	Clientset k8s.KubernetesClient
	log       logger.Logger
	sentry    sentry.SentryService
}

func NewController(cfg *config.Config, Clientset k8s.KubernetesClient, sentrySvc sentry.SentryService) *Controller {
	return &Controller{
		Cfg:       cfg,
		Clientset: Clientset,
		log:       logger.GetLogger(),
		sentry:    sentrySvc,
	}
}

func (c *Controller) RunInjector(ctx context.Context, errChan chan<- error, runSuccess chan<- bool) {
	c.log.Info("Starting server in mode injector")
	stopChan := make(chan struct{})
	is := injector.NewWebhookStarter(c.Cfg, errChan, runSuccess, c.sentry)
	hcService := healthcheck.NewService(c.Cfg)
	hcService.RegisterHandlers()
	go hcService.Start(ctx, stopChan)
	go is.StartWebhook(ctx, stopChan)
}

func (c *Controller) RunRenewer(ctx context.Context, metricsSuccess chan<- bool) {
	c.log.Info("Starting server in mode renewer")
	stopChan := make(chan struct{})
	podName, lock := c.buildLock("lock-injector-renewer")
	metrics.IsLeader.WithLabelValues(lock.LeaseMeta.GetName()).Set(0)
	clientset := c.Clientset
	cfg := c.Cfg
	le := leadership.NewLeaderElector(lock, podName, func(ctx context.Context, stopChan chan struct{}) {
		renewer.NewTokenRenewer(cfg, clientset, stopChan).RenewTokenJob(ctx)
	})
	go le.RunLeaderElection(ctx, stopChan)
	metricsService := metrics.NewMetricsService(metricsSuccess)
	go metricsService.RunMetrics()
	hcService := healthcheck.NewService(c.Cfg)
	hcService.RegisterLivenessProbe(le.IsHealthy)
	hcService.RegisterHandlers()
	go hcService.Start(ctx, stopChan)
}

func (c *Controller) RunRevoker(ctx context.Context, metricsSuccess chan<- bool) {
	c.log.Info("Starting server in mode revoker")
	stopChan := make(chan struct{})
	podName, lock := c.buildLock("lock-injector-revoker")
	metrics.IsLeader.WithLabelValues(lock.LeaseMeta.GetName()).Set(0)
	clientset := c.Clientset
	cfg := c.Cfg
	le := leadership.NewLeaderElector(lock, podName, func(ctx context.Context, stopChan chan struct{}) {
		revoker.NewTokenRevoker(cfg, clientset, stopChan).RevokeTokenJob(ctx)
	})
	go le.RunLeaderElection(ctx, stopChan)
	metricsService := metrics.NewMetricsService(metricsSuccess)
	go metricsService.RunMetrics()
	hcService := healthcheck.NewService(c.Cfg)
	hcService.RegisterLivenessProbe(le.IsHealthy)
	hcService.RegisterHandlers()
	go hcService.Start(ctx, stopChan)
}

// buildLock resolves HA environment variables and constructs the leader-election lock.
// It is called at the start of RunRenewer/RunRevoker rather than mutating controller fields,
// so the Controller struct carries no implicit initialization order dependency.
func (c *Controller) buildLock(lockName string) (string, *resourcelock.LeaseLock) {
	podName, podNamespace, err := config.GetHAEnvs()
	if err != nil {
		c.log.Fatalf("%s", err)
	}
	return podName, leadership.NewLock(c.Clientset.CoordinationV1(), lockName, podName, podNamespace)
}
