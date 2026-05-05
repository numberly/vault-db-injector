# Prerequisites

**Audience:** Platform operator

This page lists everything you need before starting the walkthrough. Each row links to the setup page that covers the details.

| Prerequisite | Minimum version | Setup page |
|---|---|---|
| Kubernetes cluster | 1.26+ with NRI-capable container runtime | [Setup: Kubernetes](setup-kubernetes.md) |
| Container runtime | containerd ≥ 1.7 with NRI enabled, or CRI-O ≥ 1.26 | [Setup: Kubernetes](setup-kubernetes.md) |
| Vault or OpenBao | Vault ≥ 1.13 / OpenBao ≥ 2.0 | [Setup: Vault](setup-vault.md) |
| Database engine | PostgreSQL 13+ (or MySQL/MariaDB/Oracle — see notes) | [Setup: Database](setup-database.md) |
| `kubectl` | matches cluster minor | local |
| `helm` | 3.12+ | local |
| `vault` CLI | matches server minor | local |

## Why each one matters

Kubernetes 1.26+ is the floor because the NRI plugin interface (CRI-O side) stabilized there. The projected ServiceAccount token API has been stable since 1.22, but without NRI support in the runtime you are limited to the legacy webhook mode.

containerd ≥ 1.7 or CRI-O ≥ 1.26 is required for NRI. The plugin speaks to the containerd NRI socket at `/var/run/nri/nri.sock`. Older runtimes have no such socket.

Vault ≥ 1.13 (or OpenBao ≥ 2.0) is needed for the Kubernetes auth method's `audience` field and the TokenRequest issuance path used in projected-SA mode.

The walkthrough uses PostgreSQL, but MySQL, MariaDB, and Oracle work with their respective Vault database plugins. The SQL examples differ; the Vault configuration pattern is the same.

Install `kubectl`, `helm`, and the `vault` CLI locally and confirm they can reach your target endpoints before continuing.

## Next

[Setup: Kubernetes cluster](setup-kubernetes.md)
