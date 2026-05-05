# Glossary

**Audience:** Anyone

| Term | Definition |
|---|---|
| KV mount | Vault KV-v2 mount that holds per-pod bookkeeping: lease ID, token ID, namespace, UUID. Helm key: `vaultSecretName` (default: `vault-injector`). |
| Vault auth path | Mount path of the Kubernetes auth method on Vault. Helm key: `vaultAuthPath` (default: `kubernetes`). |
| Injector role | Vault role used by the injector binary to log in. Helm key: `kubeRole`. |
| Database backend | Vault `database` secrets engine that issues dynamic credentials for configured DB connections. |
| Database connection | Per-DB-server config registered under `database/config/<name>` on Vault. Tells Vault how to connect and what admin account to use. |
| Database role | Vault role under `database/roles/<name>` that defines the SQL statements used to create/revoke credentials for a given application. |
| App role | Vault `auth/kubernetes` role bound to an application's ServiceAccount. Controls which DB roles the app can request credentials for. |
| `token_period` | Vault role attribute that makes the issued token periodically renewable past `token_max_ttl`. Required in projected-SA mode — without it, the pod-token dies at `token_max_ttl` and creds cannot be renewed. |
| Projected-SA | Vault authentication mode where the injector issues a Kubernetes TokenRequest JWT for the admitted pod's ServiceAccount and uses it to log into Vault on the pod's behalf. The Vault audit log then records the pod SA identity, not the injector's. |
| NRI mode | Credential delivery mode where the webhook writes placeholder strings into the PodSpec. A node-local NRI plugin (DaemonSet) substitutes the placeholders with real credentials at container creation, before runc starts the process. The plaintext credentials never appear in the PodSpec or etcd. |
| Placeholder | Opaque string of the form `__VDBI_PH_<64hex>___` inserted into env var values by the webhook in NRI mode. |
| Bookkeeping token | The injector's own Vault token, used to write per-pod metadata (lease ID, token ID) to the KV mount. Distinct from the pod-token in projected-SA mode. |
| Pod-token | In projected-SA mode, the per-pod Vault token issued via a Kubernetes TokenRequest JWT. Scoped to the pod's DB role only. |
| Lease | Vault lease that covers one set of dynamic DB credentials. The renewer extends it; the revoker terminates it when the pod is deleted. |
| Orphan token | Vault token with no parent token. Used in legacy mode so that revoking the injector token does not cascade to all pod tokens. |
| NRI | Node Resource Interface — a containerd (≥ 1.7) and CRI-O (≥ 1.26) plugin API for intercepting container lifecycle events. vault-db-injector registers as an NRI plugin to hook `CreateContainer`. |
| `uuid` | Per-dbConfig UUID set by the webhook on each admitted pod. Serves as the key in the KV mount and in all `vdbi_*` metrics labels. Set automatically; do not write it manually. |
