package healthcheck

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/vault"
)

// HealthStatusValue is the typed status string for health responses.
type HealthStatusValue string

const (
	StatusHealthy     HealthStatusValue = "healthy"
	StatusUnhealthy   HealthStatusValue = "unhealthy"
	StatusReady       HealthStatusValue = "ready"
	StatusNotReady    HealthStatusValue = "not ready"
)

type HealthStatus struct {
	Status     HealthStatusValue `json:"status"`
	Kubernetes *ServiceHealth    `json:"kubernetes,omitempty"`
	Vault      *ServiceHealth    `json:"vault,omitempty"`
	Timestamp  string            `json:"timestamp"`
}

type ServiceHealth struct {
	Status  HealthStatusValue `json:"status"`
	Message string            `json:"message,omitempty"`
}

type Service struct {
	isReady      *atomic.Value
	mux          *http.ServeMux
	server       *http.Server
	log          logger.Logger
	cfg          *config.Config
	k8sClient    k8s.ClientInterface
	livenessFn   func() bool
}

func NewService(cfg *config.Config) *Service {
	isReady := &atomic.Value{}
	isReady.Store(true)
	mux := http.NewServeMux()

	return &Service{
		isReady: isReady,
		mux:     mux,
		server: &http.Server{
			Addr:         "0.0.0.0:8888",
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		log:       logger.GetLogger(),
		cfg:       cfg,
		k8sClient: k8s.NewClient(),
	}
}

// RegisterLivenessProbe sets the liveness probe function and registers the /live
// endpoint on the service's private mux. Call before RegisterHandlers.
func (s *Service) RegisterLivenessProbe(probeFn func() bool) {
	s.livenessFn = probeFn
}

func (s *Service) RegisterHandlers() {
	s.mux.HandleFunc("/healthz", s.healthHandler)
	s.mux.HandleFunc("/readyz", s.readyzHandler())
	if s.livenessFn != nil {
		s.mux.HandleFunc("/live", s.livenessHandler)
	}
}

func (s *Service) livenessHandler(w http.ResponseWriter, _ *http.Request) {
	if s.livenessFn() {
		if _, err := w.Write([]byte("alive")); err != nil {
			s.log.Errorf("Failed to write liveness response: %s", err)
		}
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
}

func (s *Service) checkKubernetesHealth() *ServiceHealth {
	_, err := s.k8sClient.GetKubernetesClient()
	if err != nil {
		return &ServiceHealth{
			Status:  StatusUnhealthy,
			Message: "Failed to connect to Kubernetes: " + err.Error(),
		}
	}
	return &ServiceHealth{
		Status: StatusHealthy,
	}
}

func (s *Service) checkVaultHealth(ctx context.Context) *ServiceHealth {
	if err := vault.CheckVaultConnectivity(ctx, s.cfg.VaultAddress); err != nil {
		return &ServiceHealth{
			Status:  StatusUnhealthy,
			Message: err.Error(),
		}
	}
	return &ServiceHealth{
		Status: StatusHealthy,
	}
}

func (s *Service) healthHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	health := HealthStatus{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}

	// Check both services
	k8sHealth := s.checkKubernetesHealth()
	vaultHealth := s.checkVaultHealth(ctx)

	health.Kubernetes = k8sHealth
	health.Vault = vaultHealth

	w.Header().Set("Content-Type", "application/json")

	if k8sHealth.Status == StatusHealthy && vaultHealth.Status == StatusHealthy {
		health.Status = StatusHealthy
		w.WriteHeader(http.StatusOK)
	} else {
		health.Status = StatusUnhealthy
		var statusCode int

		switch {
		case k8sHealth.Status != StatusHealthy && vaultHealth.Status != StatusHealthy:
			statusCode = http.StatusServiceUnavailable
		case k8sHealth.Status != StatusHealthy:
			statusCode = http.StatusBadGateway
		case vaultHealth.Status != StatusHealthy:
			statusCode = http.StatusFailedDependency
		}

		w.WriteHeader(statusCode)
	}

	if err := json.NewEncoder(w).Encode(health); err != nil {
		s.log.Errorf("Failed to encode health status: %v", err)
	}
}

func (s *Service) readyzHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		response := HealthStatus{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}

		if !s.isReady.Load().(bool) {
			response.Status = StatusNotReady
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(response) //nolint:errcheck,gosec // G104: write to http.ResponseWriter; error unactionable
			return
		}

		response.Status = StatusReady
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response) //nolint:errcheck,gosec // G104: write to http.ResponseWriter; error unactionable
	}
}

// Start runs the health check HTTP server until ctx is cancelled or the server stops.
// Errors are logged internally; the lifecycle is fire-and-forget via goroutine at call sites.
func (s *Service) Start(ctx context.Context, doneCh chan struct{}) {
	go func() {
		s.log.Info("Listening for health checks on :8888")
		if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
			s.log.Errorf("Error serving health check: %v", err)
		}
		close(doneCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		s.log.Info("Context canceled, shutting down health check server")
		if err := s.server.Shutdown(shutdownCtx); err != nil {
			s.log.Errorf("Error shutting down health check server: %v", err)
		}
	case <-doneCh:
		if err := s.server.Shutdown(ctx); err != nil {
			s.log.Errorf("Error shutting down health check server: %v", err)
		}
		s.log.Info("Health check server has stopped")
	}
}
