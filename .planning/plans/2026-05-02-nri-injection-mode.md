# NRI Injection Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the eBPF `probe_write_user` substitution layer with a containerd/CRI-O NRI plugin that mutates container env in `CreateContainer`, fixing URI-mode and removing all kernel coupling.

**Architecture:** Webhook keeps minting placeholders + Vault response-wrap tokens. A node-local DaemonSet runs an NRI plugin that, on `CreateContainer`, reads pod-sandbox annotations, unwraps the token via Vault, and returns a `ContainerAdjustment` with substituted env vars before runc receives the spec.

**Tech Stack:** Go 1.26, `github.com/containerd/nri` v0.9+, existing `pkg/vault`, existing `pkg/k8smutator`, Helm, k3d (containerd ≥ 2.0 with NRI enabled).

**Branch:** `feat/ebpf-injection-mode` (kept by user request — name stale but commits stay together).

---

## File Structure

**Removed**
- `pkg/bpf/` (entire directory)
- `pkg/controller/runbpf_linux.go`, `pkg/controller/runbpf_other.go`
- `helm/templates/daemonset-bpf.yaml`
- `Makefile` BPF targets
- `.github/workflows/bpf-integration.yml`

**Added**
- `pkg/nri/plugin.go` — implements `nri.Plugin` interface, handles `Synchronize` / `RunPodSandbox` / `CreateContainer` / `RemovePodSandbox`. Holds a `cache` map keyed by pod UID (mapping → unwrap result). Stateless across plugin restarts.
- `pkg/nri/substitute.go` — pure-Go substitution: takes `[]nriapi.KeyValue` env + `placeholder→value` map, returns updated env. No I/O. No goroutines.
- `pkg/nri/vault.go` — wrap-token unwrap helper using existing `pkg/vault.Connector`.
- `pkg/nri/runner.go` — `Run(ctx, cfg, log) error` entrypoint registering the plugin with containerd.
- `pkg/nri/substitute_test.go`, `pkg/nri/plugin_test.go` — unit tests.
- `pkg/controller/runnri.go` — non-build-tagged (NRI works on Linux + macOS containerd setups; Linux is what matters in prod).
- `helm/templates/daemonset-nri.yaml` — replacement DaemonSet.
- `helm/templates/configmap-nri.yaml` — split from `configmaps.yaml` for clarity (the BPF block in `configmaps.yaml` becomes an NRI block).

**Modified**
- `pkg/k8s/parse_annotations.go` — `ANNOTATION_BPF_MAPPING` → `ANNOTATION_NRI_MAPPING`, `BPFMapping` type → `NRIMapping`.
- `pkg/config/config.go` — `ModeBPF` → `ModeNRI`, `BPFConfig` → `NRIConfig` (drop `TmpfsPath` and `MaxMappingsPerNode`, add `SocketPath`).
- `pkg/k8smutator/k8smutator.go` — drop length check on placeholder values (URI mode), rename `wrapAndAnnotate` body to use NRI annotation.
- `pkg/k8smutator/k8smutator_test.go` — assertion updates for new annotation name and dropped length check.
- `pkg/controller/controller.go` — `RunBPF` → `RunNRI`, calls `runNRIAgent`.
- `main.go` — mode switch case `ModeBPF` → `ModeNRI`, `cfg.BPF.Enabled` → `cfg.NRI.Enabled`.
- `helm/values.yml` — `bpf:` block → `nri:` block.
- `helm/templates/configmaps.yaml` — same rename.
- `Makefile` — remove BPF targets, add no-op so `make build` still works.

---

## Task 1: Wipe BPF code

**Files:**
- Delete: `pkg/bpf/` (entire directory)
- Delete: `pkg/controller/runbpf_linux.go`
- Delete: `pkg/controller/runbpf_other.go`
- Delete: `helm/templates/daemonset-bpf.yaml`
- Delete: `.github/workflows/bpf-integration.yml`

- [ ] **Step 1: Remove BPF source tree and DaemonSet template**

```bash
git rm -r pkg/bpf/
git rm pkg/controller/runbpf_linux.go pkg/controller/runbpf_other.go
git rm helm/templates/daemonset-bpf.yaml
git rm .github/workflows/bpf-integration.yml
```

- [ ] **Step 2: Strip BPF Makefile targets**

Edit `Makefile`. Delete lines that mention `bpf` (BPF_LIBBPF_INCLUDE assignment, `bpf-headers`, `build-bpf`, `integration-test-bpf`, `verify-bpf-object` targets and their bodies — roughly lines 42-90). Keep `build`, `test`, `lint`, anything not BPF.

- [ ] **Step 3: Verify build still compiles (it won't yet — controller.go references runBPFAgent)**

Run: `go build ./...`
Expected: FAIL with `undefined: runBPFAgent` in `pkg/controller/controller.go`.

This proves the next task is needed. Do not "fix" by importing missing symbols — Task 2 deletes the references properly.

- [ ] **Step 4: Commit**

```bash
git commit -m "refactor: remove BPF substitution layer

Pivot to NRI plugin. The probe_write_user approach can't do variable-length
writes, breaking URI/DSN-mode envs. NRI plugin replaces it cleanly.

This commit only removes BPF code. The controller and webhook still
reference BPF symbols and will be migrated in subsequent commits."
```

---

## Task 2: Rename BPF → NRI in `pkg/k8s` and `pkg/config`

**Files:**
- Modify: `pkg/k8s/parse_annotations.go:19` and the `BPFMapping` type below it
- Modify: `pkg/config/config.go:21` (Mode constant), `:25-34` (BPFConfig struct), `:55` (Config field), `:78-82` (default), `:114,121-123` (validation)

- [ ] **Step 1: Rename annotation and mapping type**

Edit `pkg/k8s/parse_annotations.go`. Replace line 19:
```go
ANNOTATION_NRI_MAPPING string = "db-creds-injector.numberly.io/nri-mapping"
```

Find the `BPFMapping` struct (somewhere after the constants — search the file). Rename the struct to `NRIMapping`. Search the rest of `pkg/k8s/` for any reference and update.

Run: `grep -rn "BPFMapping\|ANNOTATION_BPF" pkg/k8s/`
Expected: no matches.

- [ ] **Step 2: Rename config Mode and struct**

Edit `pkg/config/config.go`. Apply this exact diff:

```go
// Line 21 area:
ModeNRI Mode = "nri"  // was: ModeBPF Mode = "bpf"

// Lines 25-34 — replace BPFConfig with:
// NRIConfig holds the configuration for the NRI plugin credential layer.
// When Enabled is false, the webhook produces literal env values (legacy
// behavior). When true, the webhook wraps every credential and the NRI
// DaemonSet substitutes placeholders at CreateContainer time.
type NRIConfig struct {
    Enabled      bool          `yaml:"enabled" envconfig:"nri_enabled"`
    WrapTokenTTL time.Duration `yaml:"wrapTokenTTL" envconfig:"nri_wrap_token_ttl"`
    SocketPath   string        `yaml:"socketPath" envconfig:"nri_socket_path"`
}

// Line 55:
NRI NRIConfig `yaml:"nri" envconfig:"nri"`  // was: BPF BPFConfig

// Lines 78-82 default block:
NRI: NRIConfig{
    WrapTokenTTL: 5 * time.Minute,
    SocketPath:   "/var/run/nri/nri.sock",
},

// Line 114 validation message:
{cfg.Mode != ModeAll && cfg.Mode != ModeInjector && cfg.Mode != ModeRenewer && cfg.Mode != ModeRevoker && cfg.Mode != ModeNRI, "Wrong Mode : should be injector/renewer/revoker/nri/all"},

// Lines 122-123:
{cfg.Mode != ModeNRI && cfg.VaultSecretName == "", "no vaultSecretName specified"},
{cfg.Mode != ModeNRI && cfg.VaultSecretPrefix == "", "no vaultSecretPrefix specified"},
```

- [ ] **Step 3: Update mutator and consumers to use new names**

Edit `pkg/k8smutator/k8smutator.go`. Globally replace:
- `cfg.BPF.Enabled` → `cfg.NRI.Enabled`
- `cfg.BPF.WrapTokenTTL` → `cfg.NRI.WrapTokenTTL`
- `k8s.ANNOTATION_BPF_MAPPING` → `k8s.ANNOTATION_NRI_MAPPING`
- `k8s.BPFMapping` → `k8s.NRIMapping`
- function `annotateBPFMapping` → `annotateNRIMapping`
- variable name `bpfEnabled` → `nriEnabled`
- error string "BPF mode currently supports a single DbConfiguration per pod" → "NRI mode currently supports a single DbConfiguration per pod"

Edit `pkg/k8smutator/k8smutator_test.go`. Same renames.

Edit `main.go` lines 69, 76:
- `case config.ModeBPF:` → `case config.ModeNRI:`
- `runErr = c.RunBPF(ctx)` → `runErr = c.RunNRI(ctx)`
- `if cfg.BPF.Enabled {` → `if cfg.NRI.Enabled {`
- `g.Go(func() error { return c.RunBPF(gCtx) })` → `g.Go(func() error { return c.RunNRI(gCtx) })`

- [ ] **Step 4: Update controller.go RunBPF → RunNRI (interim, body still references runBPFAgent)**

Edit `pkg/controller/controller.go` lines 152-196. Replace the `RunBPF` method with:

```go
// RunNRI runs the binary as a node-local DaemonSet that registers an NRI
// plugin with containerd to substitute placeholders in container envs at
// CreateContainer time.
func (c *Controller) RunNRI(ctx context.Context) error {
    c.log.Info("Starting server in mode nri")
    if !c.Cfg.NRI.Enabled {
        c.log.Warn("RunNRI called but cfg.NRI.Enabled is false; idle until shutdown")
        <-ctx.Done()
        return ctx.Err()
    }

    hcService := healthcheck.NewService(c.Cfg)
    hcService.RegisterHandlers()
    metricsService := metrics.NewMetricsService()

    g, gCtx := errgroup.WithContext(ctx)

    g.Go(func() error {
        hcService.Start(gCtx, make(chan struct{}))
        return nil
    })

    g.Go(func() error {
        metricsService.RunMetrics()
        return nil
    })

    g.Go(func() error {
        err := runNRIAgent(gCtx, c.Cfg, c.log)
        if err != nil {
            c.log.Errorf("NRI agent terminated: %v", err)
        }
        return err
    })

    if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
        return err
    }
    return nil
}
```

- [ ] **Step 5: Stub `runNRIAgent` so build still works**

Create `pkg/controller/runnri.go`:

```go
package controller

import (
    "context"

    "github.com/numberly/vault-db-injector/pkg/config"
    "github.com/numberly/vault-db-injector/pkg/logger"
    "github.com/numberly/vault-db-injector/pkg/nri"
)

func runNRIAgent(ctx context.Context, cfg *config.Config, log logger.Logger) error {
    return nri.Run(ctx, cfg, log)
}
```

The `pkg/nri` package doesn't exist yet — Task 4 creates it. For now, comment-out the import and have the function return an error placeholder so the build passes:

```go
package controller

import (
    "context"

    "github.com/cockroachdb/errors"
    "github.com/numberly/vault-db-injector/pkg/config"
    "github.com/numberly/vault-db-injector/pkg/logger"
)

// Stub: pkg/nri is implemented in Task 4. This keeps the build green
// while the rename refactor lands as a discrete commit.
func runNRIAgent(ctx context.Context, cfg *config.Config, log logger.Logger) error {
    _ = cfg
    _ = log
    <-ctx.Done()
    return errors.New("NRI plugin not yet implemented")
}
```

- [ ] **Step 6: Verify build and tests**

Run: `go build ./...`
Expected: PASS.

Run: `go test ./pkg/k8smutator/... ./pkg/config/... ./pkg/k8s/...`
Expected: PASS (all old BPF assertions now pass with NRI names).

If `pkg/k8smutator/k8smutator_test.go` has explicit length-check assertions, mark them as expected to pass for now — Task 5 will drop the length check entirely. The rename should not break them.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: rename BPF → NRI in config, annotations, controller

Mechanical rename. RunNRI uses a stub agent that returns an error;
the real plugin lands in a follow-up commit. Build and tests stay green."
```

---

## Task 3: Add `containerd/nri` dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/containerd/nri@v0.9.0`
Expected: dependency added without conflict.

If conflict occurs (containerd version mismatch with k8s.io deps), pin to the version compatible with the k8s.io/api version already in go.mod. Try `v0.8.0` then `v0.7.0`.

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 3: Tidy**

Run: `go mod tidy`
Expected: PASS, go.sum updated.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add github.com/containerd/nri for NRI plugin"
```

---

## Task 4: NRI plugin — substitute logic (TDD)

**Files:**
- Create: `pkg/nri/substitute.go`
- Test: `pkg/nri/substitute_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/nri/substitute_test.go`:

```go
package nri

import (
    "testing"
)

func TestSubstitute_FullValue(t *testing.T) {
    env := []string{
        "FOO=bar",
        "DB_PASSWORD=__VDBI_PH_aaa___",
    }
    mapping := map[string]string{"__VDBI_PH_aaa___": "Sup3rPass"}
    out := Substitute(env, mapping)
    if out[0] != "FOO=bar" {
        t.Fatalf("non-placeholder env mutated: %q", out[0])
    }
    if out[1] != "DB_PASSWORD=Sup3rPass" {
        t.Fatalf("placeholder not substituted: %q", out[1])
    }
}

func TestSubstitute_URIEmbedded(t *testing.T) {
    env := []string{"DB_URI=postgres://alice:__VDBI_PH_xxx___@db:5432/x?sslmode=require"}
    mapping := map[string]string{"__VDBI_PH_xxx___": "Sup3rPass"}
    out := Substitute(env, mapping)
    want := "DB_URI=postgres://alice:Sup3rPass@db:5432/x?sslmode=require"
    if out[0] != want {
        t.Fatalf("URI mode failed:\n got: %q\nwant: %q", out[0], want)
    }
}

func TestSubstitute_MultiPlaceholder(t *testing.T) {
    env := []string{"DB_URI=postgres://__USER__:__PASS__@__HOST__/db"}
    mapping := map[string]string{
        "__USER__": "alice",
        "__PASS__": "Sup3rPass",
        "__HOST__": "db.example.com",
    }
    out := Substitute(env, mapping)
    want := "DB_URI=postgres://alice:Sup3rPass@db.example.com/db"
    if out[0] != want {
        t.Fatalf("multi-placeholder failed:\n got: %q\nwant: %q", out[0], want)
    }
}

func TestSubstitute_NoPlaceholder(t *testing.T) {
    env := []string{"FOO=bar", "BAZ=qux"}
    out := Substitute(env, map[string]string{"__VDBI_PH_xxx___": "value"})
    if len(out) != 2 || out[0] != "FOO=bar" || out[1] != "BAZ=qux" {
        t.Fatalf("env mutated when no placeholder present: %v", out)
    }
}

func TestSubstitute_EmptyEnv(t *testing.T) {
    out := Substitute(nil, map[string]string{"__a__": "b"})
    if len(out) != 0 {
        t.Fatalf("empty env became non-empty: %v", out)
    }
}

func TestSubstitute_EmptyMapping(t *testing.T) {
    env := []string{"FOO=bar"}
    out := Substitute(env, nil)
    if len(out) != 1 || out[0] != "FOO=bar" {
        t.Fatalf("env changed with nil mapping: %v", out)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/nri/...`
Expected: FAIL with "no Go files" or "package nri not found" or "undefined: Substitute".

- [ ] **Step 3: Write minimal implementation**

Create `pkg/nri/substitute.go`:

```go
// Package nri implements a containerd/CRI-O NRI plugin that substitutes
// credential placeholders in container env vars at CreateContainer time.
package nri

import "strings"

// Substitute returns a new env slice where every occurrence of any
// placeholder key in mapping is replaced by its value. Inputs are not
// mutated. Order is preserved.
func Substitute(env []string, mapping map[string]string) []string {
    if len(env) == 0 {
        return env
    }
    if len(mapping) == 0 {
        out := make([]string, len(env))
        copy(out, env)
        return out
    }
    out := make([]string, len(env))
    for i, e := range env {
        for ph, val := range mapping {
            if strings.Contains(e, ph) {
                e = strings.ReplaceAll(e, ph, val)
            }
        }
        out[i] = e
    }
    return out
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./pkg/nri/...`
Expected: PASS, 6/6 tests.

- [ ] **Step 5: Commit**

```bash
git add pkg/nri/substitute.go pkg/nri/substitute_test.go
git commit -m "feat(nri): substitute logic with URI/multi-placeholder support"
```

---

## Task 5: NRI plugin — Vault unwrap helper

**Files:**
- Create: `pkg/nri/vault.go`

- [ ] **Step 1: Write the unwrap helper**

Create `pkg/nri/vault.go`:

```go
package nri

import (
    "context"

    "github.com/cockroachdb/errors"
    "github.com/numberly/vault-db-injector/pkg/config"
    "github.com/numberly/vault-db-injector/pkg/k8s"
    "github.com/numberly/vault-db-injector/pkg/vault"
)

// unwrapAndBuildMapping unwraps the wrap token in the NRIMapping annotation
// and returns a placeholder→real-value map ready for Substitute.
//
// Maps the placeholder back to the real credential value by joining the
// placeholders[ph]=key lookup (in the annotation) with the unwrapped
// payload key→value.
func unwrapAndBuildMapping(ctx context.Context, cfg *config.Config, m k8s.NRIMapping) (map[string]string, error) {
    k8sClient := k8s.NewClient()
    tok, err := k8sClient.GetServiceAccountToken()
    if err != nil {
        return nil, errors.Wrap(err, "get serviceaccount token")
    }
    conn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, "", "", tok, cfg.VaultRateLimit)
    if err := conn.Login(ctx); err != nil {
        return nil, errors.Wrap(err, "vault login")
    }
    payload, err := conn.UnwrapValues(ctx, m.WrapToken)
    if err != nil {
        return nil, errors.Wrap(err, "vault unwrap")
    }
    out := make(map[string]string, len(m.Placeholders))
    for ph, key := range m.Placeholders {
        v, ok := payload[key]
        if !ok {
            return nil, errors.Newf("unwrap payload missing key %q", key)
        }
        out[ph] = v
    }
    return out, nil
}
```

- [ ] **Step 2: Check that `vault.UnwrapValues` exists**

Run: `grep -n "func.*UnwrapValues" pkg/vault/*.go`
Expected: function exists. If it does NOT exist, add it now alongside `WrapValues` in `pkg/vault/vault.go`:

```go
// UnwrapValues unwraps the given wrap token and returns the payload as a
// map[string]string. The token is single-use; success consumes it.
func (c *Connector) UnwrapValues(ctx context.Context, wrapToken string) (map[string]string, error) {
    secret, err := c.client.Logical().UnwrapWithContext(ctx, wrapToken)
    if err != nil {
        return nil, errors.Wrap(err, "vault unwrap")
    }
    if secret == nil || secret.Data == nil {
        return nil, errors.New("vault unwrap returned no data")
    }
    out := make(map[string]string, len(secret.Data))
    for k, v := range secret.Data {
        s, ok := v.(string)
        if !ok {
            return nil, errors.Newf("unwrap field %q is not a string", k)
        }
        out[k] = s
    }
    return out, nil
}
```

If the existing BPF code already had an unwrap helper, port it; otherwise the version above is correct.

- [ ] **Step 3: Verify build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add pkg/nri/vault.go pkg/vault/vault.go
git commit -m "feat(nri): vault unwrap helper for NRI plugin"
```

---

## Task 6: NRI plugin — implementation (plugin.go + runner.go)

**Files:**
- Create: `pkg/nri/plugin.go`
- Create: `pkg/nri/runner.go`

- [ ] **Step 1: Write the plugin**

Create `pkg/nri/plugin.go`:

```go
package nri

import (
    "context"
    "encoding/json"
    "sync"

    "github.com/cockroachdb/errors"
    nriapi "github.com/containerd/nri/pkg/api"
    "github.com/containerd/nri/pkg/stub"
    "github.com/numberly/vault-db-injector/pkg/config"
    "github.com/numberly/vault-db-injector/pkg/k8s"
    "github.com/numberly/vault-db-injector/pkg/logger"
    "github.com/numberly/vault-db-injector/pkg/metrics"
)

type plugin struct {
    cfg *config.Config
    log logger.Logger

    mu    sync.Mutex
    cache map[string]map[string]string // pod UID → placeholder→value map
}

func newPlugin(cfg *config.Config, log logger.Logger) *plugin {
    return &plugin{
        cfg:   cfg,
        log:   log,
        cache: make(map[string]map[string]string),
    }
}

// Synchronize is called when the plugin connects/reconnects to containerd.
// Already-running containers cannot be mutated (envp is fixed post-execve);
// we only need to be ready for future CreateContainer events.
func (p *plugin) Synchronize(_ context.Context, _ []*nriapi.PodSandbox, _ []*nriapi.Container) ([]*nriapi.ContainerUpdate, error) {
    p.log.Info("NRI plugin synchronized with containerd")
    return nil, nil
}

// CreateContainer is the substitution hook. Reads the pod-sandbox annotation,
// unwraps the wrap-token (cached per pod), and emits a ContainerAdjustment
// with substituted env.
func (p *plugin) CreateContainer(ctx context.Context, pod *nriapi.PodSandbox, container *nriapi.Container) (*nriapi.ContainerAdjustment, []*nriapi.ContainerUpdate, error) {
    raw, ok := pod.Annotations[k8s.ANNOTATION_NRI_MAPPING]
    if !ok || raw == "" {
        return nil, nil, nil
    }
    var m k8s.NRIMapping
    if err := json.Unmarshal([]byte(raw), &m); err != nil {
        p.log.Warnf("malformed nri-mapping annotation on pod %s/%s: %v", pod.Namespace, pod.Name, err)
        metrics.NRIUnwrapFailures.WithLabelValues("malformed_annotation").Inc()
        return nil, nil, nil
    }

    mapping, err := p.resolveMapping(ctx, pod.Uid, m)
    if err != nil {
        p.log.Errorf("unwrap failed for pod %s/%s: %v", pod.Namespace, pod.Name, err)
        metrics.NRIUnwrapFailures.WithLabelValues("unwrap_error").Inc()
        return nil, nil, nil
    }

    inEnv := make([]string, len(container.Env))
    for i, kv := range container.Env {
        inEnv[i] = kv.Key + "=" + kv.Value
    }
    outEnv := Substitute(inEnv, mapping)

    adj := &nriapi.ContainerAdjustment{}
    for _, line := range outEnv {
        k, v, _ := splitKV(line)
        adj.AddEnv(k, v)
    }
    metrics.NRISubstitutionsTotal.WithLabelValues().Inc()
    return adj, nil, nil
}

func (p *plugin) RemovePodSandbox(_ context.Context, pod *nriapi.PodSandbox) error {
    p.mu.Lock()
    delete(p.cache, pod.Uid)
    p.mu.Unlock()
    return nil
}

// resolveMapping returns the placeholder→value map for a pod, using a
// per-pod cache so multiple containers in the same pod share one unwrap
// (the wrap-token is single-use).
func (p *plugin) resolveMapping(ctx context.Context, podUID string, m k8s.NRIMapping) (map[string]string, error) {
    p.mu.Lock()
    cached, ok := p.cache[podUID]
    p.mu.Unlock()
    if ok {
        return cached, nil
    }
    mapping, err := unwrapAndBuildMapping(ctx, p.cfg, m)
    if err != nil {
        return nil, err
    }
    p.mu.Lock()
    p.cache[podUID] = mapping
    p.mu.Unlock()
    return mapping, nil
}

func splitKV(line string) (string, string, bool) {
    for i := 0; i < len(line); i++ {
        if line[i] == '=' {
            return line[:i], line[i+1:], true
        }
    }
    return line, "", false
}

// stubFor wires the plugin into the NRI stub framework. Plugin name and
// index control NRI ordering when multiple plugins are registered.
func stubFor(p *plugin) (stub.Stub, error) {
    s, err := stub.New(p,
        stub.WithPluginName("vault-db-injector"),
        stub.WithPluginIdx("10"),
        stub.WithSocketPath(p.cfg.NRI.SocketPath),
    )
    if err != nil {
        return nil, errors.Wrap(err, "create NRI stub")
    }
    return s, nil
}
```

- [ ] **Step 2: Write the runner**

Create `pkg/nri/runner.go`:

```go
package nri

import (
    "context"

    "github.com/cockroachdb/errors"
    "github.com/numberly/vault-db-injector/pkg/config"
    "github.com/numberly/vault-db-injector/pkg/logger"
)

// Run registers the NRI plugin with containerd and blocks until ctx is
// cancelled or the plugin connection drops fatally.
func Run(ctx context.Context, cfg *config.Config, log logger.Logger) error {
    p := newPlugin(cfg, log)
    s, err := stubFor(p)
    if err != nil {
        return err
    }
    log.Infof("NRI plugin connecting on %s", cfg.NRI.SocketPath)
    if err := s.Run(ctx); err != nil {
        return errors.Wrap(err, "NRI plugin run loop")
    }
    return nil
}
```

- [ ] **Step 3: Add Prometheus metrics for NRI**

Edit `pkg/metrics/metrics.go` (or wherever the existing metrics live — `grep -rn "MutatedPodWithSuccessCount" pkg/metrics/` to find the file). Add:

```go
NRISubstitutionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "vdbi_nri_substitutions_total",
    Help: "Number of CreateContainer events where the NRI plugin emitted an env adjustment.",
}, []string{})

NRIUnwrapFailures = promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "vdbi_nri_unwrap_failures_total",
    Help: "Number of NRI plugin unwrap failures by reason.",
}, []string{"reason"})
```

If the package uses a different metrics-registration pattern, follow the existing pattern instead of `promauto`.

- [ ] **Step 4: Wire runner.go into controller (replace stub from Task 2)**

Edit `pkg/controller/runnri.go`. Replace stub with real call:

```go
package controller

import (
    "context"

    "github.com/numberly/vault-db-injector/pkg/config"
    "github.com/numberly/vault-db-injector/pkg/logger"
    "github.com/numberly/vault-db-injector/pkg/nri"
)

func runNRIAgent(ctx context.Context, cfg *config.Config, log logger.Logger) error {
    return nri.Run(ctx, cfg, log)
}
```

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/nri/ pkg/metrics/ pkg/controller/runnri.go
git commit -m "feat(nri): plugin implementation with per-pod unwrap cache

CreateContainer hook reads nri-mapping annotation, unwraps the wrap token
once per pod (cached), and substitutes placeholders in env via pure-Go
ReplaceAll. Variable-length safe; URI/multi-placeholder modes work
naturally."
```

---

## Task 7: NRI plugin — unit test for plugin event handling

**Files:**
- Test: `pkg/nri/plugin_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/nri/plugin_test.go`:

```go
package nri

import (
    "context"
    "encoding/json"
    "testing"

    nriapi "github.com/containerd/nri/pkg/api"
    "github.com/numberly/vault-db-injector/pkg/config"
    "github.com/numberly/vault-db-injector/pkg/k8s"
    "github.com/numberly/vault-db-injector/pkg/logger"
)

func TestCreateContainer_NoAnnotation(t *testing.T) {
    p := newPlugin(&config.Config{}, logger.GetLogger())
    pod := &nriapi.PodSandbox{Uid: "pod-1", Annotations: map[string]string{}}
    cont := &nriapi.Container{Env: []*nriapi.KeyValue{{Key: "FOO", Value: "bar"}}}
    adj, _, err := p.CreateContainer(context.Background(), pod, cont)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if adj != nil {
        t.Fatalf("expected nil adjustment when annotation absent, got %v", adj)
    }
}

func TestCreateContainer_MalformedAnnotation(t *testing.T) {
    p := newPlugin(&config.Config{}, logger.GetLogger())
    pod := &nriapi.PodSandbox{
        Uid: "pod-1",
        Annotations: map[string]string{
            k8s.ANNOTATION_NRI_MAPPING: "{not json",
        },
    }
    cont := &nriapi.Container{}
    adj, _, err := p.CreateContainer(context.Background(), pod, cont)
    if err != nil {
        t.Fatalf("expected nil error on malformed annotation, got %v", err)
    }
    if adj != nil {
        t.Fatalf("expected nil adjustment on malformed annotation, got %v", adj)
    }
}

func TestRemovePodSandbox_EvictsCache(t *testing.T) {
    p := newPlugin(&config.Config{}, logger.GetLogger())
    p.cache["pod-1"] = map[string]string{"a": "b"}
    pod := &nriapi.PodSandbox{Uid: "pod-1"}
    if err := p.RemovePodSandbox(context.Background(), pod); err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if _, exists := p.cache["pod-1"]; exists {
        t.Fatalf("cache entry not evicted")
    }
}

func TestSplitKV(t *testing.T) {
    cases := []struct{ in, k, v string }{
        {"FOO=bar", "FOO", "bar"},
        {"DB_URI=postgres://a:b@c/d", "DB_URI", "postgres://a:b@c/d"},
        {"BARE", "BARE", ""},
        {"=val", "", "val"},
    }
    for _, c := range cases {
        k, v, _ := splitKV(c.in)
        if k != c.k || v != c.v {
            t.Errorf("splitKV(%q) = (%q,%q), want (%q,%q)", c.in, k, v, c.k, c.v)
        }
    }
}

// Ensures the NRIMapping JSON shape is what the webhook produces.
func TestNRIMappingMarshal(t *testing.T) {
    m := k8s.NRIMapping{
        WrapToken: "hvs.xxxxx",
        Placeholders: map[string]string{
            "__VDBI_PH_aaa___": "username",
            "__VDBI_PH_bbb___": "password",
        },
    }
    b, err := json.Marshal(m)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    var back k8s.NRIMapping
    if err := json.Unmarshal(b, &back); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if back.WrapToken != m.WrapToken {
        t.Fatalf("wrap token mismatch")
    }
    if len(back.Placeholders) != 2 {
        t.Fatalf("placeholder count mismatch")
    }
}
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `go test ./pkg/nri/... -v`
Expected: PASS, all tests pass.

If `k8s.NRIMapping` does not yet have JSON tags, add them in `pkg/k8s/parse_annotations.go` near the type definition:

```go
type NRIMapping struct {
    WrapToken    string            `json:"wrap_token"`
    Placeholders map[string]string `json:"placeholders"`
}
```

(Match whatever shape the existing webhook writes. Re-run `pkg/k8smutator` tests to confirm.)

- [ ] **Step 3: Commit**

```bash
git add pkg/nri/plugin_test.go pkg/k8s/parse_annotations.go
git commit -m "test(nri): plugin event handling unit tests"
```

---

## Task 8: Drop length validation in webhook (URI mode unblock)

**Files:**
- Modify: `pkg/k8smutator/k8smutator.go:245-250`
- Modify: `pkg/k8smutator/k8smutator_test.go`

- [ ] **Step 1: Remove the length check**

Edit `pkg/k8smutator/k8smutator.go`. Find the block (renamed from `wrapAndAnnotate` — should still be the same function):

```go
if len(creds.Username) > placeholder.MaxValue {
    return "", "", errors.Newf("credential username length %d exceeds BPF maximum %d; ...", ...)
}
if len(creds.Password) > placeholder.MaxValue {
    return "", "", errors.Newf("credential password length %d exceeds BPF maximum %d; ...", ...)
}
```

**Delete both blocks entirely.** NRI has no length constraint.

- [ ] **Step 2: Update tests**

Edit `pkg/k8smutator/k8smutator_test.go`. Find any test that asserts an error on long credentials (search for `placeholder.MaxValue` or "credential username length" or "MaxValue"). Either delete the test or change it to assert that the long credential now flows through successfully.

Run: `grep -n "MaxValue\|exceeds" pkg/k8smutator/k8smutator_test.go`
Expected: no remaining matches after edits.

- [ ] **Step 3: Run tests**

Run: `go test ./pkg/k8smutator/...`
Expected: PASS.

- [ ] **Step 4: Drop the placeholder.MaxValue constant (now unused)**

Edit `pkg/placeholder/placeholder.go`. Delete the `MaxValue` const and its comment block. Update the package comment to mention NRI instead of BPF.

Run: `grep -rn "placeholder.MaxValue" .`
Expected: no matches.

Run: `go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/k8smutator/k8smutator.go pkg/k8smutator/k8smutator_test.go pkg/placeholder/placeholder.go
git commit -m "feat(webhook): drop placeholder-length cap (NRI supports any length)

URI/DSN-mode envs work now. Removes placeholder.MaxValue; webhook accepts
arbitrary-length credentials."
```

---

## Task 9: Helm — replace daemonset-bpf with daemonset-nri

**Files:**
- Create: `helm/templates/daemonset-nri.yaml`
- Modify: `helm/templates/configmaps.yaml`
- Modify: `helm/values.yml`

- [ ] **Step 1: Write the NRI DaemonSet template**

Create `helm/templates/daemonset-nri.yaml`:

```yaml
{{- if .Values.nri.enabled }}
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: {{ include "vault-db-injector.fullname" . }}-nri
  labels:
  {{- include "vault-db-injector.labels" . | nindent 4 }}
    app.kubernetes.io/component: nri
spec:
  selector:
    matchLabels:
      app: vault-db-nri
    {{- include "vault-db-injector.selectorLabels" . | nindent 6 }}
      app.kubernetes.io/component: nri
  template:
    metadata:
      labels:
        app: vault-db-nri
      {{- include "vault-db-injector.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: nri
      annotations:
        prometheus.io/port: "8080"
        prometheus.io/scrape: "true"
    spec:
      containers:
      - name: vault-db-nri
        image: "{{ .Values.nri.image.repository | default .Values.vaultDbInjector.injector.image.repository }}:{{ .Values.nri.image.tag | default .Values.vaultDbInjector.injector.image.tag | default .Chart.AppVersion }}"
        imagePullPolicy: {{ .Values.nri.imagePullPolicy | default "IfNotPresent" }}
        args:
        - "--config=/nri/config.yaml"
        env:
        - name: NODE_NAME
          valueFrom:
            fieldRef:
              fieldPath: spec.nodeName
        ports:
        - containerPort: 8080
          name: metrics
          protocol: TCP
        - containerPort: 8888
          name: healthcheck
          protocol: TCP
        resources:
          {{- toYaml .Values.nri.resources | nindent 10 }}
        securityContext:
          runAsNonRoot: true
          runAsUser: 65534
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: ["ALL"]
        volumeMounts:
        - name: config-nri
          mountPath: /nri
        - name: nri-socket
          mountPath: /var/run/nri
        livenessProbe:
          failureThreshold: 3
          httpGet:
            path: /healthz
            port: healthcheck
            scheme: HTTP
          initialDelaySeconds: 20
          periodSeconds: 60
          successThreshold: 1
          timeoutSeconds: 1
        readinessProbe:
          failureThreshold: 3
          httpGet:
            path: /readyz
            port: healthcheck
            scheme: HTTP
          periodSeconds: 10
          successThreshold: 1
          timeoutSeconds: 1
      imagePullSecrets:
      - name: registry-token
      serviceAccountName: {{ include "vault-db-injector.fullname" . }}
      volumes:
      - configMap:
          defaultMode: 420
          name: {{ include "vault-db-injector.fullname" . }}-nri
        name: config-nri
      # NRI socket: hostPath to containerd's NRI socket. Plugin connects
      # over this socket to register and receive lifecycle events.
      - name: nri-socket
        hostPath:
          path: /var/run/nri
          type: Directory
      {{- if .Values.nri.tolerations }}
      tolerations:
        {{- toYaml .Values.nri.tolerations | nindent 8 }}
      {{- end }}
      {{- if .Values.nri.nodeSelector }}
      nodeSelector:
        {{- toYaml .Values.nri.nodeSelector | nindent 8 }}
      {{- end }}
{{- end }}
```

- [ ] **Step 2: Update configmaps.yaml — replace BPF block with NRI**

Edit `helm/templates/configmaps.yaml`. Find the `{{- if .Values.bpf.enabled }}` block (line 54) and replace it entirely with:

```yaml
{{- if .Values.nri.enabled }}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "vault-db-injector.fullname" . }}-nri
  labels:
    {{- include "vault-db-injector.labels" . | nindent 4 }}
    app.kubernetes.io/component: nri
data:
  config.yaml: |
    mode: nri
    vaultAddress: {{ .Values.vaultDbInjector.vault.address | quote }}
    vaultAuthPath: {{ .Values.vaultDbInjector.vault.authPath | quote }}
    kubeRole: {{ .Values.vaultDbInjector.vault.kubeRole | quote }}
    logLevel: {{ .Values.vaultDbInjector.logLevel | default "info" | quote }}
    nri:
      enabled: true
      wrapTokenTTL: {{ .Values.nri.wrapTokenTTL }}
      socketPath: /var/run/nri/nri.sock
{{- end }}
```

(Match the exact yaml indentation and `vaultDbInjector.*` paths used in the BPF version — read the original block first, copy its structure, only swap names.)

- [ ] **Step 3: Update values.yml**

Edit `helm/values.yml`. Replace the `bpf:` block (lines 63-90) with:

```yaml
nri:
  # When true, deploys the NRI DaemonSet AND tells the injector to wrap
  # every credential it issues. Both pieces are tied to this single switch
  # so the cluster cannot end up in a "webhook produces placeholders but
  # nothing substitutes" state. When false, behavior is byte-identical to
  # legacy modes (literal credentials in env vars).
  #
  # Requires containerd >= 1.7 with NRI enabled in /etc/containerd/config.toml,
  # OR CRI-O >= 1.26.
  enabled: false
  image:
    repository: ""  # defaults to vaultDbInjector.injector.image.repository
    tag: ""         # defaults to vaultDbInjector.injector.image.tag
  imagePullPolicy: Always
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      cpu: 200m
      memory: 256Mi
  tolerations: []
  nodeSelector: {}
  # Vault response-wrapping TTL for the wrap tokens that travel through
  # the PodSpec. Must outlive the longest expected scheduling + image-pull
  # delay on the cluster.
  wrapTokenTTL: 5m
```

- [ ] **Step 4: Lint helm chart**

Run: `helm lint helm/`
Expected: PASS, no errors. Warnings about missing values are OK if they were present before.

- [ ] **Step 5: Render to verify shape**

Run: `helm template helm/ --set nri.enabled=true | grep -E "kind:|name:" | head -30`
Expected: shows `vault-db-injector-nri` DaemonSet and `-nri` ConfigMap.

Run: `helm template helm/ --set nri.enabled=false | grep -E "kind:|name:" | head -30`
Expected: NRI DaemonSet and configmap absent.

- [ ] **Step 6: Commit**

```bash
git add helm/
git commit -m "helm: replace daemonset-bpf with daemonset-nri

NRI DS runs non-root with all caps dropped, mounts /var/run/nri socket
hostPath instead of /sys/fs/bpf and friends. ConfigMap renamed; values
schema renamed bpf.* → nri.*."
```

---

## Task 10: Configure k3d cluster for NRI

**Files:**
- Modify or create: `../vault-db-injector-cnd/` (k3d setup directory) — create a config patch file
- New: `scripts/enable-nri-on-k3d.sh` (in this repo, for reproducibility)

- [ ] **Step 1: Inspect current k3d cluster's containerd config**

Run:
```bash
docker exec k3d-vault-db-test-server-0 cat /var/lib/rancher/k3s/agent/etc/containerd/config.toml | head -40
```
Expected: shows current containerd config. Note whether `[plugins."io.containerd.nri.v1.nri"]` block exists.

- [ ] **Step 2: Write enable script**

Create `scripts/enable-nri-on-k3d.sh`:

```bash
#!/usr/bin/env bash
# Enables NRI on every k3d node of the cluster passed as $1.
# Idempotent. Restarts containerd via SIGHUP after patching.
set -euo pipefail

CLUSTER="${1:-vault-db-test}"

for NODE in $(k3d node list --no-headers | awk -v c="$CLUSTER" '$2==c {print $1}'); do
  echo "Patching $NODE"
  docker exec "$NODE" sh -c '
    CONFIG=/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl
    [ -f "$CONFIG" ] || CONFIG=/var/lib/rancher/k3s/agent/etc/containerd/config.toml
    if ! grep -q "io.containerd.nri.v1.nri" "$CONFIG" 2>/dev/null; then
      cat >> "$CONFIG" <<EOF

[plugins."io.containerd.nri.v1.nri"]
  disable = false
  socket_path = "/var/run/nri/nri.sock"
  plugin_path = "/opt/nri/plugins"
  plugin_config_path = "/etc/nri/conf.d"
  plugin_registration_timeout = "5s"
  plugin_request_timeout = "2s"
EOF
      mkdir -p /var/run/nri
    fi
  '
  docker restart "$NODE"
done

echo "Waiting for cluster ready..."
kubectl wait --for=condition=Ready node --all --timeout=120s
```

Run: `chmod +x scripts/enable-nri-on-k3d.sh`

- [ ] **Step 3: Run the script against the existing cluster**

Run: `./scripts/enable-nri-on-k3d.sh vault-db-test`
Expected: each node patched, cluster comes back Ready within 2 minutes.

- [ ] **Step 4: Verify NRI socket present on a node**

Run: `docker exec k3d-vault-db-test-server-0 ls -l /var/run/nri/nri.sock`
Expected: socket exists.

- [ ] **Step 5: Commit**

```bash
git add scripts/enable-nri-on-k3d.sh
git commit -m "scripts: add enable-nri-on-k3d.sh helper for integration tests"
```

---

## Task 11: Build and deploy to k3d, run smoke test

**Files:** none modified — operational task.

- [ ] **Step 1: Build container image and import into k3d**

Run:
```bash
docker build -t vault-db-injector:nri-dev .
k3d image import vault-db-injector:nri-dev -c vault-db-test
```
Expected: image built and imported.

If build fails because of missing main entry, ensure `main.go` still references all four modes (`injector`, `renewer`, `revoker`, `nri`).

- [ ] **Step 2: Helm upgrade**

Run:
```bash
helm upgrade --install vault-db-injector helm/ \
  --namespace vault-db-injector --create-namespace \
  --set vaultDbInjector.injector.image.repository=vault-db-injector \
  --set vaultDbInjector.injector.image.tag=nri-dev \
  --set nri.enabled=true \
  --set nri.image.repository=vault-db-injector \
  --set nri.image.tag=nri-dev \
  -f helm/values.yml \
  --wait --timeout 5m
```
Expected: deployment + DaemonSet ready.

- [ ] **Step 3: Verify NRI plugin connected**

Run: `kubectl logs -n vault-db-injector -l app=vault-db-nri --tail=50`
Expected: log line "NRI plugin synchronized with containerd".

- [ ] **Step 4: Apply minimal smoke pod**

Create `/tmp/nri-smoke-pod.yaml`:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nri-smoke
  namespace: default
  annotations:
    db-creds-injector.numberly.io/role: "demo-role"
    db-creds-injector.numberly.io/cluster: "databases"
    db-creds-injector.numberly.io/demo.mode: "classic"
    db-creds-injector.numberly.io/demo.env-key-dbuser: "DB_USER"
    db-creds-injector.numberly.io/demo.env-key-dbpassword: "DB_PASSWORD"
spec:
  serviceAccountName: default
  restartPolicy: Never
  containers:
  - name: app
    image: busybox:latest
    command: ["sh", "-c", "env | grep DB_; sleep 30"]
```

Run: `kubectl apply -f /tmp/nri-smoke-pod.yaml`

- [ ] **Step 5: Confirm substitution happened**

Run: `kubectl logs nri-smoke`
Expected: `DB_USER=v-token-...`, `DB_PASSWORD=...real password...` (NOT `__VDBI_PH_...`).

Also: `kubectl get pod nri-smoke -o yaml | grep -A2 -E "DB_USER|DB_PASSWORD"`
Expected: spec env still shows placeholders. (etcd-clean.)

- [ ] **Step 6: Cleanup smoke pod**

Run: `kubectl delete pod nri-smoke`

If smoke fails: collect plugin logs, webhook logs, vault logs, decide whether issue is in plugin or env. Do not commit; iterate on code until smoke passes, then continue to Task 12.

---

## Task 12: Edge-case test matrix (the gate before reporting back)

**Files:** none modified — test harness pods only.

For each test below: (1) apply the manifest, (2) check the assertion, (3) delete the pod. Track results in TaskUpdate notes.

- [ ] **Test A: URI mode (the BPF failure case)**

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: nri-test-uri
  annotations:
    db-creds-injector.numberly.io/role: "demo-role"
    db-creds-injector.numberly.io/cluster: "databases"
    db-creds-injector.numberly.io/demo.mode: "uri"
    db-creds-injector.numberly.io/demo.env-key-uri: "DB_URI"
    db-creds-injector.numberly.io/demo.template: "postgres://__USER__:__PASSWORD__@db.example.com:5432/mydb?sslmode=require"
spec:
  containers:
  - name: app
    image: busybox:latest
    command: ["sh", "-c", "echo \"DB_URI=$DB_URI\"; sleep 10"]
```

Assertion: log shows full URI including the `@db.example.com:5432/mydb?sslmode=require` tail intact.

- [ ] **Test B: Multi-container pod**

Two containers, both expecting `DB_PASSWORD`. Both must show real value.

- [ ] **Test C: Init container**

Init container reads the env, completes, then main container reads it. Both substitutions correct.

- [ ] **Test D: CrashLoopBackoff retry**

Container that exits 1 immediately. After 3 restarts, env still substituted on each restart.

- [ ] **Test E: Multi-DbConfiguration in single pod (should be rejected)**

Pod with two `db-creds-injector.numberly.io/<dbname>.role` annotations (two different dbs). Webhook should reject with "NRI mode currently supports a single DbConfiguration per pod" (the check from Task 2 step 3).

- [ ] **Test F: Long credential value (>73 bytes — was BPF limit)**

Force vault to issue a long password by configuring a vault `password_policy` with length=128, then deploy a pod using that role. Substitution must succeed.

(If vault config tweak is too involved for the test cluster, skip F with a TaskUpdate note pointing to the unit test in `pkg/nri/substitute_test.go` that covers arbitrary length.)

- [ ] **Test G: NRI DaemonSet restart between webhook event and pod start**

Apply pod, immediately delete the NRI DS pod (`kubectl delete pod -n vault-db-injector -l app=vault-db-nri`). Wait for DS to come back. Pod must successfully create with substituted env (NRI replays `RunPodSandbox` / `CreateContainer` events to the reconnecting plugin).

- [ ] **Test H: Wrap-token expired**

Apply pod, then immediately patch the wrap-TTL annotation to a token that expired (or wait > 5 min by reducing `nri.wrapTokenTTL` to 30s and sleeping 35s before letting kubelet schedule the pod). Container should start with placeholder env and CrashLoop visibly.

- [ ] **Test I: Vault unreachable**

Scale Vault to zero replicas. Apply pod. Container starts with placeholder env, CrashLoop. Restore Vault. Pod retried by kubelet → succeeds.

- [ ] **Step end: All tests pass**

If all A-I pass (or F is documented-skipped), Task 12 is complete. Mark it complete in TaskUpdate. Otherwise iterate on the failing test before moving on. Do not declare success until every test passes.

- [ ] **Commit (test artifacts)**

```bash
mkdir -p docs/superpowers/test-evidence/2026-05-02-nri/
# Save logs, kubectl describe outputs, etc. as evidence
git add docs/superpowers/test-evidence/2026-05-02-nri/
git commit -m "test(nri): k3d edge-case validation evidence"
```

---

## Task 13: Final cleanup pass

**Files:**
- Modify: `Makefile`
- Modify: `README.md` (if it mentions BPF mode)
- Modify: `docs/` (BPF-related markdown, if present)

- [ ] **Step 1: Sweep for leftover BPF references**

Run: `grep -rn -i "bpf" --include="*.go" --include="*.yaml" --include="*.yml" --include="*.md" --include="Makefile" .`
Expected: only references in spec history files (`docs/superpowers/specs/2026-05-02-ebpf-injection-mode-design.md`) and the new spec/plan documenting the pivot.

For any other file: rename or delete the reference. Document the spec linking the deprecation in `README.md` if appropriate.

- [ ] **Step 2: Run the full unit test suite**

Run: `go test ./... -count=1`
Expected: ALL PASS.

- [ ] **Step 3: Run go vet and gofmt**

Run: `go vet ./... && gofmt -l . | tee /tmp/fmt-issues.txt`
Expected: vet PASS, no gofmt issues.

If gofmt issues: `gofmt -w $(cat /tmp/fmt-issues.txt)`.

- [ ] **Step 4: Final commit**

```bash
git add -A
git commit -m "chore: sweep remaining BPF references after NRI pivot"
```

- [ ] **Step 5: Sanity check git log**

Run: `git log --oneline main..HEAD`
Expected: a clean linear sequence of commits, each with a descriptive message, ending with the cleanup commit.

---

## Self-Review Notes

This plan was self-reviewed against the spec:

- ✅ Spec coverage: every spec section maps to ≥1 task. URI fix → Task 4 unit test + Task 12 Test A. Webhook updates → Tasks 2, 8. NRI plugin → Tasks 4-7. Helm → Task 9. Removal of BPF → Tasks 1, 13.
- ✅ Placeholder scan: no TBD/TODO; commands and code blocks are explicit.
- ✅ Type consistency: `NRIMapping`, `NRIConfig`, `ANNOTATION_NRI_MAPPING`, `cfg.NRI.*` used uniformly across tasks.

Two known sequencing notes:
1. Task 2 introduces a stub `runNRIAgent`; Task 6 replaces it. The stub keeps `go build` green between commits — important so reviewers can `git bisect`.
2. Task 7's `TestNRIMappingMarshal` may surface that `NRIMapping` JSON tags differ from the BPF version. The fix is in-task (add tags if missing) — call it out only if it surfaces.
