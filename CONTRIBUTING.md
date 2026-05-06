# Contributing to vault-db-injector

Thank you for your interest in contributing. This guide covers how to get a
working development environment and how to validate NRI mode locally on a k3d cluster.

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

## Testing NRI mode locally

NRI mode requires a Kubernetes runtime that supports NRI (containerd ≥ 1.7
or CRI-O ≥ 1.26). Use the `scripts/enable-nri-on-k3d.sh` helper to enable
NRI on an existing k3d cluster, or pass the bundled `config.toml.tmpl` at
cluster creation:

```bash
K3D_FIX_DNS=0 k3d cluster create vault-db-test --servers 1 --agents 1 \
  --image rancher/k3s:v1.34.1-k3s1 \
  --volume "$PWD/scripts/k3d-containerd-config.toml.tmpl:/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl@all"
```

Verify the NRI socket exists on each node: `docker exec <node> ls /var/run/nri/nri.sock`.

## Pull request checklist

- [ ] `go test ./...` passes
- [ ] `go vet ./...` and `golangci-lint run` produce no errors
- [ ] New packages include unit tests
- [ ] If the PR changes webhook behavior, add a test case to
      `pkg/k8smutator` for both `cfg.NRI.Enabled=false` and
      `cfg.NRI.Enabled=true`
- [ ] Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/)
      (`feat:`, `fix:`, `chore:`, `docs:`, `perf:`)
