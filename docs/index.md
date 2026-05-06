# Vault DB Injector

**Audience:** Anyone

vault-db-injector issues short-lived database credentials from HashiCorp Vault (or OpenBao) and delivers them to Kubernetes workloads at runtime. It handles the full lifecycle: issuance, renewal, and revocation when the pod dies.

## Why it exists

Static database credentials in Kubernetes Secrets are a known weak point: they live forever, leak through GitOps, and are readable by anyone who can `kubectl get secret` in the namespace. vault-db-injector replaces them with credentials that exist only for the lifetime of the pod, are rotated by Vault rather than by the application, and appear in the Vault audit log tied to the pod's identity.

## Two delivery modes

| Mode | Where credentials live in Kubernetes | Recommended for |
|---|---|---|
| **NRI + Projected-SA** (canonical) | Nowhere — substituted at container start by a node-local NRI plugin. PodSpec, etcd, and audit logs only see opaque placeholders | New deployments |
| **Webhook + injector-SA** (legacy) | Plaintext environment variables in the PodSpec | Existing v2.x clusters that have not migrated yet |

The legacy mode is preserved for backward compatibility. New deployments should follow [Getting Started](getting-started/overview.md), which walks through the canonical NRI + Projected-SA path end to end.

## Pick your entry point

- [**Getting Started**](getting-started/overview.md) — install from zero, in order
- [**For application developers**](developers/annotations.md) — annotate your pods to consume injected credentials
- [**For platform operators**](operators/architecture.md) — operate, secure, monitor, and migrate the injector
- [**For contributors**](contributors/build-from-source.md) — build, test, and contribute code

## OpenBao compatibility

Every Vault API used by this project works against OpenBao without changes. Point `vaultAddress` at your OpenBao instance and follow the same setup steps. See the OpenBao note in [setup-vault](getting-started/setup-vault.md).

## License

Apache-2.0. See [`LICENSE`](https://github.com/numberly/vault-db-injector/blob/main/LICENSE).
