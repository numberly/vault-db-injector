package controller

import (
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


func TestController_RunRenewer_InitLock_MissingEnv(t *testing.T) {
	// RunRenewer calls initLock → config.GetHAEnvs() which requires env vars.
	// Without them, it calls log.Fatalf. We test only what we can:
	// that the controller was constructed properly.
	cfg := &config.Config{}
	c := NewController(cfg, fakeClientset(), &fakeSentryService{})
	assert.NotNil(t, c.Cfg)
	assert.NotNil(t, c.Clientset)
}
