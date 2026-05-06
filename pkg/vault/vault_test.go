package vault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vaultapi "github.com/hashicorp/vault/api"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newMockConnector(t *testing.T, handler http.HandlerFunc) (*Connector, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)

	vc := vaultapi.DefaultConfig()
	vc.Address = srv.URL
	cli, err := vaultapi.NewClient(vc)
	require.NoError(t, err)
	cli.SetToken("initial-token")

	c := &Connector{
		address:     srv.URL,
		client:      cli,
		vaultToken:  "initial-token",
		dbMountPath: "database",
		dbRole:      "myrole",
		Log:         logger.GetLogger(),
	}
	return c, srv.Close
}

func TestGetDbCredentials_SkipOrphanCreation_doesNotCallCreateOrphan(t *testing.T) {
	createOrphanCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/auth/token/create-orphan":
			createOrphanCalled = true
			w.WriteHeader(http.StatusInternalServerError)
		case strings.HasPrefix(r.URL.Path, "/v1/database/creds/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"lease_id":       "database/creds/myrole/abc",
				"lease_duration": 3600,
				"renewable":      true,
				"data":           map[string]any{"username": "u", "password": "p"},
			})
		default:
			// secret KV writes from StoreDataAsync — accept silently
			w.WriteHeader(http.StatusNoContent)
		}
	})
	c, stop := newMockConnector(t, handler)
	defer stop()

	creds, err := c.GetDbCredentials(context.Background(), DbCredentialsRequest{
		ContextID:          "ctx",
		TTL:                "1h",
		PodNameUID:         "uid",
		Namespace:          "ns",
		SecretName:         "sec",
		Prefix:             "p",
		ServiceAccount:     "sa",
		SkipOrphanCreation: true,
	})
	require.NoError(t, err)
	assert.False(t, createOrphanCalled, "CreateOrphanToken must not be called when SkipOrphanCreation=true")
	assert.Equal(t, "initial-token", creds.DbTokenID, "DbTokenID should be the connector's current pod-token")
	assert.Equal(t, "u", creds.Username)
	assert.Equal(t, "p", creds.Password)
}

func TestGetDbCredentials_LegacyPath_callsCreateOrphan(t *testing.T) {
	createOrphanCalled := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/auth/token/create-orphan":
			createOrphanCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"auth": map[string]any{"client_token": "orphan-tok"},
			})
		case strings.HasPrefix(r.URL.Path, "/v1/database/creds/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"lease_id":       "database/creds/myrole/abc",
				"lease_duration": 3600,
				"renewable":      true,
				"data":           map[string]any{"username": "u", "password": "p"},
			})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	c, stop := newMockConnector(t, handler)
	defer stop()

	creds, err := c.GetDbCredentials(context.Background(), DbCredentialsRequest{
		ContextID: "ctx", TTL: "1h", PodNameUID: "uid", Namespace: "ns",
		SecretName: "sec", Prefix: "p", ServiceAccount: "sa",
	})
	require.NoError(t, err)
	assert.True(t, createOrphanCalled, "CreateOrphanToken must be called in legacy path")
	assert.Equal(t, "orphan-tok", creds.DbTokenID)
}
