# Operations

**Audience:** Platform operator

This page covers the operational primitives shared across the renewer
and revoker: leader election, health checks, and running multiple
injector releases on the same cluster.

## Leader election

**File:** `pkg/leadership/leadership.go`

The renewer and revoker run multi-replica for HA but only one replica
must do the work at a time — duplicate renewals are wasteful and
duplicate revocations race. Both Deployments use Kubernetes Lease
objects via the standard `client-go/tools/leaderelection` package.

Each replica competes for a lease. The winner becomes the leader and
runs the periodic ticker (renewer) or the pod-watch (revoker). The
non-leaders idle until the leader's lease expires; one of them then
takes over within a few seconds.

The active leader emits `vdbi_is_leader{lease_name=...} = 1`; idle
replicas emit `0`. `vdbi_leader_election_attempts_total` and
`vdbi_leader_election_duration_seconds` give you the churn rate and
the wall-clock time the current leader has held the lease.

The webhook (injector) is stateless — every replica handles admission
calls in parallel without coordination.

## Health checks

**File:** `pkg/healthcheck/healthcheck.go`

Every binary serves two HTTP endpoints:

- `/healthz` — liveness. Returns 200 as long as the process is up
  enough to answer HTTP. Wire it to the kubelet liveness probe.
- `/readyz` — readiness. Returns 200 once Vault login has succeeded
  and (renewer/revoker) the leader-election machinery is initialized.
  Wire it to the kubelet readiness probe.

The chart's defaults already set both probes on every Deployment. If
you front the webhook with a Service, prefer `/readyz` for the
Service's readiness gate so admission traffic only hits replicas that
have a live Vault session.

## Multiple injector releases on one cluster

Two Helm releases (e.g. `prod` and `dev`) running side by side on the
same cluster need a few values overridden to avoid colliding on the
containerd NRI registration and the per-node cache file:

| Value | prod release | dev release |
|---|---|---|
| `nri.pluginIndex` | `"10"` (default) | `"11"` |
| `vaultDbInjector.configuration.webhookMatchLabels` | `vault-db-injector` | `vault-db-injector-dev` |

The chart auto-generates the per-release identifiers:

- `pluginName` = the Helm release fullname (unique per release)
- `cachePath` = `/run/<release-fullname>/nri/cache.json` (unique per
  release)
- `podLabel` = the `webhookMatchLabels` value (already release-specific)

Override `nri.pluginIndex` in the dev release so both indices coexist
on the same containerd. The `webhookMatchLabels` override partitions
which pods each release admits — a user pod labeled
`vault-db-injector: "true"` is owned by prod, a pod labeled
`vault-db-injector-dev: "true"` is owned by dev.
