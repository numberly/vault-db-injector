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

type HealthStatus struct {
	Status     string         `json:"status"`
	Kubernetes *ServiceHealth `json:"kubernetes,omitempty"`
	Vault      *ServiceHealth `json:"vault,omitempty"`
	Timestamp  string         `json:"timestamp"`
}

type ServiceHealth struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type HealthChecker interface {
	RegisterHandlers()
	Start(context.Context, chan struct{}) error
}

type Service struct {
	isReady   *atomic.Value
	server    *http.Server
	log       logger.Logger
	cfg       *config.Config
	k8sClient k8s.ClientInterface
}

func NewService(cfg *config.Config) *Service {
	isReady := &atomic.Value{}
	isReady.Store(true)

	return &Service{
		isReady: isReady,
		server: &http.Server{
			Addr:         "0.0.0.0:8888",
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
		},
		log:       logger.GetLogger(),
		cfg:       cfg,
		k8sClient: k8s.NewClient(),
	}
}

func (s *Service) RegisterHandlers() {
	http.HandleFunc("/healthz", s.healthHandler)
	http.HandleFunc("/readyz", s.readyzHandler())
}

func (s *Service) checkKubernetesHealth() *ServiceHealth {
	_, err := s.k8sClient.GetKubernetesClient()
	if err != nil {
		return &ServiceHealth{
			Status:  "unhealthy",
			Message: "Failed to connect to Kubernetes: " + err.Error(),
		}
	}
	return &ServiceHealth{
		Status: "healthy",
	}
}

func (s *Service) checkVaultHealth(ctx context.Context) *ServiceHealth {
	k8sClient := k8s.NewClient()
	tok, err := k8sClient.GetServiceAccountToken()
	if err != nil {
		return &ServiceHealth{
			Status:  "unhealthy",
			Message: "Failed to get ServiceAccount token: " + err.Error(),
		}
	}

	vaultConn := vault.NewConnector(s.cfg.VaultAddress, s.cfg.VaultAuthPath, s.cfg.KubeRole, "random", "random", tok, s.cfg.VaultRateLimit)

	if err := vaultConn.CheckHealth(ctx); err != nil {
		return &ServiceHealth{
			Status:  "unhealthy",
			Message: err.Error(),
		}
	}

	return &ServiceHealth{
		Status: "healthy",
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

	if k8sHealth.Status == "healthy" && vaultHealth.Status == "healthy" {
		health.Status = "healthy"
		w.WriteHeader(http.StatusOK)
	} else {
		health.Status = "unhealthy"
		var statusCode int

		switch {
		case k8sHealth.Status != "healthy" && vaultHealth.Status != "healthy":
			statusCode = http.StatusServiceUnavailable
		case k8sHealth.Status != "healthy":
			statusCode = http.StatusBadGateway
		case vaultHealth.Status != "healthy":
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

		if s.isReady == nil || !s.isReady.Load().(bool) {
			response.Status = "not ready"
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(response)
			return
		}

		response.Status = "ready"
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(response)
	}
}

func (s *Service) Start(ctx context.Context, doneCh chan struct{}) error {
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
			return err
		}
	case <-doneCh:
		if err := s.server.Shutdown(ctx); err != nil {
			s.log.Errorf("Error shutting down health check server: %v", err)
			return err
		}
		s.log.Info("Health check server has stopped")
	}
	return nil
}
