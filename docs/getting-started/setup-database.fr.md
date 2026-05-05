# Configuration : Base de données

**Audience:** Opérateur de plateforme

Cette page prépare le serveur de base de données lui-même. Le câblage côté Vault — la configuration de connexion à la base de données et le rôle dynamique — est couvert sur la page suivante. Vous devez effectuer le travail côté serveur en premier car Vault a besoin d'un compte capable de créer des rôles.

## Exemple PostgreSQL

Connectez-vous à votre instance PostgreSQL en tant que superutilisateur et exécutez :

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

Cela crée une base de données et un rôle owner avec suffisamment de permissions pour que les rôles d'application puissent opérer, tout en supprimant l'accès public aux vues de catalogue sensibles.

## Créer un utilisateur admin Vault sur la base de données

```sql
CREATE ROLE vaultadmin WITH LOGIN SUPERUSER PASSWORD '<strong-random>';
```

!!! note
    `vaultadmin` est l'identifiant que Vault utilise pour `CREATE ROLE` dynamiquement lorsqu'une application demande des identifiants. Vault ne donne jamais ce compte aux applications — il l'utilise uniquement pour émettre des rôles par pod. Les rôles dynamiques créés par Vault sont éphémères, nommés `vault-<role>-<timestamp>-<random>`, et supprimés lors de la révocation.

    Utilisez un mot de passe long et généré aléatoirement. Stockez-le dans un gestionnaire de secrets, pas dans le contrôle de version.

## Autres moteurs de base de données

### MySQL

La configuration suit le même schéma : créez un utilisateur admin Vault (`CREATE USER 'vaultadmin'@'%' IDENTIFIED BY '...' WITH GRANT OPTION`), puis configurez le `mysql-database-plugin` ou `mysql-legacy-database-plugin` Vault. Consultez la [documentation du plugin MySQL Vault](https://developer.hashicorp.com/vault/docs/secrets/databases/mysql-maria) pour le format de l'URL de connexion et les grants requis.

### MariaDB

MariaDB utilise le même `mysql-database-plugin` dans Vault que MySQL. La configuration de la connexion et de l'utilisateur admin sont identiques, mais la syntaxe SQL de `creation_statements` diffère légèrement — MariaDB ne supporte pas toute la syntaxe de privilèges MySQL 8. Consultez la [documentation du plugin MariaDB Vault](https://developer.hashicorp.com/vault/docs/secrets/databases/mysql-maria) pour le format correct de `creation_statements`.

### Oracle

Oracle nécessite le `oracle-database-plugin` (non inclus dans Vault OSS). Créez un compte de niveau DBA pour Vault, puis configurez la connexion. Consultez la [documentation du plugin Oracle Vault](https://developer.hashicorp.com/vault/docs/secrets/databases/oracle).

## Suivant

[Politiques et rôles Vault](vault-policies.md)
