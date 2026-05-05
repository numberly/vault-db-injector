# Contribuer

**Audience:** Contributeur

Ce guide explique comment obtenir un environnement de développement fonctionnel et
comment valider le mode NRI en local sur un cluster k3d.

Pour les informations générales sur le projet, consultez le [site de documentation](https://numberly.github.io/vault-db-injector).

## Démarrage rapide

```bash
git clone https://github.com/numberly/vault-db-injector.git
cd vault-db-injector
go build ./...
go test ./...
```

Les tests unitaires standards ne nécessitent aucune dépendance externe et s'exécutent sur toutes les plateformes.

## Code de conduite

Ce projet suit le [Contributor Covenant](https://www.contributor-covenant.org/).
Voir `CODE_OF_CONDUCT.md` à la racine du dépôt.

## Standards de code

- `gofmt` — tout le code doit être formatté avec `gofmt` avant de committer
- `golangci-lint run` — la configuration du linter CI se trouve dans `.golangci.yml`
- Encapsulation d'erreurs — utilisez `github.com/cockroachdb/errors` pour le wrapping, pas `fmt.Errorf`
- Les messages de commit suivent [Conventional Commits](https://www.conventionalcommits.org/) :
  `feat:`, `fix:`, `chore:`, `docs:`, `perf:`

## Checklist de pull request

- [ ] `go test ./...` passe
- [ ] `go vet ./...` et `golangci-lint run` ne produisent aucune erreur
- [ ] Les nouveaux packages incluent des tests unitaires
- [ ] Si la PR modifie le comportement du webhook, ajouter un cas de test dans
      `pkg/k8smutator` pour `cfg.NRI.Enabled=false` et `cfg.NRI.Enabled=true`
- [ ] Les messages de commit suivent Conventional Commits

## Artefacts de planification interne

Le répertoire `.planning/` à la racine du dépôt contient des specs de design
et des plans d'exécution pour les travaux en cours. Avant de soumettre une
modification majeure (nouveau mode, nouvelle métrique, changement architectural),
lisez la spec correspondante — elle explique souvent les contraintes qui ont
façonné le design actuel.

## Processus de revue de code

Toutes les PRs nécessitent au moins une approbation d'un mainteneur. La liste
des mainteneurs se trouve dans `CODEOWNERS`. Comptez une semaine de délai de
retour pour la plupart des modifications ; les PRs architecturales plus larges
peuvent prendre plus de temps.
