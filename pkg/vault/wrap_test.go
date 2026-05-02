package vault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hashicorp/vault/api"
)

func newStubVault(t *testing.T, handler http.HandlerFunc) *api.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := api.DefaultConfig()
	cfg.Address = srv.URL
	c, err := api.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestWrapValues_Success(t *testing.T) {
	client := newStubVault(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sys/wrapping/wrap" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("X-Vault-Wrap-TTL"); got != "5m0s" && got != "300" {
			t.Fatalf("wrap-ttl header = %q, want 5m0s or 300", got)
		}
		_, _ = w.Write([]byte(`{
			"wrap_info": {
				"token": "hvs.test",
				"ttl": 300,
				"creation_time": "2026-05-02T00:00:00Z",
				"creation_path": "sys/wrapping/wrap"
			}
		}`))
	})
	c := &Connector{client: client}
	tok, err := c.WrapValues(context.Background(), map[string]string{"k": "v"}, 5*time.Minute)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if tok != "hvs.test" {
		t.Fatalf("token = %q, want hvs.test", tok)
	}
}

func TestUnwrapValues_Success(t *testing.T) {
	client := newStubVault(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sys/wrapping/unwrap" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		// The Vault SDK sends the wrap token either as X-Vault-Token (when the
		// client has no current token) or in the JSON body as {"token": "..."}.
		// Accept both forms so the stub is not brittle to SDK internals.
		tokenInHeader := r.Header.Get("X-Vault-Token")
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		tokenInBody := body["token"]
		if tokenInHeader != "hvs.test" && tokenInBody != "hvs.test" {
			http.Error(w, "bad token", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{
			"data": {"username": "alice", "password": "secret"}
		}`))
	})
	// Set a different current token so the SDK sends the wrap token in the
	// request body rather than using it as the client's auth token.
	client.SetToken("current-token")
	c := &Connector{client: client}
	got, err := c.UnwrapValues(context.Background(), "hvs.test")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got["username"] != "alice" || got["password"] != "secret" {
		t.Fatalf("unexpected map: %#v", got)
	}
}

func TestWrapValues_VaultError(t *testing.T) {
	client := newStubVault(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors":["wrap denied"]}`, http.StatusForbidden)
	})
	c := &Connector{client: client}
	_, err := c.WrapValues(context.Background(), map[string]string{"k": "v"}, time.Minute)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
