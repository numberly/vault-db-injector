//go:build linux

package bpf

import (
	"bytes"
	"os"
	"runtime"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cockroachdb/errors"
)

// Constants must match pkg/bpf/c/substitute.bpf.c.
const (
	// PlaceholderLen is the byte length of placeholder strings inserted
	// into env values. Matches placeholder.Length.
	PlaceholderLen = 77
	// ValueMax is the maximum byte length of a real credential value. The
	// kernel-side substitution NUL-pads up to PlaceholderLen.
	ValueMax = 73
	// MaxMappingsPerCgroup is the BPF map's per-cgroup capacity.
	MaxMappingsPerCgroup = 8
)

// Loader owns the BPF program and the cgroup→mappings hash map. Close
// detaches the tracepoint link and frees the maps.
type Loader struct {
	coll      *ebpf.Collection
	link      link.Link
	cgroupMap *ebpf.Map
}

// Load reads the embedded .bpf.o for the current architecture, verifies
// kernel support, instantiates maps and program, and attaches the tracepoint
// hook. The returned Loader is ready to accept PutMapping / DeleteMapping
// calls. Caller MUST Close it on shutdown.
//
// maxMappings controls the BPF map capacity (cgroup_mappings MaxEntries).
// Pass 0 to use the compile-time default (MAX_CGROUPS = 4096).
func Load(maxMappings int) (*Loader, error) {
	if err := checkKernelSupport(); err != nil {
		return nil, err
	}

	var obj []byte
	switch runtime.GOARCH {
	case "amd64":
		obj = bpfObjAMD64
	case "arm64":
		obj = bpfObjARM64
	default:
		return nil, errors.Newf("unsupported GOARCH %s for BPF mode", runtime.GOARCH)
	}
	if len(obj) == 0 {
		return nil, errors.New("BPF object is empty; CI must compile substitute.bpf.c")
	}

	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(obj))
	if err != nil {
		return nil, errors.Wrap(err, "load BPF collection spec")
	}
	if maxMappings > 0 {
		if mapSpec, ok := spec.Maps["cgroup_mappings"]; ok {
			mapSpec.MaxEntries = uint32(maxMappings)
		}
	}
	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		return nil, errors.Wrap(err, "instantiate BPF collection")
	}
	prog := coll.Programs["sys_enter_execve"]
	if prog == nil {
		coll.Close()
		return nil, errors.New("BPF program sys_enter_execve not found in collection")
	}
	cgroupMap := coll.Maps["cgroup_mappings"]
	if cgroupMap == nil {
		coll.Close()
		return nil, errors.New("cgroup_mappings map not found in BPF collection")
	}
	lnk, err := link.Tracepoint("syscalls", "sys_enter_execve", prog, nil)
	if err != nil {
		coll.Close()
		return nil, errors.Wrap(err, "attach tracepoint hook")
	}
	return &Loader{coll: coll, link: lnk, cgroupMap: cgroupMap}, nil
}

// PutMapping inserts or replaces the mappings for one cgroup_id. mappings
// keys are placeholders (must be exactly PlaceholderLen bytes); values
// are real credential values (must be ≤ ValueMax bytes).
func (l *Loader) PutMapping(cgroupID uint64, mappings map[string]string) error {
	type entry struct {
		Placeholder [PlaceholderLen]byte
		Value       [ValueMax + 1]byte
		ValueLen    uint32
		_pad        uint32
	}
	type mfc struct {
		Count   uint32
		_pad    uint32
		Entries [MaxMappingsPerCgroup]entry
	}
	if len(mappings) > MaxMappingsPerCgroup {
		return errors.Newf("too many mappings (%d > %d)", len(mappings), MaxMappingsPerCgroup)
	}
	var v mfc
	i := 0
	for ph, val := range mappings {
		if len(ph) != PlaceholderLen {
			return errors.Newf("placeholder length %d != %d", len(ph), PlaceholderLen)
		}
		if len(val) > ValueMax {
			return errors.Newf("value too long: %d > %d", len(val), ValueMax)
		}
		copy(v.Entries[i].Placeholder[:], ph)
		copy(v.Entries[i].Value[:], val)
		v.Entries[i].ValueLen = uint32(len(val))
		i++
	}
	v.Count = uint32(len(mappings))

	return errors.Wrap(l.cgroupMap.Update(cgroupID, v, ebpf.UpdateAny), "update cgroup_mappings")
}

// DeleteMapping removes the entry for one cgroup_id. Idempotent: a missing
// key is not an error (the runner may call Delete on shutdown of a pod
// whose mapping was never programmed).
func (l *Loader) DeleteMapping(cgroupID uint64) error {
	if err := l.cgroupMap.Delete(cgroupID); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return errors.Wrap(err, "delete cgroup mapping")
	}
	return nil
}

// Close detaches the tracepoint hook and frees BPF maps. Safe to call multiple times.
func (l *Loader) Close() error {
	if l.link != nil {
		_ = l.link.Close()
		l.link = nil
	}
	if l.coll != nil {
		l.coll.Close()
		l.coll = nil
	}
	return nil
}

func checkKernelSupport() error {
	// Tracepoint programs do not require BPF LSM. Verify that tracefs exposes
	// the sys_enter_execve tracepoint. tracefs is typically at
	// /sys/kernel/tracing (since Linux 4.1) or /sys/kernel/debug/tracing.
	// If neither path is accessible (e.g. tracefs not bind-mounted into the
	// container), skip the pre-flight check and let link.Tracepoint fail with
	// its own error — it is more authoritative.
	candidates := []string{
		"/sys/kernel/tracing/events/syscalls/sys_enter_execve/format",
		"/sys/kernel/debug/tracing/events/syscalls/sys_enter_execve/format",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return nil // found — we're good
		}
	}
	// Neither path found — check if tracefs itself is missing vs just not mounted.
	if _, err := os.Stat("/sys/kernel/tracing"); err != nil {
		if _, err2 := os.Stat("/sys/kernel/debug/tracing"); err2 != nil {
			// tracefs not mounted at all: warn but don't block — let the
			// kernel verifier produce the definitive error.
			return errors.New("tracefs not found at /sys/kernel/tracing or /sys/kernel/debug/tracing; mount tracefs or add hostPath volume for the container")
		}
	}
	return errors.New("tracepoint syscalls/sys_enter_execve not found in tracefs: kernel may need CONFIG_FTRACE_SYSCALLS=y")
}
