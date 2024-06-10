package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
)

var stopChan = make(chan struct{})

func initTestLogger() {
	// Example configuration setup for testing
	testConfig := config.Config{
		LogLevel: "info", // Or whatever log level is appropriate for testing
	}

	// Initialize the logger with the test configuration
	logger.Initialize(testConfig)
}

// TestHealthzHandler tests the health check handler.
func TestHealthzHandler(t *testing.T) {
	// Assuming your logger has been initialized elsewhere or doing it here if needed
	// logger.Initialize(yourConfigHere)

	service := NewService()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", service.healthzHandler)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status %d, got %d", http.StatusNoContent, w.Code)
	}
}

// TestReadyzHandler tests the readiness check handler.
func TestReadyzHandler(t *testing.T) {
	// Assuming your logger has been initialized elsewhere or doing it here if needed

	service := NewService()

	mux := http.NewServeMux()
	mux.HandleFunc("/readyz", service.readyzHandler())
	mux.HandleFunc("/healthz", service.healthzHandler)

	// Test when the service is ready
	reqReady := httptest.NewRequest("GET", "/readyz", nil)
	wReady := httptest.NewRecorder()
	mux.ServeHTTP(wReady, reqReady)

	if wReady.Code != http.StatusNoContent {
		t.Errorf("expected status %d when ready, got %d", http.StatusNoContent, wReady.Code)
	}

	// Test when the service is not ready
	service.isReady.Store(false)
	reqNotReady := httptest.NewRequest("GET", "/readyz", nil)
	wNotReady := httptest.NewRecorder()
	mux.ServeHTTP(wNotReady, reqNotReady)

	if wNotReady.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status %d when not ready, got %d", http.StatusServiceUnavailable, wNotReady.Code)
	}
}

func TestServiceShutdown(t *testing.T) {
	initTestLogger() // Ensure the logger is initialized if it's required for the test

	// Create a new service and register handlers
	service := NewService()

	mux := http.NewServeMux()
	service.server = &http.Server{
		Addr:         "127.0.0.1:8888",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	service.RegisterHandlers()

	// Create a cancelable context
	ctx, cancel := context.WithCancel(context.Background())

	// Start the service in a goroutine
	go func() {
		if err := service.Start(ctx, stopChan); err != nil {
			t.Logf("Service stopped with error: %v", err)
		} else {
			t.Log("Service stopped successfully")
		}
	}()

	// Wait a bit to ensure the server has started
	time.Sleep(1 * time.Second)

	// Cancel the context, triggering the server shutdown
	cancel()

	// Wait a bit to ensure the shutdown process has a chance to complete
	time.Sleep(1 * time.Second)

	// Attempt to make a request to the server
	_, err := http.Get("http://" + service.server.Addr + "/healthz")

	// If the server has shut down correctly, the request should fail
	if err == nil || !isConnectionRefusedError(err) {
		t.Errorf("Expected server to be shutdown, but request succeeded or failed with unexpected error: %v", err)
	}
}

// isConnectionRefusedError checks if the error is a "connection refused" error.
func isConnectionRefusedError(err error) bool {
	// This is a simplistic check. In a real test, you might want to refine this
	// to more accurately detect the specific error.
	return err != nil && strings.Contains(err.Error(), "connect: connection refused")
}
