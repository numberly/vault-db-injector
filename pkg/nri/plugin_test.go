package nri

import (
	"context"
	"testing"

	nriapi "github.com/containerd/nri/pkg/api"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/placeholder"
)

func TestCreateContainer_NoEnv(t *testing.T) {
	p := newPlugin(&config.Config{}, logger.GetLogger())
	pod := &nriapi.PodSandbox{
		Uid:    "pod-1",
		Labels: map[string]string{"vault-db-injector": "true"},
	}
	cont := &nriapi.Container{Env: []string{"FOO=bar"}}
	p.cfg.NRI.PodLabel = "vault-db-injector"
	adj, _, err := p.CreateContainer(context.Background(), pod, cont)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adj != nil {
		t.Fatalf("expected nil adjustment when no placeholder in env, got %v", adj)
	}
}

func TestCreateContainer_LabelMissing(t *testing.T) {
	p := newPlugin(&config.Config{}, logger.GetLogger())
	p.cfg.NRI.PodLabel = "vault-db-injector"
	ph := placeholder.Generate()
	pod := &nriapi.PodSandbox{Uid: "pod-1"} // no label
	cont := &nriapi.Container{Env: []string{"DB_PASS=" + ph}}
	adj, _, err := p.CreateContainer(context.Background(), pod, cont)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adj != nil {
		t.Fatalf("expected nil adjustment when label missing, got %v", adj)
	}
}

func TestRemovePodSandbox_EvictsCache(t *testing.T) {
	p := newPlugin(&config.Config{}, logger.GetLogger())
	p.cache["pod-1"] = map[string]string{"a": "b"}
	pod := &nriapi.PodSandbox{Uid: "pod-1"}
	if err := p.RemovePodSandbox(context.Background(), pod); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, exists := p.cache["pod-1"]; exists {
		t.Fatalf("cache entry not evicted")
	}
}

func TestSplitKV(t *testing.T) {
	cases := []struct{ in, k, v string }{
		{"FOO=bar", "FOO", "bar"},
		{"DB_URI=postgres://a:b@c/d", "DB_URI", "postgres://a:b@c/d"},
		{"BARE", "BARE", ""},
		{"=val", "", "val"},
	}
	for _, c := range cases {
		k, v := splitKV(c.in)
		if k != c.k || v != c.v {
			t.Errorf("splitKV(%q) = (%q,%q), want (%q,%q)", c.in, k, v, c.k, c.v)
		}
	}
}

func TestExtractPlaceholdersFromEnv_Classic(t *testing.T) {
	userPH := placeholder.Generate()
	passPH := placeholder.Generate()
	env := []string{"DB_USER=" + userPH, "DB_PASS=" + passPH, "FOO=bar"}
	dbConf := k8s.DbConfiguration{
		Mode:             k8s.DbModeClassic,
		DbUserEnvKey:     "DB_USER",
		DbPasswordEnvKey: "DB_PASS",
	}
	out := extractPlaceholdersFromEnv(env, dbConf)
	if out[userPH] != "username" || out[passPH] != "password" {
		t.Fatalf("classic mapping incorrect: %v", out)
	}
}

func TestExtractPlaceholdersFromEnv_URI(t *testing.T) {
	userPH := placeholder.Generate()
	passPH := placeholder.Generate()
	dsn := "postgres://" + userPH + ":" + passPH + "@db.example.com:5432/x?sslmode=require"
	env := []string{"DB_URI=" + dsn}
	dbConf := k8s.DbConfiguration{
		Mode:        k8s.DbModeURI,
		DbURIEnvKey: "DB_URI",
	}
	out := extractPlaceholdersFromEnv(env, dbConf)
	if out[userPH] != "username" || out[passPH] != "password" {
		t.Fatalf("uri mapping incorrect: %v", out)
	}
}

func TestExtractPlaceholdersFromEnv_NoPlaceholder(t *testing.T) {
	env := []string{"FOO=bar", "DB_USER=alice"}
	dbConf := k8s.DbConfiguration{
		Mode:             k8s.DbModeClassic,
		DbUserEnvKey:     "DB_USER",
		DbPasswordEnvKey: "DB_PASS",
	}
	out := extractPlaceholdersFromEnv(env, dbConf)
	if len(out) != 0 {
		t.Fatalf("expected empty map (no placeholder), got %v", out)
	}
}

func TestEnvHasAnyPlaceholder(t *testing.T) {
	ph := placeholder.Generate()
	if !envHasAnyPlaceholder([]string{"X=" + ph}) {
		t.Fatal("missed real placeholder")
	}
	if envHasAnyPlaceholder([]string{"X=plain"}) {
		t.Fatal("false positive on plain value")
	}
}

func TestPlugin_CacheSourceField_Exists(t *testing.T) {
	p := newPlugin(&config.Config{}, logger.GetLogger())
	if p.cacheSource == nil {
		t.Fatal("newPlugin should initialise cacheSource map")
	}
	p.mu.Lock()
	p.cache["uid-1"] = map[string]string{"__VDBI_PH_x___": "v"}
	p.cacheSource["uid-1"] = "prewarm"
	got := p.cacheSource["uid-1"]
	p.mu.Unlock()
	if got != "prewarm" {
		t.Errorf("cacheSource[uid-1]: got %q, want %q", got, "prewarm")
	}
}
