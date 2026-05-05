# Setup: Vault

**Audience:** Platform operator

## Vault or OpenBao

This walkthrough targets HashiCorp Vault ≥ 1.13. Every API used — the Kubernetes auth method, KV-v2, and the database secrets engine — works against OpenBao ≥ 2.0 without changes. Point `vaultAddress` at your OpenBao instance and follow the same steps.

## Required mounts

vault-db-injector uses three Vault mounts:

| Mount | Type | Default path | Purpose |
|---|---|---|---|
| KV-v2 secrets engine | `kv` (version 2) | `vault-injector` | Per-pod bookkeeping: lease IDs, token IDs, namespace, UUID |
| Database secrets engine | `database` | `database` | Issues dynamic database credentials |
| Kubernetes auth method | `kubernetes` | `kubernetes` | Authenticates the injector's ServiceAccount and each pod's ServiceAccount |

## Enable the mounts

```bash
vault secrets enable -path=vault-injector -version=2 kv
vault secrets enable database
vault auth enable kubernetes
```

If any mount already exists at the target path, Vault returns `Error enabling: Error making API request` with status 400. Use `vault secrets list` or `vault auth list` to check existing paths before running.

## Configure the Kubernetes auth method

```bash
vault write auth/kubernetes/config \
    kubernetes_host="https://<APISERVER>:6443" \
    kubernetes_ca_cert=@/path/to/ca.crt \
    issuer="https://kubernetes.default.svc.cluster.local"
```

Replace `<APISERVER>` with the address your Vault instance uses to reach the Kubernetes API server. The `issuer` value must match the `--service-account-issuer` flag on the kube-apiserver — `https://kubernetes.default.svc.cluster.local` is the default for most distributions.

To retrieve the CA certificate from the cluster:

```bash
kubectl config view --raw -o jsonpath='{.clusters[0].cluster.certificate-authority-data}' \
    | base64 -d > /tmp/k8s-ca.crt
```

Then pass `/tmp/k8s-ca.crt` as the `@` argument above.

## Verify

```bash
vault read auth/kubernetes/config
vault secrets list | grep -E '(database|vault-injector)'
```

Expected: `auth/kubernetes/config` returns the host and CA you set. The secrets list shows both `database/` and `vault-injector/`.

## Next

[Setup: Database](setup-database.md)
