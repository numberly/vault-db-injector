package sentry

import (
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/numberly/vault-db-injector/pkg/logger"
)

type SentryService interface {
	StartSentry()
	CaptureError(err error)
	CaptureMessage(msg string)
}

func (s *SentryImpl) CaptureError(err error) {
	if s.sentryEnabled {
		eventID := sentry.CaptureException(err)
		if eventID != nil {
			s.log.Debugf("Captured error event: %s", *eventID)
		}
		sentry.Flush(2 * time.Second)
	}
}

func (s *SentryImpl) CaptureMessage(msg string) {
	if s.sentryEnabled {
		eventID := sentry.CaptureMessage(msg)
		if eventID != nil {
			s.log.Debugf("Captured message event: %s", *eventID)
		}
		sentry.Flush(2 * time.Second)
	}
}

type SentryImpl struct {
	dsn           string
	environment   string
	sentryEnabled bool
	log           logger.Logger
}

func NewSentry(dsn string, sentryEnabled bool, environment string) *SentryImpl {
	return &SentryImpl{
		dsn:           dsn,
		environment:   environment,
		sentryEnabled: sentryEnabled,
		log:           logger.GetLogger(),
	}
}

// recordSentry initializes the Sentry client.
func (s *SentryImpl) recordSentry(dsn string) {
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              s.dsn,
		Environment:      s.environment,
		Debug:            true,
		TracesSampleRate: 1.0,
		EnableTracing:    true,
	})
	if err != nil {
		s.log.Fatalf("Sentry initialization failed: %v", err)
	}
	defer sentry.Flush(2 * time.Second)
	s.log.Infof("Connected to Sentry on DSN %s", dsn)
}

// StartSentry checks if Sentry is enabled in the configuration and initiates it.
func (s *SentryImpl) StartSentry() {
	if s.sentryEnabled {
		s.recordSentry(s.dsn)
	} else {
		s.log.Info("Sentry is not enabled")
	}
}
