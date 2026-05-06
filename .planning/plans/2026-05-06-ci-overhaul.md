# CI/CD overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bring the `vault-db-injector` CI/CD up to OSS-grade parity with `terraform-provider-mica`, plus Helm-specific extensions (chart publishing on gh-pages and OCI), single-registry on `ghcr.io`, automated version bumps via `release-please`, Cosign keyless image signing, and Trivy vulnerability gating.

**Architecture:** Four GitHub Actions workflows: `ci.yml` (quality gate, also `workflow_call`), `release-please.yml` (version-bump bot), `release.yml` (tag-driven publish pipeline calling `ci.yml` as a gate), and the existing `deploy-docs.yml` (preserved with `keep_files: true`). Releases bump `helm/Chart.yml` (`version` + `appVersion`) and `helm/values.yml` image tags 1:1 with the tag.

**Tech Stack:** GitHub Actions, golangci-lint v2, govulncheck, helm-docs, chart-testing (`ct`), mkdocs-material (via `hatch -e docs`), Trivy, Cosign (keyless via Sigstore + GitHub OIDC), release-please, chart-releaser-action, Dependabot, dynamic-badges-action, Docker Buildx (multi-arch).

---

## Phase 0 — Operator setup (must complete before Phase 1)

Phase 1 commits push `ci.yml` to main. The `coverage-badge` job will fail authentication on `dynamic-badges-action` if `GIST_TOKEN` and `COVERAGE_GIST_ID` are not present. Setup must happen first.

### Task 0.1: Operator one-shot setup

**This task is run by the operator (Guillaume), not the agent.** The agent prints this checklist and waits for confirmation before proceeding to Phase 1.

- [ ] **Step 1: Create two private gists**

Visit https://gist.github.com twice. Create two **secret** gists, one for the coverage badge and one for the release version badge. Each can contain `{}` initially. Record both gist IDs from their URLs (`gist.github.com/<user>/<gistid>`).

- [ ] **Step 2: Generate a fine-grained PAT**

GitHub Settings → Developer settings → Personal access tokens → Fine-grained tokens.
- Resource owner: your account
- Expiration: 90 days (calendar-reminder; longer if org policy permits)
- Permissions → "Gists: Read and write" only (no other scopes)

Store as repo secret on `numberly/vault-db-injector`: name `GIST_TOKEN`.

- [ ] **Step 3: Add two repo variables**

Repo Settings → Secrets and variables → Actions → Variables tab → New repository variable, twice:
- `COVERAGE_GIST_ID` = the first gist ID (coverage)
- `RELEASE_GIST_ID` = the second gist ID (release)

- [ ] **Step 4: Survey open PRs**

Phase 1 introduces the `pr-title` Conventional-Commits gate. Any open PR with a non-conformant title will fail CI on rebase.

```bash
gh pr list --repo numberly/vault-db-injector --state open --json number,title,author
```

For each open PR, either: rename the title to conventional format (`feat:`, `fix:`, …), close-and-recreate, or coordinate with the author. Done before pushing Phase 1.

- [ ] **Step 5: Confirm "ready"**

Operator says "ready" or equivalent before the agent starts Phase 1.

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
        uses: golangci/golangci-lint-action@v9
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
        run: ct lint --charts helm --validate-maintainers=false
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
      - name: Install hatch
        run: pip install --upgrade hatch
      - name: Build docs (strict mode)
        run: hatch -e docs run mkdocs build --strict

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

Replaces the legacy test.yml gate with an 8-job parallel quality gate
(lint, test, coverage-badge, govulncheck, helm-lint, helm-docs-check,
docs-build-strict, pr-title) modelled on terraform-provider-mica's
ci.yml.

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

**Important context** — release-please's `extra-files` `jsonpath` field is **only** honored for `type: json`. For YAML files we use `type: generic` + inline `# x-release-please-version` annotation comments at the target lines. Task 3.4 adds those comments to `helm/Chart.yml` and `helm/values.yml`.

```json
{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "release-type": "simple",
  "include-component-in-tag": false,
  "include-v-in-tag": true,
  "packages": {
    ".": {
      "release-type": "simple",
      "package-name": "vault-db-injector",
      "extra-files": [
        {
          "type": "generic",
          "path": "helm/Chart.yml"
        },
        {
          "type": "generic",
          "path": "helm/values.yml"
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

Pin the manifest at `2.0.12` (the current image tag — see Task 3.4 which aligns chart `version` and `appVersion` to the same value). release-please bumps **from** the manifest based on conventional commits; this branch contains `feat!:` / `BREAKING CHANGE:` footers, so the next bot run computes `3.0.0`.

```json
{
  ".": "2.0.12"
}
```

The 1:1 invariant from spec §3 (chart `version` = chart `appVersion` = image tag = manifest = release tag) is now coherent.

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

### Task 3.4: Annotate `helm/Chart.yml` and `helm/values.yml` for release-please

release-please's generic updater replaces version values **on lines containing the inline annotation `# x-release-please-version`**. The chart currently has no `appVersion` field and `version: 2.0.0` is out of sync with the image tag `2.0.12`. Both must be aligned and annotated.

**Files:**
- Modify: `/home/gule/Workspace/team-infrastructure/vault-db-injector/helm/Chart.yml`
- Modify: `/home/gule/Workspace/team-infrastructure/vault-db-injector/helm/values.yml`

- [ ] **Step 1: Rewrite `helm/Chart.yml` with version sync + annotations**

```yaml
apiVersion: v1
name: vault-db-injector
version: 2.0.12 # x-release-please-version
appVersion: 2.0.12 # x-release-please-version
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
```

- [ ] **Step 2: Annotate the three image tags in `helm/values.yml`**

For each of `vaultDbInjector.injector.image.tag`, `vaultDbInjector.renewer.image.tag`, `vaultDbInjector.revoker.image.tag`, the YAML line must end with the inline annotation. The current line is:

```yaml
      tag: 2.0.12
```

Change each occurrence to:

```yaml
      tag: 2.0.12 # x-release-please-version
```

Use Edit with `replace_all: false` and a unique-context match per occurrence (the surrounding `image:` block of each section discriminates them; the helm-docs comment line `# -- <Component> container image tag.` precedes each one and is unique).

- [ ] **Step 3: Regenerate `helm/README.md`**

helm-docs strips inline annotations from the rendered values table by default (the comments after `# --` are the canonical source of description). Run:

```bash
make helm-docs
make helm-docs-check
```

Expected: README regenerates cleanly, `helm-docs-check` passes (exit 0). If `helm-docs-check` fails, the `helm/README.md` differs from a clean regenerate — commit the regenerated file.

- [ ] **Step 4: Verify a release-please dry-run picks up the annotations**

Optional but strongly recommended. From a fork or a test branch:

```bash
docker run --rm -v "$PWD":/repo -w /repo \
  ghcr.io/googleapis/release-please:latest \
  release-pr \
  --token=fake \
  --repo-url=https://github.com/numberly/vault-db-injector \
  --config-file=release-please-config.json \
  --manifest-file=.release-please-manifest.json \
  --dry-run
```

Expected: log lines mentioning `helm/Chart.yml` and `helm/values.yml` as files to be updated, with the `2.0.12 → 3.0.0` substitutions visible. If the run is silent on those files, the annotations are misplaced — fix before proceeding.

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
      - "v[0-9]+.[0-9]+.[0-9]+"
      - "v[0-9]+.[0-9]+.[0-9]+-*"

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
    permissions:
      packages: write
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
      - name: Delete the transient :VERSION-unsigned tag
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          VERSION: ${{ needs.build-image.outputs.version }}
        run: |
          # GHCR keeps versions by digest; deleting the :VERSION-unsigned
          # tag does NOT delete the underlying digest (which :VERSION and
          # :latest still reference). The signature attached to that
          # digest is preserved.
          PACKAGE_TYPE=container
          PACKAGE_NAME=vault-db-injector
          ORG=numberly
          # Find the package version whose tags include VERSION-unsigned
          VID=$(gh api -H "Accept: application/vnd.github+json" \
            "/orgs/${ORG}/packages/${PACKAGE_TYPE}/${PACKAGE_NAME}/versions" \
            --paginate \
            --jq ".[] | select(.metadata.container.tags[]? == \"${VERSION}-unsigned\") | .id" \
            | head -1)
          if [ -n "${VID}" ]; then
            gh api -X DELETE -H "Accept: application/vnd.github+json" \
              "/orgs/${ORG}/packages/${PACKAGE_TYPE}/${PACKAGE_NAME}/versions/${VID}"
            echo "Deleted package version ${VID} that held tag ${VERSION}-unsigned"
          else
            echo "No package version found holding tag ${VERSION}-unsigned (already deleted?)"
          fi

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
      - name: Pre-package the chart
        run: |
          mkdir -p .cr-release-packages
          helm package helm -d .cr-release-packages
          ls -la .cr-release-packages
      - uses: helm/chart-releaser-action@v1.7.0
        with:
          charts_dir: helm
          skip_packaging: true
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
          # helm push writes "Digest: sha256:..." to stderr. Capture only
          # that line — sha256:... could otherwise also appear in body
          # text or chart-level digests we don't want.
          helm push "${PKG}" oci://ghcr.io/numberly/charts 2> /tmp/helm-push.err
          cat /tmp/helm-push.err
          DIGEST=$(awk '/^Digest: sha256:/ {print $2}' /tmp/helm-push.err)
          if [ -z "${DIGEST}" ]; then
            echo "::error::Failed to extract chart manifest digest from helm push output"
            exit 1
          fi
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
            --certificate-identity-regexp=^https://github.com/numberly/vault-db-injector/.github/workflows/release.yml@refs/tags/v[0-9].*$ \\\\
            --certificate-oidc-issuer=https://token.actions.githubusercontent.com
          \`\`\`
          "
          gh release edit "${TAG}" --repo "${REPO}" --notes "${BODY}${APPENDIX}"

  update-release-badge:
    name: Update release version badge
    needs: [retag-stable, chart-releaser, chart-oci-push]
    if: needs.build-image.outputs.is-prerelease == 'false'
    runs-on: ubuntu-latest
    steps:
      - name: Push release version to gist
        uses: schneegans/dynamic-badges-action@v1.7.0
        with:
          auth: ${{ secrets.GIST_TOKEN }}
          gistID: ${{ vars.RELEASE_GIST_ID }}
          filename: vault-db-injector-release.json
          label: release
          message: ${{ needs.build-image.outputs.version }}
          color: blue
```

The badge job uses a **second** gist (`RELEASE_GIST_ID`) so coverage and release values do not collide on the same JSON. Operator setup (Phase 0) creates the second gist and stores its ID as a repo variable.

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
      - name: Install hatch
        run: pip install --upgrade hatch
      - name: Build site
        run: hatch -e docs run mkdocs build
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
  --certificate-identity-regexp="^https://github.com/numberly/vault-db-injector/.github/workflows/release.yml@refs/tags/v[0-9].*$" \
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
  --certificate-identity-regexp="^https://github.com/numberly/vault-db-injector/.github/workflows/release.yml@refs/tags/v[0-9].*$" \
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

After completing all phases:

- [ ] Spec §3 decisions reflected: scope tier B, all artifacts on ghcr.io, release-please for changelogs, 1:1 chart=app version (manifest pinned at `2.0.12` to match), gist coverage badge, Trivy blocking + `.trivyignore`.
- [ ] Spec §4 architecture: 4 workflows present, 3 legacy ones deleted in Phase 5.
- [ ] Spec §5 new config files: `.golangci.yml` (Phase 1), `.trivyignore` (Phase 2), `release-please-config.json` + manifest (Phase 3), `.github/dependabot.yml` (Phase 2) — all created.
- [ ] Spec §6 modified files: `deploy-docs.yml` (Phase 4), `README.md` (Phase 6), `helm/Chart.yml` (Phase 3, version sync + annotations), `helm/values.yml` (Phase 3, image-tag annotations).
- [ ] Spec §7 one-shot setup: surfaced as **Phase 0** (gist + PAT + open-PR survey) and Task 5.1 Step 3 (branch protection update post-Phase-5).
- [ ] Each phase = one commit per the spec phasing (§8). Phase 0 is operator-only, no commit.
- [ ] No GoReleaser anywhere (per §3 decisions).
- [ ] `pr-title` job guarded by `if: github.event_name == 'pull_request'` (ci.yml Task 1.2).
- [ ] release-please uses `type: generic` + `# x-release-please-version` annotations (per release-please v4 docs — `jsonpath` only honored for `type: json`).
- [ ] chart-releaser-action uses `skip_packaging: true` after a manual `helm package` because the chart lives at `helm/Chart.yml` (not `helm/<chartname>/Chart.yaml`).
- [ ] `:VERSION-unsigned` tag deleted via `gh api` after `retag-stable` to prevent leak of an unsigned tag pointer.
- [ ] Cosign verify uses workflow-pinned identity regex (`/.github/workflows/release.yml@refs/tags/v[0-9].*$`), not the bare repo URL.
- [ ] No placeholders ("TBD", "implement later"). README has `<gist-user>` and gist IDs that are **operator-fillable** with explicit instructions; this is intentional.

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
