// Package nri implements a containerd/CRI-O NRI plugin that substitutes
// credential placeholders in container env vars at CreateContainer time.
package nri

import "strings"

// Substitute returns a new env slice where every occurrence of any
// placeholder key in mapping is replaced by its value. Inputs are not
// mutated. Order is preserved.
func Substitute(env []string, mapping map[string]string) []string {
	if len(env) == 0 {
		return env
	}
	out := make([]string, len(env))
	for i, e := range env {
		for ph, val := range mapping {
			if strings.Contains(e, ph) {
				e = strings.ReplaceAll(e, ph, val)
			}
		}
		out[i] = e
	}
	return out
}
