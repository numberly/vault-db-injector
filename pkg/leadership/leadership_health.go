package leadership

import (
	"net/http"

	"github.com/numberly/vault-db-injector/pkg/logger"
)

type HealthChecker interface {
	SetupLivenessEndpoint()
}

var _ HealthChecker = (*healthCheckerImpl)(nil)

type healthCheckerImpl struct {
	log logger.Logger
}

func NewHealthChecker() HealthChecker {
	return &healthCheckerImpl{
		log: logger.GetLogger(),
	}
}

func (hc *healthCheckerImpl) SetupLivenessEndpoint() {
	http.HandleFunc("/live", func(w http.ResponseWriter, r *http.Request) {
		hc.healthz(w, r) // Calls the method on the healthCheckerImpl instance
	})
}

func (hc *healthCheckerImpl) healthz(w http.ResponseWriter, req *http.Request) {
	// Use hc.log instead of the global logger
	hc.log.Info("Checking liveness") // Example usage

	m.Lock()
	defer m.Unlock()
	if healthy {
		if _, err := w.Write([]byte("alive")); err != nil {
			hc.log.Errorf("Failed to write liveness response: %s", err)
		}
		return
	}

	w.WriteHeader(http.StatusInternalServerError)
}
