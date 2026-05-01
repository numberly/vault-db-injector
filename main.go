package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/controller"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/sentry"

	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

func main() {
	errChan := make(chan error)
	runSuccess := make(chan bool)
	metricsSuccess := make(chan bool)
	cfgFile := flag.String("config", "", "The config file to use.")
	flag.Parse()
	cfg, err := config.NewConfig(*cfgFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Could not parse config file: %s", err)
		os.Exit(1)
	}
	logger.Initialize(*cfg)
	log := logger.GetLogger()
	sentryService := sentry.NewSentry(cfg.SentryDsn, cfg.Sentry, cfg.SentryEnvironment)
	sentryService.StartSentry()

	k8sClient := k8s.NewClient()
	rawClientset, err := k8sClient.GetKubernetesClient()
	if err != nil {
		log.Fatalf("Unable to create Kubernetes client error = %v", err)
	}
	clientset := k8s.NewKubernetesClientAdapter(rawClientset)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := controller.NewController(cfg, clientset, sentryService)

	switch cfg.Mode {
	case config.ModeInjector:
		go c.RunInjector(ctx, errChan, runSuccess)
	case config.ModeRenewer:
		go c.RunRenewer(ctx, metricsSuccess)
	case config.ModeRevoker:
		go c.RunRevoker(ctx, metricsSuccess)
	case config.ModeAll:
		go c.RunInjector(ctx, errChan, runSuccess)
		go c.RunRenewer(ctx, metricsSuccess)
		go c.RunRevoker(ctx, metricsSuccess)
	}

	// Attendez le succès ou l'échec des fonctions run et runMetrics
	successCount := 0
	for {
		select {
		case err := <-errChan:
			log.Errorf("error running app: %s", err)
			os.Exit(1)
		case <-runSuccess:
			successCount++
		case <-metricsSuccess:
			successCount++
		}
	}
}
