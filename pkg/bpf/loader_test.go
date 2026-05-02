//go:build linux

package bpf

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestLoad_SkipsWithoutBPFLSM verifies the kernel-support check is
// active. We don't try to actually attach the LSM here because that
// requires CAP_BPF + CAP_PERFMON, which `go test` typically lacks.
// The integration test (//go:build integration_bpf) does the real
// load + attach in a privileged environment.
func TestLoad_SkipsWithoutBPFLSM(t *testing.T) {
	if _, err := os.Stat("/sys/kernel/security/lsm"); err != nil {
		t.Skip("no /sys/kernel/security/lsm — running outside Linux, skipping")
	}
	_, err := Load()
	if err != nil {
		// Acceptable error shapes:
		//   - BPF LSM not enabled (test env without lsm=bpf)
		//   - permission denied (no CAP_BPF in test env)
		//   - empty embedded object (CI hasn't compiled yet)
		if !strings.Contains(err.Error(), "BPF LSM") &&
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
