package logger

import (
	"os"
	"regexp"
	"time"

	"github.com/sirupsen/logrus"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/config"
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
