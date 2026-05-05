# Configuration reference

**Audience:** Platform operator, Contributor

## Binary modes

vault-db-injector ships a single binary that runs in one of three modes,
selected by the `mode` key in the config file passed via `--config`:

| Mode | What it does |
|---|---|
| `injector` | Runs the mutating admission webhook. Mutates PodSpecs at admission time. |
| `renewer` | Periodically iterates KV entries and renews tokens and leases before expiry. |
| `revoker` | Watches for pod `DELETE` events and revokes Vault tokens and leases. |

The NRI plugin is embedded in the `injector` binary and activates when
`nri.enabled=true` in Helm (which sets the appropriate config flag).

Each binary reads a YAML config file. All three share the same key schema;
keys that are irrelevant to a given mode are silently ignored.

## Full configuration key reference

| Key | Type | Default | Used by | Purpose |
|---|---|---|---|---|
| `vaultAddress` | string | — | all | Vault or OpenBao base URL (e.g. `https://vault.example.com:8200`) |
| `vaultAuthPath` | string | `kubernetes` | all | Kubernetes auth method mount path on Vault |
| `kubeRole` | string | — | all | Default Vault auth/kubernetes role for binary login |
| `kubeRoleNri` | string | falls back to `kubeRole` | NRI plugin | Override Vault role for the NRI plugin's Vault login |
| `kubeRoleRenewer` | string | falls back to `kubeRole` | renewer | Override Vault role for the renewer's Vault login |
| `kubeRoleRevoker` | string | falls back to `kubeRole` | revoker | Override Vault role for the revoker's Vault login |
| `tokenTTL` | duration | `8766h` | injector | Periodic token TTL requested at login |
| `vaultSecretName` | string | `vault-injector` | all | Name of the KV-v2 mount used for per-pod bookkeeping |
| `vaultSecretPrefix` | string | `kubernetes` | all | Path prefix inside the KV mount |
| `useProjectedSA` | bool | `false` | injector, NRI | When `true`, issues a Kubernetes TokenRequest per admitted pod and uses it to log into Vault on the pod's behalf |
| `tokenRequestAudiences` | []string | `[]` | injector, NRI | Audiences set on the TokenRequest JWT. Must be non-empty when `useProjectedSA: true` |
| `tokenRequestExpirationSeconds` | int | `600` | injector, NRI | Requested lifetime of the TokenRequest JWT in seconds (kube-apiserver floor: 600s) |
| `injectorLabel` | string | `vault-db-injector` | injector, revoker | Pod label value used to select injected pods |
| `webhookMatchLabels` | string | `vault-db-injector` | injector | Value of the `objectSelector` label on the MutatingWebhookConfiguration |
| `mode` | string | — | all | `injector`, `renewer`, or `revoker` |
| `sentry` | bool | `false` | all | Enable Sentry error reporting |
| `sentryDsn` | string | — | all | Sentry DSN |
| `logLevel` | string | `info` | all | Log level (`debug`, `info`, `warn`, `error`) — passed to logrus |
| `SyncTTLSecond` | int | `300` | renewer | Interval in seconds between renewer synchronization sweeps |
| `defaultEngine` | string | `databases` | injector | Default Vault database secrets engine mount name |
| `certFile` | string | `/tls/tls.crt` | injector | TLS certificate for the webhook HTTPS server |
| `keyFile` | string | `/tls/tls.key` | injector | TLS private key for the webhook HTTPS server |

## Example: injector config

```yaml
certFile: /tls/tls.crt
keyFile: /tls/tls.key
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector
tokenTTL: 8766h
vaultSecretName: vault-injector
vaultSecretPrefix: kubernetes
mode: injector
useProjectedSA: true
tokenRequestAudiences:
  - vault
tokenRequestExpirationSeconds: 600
injectorLabel: vault-db-injector
webhookMatchLabels: vault-db-injector
logLevel: info
sentry: false
```

## Example: renewer config

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector-renewer
tokenTTL: 8766h
vaultSecretName: vault-injector
vaultSecretPrefix: kubernetes
mode: renewer
SyncTTLSecond: 300
logLevel: info
sentry: false
```

## Example: revoker config

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector-revoker
tokenTTL: 8766h
vaultSecretName: vault-injector
vaultSecretPrefix: kubernetes
mode: revoker
injectorLabel: vault-db-injector
logLevel: info
sentry: false
```

!!! warning
    When `useProjectedSA: true`, `tokenRequestAudiences` must be non-empty.
    The binary refuses to start and logs a fatal error if this constraint
    is violated. Set at least `["vault"]` and configure a matching `audience`
    on each Vault `auth/kubernetes` role.
