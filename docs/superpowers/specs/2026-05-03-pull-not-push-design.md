# Pull-not-push: stop putting Vault tokens in the PodSpec

**Status:** active
**Date:** 2026-05-03
**Owner:** @SoulKyu
**Closes:** Hunter-mode finding #H5

## Summary

Eliminate the wrap-token-in-annotation pattern. The webhook stops wrapping
and stores only `(db_path, db_role, placeholders, request_id)` in the
annotation. The plugin authenticates to Vault using its own SA token
and creates dynamic credentials at `CreateContainer` time, not at
admission.

This aligns the project with the consensus pattern across Vault-based
credential injectors (Vault Agent Injector, Bank-Vaults vault-secrets-
webhook, SPIFFE+Vault): identity is attested by Kubernetes, never by a
bearer token in the PodSpec.

## Threat model fixed

Anyone with `pods.get` on a namespace + Vault network access could read
the wrap_token from the annotation and call `vault sys/wrapping/unwrap`
themselves before the plugin did. The wrap_token IS its own auth
credential at the unwrap endpoint. Reproduction confirmed in Hunter mode.

After this change: the annotation contains no Vault credential. An
attacker reading the annotation gets only metadata (which db role is
requested for which placeholders) — useless without their own k8s SA
that maps to the role's Vault policy.

## Design

### Annotation schema (new)

```json
{
  "db_path": "databases",
  "db_role": "postgres-readonly",
  "placeholders": {
    "__VDBI_PH_<hex>___": "username",
    "__VDBI_PH_<hex>___": "password"
  },
  "request_id": "<uuid>"
}
```

`db_path` and `db_role` come from the existing pod annotations
`db-creds-injector.numberly.io/cluster` and
`db-creds-injector.numberly.io/<dbname>.role`. `request_id` is a UUID
the webhook generates for traceability.

No Vault token. No wrap. No bearer credential.

### Webhook flow

1. Pod admission with the existing `db-creds-injector.numberly.io/*` annotations.
2. Webhook authenticates to Vault as itself (existing path), runs
   `CanIGetRoles` to verify the pod's SA is allowed by the Vault role.
   This is the **policy gate**: if the pod's SA cannot use the role,
   admission rejected. (Same as today.)
3. If NRI mode enabled, webhook generates placeholders, stores them
   plus `(db_path, db_role, request_id)` in annotation. **Does NOT
   fetch credentials.** Pod is admitted with placeholder env values.
4. If NRI mode disabled, webhook still fetches creds and stuffs them
   directly in env (legacy mode unchanged).

### Plugin flow

1. At `CreateContainer`, plugin reads the annotation. If absent, no-op.
2. Cache hit by pod UID → reuse mapping from cache.
3. Cache miss:
   a. Plugin authenticates to Vault using its own SA token (k8s auth).
   b. Plugin re-runs `CanIGetRoles` with the **target pod's** SA + namespace
      to confirm the request is still valid (defense against annotation
      forgery via a controller that has `pods.update` but not the SA).
   c. Plugin calls `GetDbCredentials(db_path, db_role, podUID)`. The
      lease metadata gets tagged with the pod UID as before.
   d. Plugin builds `placeholder → real value` mapping from the credential
      payload and the annotation's `placeholders` mapping.
   e. Cache the mapping under the pod UID; persist to tmpfs.
4. Plugin emits `ContainerAdjustment` with substituted env.

### Lifecycle alignment with existing renewer/revoker

The existing renewer/revoker daemons watch Vault leases tagged with pod
UIDs and reconcile them against live pods in the cluster. Plugin
continues to tag leases with the pod UID it sees from NRI. No changes
needed in renewer/revoker.

The existing webhook-side credential creation path (legacy, non-NRI) is
unchanged.

### Plugin RBAC and Vault policy

Plugin needs:
- K8s RBAC: `pods get/list/watch` (already granted), nothing new.
- Vault policy: read on `auth/<authPath>/role/*` (for CanIGetRoles
  validation) AND read on `<dbPath>/creds/*` (for credential creation).
  This is broader than the current plugin's policy (only needed
  `sys/wrapping/unwrap`), but no broader than the webhook's existing
  policy. The plugin replaces the webhook in the credential-fetch path
  for NRI mode.

### What the attacker sees now

- `kubectl get pod -o yaml`: annotation is `{db_path, db_role, placeholders, request_id}`.
  No Vault token. Knowing the role does not let them request creds.
- They could craft a fake pod with annotation requesting any role they
  want, but Vault's `CanIGetRoles` blocks if their pod's SA isn't bound
  to that role. Same RBAC gate as today.
- The `kubectl exec` path remains a leak for those with that RBAC, but
  that's a known threat outside our scope.

## What stays the same

- Annotation key: `db-creds-injector.numberly.io/nri-mapping`
- Helm values, DS spec, RBAC for nodes (none — we removed that)
- `pkg/placeholder`, `pkg/vault` API surface
- Renewer / revoker behavior
- 9 known edge cases must still pass

## What breaks (tagged in PR as breaking change)

- Annotation schema: old wrap_token consumers fail. Anyone who has a pod
  with the old `{wrap_token, placeholders}` shape sees the plugin treat
  it as a malformed annotation (logged warning, no substitution) →
  visible failure → ops upgrades.
- `pkg/k8s.NRIMapping` Go struct gains `DbPath`, `DbRole`, `RequestID`,
  drops `WrapToken`.
- `pkg/vault.Connector.UnwrapValues` becomes unused (kept for any future
  use case).

## Tests

- 9 existing edges, re-run: must all pass.
- New edge: pod with annotation requesting role its SA isn't bound to →
  plugin refuses, container starts with placeholder, app crashes
  visibly (same fail mode as before).
- New edge: webhook emits annotation, plugin fetches creds, lease
  appears in vault tagged with pod UID. Verify renewer still picks it up.

## Risks

1. Plugin now talks to Vault from every node — more vault load.
   Mitigation: plugin caches per pod (no rapid-fire fetches).
2. Plugin policy is broader (database/creds/* read).
   Mitigation: same scope as webhook had; if webhook is trusted,
   plugin can be too.
3. Per-namespace policy isolation: a misconfigured cluster could let a
   plugin on node A serve creds for a role intended only for node B.
   Out of scope: same as today's webhook.

## Migration

Single PR on `feat/ebpf-injection-mode`. Bumps annotation schema. No
operator action beyond redeploy. The Helm chart's vault auth role
already grants the plugin enough scope (it inherits from the webhook's
ClusterRole binding). Verify on first deploy.
