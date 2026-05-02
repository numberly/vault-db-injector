//go:build linux

package bpf

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/cockroachdb/errors"
)

// Persister stores per-pod placeholder→value mappings on tmpfs so the
// BPF DaemonSet can recover its in-memory state across self-restarts.
//
// The on-disk format is one JSON file per pod, named "<podUID>.json".
// The directory is expected to be a memory-backed emptyDir (medium: Memory
// in the Helm DaemonSet template) so contents do not survive node reboot —
// matching the credential lifecycle, since rebooted nodes lose their pods
// and trigger fresh admission.
type Persister struct {
	dir string
}

// NewPersister returns a Persister rooted at dir. Caller is responsible for
// ensuring dir is a tmpfs mount; Save lazily MkdirAll's it.
func NewPersister(dir string) *Persister {
	return &Persister{dir: dir}
}

// Save atomically writes the mappings file for podUID. Write goes via
// "<podUID>.json.tmp" and is renamed in place, so a concurrent reader or
// crash mid-write never sees a partial file.
func (p *Persister) Save(podUID string, mappings map[string]string) error {
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		return errors.Wrap(err, "mkdir tmpfs")
	}
	b, err := json.Marshal(mappings)
	if err != nil {
		return errors.Wrap(err, "marshal mappings")
	}
	tmp := filepath.Join(p.dir, podUID+".json.tmp")
	final := filepath.Join(p.dir, podUID+".json")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return errors.Wrap(err, "write tmpfs file")
	}
	if err := os.Rename(tmp, final); err != nil {
		// Best-effort cleanup: leave no .tmp files behind on rename failure.
		_ = os.Remove(tmp)
		return errors.Wrap(err, "rename tmpfs file")
	}
	return nil
}

// Load reads the mappings for one podUID. Returns an error if the file
// doesn't exist or fails to parse.
func (p *Persister) Load(podUID string) (map[string]string, error) {
	path := filepath.Join(p.dir, podUID+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.Wrap(err, "read tmpfs file")
	}
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, errors.Wrap(err, "unmarshal mappings")
	}
	return m, nil
}

// LoadAll reads every mapping file under the persister directory. Used at
// DaemonSet startup to repopulate the in-memory cache before re-programming
// BPF maps. The map key is the podUID extracted from the filename.
func (p *Persister) LoadAll() (map[string]map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(p.dir, "*.json"))
	if err != nil {
		return nil, errors.Wrap(err, "glob tmpfs dir")
	}
	out := make(map[string]map[string]string, len(files))
	for _, f := range files {
		base := filepath.Base(f)
		uid := base[:len(base)-len(".json")]
		m, err := p.Load(uid)
		if err != nil {
			return nil, errors.Wrapf(err, "load %s", uid)
		}
		out[uid] = m
	}
	return out, nil
}

// Delete removes the mapping file for podUID. A missing file is not an
// error, so the runner can call Delete unconditionally on pod-deleted
// events without checking existence first.
func (p *Persister) Delete(podUID string) error {
	path := filepath.Join(p.dir, podUID+".json")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "remove tmpfs file")
	}
	return nil
}
