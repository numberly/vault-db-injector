# Documentation overhaul — design

**Status:** approved (awaiting plan)
**Date:** 2026-05-05
**Author:** Guillaume LEGRAIN
**Scope:** Full rewrite of `docs/` (and supporting README updates), aligned with the changes brought by the `feat/ebpf-injection-mode` branch.

> Note on the branch name: eBPF mode was dropped during design. The branch
> ships **NRI mode** and **Projected-SA authentication** only.

## 1. Problem

The current documentation has grown organically and is now hard to navigate:

- `docs/getting-started/getting-started.md` is a single ~290-line monolith mixing prerequisites, vocabulary, Vault config, DB config, deploy, example app.
- `docs/how-it-works/` contains 14 files that mix concepts, components, modes, and configuration with no clear separation.
- `docs/monitoring/` has 3 files but only Grafana is wired into `mkdocs.yml`. Prometheus and Alertmanager pages are orphaned from the nav.
- The `feat/ebpf-injection-mode` branch introduces NRI mode and Projected-SA authentication — both major shifts in the security model — that are documented as add-ons rather than as the recommended path.
- i18n is configured (EN/FR) but only `getting-started/build.fr.md` exists. The current state is misleading: language switcher works but produces 99% English content.
- Internal planning artifacts (`docs/superpowers/{plans,specs}/`) live inside the published docs tree.

A new reader cannot answer basic questions:

- "What is the recommended way to deploy this in 2026?"
- "What does my application code need to do?"
- "What does the operator need to set up?"
- "How is this different from other Vault injectors?"

## 2. Goals

- Make NRI + Projected-SA the **canonical, recommended path**. The legacy webhook-with-injector-SA mode is documented but flagged as legacy.
- Provide three persona-oriented entry points (developer, operator, contributor) on top of a single linear getting-started flow.
- Every page passes the `humanizer` skill before commit, in both EN and FR.
- `mkdocs build --strict` succeeds with zero broken links at the end of every phase.
- Internal planning artifacts no longer live under `docs/`.

## 3. Non-goals

- Documenting eBPF mode. It is out of scope and will not be added.
- Translating to languages other than EN and FR.
- Restructuring the source code or Helm chart. The overhaul is documentation-only, except for trivial `mkdocs.yml`, `.gitignore`, and `README.md` adjustments.
- Adding new features to the project.

## 4. Audience

Three personas, each with a dedicated top-level section:

| Persona | Role | Primary needs |
|---|---|---|
| **Application developer** | Adds annotations to their own pods to consume injected credentials | Annotation reference, examples, app-side troubleshooting |
| **Platform operator / SRE** | Installs, configures, monitors, and migrates the injector | Architecture, security model, configuration, monitoring, operations, migration |
| **Contributor** | Extends or fixes the project | Build from source, code architecture, project comparison, contributing rules |

A fourth implicit audience — the new reader who has not yet picked a role — lands on the **Getting Started** path, which walks from zero to a first injected pod using the canonical NRI + Projected-SA stack.

## 5. Information architecture

```
Home (index.md)                      ← project pitch, mode matrix, persona links
│
├── Getting Started                  ← linear path: zero → first injected pod
│   ├── Overview & key concepts
│   ├── Prerequisites (hub)
│   ├── Setup: Kubernetes cluster
│   ├── Setup: Vault / OpenBao instance
│   ├── Setup: Database engine
│   ├── Vault policies & roles       ← NRI + Projected-SA
│   ├── Install the injector (Helm)
│   └── First injected pod
│
├── For Application Developers
│   ├── Annotations reference
│   ├── Injection modes seen from the app
│   ├── Examples (URI, env-key, multi-DB)
│   └── Troubleshooting
│
├── For Platform Operators
│   ├── Architecture overview
│   ├── Components (injector / renewer / revoker / NRI plugin)
│   ├── Security model (NRI hardening, Kyverno, PSA, Projected-SA)
│   ├── Monitoring (Prometheus + Grafana + Alertmanager merged)
│   ├── Operations (leader election, healthchecks, multi-release)
│   ├── Legacy webhook mode
│   └── Migration v2 → v3
│
├── For Contributors
│   ├── Build from source
│   ├── Contributing
│   ├── Project comparison
│   └── Architecture deep-dive
│
└── Reference
    ├── Glossary
    ├── Configuration (full)
    ├── Helm values
    └── Metrics catalog
```

Each page header carries an `Audience: <persona>` tag so the reader immediately knows whether the page is for them.

## 6. Writing standards

| Item | Rule |
|---|---|
| Source language | English. French is a mirror, suffixed `.fr.md` |
| Style | Concise, task-oriented. Short sentences. No marketing bullet lists |
| Humanizer | Every page passes `~/.agents/skills/humanizer/SKILL.md` before commit (EN and FR) |
| Code blocks | Real and runnable: Helm, Vault CLI, Terraform, kubectl. No pseudo-config |
| Internal links | mkdocs-relative (`../how-it-works/...`), never `https://numberly.github.io/...` |
| Diagrams | ASCII preferred (matches current `nri-mode.md` style). Mermaid only when ASCII genuinely fails |
| Admonitions | `!!! warning` for known pitfalls, `!!! note` for context, `!!! danger` for security caveats. No emojis in page titles |
| Versioning | Mention v3.0 where relevant (e.g. audiences hard-fail, projected-SA defaults) |
| Audience tagging | Every page begins with `Audience: <persona>` |
| Legacy pages | Begin with `!!! warning "Legacy mode"` and link to the canonical replacement |

## 7. File mapping

### Reused / moved / split

| Current | New location | Action |
|---|---|---|
| `README.md` | `README.md` | Light update for new nav links |
| `docs/index.md` | `docs/index.md` | Rewritten — pitch, mode matrix, persona links |
| `docs/getting-started/getting-started.md` | (split) | Eight new GS pages (see §5) |
| `docs/getting-started/comparison.md` | `docs/contributors/comparison.md` | Moved |
| `docs/getting-started/build.md` | `docs/contributors/build-from-source.md` | Moved + refreshed |
| `docs/getting-started/build.fr.md` | _deleted_ | Regenerated by FR mirror in phase 7 |
| `docs/getting-started/contributing.md` | `docs/contributors/contributing.md` | Moved |
| `docs/how-it-works/how-it-work.md` | `docs/operators/architecture.md` | Rewritten |
| `docs/how-it-works/nri-mode.md` | (split) | Canonical install bits → GS; threat model → operators/security |
| `docs/how-it-works/projected-sa.md` | (split) | Canonical install bits → GS/vault-policies; auth model → operators/security |
| `docs/how-it-works/configuration.md` | `docs/reference/configuration.md` | Rewritten, exhaustive |
| `docs/how-it-works/{injector,renewer,revoker}.md` | `docs/operators/components.md` | Merged into one page with three sections |
| `docs/how-it-works/vault.md` | _absorbed_ | GS (setup) + operators/architecture |
| `docs/how-it-works/vault-roles-and-policies.md` | `docs/getting-started/vault-policies.md` | Rewritten with NRI + Projected-SA as the main path |
| `docs/how-it-works/kubernetes.md` | _absorbed_ | GS (setup K8s) + operators |
| `docs/how-it-works/{leaderelection,healthcheck}.md` | `docs/operators/operations.md` | Merged |
| `docs/how-it-works/migration-v2-to-v3.md` | `docs/operators/migration-v2-to-v3.md` | Moved, content preserved |
| `docs/monitoring/{grafana,prometheus,alertmanager}.md` | `docs/operators/monitoring.md` | Merged into one page with three sections |

### Net-new pages

- `docs/getting-started/{overview,prerequisites,setup-kubernetes,setup-vault,setup-database,install-injector,first-injected-pod}.md`
- `docs/developers/{annotations,injection-modes,examples,troubleshooting}.md`
- `docs/operators/{security,legacy-webhook-mode}.md`
- `docs/reference/{glossary,helm-values,metrics}.md`

### Internal artifacts

- `docs/superpowers/` is moved out of `docs/`:
  - `docs/superpowers/specs/` → `.planning/specs/`
  - `docs/superpowers/plans/` → `.planning/plans/`
- `mkdocs.yml` excludes `.planning/`. `.gitignore` is left untouched (the directory is tracked).

## 8. Execution plan

Each phase is a single commit. `mkdocs build --strict` must succeed at the end of every phase.

| # | Phase | Output |
|---|---|---|
| 0 | Preparation | Move `docs/superpowers/` → `.planning/`. Update `mkdocs.yml` exclusions. Create empty new directories under `docs/` |
| 1 | Skeleton + Home | Rewrite `mkdocs.yml` (new nav + FR `nav_translations`). Rewrite `docs/index.md` |
| 2 | Getting Started (EN) | Eight new pages forming the canonical NRI + Projected-SA path. Humanizer pass |
| 3 | Operators (EN) | architecture, components, security, monitoring (3-merge), operations (2-merge), legacy-webhook-mode, migration. Humanizer pass |
| 4 | Developers (EN) | annotations, injection-modes, examples, troubleshooting. Humanizer pass |
| 5 | Contributors + Reference (EN) | build-from-source, contributing, comparison, glossary, helm-values, configuration, metrics. Humanizer pass |
| 6 | Cleanup | Delete obsolete files (`docs/how-it-works/*`, `docs/monitoring/*`, old `getting-started.md`, `build.fr.md`). Verify `mkdocs build --strict` is clean |
| 7 | FR mirror | Translate every page to `*.fr.md`. Humanizer pass on FR. `mkdocs build --strict` passes for both languages |

### Agent and model selection

Each writing phase is delegated to a dedicated agent. The user approves the spawn before it happens; no silent dispatch.

| Phase | Agent | Model |
|---|---|---|
| 0 | none (mechanical changes done in main session) | n/a |
| 1 | general-purpose | Sonnet |
| 2 | general-purpose | Sonnet |
| 3 | general-purpose | Opus (security model + architecture justify the cost) |
| 4 | general-purpose | Sonnet |
| 5 | general-purpose | Sonnet |
| 6 | none (mechanical) | n/a |
| 7 | general-purpose | Sonnet |

Each agent receives:

- This spec.
- The list of pages it owns.
- The writing standards from §6.
- An explicit instruction to invoke the humanizer skill on every page it produces, before reporting completion.

## 9. Validation

End of every phase:

```bash
mkdocs build --strict     # zero warnings
mkdocs serve              # spot-check nav and rendering
```

End of phase 7:

```bash
mkdocs build --strict     # both EN and FR build clean
```

Manual review at the end of phase 5: a fresh reader walks the Getting Started path against a real cluster and confirms they reach a working injected pod.

## 10. Risks and mitigations

| Risk | Mitigation |
|---|---|
| Broken external links from the published site (search engines, blog posts) | Keep the `index.md`, `getting-started/`, `how-it-works/`, `monitoring/` paths used by the current sitemap intact during phases 1–5; only delete in phase 6, after the new structure is in place. Add redirects via `mkdocs-redirects` plugin if needed |
| FR drift from EN over time | Treat FR as a regenerated mirror, not a parallel-edited tree. Any future edit goes EN-first, then FR is updated in the same commit |
| Agent ignores humanizer pass | Make humanizer invocation an explicit deliverable in each agent prompt, and verify on review that the produced text does not contain known AI tells |
| Scope creep into code/Helm changes | Spec is documentation-only. Any code change discovered as needed during writing is filed as a separate issue, not bundled |

## 11. Open questions

None at design time. Open questions discovered during execution will be added here and resolved before the affected phase commits.
