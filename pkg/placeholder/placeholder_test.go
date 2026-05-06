package placeholder

import (
	"strings"
	"testing"
)

func TestGenerate_FixedLength(t *testing.T) {
	for range 100 {
		p := Generate()
		if len(p) != Length {
			t.Fatalf("expected length %d, got %d for %q", Length, len(p), p)
		}
	}
}

func TestGenerate_Unique(t *testing.T) {
	seen := make(map[string]struct{})
	for range 1000 {
		p := Generate()
		if _, ok := seen[p]; ok {
			t.Fatalf("collision on %q after %d generations", p, len(seen))
		}
		seen[p] = struct{}{}
	}
}

func TestGenerate_PrefixSuffix(t *testing.T) {
	p := Generate()
	if !strings.HasPrefix(p, Prefix) {
		t.Fatalf("missing prefix %q in %q", Prefix, p)
	}
	if !strings.HasSuffix(p, Suffix) {
		t.Fatalf("missing suffix %q in %q", Suffix, p)
	}
}

func TestIsPlaceholder(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{Generate(), true},
		{"DB_PASSWORD", false},
		{"", false},
		{Prefix + strings.Repeat("z", HexLength) + Suffix, false}, // non-hex chars
		{Prefix + "abc" + Suffix, false},                           // wrong length
	}
	for _, c := range cases {
		if got := IsPlaceholder(c.in); got != c.want {
			t.Errorf("IsPlaceholder(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
