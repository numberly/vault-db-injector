# Vault Database Injector

[![CI](https://github.com/numberly/vault-db-injector/actions/workflows/ci.yml/badge.svg)](https://github.com/numberly/vault-db-injector/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/<gist-user>/<COVERAGE_GIST_ID>/raw/vault-db-injector-coverage.json)](https://github.com/numberly/vault-db-injector/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/numberly/vault-db-injector?logo=github)](https://github.com/numberly/vault-db-injector/releases)
[![Image](https://img.shields.io/badge/image-ghcr.io-blue?logo=docker)](https://github.com/numberly/vault-db-injector/pkgs/container/vault-db-injector)
[![License](https://img.shields.io/github/license/numberly/vault-db-injector)](LICENSE)

> Maintainer setup: replace the `<gist-user>` and `<COVERAGE_GIST_ID>` placeholders in the Coverage badge URL above with the actual gist user and gist ID created during CI setup (see `.planning/specs/2026-05-06-ci-overhaul-design.md` §7).

The Vault DB Injector relies on the database engine from Vault to generate credentials, distribute them to Kubernetes applications and handle their lifecycle.

##  1. <a name='Feature'></a>Feature
- Generate credentials through Vault Database Engine
- Distribute credentials to workload using annotations and Kubernetes mutating webhook
- Renew credentials when necessary
- Revoke credentials when application pod is deleted
- Optionally protect credentials at the Kubernetes API layer using an NRI plugin substitution layer

##  2. <a name='Documentation'></a>Documentation

Checkout the [Vault DB Injector documentation](https://numberly.github.io/vault-db-injector) for more informations.

##  3. <a name='TalkDemo'></a>Cloud Native Days France – Talk & Demo

A production feedback session presenting **Vault DB Injector**, its design decisions, trade-offs, and lessons learned after running it in production at scale.

The talk covers:
- why static database credentials become a problem
- how ephemeral credentials are injected into Kubernetes workloads
- operational feedback from real-world usage
- a live demonstration

The demo environment is based on:
- [**OpenBao**](https://github.com/openbao/openbao) (Vault-compatible secrets management)
- [**CloudNativePG (CNPG)**](https://github.com/cloudnative-pg/cloudnative-pg) for PostgreSQL on Kubernetes

📺 **Replay:** https://youtu.be/QhOEMqbrFBk

🧪 **Demo code used during the talk:**
https://github.com/SoulKyu/vault-db-injector-cnd

## 3.5. <a name='SecurityNRI'></a>Security: NRI mode hardening

NRI mode requires the plugin DaemonSet to mount `/var/run/nri/nri.sock` —
the same socket containerd uses for plugin registration. Any pod that
mounts this hostPath can register as an NRI plugin and mutate every
container created on the node (env, mounts, capabilities, args).

This is **inherent to NRI**, not specific to this project. The cluster
admin must restrict who can mount these paths.

**Required mitigations** (in order of strength):

1. **PodSecurityAdmission `restricted` or `baseline`** on user namespaces:
   both forbid hostPath volumes. The plugin DS must run in a namespace
   labeled `pod-security.kubernetes.io/enforce=privileged`.
2. **Kyverno ClusterPolicy** that blocks `/var/run/nri` and `/opt/nri`
   hostPath mounts outside the trusted namespace. A reference policy is
   provided at [helm/policies/kyverno-restrict-nri-socket.yaml](helm/policies/kyverno-restrict-nri-socket.yaml).
3. **SELinux/AppArmor**: on RHEL/CoreOS, leave SELinux enforcing;
   do not run the plugin pod with `seLinuxOptions.type: spc_t`. The
   default `container_runtime_t` socket label prevents user pods from
   connecting even if they bypass the hostPath check.

See [docs/operators/security.md](docs/operators/security.md) for
the complete threat model.

## Installation

### Helm chart (Helm Pages)

```bash
helm repo add numberly https://numberly.github.io/vault-db-injector
helm repo update
helm install vault-db-injector numberly/vault-db-injector \
  --namespace vault-db-injector --create-namespace \
  -f my-values.yaml
```

### Helm chart (OCI)

```bash
helm install vault-db-injector \
  oci://ghcr.io/numberly/charts/vault-db-injector \
  --version 3.0.0 \
  --namespace vault-db-injector --create-namespace \
  -f my-values.yaml
```

### Container image

```bash
docker pull ghcr.io/numberly/vault-db-injector:v3.0.0
```

The image is signed with Cosign keyless. Verify before deployment:

```bash
cosign verify ghcr.io/numberly/vault-db-injector:v3.0.0 \
  --certificate-identity-regexp="^https://github.com/numberly/vault-db-injector/.github/workflows/release.yml@refs/tags/v[0-9].*$" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com"
```

> **Docker Hub deprecation**: as of v3.0.0, releases publish to `ghcr.io` only. The `numberly/vault-db-injector` Docker Hub image is frozen at v2.x and will not receive further updates. Migrate by replacing `numberly/vault-db-injector:<tag>` with `ghcr.io/numberly/vault-db-injector:<tag>` in your values file.

##  4. <a name='Contribution'></a>Contribution

Contributions to the vault-db-injector are welcome. Please submit your pull requests or issues to the project's GitLab repository.

## 5. <a name='Tool Comparison'></a>Projects Comparison

Here you can find a comparison with many vault injector projects : [Comparaison](https://numberly.github.io/vault-db-injector/getting-started/comparison/)

## 6. <a name='OpenBao'></a>OpenBao Compatibility

The Vault DB Injector is fully compatible with OpenBao, a community-driven fork of HashiCorp Vault. Since OpenBao maintains API compatibility with Vault, you can seamlessly use this injector with your OpenBao installation without any code modifications.

All the Vault APIs used by this project work out of the box with OpenBao, including:
- Kubernetes authentication
- Database secrets engine
- Token management and renewal
- KV v2 secrets engine for metadata storage
- Lease management

To use the injector with OpenBao, simply point the `vaultAddress` configuration to your OpenBao instance and ensure your OpenBao setup includes the necessary authentication backends, database engine configuration, and policies that match your deployment requirements.

##  7. <a name='Acknowledgements'></a>Acknowledgements

Special thanks to the contributors and maintainers of the project.

---

