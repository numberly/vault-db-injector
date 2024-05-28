# getting-started

##  1. <a name='Requirements'></a>Requirements

To work correctly, the vault db injector need the following : 

- A working Vault instance
- A working Kubernetes Cluster
- A Database Engine (Postgres/MariaDB/MySQL/Oracle/...)

In this documentation, we assume that both of those following requirements are already installed.

##  2. <a name='Vocabulary'></a>Vocabulary

Here are some vocabulary that you need before starting the installation.

| name                        | description                 | default                     |
|-----------------------------|------------------------------------|-----------------------------|
| K/V Vault                   | Vault KVv2 path for injector state | vault-injector |
| vault policy                | Vault Injector policy to work properly | all-rw     |
| vault databases mount       | Vault database engine used to connect to our database | databases  |
| vault databases backend connection | Vault databases backend connection where we allow role to generate credentials | rw-pgsql2-pr |
| vault databases backend role | Vault databases role used to allow application on specific database | test-role |
| kubernetes auth backend     | Vault Backend used by Kubernetes Application to authenticate under Vault | kubernetes |
| kubernetes auth backend role | Vault Backend Role used by injector to authenticate under Vault | all-rw |
| postgresql | Database engine used to connecte under vault | rw-pgsql2-pr |
| database | database name to connect into | test |

##  3. <a name='VaultConfiguration'></a>Vault Configuration

First, we need to configure Vault to allow the injector to generate credentials on the `databases` engine and to store them inside the dedicated `K/V Vault`.


###  3.1. <a name='Createall-rwvault-policy'></a>Create `all-rw` vault-policy

We are going to start by create the `all-rw` `vault policy` that the injector will use :

```hcl
path "vault-injector/*" {
  capabilities = ["read", "list", "update", "create", "delete", "sudo"]
}
path "vault-injector/data/*" {
  capabilities = ["read", "list", "update", "create", "delete", "sudo"]
}
path "vault-injector/metadata/*" {
  capabilities = ["read", "list", "update", "create", "delete", "sudo"]
}
path "rw-pgsql2-pr/creds/*" {
  capabilities = ["read"]
}
path "auth/kubernetes/role/*" {
  capabilities = ["read"]
}
path "sys/leases/renew" {
  capabilities = ["create"]
}
path "auth/token/renew-self" {
  capabilities = ["create"]
}
path "auth/token/renew" {
  capabilities = ["create", "update"]
}
path "auth/token/revoke" {
  capabilities = ["create", "update"]
}
path "auth/token/create" {
  capabilities = ["create", "update", "read"]
}
path "auth/token/create-orphan" {
  capabilities = ["create", "update", "read", "sudo"]
}
path "auth/token/revoke-orphan" {
  capabilities = ["create", "update", "sudo"]
}
```
###  3.2. <a name='CreateKVVault'></a>Create `K/V Vault`

We need to create the `K/V Vault` in version v2 named vault-injector.

You can use the following documentation : [vault-injector](https://developer.hashicorp.com/vault/docs/secrets/kv/kv-v2)

###  3.3. <a name='Createvaultdatabasesmount'></a>Create `vault databases mount`

We need to create a `vault databases mount` engine named databases.

Here is a terraform example :
```hcl
resource "vault_mount" "databases" {
  path                  = "databases"
  type                  = "database"
  description           = "databases authentication automation"
  max_lease_ttl_seconds = "31536000"
}
```

###  3.4. <a name='Createvaultdatabasesbackendconnection'></a>Create `vault databases backend connection`

We need to create a `vault databases backend connection` on the vault `databases` engine. As you can see below, we allow the `test-role` role.

Here is a terraform example : 
```hcl
resource "vault_database_secret_backend_connection" "pgsql2" {
  backend = vault_mount.databases.path
  name    = "pgsql2"
  allowed_roles = [
    "test-role",
  ]

  postgresql {
    connection_url    = "postgres://{{username}}:{{password}}@rw-pgsql2-pr:5432/postgres?sslmode=verify-full"
    username          = "postgres"
    password          = "my-password"
    username_template = "{{.RoleName}}-{{unix_time}}-{{random 8}}"
  }
}
```

###  3.5. <a name='Createakubernetesauthbackend'></a>Create a `kubernetes auth backend`

We need to create a `kubernetes auth backend` to allow serviceAccount to connect under Vault.

You can use the following documentation : [kubernetes](https://developer.hashicorp.com/vault/docs/auth/kubernetes)

###  3.6. <a name='Createakubernetesauthbackendrole'></a>Create a `kubernetes auth backend role`

We need to create a `kubernetes auth backend role` to allow the service account of the vault-db-injector to connect under Vault.

Here is a terraform example : 
```hcl
resource "vault_kubernetes_auth_backend_role" "all_rw" {
  backend                          = "kubernetes"
  role_name                        = "all-rw"
  bound_service_account_names      = ["vault-db-injector"]
  bound_service_account_namespaces = ["vault-db-injector"]
  token_ttl                        = 3600
  token_policies                   = [vault_policy.all_rw.name] # remember the one created before
  token_bound_cidrs                = ["10.17.0.0/16"] # Your pod CIDR
}
```

We need to create a `kubernetes auth backend role` to allow the service account of our application to get generated credentials from vault
Here is a terraform example : 
```hcl
resource "vault_policy" "policy" {
  provider = vault.main
  name     = var.service_account

  policy = <<EOT
path "pgsql2/creds/test-role" {
  capabilities = ["read"]
}
EOT
}

resource "vault_kubernetes_auth_backend_role" "role" {
  provider                         = vault.main
  backend                          = kubernetes
  role_name                        = "test"
  bound_service_account_names      = ["test"]
  bound_service_account_namespaces = ["test"]
  token_ttl                        = 3600
  token_policies                   = [vault_policy.policy.name]
  token_bound_cidrs                = ["10.17.0.0/16"]
}
```


###  3.7. <a name='Createvaultdatabasesbackendrole'></a>Create `vault databases backend role`

We need to create a `vault databases backend role` to allow our application to consume `vault databases backend connection`

Here is a terraform example : 

```hcl
resource "vault_database_secret_backend_role" "role" {
  provider    = vault.main
  backend     = "databases"
  name        = test-role
  db_name     = test
  default_ttl = 3600
  creation_statements = [
    "CREATE ROLE \"{{name}}\" WITH LOGIN PASSWORD '{{password}}' VALID UNTIL '{{expiration}}' IN ROLE \"test\";",
    "ALTER ROLE \"{{name}}\" SET ROLE \"test\";",
  ]
  revocation_statements = [
    "DROP ROLE \"{{name}}\";"
  ]
}
```

##  4. <a name='Databaseconfiguration'></a>`Database` configuration

You need to create you database under postgres, here is an example for postgres : 

```sql
CREATE DATABASE test;
CREATE ROLE test;
revoke all on database test from public cascade;
grant connect on database test to test;
\c test
grant create, usage on schema public to test;
grant temporary on database test to test;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO test;
REVOKE ALL ON pg_user FROM public;
REVOKE ALL ON pg_roles FROM public;
REVOKE ALL ON pg_group FROM public;
REVOKE ALL ON pg_authid FROM public;
REVOKE ALL ON pg_auth_members FROM public;
REVOKE ALL ON pg_database FROM public;
REVOKE ALL ON pg_tablespace FROM public;
REVOKE ALL ON pg_settings FROM public;
```

##  5. <a name='Deploythevaultdbinjector'></a>Deploy the vault db injector

Now that you have a vault correctly configured and a database ready to be used, we can deploy our vault-db-injector application : 

For this, its quit easy, you just need to use the help chart.

```bash
kubectl create namespace vault-db-injector
helm upgrade --install vault-db-injector . --namespace vault-db-injector
```

When everything is Okay, you should have something like this : 
```
NAME                                         READY   STATUS    RESTARTS   AGE
vault-db-injector-7f74977b7c-88vvg           1/1     Running   0          29s
vault-db-injector-7f74977b7c-rq6mt           1/1     Running   0          29s
vault-db-injector-renewer-6496b84df-77skb    1/1     Running   0          29s
vault-db-injector-renewer-6496b84df-96zz4    1/1     Running   0          29s
vault-db-injector-renewer-6496b84df-tdp4r    1/1     Running   0          29s
vault-db-injector-renewer-6496b84df-wpd8x    1/1     Running   0          29s
vault-db-injector-revoker-7965857f75-2m5qp   1/1     Running   0          28s
vault-db-injector-revoker-7965857f75-5msv6   1/1     Running   0          29s
vault-db-injector-revoker-7965857f75-n29wp   1/1     Running   0          29s
vault-db-injector-revoker-7965857f75-th9vs   1/1     Running   0          28s
```

##  6. <a name='Deployanexampleapplication:'></a>Deploy an example application : 

Here is an example application deploy on the namespace test that will connect to our database test with the service account test.
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: test
  namespace: test
---
apiVersion: v1
kind: Pod
metadata:
  annotations:
    db-creds-injector.numberly.io/test.env-key-uri: POSTGRES_URL
    db-creds-injector.numberly.io/test.template: postgresql://@rw-pgsql2-pr:5432/test?sslmode=require
    db-creds-injector.numberly.io/test.mode: uri
    db-creds-injector.numberly.io/test.role: test-role
  labels:
    client: numberly
    vault-db-injector: "true"
  name: test
  namespace: test
spec:
  containers:
  - image: postgres:15.5
    command: ["/bin/bash", "-c"]
    args: ["sleep 3000000"]
    name: test
    resources: {}
    securityContext:
      allowPrivilegeEscalation: false
      capabilities:
        drop:
        - ALL
      readOnlyRootFilesystem: true
      runAsNonRoot: true
      runAsUser: 65534
  dnsPolicy: ClusterFirst
  restartPolicy: Always
  serviceAccount: test
  serviceAccountName: test

```

To deploy it : 
```bash
kubectl apply -f test.yaml
```

Wait until your application is ready and you should be able to exec inside the pods and connect to the database.

