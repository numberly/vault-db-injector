package nri

import (
	"context"
	"encoding/json"
	"testing"

	nriapi "github.com/containerd/nri/pkg/api"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
)

func TestCreateContainer_NoAnnotation(t *testing.T) {
	p := newPlugin(&config.Config{}, logger.GetLogger())
	pod := &nriapi.PodSandbox{Uid: "pod-1"}
	cont := &nriapi.Container{Env: []string{"FOO=bar"}}
	adj, _, err := p.CreateContainer(context.Background(), pod, cont)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adj != nil {
		t.Fatalf("expected nil adjustment when annotation absent, got %v", adj)
	}
}

func TestCreateContainer_MalformedAnnotation(t *testing.T) {
	p := newPlugin(&config.Config{}, logger.GetLogger())
	pod := &nriapi.PodSandbox{
		Uid:         "pod-1",
		Annotations: map[string]string{k8s.ANNOTATION_NRI_MAPPING: "{not json"},
	}
	cont := &nriapi.Container{}
	adj, _, err := p.CreateContainer(context.Background(), pod, cont)
	if err != nil {
		t.Fatalf("expected nil error on malformed annotation, got %v", err)
	}
	if adj != nil {
		t.Fatalf("expected nil adjustment on malformed annotation, got %v", adj)
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

// Ensures the NRIMapping JSON shape is what the webhook produces.
func TestNRIMappingMarshal(t *testing.T) {
	m := k8s.NRIMapping{
		WrapToken: "hvs.xxxxx",
		Placeholders: map[string]string{
			"__VDBI_PH_aaa___": "username",
			"__VDBI_PH_bbb___": "password",
		},
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back k8s.NRIMapping
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.WrapToken != m.WrapToken {
		t.Fatalf("wrap token mismatch")
	}
	if len(back.Placeholders) != 2 {
		t.Fatalf("placeholder count mismatch")
	}
}
