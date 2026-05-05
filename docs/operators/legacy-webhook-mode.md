# Legacy webhook mode

**Audience:** Platform operator

!!! warning "Legacy mode"
    This page documents the v2.x behavior preserved in v3.0 for
    backward compatibility. New deployments should follow
    [Getting Started](../getting-started/overview.md), which walks the
    canonical NRI + Projected-SA path end to end.

In legacy mode the injector authenticates to Vault with **its own**
ServiceAccount, validates pod authorization in-process via
`CanIGetRoles`, then issues a Vault orphan token holding the role's
policy and uses it to call `database/creds/<role>`. Credentials land
in the PodSpec as plaintext env vars. The renewer and revoker share
the injector's SA and policy.

## When to keep this mode

- Your container runtime does not expose NRI (containerd < 1.7
  without the NRI plugin enabled, or CRI-O < 1.26).
- You are mid-migration from v2.x and need to run with
  `useProjectedSA: false` until your Vault roles are updated.
- You operate in a constrained environment where deploying a
  privileged DaemonSet (the NRI plugin runs as root) is a non-starter
  and the cleartext-PodSpec exposure is acceptable for your threat
  model.

In every other case, follow the canonical path. NRI + Projected-SA is
the recommended target for v3.0.

## Configuration

The legacy mode is the default when `useProjectedSA: false` and
`nri.enabled: false`. Each binary reads a YAML config selected by the
`--config` flag.

**Injector** — `--config=/injector/config.yaml`:

```yaml
certFile: /tls/tls.crt
keyFile: /tls/tls.key
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
logLevel: info
kubeRole: vault-db-injector
tokenTTL: 768h
vaultSecretName: vault-injector
vaultSecretPrefix: kubernetes
mode: injector
sentry: false
injectorLabel: vault-db-injector
defaultEngine: database
```

**Renewer** — `--config=/renewer/config.yaml`:

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
logLevel: info
kubeRole: vault-db-injector
tokenTTL: 768h
vaultSecretName: vault-injector
vaultSecretPrefix: kubernetes
mode: renewer
SyncTTLSecond: 300
injectorLabel: vault-db-injector
defaultEngine: database
```

**Revoker** — `--config=/revoker/config.yaml`:

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
logLevel: info
kubeRole: vault-db-injector
tokenTTL: 768h
vaultSecretName: vault-injector
vaultSecretPrefix: kubernetes
mode: revoker
injectorLabel: vault-db-injector
defaultEngine: database
```

## Vault policies

Legacy mode needs **one** Vault policy for the injector and **one**
Vault role under `auth/kubernetes/role/` bound to the injector SA. The
renewer and revoker share the same policy and role.

### Policy `vault-db-injector` (legacy)

```hcl
# --- KV-v2 bookkeeping ---
path "vault-injector/data/+/+" {
  capabilities = ["create", "read", "update", "delete"]
}
path "vault-injector/metadata/+/+" {
  capabilities = ["read", "delete", "list"]
}

# --- Vault token operations ---
path "auth/token/create-orphan" {
  capabilities = ["update", "sudo"]
}
path "auth/token/revoke-orphan" {
  capabilities = ["update", "sudo"]
}
path "auth/token/revoke" {
  capabilities = ["update"]
}
path "auth/token/renew" {
  capabilities = ["update"]
}
path "auth/token/renew-self" {
  capabilities = ["update"]
}
path "auth/token/lookup-self" {
  capabilities = ["read"]
}

# --- Authorization check (CanIGetRoles) ---
path "auth/kubernetes/role/*" {
  capabilities = ["read"]
}

# --- Database credential issuance ---
path "database/creds/*" {
  capabilities = ["read"]
}

# --- Probes ---
path "sys/health" {
  capabilities = ["read"]
}
```

### Role `vault-db-injector` (legacy)

```bash
vault write auth/kubernetes/role/vault-db-injector \
    bound_service_account_names="vault-db-injector" \
    bound_service_account_namespaces="vault-db-injector" \
    token_policies="vault-db-injector" \
    token_ttl="1h" \
    token_max_ttl="24h"
```

The Helm value `vaultDbInjector.configuration.kubeRole` must equal
this role name.

### Renewer and revoker

They **share** the injector SA and the same `vault-db-injector`
policy. No additional Vault objects are required.

## Annotations

Annotations are identical across modes. See the
[annotations reference](../developers/annotations.md) for the full
list.

## Limitations

- **Cleartext credentials in PodSpec** — visible in `kubectl get pod
  -o yaml`, in etcd, in audit logs, and in any GitOps backup.
- **Broad injector blast radius** — a compromised injector pod can
  mint credentials for every DB role configured under `database/`.
- **Shared SA across injector / renewer / revoker** — minimum-privilege
  is impossible without the projected-SA split.
- **No native pod attestation** — Vault sees the injector's SA, not
  the pod's. The Vault audit log cannot pin issuance to a specific
  pod without correlating against the KV bookkeeping mount.

When you are ready to move off this mode, see
[migration to v3.0 with projected-SA](migration-v2-to-v3.md).
