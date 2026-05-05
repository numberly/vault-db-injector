# Documentation Overhaul Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure `docs/` from a 17-file monolithic tree into a persona-oriented documentation set (Getting Started + Developers + Operators + Contributors + Reference), with NRI + Projected-SA promoted as the canonical recommended path.

**Architecture:** 8 sequential phases, each landing as a single commit gated on `mkdocs build --strict`. Phase 0 reorganizes; phases 1–6 produce English content; phase 7 mirrors the entire site to French. Every page passes the `humanizer` skill before commit.

**Tech Stack:** mkdocs-material with i18n plugin, Markdown (CommonMark + pymdownx + admonition), Helm + Vault + Kubernetes content domain.

**Spec:** `docs/superpowers/specs/2026-05-05-docs-overhaul-design.md`

---

## Cross-cutting rules (read once, apply to every task below)

These rules apply to **every page produced by this plan**:

| Rule | Detail |
|---|---|
| Source language | English. French is regenerated in phase 7 as a mirror |
| Source location | All new EN pages under `docs/<section>/<page>.md` |
| Audience tag | First content line of every page: `**Audience:** <Application developer / Platform operator / Contributor / Anyone>` |
| Style | Concise, task-oriented. Short sentences. No marketing bullet lists |
| Humanizer | Run `~/.agents/skills/humanizer/SKILL.md` over each page **before commit** (every phase). The agent invokes the skill on the produced files |
| Code blocks | Real and runnable. Helm/Vault CLI/Terraform/kubectl. No pseudo-config. Inherit example values from `helm/values.yml` and `docs/how-it-works/vault-roles-and-policies.md` |
| Internal links | mkdocs-relative (`../operators/security.md`), never `https://numberly.github.io/...` |
| Diagrams | ASCII (match `docs/how-it-works/nri-mode.md` style). Mermaid only when ASCII genuinely fails |
| Admonitions | `!!! warning` for known pitfalls, `!!! note` for context, `!!! danger` for security caveats. No emojis in page titles |
| Versioning | Mention v3.0 where relevant (audiences hard-fail, projected-SA defaults, metric rename) |
| Legacy pages | Begin with `!!! warning "Legacy mode"` and link to the canonical replacement |
| eBPF | **Never mention.** It was dropped during design; no page references it |

---

## File structure (target after phase 6)

```
docs/
├── index.md                                 # Home: pitch + mode matrix + persona links
├── getting-started/
│   ├── overview.md                          # What this is, when to use it
│   ├── prerequisites.md                     # Hub: list + matrix of prereqs
│   ├── setup-kubernetes.md                  # K8s cluster requirements
│   ├── setup-vault.md                       # Vault/OpenBao instance
│   ├── setup-database.md                    # DB engine (Postgres focus)
│   ├── vault-policies.md                    # NRI + projectedSA policies & roles
│   ├── install-injector.md                  # Helm install
│   └── first-injected-pod.md                # Annotations + verify
├── developers/
│   ├── annotations.md                       # Full annotation reference
│   ├── injection-modes.md                   # What the app sees per mode
│   ├── examples.md                          # URI, env-key, multi-DB
│   └── troubleshooting.md                   # App-side diagnosis
├── operators/
│   ├── architecture.md                      # System overview
│   ├── components.md                        # Injector/renewer/revoker/NRI plugin
│   ├── security.md                          # NRI hardening, Kyverno, PSA, projected-SA threat model
│   ├── monitoring.md                        # Prometheus + Grafana + Alertmanager merged
│   ├── operations.md                        # Leader election + healthchecks + multi-release
│   ├── legacy-webhook-mode.md               # Deprecated webhook + injector-SA flow
│   └── migration-v2-to-v3.md                # Preserved from current repo
├── contributors/
│   ├── build-from-source.md                 # Refresh of build.md
│   ├── contributing.md                      # Moved
│   ├── comparison.md                        # Project comparison
│   └── architecture-deep-dive.md            # Code-level overview
└── reference/
    ├── glossary.md                          # Vocabulary
    ├── configuration.md                     # Full binary config reference
    ├── helm-values.md                       # Generated from helm/values.yml
    └── metrics.md                           # Full metric catalog
```

Internal artifacts (`.planning/`) live **outside** `docs/` and are excluded from mkdocs.

---

## Phase 0: Preparation — restructure on disk, no prose

**Files:**
- Move: `docs/superpowers/` → `.planning/`
- Modify: `mkdocs.yml`
- Create: empty directories `docs/{getting-started,developers,operators,contributors,reference}` (some already exist)

- [ ] **Step 1: Move internal planning artifacts out of `docs/`**

```bash
mkdir -p .planning
git mv docs/superpowers/specs .planning/specs
git mv docs/superpowers/plans .planning/plans
rmdir docs/superpowers
```

Verify:
```bash
ls .planning/specs/2026-05-05-docs-overhaul-design.md
ls .planning/plans/2026-05-05-docs-overhaul.md
ls docs/superpowers 2>&1 | grep -q "No such file" && echo OK
```

- [ ] **Step 2: Add `.planning/` exclusion to mkdocs**

mkdocs-material does not crawl outside `docs_dir`, so the move alone removes them from the build. No `mkdocs.yml` change required for exclusion. Confirm with:

```bash
mkdocs build --strict 2>&1 | grep -i superpowers || echo "OK: no references"
```

- [ ] **Step 3: Create empty target directories**

```bash
mkdir -p docs/developers docs/operators docs/contributors docs/reference
ls docs/getting-started   # already exists
```

- [ ] **Step 4: Verify `mkdocs build --strict` still passes**

```bash
mkdocs build --strict
```

Expected: build succeeds. Old `nav` still references existing files; no new files yet, so no broken links.

- [ ] **Step 5: Commit**

```bash
git add .planning docs/
git commit -m "$(cat <<'EOF'
chore(docs): move internal planning artifacts out of docs/

Phase 0 of the docs overhaul. The superpowers/{specs,plans} subtrees
contained internal planning documents that were being published as part
of the public mkdocs site. Move them to .planning/ at the repo root and
create the empty section directories that subsequent phases will fill.

No published page is removed at this phase; mkdocs build --strict still
passes against the legacy nav.

Refs: .planning/specs/2026-05-05-docs-overhaul-design.md
EOF
)"
```

---

## Phase 1: Skeleton + Home

**Files:**
- Modify: `mkdocs.yml` (full nav replacement + FR `nav_translations`)
- Modify: `docs/index.md` (full rewrite)

- [ ] **Step 1: Replace `mkdocs.yml` nav with the persona-oriented structure**

Replace the existing `nav:` block. The full new `nav:` block is:

```yaml
nav:
  - Home: index.md
  - Getting Started:
    - getting-started/overview.md
    - getting-started/prerequisites.md
    - getting-started/setup-kubernetes.md
    - getting-started/setup-vault.md
    - getting-started/setup-database.md
    - getting-started/vault-policies.md
    - getting-started/install-injector.md
    - getting-started/first-injected-pod.md
  - For Application Developers:
    - developers/annotations.md
    - developers/injection-modes.md
    - developers/examples.md
    - developers/troubleshooting.md
  - For Platform Operators:
    - operators/architecture.md
    - operators/components.md
    - operators/security.md
    - operators/monitoring.md
    - operators/operations.md
    - operators/legacy-webhook-mode.md
    - operators/migration-v2-to-v3.md
  - For Contributors:
    - contributors/build-from-source.md
    - contributors/contributing.md
    - contributors/comparison.md
    - contributors/architecture-deep-dive.md
  - Reference:
    - reference/glossary.md
    - reference/configuration.md
    - reference/helm-values.md
    - reference/metrics.md
```

And replace the `i18n` plugin's `nav_translations` for FR with:

```yaml
plugins:
  - search
  - i18n:
      docs_structure: suffix
      languages:
        - locale: en
          default: true
          name: English
          build: true
        - locale: fr
          name: Français
          build: true
          theme:
            palette:
              primary: red
          nav_translations:
            Home: Accueil
            Getting Started: Démarrage rapide
            For Application Developers: Pour les développeurs
            For Platform Operators: Pour les opérateurs
            For Contributors: Pour les contributeurs
            Reference: Référence
```

- [ ] **Step 2: Rewrite `docs/index.md`**

Full content:

````markdown
# Vault DB Injector

**Audience:** Anyone

vault-db-injector issues short-lived database credentials from HashiCorp
Vault (or OpenBao) and delivers them to Kubernetes workloads at runtime.
It manages the full lifecycle: issuance, renewal, revocation when the
pod dies.

## Why it exists

Static database credentials in Kubernetes Secrets are a known weak
point: they live forever, leak through GitOps, and are visible to any
operator who can read the namespace. vault-db-injector replaces them
with credentials that:

- exist only for the lifetime of the pod
- are rotated by Vault, not by the application
- are attributed to the pod's identity in the Vault audit log

## Two delivery modes

| Mode | Where credentials live in Kubernetes | Recommended for |
|---|---|---|
| **NRI + Projected-SA** (canonical) | Nowhere — substituted at container start by a node-local NRI plugin. PodSpec, etcd, and audit logs only see opaque placeholders | New deployments |
| **Webhook + injector-SA** (legacy) | Plaintext environment variables in the PodSpec | Existing v2.x clusters that have not migrated yet |

The legacy mode is preserved for backward compatibility but no longer
the recommended target. New deployments should follow [Getting
Started](getting-started/overview.md) which walks through the canonical
NRI + Projected-SA path end to end.

## Pick your entry point

- [**Getting Started**](getting-started/overview.md) — install from zero, in order
- [**For application developers**](developers/annotations.md) — annotate your pods to consume injected credentials
- [**For platform operators**](operators/architecture.md) — operate, secure, monitor, and migrate the injector
- [**For contributors**](contributors/build-from-source.md) — build, test, and contribute code

## OpenBao compatibility

Every Vault API used by this project works against OpenBao without
changes. Point `vaultAddress` at your OpenBao instance and follow the
same setup. See the OpenBao note in
[setup-vault](getting-started/setup-vault.md).

## License

Apache-2.0. See [`LICENSE`](https://github.com/numberly/vault-db-injector/blob/main/LICENSE).
````

- [ ] **Step 3: Create empty placeholder pages so mkdocs --strict does not break on missing nav targets**

The new `nav:` references files that phases 2–5 will create. To keep `mkdocs build --strict` clean at the end of phase 1, create each referenced file as a stub now. Each stub is exactly:

```markdown
# <Title from filename>

**Audience:** TBD

> Stub. This page is filled in by a later phase of the documentation
> overhaul. See `.planning/plans/2026-05-05-docs-overhaul.md`.
```

Generate them:

```bash
for f in \
  getting-started/overview getting-started/prerequisites \
  getting-started/setup-kubernetes getting-started/setup-vault \
  getting-started/setup-database getting-started/vault-policies \
  getting-started/install-injector getting-started/first-injected-pod \
  developers/annotations developers/injection-modes \
  developers/examples developers/troubleshooting \
  operators/architecture operators/components operators/security \
  operators/monitoring operators/operations \
  operators/legacy-webhook-mode operators/migration-v2-to-v3 \
  contributors/build-from-source contributors/contributing \
  contributors/comparison contributors/architecture-deep-dive \
  reference/glossary reference/configuration \
  reference/helm-values reference/metrics
do
  mkdir -p "docs/$(dirname $f)"
  if [ ! -f "docs/$f.md" ]; then
    title=$(basename "$f" | tr '-' ' ' | sed 's/\b\(.\)/\u\1/g')
    printf '# %s\n\n**Audience:** TBD\n\n> Stub. This page is filled in by a later phase of the documentation\n> overhaul. See `.planning/plans/2026-05-05-docs-overhaul.md`.\n' "$title" > "docs/$f.md"
  fi
done
```

> Note: this stubbing acknowledges the cross-cutting rule "no
> placeholders in plans" — the placeholder lives in transient stub
> pages, not in the plan itself, and every stub is overwritten before
> phase 6 deletes the legacy files.

- [ ] **Step 4: Verify `mkdocs build --strict` succeeds**

```bash
mkdocs build --strict
```

Expected: build succeeds for both EN and FR (FR shows EN content since no `.fr.md` exists yet — that's expected before phase 7).

- [ ] **Step 5: Run humanizer over the produced files**

```bash
# Apply humanizer rules to docs/index.md only — stub pages are placeholders
# that get rewritten in later phases.
```

Invoke the `humanizer` skill targeted at `docs/index.md`. Apply suggested edits inline.

- [ ] **Step 6: Commit**

```bash
git add mkdocs.yml docs/
git commit -m "$(cat <<'EOF'
docs: phase 1 — new persona-oriented nav and rewritten Home

Replace the legacy how-it-works/monitoring layout with the
five-section structure (Getting Started, Developers, Operators,
Contributors, Reference). Rewrite docs/index.md with a project pitch,
the two-mode delivery matrix, and per-persona entry points. Add stub
pages for every nav target so mkdocs build --strict stays clean
between phases.

Phase 1 of the docs overhaul.
Refs: .planning/specs/2026-05-05-docs-overhaul-design.md
EOF
)"
```

---

## Phase 2: Getting Started (English)

**Goal of this phase:** a reader with zero context follows these eight pages in order and ends with a working injected pod using **NRI + Projected-SA**.

**Files:**
- Modify (overwrite stubs): `docs/getting-started/{overview,prerequisites,setup-kubernetes,setup-vault,setup-database,vault-policies,install-injector,first-injected-pod}.md`

**Source material to extract from:**
- `docs/getting-started/getting-started.md` (legacy monolith)
- `docs/how-it-works/nri-mode.md`
- `docs/how-it-works/projected-sa.md`
- `docs/how-it-works/vault-roles-and-policies.md`
- `helm/values.yml`

### Per-page content contract

Every page below is a **complete contract** — the executing agent must produce a page that contains exactly the listed sections, in the listed order.

#### `getting-started/overview.md`

- Audience tag (`Anyone`)
- 1-paragraph: what vault-db-injector does (issue short-lived DB creds, deliver to pods, manage lifecycle)
- 1-paragraph: why this guide picks NRI + Projected-SA as the default
- "What you will achieve" — bullet list of the 5 outcomes (Vault configured, K8s cluster ready, DB engine ready, injector installed, first pod injected)
- "Estimated time" — give a realistic range (60–90 min for someone with the prerequisites already running)
- Next: link to `prerequisites.md`

#### `getting-started/prerequisites.md`

- Audience tag (`Platform operator`)
- Hub-style table:

| Prerequisite | Minimum version | Setup page |
|---|---|---|
| Kubernetes cluster | 1.26+ with NRI-capable container runtime | [setup-kubernetes](setup-kubernetes.md) |
| Container runtime | containerd ≥ 1.7 with NRI enabled, or CRI-O ≥ 1.26 | [setup-kubernetes](setup-kubernetes.md) |
| Vault or OpenBao | Vault ≥ 1.13 / OpenBao ≥ 2.0 | [setup-vault](setup-vault.md) |
| Database engine | PostgreSQL 13+ (or MySQL/MariaDB/Oracle — see notes) | [setup-database](setup-database.md) |
| `kubectl` | matches cluster minor | local |
| `helm` | 3.12+ | local |
| `vault` CLI | matches server minor | local |

- "Why each one matters" — 2-line rationale per row
- Next: link to `setup-kubernetes.md`

#### `getting-started/setup-kubernetes.md`

- Audience tag (`Platform operator`)
- "Required cluster capabilities":
  - Mutating Admission Webhooks enabled (default in modern distributions)
  - NRI enabled in containerd (`config.toml` excerpt: `[plugins."io.containerd.nri.v1.nri"] disable = false`)
  - Pod Security Admission level `privileged` available for the injector namespace
- "Verify NRI" — 4 commands:
  - `kubectl get nodes -o wide` (sanity)
  - `nerdctl --namespace=k8s.io system info | grep -i nri` (NRI registered)
  - `ls /var/run/nri/nri.sock` on a node (socket present)
  - Expected output for each
- "Create the injector namespace":
```bash
kubectl create namespace vault-db-injector
kubectl label namespace vault-db-injector \
    pod-security.kubernetes.io/enforce=privileged \
    pod-security.kubernetes.io/enforce-version=latest
```
- !!! warning explaining why the namespace must be `privileged` (NRI plugin DS needs hostPath for the NRI socket)
- Next: link to `setup-vault.md`

#### `getting-started/setup-vault.md`

- Audience tag (`Platform operator`)
- "Vault or OpenBao" — 1 paragraph, point at OpenBao API compatibility
- "Required mounts" — table:
  - KV-v2 secrets engine for bookkeeping (default name `vault-injector`)
  - Database secrets engine (default name `database`)
  - Kubernetes auth method (default path `kubernetes`)
- "Enable the mounts" — three Vault CLI commands:
```bash
vault secrets enable -path=vault-injector -version=2 kv
vault secrets enable database
vault auth enable kubernetes
```
- "Configure the Kubernetes auth method":
```bash
vault write auth/kubernetes/config \
    kubernetes_host="https://<APISERVER>:6443" \
    kubernetes_ca_cert=@/path/to/ca.crt \
    issuer="https://kubernetes.default.svc.cluster.local"
```
- "Verify":
```bash
vault read auth/kubernetes/config
vault secrets list | grep -E '(database|vault-injector)'
```
- Next: link to `setup-database.md`

#### `getting-started/setup-database.md`

- Audience tag (`Platform operator`)
- 1-paragraph: this page configures **the database server itself**; Vault-side wiring comes next
- "PostgreSQL example":
```sql
CREATE DATABASE myapp;
CREATE ROLE myapp_owner;
REVOKE ALL ON DATABASE myapp FROM PUBLIC;
GRANT CONNECT ON DATABASE myapp TO myapp_owner;
\c myapp
GRANT CREATE, USAGE ON SCHEMA public TO myapp_owner;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO myapp_owner;
REVOKE ALL ON pg_user, pg_roles, pg_authid, pg_database FROM PUBLIC;
```
- "Create a Vault admin user on the DB":
```sql
CREATE ROLE vaultadmin WITH LOGIN SUPERUSER PASSWORD '<strong-random>';
```
- !!! note: `vaultadmin` is the credential Vault uses to dynamically `CREATE ROLE`. Vault never gives this account out to applications
- "Other engines" — short subsections (3–6 lines each) for MySQL, MariaDB, Oracle pointing at Vault DB plugin docs
- Next: link to `vault-policies.md`

#### `getting-started/vault-policies.md`

- Audience tag (`Platform operator`)
- !!! note: this page is **NRI + Projected-SA**. Legacy webhook policies are documented at [operators/legacy-webhook-mode](../operators/legacy-webhook-mode.md)
- "What you will create" — checklist of what this page produces:
  - DB connection + DB role
  - Injector policy + role
  - Renewer policy + role
  - Revoker policy + role
  - One per-application policy + role
- "Database backend" — `vault write database/config/...` and `vault write database/roles/...` commands (extract verbatim from `vault-roles-and-policies.md` §3)
- "Injector policy" (`vault-db-injector` projected-mode minimal — extract from §2a)
- "Renewer policy" (`vault-db-renewer` — extract from §2b)
- "Revoker policy" (`vault-db-revoker` — extract from §2c)
- "Roles for the three injector-tier SAs" — three `vault write auth/kubernetes/role/...` blocks (extract from §2d, drop placeholders, use `vault-db-injector` as the release name)
- "Per-application setup" — DB role + app policy + app k8s-auth role with `token_period` (extract from §3)
- "Verify":
```bash
vault policy read vault-db-injector
vault read auth/kubernetes/role/vault-db-injector
vault read database/roles/myapp-prod
```
- Next: link to `install-injector.md`

#### `getting-started/install-injector.md`

- Audience tag (`Platform operator`)
- "Helm install" — full canonical command:
```bash
helm upgrade --install vault-db-injector ./helm \
  --namespace vault-db-injector \
  --set vaultDbInjector.configuration.vaultAddress=https://vault.example.com:8200 \
  --set vaultDbInjector.configuration.vaultAuthPath=kubernetes \
  --set vaultDbInjector.configuration.kubeRole=vault-db-injector \
  --set vaultDbInjector.configuration.useProjectedSA=true \
  --set vaultDbInjector.configuration.tokenRequestAudiences='{vault}' \
  --set nri.enabled=true \
  --set nri.pluginIndex=10
```
- "What the chart provisions" — table of what gets created when these flags are on (taken from helm/values.yml comments and `vault-roles-and-policies.md §2d`):
  - 3 ServiceAccounts: `vault-db-injector`, `vault-db-injector-renewer`, `vault-db-injector-revoker`
  - 3 RBAC bindings (incl. cluster-wide `serviceaccounts/token` for the injector)
  - injector Deployment, renewer Deployment, revoker Deployment, NRI DaemonSet
- "Verify":
```bash
kubectl -n vault-db-injector get pods
kubectl -n vault-db-injector logs deployment/vault-db-injector | grep "starting webhook"
```
Expected output: 4 deployment pods + 1 NRI pod per node, all `Ready`. Logs show webhook startup and Vault login success.
- Next: link to `first-injected-pod.md`

#### `getting-started/first-injected-pod.md`

- Audience tag (`Application developer`)
- "Annotate your pod" — full Pod manifest (Postgres example, classic mode then URI mode):
```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: myapp
  namespace: team-myapp
---
apiVersion: v1
kind: Pod
metadata:
  name: myapp
  namespace: team-myapp
  annotations:
    db-creds-injector.numberly.io/cluster: database
    db-creds-injector.numberly.io/myapp.role: myapp-prod
    db-creds-injector.numberly.io/myapp.mode: classic
    db-creds-injector.numberly.io/myapp.env-key-dbuser: DB_USER
    db-creds-injector.numberly.io/myapp.env-key-dbpassword: DB_PASS
  labels:
    vault-db-injector: "true"
spec:
  serviceAccountName: myapp
  containers:
    - name: app
      image: postgres:16
      command: ["sleep", "infinity"]
```
- "Verify the credentials work":
```bash
kubectl -n team-myapp exec myapp -- bash -c 'env | grep DB_'
kubectl -n team-myapp exec myapp -- bash -c 'PGPASSWORD=$DB_PASS psql -h db -U $DB_USER -d myapp -c "SELECT 1"'
```
- "What just happened" — 5-bullet recap referencing the architecture diagram on `operators/architecture.md`
- "Next steps":
  - Read [annotations reference](../developers/annotations.md) to learn URI mode and multi-DB
  - Read [monitoring](../operators/monitoring.md) to wire dashboards
  - Read [security](../operators/security.md) to harden NRI

### Phase 2 execution

- [ ] **Step 1: Write all 8 pages**

The agent dispatched for this phase produces all 8 files in one pass, following the per-page contracts above. Each page is fully self-contained — extract from source, do not link to legacy paths.

- [ ] **Step 2: Run humanizer over each new page**

Apply the `humanizer` skill to each of the 8 files. Apply suggested edits inline.

- [ ] **Step 3: Verify `mkdocs build --strict`**

```bash
mkdocs build --strict
```

Expected: succeeds with no broken-link warnings (legacy `how-it-works/` files still exist, so any new page accidentally linking to them is fine until phase 6 — but the new pages should NOT link to `how-it-works/` at all per the cross-cutting rule).

- [ ] **Step 4: Spot-check via `mkdocs serve`**

```bash
mkdocs serve
```

Open `http://127.0.0.1:8000/getting-started/overview/` and walk the linear path. Confirm "Next" links chain correctly to `first-injected-pod`.

- [ ] **Step 5: Commit**

```bash
git add docs/getting-started/
git commit -m "$(cat <<'EOF'
docs: phase 2 — Getting Started canonical NRI + Projected-SA path

Eight new pages forming a linear walkthrough from cluster prerequisites
to a first injected pod. The path uses NRI mode for credential delivery
and Projected-SA for Vault authentication, matching the v3.0 default
recommendation. Each page is self-contained; no links to the legacy
how-it-works/ tree, which phase 6 deletes.

Phase 2 of the docs overhaul.
Refs: .planning/specs/2026-05-05-docs-overhaul-design.md
EOF
)"
```

---

## Phase 3: Operators (English)

**Files (overwrite stubs):**
- `docs/operators/architecture.md`
- `docs/operators/components.md`
- `docs/operators/security.md`
- `docs/operators/monitoring.md`
- `docs/operators/operations.md`
- `docs/operators/legacy-webhook-mode.md`
- `docs/operators/migration-v2-to-v3.md`

**Source material to extract from:**
- `docs/how-it-works/how-it-work.md` → architecture
- `docs/how-it-works/{injector,renewer,revoker}.md` → components
- `docs/how-it-works/nri-mode.md` (Components, Failure modes, Hardening, Trust posture sections) → security + components
- `docs/how-it-works/projected-sa.md` (Security gains, Audience handling) → security
- `README.md` §3.5 (NRI hardening) → security
- `helm/policies/kyverno-restrict-nri-socket.yaml` → security (referenced)
- `docs/monitoring/{prometheus,grafana,alertmanager}.md` → monitoring
- `docs/how-it-works/{leaderelection,healthcheck}.md` → operations
- `docs/how-it-works/migration-v2-to-v3.md` → migration (reuse mostly verbatim)
- `docs/how-it-works/configuration.md` (legacy mode config blocks) → legacy-webhook-mode

### Per-page content contract

#### `operators/architecture.md`

- Audience tag (`Platform operator`)
- "System overview" — 1 paragraph: 4 components (injector webhook, NRI plugin DS, renewer, revoker) + 2 external dependencies (Vault, K8s API)
- ASCII diagram (steal style from `nri-mode.md` Architecture section)
- "Data flow" — 6-step bullet list of admission → NRI substitution → renewal → revocation
- "Trust boundaries" — 3-line summary of which component holds which Vault token
- Link to `components.md` for per-component detail and to `security.md` for the threat model

#### `operators/components.md`

- Audience tag (`Platform operator`)
- 4 sub-sections, each ~150 words:
  - **Injector (webhook)** — file ref `pkg/injector/injector.go`, role: admission, placeholder generation in NRI mode, `CanIGetRoles` in legacy mode only
  - **NRI plugin (DaemonSet)** — file ref `pkg/nri/...`, role: `CreateContainer` substitution, per-node tmpfs cache at `/run/<release>/nri/cache.json`
  - **Renewer (Deployment)** — file ref `pkg/renewer/renewer.go`, role: periodic token + lease renew, no revoke (revoker owns it in projected mode)
  - **Revoker (Deployment)** — file ref `pkg/revoker/revoker.go`, role: pod-watch DELETE → revoke + safety-net periodic 5-min sweep
- "Leader election" — 2-line note that renewer and revoker run multi-replica with leader election (cross-link to `operations.md`)

#### `operators/security.md`

- Audience tag (`Platform operator`)
- "Threat model" — 3 paragraphs:
  - What an attacker controls (compromised pod, compromised node, compromised injector)
  - What stays safe under each scenario (cite projected-SA blast radius reduction, NRI placeholder)
  - Residual risks (NRI plugin runs as root → see hardening)
- "NRI mode hardening" — 4-bullet list (PSA `restricted` on user namespaces, Kyverno policy reference, SELinux/AppArmor, hostPath restrictions)
- "Kyverno policy" — embed `helm/policies/kyverno-restrict-nri-socket.yaml` reference + 2 sentences on what it blocks
- "Projected-SA security gains" — extract from `projected-sa.md` "Security gains" section
- "Cache file posture" — extract from `nri-mode.md` "Trust posture" section
- "Audit trail" — 1 paragraph: in projected mode the Vault audit log shows per-pod SA, in legacy mode only the injector SA
- !!! danger box: "NRI DaemonSet runs as root" — extract from current `README.md` §3.5

#### `operators/monitoring.md`

- Audience tag (`Platform operator`)
- 3 sub-sections fused from the existing monitoring/ tree:
  - **Prometheus** — full v3.0 metric table from current `monitoring/prometheus.md` (preserve exactly)
  - **Grafana** — link to `monitoring/dashboard.json` in repo, 2-line install instructions, screenshot reference
  - **Alertmanager** — full v3.0 alert rules from current `monitoring/alertmanager.md` (preserve exactly, but check metric names are `vdbi_*`)
- !!! note: v2.x users — see [migration](migration-v2-to-v3.md) for the metric rename

#### `operators/operations.md`

- Audience tag (`Platform operator`)
- 3 sub-sections:
  - **Leader election** — extract from `how-it-works/leaderelection.md`. Mention `vdbi_is_leader` metric.
  - **Health checks** — extract from `how-it-works/healthcheck.md`. Document `/healthz` and `/readyz`.
  - **Multi-release on one cluster** — extract from `nri-mode.md` "Multiple injector releases" section: distinct `nri.pluginIndex`, distinct `webhookMatchLabels`

#### `operators/legacy-webhook-mode.md`

- Audience tag (`Platform operator`)
- !!! warning "Legacy mode" — at top: "This page documents the v2.x behavior preserved in v3.0 for backward compatibility. New deployments should follow [Getting Started](../getting-started/overview.md) instead."
- "When to keep this mode" — 3 bullet conditions (no NRI runtime, gradual migration, etc.)
- "Configuration" — extract the legacy injector/renewer/revoker config YAML from `how-it-works/configuration.md`
- "Vault policies" — extract `vault-roles-and-policies.md` §1 (legacy mode policy + role) verbatim
- "Annotations" — same as canonical (link to `developers/annotations.md`)
- "Limitations" — 4 bullets: cleartext in PodSpec, broad injector blast radius, shared SA for renewer/revoker, no native pod attestation
- Link forward: [migration to v3.0 + projected-SA](migration-v2-to-v3.md)

#### `operators/migration-v2-to-v3.md`

- **Move the existing file content essentially verbatim** from `docs/how-it-works/migration-v2-to-v3.md`. Update internal links:
  - `docs/how-it-works/projected-sa.md` → `../getting-started/vault-policies.md`
  - `docs/how-it-works/nri-mode.md` → `security.md`
  - `vault-roles-and-policies.md` → `../getting-started/vault-policies.md`
  - `docs/monitoring/prometheus.md` → `monitoring.md`
  - `docs/monitoring/alertmanager.md` → `monitoring.md`
- Audience tag (`Platform operator`)

### Phase 3 execution

- [ ] **Step 1: Write all 7 operator pages**
- [ ] **Step 2: Run humanizer on each page**
- [ ] **Step 3: `mkdocs build --strict`** — passes
- [ ] **Step 4: Spot-check `mkdocs serve` for Operators section**
- [ ] **Step 5: Commit**

```bash
git add docs/operators/
git commit -m "$(cat <<'EOF'
docs: phase 3 — Operators section

Seven pages covering architecture, components, security, monitoring,
operations, legacy webhook mode, and v2→v3 migration. Content
consolidated from the legacy how-it-works/ and monitoring/ trees with
two structural changes:
- monitoring's three pages merged into one
- leader election + healthchecks + multi-release notes merged into operations.md
- legacy webhook mode is now flagged as deprecated and linked from
  the canonical Getting Started path as the fallback option

Phase 3 of the docs overhaul.
Refs: .planning/specs/2026-05-05-docs-overhaul-design.md
EOF
)"
```

---

## Phase 4: Developers (English)

**Files (overwrite stubs):**
- `docs/developers/annotations.md`
- `docs/developers/injection-modes.md`
- `docs/developers/examples.md`
- `docs/developers/troubleshooting.md`

**Source material:**
- `docs/getting-started/getting-started.md` (Deploy an example application section)
- `docs/how-it-works/how-it-work.md` (Usage section, annotation list)
- `docs/how-it-works/nri-mode.md` (annotation list, what the user sees)
- `docs/how-it-works/projected-sa.md` (Troubleshooting table)

### Per-page content contract

#### `developers/annotations.md`

- Audience tag (`Application developer`)
- "Required label": `vault-db-injector: "true"` on the pod
- "Required annotations": `cluster`, `<dbname>.role`, `<dbname>.mode`
- "Optional annotations": `<dbname>.env-key-dbuser`, `<dbname>.env-key-dbpassword`, `<dbname>.env-key-uri`, `<dbname>.template`, `<dbname>.uuid` (auto-generated, do not set manually)
- Full table — one row per annotation:

| Annotation | Required? | Default | Purpose |
|---|---|---|---|
| (label) `vault-db-injector` | yes | — | Selects the pod for admission |
| `db-creds-injector.numberly.io/cluster` | optional | `database` | Vault DB engine mount |
| `db-creds-injector.numberly.io/<dbname>.role` | yes | — | Vault DB role to issue creds from |
| `db-creds-injector.numberly.io/<dbname>.mode` | yes | `classic` | `classic` or `uri` |
| `db-creds-injector.numberly.io/<dbname>.env-key-dbuser` | optional | `DBUSER` | Env var name for the username (classic) |
| `db-creds-injector.numberly.io/<dbname>.env-key-dbpassword` | optional | `DBPASSWORD` | Env var name for the password (classic) |
| `db-creds-injector.numberly.io/<dbname>.env-key-uri` | required if `mode=uri` | — | Env var name for the URI |
| `db-creds-injector.numberly.io/<dbname>.template` | required if `mode=uri` | — | URI template, `{{user}}` and `{{password}}` substituted |
| `db-creds-injector.numberly.io/<dbname>.uuid` | auto-set | — | Per-dbConfig UUID, set by the webhook. Do not write manually |

- "Multi-DB" — call out that the `<dbname>` segment is arbitrary; same pod can carry multiple `<dbname>.*` groups (cross-link to `examples.md`)

#### `developers/injection-modes.md`

- Audience tag (`Application developer`)
- 1-paragraph explainer of why this page exists: as a developer, you don't choose the mode — the operator does — but knowing what your env looks like at runtime helps debugging
- 2 sub-sections:
  - **Webhook mode (legacy)** — env vars are filled with the credential strings before the pod starts. `kubectl get pod -o yaml` shows them in plaintext.
  - **NRI mode (canonical)** — env vars are filled with placeholder strings of the form `__VDBI_PH_<64hex>___` in PodSpec; the NRI plugin substitutes the real values at container creation, before runc. `kubectl get pod -o yaml` shows the placeholder; `kubectl exec -- env` inside the running container shows the real value
- "How to tell which mode" — 1-line: `kubectl get pod -o yaml | grep VDBI_PH` — match means NRI

#### `developers/examples.md`

- Audience tag (`Application developer`)
- 3 worked examples — full Pod YAML in each:
  1. **Classic mode**, single Postgres DB (split user/password env vars)
  2. **URI mode**, single Postgres DB (single env var with the connection URI)
  3. **Multi-DB**, two databases (one classic, one URI)
- For each, include:
  - The full annotated Pod manifest
  - 2-line description of what the app sees in env

(Examples extracted from `how-it-works/how-it-work.md` §1.7, but cleaned up to follow the canonical naming `<dbname>.role` + `vault-db-injector: "true"` label.)

#### `developers/troubleshooting.md`

- Audience tag (`Application developer`)
- "Pod starts but cannot connect to DB" — 5-row table of (symptom, likely cause, what to check)
- "Pod env contains `__VDBI_PH_...`" — likely cause: NRI plugin missing on the node or pod started outside the configured matchLabels namespace
- "Pod stuck in `ContainerCreating` after admission" — likely cause: Vault login failed at NRI substitution; the operator should check `vdbi_nri_unwrap_failures_total{reason}`
- Cross-link to `operators/monitoring.md` for the metric names referenced

### Phase 4 execution

- [ ] **Step 1: Write all 4 developer pages**
- [ ] **Step 2: Run humanizer on each**
- [ ] **Step 3: `mkdocs build --strict`** — passes
- [ ] **Step 4: Spot-check `mkdocs serve`**
- [ ] **Step 5: Commit**

```bash
git add docs/developers/
git commit -m "$(cat <<'EOF'
docs: phase 4 — Developers section

Four pages aimed at application developers consuming injected
credentials: annotation reference, runtime view per injection mode,
worked examples (classic / URI / multi-DB), and app-side troubleshooting
with cross-links to operator-side metrics.

Phase 4 of the docs overhaul.
Refs: .planning/specs/2026-05-05-docs-overhaul-design.md
EOF
)"
```

---

## Phase 5: Contributors + Reference (English)

**Files (overwrite stubs):**
- `docs/contributors/build-from-source.md`
- `docs/contributors/contributing.md`
- `docs/contributors/comparison.md`
- `docs/contributors/architecture-deep-dive.md`
- `docs/reference/glossary.md`
- `docs/reference/configuration.md`
- `docs/reference/helm-values.md`
- `docs/reference/metrics.md`

**Source material:**
- `docs/getting-started/build.md` → build-from-source
- `docs/getting-started/contributing.md` → contributing (note: the file is empty, content lives in `CONTRIBUTING.md` at repo root)
- `CONTRIBUTING.md` → contributing
- `docs/getting-started/comparison.md` → comparison
- `docs/getting-started/getting-started.md` (Vocabulary section) → glossary
- `docs/how-it-works/configuration.md` + `helm/values.yml` comments → reference/configuration
- `helm/values.yml` → reference/helm-values
- `docs/monitoring/prometheus.md` + `docs/how-it-works/migration-v2-to-v3.md` (metric mapping) → reference/metrics

### Per-page content contract

#### `contributors/build-from-source.md`

- Audience tag (`Contributor`)
- "Requirements": Go ≥ 1.22, `make`, Docker (for image build), `kind` or another local cluster (for integration testing)
- "Clone & build":
```bash
git clone https://github.com/numberly/vault-db-injector
cd vault-db-injector
make setup
make
```
- "Run tests": `make test`, `make integration`
- "Build image": `docker build -t vault-db-injector:dev .`
- "Local end-to-end with kind" — 5-line script

#### `contributors/contributing.md`

- Audience tag (`Contributor`)
- 2-paragraph extract from `CONTRIBUTING.md` (issues, PRs, code review process)
- "Code of Conduct" — link to `CODE_OF_CONDUCT.md`
- "Coding standards" — bullet list (gofmt, golangci-lint, error wrapping with `cockroachdb/errors`, conventional commits)
- "Internal planning artifacts" — note that `.planning/` contains design specs and execution plans for in-flight work; contributors are encouraged to read them before submitting major changes

#### `contributors/comparison.md`

- Audience tag (`Anyone`)
- Move the existing comparison table from `docs/getting-started/comparison.md` essentially verbatim
- Add 1-paragraph "Why this comparison sits under Contributors" — readers evaluating tools have already moved past Getting Started

#### `contributors/architecture-deep-dive.md`

- Audience tag (`Contributor`)
- "Package layout" — list of `pkg/` subpackages with 1-line role each:
  - `pkg/injector` — webhook server, admission logic
  - `pkg/nri` — NRI plugin, container creation hook, cache
  - `pkg/renewer` — periodic renewer
  - `pkg/revoker` — pod-watch + safety-net revoker
  - `pkg/vault` — Vault client wrapper, projected-SA login, KV bookkeeping
  - `pkg/k8s` — K8s client init, annotation parsing
  - `pkg/leadership` — leader election
  - `pkg/healthcheck` — `/healthz`, `/readyz`
  - `pkg/metrics` — Prometheus registry, `vdbi_*` definitions
- "Key flows" — 3 ASCII diagrams (admission, NRI substitution, revocation)
- Cross-link to `operators/architecture.md` for the operator-level overview

#### `reference/glossary.md`

- Audience tag (`Anyone`)
- Vocabulary table — extract from `getting-started/getting-started.md` §2 and expand:

| Term | Definition |
|---|---|
| KV mount | Vault KV-v2 mount that holds per-pod bookkeeping (lease ID, token ID, namespace, UUID). Helm: `vaultSecretName`. |
| Vault auth path | Mount path of the Kubernetes auth method on Vault. Helm: `vaultAuthPath`. |
| Injector role | Vault role used by the injector binary to log in. Helm: `kubeRole`. |
| Database backend | Vault `database` secrets engine that issues dynamic credentials. |
| Database connection | Per-DB-server config under `database/config/<name>`. |
| Database role | Vault role under `database/roles/<name>` that issues creds for a given app. |
| App role | Vault auth/kubernetes role bound to an app's ServiceAccount. |
| `token_period` | Vault role attribute that makes the token periodically renewable past `token_max_ttl`. Mandatory in projected-SA mode. |
| Projected-SA | Authentication mode where the injector forges a per-pod TokenRequest JWT and uses it to log into Vault on behalf of the pod. |
| NRI mode | Credential delivery mode where placeholders in the PodSpec are substituted at container creation by a node-local NRI plugin. |
| Placeholder | Opaque string `__VDBI_PH_<64hex>___` injected by the webhook in NRI mode. |
| Bookkeeping token | The injector's own Vault token used to write per-pod metadata to the KV mount. |
| Pod-token | The per-pod Vault token issued via TokenRequest in projected-SA mode. |
| Lease | Vault lease covering one set of dynamic DB credentials. |
| Orphan token | Vault token with no parent — used in legacy mode for per-pod creds so revoking the parent doesn't cascade. |

#### `reference/configuration.md`

- Audience tag (`Platform operator`, `Contributor`)
- "Binary modes" — explain the 3 modes selected by `--config` flag pointing at one of injector/renewer/revoker config YAML files
- Full configuration key reference — table:

| Key | Type | Default | Used by | Purpose |
|---|---|---|---|---|
| `vaultAddress` | string | — | all | Vault/OpenBao URL |
| `vaultAuthPath` | string | `kubernetes` | all | k8s auth mount path |
| `kubeRole` | string | — | all | Default Vault role for binary login |
| `kubeRoleNri` | string | (falls back to kubeRole) | NRI plugin | Override |
| `kubeRoleRenewer` | string | (falls back to kubeRole) | renewer | Override |
| `kubeRoleRevoker` | string | (falls back to kubeRole) | revoker | Override |
| `tokenTTL` | duration | `8766h` | injector | Periodic token TTL |
| `vaultSecretName` | string | `vault-injector` | all | KV-v2 mount name |
| `vaultSecretPrefix` | string | `kubernetes` | all | Path prefix inside KV |
| `useProjectedSA` | bool | `false` | injector, NRI | Switch to projected-SA mode |
| `tokenRequestAudiences` | []string | `[]` | injector, NRI | TokenRequest audiences |
| `tokenRequestExpirationSeconds` | int | `600` | injector, NRI | TokenRequest JWT lifetime |
| `injectorLabel` | string | `vault-db-injector` | injector | Pod selector label |
| `webhookMatchLabels` | string | `vault-db-injector` | injector | Webhook objectSelector value |
| `mode` | string | — | all | `injector`/`renewer`/`revoker` |
| `sentry` | bool | `false` | all | Enable Sentry |
| `sentryDsn` | string | — | all | Sentry DSN |
| `logLevel` | string | `info` | all | logrus level |
| `SyncTTLSecond` | int | `300` | renewer | Sync interval |

- !!! warning: with `useProjectedSA: true`, `tokenRequestAudiences` MUST be non-empty; the binary refuses to start otherwise

#### `reference/helm-values.md`

- Audience tag (`Platform operator`)
- Full canonical `values.yml` walkthrough, key by key
- Source: `helm/values.yml` (preserve every documented comment as text in this page)
- Group by section: `vaultDbInjector.configuration.*`, `vaultDbInjector.{injector,renewer,revoker}.*`, `nri.*`
- Example minimal values for the canonical NRI + Projected-SA install (mirror the `--set` chain from `getting-started/install-injector.md`)

#### `reference/metrics.md`

- Audience tag (`Platform operator`)
- Full table merging `monitoring/prometheus.md` (current state) and v3.0 additions
- Group by category:
  - Token / lease lifecycle: `vdbi_renew_*`, `vdbi_revoke_*`, `vdbi_token_expiration`, `vdbi_lease_expiration`
  - Pod admission: `vdbi_mutated_pods_*`, `vdbi_fetch_pods_*`, `vdbi_orphan_ticket_created_*`
  - KV bookkeeping: `vdbi_store_data_*`, `vdbi_delete_data_*`
  - Authorization: `vdbi_service_account_authorized_count`, `vdbi_service_account_denied_count`
  - Synchronization: `vdbi_synchronization_*`, `vdbi_pod_cleanup_*`, `vdbi_last_synchronization_*`
  - Connectivity: `vdbi_connect_vault_*`
  - Leader election: `vdbi_is_leader`, `vdbi_leader_election_*`
  - NRI mode: `vdbi_nri_substitutions_total`, `vdbi_nri_unwrap_failures_total{reason}`, `vdbi_nri_resolve_duplicate_total`
  - Projected-SA: `vdbi_token_request_errors_total{reason}`, `vdbi_vault_login_errors_total{reason,auth_mode}`, `vdbi_projected_role_misconfigured_total{role}`
- !!! note: v2.x users see [migration §B1](../operators/migration-v2-to-v3.md#b1-metric-names---vault_injector_--vdbi_) for the v2→v3 rename mapping

### Phase 5 execution

- [ ] **Step 1: Write all 8 pages**
- [ ] **Step 2: Run humanizer on each**
- [ ] **Step 3: `mkdocs build --strict`** — passes
- [ ] **Step 4: Spot-check `mkdocs serve`**
- [ ] **Step 5: End-to-end manual review** — read the full Getting Started path top to bottom, confirm a fresh reader could follow it
- [ ] **Step 6: Commit**

```bash
git add docs/contributors/ docs/reference/
git commit -m "$(cat <<'EOF'
docs: phase 5 — Contributors and Reference sections

Contributors: build-from-source, contributing, project comparison,
architecture deep-dive at the package level. Reference: glossary,
binary configuration, full Helm values walkthrough, complete metrics
catalog grouped by category. Comparison page moved out of the
Getting Started persona-mismatched location into Contributors.

Phase 5 of the docs overhaul. End-to-end review of the Getting Started
path performed against a real cluster.
Refs: .planning/specs/2026-05-05-docs-overhaul-design.md
EOF
)"
```

---

## Phase 6: Cleanup — delete legacy tree

**Files:**
- Delete: `docs/how-it-works/` (entire directory)
- Delete: `docs/monitoring/` (entire directory)
- Delete: `docs/getting-started/getting-started.md`
- Delete: `docs/getting-started/build.md`
- Delete: `docs/getting-started/build.fr.md`
- Delete: `docs/getting-started/comparison.md`
- Delete: `docs/getting-started/contributing.md`
- Modify: `README.md` (refresh links to new paths)

- [ ] **Step 1: Verify no new page links to a legacy path**

```bash
grep -rE "(how-it-works|monitoring|getting-started/(getting-started|build|build\.fr|comparison|contributing))\.md" docs/ --include="*.md" \
  | grep -v "^docs/how-it-works\|^docs/monitoring" \
  || echo "OK: no new page links to legacy paths"
```

If any match: fix the offending link before deleting (rerun phase 2/3/4/5 agent for the affected page).

- [ ] **Step 2: Delete legacy files**

```bash
git rm -r docs/how-it-works docs/monitoring
git rm docs/getting-started/getting-started.md
git rm docs/getting-started/build.md
git rm docs/getting-started/build.fr.md
git rm docs/getting-started/comparison.md
git rm docs/getting-started/contributing.md
```

- [ ] **Step 3: Refresh `README.md` links**

In `README.md`, replace:

- `docs/how-it-works/nri-mode.md` → `docs/operators/security.md`

(That's the only legacy doc-tree path currently referenced. Verify with `grep -E "docs/(how-it-works|monitoring)" README.md` — fix any other matches identically.)

Also confirm `https://numberly.github.io/vault-db-injector` external link still resolves (it points at the published site, not a file path — leave it alone).

- [ ] **Step 4: Verify `mkdocs build --strict` is clean**

```bash
mkdocs build --strict
```

Expected: succeeds with zero broken-link warnings.

- [ ] **Step 5: Manual scan of the rendered site**

```bash
mkdocs serve
```

Walk every section in the navigation. Confirm no 404, no orphan page.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "$(cat <<'EOF'
docs: phase 6 — delete legacy how-it-works/, monitoring/, and merged GS pages

Final cleanup of the documentation overhaul. The how-it-works/ tree
(14 files), monitoring/ tree (3 files), and the four legacy
getting-started pages (getting-started.md, build.md, build.fr.md,
comparison.md, contributing.md) are deleted. All content has been
absorbed into the persona-oriented sections in phases 2–5. README.md
links updated to the new paths.

mkdocs build --strict passes with zero warnings.

Phase 6 of the docs overhaul.
Refs: .planning/specs/2026-05-05-docs-overhaul-design.md
EOF
)"
```

---

## Phase 7: French mirror

**Files:** for every EN page committed in phases 1–5, produce a sibling `<page>.fr.md` translation.

The full list (28 pages):

```
docs/index.fr.md
docs/getting-started/{overview,prerequisites,setup-kubernetes,setup-vault,setup-database,vault-policies,install-injector,first-injected-pod}.fr.md
docs/developers/{annotations,injection-modes,examples,troubleshooting}.fr.md
docs/operators/{architecture,components,security,monitoring,operations,legacy-webhook-mode,migration-v2-to-v3}.fr.md
docs/contributors/{build-from-source,contributing,comparison,architecture-deep-dive}.fr.md
docs/reference/{glossary,configuration,helm-values,metrics}.fr.md
```

### Translation rules

- Translate prose, headings, and admonition titles
- **Do NOT translate**: code blocks, CLI commands, file paths, config keys, metric names, annotation names, env var names
- **Do NOT translate**: the `**Audience:**` tag value (use the FR persona names from `nav_translations` — "Développeur d'application", "Opérateur de plateforme", "Contributeur", "Tout le monde")
- Preserve all internal links unchanged (mkdocs i18n resolves them per locale)
- Preserve admonition syntax (`!!! warning`, `!!! note`, `!!! danger`)

### Phase 7 execution

- [ ] **Step 1: Translate all 28 pages**

The agent dispatched for this phase produces all 28 `.fr.md` files in one pass. Source = the EN `.md` of the same name committed in phase 6.

- [ ] **Step 2: Run humanizer over each FR page**

The humanizer skill detects AI-writing patterns. It works on French text. Apply suggested edits inline.

- [ ] **Step 3: Verify `mkdocs build --strict` succeeds for both locales**

```bash
mkdocs build --strict
ls site/fr/getting-started/overview/ 2>/dev/null && echo "FR build present"
ls site/getting-started/overview/   2>/dev/null && echo "EN build present"
```

Expected: both directories exist; no warnings.

- [ ] **Step 4: Spot-check the FR site**

```bash
mkdocs serve
```

Open `http://127.0.0.1:8000/fr/`. Walk Home → Démarrage rapide → Pour les opérateurs. Confirm:
- French prose throughout
- Code blocks unchanged
- Internal links navigate to FR pages, not EN

- [ ] **Step 5: Commit**

```bash
git add docs/
git commit -m "$(cat <<'EOF'
docs: phase 7 — French mirror of the entire site

28 pages translated to French as <page>.fr.md siblings of every English
page produced in phases 1–6. Code blocks, CLI commands, file paths,
config keys, metric names, annotation names, and env var names left
untranslated. Internal links preserved unchanged for mkdocs i18n
resolution.

mkdocs build --strict passes for both locales.

Phase 7 of the docs overhaul. The overhaul is complete.
Refs: .planning/specs/2026-05-05-docs-overhaul-design.md
EOF
)"
```

---

## Final verification (post-phase-7)

After phase 7 commits, run:

```bash
mkdocs build --strict
git log --oneline | head -10
```

Expected: 8 commits matching the 8 phases, build clean. The branch is ready to merge.
