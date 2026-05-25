# vault-db-injector

vault-db-injector helm chart

![Version: 3.2.1](https://img.shields.io/badge/Version-3.2.1-informational?style=flat-square) ![AppVersion: 3.2.1](https://img.shields.io/badge/AppVersion-3.2.1-informational?style=flat-square)

## TL;DR — minimal values for the canonical NRI + projected-SA install

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

All other keys fall back to the defaults documented below. See
[getting-started/install-injector](https://numberly.github.io/vault-db-injector/getting-started/install-injector/)
for a full walkthrough including Vault prerequisites.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| nri.enabled | bool | `false` | When `true`, deploys the NRI DaemonSet AND tells the injector to wrap every credential it issues with placeholders. Both pieces are tied to this single switch so the cluster cannot end up in a "webhook produces placeholders but nothing substitutes" state. When `false`, behavior is byte-identical to the legacy webhook mode (literal credentials in env vars). Requires containerd ≥ 1.7 with NRI enabled, OR CRI-O ≥ 1.26. |
| nri.fetchTimeout | string | `"1500ms"` | Vault credential fetch timeout per CreateContainer event. MUST be strictly less than containerd `plugin_request_timeout` so the plugin returns an error BEFORE containerd times out the plugin (containerd-side timeout fails-open and leaks placeholders into env). Default `"1500ms"` is aligned with containerd's default `plugin_request_timeout` of `2s` (leaves 500ms for containerd to propagate the error). On nodes configured with a higher `plugin_request_timeout` (e.g. `30s` to absorb Vault bursts), raise this to ~`plugin_request_timeout - 5s`. |
| nri.image.repository | string | `""` | NRI container image repository. Empty = falls back to `vaultDbInjector.injector.image.repository`. |
| nri.image.tag | string | `""` | NRI container image tag. Empty = falls back to `vaultDbInjector.injector.image.tag`. |
| nri.imagePullPolicy | string | `"Always"` | imagePullPolicy applied to the NRI container. |
| nri.nodeSelector | object | `{}` | Node selector for the NRI DaemonSet. |
| nri.pluginIndex | string | `"10"` | NRI plugin priority for `stub.WithPluginIdx`. Must be unique per containerd instance — running multiple injector releases on the same cluster requires distinct values (e.g. `"10"` for prod, `"11"` for dev). The plugin name auto-defaults to the helm release fullname. |
| nri.prewarmer | object | `{"enabled":true,"maxConcurrent":50}` | Async credential prefetcher. When enabled, a SharedInformer watches labelled pods on the local node and pre-populates the NRI cache before CreateContainer fires. Removes Vault fetch from containerd's hot path in the common case; sync fetch remains as fail-closed fallback. See `docs/reference/configuration.md` (NRI tuning > Prewarming) for details. |
| nri.prewarmer.enabled | bool | `true` | Master switch. When false, the prewarmer is not constructed and CreateContainer always uses the sync fetch path. |
| nri.prewarmer.maxConcurrent | int | `50` | Maximum number of in-flight async prewarm fetches per DS pod. Caps Vault and apiserver load during bursts. Raise on dense nodes. |
| nri.resources | object | `{"limits":{"cpu":"200m","memory":"256Mi"},"requests":{"cpu":"50m","memory":"64Mi"}}` | Resource requests and limits for each NRI plugin pod. |
| nri.serviceAccountName | string | `""` | Override the ServiceAccount used by the NRI DaemonSet. Empty = chart-managed default (`<release>`, shared with the injector). Set to a distinct value to enable privilege separation between webhook and NRI identities (also requires `kubeRoleNri`). |
| nri.tolerations | list | `[{"operator":"Exists"}]` | Tolerations for the NRI DaemonSet. The default tolerates every taint so the plugin runs on every node. The plugin is node-local — if it is missing on a tainted node where labelled pods are scheduled, those pods start with the raw placeholder string in env (visible CrashLoop, but still a leak window). Override to restrict where the plugin runs. |
| vaultDbInjector.configuration.injectorLabel | string | `"vault-db-injector"` | Pod label value the injector uses to recognise pods it owns. |
| vaultDbInjector.configuration.kubeRole | string | `"all-rw"` | Default Vault auth/kubernetes role used by the binaries to log in. Per-component overrides below take precedence when set. |
| vaultDbInjector.configuration.kubeRoleNri | string | `""` | Override `kubeRole` for the NRI plugin only. Empty = falls back to `kubeRole`. Useful in projected-SA mode for privilege separation between webhook and DaemonSet identities. |
| vaultDbInjector.configuration.kubeRoleRenewer | string | `""` | Override `kubeRole` for the renewer Deployment only. Empty = falls back to `kubeRole`. In projected-SA mode this should point at the dedicated renewer Vault role + policy. |
| vaultDbInjector.configuration.kubeRoleRevoker | string | `""` | Override `kubeRole` for the revoker Deployment only. Empty = falls back to `kubeRole`. In projected-SA mode this should point at the dedicated revoker Vault role + policy. |
| vaultDbInjector.configuration.logLevel | string | `"info"` | Log level for all three binaries (`debug`, `info`, `warn`, `error`). |
| vaultDbInjector.configuration.sentry | bool | `true` | Enable Sentry error reporting in the binaries. |
| vaultDbInjector.configuration.sentryDsn | string | `"https://your-sentry@sentry/660"` | Sentry DSN. Required when `sentry: true`. |
| vaultDbInjector.configuration.tokenRequestAudiences | list | `[]` | Audiences set on the TokenRequest JWT in projected-SA mode. Empty = cluster-default audience (legacy compat). Recommended for new deployments: `["vault"]` with a matching `audience` configured on each Vault k8s-auth role. The binary refuses to start when `useProjectedSA: true` and this list is empty. |
| vaultDbInjector.configuration.tokenRequestExpirationSeconds | int | `600` | Requested lifetime of the TokenRequest JWT in seconds. The kube-apiserver enforces a hard floor of 600s (`--service-account-min-token-expiration` default). The JWT is used for one Vault login round-trip only, so 600 is the practical minimum. |
| vaultDbInjector.configuration.tokenTTL | string | `"8766h"` | Periodic Vault token TTL requested at login. Applies to the binaries' own login tokens, not to per-pod credentials. Default ≈ 1 year. |
| vaultDbInjector.configuration.useProjectedSA | bool | `false` | When `true`, the injector authenticates to Vault per-pod using a Kubernetes TokenRequest JWT for the admitted pod's ServiceAccount, instead of using the injector's own SA. Requires every Vault auth/kubernetes role to have `token_period > 0`. The chart provisions the ClusterRole granting `create` on `serviceaccounts/token`. |
| vaultDbInjector.configuration.vaultAddress | string | `"https://vault1.numberly.in:8200"` | Vault or OpenBao base URL the binaries log into. |
| vaultDbInjector.configuration.vaultAuthPath | string | `"kubernetes"` | Mount path of the Kubernetes auth method on Vault. |
| vaultDbInjector.configuration.vaultSecretName | string | `"vault-db-injector"` | Name of the KV-v2 mount used for per-pod bookkeeping (lease ID, token ID, namespace, UUID). Must match the path enabled in Vault and referenced in the policies. |
| vaultDbInjector.configuration.vaultSecretPrefix | string | `"kubernetes"` | First path segment under `vaultSecretName` used to scope bookkeeping per cluster. Production-grade policies should pin this segment to prevent cross-cluster reads/writes. |
| vaultDbInjector.configuration.webhookFqdn | string | `"vault-db-injector.numberly.io"` | FQDN used in the webhook service name and the TLS cert SANs. |
| vaultDbInjector.configuration.webhookMatchLabels | string | `"vault-db-injector"` | Value of the `objectSelector` label on the MutatingWebhookConfiguration. Pods carrying this label are sent to the webhook for admission. |
| vaultDbInjector.injector.args | list | `["--config=/injector/config.yaml"]` | Arguments passed to the injector container. |
| vaultDbInjector.injector.containerSecurityContext | object | `{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true,"runAsGroup":65534,"runAsNonRoot":true,"runAsUser":65534}` | Pod-level securityContext applied to the injector container. Defaults are non-root + read-only root filesystem. |
| vaultDbInjector.injector.image.repository | string | `"numberly/vault-db-injector"` | Injector container image repository. |
| vaultDbInjector.injector.image.tag | string | `"3.2.1"` | Injector container image tag. |
| vaultDbInjector.injector.imagePullPolicy | string | `"Always"` | imagePullPolicy applied to the injector container. |
| vaultDbInjector.injector.ports | list | `[{"port":8443,"targetPort":8443}]` | Service ports for the webhook (HTTPS). |
| vaultDbInjector.injector.replicas | int | `2` | Number of injector Deployment replicas. |
| vaultDbInjector.injector.serviceAccount.annotations | object | `{}` | Annotations added to the injector ServiceAccount only (e.g. for IRSA / GCP Workload Identity). Not propagated to the renewer/revoker SAs. |
| vaultDbInjector.injector.serviceAccountName | string | `""` | Override the ServiceAccount used by the injector Deployment. Empty = chart-managed default (`<release>`). Set to bring your own SA provisioned outside the chart (e.g. shared SA across releases). |
| vaultDbInjector.injector.type | string | `"ClusterIP"` | Service type for the webhook service. |
| vaultDbInjector.renewer.args | list | `["--config=/renewer/config.yaml"]` | Arguments passed to the renewer container. |
| vaultDbInjector.renewer.containerSecurityContext | object | `{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true,"runAsGroup":65534,"runAsNonRoot":true,"runAsUser":65534}` | Pod-level securityContext applied to the renewer container. |
| vaultDbInjector.renewer.image.repository | string | `"numberly/vault-db-injector"` | Renewer container image repository. |
| vaultDbInjector.renewer.image.tag | string | `"3.2.1"` | Renewer container image tag. |
| vaultDbInjector.renewer.imagePullPolicy | string | `"Always"` | imagePullPolicy applied to the renewer container. |
| vaultDbInjector.renewer.replicas | int | `4` | Number of renewer replicas. Leader election selects one active renewer at a time. |
| vaultDbInjector.renewer.serviceAccountName | string | `""` | Override the ServiceAccount used by the renewer Deployment. Empty = chart-managed default (`<release>` in legacy mode, `<release>-renewer` when `useProjectedSA: true`). |
| vaultDbInjector.revoker.args | list | `["--config=/revoker/config.yaml"]` | Arguments passed to the revoker container. |
| vaultDbInjector.revoker.containerSecurityContext | object | `{"allowPrivilegeEscalation":false,"readOnlyRootFilesystem":true,"runAsGroup":65534,"runAsNonRoot":true,"runAsUser":65534}` | Pod-level securityContext applied to the revoker container. |
| vaultDbInjector.revoker.image.repository | string | `"numberly/vault-db-injector"` | Revoker container image repository. |
| vaultDbInjector.revoker.image.tag | string | `"3.2.1"` | Revoker container image tag. |
| vaultDbInjector.revoker.imagePullPolicy | string | `"Always"` | imagePullPolicy applied to the revoker container. |
| vaultDbInjector.revoker.replicas | int | `4` | Number of revoker replicas. Leader election selects one active revoker at a time. |
| vaultDbInjector.revoker.serviceAccountName | string | `""` | Override the ServiceAccount used by the revoker Deployment. Empty = chart-managed default (`<release>` in legacy mode, `<release>-revoker` when `useProjectedSA: true`). |

## Vault auth roles the operator must create when `useProjectedSA: true`

When projected-SA mode is enabled, the chart provisions dedicated
ServiceAccounts for the renewer and the revoker. The Vault operator must
create matching `auth/kubernetes/role` entries:

```
auth/kubernetes/role/<release>-renewer:
  bound_service_account_names = <release>-renewer
  bound_service_account_namespaces = <release-namespace>
  token_policies = <release>-renewer

auth/kubernetes/role/<release>-revoker:
  bound_service_account_names = <release>-revoker
  bound_service_account_namespaces = <release-namespace>
  token_policies = <release>-revoker
```

See [Vault policies and roles](https://numberly.github.io/vault-db-injector/getting-started/vault-policies/)
for the full policy HCL and `vault write` commands.

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
