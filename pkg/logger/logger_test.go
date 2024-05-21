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

// TestGetLoggerWithoutInitialization checks if GetLogger returns a nil or uninitialized logger when called before initialization.
func TestGetLoggerWithoutInitialization(t *testing.T) {
	// Resetting logInstance to simulate the situation before Initialization
	// Note: This can affect other tests if run in parallel or if tests depend on state.
	// In a real-world scenario, this should be handled more carefully or by using dependency injection.
	ResetLogger() // Assuming ResetLogger sets logInstance to nil; this function would need to be implemented.

	logInstance := GetLogger()

	// Before calling Initialize, logInstance should be nil
	assert.Nil(t, logInstance, "Expected logInstance to be nil before initialization")
}
