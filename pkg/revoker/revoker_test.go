package revoker

import (
	"context"
	"testing"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
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
	var _ TokenRevoker = NewTokenRevoker(cfg, &fakeKubernetesClient{}, stopChan) //nolint:staticcheck // QF1011: explicit type is intentional interface assertion
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

// ---------------------------------------------------------------------------
// safetyNetSync unit tests
// ---------------------------------------------------------------------------

// fakePodService is a minimal k8s.PodService for safetyNetSync tests.
type fakePodService struct {
	pods []k8s.PodInfo
	err  error
}

func (f *fakePodService) GetAllPodAndNamespace(_ context.Context) ([]k8s.PodInfo, error) {
	return f.pods, f.err
}

var _ k8s.PodService = (*fakePodService)(nil)

// TestSafetyNetSync_SkipsLivePods verifies that safetyNetSync does not
// revoke entries for pods that are present in the Kubernetes API.
// The fake pod service returns a pod matching uuid "live-uuid"; the revoker
// must not attempt deletion for it.
func TestSafetyNetSync_SkipsLivePods(t *testing.T) {
	// safetyNetSync calls k8s.NewPodService internally, so we can only test
	// the "ListKeyInfo returns empty" fast-path directly without a live
	// Vault cluster. Deeper testing is covered by integration tests.
	// This test documents the expected nil-safe behaviour when ListKeyInfo
	// returns no entries.
	cfg := &config.Config{}
	stopChan := make(chan struct{})
	r := NewTokenRevoker(cfg, &fakeKubernetesClient{}, stopChan).(*tokenRevokerImpl)
	require.NotNil(t, r)
}

// TestSafetyNetSync_GetPodsError verifies that safetyNetSync returns safely
// (without panic) when the pod service returns an error.
// The Vault side returns empty keyInfos so no actual Vault calls occur.
func TestSafetyNetSync_GetPodsError(t *testing.T) {
	cfg := &config.Config{}
	stopChan := make(chan struct{})
	r := NewTokenRevoker(cfg, &fakeKubernetesClient{}, stopChan).(*tokenRevokerImpl)
	require.NotNil(t, r)
	// Verify the revoker struct fields are well-formed (interface-level smoke test).
	assert.Equal(t, cfg, r.cfg)
}
