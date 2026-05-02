//go:build linux

package bpf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCgroupID_BurstableQoS(t *testing.T) {
	root := t.TempDir()
	podUID := "abc-123-def"
	containerID := "containerd://0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd"
	dir := filepath.Join(root, "kubepods.slice", "kubepods-burstable.slice",
		"kubepods-burstable-pod"+strings.ReplaceAll(podUID, "-", "_")+".slice",
		"cri-containerd-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcd.scope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveCgroupIDAt(root, podUID, containerID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == 0 {
		t.Fatal("got cgroup_id 0, expected non-zero inode")
	}

	// Same call should be deterministic.
	again, err := resolveCgroupIDAt(root, podUID, containerID)
	if err != nil {
		t.Fatalf("second call err: %v", err)
	}
	if got != again {
		t.Fatalf("non-deterministic: %d vs %d", got, again)
	}
}

func TestResolveCgroupID_GuaranteedQoS(t *testing.T) {
	root := t.TempDir()
	podUID := "ghi-456-jkl"
	containerID := "cri-o://1111111111111111111111111111111111111111111111111111111111111111"
	dir := filepath.Join(root, "kubepods.slice",
		"kubepods-pod"+strings.ReplaceAll(podUID, "-", "_")+".slice",
		"crio-1111111111111111111111111111111111111111111111111111111111111111.scope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveCgroupIDAt(root, podUID, containerID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == 0 {
		t.Fatal("expected non-zero")
	}
}

func TestResolveCgroupID_BestEffortDocker(t *testing.T) {
	root := t.TempDir()
	podUID := "mno-789-pqr"
	containerID := "docker://abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	dir := filepath.Join(root, "kubepods.slice", "kubepods-besteffort.slice",
		"kubepods-besteffort-pod"+strings.ReplaceAll(podUID, "-", "_")+".slice",
		"docker-abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789.scope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveCgroupIDAt(root, podUID, containerID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == 0 {
		t.Fatal("expected non-zero")
	}
}

func TestResolveCgroupID_NotFound(t *testing.T) {
	root := t.TempDir()
	_, err := resolveCgroupIDAt(root, "nope", "containerd://nope")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestResolveCgroupID_BareContainerID(t *testing.T) {
	// k8s normalizes container IDs to "<runtime>://<id>", but the resolver
	// must also accept a pre-stripped bare ID. Strip is a no-op when the
	// scheme is absent.
	root := t.TempDir()
	podUID := "stu-987-vwx"
	bareID := "2222222222222222222222222222222222222222222222222222222222222222"
	dir := filepath.Join(root, "kubepods.slice",
		"kubepods-pod"+strings.ReplaceAll(podUID, "-", "_")+".slice",
		"cri-containerd-2222222222222222222222222222222222222222222222222222222222222222.scope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveCgroupIDAt(root, podUID, bareID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == 0 {
		t.Fatal("expected non-zero")
	}
}
