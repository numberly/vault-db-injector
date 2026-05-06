# Build from source

**Audience:** Contributor

## Requirements

| Tool | Minimum version | Notes |
|---|---|---|
| Go | 1.22 | Module-aware workspace (`go.mod` at root) |
| `make` | any | GNU Make; BSD Make is untested |
| Docker | 24+ | Required only for image builds |
| `kind` or `k3d` | any recent | Required only for integration tests |
| `kubectl` | matches cluster minor | For integration test verification |

## Clone and build

```bash
git clone https://github.com/numberly/vault-db-injector.git
cd vault-db-injector
make setup
make
```

`make setup` installs Go tool dependencies (`golangci-lint`, code generators).
`make` compiles all three binaries and puts them in `./bin/`.

## Run tests

Unit tests require no external dependencies:

```bash
make test
```

Integration tests require a running NRI-capable cluster and Vault instance:

```bash
make integration
```

## Build the container image

```bash
docker build -t vault-db-injector:dev .
```

The Dockerfile uses a multi-stage build. The final image is scratch-based and
contains only the compiled binary.

## Testing NRI mode locally

NRI mode requires containerd ≥ 1.7 with NRI enabled, or CRI-O ≥ 1.26. The
repository ships a helper for k3d:

```bash
K3D_FIX_DNS=0 k3d cluster create vault-db-test \
  --servers 1 --agents 1 \
  --image rancher/k3s:v1.34.1-k3s1 \
  --volume "$PWD/scripts/k3d-containerd-config.toml.tmpl:/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl@all"

# Verify the NRI socket is present on each node
docker exec k3d-vault-db-test-agent-0 ls /var/run/nri/nri.sock
```

If you have an existing k3d cluster, `scripts/enable-nri-on-k3d.sh` patches
the containerd config in-place and restarts containerd without recreating the
cluster.

Once the cluster is ready, install the chart with `nri.enabled=true` to
exercise the full v3.0 path:

```bash
helm upgrade --install vault-db-injector ./helm \
  --namespace vault-db-injector --create-namespace \
  --set vaultDbInjector.configuration.vaultAddress=http://vault.local:8200 \
  --set vaultDbInjector.configuration.useProjectedSA=true \
  --set vaultDbInjector.configuration.tokenRequestAudiences='{vault}' \
  --set nri.enabled=true \
  --set nri.pluginIndex=10
```

## Linting

```bash
go vet ./...
golangci-lint run
```

CI runs both checks. PRs that introduce lint errors will not be merged.
