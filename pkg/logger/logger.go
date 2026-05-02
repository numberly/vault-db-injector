package logger

import (
	"fmt"
	"os"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/sirupsen/logrus"
)

type Logger interface {
	Trace(args ...any)
	Tracef(format string, args ...any)
	Info(args ...any)
	Infof(format string, args ...any)
	Debug(args ...any)
	Debugf(format string, args ...any)
	Print(args ...any)
	Printf(format string, args ...any)
	Warn(args ...any)
	Warnf(format string, args ...any)
	Error(args ...any)
	Errorf(format string, args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	WithFields(fields logrus.Fields) *logrus.Entry
}

var logPtr atomic.Pointer[logrus.Logger]
var _ Logger = (*logrus.Logger)(nil)

func init() {
	logPtr.Store(logrus.New())
}

type LogrusWriter struct{}

type SentryHook struct {
	levels     []logrus.Level
	sampleRate float64
}

func NewSentryHook(sampleRate float64) *SentryHook {
	return &SentryHook{
		levels: []logrus.Level{
			logrus.PanicLevel,
			logrus.FatalLevel,
			logrus.ErrorLevel,
		},
		sampleRate: sampleRate,
	}
}

func (hook *SentryHook) Levels() []logrus.Level {
	return hook.levels
}

func (hook *SentryHook) Fire(entry *logrus.Entry) error {
	if sentry.CurrentHub().Client() == nil {
		return nil
	}

	event := sentry.NewEvent()
	event.Message = entry.Message
	event.Level = sentryLevel(entry.Level)
	event.Extra = entry.Data

	// Add error if present
	if err, ok := entry.Data[logrus.ErrorKey].(error); ok {
		event.Exception = []sentry.Exception{{
			Value:      err.Error(),
			Type:       typeof(err),
			Stacktrace: sentry.ExtractStacktrace(err),
		}}
	}

	sentry.CaptureEvent(event)
	return nil
}

func typeof(v any) string {
	return fmt.Sprintf("%T", v)
}

func sentryLevel(level logrus.Level) sentry.Level {
	switch level {
	case logrus.PanicLevel:
		return sentry.LevelFatal
	case logrus.FatalLevel:
		return sentry.LevelFatal
	case logrus.ErrorLevel:
		return sentry.LevelError
	case logrus.WarnLevel:
		return sentry.LevelWarning
	case logrus.InfoLevel:
		return sentry.LevelInfo
	default:
		return sentry.LevelDebug
	}
}

// This will permit to fetch leaderelection log and use logrus with good formatting to forward them
func (w LogrusWriter) Write(p []byte) (n int, err error) {
	message := string(p)
	log := logPtr.Load()

	errorRegex := regexp.MustCompile(`(?i)\berror\b`)
	warnRegex := regexp.MustCompile(`(?i)\bwarn\b|warning`)

	switch {
	case errorRegex.MatchString(message):
		log.Error(message)
	case warnRegex.MatchString(message):
		log.Warn(message)
	default:
		log.Info(message)
	}
	return len(p), nil
}

func Initialize(cfg config.Config) {
	newLogger := logrus.New()
	newLogger.Out = os.Stdout
	newLogger.Formatter = &logrus.JSONFormatter{
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyMsg: "message",
		},
		TimestampFormat: time.RFC3339,
	}
	level, err := config.GetLogLevel(cfg.LogLevel)
	if err != nil {
		newLogger.Warnf("unknown log level %q, defaulting to info: %v", cfg.LogLevel, err)
		level = logrus.InfoLevel
	}
	newLogger.SetLevel(level)

	if cfg.Sentry {
		newLogger.AddHook(NewSentryHook(cfg.SentrySampleRate))
	}
	logPtr.Store(newLogger)
}

func GetLogger() Logger {
	return logPtr.Load()
}

func GetEntry() *logrus.Entry {
	return logrus.NewEntry(logPtr.Load())
}

func ResetLogger() {
	logPtr.Store(logrus.New())
}
