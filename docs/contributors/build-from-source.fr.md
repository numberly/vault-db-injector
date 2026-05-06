# Construire depuis les sources

**Audience:** Contributeur

## Prérequis

| Outil | Version minimale | Notes |
|---|---|---|
| Go | 1.22 | Workspace géré par modules (`go.mod` à la racine) |
| `make` | quelconque | GNU Make ; BSD Make non testé |
| Docker | 24+ | Requis uniquement pour les builds d'images |
| `kind` ou `k3d` | récent | Requis uniquement pour les tests d'intégration |
| `kubectl` | correspond au minor du cluster | Pour la vérification des tests d'intégration |

## Cloner et construire

```bash
git clone https://github.com/numberly/vault-db-injector.git
cd vault-db-injector
make setup
make
```

`make setup` installe les dépendances d'outils Go (`golangci-lint`, générateurs de code).
`make` compile les trois binaires et les place dans `./bin/`.

## Exécuter les tests

Les tests unitaires ne nécessitent aucune dépendance externe :

```bash
make test
```

Les tests d'intégration nécessitent un cluster compatible NRI et une instance Vault en cours d'exécution :

```bash
make integration
```

## Construire l'image de conteneur

```bash
docker build -t vault-db-injector:dev .
```

Le Dockerfile utilise un build multi-étapes. L'image finale est basée sur scratch et
ne contient que le binaire compilé.

## Tester le mode NRI en local

Le mode NRI nécessite containerd ≥ 1.7 avec NRI activé, ou CRI-O ≥ 1.26. Le
dépôt fournit un helper pour k3d :

```bash
K3D_FIX_DNS=0 k3d cluster create vault-db-test \
  --servers 1 --agents 1 \
  --image rancher/k3s:v1.34.1-k3s1 \
  --volume "$PWD/scripts/k3d-containerd-config.toml.tmpl:/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl@all"

# Vérifier que le socket NRI est présent sur chaque nœud
docker exec k3d-vault-db-test-agent-0 ls /var/run/nri/nri.sock
```

Si vous disposez déjà d'un cluster k3d, `scripts/enable-nri-on-k3d.sh` corrige
la configuration de containerd en place et redémarre containerd sans recréer le
cluster.

Une fois le cluster prêt, installez le chart avec `nri.enabled=true` pour
exercer le chemin complet v3.0 :

```bash
helm upgrade --install vault-db-injector ./helm \
  --namespace vault-db-injector --create-namespace \
  --set vaultDbInjector.configuration.vaultAddress=http://vault.local:8200 \
  --set vaultDbInjector.configuration.useProjectedSA=true \
  --set vaultDbInjector.configuration.tokenRequestAudiences='{vault}' \
  --set nri.enabled=true \
  --set nri.pluginIndex=10
```

## Linting

```bash
go vet ./...
golangci-lint run
```

La CI exécute les deux vérifications. Les PRs introduisant des erreurs de lint ne seront pas mergées.
