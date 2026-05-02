package controller

import (
	"context"

	"github.com/cockroachdb/errors"
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
	"golang.org/x/sync/errgroup"
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

// RunInjector starts the webhook injector and blocks until ctx is cancelled or a fatal error occurs.
func (c *Controller) RunInjector(ctx context.Context) error {
	c.log.Info("Starting server in mode injector")

	stopChan := make(chan struct{})
	// Bridge ctx cancellation to stopChan for components that still use it.
	context.AfterFunc(ctx, func() { close(stopChan) })

	is := injector.NewWebhookStarter(c.Cfg, c.sentry)
	hcService := healthcheck.NewService(c.Cfg)
	hcService.RegisterHandlers()

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		hcService.Start(gCtx, make(chan struct{}))
		return nil
	})

	g.Go(func() error {
		if err := is.StartWebhook(gCtx, stopChan); err != nil {
			return errors.Wrap(err, "webhook starter failed")
		}
		// StartWebhook is non-blocking; wait for context cancellation.
		<-gCtx.Done()
		return nil
	})

	return g.Wait()
}

// RunRenewer starts the token renewer with leader election and blocks until ctx is cancelled or a fatal error occurs.
func (c *Controller) RunRenewer(ctx context.Context) error {
	c.log.Info("Starting server in mode renewer")

	stopChan := make(chan struct{})
	podName, lock, err := c.buildLock("lock-injector-renewer")
	if err != nil {
		return errors.Wrap(err, "failed to build leader election lock")
	}
	metrics.IsLeader.WithLabelValues(lock.LeaseMeta.GetName()).Set(0)

	clientset := c.Clientset
	cfg := c.Cfg
	le := leadership.NewLeaderElector(lock, podName, func(ctx context.Context, stopChan chan struct{}) {
		renewer.NewTokenRenewer(cfg, clientset, stopChan).RenewTokenJob(ctx)
	})

	hcService := healthcheck.NewService(c.Cfg)
	hcService.RegisterLivenessProbe(le.IsHealthy)
	hcService.RegisterHandlers()
	metricsService := metrics.NewMetricsService()

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		le.RunLeaderElection(gCtx, stopChan)
		return nil
	})

	g.Go(func() error {
		metricsService.RunMetrics()
		return nil
	})

	g.Go(func() error {
		hcService.Start(gCtx, make(chan struct{}))
		return nil
	})

	return g.Wait()
}

// RunRevoker starts the token revoker with leader election and blocks until ctx is cancelled or a fatal error occurs.
func (c *Controller) RunRevoker(ctx context.Context) error {
	c.log.Info("Starting server in mode revoker")

	stopChan := make(chan struct{})
	podName, lock, err := c.buildLock("lock-injector-revoker")
	if err != nil {
		return errors.Wrap(err, "failed to build leader election lock")
	}
	metrics.IsLeader.WithLabelValues(lock.LeaseMeta.GetName()).Set(0)

	clientset := c.Clientset
	cfg := c.Cfg
	le := leadership.NewLeaderElector(lock, podName, func(ctx context.Context, stopChan chan struct{}) {
		revoker.NewTokenRevoker(cfg, clientset, stopChan).RevokeTokenJob(ctx)
	})

	hcService := healthcheck.NewService(c.Cfg)
	hcService.RegisterLivenessProbe(le.IsHealthy)
	hcService.RegisterHandlers()
	metricsService := metrics.NewMetricsService()

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		le.RunLeaderElection(gCtx, stopChan)
		return nil
	})

	g.Go(func() error {
		metricsService.RunMetrics()
		return nil
	})

	g.Go(func() error {
		hcService.Start(gCtx, make(chan struct{}))
		return nil
	})

	return g.Wait()
}

// RunBPF runs the binary as a node-local DaemonSet that loads the BPF
// substitution program and watches local pods to populate the BPF maps.
//
// The kernel-coupled body lives in pkg/bpf and is delegated to via
// runBPFAgent (build-tagged: linux vs other). This entry point performs
// the no-op idle path when BPF is disabled by config, so the binary still
// runs in mode=bpf without crashing — useful for cluster operators
// staging the rollout.
func (c *Controller) RunBPF(ctx context.Context) error {
	c.log.Info("Starting server in mode bpf")
	if !c.Cfg.BPF.Enabled {
		c.log.Warn("RunBPF called but cfg.BPF.Enabled is false; idle until shutdown")
		<-ctx.Done()
		return nil
	}
	return runBPFAgent(ctx, c.Cfg, c.Clientset, c.log)
}

// buildLock resolves HA environment variables and constructs the leader-election lock.
func (c *Controller) buildLock(lockName string) (string, *resourcelock.LeaseLock, error) {
	podName, podNamespace, err := config.GetHAEnvs()
	if err != nil {
		return "", nil, err
	}
	return podName, leadership.NewLock(c.Clientset.CoordinationV1(), lockName, podName, podNamespace), nil
}
