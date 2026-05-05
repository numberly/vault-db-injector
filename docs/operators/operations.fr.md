# Opérations

**Audience:** Opérateur de plateforme

Cette page couvre les primitives opérationnelles partagées entre le renewer et
le revoker : élection de leader, vérifications de santé, et exécution de
plusieurs releases d'injector sur le même cluster.

## Élection de leader

**Fichier :** `pkg/leadership/leadership.go`

Le renewer et le revoker s'exécutent en mode multi-réplica pour la haute
disponibilité, mais une seule réplica doit effectuer le travail à la fois —
les renouvellements en double sont inutiles et les révocations en double créent
des races. Les deux Deployments utilisent des objets Lease Kubernetes via le
package standard `client-go/tools/leaderelection`.

Chaque réplica dispute un bail. Le vainqueur devient leader et exécute le
ticker périodique (renewer) ou le pod-watch (revoker). Les non-leaders
attendent jusqu'à l'expiration du bail du leader ; l'un d'eux prend alors le
relais en quelques secondes.

Le leader actif émet `vdbi_is_leader{lease_name=...} = 1` ; les réplicas en
attente émettent `0`. `vdbi_leader_election_attempts_total` et
`vdbi_leader_election_duration_seconds` donnent le taux de rotation et la durée
en horloge murale pendant laquelle le leader actuel détient le bail.

Le webhook (injector) est sans état — chaque réplica traite les appels
d'admission en parallèle sans coordination.

## Vérifications de santé

**Fichier :** `pkg/healthcheck/healthcheck.go`

Chaque binaire expose deux endpoints HTTP :

- `/healthz` — liveness. Retourne 200 tant que le processus est capable de
  répondre à HTTP. À brancher sur la sonde liveness de kubelet.
- `/readyz` — readiness. Retourne 200 une fois la connexion Vault établie et
  (renewer/revoker) la machinerie d'élection de leader initialisée. À brancher
  sur la sonde readiness de kubelet.

Les valeurs par défaut du chart configurent déjà les deux sondes sur chaque
Deployment. Si vous exposez le webhook via un Service, préférez `/readyz` pour
la gate de readiness du Service afin que le trafic d'admission n'atteigne que
les réplicas disposant d'une session Vault active.

## Plusieurs releases d'injector sur un même cluster

Deux releases Helm (p. ex. `prod` et `dev`) fonctionnant côte à côte sur le
même cluster nécessitent quelques valeurs surchargées pour éviter les collisions
sur l'enregistrement NRI de containerd et le fichier de cache par nœud :

| Valeur | Release prod | Release dev |
|---|---|---|
| `nri.pluginIndex` | `"10"` (défaut) | `"11"` |
| `vaultDbInjector.configuration.webhookMatchLabels` | `vault-db-injector` | `vault-db-injector-dev` |

Le chart génère automatiquement les identifiants par release :

- `pluginName` = le fullname de la release Helm (unique par release)
- `cachePath` = `/run/<release-fullname>/nri/cache.json` (unique par release)
- `podLabel` = la valeur de `webhookMatchLabels` (déjà spécifique à la release)

Surchargez `nri.pluginIndex` dans la release dev afin que les deux indices
coexistent sur le même containerd. La surcharge de `webhookMatchLabels` détermine
quels pods chaque release admet — un pod labelisé `vault-db-injector: "true"`
appartient à prod, un pod labelisé `vault-db-injector-dev: "true"` appartient
à dev.
