package vault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	vaultapi "github.com/hashicorp/vault/api"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vaultHealthResponse matches the JSON structure returned by /v1/sys/health.
type vaultHealthResponse struct {
	Initialized bool   `json:"initialized"`
	Sealed      bool   `json:"sealed"`
	Version     string `json:"version"`
}

// stubVaultServer creates an httptest.Server that returns the provided health
// response and HTTP status code on GET /v1/sys/health.
func stubVaultServer(t *testing.T, statusCode int, body vaultHealthResponse) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ---------------------------------------------------------------------------
// CheckVaultConnectivity
// ---------------------------------------------------------------------------

func TestCheckVaultConnectivity_Healthy(t *testing.T) {
	srv := stubVaultServer(t, http.StatusOK, vaultHealthResponse{
		Initialized: true,
		Sealed:      false,
	})

	err := CheckVaultConnectivity(context.Background(), srv.URL)
	require.NoError(t, err)
}

func TestCheckVaultConnectivity_Sealed(t *testing.T) {
	// Vault returns 503 when sealed.
	srv := stubVaultServer(t, http.StatusServiceUnavailable, vaultHealthResponse{
		Initialized: true,
		Sealed:      true,
	})

	err := CheckVaultConnectivity(context.Background(), srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sealed")
}

func TestCheckVaultConnectivity_NotInitialized_SDK501(t *testing.T) {
	// Vault returns 501 when not initialized. The SDK treats this as an HTTP
	// error before our code can inspect the body, so we only assert non-nil.
	srv := stubVaultServer(t, http.StatusNotImplemented, vaultHealthResponse{
		Initialized: false,
		Sealed:      false,
	})

	err := CheckVaultConnectivity(context.Background(), srv.URL)
	require.Error(t, err)
}

func TestCheckVaultConnectivity_NotInitialized_200Body(t *testing.T) {
	// Simulate a 200 response with initialized=false to exercise the
	// !health.Initialized branch in CheckHealth.
	srv := stubVaultServer(t, http.StatusOK, vaultHealthResponse{
		Initialized: false,
		Sealed:      false,
		Version:     "1.14.0",
	})

	err := CheckVaultConnectivity(context.Background(), srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initialized")
}

func TestCheckVaultConnectivity_BadURL(t *testing.T) {
	// An invalid address should fail when creating the client or making the request.
	err := CheckVaultConnectivity(context.Background(), "http://127.0.0.1:0")
	require.Error(t, err)
}

func TestCheckVaultConnectivity_5xx(t *testing.T) {
	// A 500 response is an error (not a recognized Vault health code).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errors":["internal error"]}`))
	}))
	t.Cleanup(srv.Close)

	err := CheckVaultConnectivity(context.Background(), srv.URL)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// LoginAsInjectorSA
// ---------------------------------------------------------------------------

// stubVaultK8sLoginServer creates an httptest.Server that responds to
// PUT /v1/auth/kubernetes/login with a fake Vault token (statusCode 200).
// The Vault SDK's Logical().WriteWithContext sends a PUT, not POST.
// All other paths return 404 so tests fail loudly if unexpected calls are made.
func stubVaultK8sLoginServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/v1/auth/kubernetes/login" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			// The vault SDK's ParseSecret uses json.Decoder with UseNumber().
			// Auth.LeaseDuration is int so int literal is fine here.
			resp := map[string]any{
				"request_id":   "test-req-id",
				"lease_id":     "",
				"renewable":    false,
				"lease_duration": 0,
				"auth": map[string]any{
					"client_token":   token,
					"accessor":       "test-accessor",
					"policies":       []string{"vault-db-injector"},
					"metadata":       map[string]any{},
					"lease_duration": 3600,
					"renewable":      true,
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginAsInjectorSA_EmptyToken(t *testing.T) {
	cfg := &config.Config{
		VaultAddress:  "http://localhost:8200",
		VaultAuthPath: "kubernetes",
		KubeRole:      "vault-db-injector",
	}
	_, err := LoginAsInjectorSA(context.Background(), cfg, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty k8s SA token")
}

func TestLoginAsInjectorSA_LoginFailure(t *testing.T) {
	// Server that always returns 403 for login.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		VaultAddress:  srv.URL,
		VaultAuthPath: "kubernetes",
		KubeRole:      "vault-db-injector",
	}
	_, err := LoginAsInjectorSA(context.Background(), cfg, "sa-jwt-token", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vault login as injector SA")
}

func TestLoginAsInjectorSA_Success(t *testing.T) {
	const wantToken = "s.injectorBookkeepingToken"
	srv := stubVaultK8sLoginServer(t, wantToken)

	cfg := &config.Config{
		VaultAddress:  srv.URL,
		VaultAuthPath: "kubernetes",
		KubeRole:      "vault-db-injector",
	}
	got, err := LoginAsInjectorSA(context.Background(), cfg, "sa-jwt-token", "")
	require.NoError(t, err)
	assert.Equal(t, wantToken, got)
}

// ---------------------------------------------------------------------------
// sliceToStrings
// ---------------------------------------------------------------------------

func TestSliceToStrings_Strings(t *testing.T) {
	in := []any{"a", "b", "c"}
	got, err := sliceToStrings(in)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, got)
}

func TestSliceToStrings_Mixed(t *testing.T) {
	in := []any{"hello", 42, int64(7), float64(3.14), true}
	got, err := sliceToStrings(in)
	require.NoError(t, err)
	assert.Len(t, got, 5)
	assert.Equal(t, "hello", got[0])
}

func TestSliceToStrings_Empty(t *testing.T) {
	got, err := sliceToStrings(nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestSliceToStrings_InvalidType(t *testing.T) {
	in := []any{"ok", []string{"nested"}}
	_, err := sliceToStrings(in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected type")
}

// ---------------------------------------------------------------------------
// SetToken / GetToken symmetry
// ---------------------------------------------------------------------------

func TestSetGetToken_Symmetry(t *testing.T) {
	// Build a minimal connector with a real vault.Client so SetToken doesn't
	// nil-deref on c.client.SetToken. We do NOT call Login (which requires k8s auth).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c := NewConnector(srv.URL, "auth/kubernetes", "my-role", "db", "db-role", "sa-token", 10)

	// Manually initialise the vault client without doing full Login.
	vaultCfg := vaultapi.DefaultConfig()
	vaultCfg.Address = srv.URL
	client, err := vaultapi.NewClient(vaultCfg)
	require.NoError(t, err)
	c.client = client

	const tok = "s.testtoken123"
	c.SetToken(tok)
	assert.Equal(t, tok, c.GetToken())
}
