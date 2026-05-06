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
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"github.com/numberly/vault-db-injector/pkg/sentry"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	kwhhttp "github.com/slok/kubewebhook/v2/pkg/http"
	kwhlogrus "github.com/slok/kubewebhook/v2/pkg/log/logrus"
	kwhprometheus "github.com/slok/kubewebhook/v2/pkg/metrics/prometheus"
	kwhwebhook "github.com/slok/kubewebhook/v2/pkg/webhook"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"
)

var _ Starter = (*starterImpl)(nil)

type Starter interface {
	StartWebhook(ctx context.Context, stopChan chan struct{}) error
}

type starterImpl struct {
	cfg    *config.Config
	log    logger.Logger
	sentry sentry.SentryService
}

func NewWebhookStarter(cfg *config.Config, sentrySvc sentry.SentryService) Starter {
	return &starterImpl{
		cfg:    cfg,
		log:    logger.GetLogger(),
		sentry: sentrySvc,
	}
}

func (s *starterImpl) StartWebhook(ctx context.Context, stopChan chan struct{}) error {

	whLogger := kwhlogrus.NewLogrus(logger.GetEntry())
	k8sClient := k8s.NewClient()

	mt := k8smutator.CreateMutator(ctx, whLogger, s.cfg)

	// Prepare metrics
	reg := prometheus.NewRegistry()
	metrics.Init(reg)
	metricsRec, err := kwhprometheus.NewRecorder(kwhprometheus.RecorderConfig{Registry: reg})
	if err != nil {
		close(stopChan)
		s.sentry.CaptureError(err)
		return errors.Newf("could not create Prometheus metrics recorder: %w", err)
	}

	// Create webhook
	mcfg := kwhmutating.WebhookConfig{
		ID:      "pod-annotate",
		Mutator: mt,
		Logger:  whLogger,
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
		return errors.Newf("failed to load server certificate: %w", err)
	}

	caCertPool, err := k8sClient.GetKubernetesCACert()
	if err != nil {
		close(stopChan)
		s.sentry.CaptureError(err)
		return errors.Newf("failed to get Kubernetes CA certificate: %w", err)
	}

	certByte, err := os.ReadFile(s.cfg.CertFile)
	if err != nil {
		s.sentry.CaptureError(err)
		whLogger.Errorf(err.Error())
	}
	caCertPool.AppendCertsFromPEM(certByte)

	// Get HTTP handler from webhook
	whHandler, err := kwhhttp.HandlerFor(kwhhttp.HandlerConfig{
		Webhook: kwhwebhook.NewMeasuredWebhook(metricsRec, wh),
		Logger:  whLogger,
	})
	if err != nil {
		close(stopChan)
		s.sentry.CaptureError(err)
		return errors.Newf("error creating webhook handler: %w", err)
	}

	// Add Sentry recovery middleware
	wrappedHandler := SentryRecoveryMiddleware(s.sentry)(whHandler)

	// Configure mTLS
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS12,
	}

	httpServer := &http.Server{
		Addr:         "0.0.0.0:8443",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		TLSConfig:    tlsConfig,
		Handler:      wrappedHandler,
	}

	errCh := make(chan error)
	// Serve webhook
	go func() {
		whLogger.Infof("Listening on :8443")
		err = httpServer.ListenAndServeTLS("", "")
		if err != nil {
			s.sentry.CaptureError(err)
			errCh <- errors.Newf("error serving webhook: %w", err)
			close(stopChan)
		}
		errCh <- nil
	}()

	// Serve metrics
	metricsServer := &http.Server{
		Addr:              ":8080",
		Handler:           promhttp.HandlerFor(reg, promhttp.HandlerOpts{}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	go func() {
		whLogger.Infof("Listening metrics on :8080")
		err = metricsServer.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			s.sentry.CaptureError(err)
			errCh <- errors.Newf("error serving webhook metrics: %w", err)
			close(stopChan)
			return
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
			}
		case <-ctx.Done():
			shutdownMess := "Shutting down servers due to context cancellation"
			s.sentry.CaptureMessage(shutdownMess)
			s.log.Info(shutdownMess)
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := httpServer.Shutdown(shutdownCtx); err != nil {
				s.log.Errorf("webhook server shutdown error: %v", err)
			}
			close(stopChan)
			// Shutdown metrics server as well
		}
	}()
	return nil
}

func SentryRecoveryMiddleware(sentrySvc sentry.SentryService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					sentrySvc.CaptureError(errors.Wrapf(errors.New(fmt.Sprintf("%v", err)), "panic in webhook handler"))
					w.WriteHeader(http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
