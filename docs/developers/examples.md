# Examples

**Audience:** Application developer

All examples assume:

- Namespace: `team-myapp`
- ServiceAccount: `myapp` (must exist and be bound to the Vault role)
- Vault database roles: `myapp-prod`, `myapp-analytics`
- PostgreSQL server: `db.team-myapp.svc`

## Example 1 — Classic mode, single database

Two separate env vars: one for username, one for password.

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
      image: myapp:latest
      env:
        - name: DB_HOST
          value: db.team-myapp.svc
        - name: DB_NAME
          value: myapp
```

The app reads `DB_USER` and `DB_PASS` from its environment. Both vars
are populated before the container process starts.

## Example 2 — URI mode, single database

One env var containing a rendered connection URI. Useful when the
application expects a DSN or connection string rather than separate
host/user/password fields.

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
    db-creds-injector.numberly.io/myapp.mode: uri
    db-creds-injector.numberly.io/myapp.template: postgresql://{{user}}:{{password}}@db.team-myapp.svc:5432/myapp?sslmode=require
    db-creds-injector.numberly.io/myapp.env-key-uri: DATABASE_URL
  labels:
    vault-db-injector: "true"
spec:
  serviceAccountName: myapp
  containers:
    - name: app
      image: myapp:latest
```

The webhook replaces `{{user}}` and `{{password}}` in the template with
the issued credentials. The app reads `DATABASE_URL` which contains the
complete rendered URI — for example:
`postgresql://vault-myapp-prod-1746345600-x8k2j9ab:A1B2-c3d4@db.team-myapp.svc:5432/myapp?sslmode=require`.

## Example 3 — Multi-database, mixed modes

Two databases injected into the same pod: one with `classic` mode, one
with `uri` mode. Each `<dbname>` group is independent.

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
    # Primary database — classic mode
    db-creds-injector.numberly.io/primary.role: myapp-prod
    db-creds-injector.numberly.io/primary.mode: classic
    db-creds-injector.numberly.io/primary.env-key-dbuser: PG_USER
    db-creds-injector.numberly.io/primary.env-key-dbpassword: PG_PASS
    # Analytics database — URI mode
    db-creds-injector.numberly.io/analytics.role: myapp-analytics
    db-creds-injector.numberly.io/analytics.mode: uri
    db-creds-injector.numberly.io/analytics.template: postgresql://{{user}}:{{password}}@analytics-db.team-myapp.svc:5432/analytics?sslmode=require
    db-creds-injector.numberly.io/analytics.env-key-uri: ANALYTICS_URL
  labels:
    vault-db-injector: "true"
spec:
  serviceAccountName: myapp
  containers:
    - name: app
      image: myapp:latest
```

The app sees four env vars at runtime:

- `PG_USER` — username for the primary database
- `PG_PASS` — password for the primary database
- `ANALYTICS_URL` — rendered connection URI for the analytics database

Each credential set comes from a separate Vault lease. They are renewed
and revoked independently.

See [annotations](annotations.md) for the full annotation reference
and [injection-modes](injection-modes.md) to understand what the pod
environment looks like at runtime depending on the delivery mode.
