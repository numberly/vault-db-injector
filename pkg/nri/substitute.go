// Package nri implements a containerd/CRI-O NRI plugin that substitutes
// credential placeholders in container env vars at CreateContainer time.
package nri

import (
	"strings"

	"github.com/numberly/vault-db-injector/pkg/placeholder"
)

// Substitute returns a new env slice where every occurrence of any
// placeholder key in mapping is replaced by its value. Inputs are not
// mutated. Order is preserved.
//
// Placeholder keys are validated against placeholder.IsPlaceholder before
// use. An empty or malformed key would otherwise corrupt every env var:
// strings.ReplaceAll(s, "", v) inserts v between every character.
// Invalid keys are silently skipped — the substitution failure is more
// visible than a corrupted env, which is the safer fail mode.
func Substitute(env []string, mapping map[string]string) []string {
	if len(env) == 0 {
		return env
	}
	out := make([]string, len(env))
	for i, e := range env {
		for ph, val := range mapping {
			if !placeholder.IsPlaceholder(ph) {
				continue
			}
			if strings.Contains(e, ph) {
				e = strings.ReplaceAll(e, ph, val)
			}
		}
		out[i] = e
	}
	return out
}
