# Helm values reference

**Audience:** Platform operator

This page documents every key in `helm/values.yml`. The Helm chart provisions
all three Deployments (injector, renewer, revoker) and, optionally, the NRI
DaemonSet from a single `values.yml`.

## Minimal values for the canonical NRI + projected-SA install

```yaml
vaultDbInjector:
  configuration:
    vaultAddress: "https://vault.example.com:8200"
    vaultAuthPath: "kubernetes"
    kubeRole: "vault-db-injector"
    useProjectedSA: true
    tokenRequestAudiences:
      - vault

nri:
  enabled: true
  pluginIndex: "10"
```

All other keys fall back to the defaults documented below.

---

## `vaultDbInjector.configuration.*`

These keys map directly to the binary configuration file. The chart renders
them into a ConfigMap consumed by all three Deployments.

| Key | Default | Purpose |
|---|---|---|
| `vaultAddress` | `https://vault1.numberly.in:8200` | Vault or OpenBao base URL |
| `vaultAuthPath` | `kubernetes` | Kubernetes auth method mount path |
| `logLevel` | `info` | Log level for all three binaries |
| `kubeRole` | `all-rw` | Default Vault role for binary login |
| `kubeRoleNri` | `""` | Override role for the NRI plugin. Falls back to `kubeRole` when empty. Useful in projected-SA mode where the NRI plugin has a dedicated Vault policy. |
| `kubeRoleRenewer` | `""` | Override role for the renewer. Falls back to `kubeRole` when empty. |
| `kubeRoleRevoker` | `""` | Override role for the revoker. Falls back to `kubeRole` when empty. |
| `tokenTTL` | `8766h` | Periodic token TTL requested at login (approximately 1 year) |
| `vaultSecretName` | `vault-injector` | Name of the KV-v2 mount for per-pod bookkeeping |
| `vaultSecretPrefix` | `kubernetes` | Path prefix inside the KV mount |
| `sentry` | `true` | Enable Sentry error reporting |
| `sentryDsn` | `https://your-sentry@sentry/660` | Sentry DSN |
| `webhookFqdn` | `vault-db-injector.numberly.io` | FQDN used in the webhook service name |
| `webhookMatchLabels` | `vault-db-injector` | Value of the `objectSelector` label on the MutatingWebhookConfiguration |
| `injectorLabel` | `vault-db-injector` | Pod label value used to select injected pods |
| `useProjectedSA` | `false` | When `true`, the injector authenticates to Vault per-pod using a Kubernetes TokenRequest JWT for the admitted pod's ServiceAccount. Requires each Vault auth/kubernetes role to have `token_period > 0`. The chart provisions a ClusterRole granting `create` on `serviceaccounts/token`. |
| `tokenRequestAudiences` | `[]` | Audiences set on the TokenRequest JWT. Empty = cluster-default audience (legacy compat). Recommended for new deployments: `["vault"]` with a matching `audience` on each Vault k8s-auth role. |
| `tokenRequestExpirationSeconds` | `600` | Requested lifetime of the TokenRequest JWT in seconds. The kube-apiserver enforces a floor of 600s. |

---

## `vaultDbInjector.injector.*`

Configuration specific to the injector Deployment.

| Key | Default | Purpose |
|---|---|---|
| `serviceAccountName` | `""` | Override the ServiceAccount used by the injector Deployment. Empty = chart-managed default (release name). Set to bring your own SA provisioned outside the chart. |
| `args` | `["--config=/injector/config.yaml"]` | Arguments passed to the injector binary |
| `image.repository` | `numberly/vault-db-injector` | Container image repository |
| `image.tag` | `2.0.12` | Container image tag |
| `imagePullPolicy` | `Always` | Image pull policy |
| `replicas` | `2` | Number of injector Deployment replicas |
| `ports[0].port` | `8443` | Webhook HTTPS port |
| `ports[0].targetPort` | `8443` | Container target port |
| `type` | `ClusterIP` | Service type |
| `serviceAccount.annotations` | `{}` | Annotations added to the injector ServiceAccount (e.g. for IRSA/Workload Identity) |
| `containerSecurityContext.allowPrivilegeEscalation` | `false` | Security context |
| `containerSecurityContext.readOnlyRootFilesystem` | `true` | Security context |
| `containerSecurityContext.runAsNonRoot` | `true` | Security context |
| `containerSecurityContext.runAsUser` | `65534` | UID for the injector process |
| `containerSecurityContext.runAsGroup` | `65534` | GID for the injector process |

---

## `vaultDbInjector.renewer.*`

Configuration specific to the renewer Deployment.

| Key | Default | Purpose |
|---|---|---|
| `serviceAccountName` | `""` | Override the ServiceAccount. Empty = `<release>` in legacy mode, `<release>-renewer` when `useProjectedSA: true`. |
| `args` | `["--config=/renewer/config.yaml"]` | Arguments passed to the renewer binary |
| `image.repository` | `numberly/vault-db-injector` | Container image repository |
| `image.tag` | `2.0.12` | Container image tag |
| `imagePullPolicy` | `Always` | Image pull policy |
| `replicas` | `4` | Number of renewer replicas (leader election selects one active) |
| `containerSecurityContext` | same as injector | Dropped privileges, non-root |

---

## `vaultDbInjector.revoker.*`

Configuration specific to the revoker Deployment.

| Key | Default | Purpose |
|---|---|---|
| `serviceAccountName` | `""` | Override the ServiceAccount. Empty = `<release>` in legacy mode, `<release>-revoker` when `useProjectedSA: true`. |
| `args` | `["--config=/revoker/config.yaml"]` | Arguments passed to the revoker binary |
| `image.repository` | `numberly/vault-db-injector` | Container image repository |
| `image.tag` | `2.0.12` | Container image tag |
| `imagePullPolicy` | `Always` | Image pull policy |
| `replicas` | `4` | Number of revoker replicas (leader election selects one active) |
| `containerSecurityContext` | same as injector | Dropped privileges, non-root |

---

## `nri.*`

Configuration for the NRI DaemonSet (v3.0+).

| Key | Default | Purpose |
|---|---|---|
| `enabled` | `false` | When `true`, deploys the NRI DaemonSet and tells the injector to wrap credentials with placeholders. Both are tied to this single switch so the cluster cannot end up in a state where the webhook produces placeholders but nothing substitutes them. Requires containerd ≥ 1.7 with NRI enabled, or CRI-O ≥ 1.26. |
| `serviceAccountName` | `""` | Override the ServiceAccount for the NRI DaemonSet. Empty = `<release>`. |
| `image.repository` | `""` | Defaults to `vaultDbInjector.injector.image.repository` |
| `image.tag` | `""` | Defaults to `vaultDbInjector.injector.image.tag` |
| `imagePullPolicy` | `Always` | Image pull policy |
| `resources.requests.cpu` | `50m` | CPU request for each NRI plugin pod |
| `resources.requests.memory` | `64Mi` | Memory request for each NRI plugin pod |
| `resources.limits.cpu` | `200m` | CPU limit |
| `resources.limits.memory` | `256Mi` | Memory limit |
| `pluginIndex` | `"10"` | NRI plugin priority index. Must be unique per containerd instance. Running multiple injector releases on the same cluster requires distinct values (e.g. `"10"` for prod, `"11"` for dev). Plugin name auto-defaults to the Helm release fullname. |
| `tolerations` | `[{operator: Exists}]` | Tolerations for the NRI DaemonSet. The default tolerates all taints so the plugin runs on every node. If a tainted node runs labeled pods but the plugin is absent, those pods start with the raw placeholder string in env — override this if you want to restrict which nodes run the plugin. |
| `nodeSelector` | `{}` | Node selector for the NRI DaemonSet |

---

## Projected-SA: Vault roles the operator must create

When `useProjectedSA: true`, the chart provisions dedicated ServiceAccounts
for the renewer and revoker. The Vault operator must create matching
`auth/kubernetes/role` entries:

```
auth/kubernetes/role/<release>-renewer:
  bound_service_account_names = <release>-renewer
  bound_service_account_namespaces = <release-namespace>
  token_policies = <release>-renewer
  # policy: update on auth/token/renew + sys/leases/renew

auth/kubernetes/role/<release>-revoker:
  bound_service_account_names = <release>-revoker
  bound_service_account_namespaces = <release-namespace>
  token_policies = <release>-revoker
  # policy: update on auth/token/revoke-orphan + sys/leases/revoke
```

See [getting-started/vault-policies](../getting-started/vault-policies.md) for
the full policy HCL and `vault write` commands.
