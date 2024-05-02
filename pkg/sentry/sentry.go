package sentry

import (
	"time"

	"github.com/getsentry/sentry-go"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/logger"
)

type SentryService interface {
	StartSentry()
}

type SentryImpl struct {
	dsn           string
	sentryEnabled bool
	log           logger.Logger
}

func NewSentry(dsn string, sentryEnabled bool) *SentryImpl {
	return &SentryImpl{
		dsn:           dsn,
		sentryEnabled: sentryEnabled,
		log:           logger.GetLogger(),
	}
}

// recordSentry initializes the Sentry client.
func (s *SentryImpl) recordSentry(dsn string) {
	err := sentry.Init(sentry.ClientOptions{
		Dsn: s.dsn,
		// You can set other options here.
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
