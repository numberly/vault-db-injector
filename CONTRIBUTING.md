# Contributing to vault-db-injector

Thank you for your interest in contributing. This guide covers how to get a
working development environment and how to validate BPF mode locally.

For general project information, see the [documentation site](https://numberly.github.io/vault-db-injector).

---

## Getting started

```bash
git clone https://github.com/numberly/vault-db-injector.git
cd vault-db-injector
go build ./...
go test ./...
```

Standard unit tests require no external dependencies and run on any platform.

---

## Testing BPF mode locally

BPF mode requires specific kernel features that are not enabled by default
on most development environments. See
[docs/getting-started/bpf-requirements.md](docs/getting-started/bpf-requirements.md)
for the full list of requirements.

### Check your kernel

```bash
cat /sys/kernel/security/lsm
# Must contain "bpf"
# Example: lockdown,capability,landlock,yama,apparmor,bpf
```

If `bpf` is not listed, follow the steps below to enable it.

### Enabling BPF LSM on Ubuntu 22.04+

Ubuntu 22.04 ships with `CONFIG_BPF_LSM=y` compiled in but the LSM is not
activated by default. Enable it via the kernel cmdline:

```bash
# 1. Edit GRUB configuration
sudo nano /etc/default/grub

# 2. Find or add GRUB_CMDLINE_LINUX and append "bpf" to the lsm= parameter:
#
#    GRUB_CMDLINE_LINUX="lsm=lockdown,capability,landlock,yama,apparmor,bpf"
#
#    If lsm= is already present, add ",bpf" at the end of the existing list.

# 3. Apply the change
sudo update-grub

# 4. Reboot
sudo reboot

# 5. Verify after reboot
cat /sys/kernel/security/lsm
# Must contain "bpf"
```

For Bottlerocket or Talos clusters, BPF LSM is enabled by default — no
changes required.

**Note:** `kind` and `minikube` (Docker/Podman drivers) do not support BPF
LSM. Use a VM-based environment, a bare-metal machine, or a cloud instance
with an Ubuntu 22.04+ kernel.

### Build the BPF objects

The BPF C program must be compiled with clang before the Go integration
tests can run. The build stage requires clang ≥ 14, libbpf, and bpftool.

```bash
make build-bpf
```

This produces the embedded `.bpf.o` objects used by `pkg/bpf/embed.go`.

### Run integration tests

BPF integration tests load the actual BPF program and exercise the
substitution path. They require root (for `CAP_BPF`) and a kernel with
BPF LSM enabled.

```bash
sudo make integration-test-bpf
```

This runs:

```
go test -tags=integration_bpf -v ./pkg/bpf/...
```

The tests will:

1. Verify BPF LSM is present and the program loads cleanly.
2. Spawn a child process through a synthetic execve and assert that
   placeholder strings are replaced with real values.
3. Verify cleanup (BPF map entries removed, tmpfs files deleted) on pod
   deletion events.

Unit tests (no kernel required) continue to run with the standard `go test`:

```bash
go test ./...
```

### Troubleshooting

**`operation not permitted` when loading BPF program**

The test process needs `CAP_BPF`, `CAP_PERFMON`, and `CAP_SYS_RESOURCE`.
Run with `sudo` or add the capabilities to your shell session:

```bash
sudo setcap cap_bpf,cap_perfmon,cap_sys_resource+eip $(which go)
```

**`bpf: operation not supported` — BPF LSM not active**

Confirm that `bpf` appears in `/sys/kernel/security/lsm`. If it does not,
re-apply the cmdline change and reboot. A compiled-in `CONFIG_BPF_LSM` is
not sufficient; the `lsm=...,bpf` boot parameter is also required.

**`BTF not found` or `no BTF for vmlinux`**

Your kernel was built without `CONFIG_DEBUG_INFO_BTF=y`. This is required
for CO-RE. Use a distribution kernel (Ubuntu 22.04+, Bottlerocket, Talos)
rather than a custom-compiled kernel that omits BTF.

**`make build-bpf` fails: clang not found**

Install clang ≥ 14 and libbpf-dev:

```bash
# Ubuntu
sudo apt-get install clang-14 libbpf-dev linux-headers-$(uname -r)
```

**DaemonSet exits immediately in a test cluster**

Check the pod logs for the startup sanity check message:
```
BPF LSM not active: /sys/kernel/security/lsm does not contain "bpf"
```
This is the expected fail-closed behavior on unsupported kernels.

---

## Pull request checklist

- [ ] `go test ./...` passes
- [ ] `go vet ./...` and `golangci-lint run` produce no errors
- [ ] New packages include unit tests
- [ ] BPF C code changes include a `make build-bpf` verification step in
      the PR description
- [ ] If the PR changes webhook behavior, add a test case to
      `pkg/k8smutator` for both `cfg.BPF.Enabled=false` and
      `cfg.BPF.Enabled=true`
- [ ] Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
      (`feat:`, `fix:`, `chore:`, `docs:`, `perf:`)
