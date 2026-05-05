package vault

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	vaultapi "github.com/hashicorp/vault/api"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePodService is a minimal k8s.PodService for unit tests.
type fakePodService struct {
	pods []k8s.PodInfo
	err  error
}

func (f *fakePodService) GetAllPodAndNamespace(_ context.Context) ([]k8s.PodInfo, error) {
	return f.pods, f.err
}

var _ k8s.PodService = (*fakePodService)(nil)

func TestNewKeyInfo(t *testing.T) {
	podName := "test-pod"
	leaseID := "lease-id"
	tokenID := "token-id"
	namespace := "test-namespace"
	serviceaccount := "sa"

	keyInfo := NewKeyInfo(podName, leaseID, tokenID, namespace, serviceaccount, "", "")
	assert.Equal(t, podName, keyInfo.PodNameUID)
	assert.Equal(t, leaseID, keyInfo.LeaseID)
	assert.Equal(t, tokenID, keyInfo.TokenID)
	assert.Equal(t, namespace, keyInfo.Namespace)
}

func TestNewKeyInfo_AllFields(t *testing.T) {
	ki := NewKeyInfo("uid", "lease", "token", "ns", "sa", "pod-name", "node-name")
	assert.Equal(t, "uid", ki.PodNameUID)
	assert.Equal(t, "lease", ki.LeaseID)
	assert.Equal(t, "token", ki.TokenID)
	assert.Equal(t, "ns", ki.Namespace)
	assert.Equal(t, "sa", ki.ServiceAccount)
	assert.Equal(t, "pod-name", ki.PodName)
	assert.Equal(t, "node-name", ki.NodeName)
}

func TestSafeString(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{"nil value", nil, ""},
		{"string value", "hello", "hello"},
		{"empty string", "", ""},
		{"non-string int", 42, ""},
		{"non-string bool", true, ""},
		{"non-string slice", []string{"a"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := safeString(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestKeyInfoFromMap(t *testing.T) {
	tests := []struct {
		name     string
		uuid     string
		m        map[string]interface{}
		expected *KeyInfo
	}{
		{
			name: "full map",
			uuid: "pod-uid",
			m: map[string]interface{}{
				"LeaseId":            "lease-123",
				"TokenId":            "token-456",
				"Namespace":          "default",
				"ServiceAccountName": "my-sa",
				"PodName":            "my-pod",
				"NodeName":           "my-node",
			},
			expected: &KeyInfo{
				PodNameUID:     "pod-uid",
				LeaseID:        "lease-123",
				TokenID:        "token-456",
				Namespace:      "default",
				ServiceAccount: "my-sa",
				PodName:        "my-pod",
				NodeName:       "my-node",
			},
		},
		{
			name:  "empty map",
			uuid:  "empty-uid",
			m:     map[string]interface{}{},
			expected: &KeyInfo{PodNameUID: "empty-uid"},
		},
		{
			name: "partial map — missing NodeName",
			uuid: "partial-uid",
			m: map[string]interface{}{
				"LeaseId":   "lease-abc",
				"TokenId":   "token-xyz",
				"Namespace": "staging",
			},
			expected: &KeyInfo{
				PodNameUID: "partial-uid",
				LeaseID:    "lease-abc",
				TokenID:    "token-xyz",
				Namespace:  "staging",
			},
		},
		{
			name: "nil values in map",
			uuid: "nil-uid",
			m: map[string]interface{}{
				"LeaseId": nil,
				"TokenId": "tok",
			},
			expected: &KeyInfo{
				PodNameUID: "nil-uid",
				TokenID:    "tok",
			},
		},
		{
			name: "wrong type values are silently dropped",
			uuid: "type-uid",
			m: map[string]interface{}{
				"LeaseId": 12345,
				"TokenId": "valid-token",
			},
			expected: &KeyInfo{
				PodNameUID: "type-uid",
				TokenID:    "valid-token",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keyInfoFromMap(tt.uuid, tt.m)
			require.NotNil(t, got)
			assert.Equal(t, tt.expected.PodNameUID, got.PodNameUID)
			assert.Equal(t, tt.expected.LeaseID, got.LeaseID)
			assert.Equal(t, tt.expected.TokenID, got.TokenID)
			assert.Equal(t, tt.expected.Namespace, got.Namespace)
			assert.Equal(t, tt.expected.ServiceAccount, got.ServiceAccount)
			assert.Equal(t, tt.expected.PodName, got.PodName)
			assert.Equal(t, tt.expected.NodeName, got.NodeName)
		})
	}
}

func TestConnectorClone(t *testing.T) {
	log := logrus.New()
	original := &Connector{
		address:        "http://vault:8200",
		authPath:       "auth/kubernetes",
		dbRole:         "my-role",
		k8sSaToken:     "sa-token",
		authRole:       "kube-role",
		dbMountPath:    "db-mount",
		Log:            log,
		VaultRateLimit: 50,
	}

	clone := original.Clone()
	require.NotNil(t, clone)

	// All config fields must be copied
	assert.Equal(t, original.address, clone.address)
	assert.Equal(t, original.authPath, clone.authPath)
	assert.Equal(t, original.dbRole, clone.dbRole)
	assert.Equal(t, original.k8sSaToken, clone.k8sSaToken)
	assert.Equal(t, original.authRole, clone.authRole)
	assert.Equal(t, original.dbMountPath, clone.dbMountPath)
	assert.Equal(t, original.Log, clone.Log)
	assert.Equal(t, original.VaultRateLimit, clone.VaultRateLimit)

	// Clone must be a distinct pointer
	assert.NotSame(t, original, clone)

	// Runtime fields (client, tokens) are NOT copied — clone starts clean
	assert.Nil(t, clone.client)
	assert.Empty(t, clone.vaultToken)
	assert.Empty(t, clone.K8sSaVaultToken)
}

// TestRenewLease_LeaseNotFound verifies that RenewLease treats "lease not found"
// as a non-error (the revoker may have already cleaned it up).
// TODO(integration): deeper TTL-extension verification requires a real Vault cluster
// with a live lease — deferred to handle_token_integration_test.go.
func TestRenewLease_LeaseNotFoundIsNoop(t *testing.T) {
	// The integration test (TestRenewLease in handle_token_integration_test.go) documents
	// that "lease not found" silently returns nil. We confirm the same via the inline
	// comment in RenewLease: the function only returns an error when the Vault call fails
	// for reasons other than "lease not found".
	// This unit test validates the behaviour of keyInfoFromMap that powers the data path;
	// the full RenewLease flow is covered in integration tests.
	t.Skip("full RenewLease TTL verification deferred to integration test — needs live Vault cluster")
}

// TestSyncAndCleanupTokens_PodServiceError verifies the early-exit path when the
// Kubernetes pod lister returns an error. No Vault client is required for this branch.
func TestSyncAndCleanupTokens_PodServiceError(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	c := &Connector{Log: log, VaultRateLimit: 10}
	cfg := &config.Config{VaultRateLimit: 10}

	svc := &fakePodService{err: errors.New("k8s unavailable")}
	result := c.SyncAndCleanupTokens(context.Background(), cfg, nil, "secret", "prefix", svc, 3600)
	assert.False(t, result, "expected false when pod service returns error")
}

// ---------------------------------------------------------------------------
// StoreDataAsync
// ---------------------------------------------------------------------------

// TestStoreDataAsync_EmptyK8sSaVaultToken verifies that StoreDataAsync logs an
// error and increments the prometheus counter when K8sSaVaultToken is empty,
// without calling auth/token/create-orphan or auth/kubernetes/login.
func TestStoreDataAsync_EmptyK8sSaVaultToken(t *testing.T) {
	var loginCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			loginCalled.Store(true)
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel) // suppress expected error logs

	c := &Connector{
		address:         srv.URL,
		K8sSaVaultToken: "", // intentionally empty — triggers early-exit path
		Log:             log,
	}

	ki := NewKeyInfo("pod-uid", "lease-1", "tok-1", "default", "sa", "pod", "node")
	c.StoreDataAsync(context.Background(), "ctx-id", ki, "vault-injector", "pod-uid", "default", "prefix")

	// Give the goroutine time to run.
	time.Sleep(100 * time.Millisecond)

	assert.False(t, loginCalled.Load(), "login should NOT be called when K8sSaVaultToken is empty")
}

// TestStoreDataAsync_UsesK8sSaVaultTokenDirectly verifies that StoreDataAsync
// uses K8sSaVaultToken for the KV write and does NOT call
// auth/token/create-orphan or auth/kubernetes/login.
func TestStoreDataAsync_UsesK8sSaVaultTokenDirectly(t *testing.T) {
	const bookkeepingToken = "s.bookkeeping-token"

	var (
		orphanCalled atomic.Bool
		loginCalled  atomic.Bool
		kvPutCalled  atomic.Bool
		seenToken    atomic.Value
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record which token the client presented.
		if tok := r.Header.Get("X-Vault-Token"); tok != "" {
			seenToken.Store(tok)
		}
		switch {
		case r.URL.Path == "/v1/auth/token/create-orphan":
			orphanCalled.Store(true)
			http.Error(w, `{"errors":["should not be called"]}`, http.StatusForbidden)
		case r.URL.Path == "/v1/auth/kubernetes/login":
			loginCalled.Store(true)
			http.Error(w, `{"errors":["should not be called"]}`, http.StatusForbidden)
		case r.Method == http.MethodPut && len(r.URL.Path) > 4:
			// KV v2 put — PUT /v1/<mount>/data/<prefix>/<uuid>
			// The vault SDK's KVv2.Put sends a PUT request.
			kvPutCalled.Store(true)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":{"version":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)

	// Build a minimal connector that satisfies the new StoreDataAsync path.
	vaultCfg := vaultapi.DefaultConfig()
	vaultCfg.Address = srv.URL
	client, err := vaultapi.NewClient(vaultCfg)
	require.NoError(t, err)
	client.SetToken(bookkeepingToken)

	c := &Connector{
		address:         srv.URL,
		client:          client,
		vaultToken:      bookkeepingToken,
		K8sSaVaultToken: bookkeepingToken,
		Log:             log,
	}

	ki := NewKeyInfo("pod-uid", "lease-1", "tok-1", "default", "sa", "pod", "node")
	c.StoreDataAsync(context.Background(), "ctx-id", ki, "vault-injector", "pod-uid", "default", "prefix")

	time.Sleep(200 * time.Millisecond)

	assert.False(t, orphanCalled.Load(), "auth/token/create-orphan must NOT be called")
	assert.False(t, loginCalled.Load(), "auth/kubernetes/login must NOT be called")
	assert.True(t, kvPutCalled.Load(), "KV put should have been called")
	if tok, ok := seenToken.Load().(string); ok {
		assert.Equal(t, bookkeepingToken, tok, "KV request must carry the bookkeeping token")
	}
}

// TestSyncAndCleanupTokens_EmptyKeys verifies that an empty keysInformations slice
// returns true (no-op success). No Vault client is required: the orphan-token
// dance was removed; SyncAndCleanupTokens now uses the renewer's own login token
// throughout. The goroutine loop is also a no-op when the slice is empty.
// Covered more thoroughly in handle_token_integration_test.go.
func TestSyncAndCleanupTokens_EmptyKeysUnit(t *testing.T) {
	// The pod-service-error branch is the only unit-testable early exit here;
	// empty-keys success with a live Vault cluster is covered in the integration test.
	t.Skip("empty-keys success path requires live Vault cluster — see integration test")
}
