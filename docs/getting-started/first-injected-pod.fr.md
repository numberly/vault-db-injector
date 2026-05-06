# Premier pod injecté

**Audience:** Développeur d'application

## Annoter votre pod

Appliquez ce manifeste. Il crée un ServiceAccount et un Pod dans le namespace `team-myapp` en utilisant le rôle Vault `myapp-prod` configuré dans [Politiques et rôles Vault](vault-policies.md).

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

Le label `vault-db-injector: "true"` est ce qui déclenche l'admission. Le webhook détecte le label, place des placeholders opaques dans les variables d'environnement `DB_USER` et `DB_PASS`, et le plugin NRI sur le nœud substitue les vrais identifiants avant le démarrage du conteneur.

Appliquez-le :

```bash
kubectl apply -f myapp.yaml
```

Si votre application lit une chaîne de connexion unique depuis une seule variable d'environnement, utilisez le mode URI à la place. Remplacez les annotations par :

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
    db-creds-injector.numberly.io/myapp.template: postgresql://@db.team-myapp.svc:5432/myapp?sslmode=require
    db-creds-injector.numberly.io/myapp.env-key-uri: DATABASE_URL
  labels:
    vault-db-injector: "true"
spec:
  serviceAccountName: myapp
  containers:
    - name: app
      image: postgres:16
      command: ["sleep", "infinity"]
```

## Vérifier que les identifiants fonctionnent

```bash
kubectl -n team-myapp exec myapp -- bash -c 'env | grep DB_'
```

La sortie attendue affiche le nom d'utilisateur et le mot de passe réels, pas un placeholder :
```
DB_USER=vault-myapp-prod-1746345600-x8k2j9ab
DB_PASS=A1B2-c3d4-E5F6-g7h8
```

En mode URI, la variable d'environnement est `DATABASE_URL` au lieu de `DB_USER`/`DB_PASS`.

Testez la connexion à la base de données :

```bash
kubectl -n team-myapp exec myapp -- bash -c \
  'PGPASSWORD=$DB_PASS psql -h db -U $DB_USER -d myapp -c "SELECT 1"'
```

Résultat attendu : `SELECT 1` retourne une ligne. Si la connexion échoue, vérifiez que le nom d'hôte `db` est résolu depuis l'intérieur du pod et que le serveur PostgreSQL autorise les connexions depuis l'IP du pod.

## Ce qui vient de se passer

- Le webhook a admis le pod et a remplacé `DB_USER` et `DB_PASS` par des placeholders hexadécimaux de 64 caractères. Le PodSpec stocké dans etcd ne contient que les placeholders.
- Le plugin NRI sur le nœud a intercepté `CreateContainer`, lu les annotations du pod, et utilisé le ServiceAccount du pod pour se connecter à Vault en tant que `myapp` dans `team-myapp`.
- Vault a authentifié le JWT TokenRequest, vérifié le ServiceAccount contre `auth/kubernetes/role/myapp-prod`, et émis un identifiant de base de données éphémère.
- Le plugin a substitué les placeholders par le nom d'utilisateur et le mot de passe réels dans l'environnement du conteneur avant l'exécution de `runc`.
- Le renewer détient maintenant le token du pod et le renouvellera périodiquement. À la mort du pod, le revoker révoque le token et l'identifiant DB.

Pour un schéma du flux de données complet, consultez [operators/architecture](../operators/architecture.md).

## Étapes suivantes

- Lisez la [référence des annotations](../developers/annotations.md) pour découvrir le mode URI et l'injection multi-base de données.
- Lisez [monitoring](../operators/monitoring.md) pour connecter les tableaux de bord Prometheus et les alertes.
- Lisez [security](../operators/security.md) pour renforcer le DaemonSet NRI.
