# Exemples

**Audience:** Développeur d'application

Tous les exemples supposent :

- Namespace : `team-myapp`
- ServiceAccount : `myapp` (doit exister et être lié au rôle Vault)
- Rôles de base de données Vault : `myapp-prod`, `myapp-analytics`
- Serveur PostgreSQL : `db.team-myapp.svc`

## Exemple 1 — Mode classic, base de données unique

Deux variables d'environnement séparées : une pour le nom d'utilisateur, une pour le mot de passe.

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

L'application lit `DB_USER` et `DB_PASS` depuis son environnement. Les deux variables
sont renseignées avant le démarrage du processus conteneur.

## Exemple 2 — Mode URI, base de données unique

Une variable d'environnement contenant un URI de connexion rendu. Utile lorsque
l'application attend un DSN ou une chaîne de connexion plutôt que des champs
hôte/utilisateur/mot de passe séparés.

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

Le webhook remplace `{{user}}` et `{{password}}` dans le modèle par les identifiants émis.
L'application lit `DATABASE_URL` qui contient l'URI rendu complet — par exemple :
`postgresql://vault-myapp-prod-1746345600-x8k2j9ab:A1B2-c3d4@db.team-myapp.svc:5432/myapp?sslmode=require`.

## Exemple 3 — Multi-base de données, modes mixtes

Deux bases de données injectées dans le même pod : une en mode `classic`, une
en mode `uri`. Chaque groupe `<dbname>` est indépendant.

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
    # Base de données principale — mode classic
    db-creds-injector.numberly.io/primary.role: myapp-prod
    db-creds-injector.numberly.io/primary.mode: classic
    db-creds-injector.numberly.io/primary.env-key-dbuser: PG_USER
    db-creds-injector.numberly.io/primary.env-key-dbpassword: PG_PASS
    # Base de données analytique — mode URI
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

L'application voit quatre variables d'environnement à l'exécution :

- `PG_USER` — nom d'utilisateur pour la base de données principale
- `PG_PASS` — mot de passe pour la base de données principale
- `ANALYTICS_URL` — URI de connexion rendu pour la base de données analytique

Chaque jeu d'identifiants provient d'un bail Vault séparé. Ils sont renouvelés
et révoqués indépendamment.

Voir [annotations](annotations.md) pour la référence complète des annotations
et [injection-modes](injection-modes.md) pour comprendre ce à quoi ressemble
l'environnement du pod à l'exécution selon le mode de livraison.
