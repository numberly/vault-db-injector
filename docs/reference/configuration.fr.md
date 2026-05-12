# Référence de configuration

**Audience:** Opérateur de plateforme, Contributeur

## Modes binaires

vault-db-injector fournit un binaire unique qui s'exécute dans l'un des trois modes,
sélectionné par la clé `mode` dans le fichier de configuration passé via `--config` :

| Mode | Ce qu'il fait |
|---|---|
| `injector` | Exécute le webhook d'admission mutant. Modifie les PodSpecs au moment de l'admission. |
| `renewer` | Itère périodiquement les entrées KV et renouvelle les tokens et leases avant expiration. |
| `revoker` | Surveille les événements `DELETE` des pods et révoque les tokens et leases Vault. |

Le plugin NRI est intégré dans le binaire `injector` et s'active lorsque
`nri.enabled=true` dans Helm (ce qui définit l'indicateur de configuration approprié).

Chaque binaire lit un fichier de configuration YAML. Les trois partagent le même schéma
de clés ; les clés non pertinentes pour un mode donné sont silencieusement ignorées.

## Référence complète des clés de configuration

| Clé | Type | Défaut | Utilisé par | Rôle |
|---|---|---|---|---|
| `vaultAddress` | string | — | all | URL de base de Vault ou OpenBao (ex. `https://vault.example.com:8200`) |
| `vaultAuthPath` | string | `kubernetes` | all | Chemin de mount de la méthode d'authentification Kubernetes sur Vault |
| `kubeRole` | string | — | all | Rôle auth/kubernetes Vault par défaut pour la connexion du binaire |
| `kubeRoleNri` | string | repli sur `kubeRole` | plugin NRI | Rôle Vault de substitution pour la connexion du plugin NRI à Vault |
| `kubeRoleRenewer` | string | repli sur `kubeRole` | renewer | Rôle Vault de substitution pour la connexion du renewer à Vault |
| `kubeRoleRevoker` | string | repli sur `kubeRole` | revoker | Rôle Vault de substitution pour la connexion du revoker à Vault |
| `tokenTTL` | duration | `8766h` | injector | TTL du token périodique demandé à la connexion |
| `vaultSecretName` | string | `vault-db-injector` | all | Nom du mount KV-v2 utilisé pour les métadonnées par pod |
| `vaultSecretPrefix` | string | `kubernetes` | all | Préfixe de chemin à l'intérieur du mount KV |
| `useProjectedSA` | bool | `false` | injector, NRI | Lorsque `true`, émet un Kubernetes TokenRequest par pod admis et l'utilise pour se connecter à Vault au nom du pod |
| `tokenRequestAudiences` | []string | `[]` | injector, NRI | Audiences définies sur le JWT TokenRequest. Doit être non vide lorsque `useProjectedSA: true` |
| `tokenRequestExpirationSeconds` | int | `600` | injector, NRI | Durée de vie demandée du JWT TokenRequest en secondes (plancher kube-apiserver : 600s) |
| `injectorLabel` | string | `vault-db-injector` | injector, revoker | Valeur du label pod utilisée pour sélectionner les pods injectés |
| `webhookMatchLabels` | string | `vault-db-injector` | injector | Valeur du label `objectSelector` sur la MutatingWebhookConfiguration |
| `mode` | string | — | all | `injector`, `renewer` ou `revoker` |
| `sentry` | bool | `false` | all | Activer le rapport d'erreurs Sentry |
| `sentryDsn` | string | — | all | DSN Sentry |
| `logLevel` | string | `info` | all | Niveau de log (`debug`, `info`, `warn`, `error`) — transmis à logrus |
| `SyncTTLSecond` | int | `300` | renewer | Intervalle en secondes entre les balayages de synchronisation du renewer |
| `defaultEngine` | string | `databases` | injector | Nom du mount du moteur de secrets de base de données Vault par défaut |
| `certFile` | string | `/tls/tls.crt` | injector | Certificat TLS pour le serveur HTTPS du webhook |
| `keyFile` | string | `/tls/tls.key` | injector | Clé privée TLS pour le serveur HTTPS du webhook |

### Clés du plugin NRI

Le DaemonSet NRI lit sa configuration depuis le même schéma YAML, sous la clé racine `nri:`.

| Clé | Type | Défaut | Rôle |
|---|---|---|---|
| `nri.enabled` | bool | `false` | Active le chemin de code du plugin NRI. Défini par Helm. |
| `nri.socketPath` | string | `/var/run/nri/nri.sock` | Socket UNIX que le plugin utilise pour s'enregistrer auprès de containerd. Doit correspondre au socket NRI du nœud. |
| `nri.cachePath` | string | `/run/vault-db-injector/nri/cache.json` | Cache JSON sur disque des credentials déballés. HostPath tmpfs — survit aux redémarrages du pod DS, vidé au reboot du nœud. |
| `nri.pluginName` | string | `vault-db-injector` (fullname Helm) | Nom du plugin NRI à l'enregistrement. Doit être unique par instance containerd — plusieurs releases (prod + dev) sur le même cluster nécessitent des valeurs distinctes. |
| `nri.pluginIndex` | string | `"10"` | Priorité du plugin NRI (`stub.WithPluginIdx`). Doit également être unique par instance containerd quand plusieurs plugins coexistent (ex. `"10"` prod, `"11"` dev). |
| `nri.podLabel` | string | `vault-db-injector` | Clé de label pod sur laquelle le plugin filtre. Les pods sans ce label (ou avec une valeur `!= "true"`) sont ignorés. Avec plusieurs releases, à régler sur le label spécifique utilisé dans l'`objectSelector` du webhook correspondant. Vide désactive le filtre. |
| `nri.fetchTimeout` | duration | `1500ms` | Timeout du fetch de credentials Vault par évènement `CreateContainer`. **DOIT être strictement inférieur au `plugin_request_timeout` de containerd** (défaut containerd : `2s`). Voir [Tuning NRI](#tuning-nri) ci-dessous. |

## Exemple : configuration de l'injector

```yaml
certFile: /tls/tls.crt
keyFile: /tls/tls.key
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector
tokenTTL: 8766h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: injector
useProjectedSA: true
tokenRequestAudiences:
  - vault
tokenRequestExpirationSeconds: 600
injectorLabel: vault-db-injector
webhookMatchLabels: vault-db-injector
logLevel: info
sentry: false
```

## Exemple : configuration du renewer

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector-renewer
tokenTTL: 8766h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: renewer
SyncTTLSecond: 300
logLevel: info
sentry: false
```

## Exemple : configuration du revoker

```yaml
vaultAddress: https://vault.example.com:8200
vaultAuthPath: kubernetes
kubeRole: vault-db-injector-revoker
tokenTTL: 8766h
vaultSecretName: vault-db-injector
vaultSecretPrefix: kubernetes
mode: revoker
injectorLabel: vault-db-injector
logLevel: info
sentry: false
```

!!! warning
    Lorsque `useProjectedSA: true`, `tokenRequestAudiences` doit être non vide.
    Le binaire refuse de démarrer et consigne une erreur fatale si cette contrainte
    est violée. Définissez au minimum `["vault"]` et configurez une `audience`
    correspondante sur chaque rôle `auth/kubernetes` de Vault.

## Tuning NRI

Le plugin NRI tourne en DaemonSet et intercepte chaque évènement
`CreateContainer` sur son nœud. Pour chaque pod labellisé contenant des
placeholders en env, il récupère synchroniquement les credentials depuis
Vault et renvoie l'env substitué à containerd. Deux timeouts interagissent :

| Couche | Paramètre | Défaut | Comportement au timeout |
|---|---|---|---|
| containerd | `plugin_request_timeout` (dans `/etc/containerd/config.toml`) | `2s` | **Fail-open** : containerd abandonne l'appel NRI et démarre le container avec l'env non modifié (les placeholders fuitent). |
| vault-db-injector | `nri.fetchTimeout` | `1500ms` | **Fail-closed** : le plugin renvoie une erreur avant que le timeout containerd ne déclenche. Containerd propage l'erreur à kubelet, le pod passe en `CreateContainerError`, kubelet retente avec backoff. |

L'invariant à respecter : `nri.fetchTimeout < plugin_request_timeout` (avec
quelques centaines de ms de marge pour que containerd ait le temps de
propager notre erreur). Sinon containerd timeout en premier et fuit
silencieusement les placeholders.

### Profil par défaut (containerd vanilla)

Out of the box, containerd embarque `plugin_request_timeout = 2s`. Le
défaut `nri.fetchTimeout = 1500ms` est dimensionné pour ce réglage et
fonctionne sans configuration côté nœud. Compromis : tout fetch Vault
plus lent que 1.5s (par exemple pendant un burst qui sature
`auth/kubernetes/login` côté Vault) tombe en fail-closed. Kubelet retente
avec backoff (10s → 20s → 40s → … → plafond 5min).

### Profil haute charge (bursts Vault attendus)

Quand ton workload schedule beaucoup de pods labellisés en simultané (runs
de DAG Airflow, cronjobs en début d'heure, événements de scale-out), le
`auth/kubernetes/login` Vault peut grimper à plusieurs secondes. Pour
absorber le burst sans `CreateContainerError`, monte les deux timeouts en
parallèle :

**Sur chaque nœud, dans `/etc/containerd/config.toml` :**

```toml
[plugins."io.containerd.nri.v1.nri"]
  disable = false
  disable_connections = false
  plugin_registration_timeout = "15s"
  plugin_request_timeout = "30s"   # vs défaut 2s
  socket_path = "/var/run/nri/nri.sock"
```

Puis `systemctl reload containerd` (ou `restart` si `reload` n'est pas
supporté sur ta distribution).

**Dans les values Helm :**

```yaml
nri:
  fetchTimeout: "25s"   # < plugin_request_timeout containerd (30s), marge 5s
```

Compromis : quand Vault est réellement indisponible, les pods vont
attendre jusqu'à 25s par tentative avant que kubelet retente. Acceptable
dans la plupart des cas — l'alternative (pod qui fuit le placeholder et
crashe l'application) est pire.

### Diagnostiquer les évènements fail-closed

Chaque chemin fail-closed incrémente `vdbi_nri_unwrap_failures_total{reason=...}`
et produit un évènement Kubernetes Warning sur le pod avec un préfixe
`vault-db-injector:`. Les raisons :

| Label `reason` | Cause |
|---|---|
| `fetch_error` | Le fetch Vault a retourné une erreur ou a timeout (le plus courant — augmenter `fetchTimeout` si corrélé à des bursts Vault). |
| `empty_mapping` | Le pod a des placeholders en env mais aucune annotation `db-creds-injector.numberly.io/*.env-key-*` ne match un nom de variable d'env du container. Erreur de configuration utilisateur. |
| `no_change` | Le mapping a été résolu, mais `Substitute()` a produit un env identique. Indique qu'une annotation env-key référence une clé qui n'existe pas sur ce container précis. |
| `residual_placeholder` | Un token `__VDBI_PH_…___` est resté dans l'env après substitution (ex. seul le password a été résolu, le placeholder username a fuité). Indique un bug de mapping partiel. |

Requêtes utiles :

```promql
# Taux fail-closed, par raison
sum by (reason) (rate(vdbi_nri_unwrap_failures_total[5m]))

# Substitutions réussies vs échecs
sum(rate(vdbi_nri_substitutions_total[5m]))
  / (sum(rate(vdbi_nri_substitutions_total[5m]))
     + sum(rate(vdbi_nri_unwrap_failures_total[5m])))
```

La latence par étape est aussi loggée au niveau `info` sous le tag
`[timing]`, visible via `kubectl logs -l app=vault-db-injector-nri`. Le
total `fetchAndBuildMapping TOTAL` comparé à `nri.fetchTimeout` te dit à
quel point tu es proche du fail-close sous charge.
