# Install the injector

**Audience:** Platform operator

## Helm install

```bash
helm upgrade --install vault-db-injector ./helm \
  --namespace vault-db-injector \
  --set vaultDbInjector.configuration.vaultAddress=https://vault.example.com:8200 \
  --set vaultDbInjector.configuration.vaultAuthPath=kubernetes \
  --set vaultDbInjector.configuration.kubeRole=vault-db-injector \
  --set vaultDbInjector.configuration.useProjectedSA=true \
  --set vaultDbInjector.configuration.tokenRequestAudiences='{vault}' \
  --set nri.enabled=true \
  --set nri.pluginIndex=10
```

Replace `https://vault.example.com:8200` with your Vault or OpenBao address. All other values match the example names used in [Vault policies and roles](vault-policies.md).

For the full list of chart values, defaults, and per-key documentation, see the
[Helm values reference](../reference/helm-values.md) — auto-generated from
`helm/values.yml`.

!!! warning
    With `useProjectedSA: true`, `tokenRequestAudiences` must be non-empty. The binary refuses to start if it is empty — this prevents silent security degradation where any pod's token could be reused across services.

## What the chart provisions

When `useProjectedSA: true` and `nri.enabled: true`, the chart creates:

| Object | Name | Purpose |
|---|---|---|
| ServiceAccount | `vault-db-injector` | Injector webhook and NRI plugin identity |
| ServiceAccount | `vault-db-injector-renewer` | Renewer Deployment identity |
| ServiceAccount | `vault-db-injector-revoker` | Revoker Deployment identity |
| ClusterRole + binding | `vault-db-injector-token` | Grants the injector SA `create` on `serviceaccounts/token` (needed to issue per-pod TokenRequest JWTs) |
| Deployment | `vault-db-injector` | Webhook server (2 replicas by default) |
| Deployment | `vault-db-injector-renewer` | Periodic token and lease renewer (4 replicas) |
| Deployment | `vault-db-injector-revoker` | Pod-watch revoker with safety-net sweep (4 replicas) |
| DaemonSet | `vault-db-injector-nri` | Node-local NRI plugin (1 pod per node) |
| MutatingWebhookConfiguration | `vault-db-injector` | Intercepts pods with the `vault-db-injector: "true"` label |

## Verify

```bash
kubectl -n vault-db-injector get pods
```

Expected: 2 injector pods, 4 renewer pods, 4 revoker pods, and 1 NRI pod per node — all `Ready`.

```bash
kubectl -n vault-db-injector logs deployment/vault-db-injector | grep -E "(starting webhook|vault login)"
```

Expected output lines like:
```
starting webhook server on :8443
vault login successful role=vault-db-injector
```

If the NRI plugin fails to register, check containerd logs on the node:

```bash
journalctl -u containerd --since "5 minutes ago" | grep nri
```

## Next

[First injected pod](first-injected-pod.md)
