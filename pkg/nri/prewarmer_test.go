package nri

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

// stubResolver counts resolveMappingWithSource invocations and records sources.
// When plugin is set, it also writes the mapping into plugin.cache so cache-hit
// tests behave end-to-end.
type stubResolver struct {
	plugin  *plugin
	calls   atomic.Int32
	sources sync.Map // map[uid]string
}

func (s *stubResolver) resolveMappingWithSource(_ context.Context, uid, _, _, source string) (map[string]string, error) {
	s.calls.Add(1)
	s.sources.Store(uid, source)
	m := map[string]string{"__VDBI_PH_x___": "v"}
	if s.plugin != nil {
		s.plugin.mu.Lock()
		s.plugin.cache[uid] = m
		s.plugin.cacheSource[uid] = source
		s.plugin.mu.Unlock()
	}
	return m, nil
}

func TestPrewarmer_AddFunc_TriggersFetchForLabelledPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		PodLabel:  "vault-db-injector",
		CachePath: t.TempDir() + "/cache.json",
		Prewarmer: config.NRIPrewarmerConfig{Enabled: true, MaxConcurrent: 5},
	}}, logger.GetLogger())

	resolver := &stubResolver{}
	pw := newPrewarmer(p, client, "node-1", 5, logger.GetLogger())
	pw.resolver = resolver.resolveMappingWithSource

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pw.Run(ctx)

	// Wait for informer to be ready, then push a pod.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-x",
			Namespace: "default",
			UID:       types.UID("uid-x"),
			Labels:    map[string]string{"vault-db-injector": "true"},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	if _, err := client.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create pod: %v", err)
	}

	// Poll up to 2s for the prewarm fetch to fire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if resolver.calls.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resolver.calls.Load() != 1 {
		t.Fatalf("expected 1 prewarm fetch, got %d", resolver.calls.Load())
	}
	src, ok := resolver.sources.Load("uid-x")
	if !ok || src != "prewarm" {
		t.Errorf("expected source=prewarm for uid-x, got %v", src)
	}
}
