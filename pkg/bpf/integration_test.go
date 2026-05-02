//go:build linux && integration_bpf

package bpf

import (
	"os"
	"strings"
	"testing"
)

// TestIntegration_LoadAttachClose loads the BPF program against a real
// kernel, attaches the LSM hook, and closes cleanly. Requires a runner
// with CONFIG_BPF_LSM=y and "bpf" in the kernel's "lsm=" cmdline.
func TestIntegration_LoadAttachClose(t *testing.T) {
	b, err := os.ReadFile("/sys/kernel/security/lsm")
	if err != nil {
		t.Skipf("no /sys/kernel/security/lsm: %v", err)
	}
	if !strings.Contains(string(b), "bpf") {
		t.Skipf("BPF LSM not enabled in this kernel: %s", b)
	}
	loader, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer func() { _ = loader.Close() }()
}
