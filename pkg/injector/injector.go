package injector

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/cockroachdb/errors"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/k8smutator"
	"github.com/numberly/vault-db-injector/pkg/logger"
	promInjector "github.com/numberly/vault-db-injector/pkg/prometheus"
	"github.com/numberly/vault-db-injector/pkg/sentry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	kwhhttp "github.com/slok/kubewebhook/v2/pkg/http"
	kwhlogrus "github.com/slok/kubewebhook/v2/pkg/log/logrus"
	kwhprometheus "github.com/slok/kubewebhook/v2/pkg/metrics/prometheus"
	kwhwebhook "github.com/slok/kubewebhook/v2/pkg/webhook"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"
)

var _ Startor = (*starterImpl)(nil)

type Startor interface {
	StartWebhook(ctx context.Context, stopChan chan struct{}) error
}

type starterImpl struct {
	cfg         *config.Config
	errChan     chan<- error
	successChan chan<- bool
	log         logger.Logger
	sentry      sentry.SentryService
}

func NewWebhookStartor(cfg *config.Config, errChan chan<- error, successChan chan<- bool, sentrySvc sentry.SentryService) Startor {
	return &starterImpl{
		cfg:         cfg,
		errChan:     errChan,
		successChan: successChan,
		log:         logger.GetLogger(),
		sentry:      sentrySvc,
	}
}

func (s *starterImpl) StartWebhook(ctx context.Context, stopChan chan struct{}) error {

	logger := kwhlogrus.NewLogrus(logger.GetEntry())
	k8sClient := k8s.NewClient()

	mt := k8smutator.CreateMutator(ctx, logger, s.cfg)

	// Prepare metrics
	reg := prometheus.NewRegistry()
	promInjector.Init(reg)
	metricsRec, err := kwhprometheus.NewRecorder(kwhprometheus.RecorderConfig{Registry: reg})
	if err != nil {
		close(stopChan)
		s.sentry.CaptureError(err)
		s.log.Fatalf("could not create Prometheus metrics recorder: %v", err)
	}

	// Create webhook
	mcfg := kwhmutating.WebhookConfig{
		ID:      "pod-annotate",
		Mutator: mt,
		Logger:  logger,
	}
	wh, err := kwhmutating.NewWebhook(mcfg)
	if err != nil {
		close(stopChan)
		s.sentry.CaptureError(err)
		return errors.Newf("error creating webhook: %w", err)
	}

	serverCert, err := tls.LoadX509KeyPair(s.cfg.CertFile, s.cfg.KeyFile)
	if err != nil {
		close(stopChan)
		s.sentry.CaptureError(err)
		s.log.Fatalf("Failed to load server certificate: %v", err)
	}

	caCertPool, err := k8sClient.GetKubernetesCACert()
	if err != nil {
		close(stopChan)
		s.sentry.CaptureError(err)
		s.log.Fatalf("Failed to get Kubernetes CA certificate: %v", err)
	}

	certByte, err := os.ReadFile(s.cfg.CertFile)
	if err != nil {
		s.sentry.CaptureError(err)
		logger.Errorf(err.Error())
	}
	caCertPool.AppendCertsFromPEM(certByte)

	// Get HTTP handler from webhook
	whHandler, err := kwhhttp.HandlerFor(kwhhttp.HandlerConfig{
		Webhook: kwhwebhook.NewMeasuredWebhook(metricsRec, wh),
		Logger:  logger,
	})
	if err != nil {
		close(stopChan)
		s.sentry.CaptureError(err)
		return errors.Newf("error creating webhook handler: %w", err)
	}

	// Add Sentry recovery middleware
	wrappedHandler := SentryRecoveryMiddleware(s.sentry)(whHandler)

	// Configurer mTLS
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caCertPool,
	}

	httpServer := &http.Server{
		Addr:         "0.0.0.0:8443",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		TLSConfig:    tlsConfig,
		Handler:      wrappedHandler,
	}

	s.successChan <- true

	errCh := make(chan error)
	// Serve webhook
	go func() {
		logger.Infof("Listening on :8443")
		err = httpServer.ListenAndServeTLS("", "")
		if err != nil {
			s.sentry.CaptureError(err)
			errCh <- errors.Newf("error serving webhook: %w", err)
			close(stopChan)
		}
		errCh <- nil
	}()

	// Serve metrics
	go func() {
		logger.Infof("Listening metrics on :8080")
		err = http.ListenAndServe(":8080", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
		if err != nil {
			s.sentry.CaptureError(err)
			errCh <- errors.Newf("error serving webhook metrics: %w", err)
			close(stopChan)
		}
		errCh <- nil
	}()

	go func() {
		select {
		case err := <-errCh:
			if err != nil {
				s.sentry.CaptureError(err)
				s.log.Errorf("Server error: %v", err)
				close(stopChan)
				s.errChan <- err
			}
		case <-ctx.Done():
			shutdownMess := "Shutting down servers due to context cancellation"
			s.sentry.CaptureMessage(shutdownMess)
			s.log.Info(shutdownMess)
			httpServer.Shutdown(ctx)
			close(stopChan)
			// Shutdown metrics server as well
		}
	}()
	return nil
}

// Add this recovery middleware function
func SentryRecoveryMiddleware(sentrySvc sentry.SentryService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					sentrySvc.CaptureError(fmt.Errorf("panic in webhook handler: %v", err))
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
