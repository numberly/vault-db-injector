package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	sentrygo "github.com/getsentry/sentry-go"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/controller"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/sentry"
	"golang.org/x/sync/errgroup"

	"github.com/cockroachdb/errors"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfgFile := flag.String("config", "", "The config file to use.")
	flag.Parse()

	cfg, err := config.NewConfig(*cfgFile)
	if err != nil {
		return errors.Wrap(err, "could not parse config file")
	}

	logger.Initialize(*cfg)
	log := logger.GetLogger()

	sentryService := sentry.NewSentry(cfg.SentryDsn, cfg.Sentry, cfg.SentryEnvironment)
	sentryService.StartSentry()
	defer sentrygo.Flush(2 * time.Second)

	k8sClient := k8s.NewClient()
	rawClientset, err := k8sClient.GetKubernetesClient()
	if err != nil {
		return errors.Wrap(err, "unable to create Kubernetes client")
	}
	clientset := k8s.NewKubernetesClientAdapter(rawClientset)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	c := controller.NewController(cfg, clientset, sentryService)

	log.Infof("Starting vault-db-injector in mode %q", cfg.Mode)

	var runErr error
	switch cfg.Mode {
	case config.ModeInjector:
		runErr = c.RunInjector(ctx)
	case config.ModeRenewer:
		runErr = c.RunRenewer(ctx)
	case config.ModeRevoker:
		runErr = c.RunRevoker(ctx)
	case config.ModeBPF:
		runErr = c.RunBPF(ctx)
	case config.ModeAll:
		g, gCtx := errgroup.WithContext(ctx)
		g.Go(func() error { return c.RunInjector(gCtx) })
		g.Go(func() error { return c.RunRenewer(gCtx) })
		g.Go(func() error { return c.RunRevoker(gCtx) })
		if cfg.BPF.Enabled {
			g.Go(func() error { return c.RunBPF(gCtx) })
		}
		runErr = g.Wait()
	default:
		return errors.Newf("unknown mode %q", cfg.Mode)
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		log.Errorf("vault-db-injector exiting with error: %v", runErr)
	}
	log.Info("vault-db-injector stopped")

	if errors.Is(runErr, context.Canceled) {
		return nil
	}
	return runErr
}
