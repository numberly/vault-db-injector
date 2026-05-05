# Architecture en profondeur

**Audience:** Contributeur

Cette page parcourt le code au niveau des packages. Pour la vue d'ensemble au niveau
opérateur (composants, flux de données, frontières de confiance), voir
[operators/architecture](../operators/architecture.md).

## Architecture des packages

| Package | Rôle |
|---|---|
| `pkg/injector` | Serveur webhook et logique d'admission. Modifie les PodSpecs : ajoute des variables d'env (mode classique) ou des chaînes placeholder (mode NRI). Appelle `CanIGetRoles` uniquement en mode legacy. |
| `pkg/nri` | Plugin NRI. S'enregistre auprès de containerd au démarrage. Intercepte les événements `CreateContainer` et substitue les variables d'env placeholder par les vrais identifiants récupérés depuis le cache par nœud. |
| `pkg/renewer` | Renewer périodique. Itère les entrées du mount KV et renouvelle les tokens et leases avant leur expiration. Ne révoque pas (c'est le rôle du revoker). |
| `pkg/revoker` | Revoker par observation des pods. Surveille les événements `DELETE` sur les pods portant le label de l'injector et révoque leur token Vault et leur lease. Effectue également un balayage de sécurité toutes les 5 minutes pour rattraper les pods manqués par la surveillance. |
| `pkg/vault` | Wrapper client Vault. Gère tous les appels API Vault : lectures/écritures KV, émission d'identifiants de base de données, renouvellement/révocation de tokens et leases, connexion projected-SA via JWT TokenRequest. |
| `pkg/k8s` | Initialisation du client Kubernetes, analyse des annotations, requêtes de tokens ServiceAccount. |
| `pkg/k8smutator` | Logique de mutation webhook extraite de `pkg/injector`. Contient la logique par admission et est testable unitairement indépendamment avec `cfg.NRI.Enabled` activé ou non. |
| `pkg/leadership` | Élection de leader via les objets Lease Kubernetes. Le renewer et le revoker fonctionnent en multi-réplique ; seul le leader effectue le travail actif. |
| `pkg/healthcheck` | Handlers HTTP `/healthz` et `/readyz`. |
| `pkg/metrics` | Registre Prometheus et toutes les définitions de métriques `vdbi_*`. |
| `pkg/config` | Analyse et validation du fichier de configuration (YAML → struct). Partagé par les trois binaires. |
| `pkg/placeholder` | Génération et analyse des chaînes placeholder (format `__VDBI_PH_<64hex>___`). |
| `pkg/controller` | Logique de point d'entrée des binaires de haut niveau ; assemble la configuration, les métriques, la santé et le composant spécifique au mode. |
| `pkg/logger` | Wrapper Logrus avec des conventions de champs cohérentes. |
| `pkg/sentry` | Initialisation du rapporteur d'erreurs Sentry. |

## Flux clés

### 1. Admission (webhook → injector)

```
kube-apiserver
    │  POST /mutate (AdmissionReview)
    ▼
pkg/injector (HTTPS :8443)
    │  analyse des annotations
    │  vérification du label pod : vault-db-injector=true
    │
    ├─ Mode NRI (useProjectedSA=true, nri.enabled=true)
    │      émet un TokenRequest pour le SA du pod  (pkg/k8s)
    │      connexion à Vault avec JWT               (pkg/vault)
    │      récupère les identifiants DB depuis database/creds/<role>
    │      écrit une entrée KV (uuid → token/lease IDs)
    │      remplace les valeurs d'env par des chaînes placeholder
    │      retourne le PodSpec modifié
    │
    └─ Mode legacy
           connexion avec le token SA de l'injector
           création d'un token orphan pour le pod
           récupération des identifiants DB
           écriture de l'entrée KV
           injection des identifiants en clair dans les variables d'env
           retourne le PodSpec modifié
```

### 2. Substitution NRI (locale au nœud, à la création du conteneur)

```
containerd
    │  CreateContainer(containerConfig)
    ▼
pkg/nri (plugin NRI, DaemonSet)
    │  recherche de __VDBI_PH_<hex>___ dans les variables d'env
    │
    │  cache hit ?
    │  ├─ oui → substitue le placeholder → retourne la config ajustée
    │  └─ non  → lit l'entrée KV (pkg/vault)
    │             met en cache dans /run/<release>/nri/cache.json
    │             substitue le placeholder → retourne la config ajustée
    │
    └─ en cas d'échec : retourne une erreur → containerd abandonne CreateContainer
                        (le pod reste en ContainerCreating, métrique incrémentée)
```

### 3. Révocation (revoker)

```
kube-apiserver
    │  WATCH pods (label : vault-db-injector=true)
    │
    ▼  événement DELETE
pkg/revoker
    │  lit l'entrée KV pour l'UUID du pod      (pkg/vault)
    │  révoque le lease Vault                   (pkg/vault)
    │  révoque le token orphan                  (pkg/vault)
    │  supprime l'entrée KV                     (pkg/vault)
    │  émet vdbi_revoke_token_count_success / _error
    │
    └─ balayage de sécurité (toutes les 5 min)
           liste les entrées KV
           pour chacune : vérifie que le pod tourne toujours (pkg/k8s)
           sinon → révoque + supprime
```

## Ajouter une nouvelle métrique

1. Définir la métrique dans `pkg/metrics/metrics.go` avec le préfixe `vdbi_`.
2. L'enregistrer dans `init()` dans le même fichier.
3. L'incrémenter/observer dans le package concerné.
4. L'ajouter à `docs/reference/metrics.md` dans la catégorie appropriée.
