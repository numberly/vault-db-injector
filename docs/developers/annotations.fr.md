# Annotations

**Audience:** Développeur d'application

Labels et annotations que vault-db-injector lit lors de l'admission de votre pod.

## Label obligatoire

Tout pod devant recevoir des identifiants injectés doit porter ce label :

```yaml
labels:
  vault-db-injector: "true"
```

Sans ce label, le webhook ignore complètement le pod — aucune
annotation n'est lue, aucun identifiant n'est émis.

## Référence des annotations

Toutes les annotations partagent le préfixe `db-creds-injector.numberly.io/`.
Les annotations ciblant une base de données spécifique utilisent `<dbname>` comme
segment choisi par l'utilisateur — choisissez un nom qui identifie la connexion
(`myapp`, `analytics`, `pg`, etc.).

| Annotation | Obligatoire ? | Défaut | Rôle |
|---|---|---|---|
| `cluster` | Optionnel | `database` | Point de montage du moteur de secrets de bases de données Vault |
| `<dbname>.role` | Oui | — | Rôle de base de données Vault utilisé pour émettre les identifiants |
| `<dbname>.mode` | Oui | `classic` | Mode d'annotation : `classic` ou `uri` |
| `<dbname>.env-key-dbuser` | Optionnel | `DBUSER` | Nom de la variable d'environnement recevant le nom d'utilisateur (mode classic) |
| `<dbname>.env-key-dbpassword` | Optionnel | `DBPASSWORD` | Nom de la variable d'environnement recevant le mot de passe (mode classic) |
| `<dbname>.env-key-uri` | Obligatoire si `mode=uri` | — | Nom de la variable d'environnement recevant l'URI de connexion |
| `<dbname>.template` | Obligatoire si `mode=uri` | — | Modèle d'URI ; `{{user}}` et `{{password}}` sont substitués par le webhook |
| `<dbname>.uuid` | Auto-renseigné | — | UUID par dbConfig écrit par le webhook. **Ne pas renseigner manuellement.** |

### `cluster`

Point de montage du moteur de secrets de bases de données Vault. Par défaut `database`.
À surcharger uniquement si votre opérateur a configuré un montage non standard :

```yaml
db-creds-injector.numberly.io/cluster: database
```

### `<dbname>.role`

Le rôle Vault sous `database/roles/<name>` que le webhook utilise pour
émettre des identifiants. Votre opérateur crée ce rôle ; confirmez le nom
avec lui.

```yaml
db-creds-injector.numberly.io/myapp.role: myapp-prod
```

### `<dbname>.mode`

Deux modes d'annotation contrôlent la façon dont les identifiants sont exposés en variables d'environnement :

- `classic` — deux variables d'environnement séparées : une pour le nom d'utilisateur, une pour le mot de passe
- `uri` — une seule variable d'environnement contenant un URI de connexion rendu

Le mode d'annotation est indépendant du mode de livraison (NRI ou
webhook). Les deux modes d'annotation fonctionnent avec les deux modes de livraison.

### `<dbname>.uuid`

Écrit par le webhook lors de l'admission. Le renewer et le revoker utilisent cet
UUID pour corréler le bail d'identifiant avec le pod. Ne pas le renseigner
manuellement — le webhook écrase toute valeur fournie.

## Injection multi-base de données

`<dbname>` est arbitraire. Ajoutez plusieurs groupes d'annotations `<dbname>.*`
pour injecter des identifiants de plusieurs bases de données dans le même pod :

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

Chaque groupe `<dbname>` génère une demande d'identifiant indépendante,
un bail séparé et un jeu de variables d'environnement distinct. Voir [examples](examples.md)
pour des manifestes Pod complets.
