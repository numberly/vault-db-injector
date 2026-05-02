package nri

import "testing"

func TestSubstitute_FullValue(t *testing.T) {
	env := []string{
		"FOO=bar",
		"DB_PASSWORD=__VDBI_PH_aaa___",
	}
	mapping := map[string]string{"__VDBI_PH_aaa___": "Sup3rPass"}
	out := Substitute(env, mapping)
	if out[0] != "FOO=bar" {
		t.Fatalf("non-placeholder env mutated: %q", out[0])
	}
	if out[1] != "DB_PASSWORD=Sup3rPass" {
		t.Fatalf("placeholder not substituted: %q", out[1])
	}
}

func TestSubstitute_URIEmbedded(t *testing.T) {
	env := []string{"DB_URI=postgres://alice:__VDBI_PH_xxx___@db:5432/x?sslmode=require"}
	mapping := map[string]string{"__VDBI_PH_xxx___": "Sup3rPass"}
	out := Substitute(env, mapping)
	want := "DB_URI=postgres://alice:Sup3rPass@db:5432/x?sslmode=require"
	if out[0] != want {
		t.Fatalf("URI mode failed:\n got: %q\nwant: %q", out[0], want)
	}
}

func TestSubstitute_MultiPlaceholder(t *testing.T) {
	env := []string{"DB_URI=postgres://__USER__:__PASS__@__HOST__/db"}
	mapping := map[string]string{
		"__USER__": "alice",
		"__PASS__": "Sup3rPass",
		"__HOST__": "db.example.com",
	}
	out := Substitute(env, mapping)
	want := "DB_URI=postgres://alice:Sup3rPass@db.example.com/db"
	if out[0] != want {
		t.Fatalf("multi-placeholder failed:\n got: %q\nwant: %q", out[0], want)
	}
}

func TestSubstitute_NoPlaceholder(t *testing.T) {
	env := []string{"FOO=bar", "BAZ=qux"}
	out := Substitute(env, map[string]string{"__VDBI_PH_xxx___": "value"})
	if len(out) != 2 || out[0] != "FOO=bar" || out[1] != "BAZ=qux" {
		t.Fatalf("env mutated when no placeholder present: %v", out)
	}
}

func TestSubstitute_EmptyEnv(t *testing.T) {
	out := Substitute(nil, map[string]string{"__a__": "b"})
	if len(out) != 0 {
		t.Fatalf("empty env became non-empty: %v", out)
	}
}

func TestSubstitute_EmptyMapping(t *testing.T) {
	env := []string{"FOO=bar"}
	out := Substitute(env, nil)
	if len(out) != 1 || out[0] != "FOO=bar" {
		t.Fatalf("env changed with nil mapping: %v", out)
	}
}

func TestSubstitute_LongValue(t *testing.T) {
	// >73 bytes (former BPF placeholder.MaxValue limit)
	long := "extremely-long-credential-value-that-far-exceeds-the-old-bpf-placeholder-buffer-of-73-bytes"
	env := []string{"DB_PASSWORD=__VDBI_PH_xxx___"}
	mapping := map[string]string{"__VDBI_PH_xxx___": long}
	out := Substitute(env, mapping)
	want := "DB_PASSWORD=" + long
	if out[0] != want {
		t.Fatalf("long value failed:\n got: %q\nwant: %q", out[0], want)
	}
}
