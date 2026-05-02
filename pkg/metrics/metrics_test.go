package metrics_test

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
	prom "github.com/numberly/vault-db-injector/pkg/metrics"

	"github.com/stretchr/testify/assert"
)

func initTestLogger() {
	testConfig := config.Config{
		LogLevel: "info",
	}
	logger.Initialize(testConfig)
}

func TestRunMetrics(t *testing.T) {
	initTestLogger()

	service := prom.NewMetricsService()

	// RunMetrics blocks; run it in a goroutine.
	go service.RunMetrics()

	serverURL := "http://127.0.0.1:8080/metrics"
	var resp *http.Response
	var err error

	// Retry until the server is up (up to 5 seconds).
	for range 10 {
		resp, err = http.Get(serverURL)
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if err != nil {
		t.Fatalf("Failed to make a request to the server: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Expected status %d, got %d, body: %s", http.StatusOK, resp.StatusCode, string(bodyBytes))
}
