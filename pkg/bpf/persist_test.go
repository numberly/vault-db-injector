//go:build linux

package bpf

import (
	"path/filepath"
	"testing"
)

func TestPersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := NewPersister(dir)

	mappings := map[string]string{
		"__VDBI_PH_aa___": "secret-pwd",
		"__VDBI_PH_bb___": "secret-user",
	}
	if err := p.Save("pod-uid-1", mappings); err != nil {
		t.Fatal(err)
	}

	got, err := p.Load("pod-uid-1")
	if err != nil {
		t.Fatal(err)
	}
	if got["__VDBI_PH_aa___"] != "secret-pwd" {
		t.Fatalf("missing entry, got %#v", got)
	}
	if got["__VDBI_PH_bb___"] != "secret-user" {
		t.Fatalf("missing entry, got %#v", got)
	}
}

func TestPersist_LoadAll(t *testing.T) {
	dir := t.TempDir()
	p := NewPersister(dir)
	if err := p.Save("a", map[string]string{"__VDBI_PH_a___": "av"}); err != nil {
		t.Fatal(err)
	}
	if err := p.Save("b", map[string]string{"__VDBI_PH_b___": "bv"}); err != nil {
		t.Fatal(err)
	}

	all, err := p.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	if all["a"]["__VDBI_PH_a___"] != "av" {
		t.Fatalf("LoadAll missing a entry: %#v", all)
	}
	if all["b"]["__VDBI_PH_b___"] != "bv" {
		t.Fatalf("LoadAll missing b entry: %#v", all)
	}
}

func TestPersist_Delete(t *testing.T) {
	dir := t.TempDir()
	p := NewPersister(dir)
	if err := p.Save("uid", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}

	if err := p.Delete("uid"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Load("uid"); err == nil {
		t.Fatal("expected error after delete")
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) != 0 {
		t.Fatalf("file not deleted: %v", files)
	}
}

func TestPersist_Delete_NotExist_NoError(t *testing.T) {
	dir := t.TempDir()
	p := NewPersister(dir)
	if err := p.Delete("nonexistent"); err != nil {
		t.Fatalf("Delete on nonexistent should not error, got %v", err)
	}
}

func TestPersist_AtomicSave_NoLeftoverTempFile(t *testing.T) {
	dir := t.TempDir()
	p := NewPersister(dir)
	if err := p.Save("uid", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	tmpFiles, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(tmpFiles) != 0 {
		t.Fatalf("temp file leftover: %v", tmpFiles)
	}
}
