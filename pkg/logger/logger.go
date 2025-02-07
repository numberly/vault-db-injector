package logger

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/sirupsen/logrus"
)

type Logger interface {
	Trace(args ...interface{})
	Tracef(format string, args ...interface{})
	Info(args ...interface{})
	Infof(format string, args ...interface{})
	Debug(args ...interface{})
	Debugf(format string, args ...interface{})
	Print(args ...interface{})
	Printf(format string, args ...interface{})
	Warn(args ...interface{})
	Warnf(format string, args ...interface{})
	Error(args ...interface{})
	Errorf(format string, args ...interface{})
	Fatal(args ...interface{})
	Fatalf(format string, args ...interface{})
}

var logInstance *logrus.Logger
var _ Logger = (*logrus.Logger)(nil)

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

func typeof(v interface{}) string {
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

	errorRegex := regexp.MustCompile(`(?i)\berror\b`)
	warnRegex := regexp.MustCompile(`(?i)\bwarn\b|warning`)

	switch {
	case errorRegex.MatchString(message):
		logInstance.Error(message) // Use logInstance instead of logrus.Error
	case warnRegex.MatchString(message):
		logInstance.Warn(message) // Use logInstance instead of logrus.Warn
	default:
		logInstance.Info(message) // Use logInstance instead of logrus.Info
	}
	return len(p), nil
}

func Initialize(cfg config.Config) {
	if logInstance == nil {
		logInstance = logrus.New()
		logInstance.Out = os.Stdout
		logInstance.Formatter = &logrus.JSONFormatter{
			FieldMap: logrus.FieldMap{
				logrus.FieldKeyMsg: "message",
			},
			TimestampFormat: time.RFC3339,
		}
		logInstance.SetLevel(config.GetLogLevel(cfg.LogLevel))

		if cfg.Sentry {
			logInstance.AddHook(NewSentryHook(cfg.SentrySampleRate))
		}
	}
}
func GetLogger() Logger {
	return logInstance
}

func GetEntry() *logrus.Entry {
	return logrus.NewEntry(logInstance)
}
func ResetLogger() {
	logInstance = nil
}
