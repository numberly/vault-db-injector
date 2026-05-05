# Setup: Database

**Audience:** Platform operator

This page prepares the database server itself. Vault-side wiring — the database connection config and dynamic role — comes on the next page. You need to do the server-side work first because Vault needs a login that can create roles.

## PostgreSQL example

Connect to your PostgreSQL instance as a superuser and run:

```sql
CREATE DATABASE myapp;
CREATE ROLE myapp_owner;
REVOKE ALL ON DATABASE myapp FROM PUBLIC;
GRANT CONNECT ON DATABASE myapp TO myapp_owner;
\c myapp
GRANT CREATE, USAGE ON SCHEMA public TO myapp_owner;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO myapp_owner;
REVOKE ALL ON pg_user, pg_roles, pg_authid, pg_database FROM PUBLIC;
```

This creates a database and an owner role with enough permissions for application roles to operate, while removing public access to sensitive catalog views.

## Create a Vault admin user on the database

```sql
CREATE ROLE vaultadmin WITH LOGIN SUPERUSER PASSWORD '<strong-random>';
```

!!! note
    `vaultadmin` is the credential Vault uses to `CREATE ROLE` dynamically when an application requests credentials. Vault never gives this account to applications — it uses it only to issue per-pod roles. The dynamic roles Vault creates are short-lived, named `vault-<role>-<timestamp>-<random>`, and dropped when revoked.

    Use a long, randomly generated password. Store it in a secret manager, not in version control.

## Other database engines

### MySQL

The setup follows the same pattern: create a Vault admin user (`CREATE USER 'vaultadmin'@'%' IDENTIFIED BY '...' WITH GRANT OPTION`), then configure the Vault `mysql-database-plugin` or `mysql-legacy-database-plugin`. See the [Vault MySQL plugin documentation](https://developer.hashicorp.com/vault/docs/secrets/databases/mysql-maria) for the connection URL format and required grants.

### MariaDB

MariaDB uses the same `mysql-database-plugin` in Vault as MySQL. The connection and admin user setup are identical, but the `creation_statements` SQL syntax differs slightly — MariaDB does not support all MySQL 8 privilege syntax. See the [Vault MariaDB plugin documentation](https://developer.hashicorp.com/vault/docs/secrets/databases/mysql-maria) for the correct `creation_statements` format.

### Oracle

Oracle requires the `oracle-database-plugin` (not bundled in Vault OSS). Create a DBA-level account for Vault, then configure the connection. See the [Vault Oracle plugin documentation](https://developer.hashicorp.com/vault/docs/secrets/databases/oracle).

## Next

[Vault policies and roles](vault-policies.md)
