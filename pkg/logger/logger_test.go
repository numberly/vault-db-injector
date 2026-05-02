package logger

import (
	"bytes"
	"testing"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// TestInitializeLogger checks if the logger is correctly initialized with the given configuration.
func TestInitializeLogger(t *testing.T) {
	// Define a buffer to capture log output during the test
	var buf bytes.Buffer

	// Create a sample config
	cfg := config.Config{
		LogLevel: "debug",
	}

	// Initialize should set the logger according to this configuration
	Initialize(cfg)

	logInstance := GetLogger()

	// After calling Initialize, logInstance should not be nil
	assert.NotNil(t, logInstance, "Expected logInstance to be non-nil after initialization")

	// Type assert to *logrus.Logger to inspect its internal state
	logrusLogger, ok := logInstance.(*logrus.Logger)
	assert.True(t, ok, "Expected logInstance to be of type *logrus.Logger")

	// Set output to buffer and perform a log action
	logrusLogger.Out = &buf
	logrusLogger.Info("Testing log output")

	// Check if log output is as expected
	logOutput := buf.String()
	assert.Contains(t, logOutput, "Testing log output", "Log output should contain the test message")
	assert.Contains(t, logOutput, `"level":"info"`, "Log level should be info")

	// Check that the log level is set correctly according to cfg.LogLevel
	assert.Equal(t, logrus.DebugLevel, logrusLogger.GetLevel(), "Log level should be set to debug")
}

// TestGetLoggerWithoutInitialization checks that GetLogger never returns nil, even before Initialize is called.
// logInstance is always set to a default logrus.Logger at package init time.
func TestGetLoggerWithoutInitialization(t *testing.T) {
	ResetLogger()

	logInstance := GetLogger()

	assert.NotNil(t, logInstance, "Expected logInstance to be non-nil even before explicit initialization")
}
