package k8s_test

import (
	"os"
	"testing"

	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/k8s"
)

// Temporarily replace the tokenFilePath for testing.
func TestGetServiceAccountToken(t *testing.T) {
	// Setup a temporary file to mimic the service account token file.
	tempFile, err := os.CreateTemp("", "fake-token")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name()) // clean up

	expectedToken := "fake-token-content"
	if _, err := tempFile.WriteString(expectedToken); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tempFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	token, err := k8s.GetServiceAccountTokenImpl(tempFile.Name())
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if token != expectedToken {
		t.Errorf("Expected token to be %q, got %q", expectedToken, token)
	}
}
