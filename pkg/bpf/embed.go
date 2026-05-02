//go:build linux

package bpf

import _ "embed"

// substitute.amd64.bpf.o is produced by `make build-bpf` and contains the
// BPF LSM program plus its maps. It is committed to the repo for build
// reproducibility; CI rebuilds it via docker buildx in a later task.
//
//go:embed substitute.amd64.bpf.o
var bpfObjAMD64 []byte

//go:embed substitute.arm64.bpf.o
var bpfObjARM64 []byte
