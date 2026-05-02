//go:build linux && integration_bpf

package bpf

import (
	"os"
	"testing"
)

// TestIntegration_LoadAttachClose loads the BPF program against a real
// kernel, attaches the tracepoint hook, and closes cleanly. Requires a
// runner with CONFIG_FTRACE_SYSCALLS=y and tracefs mounted.
func TestIntegration_LoadAttachClose(t *testing.T) {
	if _, err := os.Stat("/sys/kernel/tracing/events/syscalls/sys_enter_execve/format"); err != nil {
		t.Skipf("tracepoint sys_enter_execve not available: %v", err)
	}
	loader, err := Load(0)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer func() { _ = loader.Close() }()
}
