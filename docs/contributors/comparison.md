# Project comparison

**Audience:** Anyone

This page compares vault-db-injector with other tools that retrieve secrets
from Vault and deliver them to Kubernetes workloads. It sits under Contributors
rather than Getting Started because the comparison is most useful to people who
have already decided to dig deeper — either evaluating the project seriously or
planning to contribute. If you are still in early evaluation, this gives you
the full picture.

## Why vault-db-injector exists

We investigated existing Vault injector solutions before building this one.
None of them matched our requirements: most focused on generic secret delivery
rather than the Vault database engine specifically, and several were difficult
to extend. vault-db-injector is not a drop-in replacement for any existing
tool. It is a focused tool built around the database engine, automatic lease
renewal, lease revocation on pod deletion, and — in v3.0 — credential
delivery that leaves no plaintext in the PodSpec.

## Tools compared

- [Vault Agent Injector](https://developer.hashicorp.com/vault/docs/platform/k8s/injector)
- [Bank Vaults](https://github.com/bank-vaults/bank-vaults)
- [Vals Operator](https://github.com/digitalis-io/vals-operator)
- [Vault CSI Provider](https://developer.hashicorp.com/vault/docs/platform/k8s/csi)

## Our requirements (by priority)

1. Handle the Vault database engine natively
2. Inject credentials through environment variables
3. Simple configuration for application developers (annotations only)
4. Vault audit logging attributed to the pod identity
5. Automatic lease renewal and revocation tied to the pod lifecycle
6. Inspectable state (for debugging and manual revocation)
7. Single Deployment — no sidecar containers

## Comparison table

| Feature | Vault-Db-Injector | Vault Agent Injector | Bank Vaults (webhook) | Vals Operator | Vault CSI Provider |
|---|---|---|---|---|---|
| **Credential source** | Vault Database Engine | Multiple engines | Secret engine | Multiple engines | K/V |
| **Engine** | Database | All | K/V | Database and K/V | K/V |
| **Injection method** | Pod environment vars | Sidecar / Init container | Init container (in-memory) | Kubernetes Secrets | CSI volume |
| **Dynamic secret rotation** | Not needed | Yes | Yes | No | Yes |
| **Access control** | Role-based policies | Role-based policies | Role-based policies | Role-based policies | Role-based policies |
| **Configuration complexity** | Low | Very high | Low | Moderate | Moderate |
| **User complexity** | Low | Very high | Low | Moderate | Low |
| **Operation mode** | Deployment | Deployment | Deployment | Operator | Operator |
| **Configuration method** | Annotations | Annotations | Through env | CRDs | CRDs |
| **Injects into environment** | Yes | No | Yes | Yes | Yes (secretRef) |
| **Secret encryption** | Yes | Yes | Yes | Yes | Yes |
| **Audit logging** | Yes | Yes | Yes | Yes | Yes |
| **Accessible state** | Yes | No | No | No | No |
| **Lease renewal** | Yes | Yes | — | With pod restart | — |
| **Lease revocation** | Yes | No | — | No | — |
| **Community support** | Growing | Established | Moderate | Moderate | Established |
| **Credentials invisible at K8s API layer (PodSpec / etcd / audit logs / GitOps)** | Yes (with NRI mode) | No | No | No | No |

### Key

| Symbol | Meaning |
|---|---|
| Yes | Supported |
| No | Not supported |
| — | Not applicable |

## Summary

vault-db-injector focuses specifically on the Vault database engine, revokes
leases when pods are deleted (most alternatives do not), and in NRI mode keeps
credentials out of the Kubernetes API entirely.
