package healthcheck

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/vault"
	"github.com/undefinedlabs/go-mpatch"
	"k8s.io/client-go/kubernetes"
)

var stopChan = make(chan struct{})

// MockK8sClient implements k8s.ClientInterface for testing
type MockK8sClient struct {
	shouldFail bool
}

func (m *MockK8sClient) GetServiceAccountToken() (string, error) {
	return "mock-token", nil
}

func (m *MockK8sClient) GetKubernetesCACert() (*x509.CertPool, error) {
	return nil, nil
}

func (m *MockK8sClient) GetKubernetesClient() (*kubernetes.Clientset, error) {
	if m.shouldFail {
		return nil, fmt.Errorf("mock k8s connection failed")
	}
	return &kubernetes.Clientset{}, nil
}

func setupTestService(k8sShouldFail bool) (*Service, *mpatch.Patch) {
	cfg := &config.Config{
		LogLevel:      "info",
		VaultAddress:  "http://vault:8200",
		VaultAuthPath: "auth/kubernetes",
		KubeRole:      "my-role",
	}

	service := NewService(cfg)
	service.k8sClient = &MockK8sClient{shouldFail: k8sShouldFail}

	return service, nil
}

func TestHealthzHandler(t *testing.T) {
	tests := []struct {
		name            string
		k8sShouldFail   bool
		vaultShouldFail bool
		expectedStatus  int
		expectedBody    map[string]interface{}
	}{
		// Your existing test cases...
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create patch for vault.ConnectToVault and Connector.CheckHealth
			connectPatch, err := mpatch.PatchMethod(vault.ConnectToVault, func(ctx context.Context, cfg *config.Config) (*vault.Connector, error) {
				if tt.vaultShouldFail {
					return nil, fmt.Errorf("mock vault connection failed")
				}
				// Return a real connector
				return &vault.Connector{}, nil
			})
			if err != nil {
				t.Fatalf("Failed to patch vault.ConnectToVault: %v", err)
			}
			defer connectPatch.Unpatch()

			// Patch the CheckHealth method
			checkHealthPatch, err := mpatch.PatchInstanceMethodByName(reflect.TypeOf(&vault.Connector{}), "CheckHealth", func(c *vault.Connector, ctx context.Context) error {
				if tt.vaultShouldFail {
					return fmt.Errorf("mock vault connection failed")
				}
				return nil
			})
			if err != nil {
				t.Fatalf("Failed to patch Connector.CheckHealth: %v", err)
			}
			defer checkHealthPatch.Unpatch()

			service, _ := setupTestService(tt.k8sShouldFail)

			req := httptest.NewRequest("GET", "/healthz", nil)
			w := httptest.NewRecorder()

			service.healthHandler(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			var response HealthStatus
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("Failed to decode response body: %v", err)
			}

			if response.Status != tt.expectedBody["status"] {
				t.Errorf("expected status %s, got %s", tt.expectedBody["status"], response.Status)
			}

			// Verify timestamp exists and is in correct format
			if _, err := time.Parse(time.RFC3339, response.Timestamp); err != nil {
				t.Errorf("invalid timestamp format: %v", err)
			}
		})
	}
}
func TestReadyzHandler(t *testing.T) {
	tests := []struct {
		name           string
		isReady        bool
		expectedStatus int
		expectedBody   map[string]interface{}
	}{
		{
			name:           "Service ready",
			isReady:        true,
			expectedStatus: http.StatusOK,
			expectedBody: map[string]interface{}{
				"status": "ready",
			},
		},
		{
			name:           "Service not ready",
			isReady:        false,
			expectedStatus: http.StatusServiceUnavailable,
			expectedBody: map[string]interface{}{
				"status": "not ready",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, _ := setupTestService(false)
			service.isReady.Store(tt.isReady)

			req := httptest.NewRequest("GET", "/readyz", nil)
			w := httptest.NewRecorder()

			handler := service.readyzHandler()
			handler(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			var response HealthStatus
			if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
				t.Fatalf("Failed to decode response body: %v", err)
			}

			if response.Status != tt.expectedBody["status"] {
				t.Errorf("expected status %s, got %s", tt.expectedBody["status"], response.Status)
			}

			// Verify timestamp exists and is in correct format
			if _, err := time.Parse(time.RFC3339, response.Timestamp); err != nil {
				t.Errorf("invalid timestamp format: %v", err)
			}
		})
	}
}

func initTestLogger() {
	testConfig := config.Config{
		LogLevel: "info",
	}
	logger.Initialize(testConfig)
}

func TestServiceShutdown(t *testing.T) {
	initTestLogger()

	service, _ := setupTestService(false)
	mux := http.NewServeMux()
	service.server = &http.Server{
		Addr:         "127.0.0.1:8888",
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	service.RegisterHandlers()

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		if err := service.Start(ctx, stopChan); err != nil {
			t.Logf("Service stopped with error: %v", err)
		} else {
			t.Log("Service stopped successfully")
		}
	}()

	time.Sleep(1 * time.Second)
	cancel()
	time.Sleep(1 * time.Second)

	_, err := http.Get("http://" + service.server.Addr + "/healthz")

	if err == nil || !isConnectionRefusedError(err) {
		t.Errorf("Expected server to be shutdown, but request succeeded or failed with unexpected error: %v", err)
	}
}

func isConnectionRefusedError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "connect: connection refused")
}
