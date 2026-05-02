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
// detaches the LSM link and frees the maps.
type Loader struct {
	coll *ebpf.Collection
	link link.Link
}

// Load reads the embedded .bpf.o for the current architecture, verifies
// kernel support, instantiates maps and program, and attaches the LSM
// hook. The returned Loader is ready to accept PutMapping / DeleteMapping
// calls. Caller MUST Close it on shutdown.
func Load() (*Loader, error) {
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
	coll, err := ebpf.NewCollection(spec)
	if err != nil {
		return nil, errors.Wrap(err, "instantiate BPF collection")
	}
	prog := coll.Programs["substitute_envp"]
	if prog == nil {
		coll.Close()
		return nil, errors.New("BPF program substitute_envp not found in collection")
	}
	lnk, err := link.AttachLSM(link.LSMOptions{Program: prog})
	if err != nil {
		coll.Close()
		return nil, errors.Wrap(err, "attach LSM hook")
	}
	return &Loader{coll: coll, link: lnk}, nil
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

	m := l.coll.Maps["cgroup_mappings"]
	if m == nil {
		return errors.New("cgroup_mappings map not found in BPF collection")
	}
	return m.Update(cgroupID, v, ebpf.UpdateAny)
}

// DeleteMapping removes the entry for one cgroup_id. Idempotent: a missing
// key is not an error (the runner may call Delete on shutdown of a pod
// whose mapping was never programmed).
func (l *Loader) DeleteMapping(cgroupID uint64) error {
	m := l.coll.Maps["cgroup_mappings"]
	if m == nil {
		return errors.New("cgroup_mappings map not found")
	}
	if err := m.Delete(cgroupID); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return err
	}
	return nil
}

// Close detaches the LSM hook and frees BPF maps. Safe to call multiple times.
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
	const lsmFile = "/sys/kernel/security/lsm"
	b, err := os.ReadFile(lsmFile)
	if err != nil {
		return errors.Wrapf(err, "cannot read %s (kernel may lack security subsystem)", lsmFile)
	}
	if !bytes.Contains(b, []byte("bpf")) {
		return errors.Newf("BPF LSM not enabled in kernel cmdline (lsm=...,bpf required); current: %s", string(b))
	}
	return nil
}
