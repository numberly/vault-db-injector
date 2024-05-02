package prometheus_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/config"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/logger"
	prom "gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/prometheus"

	"github.com/stretchr/testify/assert"
)

func initTestLogger() {
	// Example configuration setup for testing
	testConfig := config.Config{
		LogLevel: "info", // Or whatever log level is appropriate for testing
	}

	// Initialize the logger with the test configuration
	logger.Initialize(testConfig)
}

func TestRunMetrics(t *testing.T) {
	// Initialize logger for testing
	initTestLogger()

	// Create a channel to receive the success signal
	successChan := make(chan bool, 1)

	// Initialize the metric service
	service := prom.NewService(successChan)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	go service.RunMetrics()

	select {
	case success := <-successChan:
		assert.True(t, success, "RunMetrics should send true into successChan")
	case <-ctx.Done():
		t.Fatal("Test timed out waiting for RunMetrics to send success signal")
	}

	serverURL := "http://" + "127.0.0.1:8080" + "/metrics"
	resp, err := http.Get(serverURL)
	if err != nil {
		t.Fatalf("Failed to make a request to the server: %v", err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Expected status %d, got %d, body: %s", http.StatusOK, resp.StatusCode, string(bodyBytes))
}
