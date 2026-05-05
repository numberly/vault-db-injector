# Migrating from v2.x to v3.0

**Audience:** Platform operator

> ⚠️ **v3.0 is a breaking release.** Read this entire document before
> upgrading. Plan a maintenance window if you run dashboards/alerts
> against the legacy metric names.

## TL;DR

Three things change between v2.x (current `main`) and v3.0:

1. **Two new injection modes** sit beside the legacy mutating webhook:
   the **NRI plugin mode** (creds resolved at container creation, no
   plaintext in PodSpec), and the **projected-SA Vault auth mode**
   (per-pod identity, native Vault attestation, least-privilege injector).
   Both are opt-in feature flags. The legacy webhook flow is unchanged
   when the flags are off.
2. **All Prometheus metric names** were renamed from the
   `vault_injector_*` prefix to `vdbi_*`. Dashboards, alert rules and
   recording rules MUST be updated before upgrading or you lose
   visibility.
3. **Helm values gain new keys** (`useProjectedSA`, `nri.enabled`, etc.)
   and **conditionally provision** new RBAC and ServiceAccounts. Default
   values keep behavior byte-identical to v2.x.

---

## Why upgrade

| Capability | v2.x | v3.0 |
|---|---|---|
| Pod identity attested by Vault | ❌ in-process check (`CanIGetRoles`) | ✅ native via `bound_service_account_names` |
| Injector blast radius if compromised | broad (can mint creds for any DB role) | scoped to the pod's role only (when projected mode is on) |
| Creds at rest in PodSpec | plaintext env vars | optional NRI mode resolves at container creation, no plaintext |
| Renewer / revoker policies | shared with injector (broad) | dedicated SAs + minimal Vault policies (when projected mode is on) |
| Token request audience constraint | n/a | configurable per Vault role |
| Observability | counters + leader gauge | + 4 new metrics (TokenRequest errors, Vault login error reasons, role misconfig, audience-unconstrained gauge) |

**You SHOULD upgrade if** any of the following apply:
- Your threat model treats the injector as a privileged single-point-of-compromise
- You want Vault audit logs to attribute credential issuance to the
  actual pod SA, not the injector SA
- You want to move credentials out of the cleartext PodSpec
  (NRI mode)

**You can stay on v2.x if** you're happy with the current trust model
and you don't have time to update dashboards/alerts.

---

## Breaking changes (the full list)

### B1. Metric names — `vault_injector_*` → `vdbi_*`

Every metric was renamed by prefix swap. Old behavior, same labels,
new name. Drop-in renaming.

The complete mapping (39 metric names):

| v2.x name | v3.0 name |
|---|---|
| `vault_injector_renew_token_count_success` | `vdbi_renew_token_count_success` |
| `vault_injector_renew_token_count_error` | `vdbi_renew_token_count_error` |
| `vault_injector_renew_lease_count_success` | `vdbi_renew_lease_count_success` |
| `vault_injector_renew_lease_count_error` | `vdbi_renew_lease_count_error` |
| `vault_injector_revoke_token_count_success` | `vdbi_revoke_token_count_success` |
| `vault_injector_revoke_token_count_error` | `vdbi_revoke_token_count_error` |
| `vault_injector_token_expiration` | `vdbi_token_expiration` |
| `vault_injector_lease_expiration` | `vdbi_lease_expiration` |
| `vault_injector_synchronization_count_success` | `vdbi_synchronization_count_success` |
| `vault_injector_synchronization_count_error` | `vdbi_synchronization_count_error` |
| `vault_injector_pod_cleanup_count_success` | `vdbi_pod_cleanup_count_success` |
| `vault_injector_pod_cleanup_count_error` | `vdbi_pod_cleanup_count_error` |
| `vault_injector_last_synchronization_success` | `vdbi_last_synchronization_success` |
| `vault_injector_orphan_ticket_created_count_success` | `vdbi_orphan_ticket_created_count_success` |
| `vault_injector_orphan_ticket_created_count_error` | `vdbi_orphan_ticket_created_count_error` |
| `vault_injector_store_data_count_success` | `vdbi_store_data_count_success` |
| `vault_injector_store_data_count_error` | `vdbi_store_data_count_error` |
| `vault_injector_delete_data_count_success` | `vdbi_delete_data_count_success` |
| `vault_injector_delete_data_count_error` | `vdbi_delete_data_count_error` |
| `vault_injector_connect_vault_count_success` | `vdbi_connect_vault_count_success` |
| `vault_injector_connect_vault_count_error` | `vdbi_connect_vault_count_error` |
| `vault_injector_service_account_authorized_count` | `vdbi_service_account_authorized_count` |
| `vault_injector_service_account_denied_count` | `vdbi_service_account_denied_count` |
| `vault_injector_last_synchronization_duration` | `vdbi_last_synchronization_duration` |
| `vault_injector_is_leader` | `vdbi_is_leader` |
| `vault_injector_leader_election_attempts_total` | `vdbi_leader_election_attempts_total` |
| `vault_injector_leader_election_duration_seconds` | `vdbi_leader_election_duration_seconds` |
| `vault_injector_fetch_pods_success_count` | `vdbi_fetch_pods_success_count` |
| `vault_injector_fetch_pods_error_count` | `vdbi_fetch_pods_error_count` |
| `vault_injector_mutated_pods_success_count` | `vdbi_mutated_pods_success_count` |
| `vault_injector_mutated_pods_error_count` | `vdbi_mutated_pods_error_count` |

New metrics in v3.0 (no v2.x equivalent):

| New metric | Type | Purpose |
|---|---|---|
| `vdbi_nri_substitutions_total` | counter | NRI plugin substituted env at CreateContainer |
| `vdbi_nri_unwrap_failures_total{reason}` | counter | NRI plugin failed to fetch a credential |
| `vdbi_token_request_errors_total{reason}` | counter | Kubernetes TokenRequest failed (projected mode) |
| `vdbi_vault_login_errors_total{reason,auth_mode}` | counter | Vault login failed; `auth_mode` is `legacy` or `projected` |
| `vdbi_projected_role_misconfigured_total{role}` | counter | A Vault role used in projected mode lacks `token_period > 0` |
| `vdbi_nri_resolve_duplicate_total` | counter | Concurrent in-flight `resolveMapping` calls collapsed by singleflight. Should stay near 0; spikes indicate sidecar/main race we successfully prevent from issuing duplicate creds. |

**Migration**: see "Updating dashboards & alerts" below for an automated `sed` recipe.

### B2. Helm chart changes

The chart **always** creates `<release>-renewer` and `<release>-revoker`
ServiceAccounts and binds the existing renewer/revoker pods to them
when `useProjectedSA: true`. When `useProjectedSA: false` (default),
those SAs are not created and the existing single-SA topology is preserved
— byte-identical to v2.x.

### B3. Config schema additions

New keys under `vaultDbInjector.configuration`:

```yaml
useProjectedSA: false                    # default false
tokenRequestAudiences: []                # default empty
tokenRequestExpirationSeconds: 600       # default 600s (apiserver minimum)
kubeRoleNri: ""                          # optional override; falls back to kubeRole
kubeRoleRenewer: ""                      # optional override; falls back to kubeRole
kubeRoleRevoker: ""                      # optional override; falls back to kubeRole
```

Plus the entire `nri:` top-level block (see [NRI security model](security.md)). Defaults
keep all new features OFF.

> ⚠️ **Hard-fail validation**: when `useProjectedSA: true` is set, the
> binary now refuses to start unless `tokenRequestAudiences` is non-empty.
> An empty audience disables the cryptographic SA-impersonation bound
> (any JWT bearer can present any SA's token to any service that does not
> strictly check the audience), defeating the security goal of projected
> mode. Set `tokenRequestAudiences: ["vault"]` (or your matching audience
> name) before flipping the flag.

### B4. CanIGetRoles is skipped in projected mode

When `useProjectedSA: true`, the in-process `CanIGetRoles` check is
**not** invoked because Vault performs the equivalent attestation
natively at login time. In legacy mode (`useProjectedSA: false`),
`CanIGetRoles` is unchanged.

### B5. Dual Vault identity in projected-SA mode

In projected-SA mode the injector holds two distinct Vault tokens per
pod credential fetch: the pod-token (issued via the pod's projected
ServiceAccount TokenRequest, used for `database/creds`) and the
bookkeeping token (`K8sSaVaultToken`, issued via the injector's own SA,
used for KV writes and lease management). Cleanup paths use
`conn.GetToken()` for the pod-token and `conn.K8sSaVaultToken` for the
bookkeeping token. External operators and out-of-tree `pkg/vault`
importers should use these accessors; the deprecated `PodVaultToken`
field has been removed.

### B6. Multi-dbConfiguration in NRI mode now works correctly

Previously, pods with multiple `db-creds-injector.numberly.io/role-N`
annotations in NRI mode would only have their **first** dbConfig
credential pair resolved; all other placeholder pairs were left
unsubstituted (app would crash with a literal placeholder as password).

This is fixed: the webhook now stamps one UUID per dbConfig into the
`db-creds-injector.numberly.io/uuid` annotation, and the NRI plugin
iterates all dbConfigs using those UUIDs as distinct KV keys.

**Upgrade behavior**: pods admitted before upgrading to this version
carry no UUID annotation. The NRI plugin falls back to the pod UID for
the first dbConfig only (preserving single-dbConfig behavior). Pods
with multiple dbConfigs must be re-rolled after the upgrade to get the
UUID annotation stamped for all dbConfigs.

### B7. Renewer and revoker responsibility split (projected-SA mode)

The renewer's safety-net cleanup logic (revoking + KV-deleting orphaned
entries for pods that no longer exist) has been moved to the revoker as
a periodic ticker (5-minute interval). Two consequences:

1. The **renewer Vault policy** is now strictly minimal: read on KV +
   `auth/token/renew` + `sys/leases/renew` + `auth/token/renew-self` +
   `sys/health`. Notably, it no longer needs `auth/token/revoke-orphan`
   nor KV `delete`. If you previously granted the wider policy following
   an earlier version of the doc, it's safe (and recommended) to revoke
   the extra capabilities.

2. The **revoker Vault policy** now needs `sys/leases/lookup` (used to
   look up lease metadata when running the safety-net sync). Add this
   capability to your `vault-db-revoker` policy before upgrading.

See [Vault policies](../getting-started/vault-policies.md) §2b (renewer) and §2c (revoker) for
the exact policy blocks.

---

## What does NOT change

- All annotations on user pods (`db-creds-injector.numberly.io/*`).
- Vault KV layout for stored lease/token information.
- Renewer / revoker behavior on existing leases.
- The mutating webhook URL, certificate bootstrap, NetworkPolicy.
- Default Helm values (legacy webhook + plaintext envs unless flags flipped).

A v2.x cluster upgraded to v3.0 with **no values changes** runs the
exact same flow it ran before, with the same observable behavior
modulo metric names.

- All annotations on user pods (`db-creds-injector.numberly.io/*`).
- Vault KV layout for stored lease/token information.
- Renewer / revoker behavior on existing leases.
- The mutating webhook URL, certificate bootstrap, NetworkPolicy.
- Default Helm values (legacy webhook + plaintext envs unless flags flipped).

A v2.x cluster upgraded to v3.0 with **no values changes** runs the
exact same flow it ran before, with the same observable behavior
modulo metric names.

---

## Pre-migration checklist

Before `helm upgrade`:

- [ ] **Inventory dashboards**: list all Grafana panels and Prometheus
  rules referencing `vault_injector_*` metric names.
- [ ] **Inventory alerts**: same for Alertmanager rules.
- [ ] **Decide your target topology** for v3.0:
  - Stay on legacy webhook? You're done — just update metric names.
  - Move to NRI mode? Read [NRI security model](security.md). Cluster prerequisite:
    containerd ≥ 1.7 with NRI enabled, or CRI-O ≥ 1.26.
  - Move to projected-SA mode? Read [Vault policies](../getting-started/vault-policies.md). Vault prerequisite:
    every k8s-auth role used by injected pods needs `token_period > 0`,
    and dedicated `<release>-renewer`/`<release>-revoker` Vault roles
    need to exist before the chart upgrade.
- [ ] **Plan a rollback window**: keep the v2.x chart and image tag
  pinned in case you need to roll back.

---

## Migration steps

### Step 1 — Update dashboards & alerts (do this BEFORE the chart upgrade)

Use any of:

```bash
# Grafana JSON dashboards
sed -i 's/vault_injector_/vdbi_/g' grafana-dashboards/*.json

# Prometheus rule files
sed -i 's/vault_injector_/vdbi_/g' prometheus-rules/*.yml

# Alertmanager rule files (when alerts include the metric in expr)
sed -i 's/vault_injector_/vdbi_/g' alertmanager-rules/*.yml
```

Reload Prometheus & Alertmanager. Validate that PromQL queries in
Grafana still resolve (they will return **no data** until v3.0 is
deployed; that's expected).

> Note: the legacy `vault_injector_*` and the new `vdbi_*` series
> are **not** dual-emitted in v3.0. There is no overlap window. Plan
> a brief observability gap during the rollout, or run dashboards
> with both names temporarily (`vault_injector_X OR vdbi_X`) during
> the transition.

### Step 2 — Upgrade chart with flags OFF (no behavior change)

```bash
helm upgrade <release> ./helm/ \
  --reuse-values \
  --version 3.0.0
```

Default values keep:
- `vaultDbInjector.configuration.useProjectedSA: false`
- `nri.enabled: false`

Validate:
- All pods reach `Ready`.
- Renewer & revoker continue to renew/revoke existing leases.
- New `vdbi_*` metrics start populating.
- No existing pod is denied/disrupted.

This step is the safest point to commit the upgrade. If anything is
broken, see "Rollback".

### Step 3 (optional) — Enable NRI mode

If your cluster meets the prerequisites and you want creds out of
plaintext PodSpec:

```yaml
nri:
  enabled: true
  pluginIndex: "10"     # must be unique per containerd instance
```

See [NRI security model](security.md) for the full prerequisite list, the NRI socket path,
and per-runtime caveats. Roll out one cluster at a time and validate
at least one credential injection end-to-end before proceeding.

### Step 4 (optional) — Enable projected-SA mode

This is the largest change and requires Vault-side preparation.
Follow [Vault policies](../getting-started/vault-policies.md) step by step.

In summary:

1. Pre-Vault: configure `token_period > 0` on every k8s-auth role used
   by injected pods. Create `<release>-renewer` and `<release>-revoker`
   Vault roles + policies.
2. Per cluster, set `vaultDbInjector.configuration.useProjectedSA: true`
   in values. The chart now provisions:
   - The `serviceaccounts/token` ClusterRole for the injector SA
   - The dedicated `-renewer` and `-revoker` SAs and their bindings
   - Renewer/revoker deployments switch to use the dedicated SAs
3. Validate: existing pods continue to renew normally; newly admitted
   pods get a Vault token whose `policies` list contains only their
   role policy (verifiable via `vault token lookup <stored-tokenID>`).

> ⚠️ **Important**: when you flip `useProjectedSA: true`, the chart
> immediately switches the renewer and revoker Deployments to use
> dedicated ServiceAccounts (`<release>-renewer`, `<release>-revoker`).
> Vault auth/kubernetes roles bound to these SAs (`<release>-renewer`,
> `<release>-revoker`) MUST exist BEFORE the chart upgrade, otherwise
> the renewer/revoker pods will crash-loop on Vault login and existing
> leases will silently expire at TTL.
>
> Recommended order:
> 1. Create the new Vault roles + policies (see [Vault policies](../getting-started/vault-policies.md) (renewer + revoker sections))
> 2. `helm upgrade` with `useProjectedSA: true`
> 3. Verify renewer/revoker pods Ready
> 4. Optionally: tighten the legacy injector policy (see §4 of that doc)

---

## Rollback

The legacy path is preserved unconditionally — rollback is just a
`helm rollback`:

```bash
helm rollback <release> <previous-revision>
```

Caveats:
- If you renamed dashboards/alerts (Step 1) before downgrading, they
  will see no data until you revert the rename or run dual queries.
- If you enabled `useProjectedSA: true` (Step 4) and Vault-side roles
  still expect the dedicated renewer/revoker SAs, downgrade leaves
  those Vault roles orphan but harmless. Clean them up at leisure.
- If you enabled NRI mode (Step 3) and credentials were injected via
  NRI, those pods continue to have valid creds (NRI didn't change
  Vault state) — but they will need to be re-rolled to switch back to
  plaintext-env mode if you want the legacy behavior.

---

## Troubleshooting

| Symptom after upgrade | Likely cause |
|---|---|
| Grafana panel "no data" | Step 1 (rename) skipped — dashboards still query `vault_injector_*` |
| Renewer pods CrashLoop with Vault `permission denied` | `useProjectedSA: true` but Vault role `<release>-renewer` not yet created with `bound_service_account_names: <release>-renewer` |
| `vdbi_token_request_errors_total{reason="rbac_denied"}` increases | `useProjectedSA: true` but ClusterRoleBinding for `serviceaccounts/token` not yet applied |
| `vdbi_vault_login_errors_total{reason="audience_mismatch"}` | Vault role has `audience="vault"` but `tokenRequestAudiences: []` (or vice versa) |
| `vdbi_projected_role_misconfigured_total{role=…} > 0` | The named Vault role lacks `token_period`; pod-token will die at `token_max_ttl` |
| Injector pod fails to start with `tokenRequestAudiences must be set` | `useProjectedSA: true` but `tokenRequestAudiences: []`. The chart now hard-fails at startup to prevent silent security degradation. Set `tokenRequestAudiences: ["vault"]` (or your audience name) in values. |
| `vdbi_nri_resolve_duplicate_total > 0` | Sidecars or multi-container pods triggered concurrent `CreateContainer`. The plugin correctly de-duplicates via singleflight, so this is informational only — but persistently high values may indicate a hot pod creation pattern worth investigating. |
| NRI plugin pods CrashLoop | Cluster doesn't meet NRI prereqs (containerd < 1.7 without NRI plugin enabled). See [NRI security model](security.md) |

---

## Reference

- [`getting-started/vault-policies.md`](../getting-started/vault-policies.md) — projected-SA Vault auth deep dive
- [`operators/security.md`](security.md) — NRI plugin deep dive
- [`operators/monitoring.md`](monitoring.md) — full metric reference (v3.0 names)
- [`operators/monitoring.md`](monitoring.md) — example alert rules (v3.0 names)
