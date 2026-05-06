package nri

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCache_MissingFile(t *testing.T) {
	dir := t.TempDir()
	pods, err := loadCache(filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(pods) != 0 {
		t.Fatalf("missing file should yield empty map, got %v", pods)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "cache.json")
	in := map[string]map[string]string{
		"pod-1": {"__PH_A__": "alice", "__PH_B__": "Sup3rPass"},
		"pod-2": {"__PH_C__": "bob"},
	}
	if err := saveCache(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := loadCache(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(out) != 2 || out["pod-1"]["__PH_A__"] != "alice" || out["pod-2"]["__PH_C__"] != "bob" {
		t.Fatalf("round-trip mismatch: %v", out)
	}
}

func TestSaveCache_FileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := saveCache(path, map[string]map[string]string{"x": {"a": "b"}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("file perms = %v, want 0600", st.Mode().Perm())
	}
}

func TestSaveCache_AtomicNoLeftoverTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := saveCache(path, map[string]map[string]string{}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("tmp file leaked: %v", err)
	}
}

func TestLoadCache_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := loadCache(path)
	if err == nil {
		t.Fatalf("expected parse error on corrupt file")
	}
}
