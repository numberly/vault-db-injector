# Troubleshooting

**Audience:** Application developer

Symptoms diagnosable from the pod side. For Vault policy issues,
cluster configuration, or metric queries, see
[operators/monitoring](../operators/monitoring.md).

## Pod starts but cannot connect to the database

| Symptom | Likely cause | What to check |
|---|---|---|
| `authentication failed for user "DBUSER"` | Annotation mode defaults used — env var name is `DBUSER`, not what the app reads | Set `<dbname>.env-key-dbuser` and `<dbname>.env-key-dbpassword` explicitly |
| `role "DBUSER" does not exist` | Same as above, app is reading the default env var name | Confirm the env var names match what the app expects |
| `connection refused` | Wrong host or port in URI template | Check `<dbname>.template`; verify the DB hostname resolves from inside the pod |
| `SSL connection required` | App connects without TLS, DB requires it | Add `?sslmode=require` (or `verify-full`) to your URI template |
| Credentials work but expire after a few hours | `token_period` not set on the Vault role | Ask your operator to check `vault read auth/kubernetes/role/<role>` — `token_period` must be non-zero |

## Pod env contains `__VDBI_PH_...`

This placeholder is set by the webhook in NRI mode and should be
replaced by the NRI plugin before the container process starts.

If `kubectl exec -- env` still shows the placeholder string rather than
real credentials, the NRI plugin did not perform the substitution.

Common causes:

- **NRI plugin DaemonSet pod not running on the node.** Check that the
  DaemonSet has a ready pod on the same node as your pod:
  ```bash
  kubectl -n vault-db-injector get pods -o wide | grep nri
  kubectl get pod myapp -n team-myapp -o wide   # note the NODE
  ```
- **Pod was scheduled on a node where containerd NRI is not enabled.**
  The operator must enable NRI in containerd's configuration on every
  node that runs injected workloads.
- **Pod does not carry the correct label.** The NRI plugin filters pods
  by the `vault-db-injector: "true"` label (or the operator-configured
  equivalent). A pod admitted without that label receives placeholders
  that are never substituted.

The operator can check `vdbi_nri_unwrap_failures_total{reason}` to see
why substitution failed. See [operators/monitoring](../operators/monitoring.md)
for the metric reference.

## Pod stuck in `ContainerCreating` after admission

The NRI plugin runs synchronously at container creation. If the plugin
fails to resolve credentials, containerd may stall the container start.

Likely causes:

- **Vault login failed at NRI substitution.** The plugin authenticates
  to Vault using the pod's ServiceAccount. If the SA is not bound to
  any Vault role, the login is rejected.
- **Pod ServiceAccount not present in the Vault role's
  `bound_service_account_names`.** Ask your operator to verify the
  Vault role:
  ```bash
  vault read auth/kubernetes/role/<role-name>
  ```
  The SA name and namespace must appear in the output.
- **Pod admitted outside the webhook's namespace selector.** If the pod
  was created in a namespace not covered by the webhook, the webhook was
  not called, no placeholders were set, and the NRI plugin skips the
  pod (no placeholders to substitute). The pod should start normally in
  this case — if it stalls, it is unrelated to the injector.

The operator metric `vdbi_nri_unwrap_failures_total{reason}` is the
primary signal for NRI substitution failures. See
[operators/monitoring](../operators/monitoring.md) for the full metric
catalog and suggested alert rules.

## Checking the annotation the webhook set

After pod admission, inspect the annotations the webhook wrote:

```bash
kubectl -n team-myapp get pod myapp -o yaml | grep db-creds-injector
```

Verify that:

- Each `<dbname>.role` is present and points to the expected Vault role.
- Each `<dbname>.uuid` is set (written by the webhook at admission). If
  it is missing, the webhook did not process the pod.
- No stale or misspelled annotation keys are present.

## Webhook rejection messages

Pod events often contain the rejection reason from the webhook:

```bash
kubectl -n team-myapp describe pod myapp | grep -A10 Events
```

A `FailedCreate` or webhook-related event with a Vault error message
indicates the injector returned a non-200 response at admission time.
Share the full event text with your operator.
