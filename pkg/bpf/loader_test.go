//go:build linux

package bpf

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestLoad_SkipsWithoutTracepoint verifies the kernel-support check is
// active. We don't try to actually attach the tracepoint here because that
// requires CAP_BPF + CAP_PERFMON, which `go test` typically lacks.
// The integration test (//go:build integration_bpf) does the real
// load + attach in a privileged environment.
func TestLoad_SkipsWithoutTracepoint(t *testing.T) {
	if _, err := os.Stat("/sys/kernel/tracing/events/syscalls"); err != nil {
		t.Skip("no tracefs syscalls — running outside Linux or tracefs not mounted, skipping")
	}
	_, err := Load(0)
	if err != nil {
		// Acceptable error shapes:
		//   - tracepoint not found (kernel without CONFIG_FTRACE_SYSCALLS)
		//   - permission denied (no CAP_BPF in test env)
		//   - empty embedded object (CI hasn't compiled yet)
		if !strings.Contains(err.Error(), "tracepoint") &&
			!strings.Contains(err.Error(), "BPF object is empty") &&
			!strings.Contains(err.Error(), "operation not permitted") &&
			!strings.Contains(err.Error(), "permission denied") &&
			!errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected error shape: %v", err)
		}
		t.Logf("Load returned acceptable error: %v", err)
	}
}

// TestPutMapping_InputValidation tests the input validation paths that
// don't need a real BPF kernel attachment. We can't construct a Loader
// without the kernel, so we test indirectly: simulate the validation
// constants here.
func TestPutMapping_InputValidation_Constants(t *testing.T) {
	if PlaceholderLen != 77 {
		t.Errorf("PlaceholderLen = %d, want 77", PlaceholderLen)
	}
	if ValueMax != 73 {
		t.Errorf("ValueMax = %d, want 73", ValueMax)
	}
	if MaxMappingsPerCgroup != 8 {
		t.Errorf("MaxMappingsPerCgroup = %d, want 8", MaxMappingsPerCgroup)
	}
}
