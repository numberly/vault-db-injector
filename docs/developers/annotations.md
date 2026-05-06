# Annotations

**Audience:** Application developer

Label and annotations that vault-db-injector reads when it admits your pod.

## Required label

Every pod that should receive injected credentials must carry this label:

```yaml
labels:
  vault-db-injector: "true"
```

Without this label, the webhook ignores the pod entirely — no
annotations are read, no credentials are issued.

## Annotation reference

All annotations share the prefix `db-creds-injector.numberly.io/`.
Annotations that target a specific database use `<dbname>` as a
user-chosen segment — pick any name that identifies the connection
(`myapp`, `analytics`, `pg`, etc.).

| Annotation | Required? | Default | Purpose |
|---|---|---|---|
| `cluster` | optional | `database` | Vault database secrets engine mount path |
| `<dbname>.role` | yes | — | Vault database role to issue credentials from |
| `<dbname>.mode` | yes | `classic` | Annotation mode: `classic` or `uri` |
| `<dbname>.env-key-dbuser` | optional | `DBUSER` | Env var name that receives the username (classic mode) |
| `<dbname>.env-key-dbpassword` | optional | `DBPASSWORD` | Env var name that receives the password (classic mode) |
| `<dbname>.env-key-uri` | required if `mode=uri` | — | Env var name that receives the connection URI |
| `<dbname>.template` | required if `mode=uri` | — | URI template; `{{user}}` and `{{password}}` are substituted by the webhook |
| `<dbname>.uuid` | auto-set | — | Per-dbConfig UUID written by the webhook. **Do not set this manually.** |

### `cluster`

The Vault database secrets engine mount. Defaults to `database`.
Override only if your operator has configured a non-default mount:

```yaml
db-creds-injector.numberly.io/cluster: database
```

### `<dbname>.role`

The Vault role under `database/roles/<name>` that the webhook uses to
issue credentials. Your operator creates this role; confirm the name
with them.

```yaml
db-creds-injector.numberly.io/myapp.role: myapp-prod
```

### `<dbname>.mode`

Two annotation modes control how credentials are surfaced as env vars:

- `classic` — two separate env vars: one for username, one for password
- `uri` — one env var containing a rendered connection URI

The annotation mode is independent of the delivery mode (NRI or
webhook). Both modes work with both delivery modes.

### `<dbname>.uuid`

Written by the webhook at admission. The renewer and revoker use this
UUID to correlate the credential lease with the pod. Do not set it
manually — the webhook overwrites any value you provide.

## Multi-database injection

`<dbname>` is arbitrary. Add multiple groups of `<dbname>.*` annotations
to inject credentials for more than one database into the same pod:

```yaml
db-creds-injector.numberly.io/cluster: database
db-creds-injector.numberly.io/primary.role: myapp-prod
db-creds-injector.numberly.io/primary.mode: classic
db-creds-injector.numberly.io/primary.env-key-dbuser: PG_USER
db-creds-injector.numberly.io/primary.env-key-dbpassword: PG_PASS
db-creds-injector.numberly.io/analytics.role: myapp-analytics
db-creds-injector.numberly.io/analytics.mode: uri
db-creds-injector.numberly.io/analytics.template: postgresql://@analytics-db.svc:5432/analytics?sslmode=require
db-creds-injector.numberly.io/analytics.env-key-uri: ANALYTICS_URL
```

Each `<dbname>` group results in an independent credential request,
separate lease, and separate env var set. See [examples](examples.md)
for full Pod manifests.
