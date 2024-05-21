package healthcheck

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/numberly/vault-db-injector/pkg/leadership"
	"github.com/numberly/vault-db-injector/pkg/logger"
)

type HealthChecker interface {
	RegisterHandlers()
	Start() error
}

type Service struct {
	isReady *atomic.Value
	server  *http.Server
	log     logger.Logger
}

func NewService() *Service {
	isReady := &atomic.Value{}
	isReady.Store(true) // Initialize as ready

	return &Service{
		isReady: isReady,
		server: &http.Server{
			Addr:         "0.0.0.0:8888",
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		log: logger.GetLogger(),
	}
}

// RegisterHandlers sets up the HTTP endpoints for the health check service.
func (s *Service) RegisterHandlers() {
	http.HandleFunc("/healthz", s.healthzHandler)
	http.HandleFunc("/readyz", s.readyzHandler())
	hcle := leadership.NewHealthChecker()
	hcle.SetupLivenessEndpoint()
}

// Start begins listening for health check requests.
func (s *Service) Start(ctx context.Context, doneCh chan struct{}) error {
	// Start the server in a separate goroutine.
	go func() {
		s.log.Info("Listening for health checks on :8888")
		if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
			// Log the error if it's not ErrServerClosed, as we expect this error on shutdown.
			s.log.Errorf("Error serving health check: %v", err)
		}
		close(doneCh) // Signal that the server has stopped.
	}()

	// Wait for context cancellation or server stop signal.
	select {
	case <-ctx.Done():
		// Context was canceled, shut down the server.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		s.log.Info("Context canceled, shutting down health check server")
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			s.log.Errorf("Error shutting down health check server: %v", err)
			return err // Return error if shutdown fails.
		}
	case <-doneCh:
		if err := s.server.Shutdown(ctx); err != nil {
			s.log.Errorf("Error shutting down health check server: %v", err)
			return err // Return error if shutdown fails.
		}
		s.log.Info("Health check server has stopped")
	}
	return nil // Return nil as the service stopped cleanly or was shutdown on context cancel.
}

func (s *Service) healthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) readyzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if s.isReady == nil || !s.isReady.Load().(bool) {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
