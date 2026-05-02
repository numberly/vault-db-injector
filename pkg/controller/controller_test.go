package controller

import (
	"context"
	"testing"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/sentry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

// fakeSentryService satisfies sentry.SentryService without side-effects.
type fakeSentryService struct{}

func (f *fakeSentryService) StartSentry()          {}
func (f *fakeSentryService) CaptureError(_ error)  {}
func (f *fakeSentryService) CaptureMessage(_ string) {}

var _ sentry.SentryService = (*fakeSentryService)(nil)

// fakeKubernetesClient satisfies k8s.KubernetesClient for unit tests.
type fakeKubernetesClient struct{}

func (f *fakeKubernetesClient) CoreV1() corev1.CoreV1Interface                          { return nil }
func (f *fakeKubernetesClient) CoordinationV1() coordinationv1.CoordinationV1Interface  { return nil }
func (f *fakeKubernetesClient) GetServiceAccountToken() (string, error)                 { return "fake-token", nil }

var _ k8s.KubernetesClient = (*fakeKubernetesClient)(nil)

func fakeClientset() k8s.KubernetesClient {
	return &fakeKubernetesClient{}
}

func TestNewController_NotNil(t *testing.T) {
	cfg := &config.Config{}
	c := NewController(cfg, fakeClientset(), &fakeSentryService{})
	require.NotNil(t, c)
}

func TestNewController_Fields(t *testing.T) {
	cfg := &config.Config{LogLevel: "debug"}
	cs := fakeClientset()
	svc := &fakeSentryService{}

	c := NewController(cfg, cs, svc)
	assert.Equal(t, cfg, c.Cfg)
	assert.Equal(t, cs, c.Clientset)
	assert.Equal(t, svc, c.sentry)
	assert.NotNil(t, c.log)
}


func TestRunBPF_ReturnsOnContextCancel(t *testing.T) {
	cfg := &config.Config{Mode: config.ModeBPF}
	c := NewController(cfg, fakeClientset(), &fakeSentryService{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// With BPF disabled (default), RunBPF is a no-op that blocks until the
	// context is cancelled, then returns nil. Matches the shape of the
	// other Run* methods.
	err := c.RunBPF(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestController_BuildLock_MissingEnv(t *testing.T) {
	// buildLock → config.GetHAEnvs() requires env vars; without them it returns an error.
	cfg := &config.Config{}
	c := NewController(cfg, fakeClientset(), &fakeSentryService{})
	_, _, err := c.buildLock("test-lock")
	assert.Error(t, err, "buildLock must return an error when HA env vars are missing")
}
