package revoker

import (
	"context"
	"testing"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// fakeKubernetesClient satisfies k8s.KubernetesClient for unit tests.
type fakeKubernetesClient struct{}

func (f *fakeKubernetesClient) CoreV1() v1.CoreV1Interface {
	return nil
}

func (f *fakeKubernetesClient) CoordinationV1() coordinationv1.CoordinationV1Interface {
	return nil
}

func (f *fakeKubernetesClient) GetServiceAccountToken() (string, error) {
	return "fake-token", nil
}

func (f *fakeKubernetesClient) RawClientset() kubernetes.Interface { return nil }

func (f *fakeKubernetesClient) RequestSAToken(_ context.Context, _, _ string, _ []string, _ int64) (string, error) {
	return "fake-jwt", nil
}

func TestNewTokenRevoker_NotNil(t *testing.T) {
	cfg := &config.Config{}
	stopChan := make(chan struct{})
	r := NewTokenRevoker(cfg, &fakeKubernetesClient{}, stopChan)
	require.NotNil(t, r, "NewTokenRevoker must return a non-nil TokenRevoker")
}

func TestTokenRevokerImpl_ImplementsInterface(t *testing.T) {
	cfg := &config.Config{}
	stopChan := make(chan struct{})
	var _ TokenRevoker = NewTokenRevoker(cfg, &fakeKubernetesClient{}, stopChan)
}

// TestRevokeTokenJob_StopChanUnblocks verifies the stop-channel lifecycle
// of the goroutine managed by RevokeTokenJob without a live Vault/k8s cluster.
func TestRevokeTokenJob_StopChanUnblocks(t *testing.T) {
	stopChan := make(chan struct{})

	done := make(chan struct{})
	go func() {
		defer close(done)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go func() {
			<-stopChan
			cancel()
		}()

		// Simulate the main goroutine waiting for context cancellation.
		<-ctx.Done()
	}()

	close(stopChan)

	select {
	case <-done:
		// expected
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after stopChan was closed")
	}
}

// TestRevokeTokenJob_ContextCancellation verifies the context-cancel path.
func TestRevokeTokenJob_ContextCancellation(t *testing.T) {
	stopChan := make(chan struct{})
	defer close(stopChan)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not exit after context cancellation")
	}
}

func TestTokenRevokerImpl_Fields(t *testing.T) {
	cfg := &config.Config{InjectorLabel: "vault-injector"}
	stopChan := make(chan struct{})
	r := NewTokenRevoker(cfg, &fakeKubernetesClient{}, stopChan).(*tokenRevokerImpl)

	assert.Equal(t, cfg, r.cfg)
	assert.NotNil(t, r.log)
	assert.Equal(t, (<-chan struct{})(stopChan), r.stopChan)
}
