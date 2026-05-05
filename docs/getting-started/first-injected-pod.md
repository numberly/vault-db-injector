# First injected pod

**Audience:** Application developer

## Annotate your pod

Apply this manifest. It creates a ServiceAccount and a Pod in the `team-myapp` namespace using the `myapp-prod` Vault role configured in [Vault policies and roles](vault-policies.md).

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: myapp
  namespace: team-myapp
---
apiVersion: v1
kind: Pod
metadata:
  name: myapp
  namespace: team-myapp
  annotations:
    db-creds-injector.numberly.io/cluster: database
    db-creds-injector.numberly.io/myapp.role: myapp-prod
    db-creds-injector.numberly.io/myapp.mode: classic
    db-creds-injector.numberly.io/myapp.env-key-dbuser: DB_USER
    db-creds-injector.numberly.io/myapp.env-key-dbpassword: DB_PASS
  labels:
    vault-db-injector: "true"
spec:
  serviceAccountName: myapp
  containers:
    - name: app
      image: postgres:16
      command: ["sleep", "infinity"]
```

The `vault-db-injector: "true"` label is what triggers admission. The webhook sees the label, places opaque placeholders in the `DB_USER` and `DB_PASS` env vars, and the NRI plugin on the node substitutes the real credentials before the container starts.

Apply it:

```bash
kubectl apply -f myapp.yaml
```

## Verify the credentials work

```bash
kubectl -n team-myapp exec myapp -- bash -c 'env | grep DB_'
```

Expected output shows the real username and password, not a placeholder:
```
DB_USER=vault-myapp-prod-1746345600-x8k2j9ab
DB_PASS=A1B2-c3d4-E5F6-g7h8
```

Test the database connection:

```bash
kubectl -n team-myapp exec myapp -- bash -c \
  'PGPASSWORD=$DB_PASS psql -h db -U $DB_USER -d myapp -c "SELECT 1"'
```

Expected: `SELECT 1` returns a row. If the connection fails, check that the `db` hostname resolves from inside the pod and that the PostgreSQL server allows connections from the pod's IP.

## What just happened

- The webhook admitted the pod and replaced `DB_USER` and `DB_PASS` with 64-hex placeholders. The PodSpec stored in etcd contains only the placeholders.
- The NRI plugin on the node intercepted `CreateContainer`, read the pod's annotations, and used the pod's ServiceAccount to log into Vault as `myapp` in `team-myapp`.
- Vault authenticated the TokenRequest JWT, verified the ServiceAccount against `auth/kubernetes/role/myapp-prod`, and issued a short-lived database credential.
- The plugin substituted the placeholders with the real username and password in the container env before `runc` executed.
- The renewer now holds the pod-token and will renew it periodically. When the pod dies, the revoker revokes the token and the DB credential.

For a diagram of the full data flow, see [operators/architecture](../operators/architecture.md).

## Next steps

- Read [annotations reference](../developers/annotations.md) to learn URI mode and multi-database injection.
- Read [monitoring](../operators/monitoring.md) to wire Prometheus dashboards and alerts.
- Read [security](../operators/security.md) to harden the NRI DaemonSet.
