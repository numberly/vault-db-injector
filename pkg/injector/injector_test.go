package injector

import (
	"testing"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/sentry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// noopSentry is a minimal SentryService that discards all events.
type noopSentry struct{}

func (n *noopSentry) StartSentry()            {}
func (n *noopSentry) CaptureError(_ error)    {}
func (n *noopSentry) CaptureMessage(_ string) {}

var _ sentry.SentryService = (*noopSentry)(nil)

// TestNewWebhookStarter_NotNil verifies that the constructor returns a non-nil Starter.
func TestNewWebhookStarter_NotNil(t *testing.T) {
	cfg := &config.Config{
		CertFile: "/tmp/nonexistent.crt",
		KeyFile:  "/tmp/nonexistent.key",
	}

	s := NewWebhookStarter(cfg, &noopSentry{})
	require.NotNil(t, s, "NewWebhookStarter must return a non-nil Starter")
}

// TestNewWebhookStarter_ImplementsInterface verifies that the returned value implements Starter.
func TestNewWebhookStarter_ImplementsInterface(t *testing.T) {
	cfg := &config.Config{}

	var _ Starter = NewWebhookStarter(cfg, &noopSentry{}) //nolint:staticcheck // QF1011: explicit type is intentional interface assertion
}

// TestNewWebhookStarter_ConfigIsStored verifies that the config passed to the constructor
// is accessible on the concrete type (regression guard for nil-config crashes).
func TestNewWebhookStarter_ConfigIsStored(t *testing.T) {
	cfg := &config.Config{CertFile: "test.crt", KeyFile: "test.key"}

	s := NewWebhookStarter(cfg, &noopSentry{})
	impl, ok := s.(*starterImpl)
	require.True(t, ok, "NewWebhookStarter must return a *starterImpl")
	assert.Equal(t, cfg.CertFile, impl.cfg.CertFile)
	assert.Equal(t, cfg.KeyFile, impl.cfg.KeyFile)
	assert.NotNil(t, impl.log, "log must be initialized by constructor")
	assert.NotNil(t, impl.sentry, "sentry must be initialized by constructor")
}

// TestSentryRecoveryMiddleware_ReturnsHandler verifies that the middleware wraps a handler
// and returns a non-nil http.Handler.
func TestSentryRecoveryMiddleware_ReturnsHandler(t *testing.T) {
	middleware := SentryRecoveryMiddleware(&noopSentry{})
	require.NotNil(t, middleware, "SentryRecoveryMiddleware must return a non-nil middleware func")
}

// Guards the invariants documented on the webhook timeout constants: the
// apiserver caps webhook timeoutSeconds at 30s and client-go keeps idle
// connections for 90s. Dropping below either reintroduces random EOF /
// "use of closed network connection" on admission calls.
func TestWebhookServerTimeouts(t *testing.T) {
	const (
		apiserverMaxWebhookTimeout = 30 * time.Second
		clientGoIdleConnTimeout    = 90 * time.Second
	)
	if webhookReadTimeout < apiserverMaxWebhookTimeout {
		t.Errorf("webhookReadTimeout %v < apiserver max webhook timeout %v", webhookReadTimeout, apiserverMaxWebhookTimeout)
	}
	if webhookWriteTimeout < apiserverMaxWebhookTimeout {
		t.Errorf("webhookWriteTimeout %v < apiserver max webhook timeout %v", webhookWriteTimeout, apiserverMaxWebhookTimeout)
	}
	if webhookIdleTimeout <= clientGoIdleConnTimeout {
		t.Errorf("webhookIdleTimeout %v must be > client-go IdleConnTimeout %v", webhookIdleTimeout, clientGoIdleConnTimeout)
	}
}
