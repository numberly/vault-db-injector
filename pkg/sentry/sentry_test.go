package sentry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NewSentry constructor
// ---------------------------------------------------------------------------

func TestNewSentry_NotNil(t *testing.T) {
	s := NewSentry("https://key@sentry.io/123", false, "production")
	require.NotNil(t, s)
}

func TestNewSentry_EmptyDsn(t *testing.T) {
	s := NewSentry("", false, "staging")
	require.NotNil(t, s)
	assert.False(t, s.sentryEnabled)
}

func TestNewSentry_Fields(t *testing.T) {
	s := NewSentry("https://key@sentry.io/456", true, "staging")
	assert.Equal(t, "https://key@sentry.io/456", s.dsn)
	assert.Equal(t, "staging", s.environment)
	assert.True(t, s.sentryEnabled)
	assert.NotNil(t, s.log)
}

// ---------------------------------------------------------------------------
// StartSentry — disabled path is a no-op (does not call initSentry/SDK)
// ---------------------------------------------------------------------------

func TestStartSentry_Disabled_NoOp(t *testing.T) {
	s := NewSentry("", false, "production")
	// Must not panic and must not attempt to initialize the real Sentry SDK.
	assert.NotPanics(t, func() {
		s.StartSentry()
	})
}

func TestStartSentry_Enabled_ValidDsn(t *testing.T) {
	// Use a syntactically valid but unreachable DSN.
	// sentry.Init succeeds even with an unreachable endpoint; it queues events
	// asynchronously — no HTTP call happens during Init itself.
	s := NewSentry("https://0000000000000000000000000000000000000000000000000000000000000000@o0.ingest.sentry.io/0", true, "test")
	assert.NotPanics(t, func() {
		s.StartSentry()
	})
}

// ---------------------------------------------------------------------------
// CaptureError — disabled path must not panic even with nil error
// ---------------------------------------------------------------------------

func TestCaptureError_Disabled_NilError(t *testing.T) {
	s := NewSentry("", false, "production")
	assert.NotPanics(t, func() {
		s.CaptureError(nil)
	})
}

func TestCaptureError_Disabled_RealError(t *testing.T) {
	s := NewSentry("", false, "production")
	assert.NotPanics(t, func() {
		s.CaptureError(errors.New("something went wrong"))
	})
}

// ---------------------------------------------------------------------------
// CaptureMessage — disabled path must not panic even with empty string
// ---------------------------------------------------------------------------

func TestCaptureMessage_Disabled_EmptyString(t *testing.T) {
	s := NewSentry("", false, "production")
	assert.NotPanics(t, func() {
		s.CaptureMessage("")
	})
}

func TestCaptureMessage_Disabled_Message(t *testing.T) {
	s := NewSentry("", false, "production")
	assert.NotPanics(t, func() {
		s.CaptureMessage("hello world")
	})
}

// ---------------------------------------------------------------------------
// SentryRecoveryMiddleware — wraps handler, recovers from panics
// ---------------------------------------------------------------------------

// SentryRecoveryMiddleware is a convenience HTTP middleware that recovers from
// panics and reports them. We define it inline here to test the pattern without
// importing the real Sentry SDK in tests.

func SentryRecoveryMiddleware(s SentryService, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				var err error
				switch v := rec.(type) {
				case error:
					err = v
				default:
					err = errors.New("panic recovered")
				}
				s.CaptureError(err)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func TestSentryRecoveryMiddleware_NoPanic(t *testing.T) {
	s := NewSentry("", false, "production")
	handler := SentryRecoveryMiddleware(s, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestSentryRecoveryMiddleware_Panic(t *testing.T) {
	s := NewSentry("", false, "production")
	handler := SentryRecoveryMiddleware(s, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	assert.NotPanics(t, func() {
		handler.ServeHTTP(rec, req)
	})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSentryRecoveryMiddleware_PanicError(t *testing.T) {
	s := NewSentry("", false, "production")
	handler := SentryRecoveryMiddleware(s, http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(errors.New("explicit error panic"))
	}))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	assert.NotPanics(t, func() {
		handler.ServeHTTP(rec, req)
	})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ---------------------------------------------------------------------------
// Enabled path — calls real Sentry SDK methods but without a real DSN.
// The SDK initialised with an invalid/empty DSN is a no-op at runtime.
// ---------------------------------------------------------------------------

func TestCaptureError_Enabled_NilError_NoInit(t *testing.T) {
	// sentryEnabled=true but no initSentry called → SDK client is nil → no-op
	s := &SentryImpl{sentryEnabled: true, log: newDiscardLogger()}
	assert.NotPanics(t, func() {
		s.CaptureError(nil)
	})
}

func TestCaptureError_Enabled_RealError_NoInit(t *testing.T) {
	s := &SentryImpl{sentryEnabled: true, log: newDiscardLogger()}
	assert.NotPanics(t, func() {
		s.CaptureError(errors.New("something"))
	})
}

func TestCaptureMessage_Enabled_NoInit(t *testing.T) {
	s := &SentryImpl{sentryEnabled: true, log: newDiscardLogger()}
	assert.NotPanics(t, func() {
		s.CaptureMessage("test message")
	})
}

// newDiscardLogger returns a logger.Logger backed by logrus that discards output.
func newDiscardLogger() *discardLogger { return &discardLogger{} }

// discardLogger is a minimal logger.Logger implementation that discards all output.
type discardLogger struct{}

func (d *discardLogger) Trace(...any)                                     {}
func (d *discardLogger) Tracef(string, ...any)                            {}
func (d *discardLogger) Info(...any)                                      {}
func (d *discardLogger) Infof(string, ...any)                             {}
func (d *discardLogger) Debug(...any)                                     {}
func (d *discardLogger) Debugf(string, ...any)                            {}
func (d *discardLogger) Print(...any)                                     {}
func (d *discardLogger) Printf(string, ...any)                            {}
func (d *discardLogger) Warn(...any)                                      {}
func (d *discardLogger) Warnf(string, ...any)                             {}
func (d *discardLogger) Error(...any)                                     {}
func (d *discardLogger) Errorf(string, ...any)                            {}
func (d *discardLogger) Fatal(...any)                                     {}
func (d *discardLogger) Fatalf(string, ...any)                            {}
func (d *discardLogger) WithFields(f logrus.Fields) *logrus.Entry {
	return logrus.NewEntry(logrus.New())
}

// Compile-time check: SentryImpl implements SentryService.
var _ SentryService = (*SentryImpl)(nil)
