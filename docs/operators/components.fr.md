# Composants

**Audience:** Opérateur de plateforme

vault-db-injector s'exécute sous la forme de quatre composants coopérants. Ils
partagent le même binaire Go et sélectionnent leur rôle au démarrage via la
clé de configuration `mode`.

## Injector (webhook)

**Fichier :** `pkg/injector/injector.go`

L'injector s'exécute en tant que Deployment et expose un Mutating Admission
Webhook Kubernetes en TLS. Lorsqu'un pod portant le label
`vault-db-injector: "true"` est admis, le webhook lit les annotations
`db-creds-injector.numberly.io/*` et détermine ce qu'il faut injecter dans
les variables d'environnement du pod.

En **mode legacy**, le webhook appelle `CanIGetRoles` contre Vault pour vérifier
que le ServiceAccount du pod est associé au rôle DB demandé, émet un token
orphelin Vault portant la politique du rôle, récupère les identifiants
dynamiques, et les écrit en clair dans les variables d'environnement du spec
du conteneur.

En **mode NRI** (auth projected), le webhook ne récupère pas les identifiants.
Il écrit des placeholders opaques `__VDBI_PH_<64hex>___` dans les variables
d'environnement — une paire par dbConfig — et inscrit un UUID par dbConfig dans
l'annotation `db-creds-injector.numberly.io/uuid` pour corrélation ultérieure.
`CanIGetRoles` est ignoré car Vault atteste l'identité du pod nativement au
moment du pod-token.

## Plugin NRI (DaemonSet)

**Fichiers :** `pkg/nri/...`

Le plugin NRI s'exécute en tant que DaemonSet sur chaque nœud, montant
`/var/run/nri/nri.sock` depuis l'hôte. Il s'enregistre comme plugin NRI auprès
de containerd ou CRI-O. À chaque événement `CreateContainer`, il filtre par
label de pod, analyse les variables d'environnement à la recherche de
placeholders, et en cas de correspondance récupère l'identité du pod depuis
le kube-apiserver, se connecte à Vault en tant que ce pod (mode projected) ou
en son propre nom (mode legacy), émet les identifiants, et envoie un
`ContainerAdjustment` pour que runc démarre le conteneur avec les vraies
variables d'environnement.

Un cache tmpfs par nœud à `/run/<release-fullname>/nri/cache.json` conserve
les identifiants déchiffrés entre les redémarrages du plugin, de sorte qu'un
CrashLoop ne nécessite pas de réémettre les identifiants à chaque tentative.
Le cache est effacé au redémarrage du nœud.

## Renewer (Deployment)

**Fichier :** `pkg/renewer/renewer.go`

Le renewer s'exécute en tant que Deployment avec élection de leader. Toutes les
5 minutes (configurable via `SyncTTLSecond`), le leader parcourt le mont de
bookkeeping KV, appelle `auth/token/renew` sur chaque token stocké et
`sys/leases/renew` sur chaque bail stocké. En mode projected-SA, le renewer
détient une politique Vault minimale : renew uniquement, sans revoke ni
suppression KV. La révocation est gérée exclusivement par le revoker.

## Revoker (Deployment)

**Fichier :** `pkg/revoker/revoker.go`

Le revoker s'exécute en tant que Deployment avec élection de leader. Le leader
surveille l'API Kubernetes pour les événements `DELETE` de pods filtrés par le
label `vault-db-injector: "true"`. À la suppression, il révoque le token et le
bail du pod, puis efface l'entrée KV. Un balayage périodique de filet de
sécurité toutes les 5 minutes (`safetyNetSync`) rattrape les pods décédés
pendant que le watch était déconnecté ou que le revoker était arrêté.

En mode projected-SA, le revoker est **l'unique** responsable de la révocation :
le renewer ne touche plus `auth/token/revoke-orphan` ni la `delete` KV.

## Élection de leader

Le renewer et le revoker s'exécutent en mode multi-réplica pour la haute
disponibilité. Seul le leader élu effectue le travail ; les autres attendent.
Le webhook (injector) est sans état — toutes les réplicas traitent les appels
d'admission en parallèle sans coordination. Voir [operations](operations.md)
pour les mécanismes et la métrique `vdbi_is_leader`.
