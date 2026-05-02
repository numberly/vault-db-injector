// Package placeholder generates fixed-length opaque tokens that the webhook
// embeds in the PodSpec in lieu of real credential values, and that the BPF
// program substitutes at execve time.
//
// The format is deliberately recognizable by a simple byte-pattern match so
// the BPF C program can scan envp without parsing structure: "__VDBI_PH_"
// + 64 lowercase hex chars + "___".
//
// Length is fixed (77 bytes) so the BPF program can substitute in place
// without reallocating the user-space stack.
package placeholder

import (
	"crypto/rand"
	"encoding/hex"
)

const (
	Prefix    = "__VDBI_PH_"
	Suffix    = "___"
	HexLength = 64 // 32 bytes encoded as hex
	Length    = len(Prefix) + HexLength + len(Suffix) // 10 + 64 + 3 = 77

	// MaxValue is the maximum byte length of a real credential value that can be
	// substituted by the BPF program. The kernel-side buffer is PlaceholderLen
	// minus the NUL terminator (77 - 1 = 76), but the C-side constant is 73 to
	// leave room for padding alignment. This constant is the authoritative Go
	// value; pkg/bpf/loader.go ValueMax must equal this.
	MaxValue = 73
)

// Generate returns a fresh placeholder. Two calls always produce different
// values; the entropy comes from crypto/rand.
func Generate() string {
	var raw [HexLength / 2]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand.Read on Linux only fails if the entropy source is
		// unavailable, which would mean the host is so broken that the
		// webhook can't function anyway.
		panic("placeholder: rand.Read failed: " + err.Error())
	}
	hexStr := hex.EncodeToString(raw[:])
	return Prefix + hexStr + Suffix
}

// IsPlaceholder reports whether s is shaped like a placeholder produced by
// Generate. It does NOT check that s was actually issued — it's only a
// structural match used by tests and webhook validation.
func IsPlaceholder(s string) bool {
	if len(s) != Length {
		return false
	}
	if s[:len(Prefix)] != Prefix {
		return false
	}
	if s[len(s)-len(Suffix):] != Suffix {
		return false
	}
	for i := len(Prefix); i < len(Prefix)+HexLength; i++ {
		c := s[i]
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')
		if !isHex {
			return false
		}
	}
	return true
}
