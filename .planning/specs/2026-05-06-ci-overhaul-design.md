# CI/CD overhaul — design

**Status:** Draft
**Author:** Guillaume LEGRAIN (with Claude)
**Date:** 2026-05-06
**Reference:** [numberly/terraform-provider-mica](https://github.com/numberly/terraform-provider-mica) CI

## 1. Goal

Bring the `vault-db-injector` CI/CD up to OSS-grade parity with
`terraform-provider-mica`, plus the extensions that fit a Helm-deployed
Go binary: dependency automation, Go vulnerability scanning, image
signing, container vulnerability scanning, and Helm chart publishing
(both Helm Pages and OCI).

Success criteria:

- Every PR is gated by lint, test+coverage+race, govulncheck, helm lint,
  helm-docs sync, and a strict mkdocs build.
- Every release (tag `v*`) ships a multi-arch image to ghcr.io, signed
  with Cosign keyless, scanned by Trivy, with a Helm chart published to
  both `gh-pages` and `oci://ghcr.io/numberly/charts`.
- Versions are bumped, tags created, and changelogs maintained
  automatically by `release-please` from conventional commits.
- The image and chart version match 1:1 with the Go binary version
  (single source of truth).

## 2. Out of scope

- Standalone binary releases (the binary requires Kubernetes + Vault to
  do anything useful).
- GoReleaser (its only role here would have been changelog generation,
  which `release-please` covers).
- License header per-file checks (Numberly OSS convention is one
  `LICENSE` file at repo root, no per-file headers).
- DCO/CLA bot (no Numberly OSS project enforces this today).
- SLSA provenance level 3 (overkill for v1; Cosign keyless attestation
  + Trivy scan covers the common attack surface).

## 3. Decisions taken in brainstorming

| Decision | Choice | Rationale |
|---|---|---|
| Scope tier | B — mica parity + OSS extensions | Matches Numberly's "production-OSS" bar |
| Artifacts published | Docker image + Helm chart (gh-pages) + Helm chart (OCI) | Helm-first project; standalone binary has no use case |
| Registry strategy | All on `ghcr.io` (image + chart OCI) | Single-registry coherence; native `GITHUB_TOKEN` auth; better provenance than Docker Hub. Docker Hub deprecated as of v3.0.0 |
| Release notes / version bumps | `release-please` bot | Project already uses conventional commits; the auto-PR pattern forces version + changelog discipline in one reviewable artifact |
| Chart vs app version | 1:1 (chart `version` = chart `appVersion` = image tag) | Mono-artifact project; no chart-only release stream needed |
| Coverage badge | Gist + `dynamic-badges-action` | Matches mica; zero third-party supply-chain dependency; FinOps-friendly (no recurring service cost) |
| Image vulnerability gate | Trivy blocking on HIGH/CRITICAL + `.trivyignore` | The injector is security-critical (Vault + DB credentials path); auditable exception file gives SREs a documented bypass without lowering the gate |

Decisions deferred to implementation (no user input requested):

- **No GoReleaser**: with no binaries and `release-please` handling
  changelogs, GoReleaser would add complexity for zero value.
- **`-race` on tests**: mica does not have it; we add it because the
  injector has concurrent code paths in the renewer and revoker.
- **No coverage threshold gate** for v1: badge gives visibility, we add
  a threshold (or "no regression vs main" rule) once we have a
  reference baseline.
- **Trivy filesystem scan in CI**: skipped — redundant with
  `govulncheck` for Go vulns, the OS-layer scan only makes sense
  against the built image (release time).

## 4. Architecture

### 4.1 Workflow inventory

| Workflow | Trigger | Role |
|---|---|---|
| `ci.yml` | push main, PR main, `workflow_call` | Quality gate — lint, test, coverage, govulncheck, helm lint, helm-docs sync, mkdocs strict, conventional-commit PR title |
| `release-please.yml` | push main | release-please bot — opens/maintains a "Release vX.Y.Z" PR; merging the PR creates the tag |
| `release.yml` | tag `v*` | Release pipeline — calls `ci.yml` as gate, builds and signs the image, scans it, publishes the chart on gh-pages and OCI, attaches SBOM |
| `deploy-docs.yml` | push main (existing, modified) | mkdocs build + push to gh-pages — preserves chart-releaser artifacts (`index.yaml`, `*.tgz`) |

Removed (folded into the above):

- `test.yml` → into `ci.yml`
- `build_and_push.yml` → into `release.yml`
- `helm-docs.yml` → into `ci.yml`

### 4.2 `ci.yml` jobs (parallel)

| Job | Action | Notes |
|---|---|---|
| `lint` | `golangci-lint-action@v7` | Reads `.golangci.yml` (Numberly profile: govet, errcheck, ineffassign, staticcheck, gosec, revive) |
| `test` | `go test -race -coverpkg=./... -coverprofile=coverage.out ./...` | `-race` enabled |
| `coverage-badge` | `schneegans/dynamic-badges-action@v1` | Conditional on `github.ref == 'refs/heads/main'`; depends on `test`; pushes to gist via `GIST_TOKEN` |
| `govulncheck` | `golang/govulncheck-action@v1` | Go modules + stdlib CVEs; HIGH+ fails the build |
| `helm-lint` | `helm/chart-testing-action@v2` (`ct lint`) + `helm template` | Catches schema violations, missing values, broken templates |
| `helm-docs-check` | `make helm-docs-check` | Validates `helm/README.md` is in sync with `helm/values.yml` |
| `docs-build-strict` | `mkdocs build --strict` | Catches broken links, missing pages |
| `pr-title` | `amannn/action-semantic-pull-request@v5` | Conventional-commits format on PR titles; required because `release-please` parses commits |

All Go jobs share the same setup:

```yaml
- uses: actions/setup-go@v5
  with:
    go-version-file: go.mod
    cache: true
```

### 4.3 `release-please.yml`

Single job, runs on push to main:

```yaml
- uses: googleapis/release-please-action@v4
  with:
    token: ${{ secrets.GITHUB_TOKEN }}
    config-file: release-please-config.json
    manifest-file: .release-please-manifest.json
```

`release-please-config.json` — single component, version bumps applied
to:

- `helm/Chart.yml` field `version` (chart version)
- `helm/Chart.yml` field `appVersion` (image tag reference)
- `helm/values.yml` field `image.tag` (under each component:
  injector / renewer / revoker)
- `CHANGELOG.md` (kept in sync with conventional commits)

`.release-please-manifest.json` — initialised at `3.0.0` when this
branch merges (matches the documented v2-to-v3 migration page).

### 4.4 `release.yml` pipeline (sequential unless marked parallel)

```text
1. ci-gate          → workflow_call ci.yml
2. build-image      → docker/build-push-action multi-arch
                      target: ghcr.io/numberly/vault-db-injector:vX.Y.Z-unsigned
3. cosign-sign      → cosign sign --yes <digest> (keyless, OIDC)
4. trivy-scan       → aquasecurity/trivy-action HIGH,CRITICAL with .trivyignore
5. retag-stable     → docker buildx imagetools create
                      :vX.Y.Z-unsigned → :vX.Y.Z and :latest (if not prerelease)
6. [parallel]       ├── chart-releaser    → helm/chart-releaser-action → gh-pages
                    ├── chart-oci-push    → helm package + helm push oci://...
                    └── image-sbom        → anchore/sbom-action attaches SBOM to release
7. update-release   → augment release notes (cosign verify command, image pull example)
8. update-badges    → release version badge to gist
```

The `unsigned → signed` retag pattern (step 5) prevents a window where
consumers could pull `:vX.Y.Z` before the signature is attached. If
`cosign-sign` or `trivy-scan` fails, the stable tag is never set.

Permissions are scoped per job:

- `contents: write` only on the `update-release` job
- `packages: write` on `build-image`, `chart-oci-push`
- `id-token: write` on `cosign-sign`, `chart-oci-push` (for keyless cosign)

## 5. New configuration files

| Path | Content |
|---|---|
| `.golangci.yml` | golangci-lint profile (govet, errcheck, ineffassign, staticcheck, gosec, revive) — Numberly defaults |
| `.trivyignore` | One CVE per line, format: `CVE-XXXX-NNNN # reason — operator — YYYY-MM-DD`. Initially empty |
| `release-please-config.json` | Single-package config; bumps `helm/Chart.yml` (`version` + `appVersion`) and `helm/values.yml` image tags |
| `.release-please-manifest.json` | `{"."  :  "3.0.0"}` at branch merge |
| `.github/dependabot.yml` | Weekly cadence, group minor+patch per ecosystem, ignore major bumps for now (annual review) |

## 6. Modified configuration files

- `.github/workflows/deploy-docs.yml` — `keep_files: true` (preserves `index.yaml`, `helm-*.tgz` from chart-releaser).
- `README.md` — badges row, ghcr.io install instructions, deprecation notice for Docker Hub.
- `helm/Chart.yml` — bump `appVersion` to `3.0.0` at merge.

## 7. One-shot human setup (post-merge checklist)

1. Create a private gist on the maintainer account → record the gist ID.
2. Generate a PAT scoped `gist` only → store as repo secret `GIST_TOKEN`.
3. Verify GHCR is enabled on the `numberly` org → no action needed if
   any other ghcr image already exists from the org.
4. Configure branch protection on `main`:
   - Required status checks (all from `ci.yml`): `lint`, `test`,
     `govulncheck`, `helm-lint`, `helm-docs-check`,
     `docs-build-strict`, `pr-title`
   - No force-push, no direct push, no delete
5. After v3.0.0 ships:
   - Announce Docker Hub deprecation in release notes.
   - Remove repo secrets `DOCKER_USERNAME`, `DOCKER_PASSWORD`.
6. Smoke-test v3.0.0:
   - `cosign verify` the image with the GitHub OIDC issuer
   - `helm pull oci://ghcr.io/numberly/charts/vault-db-injector --version 3.0.0`
   - `helm repo add numberly https://numberly.github.io/vault-db-injector`
     then `helm pull numberly/vault-db-injector --version 3.0.0`

## 8. Phasing

Six phases, each one commit. Atomic per phase so a regression can be
bisected to a single change.

| Phase | Scope | Friction |
|---|---|---|
| 1 | `ci.yml` consolidated + `.golangci.yml` + `go-version-file` everywhere | Zero — pure improvement |
| 2 | Dependabot + `.trivyignore` (empty) | Zero |
| 3 | `release-please.yml` + config + manifest | Adds bot PR; harmless until merged |
| 4 | `release.yml` + first prerelease tag (`v3.0.0-rc.1`) for smoke test | Tag-driven, validates without publishing `latest` |
| 5 | Remove `test.yml`, `build_and_push.yml`, `helm-docs.yml` | Cleanup; no behavior change |
| 6 | README badges + Docker Hub deprecation note | Communication |

Each phase verified with `mkdocs build --strict` (where docs touched),
`act` or a real CI run for workflow validation, and a clean
`git status` before commit.

## 9. Known risks

- **gh-pages collision**: `chart-releaser-action` and `deploy-docs`
  both push to `gh-pages`. Mitigated by `keep_files: true` on the
  docs deploy, and chart-releaser's append-only `index.yaml` write
  mode. Test at Phase 4 with the rc tag.
- **First v3.0.0 release**: high visibility (Cosign, OCI pull, badges
  all need to work first time). Mitigation: rc tag at Phase 4 catches
  this before the real v3.0.0 tag.
- **Conventional commits enforcement**: existing commits on this
  branch already follow the convention (verified during the recent
  filter-branch). Future contributors need the `pr-title` action to
  prevent accidental "fix typo" merges that would break release-please.
- **PAT scope `gist` lifecycle**: PAT lives on the maintainer's account
  and expires per GitHub policy. Calendar reminder needed every 90
  days (or use a fine-grained PAT with no expiration if org policy
  allows).

## 10. References

- mica `ci.yml`: https://github.com/numberly/terraform-provider-mica/blob/main/.github/workflows/ci.yml
- mica `release.yml`: https://github.com/numberly/terraform-provider-mica/blob/main/.github/workflows/release.yml
- release-please: https://github.com/googleapis/release-please
- chart-releaser-action: https://github.com/helm/chart-releaser-action
- Cosign keyless: https://docs.sigstore.dev/cosign/signing/overview/
- Trivy action: https://github.com/aquasecurity/trivy-action
