package nri

import (
	"strings"
	"testing"

	"github.com/numberly/vault-db-injector/pkg/placeholder"
)

// realPH returns a syntactically-valid placeholder. Tests use these because
// Substitute now validates placeholder shape before applying.
func realPH() string { return placeholder.Generate() }

func TestSubstitute_FullValue(t *testing.T) {
	ph := realPH()
	env := []string{
		"FOO=bar",
		"DB_PASSWORD=" + ph,
	}
	mapping := map[string]string{ph: "Sup3rPass"}
	out := Substitute(env, mapping)
	if out[0] != "FOO=bar" {
		t.Fatalf("non-placeholder env mutated: %q", out[0])
	}
	if out[1] != "DB_PASSWORD=Sup3rPass" {
		t.Fatalf("placeholder not substituted: %q", out[1])
	}
}

func TestSubstitute_URIEmbedded(t *testing.T) {
	ph := realPH()
	env := []string{"DB_URI=postgres://alice:" + ph + "@db:5432/x?sslmode=require"}
	mapping := map[string]string{ph: "Sup3rPass"}
	out := Substitute(env, mapping)
	want := "DB_URI=postgres://alice:Sup3rPass@db:5432/x?sslmode=require"
	if out[0] != want {
		t.Fatalf("URI mode failed:\n got: %q\nwant: %q", out[0], want)
	}
}

func TestSubstitute_MultiPlaceholder(t *testing.T) {
	phUser := realPH()
	phPass := realPH()
	phHost := realPH()
	env := []string{"DB_URI=postgres://" + phUser + ":" + phPass + "@" + phHost + "/db"}
	mapping := map[string]string{
		phUser: "alice",
		phPass: "Sup3rPass",
		phHost: "db.example.com",
	}
	out := Substitute(env, mapping)
	want := "DB_URI=postgres://alice:Sup3rPass@db.example.com/db"
	if out[0] != want {
		t.Fatalf("multi-placeholder failed:\n got: %q\nwant: %q", out[0], want)
	}
}

func TestSubstitute_NoPlaceholder(t *testing.T) {
	env := []string{"FOO=bar", "BAZ=qux"}
	out := Substitute(env, map[string]string{realPH(): "value"})
	if len(out) != 2 || out[0] != "FOO=bar" || out[1] != "BAZ=qux" {
		t.Fatalf("env mutated when no placeholder present: %v", out)
	}
}

func TestSubstitute_EmptyEnv(t *testing.T) {
	out := Substitute(nil, map[string]string{realPH(): "b"})
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

func TestSubstitute_EmptyPlaceholderRejected(t *testing.T) {
	// Regression: an empty placeholder key would cause strings.ReplaceAll
	// to insert val between every character of every env var, corrupting
	// PATH/HOSTNAME/etc. We must skip non-conforming keys.
	env := []string{"FOO=bar", "PATH=/usr/bin"}
	mapping := map[string]string{"": "PWNED"}
	out := Substitute(env, mapping)
	if out[0] != "FOO=bar" || out[1] != "PATH=/usr/bin" {
		t.Fatalf("empty placeholder corrupted env: %v", out)
	}
}

func TestSubstitute_NonPlaceholderShapeRejected(t *testing.T) {
	// Keys not matching the __VDBI_PH_<64hex>___ shape must be ignored.
	env := []string{"FOO=__VDBI_PH_aaa___"}
	mapping := map[string]string{
		"__VDBI_PH_aaa___": "should-not-apply", // wrong hex length
		"random":           "also-skip",
	}
	out := Substitute(env, mapping)
	if out[0] != "FOO=__VDBI_PH_aaa___" {
		t.Fatalf("malformed placeholder accepted: %v", out)
	}
}

func TestSubstitute_LongValue(t *testing.T) {
	long := strings.Repeat("x", 200)
	ph := realPH()
	env := []string{"DB_PASSWORD=" + ph}
	mapping := map[string]string{ph: long}
	out := Substitute(env, mapping)
	want := "DB_PASSWORD=" + long
	if out[0] != want {
		t.Fatalf("long value failed:\n got: %q\nwant: %q", out[0], want)
	}
}
