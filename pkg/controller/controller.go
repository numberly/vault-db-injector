package controller

import (
	"context"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/healthcheck"
	"github.com/numberly/vault-db-injector/pkg/injector"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/leadership"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/prometheus"
	"github.com/numberly/vault-db-injector/pkg/renewer"
	"github.com/numberly/vault-db-injector/pkg/revoker"
	"github.com/numberly/vault-db-injector/pkg/sentry"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

var stopChan = make(chan struct{})

type Controller struct {
	Cfg       *config.Config
	Clientset *kubernetes.Clientset
	Lock      *resourcelock.LeaseLock
	PodName   string
	log       logger.Logger
	sentry    sentry.SentryService
}

func NewController(cfg *config.Config, Clientset *kubernetes.Clientset, sentrySvc sentry.SentryService) *Controller {
	return &Controller{
		Cfg:       cfg,
		Clientset: Clientset,
		log:       logger.GetLogger(),
		sentry:    sentrySvc,
	}
}

func (c *Controller) RunInjector(ctx context.Context, errChan chan<- error, runSuccess chan<- bool) {
	c.log.Info("Starting server in mode injector")
	is := injector.NewWebhookStartor(c.Cfg, errChan, runSuccess, c.sentry)
	hcService := healthcheck.NewService(c.Cfg)
	hcService.RegisterHandlers()
	go hcService.Start(ctx, stopChan)
	go is.StartWebhook(ctx, stopChan)
}

func (c *Controller) RunRenewer(ctx context.Context, metricsSuccess chan<- bool) {
	c.log.Info("Starting server in mode renewer")
	c.GetLock("lock-injector-renewer")
	prometheus.IsLeader.WithLabelValues(c.Lock.LeaseMeta.GetName()).Set(0)
	le := leadership.NewLeaderElector(c.Lock, c.Cfg, c.PodName, c.Clientset, RenewTokenJobWrapper)
	go le.RunLeaderElection(ctx, stopChan)
	metricsService := prometheus.NewService(metricsSuccess)
	go metricsService.RunMetrics()
	hcService := healthcheck.NewService(c.Cfg)
	hcService.RegisterHandlers()
	go hcService.Start(ctx, stopChan)
}

func (c *Controller) RunRevoker(ctx context.Context, metricsSuccess chan<- bool) {
	c.log.Info("Starting server in mode revoker")
	c.GetLock("lock-injector-revoker")
	prometheus.IsLeader.WithLabelValues(c.Lock.LeaseMeta.GetName()).Set(0)
	le := leadership.NewLeaderElector(c.Lock, c.Cfg, c.PodName, c.Clientset, RevokeTokenJobWrapper)
	go le.RunLeaderElection(ctx, stopChan)
	metricsService := prometheus.NewService(metricsSuccess)
	go metricsService.RunMetrics()
	hcService := healthcheck.NewService(c.Cfg)
	hcService.RegisterHandlers()
	go hcService.Start(ctx, stopChan)
}

func (c *Controller) GetLock(lockName string) {
	var podNamespace string
	var err error

	// Properly assign the value to c.PodName and initialize podNamespace and err.
	c.PodName, podNamespace, err = config.GetHAEnvs()

	if err != nil {
		c.log.Fatalf("%s", err)
	}

	// Now c.PodName contains the value from GetHAEnvs and can be used to get a new lock.
	c.Lock = leadership.GetNewLock(c.Clientset.CoordinationV1(), lockName, c.PodName, podNamespace)
}

func RenewTokenJobWrapper(ctx context.Context, stopChan chan struct{}, cfg *config.Config, clientset k8s.KubernetesClient) {
	tri := renewer.NewTokenRenewor(cfg, clientset, stopChan)
	tri.RenewTokenJob(ctx)
}

func RevokeTokenJobWrapper(ctx context.Context, stopChan chan struct{}, cfg *config.Config, clientset k8s.KubernetesClient) {
	tri := revoker.NewTokenRevokor(cfg, clientset, stopChan)
	tri.RevokeTokenJob(ctx)
}
