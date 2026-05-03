package nri

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	cerrors "github.com/cockroachdb/errors"
)

// cacheFile is the on-disk format of the persisted plugin cache. Stored as a
// single JSON document at NRIConfig.CachePath. Survives plugin pod restart
// (hostPath tmpfs) so wrap tokens consumed at first CreateContainer remain
// usable for subsequent kubelet container retries within the same pod UID.
type cacheFile struct {
	Version int                          `json:"version"`
	Pods    map[string]map[string]string `json:"pods"` // pod UID → placeholder → value
}

const cacheVersion = 1

// loadCache reads and parses the cache file. Missing file is not an error;
// returns an empty map. Any parse error returns the underlying error so
// the caller can decide whether to crash or proceed with empty cache.
func loadCache(path string) (map[string]map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]map[string]string{}, nil
		}
		return nil, cerrors.Wrap(err, "read cache file")
	}
	var f cacheFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, cerrors.Wrap(err, "parse cache file")
	}
	if f.Pods == nil {
		f.Pods = map[string]map[string]string{}
	}
	return f.Pods, nil
}

// saveCache writes the cache atomically: write to <path>.tmp, fsync, rename.
// Creates the parent directory with 0700 perms if missing. File mode 0600.
func saveCache(path string, pods map[string]map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return cerrors.Wrap(err, "create cache dir")
	}
	b, err := json.Marshal(cacheFile{Version: cacheVersion, Pods: pods})
	if err != nil {
		return cerrors.Wrap(err, "marshal cache")
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return cerrors.Wrap(err, "open cache tmp")
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return cerrors.Wrap(err, "write cache tmp")
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return cerrors.Wrap(err, "fsync cache tmp")
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return cerrors.Wrap(err, "close cache tmp")
	}
	if err := os.Rename(tmp, path); err != nil {
		return cerrors.Wrap(err, "rename cache tmp → final")
	}
	return nil
}
