# Contributing

**Audience:** Contributor

This guide covers how to get a working development environment and how to
validate NRI mode locally on a k3d cluster.

For general project information, see the [documentation site](https://numberly.github.io/vault-db-injector).

## Getting started

```bash
git clone https://github.com/numberly/vault-db-injector.git
cd vault-db-injector
go build ./...
go test ./...
```

Standard unit tests require no external dependencies and run on any platform.

## Code of conduct

This project follows the [Contributor Covenant](https://www.contributor-covenant.org/).
See `CODE_OF_CONDUCT.md` at the repository root.

## Coding standards

- `gofmt` — all code must be formatted with `gofmt` before committing
- `golangci-lint run` — the CI linter config lives at `.golangci.yml`
- Error wrapping — use `github.com/cockroachdb/errors` for wrapping, not `fmt.Errorf`
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):
  `feat:`, `fix:`, `chore:`, `docs:`, `perf:`

## Pull request checklist

- [ ] `go test ./...` passes
- [ ] `go vet ./...` and `golangci-lint run` produce no errors
- [ ] New packages include unit tests
- [ ] If the PR changes webhook behavior, add a test case to
      `pkg/k8smutator` for both `cfg.NRI.Enabled=false` and
      `cfg.NRI.Enabled=true`
- [ ] Commit messages follow Conventional Commits

## Internal planning artifacts

The `.planning/` directory at the repository root contains design specs
and execution plans for in-flight work. Before submitting a major change
(new mode, new metric, architectural shift), read the relevant spec there
first — it often explains the constraints that shaped the current design.

## Code review process

All PRs require at least one approval from a maintainer. The maintainer
list is in `CODEOWNERS`. Expect feedback within a week for most changes;
larger architectural PRs may take longer.
