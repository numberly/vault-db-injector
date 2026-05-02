//go:build linux

// Package bpf contains the node-local DaemonSet runtime that loads the BPF
// substitution program and populates its maps. Linux-only — the LSM hook
// used by the substitution program is a Linux kernel feature.
package bpf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/cockroachdb/errors"
)

const defaultCgroupRoot = "/sys/fs/cgroup"

// ResolveCgroupID returns the kernel inode (cgroup_id) of the cgroup
// associated with a given pod's container. The returned value matches what
// bpf_get_current_cgroup_id() returns inside the LSM hook on a process
// whose task is in this cgroup, allowing the userspace agent and the BPF
// program to coordinate via a hash map keyed by cgroup_id.
//
// Cgroup v2 only — kubelet uses systemd-managed cgroupv2 on all targeted
// distributions (Bottlerocket, Talos, Ubuntu 22.04+, GKE COS, AKS Ubuntu).
func ResolveCgroupID(podUID, containerID string) (uint64, error) {
	return resolveCgroupIDAt(defaultCgroupRoot, podUID, containerID)
}

// resolveCgroupIDAt is the testable variant accepting a custom cgroup root.
func resolveCgroupIDAt(root, podUID, containerID string) (uint64, error) {
	// systemd escapes hyphens in unit names by replacing them with underscores;
	// kubelet applies the same escaping when converting pod UIDs into cgroup
	// slice names. Mirror that here so we can find the cgroup directory.
	cleanPodUID := strings.ReplaceAll(podUID, "-", "_")
	// Strip the runtime URI scheme (containerd://, cri-o://, docker://).
	cid := containerID
	if i := strings.Index(cid, "://"); i >= 0 {
		cid = cid[i+3:]
	}

	// kubelet's systemd cgroup driver only creates intermediate QoS slices
	// for Burstable and BestEffort pods. Guaranteed pods land directly under
	// kubepods.slice with no kubepods-guaranteed.slice level — see
	// kubelet/cm/qos_container_manager_linux.go which sets
	// QOSContainersInfo.Guaranteed = rootContainer (= "kubepods").
	// We search the standard QoS slices in priority order.
	podSlices := []string{
		filepath.Join(root, "kubepods.slice",
			fmt.Sprintf("kubepods-pod%s.slice", cleanPodUID)),
		filepath.Join(root, "kubepods.slice", "kubepods-burstable.slice",
			fmt.Sprintf("kubepods-burstable-pod%s.slice", cleanPodUID)),
		filepath.Join(root, "kubepods.slice", "kubepods-besteffort.slice",
			fmt.Sprintf("kubepods-besteffort-pod%s.slice", cleanPodUID)),
	}

	// Each runtime uses a different prefix for the container scope filename.
	runtimePrefixes := []string{"cri-containerd-", "crio-", "docker-"}

	var tried []string
	for _, podDir := range podSlices {
		for _, prefix := range runtimePrefixes {
			scope := filepath.Join(podDir, fmt.Sprintf("%s%s.scope", prefix, cid))
			tried = append(tried, scope)
			if id, ok := inodeOf(scope); ok {
				return id, nil
			}
		}
	}

	return 0, errors.Newf(
		"cgroup not found for podUID=%s containerID=%s under %s; tried:\n  %s",
		podUID, containerID, root, strings.Join(tried, "\n  "))
}

// checkCgroupSetup verifies the host runs cgroup v2 with systemd driver.
// kubelet on cgroupfs uses /sys/fs/cgroup/kubepods/pod<UID>/<containerID>
// paths which the resolver doesn't search; emit a clear error rather than
// per-pod "cgroup not found" diagnostics.
func checkCgroupSetup() error {
	return checkCgroupSetupAt("/sys/fs/cgroup")
}

// checkCgroupSetupAt is the testable variant accepting a custom cgroup root.
func checkCgroupSetupAt(root string) error {
	// cgroup v2: /sys/fs/cgroup/cgroup.controllers exists
	if _, err := os.Stat(filepath.Join(root, "cgroup.controllers")); err != nil {
		return errors.Wrap(err, "cgroup v2 not detected (need cgroupv2 unified hierarchy)")
	}
	// systemd driver: kubepods.slice exists
	if _, err := os.Stat(filepath.Join(root, "kubepods.slice")); err != nil {
		return errors.Wrap(err, "kubelet systemd cgroup driver not detected (need cgroupDriver: systemd)")
	}
	return nil
}

func inodeOf(path string) (uint64, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return sys.Ino, true
}
