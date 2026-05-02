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

func TestCheckCgroupSetupAt_OK(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "kubepods.slice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkCgroupSetupAt(root); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

func TestCheckCgroupSetupAt_NoCgroupV2(t *testing.T) {
	root := t.TempDir()
	// No cgroup.controllers file — simulate cgroup v1 or wrong mount.
	if err := checkCgroupSetupAt(root); err == nil {
		t.Fatal("expected error for missing cgroup v2, got nil")
	}
}

func TestCheckCgroupSetupAt_NoSystemdDriver(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	// kubepods.slice absent but kubepods/ present — cgroupfs driver is accepted.
	if err := os.MkdirAll(filepath.Join(root, "kubepods"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkCgroupSetupAt(root); err != nil {
		t.Fatalf("expected nil for cgroupfs driver, got: %v", err)
	}
}

func TestCheckCgroupSetupAt_NeitherDriver(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Neither kubepods.slice nor kubepods/ present.
	if err := checkCgroupSetupAt(root); err == nil {
		t.Fatal("expected error when neither driver hierarchy found, got nil")
	}
}

func TestCheckCgroupSetupAt_CgroupfsDriver(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup.controllers"), []byte("cpu memory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "kubepods"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkCgroupSetupAt(root); err != nil {
		t.Fatalf("expected nil for cgroupfs driver, got: %v", err)
	}
}

func TestResolveCgroupID_CgroupfsGuaranteed(t *testing.T) {
	root := t.TempDir()
	podUID := "aaa-111-bbb"
	containerID := "containerd://aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666000011112222333"
	cid := "aaaa1111bbbb2222cccc3333dddd4444eeee5555ffff6666000011112222333"
	// Guaranteed QoS: kubepods/pod<UID>/<containerID> (no runtime prefix, no .scope)
	dir := filepath.Join(root, "kubepods", "pod"+podUID, cid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveCgroupIDAt(root, podUID, containerID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == 0 {
		t.Fatal("expected non-zero cgroup_id")
	}
}

func TestResolveCgroupID_CgroupfsBurstable(t *testing.T) {
	root := t.TempDir()
	podUID := "ccc-222-ddd"
	containerID := "cri-o://bbbb2222cccc3333dddd4444eeee5555ffff6666000011112222333344445555"
	cid := "bbbb2222cccc3333dddd4444eeee5555ffff6666000011112222333344445555"
	// Burstable QoS: kubepods/burstable/pod<UID>/<containerID>
	dir := filepath.Join(root, "kubepods", "burstable", "pod"+podUID, cid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveCgroupIDAt(root, podUID, containerID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == 0 {
		t.Fatal("expected non-zero cgroup_id")
	}
}

func TestResolveCgroupID_CgroupfsBestEffort(t *testing.T) {
	root := t.TempDir()
	podUID := "eee-333-fff"
	containerID := "docker://cccc3333dddd4444eeee5555ffff6666000011112222333344445555aaaa6666"
	cid := "cccc3333dddd4444eeee5555ffff6666000011112222333344445555aaaa6666"
	// BestEffort QoS: kubepods/besteffort/pod<UID>/<containerID>
	dir := filepath.Join(root, "kubepods", "besteffort", "pod"+podUID, cid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := resolveCgroupIDAt(root, podUID, containerID)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == 0 {
		t.Fatal("expected non-zero cgroup_id")
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
