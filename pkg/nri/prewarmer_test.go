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

func TestPrewarmer_AddFunc_SkipsTerminatingPod(t *testing.T) {
	client := fake.NewSimpleClientset()
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		PodLabel:  "vault-db-injector",
		CachePath: t.TempDir() + "/cache.json",
		Prewarmer: config.NRIPrewarmerConfig{Enabled: true, MaxConcurrent: 5},
	}}, logger.GetLogger())

	resolver := &stubResolver{}
	pw := newPrewarmer(p, client, "node-1", 5, logger.GetLogger())
	pw.resolver = resolver.resolveMappingWithSource

	now := metav1.Now()
	terminatingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pod-term",
			Namespace:         "default",
			UID:               types.UID("uid-term"),
			Labels:            map[string]string{"vault-db-injector": "true"},
			DeletionTimestamp: &now,
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}

	pw.onAdd(terminatingPod)
	// Give the (non-existent) goroutine a moment to NOT run.
	time.Sleep(50 * time.Millisecond)
	if resolver.calls.Load() != 0 {
		t.Errorf("expected 0 fetches for terminating pod, got %d", resolver.calls.Load())
	}
}

// slowResolver blocks until released, simulating a slow Vault.
type slowResolver struct {
	released chan struct{}
	calls    atomic.Int32
}

func (s *slowResolver) resolveMappingWithSource(_ context.Context, _, _, _, _ string) (map[string]string, error) {
	s.calls.Add(1)
	<-s.released
	return map[string]string{"k": "v"}, nil
}

func TestPrewarmer_AddFunc_SemaphoreSaturates(t *testing.T) {
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		PodLabel:  "vault-db-injector",
		CachePath: t.TempDir() + "/cache.json",
		Prewarmer: config.NRIPrewarmerConfig{Enabled: true, MaxConcurrent: 2},
	}}, logger.GetLogger())

	slow := &slowResolver{released: make(chan struct{})}
	pw := newPrewarmer(p, fake.NewSimpleClientset(), "node-1", 2, logger.GetLogger())
	pw.resolver = slow.resolveMappingWithSource

	makePod := func(uid string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name: "p-" + uid, Namespace: "default", UID: types.UID(uid),
				Labels: map[string]string{"vault-db-injector": "true"},
			},
			Spec: corev1.PodSpec{NodeName: "node-1"},
		}
	}

	pw.onAdd(makePod("u1"))
	pw.onAdd(makePod("u2"))
	time.Sleep(50 * time.Millisecond)
	pw.onAdd(makePod("u3"))
	time.Sleep(50 * time.Millisecond)

	if slow.calls.Load() != 2 {
		t.Errorf("expected 2 in-flight calls, got %d", slow.calls.Load())
	}
	close(slow.released)
	time.Sleep(100 * time.Millisecond)
}

func TestPrewarmer_OnDelete_EvictsCache(t *testing.T) {
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		CachePath: t.TempDir() + "/cache.json",
	}}, logger.GetLogger())
	p.mu.Lock()
	p.cache["uid-del"] = map[string]string{"k": "v"}
	p.cacheSource["uid-del"] = "prewarm"
	p.mu.Unlock()

	pw := newPrewarmer(p, fake.NewSimpleClientset(), "node-1", 1, logger.GetLogger())
	pw.onDelete(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "p", Namespace: "ns", UID: types.UID("uid-del"),
	}})

	p.mu.Lock()
	_, hasCache := p.cache["uid-del"]
	_, hasSrc := p.cacheSource["uid-del"]
	p.mu.Unlock()
	if hasCache {
		t.Error("cache entry not evicted by onDelete")
	}
	if hasSrc {
		t.Error("cacheSource entry not evicted by onDelete")
	}
}

func TestPrewarmer_UpdateFunc_DoesNothing(t *testing.T) {
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

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p", Namespace: "default", UID: types.UID("uid-u"),
			Labels: map[string]string{"vault-db-injector": "true"},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	if _, err := client.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create pod: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if resolver.calls.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	beforeUpdate := resolver.calls.Load()
	if beforeUpdate != 1 {
		t.Fatalf("expected 1 call after Add, got %d", beforeUpdate)
	}

	pod.Annotations = map[string]string{"unrelated": "change"}
	if _, err := client.CoreV1().Pods("default").Update(ctx, pod, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("Update pod: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if resolver.calls.Load() != beforeUpdate {
		t.Errorf("expected no additional fetches after Update, got %d (was %d)",
			resolver.calls.Load(), beforeUpdate)
	}
}

func TestPrewarmer_Integration_PrewarmBeatsCreateContainer(t *testing.T) {
	client := fake.NewSimpleClientset()
	p := newPlugin(&config.Config{NRI: config.NRIConfig{
		PodLabel:  "vault-db-injector",
		CachePath: t.TempDir() + "/cache.json",
		Prewarmer: config.NRIPrewarmerConfig{Enabled: true, MaxConcurrent: 5},
	}}, logger.GetLogger())

	resolver := &stubResolver{plugin: p}
	pw := newPrewarmer(p, client, "node-1", 5, logger.GetLogger())
	pw.resolver = resolver.resolveMappingWithSource

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pw.Run(ctx)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p", Namespace: "default", UID: types.UID("uid-int"),
			Labels: map[string]string{"vault-db-injector": "true"},
		},
		Spec: corev1.PodSpec{NodeName: "node-1"},
	}
	if _, err := client.CoreV1().Pods("default").Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatalf("Create pod: %v", err)
	}
	// Wait until prewarm has populated the cache via the stub resolver.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		_, ok := p.cache["uid-int"]
		p.mu.Unlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Now check that resolveMappingWithSource("sync") returns the cache hit
	// and does NOT call the resolver again.
	callsBefore := resolver.calls.Load()
	mapping, err := p.resolveMappingWithSource(ctx, "uid-int", "default", "p", "sync")
	if err != nil {
		t.Fatalf("resolveMappingWithSource cache hit: %v", err)
	}
	if len(mapping) == 0 {
		t.Error("expected non-empty mapping from cache")
	}
	if resolver.calls.Load() != callsBefore {
		t.Errorf("expected zero additional resolver calls on cache hit, got %d",
			resolver.calls.Load()-callsBefore)
	}
	// The source recorded should be "prewarm" (set by the prewarmer's earlier call).
	p.mu.Lock()
	src := p.cacheSource["uid-int"]
	p.mu.Unlock()
	if src != "prewarm" {
		t.Errorf("cacheSource[uid-int]: got %q, want %q", src, "prewarm")
	}
}
