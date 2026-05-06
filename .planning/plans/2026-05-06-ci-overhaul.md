# CI/CD overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the `vault-db-injector` CI/CD up to OSS-grade parity with `terraform-provider-mica`, plus Helm-specific extensions (chart publishing on gh-pages and OCI), single-registry on `ghcr.io`, automated version bumps via `release-please`, Cosign keyless image signing, and Trivy vulnerability gating.

**Architecture:** Four GitHub Actions workflows: `ci.yml` (quality gate, also `workflow_call`), `release-please.yml` (version-bump bot), `release.yml` (tag-driven publish pipeline calling `ci.yml` as a gate), and the existing `deploy-docs.yml` (preserved with `keep_files: true`). Releases bump `helm/Chart.yml` (`version` + `appVersion`) and `helm/values.yml` image tags 1:1 with the tag.

**Tech Stack:** GitHub Actions, golangci-lint v2, govulncheck, helm-docs, chart-testing (`ct`), mkdocs-material, Trivy, Cosign (keyless via Sigstore + GitHub OIDC), release-please, chart-releaser-action, Dependabot, dynamic-badges-action, Docker Buildx (multi-arch).

---

## Phase 1 — Consolidated `ci.yml` + golangci-lint + go-version-file

Replaces `test.yml` with a 7-job quality gate. The old `test.yml` is **kept** until Phase 5 — this phase creates `ci.yml` alongside it so we can verify the new gate runs green before deleting the old one.

### Task 1.1: Add `.golangci.yml`

**Files:**
- Create: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.golangci.yml`

- [ ] **Step 1: Write the config**

```yaml
version: "2"

linters:
  default: none
  enable:
    - errcheck
    - govet
    - staticcheck
    - unused
    - ineffassign
    - gosec
    - bodyclose
    - noctx
    - exhaustive
  settings:
    govet:
      enable-all: false
    exhaustive:
      default-signifies-exhaustive: true
    gosec:
      excludes:
        - G115
  exclusions:
    presets:
      - std-error-handling
      - common-false-positives
    rules:
      - linters: [staticcheck]
        text: "SA1019"
      - linters: [gosec]
        path: "_test\\.go"
```

- [ ] **Step 2: Run golangci-lint locally to baseline**

Run:
```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.0
$(go env GOPATH)/bin/golangci-lint run ./... 2>&1 | tee /tmp/lint-baseline.txt
```

Expected: either passes clean, or reports a finite list of issues. Record the count.

- [ ] **Step 3: If lint reports issues, decide each issue's resolution**

For each issue, choose one of:
- Fix it inline (preferred for trivial cases like `errcheck` on `defer Close()`)
- Add a targeted `//nolint:lintname // reason` comment with justification
- Add an exclusion rule in `.golangci.yml` if the pattern is repo-wide and unactionable

**Do not silence findings without a recorded reason.** The point of this gate is to surface real issues; blanket suppression defeats the purpose.

Re-run lint after fixes. Loop until clean.

- [ ] **Step 4: Stage the config and any inline fixes**

Run: `git add .golangci.yml $(git diff --name-only | grep -E '\.go$' || true)`
Do not commit yet — Phase 1 is one commit per the spec.

### Task 1.2: Create `ci.yml`

**Files:**
- Create: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.github/workflows/ci.yml`

- [ ] **Step 1: Write the workflow**

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
  workflow_call:

jobs:
  lint:
    name: Lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - name: Run golangci-lint
        uses: golangci/golangci-lint-action@v7
        with:
          version: v2.1.0

  test:
    name: Tests
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - name: Run tests with race detector and coverage
        run: |
          go test -race \
            -coverpkg=./... \
            -coverprofile=coverage.out \
            -timeout 5m \
            ./...
      - name: Upload coverage artifact
        uses: actions/upload-artifact@v4
        with:
          name: coverage
          path: coverage.out
          retention-days: 7

  coverage-badge:
    name: Coverage badge
    needs: test
    if: github.ref == 'refs/heads/main' && github.event_name == 'push'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true
      - name: Download coverage
        uses: actions/download-artifact@v4
        with:
          name: coverage
      - name: Extract coverage percentage
        id: coverage
        run: |
          PCT=$(go tool cover -func=coverage.out | grep total | awk '{print $3}' | tr -d '%')
          echo "percentage=${PCT}" >> "$GITHUB_OUTPUT"
      - name: Update coverage badge gist
        uses: schneegans/dynamic-badges-action@v1.7.0
        with:
          auth: ${{ secrets.GIST_TOKEN }}
          gistID: ${{ vars.COVERAGE_GIST_ID }}
          filename: vault-db-injector-coverage.json
          label: coverage
          message: ${{ steps.coverage.outputs.percentage }}%
          valColorRange: ${{ steps.coverage.outputs.percentage }}
          minColorRange: 40
          maxColorRange: 80

  govulncheck:
    name: govulncheck
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Run govulncheck
        uses: golang/govulncheck-action@v1
        with:
          go-version-file: go.mod
          cache: true

  helm-lint:
    name: Helm lint
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: azure/setup-helm@v4
        with:
          version: v3.16.4
      - name: Set up chart-testing
        uses: helm/chart-testing-action@v2.7.0
      - name: Run chart-testing lint
        run: ct lint --charts helm --validate-maintainers=false --target-branch=${{ github.event.repository.default_branch }}
      - name: Render templates with default values
        run: helm template test-render helm/ > /dev/null

  helm-docs-check:
    name: helm-docs sync
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Install helm-docs
        run: |
          HELM_DOCS_VERSION=1.14.2
          curl -fsSL "https://github.com/norwoodj/helm-docs/releases/download/v${HELM_DOCS_VERSION}/helm-docs_${HELM_DOCS_VERSION}_Linux_x86_64.deb" -o /tmp/helm-docs.deb
          sudo dpkg -i /tmp/helm-docs.deb
      - name: Verify helm/README.md is in sync
        run: make helm-docs-check

  docs-build-strict:
    name: mkdocs strict build
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.x"
      - name: Install mkdocs and plugins
        run: pip install mkdocs mkdocs-material mkdocs-static-i18n
      - name: Build docs (strict mode)
        run: mkdocs build --strict

  pr-title:
    name: Conventional PR title
    if: github.event_name == 'pull_request'
    runs-on: ubuntu-latest
    steps:
      - uses: amannn/action-semantic-pull-request@v5
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
        with:
          types: |
            feat
            fix
            chore
            docs
            perf
            refactor
            test
            build
            ci
            style
```

- [ ] **Step 2: Validate workflow syntax**

Run:
```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
docker run --rm -v "$PWD":/app -w /app rhysd/actionlint:1.7.4 actionlint .github/workflows/ci.yml
```

Expected: no output (clean).

If actionlint isn't preferred, alternative:
```bash
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"
```
Expected: silent (valid YAML).

- [ ] **Step 3: Verify the local helm-docs-check still passes**

Run: `make helm-docs-check`
Expected: exit 0, "(file state is current)" or no diff output.

- [ ] **Step 4: Verify mkdocs strict still passes**

Run: `.venv/bin/mkdocs build --strict 2>&1 | tail -3`
Expected: `INFO    -  Documentation built in X.XXs` (no ERROR/WARNING).

### Task 1.3: Commit Phase 1

- [ ] **Step 1: Stage all phase files**

Run:
```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
git add .golangci.yml .github/workflows/ci.yml
git diff --cached --stat
```

If Task 1.1 Step 3 produced inline lint fixes, also `git add` those Go files.

- [ ] **Step 2: Commit**

```bash
git commit -m "ci: add consolidated ci.yml with lint, test+coverage, govulncheck, helm-lint, helm-docs sync, and mkdocs strict

Replaces the legacy test.yml gate with a 7-job parallel quality gate
modelled on terraform-provider-mica's ci.yml.

- golangci-lint v2 with the Numberly profile (govet, errcheck, staticcheck,
  ineffassign, unused, gosec, bodyclose, noctx, exhaustive)
- go test with -race and coverage profile uploaded as artifact
- coverage-badge job pushes to a gist on push to main only
- govulncheck for Go module CVEs
- chart-testing lint + helm template render for Helm chart validation
- helm-docs sync gate (replaces the standalone helm-docs.yml in Phase 5)
- mkdocs --strict to catch broken links across the bilingual doc site
- pr-title gate enforcing Conventional Commits on PR titles (required
  for release-please to parse commits correctly)

Every Go job uses go-version-file: go.mod with cache: true to eliminate
hardcoded Go version drift.

The legacy test.yml is left in place; it will be removed in a later
phase once we have green runs of ci.yml on main."
```

- [ ] **Step 3: Push and verify CI runs**

```bash
git push
```

Open the GitHub Actions tab, wait for `CI` to complete on the push.
Expected: all 6 jobs green on push (`pr-title` skipped on push). The `coverage-badge` job will skip until secrets are configured — that is **expected** and not a failure.

If any other job fails, fix it before proceeding to Phase 2. Do not skip green-on-main verification.

---

## Phase 2 — Dependabot + `.trivyignore`

Two static config files. No CI-runtime impact yet (Trivy isn't wired until Phase 4).

### Task 2.1: Add Dependabot config

**Files:**
- Create: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.github/dependabot.yml`

- [ ] **Step 1: Write the config**

```yaml
version: 2
updates:
  - package-ecosystem: gomod
    directory: "/"
    schedule:
      interval: weekly
      day: monday
      time: "06:00"
      timezone: "Europe/Paris"
    groups:
      go-minor-and-patch:
        update-types:
          - minor
          - patch
    ignore:
      - dependency-name: "*"
        update-types:
          - version-update:semver-major

  - package-ecosystem: github-actions
    directory: "/"
    schedule:
      interval: weekly
      day: monday
      time: "06:00"
      timezone: "Europe/Paris"
    groups:
      actions-minor-and-patch:
        update-types:
          - minor
          - patch
    ignore:
      - dependency-name: "*"
        update-types:
          - version-update:semver-major

  - package-ecosystem: docker
    directory: "/"
    schedule:
      interval: weekly
      day: monday
      time: "06:00"
      timezone: "Europe/Paris"
    ignore:
      - dependency-name: "*"
        update-types:
          - version-update:semver-major
```

- [ ] **Step 2: Validate YAML**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/dependabot.yml'))"`
Expected: silent.

### Task 2.2: Add `.trivyignore`

**Files:**
- Create: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.trivyignore`

- [ ] **Step 1: Write the file**

```
# Trivy CVE allowlist — auditable exceptions for the vault-db-injector image.
#
# Format (one per line):
#   CVE-XXXX-NNNN  # short reason — operator name — YYYY-MM-DD
#
# Add a CVE here only when:
#   1. Trivy reports it on the built image
#   2. There is no patched upstream version available
#   3. Exposure has been assessed (does the vulnerable code path execute
#      with attacker-controlled input in the injector? Usually no)
#   4. A linked issue or ADR documents the long-term mitigation
#
# Review every entry quarterly: stale exceptions are worse than no exceptions.
```

(Initially empty of CVEs — only the header. The file must exist for `release.yml` to pass it to Trivy in Phase 4.)

### Task 2.3: Commit Phase 2

- [ ] **Step 1: Stage and commit**

```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
git add .github/dependabot.yml .trivyignore
git diff --cached --stat
git commit -m "ci: enable Dependabot and seed .trivyignore for upcoming Trivy gate

Dependabot updates Go modules, GitHub Actions, and the Dockerfile base
image weekly on Monday 06:00 Europe/Paris. Minor and patch bumps are
grouped into a single PR per ecosystem to reduce review noise; major
bumps are ignored for now and will be reviewed manually on a quarterly
cadence.

.trivyignore is created empty (header only). It will be consumed by the
release.yml Trivy job added in a later phase. The file format is
documented inline so future operators know the discipline expected
when adding exceptions."
```

- [ ] **Step 2: Push**

```bash
git push
```

Verify Dependabot's first scan kicks off on the next Monday tick (or trigger manually via the Insights → Dependency graph → Dependabot tab).

---

## Phase 3 — release-please bot

Bot only. No tags fired, no images built. The bot will open a "release v3.0.0" PR; Phase 4 then handles what happens when that PR is merged.

### Task 3.1: Add `release-please-config.json`

**Files:**
- Create: `/home/gule/Workspace/team-infrastructure/vault-db-injector/release-please-config.json`

- [ ] **Step 1: Write the config**

```json
{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "release-type": "go",
  "include-component-in-tag": false,
  "include-v-in-tag": true,
  "bump-minor-pre-major": true,
  "bump-patch-for-minor-pre-major": true,
  "packages": {
    ".": {
      "release-type": "go",
      "package-name": "vault-db-injector",
      "extra-files": [
        {
          "type": "yaml",
          "path": "helm/Chart.yml",
          "jsonpath": "$.version"
        },
        {
          "type": "yaml",
          "path": "helm/Chart.yml",
          "jsonpath": "$.appVersion"
        },
        {
          "type": "yaml",
          "path": "helm/values.yml",
          "jsonpath": "$.vaultDbInjector.injector.image.tag"
        },
        {
          "type": "yaml",
          "path": "helm/values.yml",
          "jsonpath": "$.vaultDbInjector.renewer.image.tag"
        },
        {
          "type": "yaml",
          "path": "helm/values.yml",
          "jsonpath": "$.vaultDbInjector.revoker.image.tag"
        }
      ]
    }
  },
  "changelog-sections": [
    {"type": "feat", "section": "Features"},
    {"type": "fix", "section": "Bug Fixes"},
    {"type": "perf", "section": "Performance"},
    {"type": "docs", "section": "Documentation"},
    {"type": "refactor", "section": "Code refactoring"},
    {"type": "build", "section": "Build & dependencies"},
    {"type": "ci", "section": "CI"},
    {"type": "chore", "section": "Chores", "hidden": true},
    {"type": "test", "section": "Tests", "hidden": true}
  ]
}
```

- [ ] **Step 2: Validate JSON**

Run: `python3 -c "import json; json.load(open('release-please-config.json'))"`
Expected: silent.

### Task 3.2: Add `.release-please-manifest.json`

**Files:**
- Create: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.release-please-manifest.json`

- [ ] **Step 1: Write the manifest**

The manifest pins the **current** version. Because release-please bumps
**from** the manifest, we set it to `2.99.99` so the next release-please
run computes `3.0.0` from the conventional commits since the last
release tag (this branch contains breaking changes — `BREAKING CHANGE:`
footers and `feat!:` prefixes — that release-please will recognise).

```json
{
  ".": "2.99.99"
}
```

If the operator prefers a different starting point (e.g. they want the
bot to not bump anything until they manually open a PR), they can also
seed `"3.0.0"` directly and rely on the bot to bump from there.

- [ ] **Step 2: Validate JSON**

Run: `python3 -c "import json; json.load(open('.release-please-manifest.json'))"`
Expected: silent.

### Task 3.3: Add `release-please.yml` workflow

**Files:**
- Create: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.github/workflows/release-please.yml`

- [ ] **Step 1: Write the workflow**

```yaml
name: release-please

on:
  push:
    branches: [main]

permissions:
  contents: write
  pull-requests: write

jobs:
  release-please:
    name: release-please
    runs-on: ubuntu-latest
    steps:
      - uses: googleapis/release-please-action@v4
        with:
          token: ${{ secrets.GITHUB_TOKEN }}
          config-file: release-please-config.json
          manifest-file: .release-please-manifest.json
```

- [ ] **Step 2: Validate YAML**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release-please.yml'))"`
Expected: silent.

### Task 3.4: Add a `.versionrc` aware of `helm/Chart.yml` and a placeholder `appVersion`

The chart currently has no `appVersion` field. release-please's `extra-files` jsonpath replacement requires the field to **exist** before the first run, otherwise it silently no-ops on the missing key.

**Files:**
- Modify: `/home/gule/Workspace/team-infrastructure/vault-db-injector/helm/Chart.yml`

- [ ] **Step 1: Add `appVersion: 2.0.12` (current image tag) at the bottom of `Chart.yml`**

The file becomes:

```yaml
apiVersion: v1
name: vault-db-injector
version: 2.0.0
description: vault-db-injector helm chart
keywords:
- vault-db-injector
- vault
sources:
- https://github.com/numberly/vault-db-injector
maintainers:
- name: Guillaume LEGRAIN
  email: guillaume.legrain@numberly.com
engine: gotpl
appVersion: 2.0.12
```

- [ ] **Step 2: Regenerate `helm/README.md`**

Run: `make helm-docs`
Expected: `helm/README.md` is updated with the `appVersion`. `make helm-docs-check` should now pass.

Run: `make helm-docs-check`
Expected: exit 0.

### Task 3.5: Commit Phase 3

- [ ] **Step 1: Stage and commit**

```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
git add release-please-config.json .release-please-manifest.json \
        .github/workflows/release-please.yml \
        helm/Chart.yml helm/README.md
git diff --cached --stat
git commit -m "ci: enable release-please for automated version bumps and changelog

The bot watches push events on main, parses Conventional Commits, and
opens (or updates) a 'Release vX.Y.Z' PR that bumps:
- helm/Chart.yml field 'version' (chart version)
- helm/Chart.yml field 'appVersion' (image tag reference)
- helm/values.yml fields 'vaultDbInjector.{injector,renewer,revoker}.image.tag'
- CHANGELOG.md (auto-generated from conventional commits)

Merging the bot's PR creates the 'vX.Y.Z' tag, which the upcoming
release.yml pipeline (added in a later phase) will pick up to publish
artifacts.

The current chart had no 'appVersion' field — release-please needs the
key to exist before it can replace it via jsonpath, so this commit
seeds appVersion to the current image tag (2.0.12) and regenerates
helm/README.md to keep the helm-docs gate green.

Manifest is initialised at 2.99.99 so release-please's first run on
this branch recognises the breaking changes accumulated in the v3.0
work and bumps to 3.0.0."
```

- [ ] **Step 2: Push and verify the bot opens its first PR**

```bash
git push
```

Expected: within ~1 minute of push, a new PR titled "release: 3.0.0" appears, with a CHANGELOG diff and bumped chart files. Do **not** merge it yet — Phase 4 wires the release pipeline that consumes the resulting tag.

---

## Phase 4 — `release.yml` + smoke-test on `v3.0.0-rc.1`

Tag-driven publish pipeline. Tested on a release candidate before the bot's PR merges.

### Task 4.1: Add `release.yml`

**Files:**
- Create: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.github/workflows/release.yml`

- [ ] **Step 1: Write the workflow**

```yaml
name: Release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write
  packages: write
  id-token: write

jobs:
  ci-gate:
    name: CI gate
    uses: ./.github/workflows/ci.yml

  build-image:
    name: Build and push image (unsigned)
    needs: ci-gate
    runs-on: ubuntu-latest
    outputs:
      digest: ${{ steps.push.outputs.digest }}
      version: ${{ steps.version.outputs.version }}
      is-prerelease: ${{ steps.version.outputs.is-prerelease }}
    steps:
      - uses: actions/checkout@v4
      - id: version
        run: |
          VERSION="${GITHUB_REF#refs/tags/}"
          echo "version=${VERSION}" >> "$GITHUB_OUTPUT"
          if [[ "${VERSION}" == *-* ]]; then
            echo "is-prerelease=true" >> "$GITHUB_OUTPUT"
          else
            echo "is-prerelease=false" >> "$GITHUB_OUTPUT"
          fi
      - uses: docker/setup-qemu-action@v3
      - uses: docker/setup-buildx-action@v3
      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - id: push
        name: Build and push (unsigned tag)
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile
          platforms: linux/amd64,linux/arm64
          push: true
          tags: ghcr.io/numberly/vault-db-injector:${{ steps.version.outputs.version }}-unsigned
          provenance: true
          sbom: true

  cosign-sign:
    name: Cosign keyless sign
    needs: build-image
    runs-on: ubuntu-latest
    permissions:
      packages: write
      id-token: write
    steps:
      - uses: sigstore/cosign-installer@v3
      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - name: Sign image by digest
        env:
          DIGEST: ${{ needs.build-image.outputs.digest }}
        run: cosign sign --yes "ghcr.io/numberly/vault-db-injector@${DIGEST}"

  trivy-scan:
    name: Trivy image scan
    needs: build-image
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Run Trivy
        uses: aquasecurity/trivy-action@0.28.0
        with:
          image-ref: ghcr.io/numberly/vault-db-injector:${{ needs.build-image.outputs.version }}-unsigned
          severity: HIGH,CRITICAL
          exit-code: "1"
          ignore-unfixed: true
          trivyignores: ".trivyignore"

  retag-stable:
    name: Promote to stable tag
    needs: [cosign-sign, trivy-scan]
    runs-on: ubuntu-latest
    steps:
      - uses: docker/setup-buildx-action@v3
      - name: Log in to GHCR
        uses: docker/login-action@v3
        with:
          registry: ghcr.io
          username: ${{ github.actor }}
          password: ${{ secrets.GITHUB_TOKEN }}
      - name: Create stable tag from unsigned-tag manifest
        env:
          VERSION: ${{ needs.build-image.outputs.version }}
        run: |
          docker buildx imagetools create \
            -t "ghcr.io/numberly/vault-db-injector:${VERSION}" \
            "ghcr.io/numberly/vault-db-injector:${VERSION}-unsigned"
      - name: Tag :latest if not prerelease
        if: needs.build-image.outputs.is-prerelease == 'false'
        env:
          VERSION: ${{ needs.build-image.outputs.version }}
        run: |
          docker buildx imagetools create \
            -t "ghcr.io/numberly/vault-db-injector:latest" \
            "ghcr.io/numberly/vault-db-injector:${VERSION}-unsigned"

  chart-releaser:
    name: Publish chart to gh-pages
    needs: retag-stable
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - name: Configure git
        run: |
          git config user.name "$GITHUB_ACTOR"
          git config user.email "$GITHUB_ACTOR@users.noreply.github.com"
      - uses: azure/setup-helm@v4
        with:
          version: v3.16.4
      - uses: helm/chart-releaser-action@v1.7.0
        with:
          charts_dir: .
          config: |
            chart-dirs:
              - helm
        env:
          CR_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          CR_SKIP_EXISTING: "true"

  chart-oci-push:
    name: Publish chart to ghcr.io OCI
    needs: retag-stable
    runs-on: ubuntu-latest
    permissions:
      packages: write
      id-token: write
    steps:
      - uses: actions/checkout@v4
      - uses: azure/setup-helm@v4
        with:
          version: v3.16.4
      - uses: sigstore/cosign-installer@v3
      - name: Log in to GHCR
        run: echo "${{ secrets.GITHUB_TOKEN }}" | helm registry login ghcr.io -u "${{ github.actor }}" --password-stdin
      - name: Package and push chart
        id: push
        run: |
          helm package helm
          PKG=$(ls vault-db-injector-*.tgz | head -1)
          OUT=$(helm push "${PKG}" oci://ghcr.io/numberly/charts 2>&1 | tee /dev/stderr)
          DIGEST=$(echo "${OUT}" | grep -oE 'sha256:[a-f0-9]{64}' | head -1)
          echo "digest=${DIGEST}" >> "$GITHUB_OUTPUT"
      - name: Sign chart by digest
        env:
          DIGEST: ${{ steps.push.outputs.digest }}
        run: cosign sign --yes "ghcr.io/numberly/charts/vault-db-injector@${DIGEST}"

  augment-release-notes:
    name: Add verification commands to release notes
    needs: [retag-stable, chart-releaser, chart-oci-push]
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - name: Append verification commands
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          TAG: ${{ github.ref_name }}
          REPO: ${{ github.repository }}
        run: |
          BODY=$(gh release view "${TAG}" --repo "${REPO}" --json body --jq '.body')
          APPENDIX="

          ## Pull

          \`\`\`
          docker pull ghcr.io/numberly/vault-db-injector:${TAG}
          helm pull oci://ghcr.io/numberly/charts/vault-db-injector --version ${TAG#v}
          helm repo add numberly https://numberly.github.io/vault-db-injector
          helm pull numberly/vault-db-injector --version ${TAG#v}
          \`\`\`

          ## Verify the Cosign signature

          \`\`\`
          cosign verify ghcr.io/numberly/vault-db-injector:${TAG} \\\\
            --certificate-identity-regexp=https://github.com/numberly/vault-db-injector \\\\
            --certificate-oidc-issuer=https://token.actions.githubusercontent.com
          \`\`\`
          "
          gh release edit "${TAG}" --repo "${REPO}" --notes "${BODY}${APPENDIX}"
```

- [ ] **Step 2: Validate YAML**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/release.yml'))"`
Expected: silent.

### Task 4.2: Modify `deploy-docs.yml` to keep chart-releaser artifacts

**Files:**
- Modify: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.github/workflows/deploy-docs.yml`

- [ ] **Step 1: Read the current file**

```bash
cat /home/gule/Workspace/team-infrastructure/vault-db-injector/.github/workflows/deploy-docs.yml
```

- [ ] **Step 2: Replace `oprypin/push-to-gh-pages` with `peaceiris/actions-gh-pages` and pass `keep_files: true`**

The replacement workflow:

```yaml
name: Deploy docs

on:
  push:
    branches:
      - main

jobs:
  build:
    name: Deploy docs
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.x"
      - name: Install mkdocs and plugins
        run: pip install mkdocs mkdocs-material mkdocs-static-i18n
      - name: Build site
        run: mkdocs build
      - name: Deploy to gh-pages (preserving chart-releaser artifacts)
        if: github.event_name == 'push' && github.ref == 'refs/heads/main'
        uses: peaceiris/actions-gh-pages@v4
        with:
          github_token: ${{ secrets.GITHUB_TOKEN }}
          publish_dir: site
          keep_files: true
          commit_message: 'docs: deploy site'
```

`keep_files: true` is the critical flag — it stops the action from wiping `index.yaml`, `*.tgz`, and any other chart-releaser-managed file from the gh-pages branch on every docs deploy.

- [ ] **Step 3: Validate YAML**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/deploy-docs.yml'))"`
Expected: silent.

### Task 4.3: Commit Phase 4 (release pipeline + deploy-docs preservation)

- [ ] **Step 1: Stage and commit**

```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
git add .github/workflows/release.yml .github/workflows/deploy-docs.yml
git diff --cached --stat
git commit -m "ci: add release.yml pipeline and preserve chart-releaser artifacts in gh-pages

Tag-driven release pipeline that runs on every 'v*' tag:

1. ci.yml gate (workflow_call)
2. Build multi-arch image, push to ghcr.io with the ':vX.Y.Z-unsigned' tag
3. Cosign keyless sign the image digest (Sigstore + GitHub OIDC)
4. Trivy scan with .trivyignore consulted; fails on HIGH/CRITICAL
5. Promote ':vX.Y.Z-unsigned' to ':vX.Y.Z' (and ':latest' if not prerelease)
   only after both Cosign and Trivy succeed
6. In parallel: chart-releaser-action publishes the chart to gh-pages,
   helm push publishes the chart OCI to ghcr.io/numberly/charts (signed
   by Cosign), and the augment-release-notes job appends pull and
   verify commands to the GitHub release body

The 'unsigned -> signed' retag pattern prevents a window where consumers
could pull ':vX.Y.Z' before the signature is attached.

deploy-docs.yml is migrated to peaceiris/actions-gh-pages with
keep_files: true so the docs deploy no longer wipes chart-releaser's
index.yaml and chart .tgz artifacts on every push to main."
```

- [ ] **Step 2: Push**

```bash
git push
```

### Task 4.4: One-shot setup (operator action)

**This task is run by the operator (Guillaume), not the agent.** The agent should print this checklist and stop until the operator confirms each item.

- [ ] **Step 1: Print the operator checklist**

The operator must complete the following on `github.com/numberly/vault-db-injector` before the rc smoke test:

1. **Create a private gist** at https://gist.github.com (any filename, can be empty). Record the gist ID from the URL (`gist.github.com/<user>/<gistid>`).
2. **Generate a fine-grained PAT** scoped to "Gists: Read and write" only — no other scopes. Store as repo secret `GIST_TOKEN`.
3. **Add a repo variable** `COVERAGE_GIST_ID` set to the gist ID from step 1.
4. **Verify GHCR access**: `gh api user/packages?package_type=container --jq '.[].name'` should list at least one container if the org has used GHCR before. If not, the first push will create the namespace automatically.
5. **Configure branch protection on `main`**: required status checks `lint`, `test`, `govulncheck`, `helm-lint`, `helm-docs-check`, `docs-build-strict`, `pr-title`. No force-push, no direct push.

Wait for the operator to confirm before proceeding to the rc smoke test.

- [ ] **Step 2: Operator confirms**

Operator says "ready" or equivalent.

### Task 4.5: Smoke-test with `v3.0.0-rc.1`

- [ ] **Step 1: Create and push the rc tag**

```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
git tag -a v3.0.0-rc.1 -m "Release candidate 1 for v3.0.0 (CI overhaul smoke test)"
git push origin v3.0.0-rc.1
```

- [ ] **Step 2: Watch the release pipeline**

Open the GitHub Actions tab. Wait for the `Release` workflow to run on the tag.

Expected:
- `ci-gate` green
- `build-image` green, image at `ghcr.io/numberly/vault-db-injector:v3.0.0-rc.1-unsigned`
- `cosign-sign` green
- `trivy-scan` green (or fails on real CVE — if so, add to `.trivyignore` with justification, push, recreate the tag)
- `retag-stable` green; `:v3.0.0-rc.1` exists, `:latest` does **not** exist (prerelease guard)
- `chart-releaser` green; `index.yaml` updated on `gh-pages`
- `chart-oci-push` green; chart at `oci://ghcr.io/numberly/charts/vault-db-injector:3.0.0-rc.1`
- `augment-release-notes` green; release body has Pull and Verify sections

- [ ] **Step 3: Smoke-test the artifacts**

```bash
# Image pull and signature verify
docker pull ghcr.io/numberly/vault-db-injector:v3.0.0-rc.1
cosign verify ghcr.io/numberly/vault-db-injector:v3.0.0-rc.1 \
  --certificate-identity-regexp="https://github.com/numberly/vault-db-injector" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com" \
  | head -5

# Helm OCI pull
helm pull oci://ghcr.io/numberly/charts/vault-db-injector --version 3.0.0-rc.1
ls vault-db-injector-3.0.0-rc.1.tgz

# Helm gh-pages pull
helm repo add numberly-test https://numberly.github.io/vault-db-injector
helm repo update numberly-test
helm pull numberly-test/vault-db-injector --version 3.0.0-rc.1
helm repo remove numberly-test
```

Expected: all four pulls succeed, `cosign verify` prints a JSON certificate.

- [ ] **Step 4: Clean up the rc**

```bash
# Delete the local and remote tag (the GitHub release stays as record;
# the published artifacts on ghcr.io stay too — that's fine for an rc).
git tag -d v3.0.0-rc.1
git push origin :refs/tags/v3.0.0-rc.1
```

The release-please bot will eventually open the v3.0.0 PR. Merging that PR creates the real `v3.0.0` tag, which triggers the same pipeline for the real release.

---

## Phase 5 — Remove legacy workflows

`ci.yml` has been green on main for a full Phase 4 cycle. Safe to retire the old gate.

### Task 5.1: Delete obsolete workflows

**Files:**
- Delete: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.github/workflows/test.yml`
- Delete: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.github/workflows/build_and_push.yml`
- Delete: `/home/gule/Workspace/team-infrastructure/vault-db-injector/.github/workflows/helm-docs.yml`

- [ ] **Step 1: Remove the files**

```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
git rm .github/workflows/test.yml \
       .github/workflows/build_and_push.yml \
       .github/workflows/helm-docs.yml
```

- [ ] **Step 2: Verify nothing else references them**

```bash
grep -rn "test.yml\|build_and_push\|helm-docs.yml" .github/ docs/ Makefile 2>/dev/null
```

Expected: no matches except possibly in `.github/dependabot.yml` if it referenced them by name (it doesn't — Dependabot scans the directory globally).

- [ ] **Step 3: Verify branch protection rules don't pin the deleted workflow names**

The operator must check repo Settings → Branches → main → "Required status checks" and remove any rule pointing at `Go` (the old `test.yml`'s job name) or `Build and Push Docker Image`. Required checks should now reference only `lint`, `test`, `govulncheck`, `helm-lint`, `helm-docs-check`, `docs-build-strict`, `pr-title`.

### Task 5.2: Commit Phase 5

- [ ] **Step 1: Stage and commit**

```bash
git diff --cached --stat
git commit -m "ci: remove legacy workflows now folded into ci.yml and release.yml

- test.yml: replaced by ci.yml (lint + test + coverage + govulncheck +
  helm-lint + helm-docs-check + docs-build-strict + pr-title, all
  parallel)
- build_and_push.yml: replaced by release.yml (multi-arch build, Cosign
  keyless sign, Trivy gate, retag-stable, parallel chart publish)
- helm-docs.yml: folded into ci.yml as the helm-docs-check job

The repo's required status checks must be updated by an operator to
remove references to the deleted workflow job names ('Go' from test.yml
in particular). The new gate names are listed in the design spec under
the post-merge checklist."
```

- [ ] **Step 2: Push**

```bash
git push
```

Verify the next push to a PR runs **only** `ci.yml` (no `Go` job from `test.yml`).

---

## Phase 6 — README badges + Docker Hub deprecation

Communication-only commit.

### Task 6.1: Add badges and ghcr.io install instructions to README

**Files:**
- Modify: `/home/gule/Workspace/team-infrastructure/vault-db-injector/README.md`

- [ ] **Step 1: Read the current README**

```bash
sed -n '1,30p' /home/gule/Workspace/team-infrastructure/vault-db-injector/README.md
```

- [ ] **Step 2: Add a badges row directly under the H1 title**

Insert after the first H1 line:

```markdown
[![CI](https://github.com/numberly/vault-db-injector/actions/workflows/ci.yml/badge.svg)](https://github.com/numberly/vault-db-injector/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/endpoint?url=https://gist.githubusercontent.com/<gist-user>/<COVERAGE_GIST_ID>/raw/vault-db-injector-coverage.json)](https://github.com/numberly/vault-db-injector/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/numberly/vault-db-injector?logo=github)](https://github.com/numberly/vault-db-injector/releases)
[![Image](https://img.shields.io/badge/image-ghcr.io-blue?logo=docker)](https://github.com/numberly/vault-db-injector/pkgs/container/vault-db-injector)
[![License](https://img.shields.io/github/license/numberly/vault-db-injector)](LICENSE)
```

The `<gist-user>` and `<COVERAGE_GIST_ID>` placeholders must be replaced by the operator with the actual values from the gist they created in Task 4.4.

- [ ] **Step 3: Update install instructions to reference ghcr.io**

Find the install section (typically `## Installation` or similar). Update to:

````markdown
## Installation

### Helm chart (Helm Pages)

```bash
helm repo add numberly https://numberly.github.io/vault-db-injector
helm repo update
helm install vault-db-injector numberly/vault-db-injector \
  --namespace vault-db-injector --create-namespace \
  -f my-values.yaml
```

### Helm chart (OCI)

```bash
helm install vault-db-injector \
  oci://ghcr.io/numberly/charts/vault-db-injector \
  --version 3.0.0 \
  --namespace vault-db-injector --create-namespace \
  -f my-values.yaml
```

### Container image

```bash
docker pull ghcr.io/numberly/vault-db-injector:v3.0.0
```

The image is signed with Cosign keyless. Verify before deployment:

```bash
cosign verify ghcr.io/numberly/vault-db-injector:v3.0.0 \
  --certificate-identity-regexp="https://github.com/numberly/vault-db-injector" \
  --certificate-oidc-issuer="https://token.actions.githubusercontent.com"
```

> **Docker Hub deprecation**: as of v3.0.0, releases publish to `ghcr.io` only. The `numberly/vault-db-injector` Docker Hub image is frozen at v2.x and will not receive further updates. Migrate by replacing `numberly/vault-db-injector:<tag>` with `ghcr.io/numberly/vault-db-injector:<tag>` in your values file.
````

If the existing README has different install copy, preserve the structural conventions (headers, code blocks) but make sure the three blocks above are present and the deprecation callout is at the bottom of the install section.

### Task 6.2: Commit Phase 6

- [ ] **Step 1: Stage and commit**

```bash
cd /home/gule/Workspace/team-infrastructure/vault-db-injector
git add README.md
git diff --cached --stat
git commit -m "docs(README): add CI/CD badges and migrate install instructions to ghcr.io

- Badge row covers CI status, coverage (gist-backed), latest release,
  image registry, and license
- Install section now leads with the Helm gh-pages repo (most common
  consumer flow), then OCI, then bare image pull
- Cosign verify command included for users who want to validate the
  signature before deploying
- Explicit Docker Hub deprecation callout: v2.x frozen on Docker Hub,
  v3.0+ on ghcr.io only

The badge URLs reference placeholder gist user and ID values; the
operator must replace these with the actual gist created during the
Phase 4 setup before merging."
```

- [ ] **Step 2: Push**

```bash
git push
```

Visual check: open the README on GitHub. Badges should render. If a badge URL still has `<placeholder>`, the operator hasn't filled it in yet.

---

## Self-review checklist

After completing all six phases:

- [ ] Spec section §3 decisions all reflected: scope tier B (mica + extensions), all artifacts on ghcr.io, release-please for changelogs, 1:1 chart=app version, gist coverage badge, Trivy blocking + `.trivyignore`. ✓
- [ ] Spec section §4 architecture: 4 workflows present, 3 legacy ones deleted in Phase 5. ✓
- [ ] Spec section §5 new config files: `.golangci.yml` (Phase 1), `.trivyignore` (Phase 2), `release-please-config.json` + manifest (Phase 3), `.github/dependabot.yml` (Phase 2) — all created. ✓
- [ ] Spec section §6 modified files: `deploy-docs.yml` (Phase 4), `README.md` (Phase 6), `helm/Chart.yml` `appVersion` (Phase 3). ✓
- [ ] Spec section §7 one-shot setup: surfaced as Task 4.4 (gist + PAT + branch protection) and Task 5.1 Step 3 (branch protection update). ✓
- [ ] Each phase = one commit per the spec phasing (§8). ✓
- [ ] No GoReleaser anywhere (per §3 "decisions deferred"). ✓
- [ ] `pr-title` job is guarded by `if: github.event_name == 'pull_request'` (per the design self-review note). ✓ (ci.yml Task 1.2)
- [ ] No placeholders ("TBD", "implement later"). The README badge URL has `<gist-user>` placeholders that are **operator-fillable**, with explicit instructions — this is intentional, not a plan defect. ✓

## Known operator-side blockers

These are not agent-executable steps; they will block the smoke test until done by the operator:

1. Create gist + `GIST_TOKEN` PAT + `COVERAGE_GIST_ID` repo variable (Task 4.4 Step 1)
2. Update branch protection required checks to match the new ci.yml job names (Task 4.4 Step 1, Task 5.1 Step 3)
3. Replace gist placeholders in README.md badges (Task 6.1 Step 2)

## Migration from Docker Hub (post v3.0.0)

After v3.0.0 ships, the operator should:

1. Verify v3.0.0 is live on `ghcr.io/numberly/vault-db-injector` and the chart on both gh-pages and OCI.
2. Add a `README` to the legacy `numberly/vault-db-injector` Docker Hub repo pointing at ghcr.io.
3. Remove repo secrets `DOCKER_USERNAME` and `DOCKER_PASSWORD` (no longer used).
4. Open a tracking issue for the eventual archival of the Docker Hub image (e.g. 12 months after v3.0.0 ships).
