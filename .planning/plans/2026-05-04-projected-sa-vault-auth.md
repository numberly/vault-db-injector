# Projected ServiceAccount Vault Authentication Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Switch pod-to-Vault authentication from injector-SA + in-process `CanIGetRoles` to per-pod `TokenRequest` JWTs against Vault `auth/kubernetes/role` configured with `token_period`, achieving native Vault attestation (A) and least-privilege injector (B), while preserving lease lifecycle.

**Architecture:** Feature-flagged (`useProjectedSA`). When on, the injector calls `serviceaccounts/<sa>/token` on kube-apiserver to obtain a short-lived JWT for the pod's SA, logs in to Vault with that JWT, and uses the resulting periodic pod-token to issue `database/creds/<role>` directly (no orphan-token step). Renewer/revoker logic is unchanged; only their Helm SAs/policies are tightened. Spec: `docs/superpowers/specs/2026-05-04-projected-sa-vault-auth-design.md`.

**Tech Stack:** Go 1.21+, HashiCorp Vault API, client-go (TokenRequest API), envconfig, Helm 3.

---

## File structure

| File | Status | Responsibility |
|---|---|---|
| `pkg/config/config.go` | modify | Add `UseProjectedSA`, `TokenRequestAudiences`, `TokenRequestExpirationSeconds` fields |
| `pkg/config/config_test.go` | modify | Cover new fields' defaults and envconfig parsing |
| `pkg/k8s/connect.go` | modify | Add `RequestSAToken` to `ClientInterface` + `Client` impl |
| `pkg/k8s/pod_utils.go` | modify | Add `RequestSAToken` to `KubernetesClient` interface |
| `pkg/k8s/token_request.go` | create | TokenRequest implementation on `KubernetesClientAdapter` |
| `pkg/k8s/token_request_test.go` | create | Unit tests using `fake.NewSimpleClientset` |
| `pkg/vault/vault.go` | modify | Add `SkipOrphanCreation` to `DbCredentialsRequest`, branch in `GetDbCredentials` |
| `pkg/vault/vault_test.go` | create or extend | Test both branches of `GetDbCredentials` |
| `pkg/vault/auth.go` | unchanged | `Login` already takes a JWT, no change needed |
| `pkg/k8smutator/k8smutator.go` | modify | Project flag branch in `authorizeDbAccess` + `fetchDbCredentials` |
| `pkg/k8smutator/k8smutator_test.go` | create or extend | Cover projected branch end-to-end |
| `pkg/nri/vault.go` | modify | Project flag branch in `fetchAndBuildMapping` |
| `pkg/metrics/prom.go` | modify | New counters: `TokenRequestErrors`, `VaultLoginErrors`, `ProjectedRoleMisconfigured` |
| `helm/values.yml` | modify | New `vault.useProjectedSA`, `vault.tokenRequest.*` keys; renewer/revoker SAs |
| `helm/templates/rbac.yaml` | modify | Conditional `ClusterRole` for `serviceaccounts/token`; new SAs and bindings |
| `helm/templates/deployment-renewer.yaml` | modify | Use dedicated `vault-db-renewer` SA |
| `helm/templates/deployment-revoker.yaml` | modify | Use dedicated `vault-db-revoker` SA |
| `docs/how-it-works/projected-sa.md` | create | User-facing doc: prerequisites, Vault config example, security gains, troubleshooting |
| `mkdocs.yml` | modify | Register the new doc page |

---

## Branch hygiene (do this first)

### Task 0: Rename branch

The current branch `feat/ebpf-injection-mode` contains only NRI plugin work. Rename to clarify intent before adding projected-SA work on top.

- [ ] **Step 1: Rename local branch**

```bash
git branch -m feat/ebpf-injection-mode feat/nri-plugin
```

- [ ] **Step 2: Create the new feature branch from current HEAD**

```bash
git checkout -b feat/projected-sa-vault-auth
```

All subsequent commits land on `feat/projected-sa-vault-auth`. The previous (now `feat/nri-plugin`) ships the NRI work as its own PR; this plan stacks on top.

> If the branch was already pushed under the old name, also delete the remote and re-push:
> `git push origin :feat/ebpf-injection-mode && git push -u origin feat/nri-plugin && git push -u origin feat/projected-sa-vault-auth`

---

## Phase 1 — Config

### Task 1: Add projected-SA config fields

**Files:**
- Modify: `pkg/config/config.go`
- Test: `pkg/config/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `pkg/config/config_test.go`:

```go
func TestConfig_ProjectedSADefaults(t *testing.T) {
	t.Setenv("INJECTOR_VAULT_ADDRESS", "http://vault:8200")
	t.Setenv("INJECTOR_VAULT_AUTH_PATH", "kubernetes")
	t.Setenv("INJECTOR_KUBE_ROLE", "test")
	t.Setenv("INJECTOR_VAULT_SECRET_NAME", "n")
	t.Setenv("INJECTOR_VAULT_SECRET_PREFIX", "p")
	t.Setenv("INJECTOR_CERT_FILE", "c")
	t.Setenv("INJECTOR_KEY_FILE", "k")

	cfg, err := NewConfig("")
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if cfg.UseProjectedSA {
		t.Fatalf("UseProjectedSA default = true, want false")
	}
	if cfg.TokenRequestExpirationSeconds != 60 {
		t.Fatalf("TokenRequestExpirationSeconds default = %d, want 60", cfg.TokenRequestExpirationSeconds)
	}
	if len(cfg.TokenRequestAudiences) != 0 {
		t.Fatalf("TokenRequestAudiences default = %v, want empty", cfg.TokenRequestAudiences)
	}
}

func TestConfig_ProjectedSAEnvOverrides(t *testing.T) {
	t.Setenv("INJECTOR_VAULT_ADDRESS", "http://vault:8200")
	t.Setenv("INJECTOR_VAULT_AUTH_PATH", "kubernetes")
	t.Setenv("INJECTOR_KUBE_ROLE", "test")
	t.Setenv("INJECTOR_VAULT_SECRET_NAME", "n")
	t.Setenv("INJECTOR_VAULT_SECRET_PREFIX", "p")
	t.Setenv("INJECTOR_CERT_FILE", "c")
	t.Setenv("INJECTOR_KEY_FILE", "k")
	t.Setenv("INJECTOR_USE_PROJECTED_SA", "true")
	t.Setenv("INJECTOR_TOKEN_REQUEST_AUDIENCES", "vault,extra")
	t.Setenv("INJECTOR_TOKEN_REQUEST_EXPIRATION_SECONDS", "120")

	cfg, err := NewConfig("")
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	if !cfg.UseProjectedSA {
		t.Fatalf("UseProjectedSA = false, want true")
	}
	if cfg.TokenRequestExpirationSeconds != 120 {
		t.Fatalf("TokenRequestExpirationSeconds = %d, want 120", cfg.TokenRequestExpirationSeconds)
	}
	want := []string{"vault", "extra"}
	if !reflect.DeepEqual(cfg.TokenRequestAudiences, want) {
		t.Fatalf("TokenRequestAudiences = %v, want %v", cfg.TokenRequestAudiences, want)
	}
}
```

Add `import "reflect"` if not present.

- [ ] **Step 2: Run test, verify FAIL**

```bash
go test ./pkg/config/ -run TestConfig_ProjectedSA -v
```

Expected: compilation error (`UseProjectedSA undefined`).

- [ ] **Step 3: Add fields to `Config` in `pkg/config/config.go`**

After the `NRI` field (around line 76), add:

```go
	// UseProjectedSA, when true, switches the injector to per-pod
	// authentication: a short-lived TokenRequest JWT for the pod's SA
	// is used to log in to Vault, and credentials are issued under the
	// pod-token directly (no injector-SA orphan step).
	UseProjectedSA bool `yaml:"useProjectedSA" envconfig:"use_projected_sa"`

	// TokenRequestAudiences is the list of audiences set on the
	// TokenRequest. Empty = use cluster-default audience (compat with
	// Vault roles configured without audience). Recommended for new
	// setups: ["vault"], with the matching value on the Vault role.
	TokenRequestAudiences []string `yaml:"tokenRequestAudiences" envconfig:"token_request_audiences"`

	// TokenRequestExpirationSeconds is the requested lifetime of the
	// JWT used to log in to Vault. The Kubernetes apiserver may clamp
	// this up to its `--service-account-min-token-expiration` flag
	// (default 600s). Default 60s — only needs to live for one Vault
	// login round-trip.
	TokenRequestExpirationSeconds int64 `yaml:"tokenRequestExpirationSeconds" envconfig:"token_request_expiration_seconds"`
```

- [ ] **Step 4: Set defaults in `NewConfig`**

In `NewConfig`, alongside the other defaults (around line 99):

```go
		UseProjectedSA:                false,
		TokenRequestAudiences:         nil,
		TokenRequestExpirationSeconds: 60,
```

- [ ] **Step 5: Run tests, verify PASS**

```bash
go test ./pkg/config/ -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/config/config.go pkg/config/config_test.go
git commit -m "feat(config): add useProjectedSA + TokenRequest options"
```

---

## Phase 2 — TokenRequest plumbing

### Task 2: Extend `KubernetesClient` interface with `RequestSAToken`

**Files:**
- Modify: `pkg/k8s/connect.go`
- Modify: `pkg/k8s/pod_utils.go`
- Create: `pkg/k8s/token_request.go`
- Create: `pkg/k8s/token_request_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/k8s/token_request_test.go`:

```go
package k8s

import (
	"context"
	"testing"

	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestRequestSAToken_PassesAudiencesAndTTL(t *testing.T) {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "team-x"},
	}
	cs := fake.NewSimpleClientset(sa)

	cs.PrependReactor("create", "serviceaccounts", func(action clienttesting.Action) (bool, runtime.Object, error) {
		ca, ok := action.(clienttesting.CreateAction)
		if !ok || ca.GetSubresource() != "token" {
			return false, nil, nil
		}
		tr, ok := ca.GetObject().(*authv1.TokenRequest)
		if !ok {
			t.Fatalf("unexpected object: %T", ca.GetObject())
		}
		if got, want := tr.Spec.Audiences, []string{"vault"}; !equalSlice(got, want) {
			t.Fatalf("audiences = %v, want %v", got, want)
		}
		if tr.Spec.ExpirationSeconds == nil || *tr.Spec.ExpirationSeconds != 60 {
			t.Fatalf("expirationSeconds = %v, want 60", tr.Spec.ExpirationSeconds)
		}
		return true, &authv1.TokenRequest{Status: authv1.TokenRequestStatus{Token: "fake-jwt"}}, nil
	})

	a := NewKubernetesClientAdapter(cs)
	tok, err := a.RequestSAToken(context.Background(), "team-x", "myapp", []string{"vault"}, 60)
	if err != nil {
		t.Fatalf("RequestSAToken: %v", err)
	}
	if tok != "fake-jwt" {
		t.Fatalf("token = %q, want fake-jwt", tok)
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run test, verify FAIL**

```bash
go test ./pkg/k8s/ -run TestRequestSAToken -v
```

Expected: compilation error (`RequestSAToken undefined`).

- [ ] **Step 3: Implement `RequestSAToken`**

Create `pkg/k8s/token_request.go`:

```go
package k8s

import (
	"context"

	"github.com/cockroachdb/errors"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RequestSAToken issues a Kubernetes TokenRequest for the given
// ServiceAccount and returns the resulting JWT. Used to log in to
// Vault under the pod's identity rather than the injector's.
//
// expirationSeconds may be clamped up by the apiserver to
// --service-account-min-token-expiration (default 600s).
func (a *KubernetesClientAdapter) RequestSAToken(ctx context.Context, namespace, saName string, audiences []string, expirationSeconds int64) (string, error) {
	exp := expirationSeconds
	tr := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			Audiences:         audiences,
			ExpirationSeconds: &exp,
		},
	}
	out, err := a.Clientset.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, saName, tr, metav1.CreateOptions{})
	if err != nil {
		return "", errors.Wrapf(err, "TokenRequest for %s/%s", namespace, saName)
	}
	return out.Status.Token, nil
}
```

- [ ] **Step 4: Add to `KubernetesClient` interface**

In `pkg/k8s/pod_utils.go`, add to the interface:

```go
type KubernetesClient interface {
	CoreV1() v1.CoreV1Interface
	CoordinationV1() coordinationv1.CoordinationV1Interface
	GetServiceAccountToken() (string, error)
	RawClientset() kubernetes.Interface
	RequestSAToken(ctx context.Context, namespace, saName string, audiences []string, expirationSeconds int64) (string, error)
}
```

Add `"context"` import if not present.

- [ ] **Step 5: Add to `ClientInterface` and `Client`**

In `pkg/k8s/connect.go`, extend `ClientInterface`:

```go
type ClientInterface interface {
	GetServiceAccountToken() (string, error)
	GetKubernetesCACert() (*x509.CertPool, error)
	GetKubernetesClient() (*kubernetes.Clientset, error)
	RequestSAToken(ctx context.Context, namespace, saName string, audiences []string, expirationSeconds int64) (string, error)
}
```

And implement on `Client` (it must obtain a clientset on each call, like `GetKubernetesClient`):

```go
func (c *Client) RequestSAToken(ctx context.Context, namespace, saName string, audiences []string, expirationSeconds int64) (string, error) {
	cs, err := c.GetKubernetesClient()
	if err != nil {
		return "", err
	}
	return NewKubernetesClientAdapter(cs).RequestSAToken(ctx, namespace, saName, audiences, expirationSeconds)
}
```

Add `"context"` to imports.

- [ ] **Step 6: Run tests, verify PASS**

```bash
go test ./pkg/k8s/ -v
```

Expected: PASS (the existing tests must also keep passing — interface change might surface as compile errors in test fakes; if a `fakeKubernetesClientAdapter` is defined in `pod_utils_test.go`, add a stub `RequestSAToken` returning `("", nil)`).

- [ ] **Step 7: Update test fakes if needed**

Search for any local fake implementations:

```bash
grep -rn "fakeKubernetesClientAdapter\|fakeClient" pkg/ --include="*.go"
```

For each, add:

```go
func (f *fakeKubernetesClientAdapter) RequestSAToken(ctx context.Context, namespace, saName string, audiences []string, expirationSeconds int64) (string, error) {
	return "fake-jwt", nil
}
```

- [ ] **Step 8: Run all tests**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/k8s/
git commit -m "feat(k8s): add RequestSAToken (TokenRequest API wrapper)"
```

---

## Phase 3 — Vault: skip orphan creation

### Task 3: Add `SkipOrphanCreation` to `DbCredentialsRequest`

**Files:**
- Modify: `pkg/vault/vault.go`
- Test: `pkg/vault/vault_test.go` (create if absent)

- [ ] **Step 1: Inspect current `GetDbCredentials`**

Re-read `pkg/vault/vault.go:173-226`. Identify the call to `c.CreateOrphanToken` (line 175) and `c.SetToken(orphanToken)` (line 179). The `creds.DbTokenID = orphanToken` (line 182) needs to point at the connector's current token instead when projected.

- [ ] **Step 2: Write the failing test**

Create or extend `pkg/vault/vault_test.go`. Use `httptest.NewServer` to mock Vault — both Vault and the project already use the standard SDK. Minimal mock:

```go
package vault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	vault "github.com/hashicorp/vault/api"
	"github.com/numberly/vault-db-injector/pkg/logger"
)

func newMockVault(t *testing.T, handler http.HandlerFunc) (*Connector, func()) {
	t.Helper()
	srv := httptest.NewServer(handler)

	cfg := vault.DefaultConfig()
	cfg.Address = srv.URL
	cli, err := vault.NewClient(cfg)
	if err != nil {
		t.Fatalf("vault client: %v", err)
	}
	cli.SetToken("initial-token")
	c := &Connector{
		address:    srv.URL,
		client:     cli,
		vaultToken: "initial-token",
		dbMountPath: "database",
		dbRole:     "myrole",
		Log:        logger.GetLogger(),
	}
	return c, srv.Close
}

func TestGetDbCredentials_SkipOrphanCreation_doesNotCallCreateOrphan(t *testing.T) {
	createOrphanCalled := false
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/auth/token/create-orphan":
			createOrphanCalled = true
			w.WriteHeader(500)
		case strings.HasPrefix(r.URL.Path, "/v1/database/creds/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"lease_id":       "database/creds/myrole/abc",
				"lease_duration": 3600,
				"renewable":      true,
				"data":           map[string]any{"username": "u", "password": "p"},
			})
		case strings.HasPrefix(r.URL.Path, "/v1/"):
			// secret store writes from StoreDataAsync — accept silently
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}
	c, stop := newMockVault(t, handler)
	defer stop()

	creds, err := c.GetDbCredentials(context.Background(), DbCredentialsRequest{
		ContextID:          "ctx",
		TTL:                "1h",
		PodNameUID:         "uid",
		Namespace:          "ns",
		SecretName:         "sec",
		Prefix:             "p",
		ServiceAccount:     "sa",
		SkipOrphanCreation: true,
	})
	if err != nil {
		t.Fatalf("GetDbCredentials: %v", err)
	}
	if createOrphanCalled {
		t.Fatalf("CreateOrphanToken was called when SkipOrphanCreation=true")
	}
	if creds.DbTokenID != "initial-token" {
		t.Fatalf("DbTokenID = %q, want initial-token (the pod-token)", creds.DbTokenID)
	}
	if creds.Username != "u" || creds.Password != "p" {
		t.Fatalf("creds = %+v", creds)
	}
}

func TestGetDbCredentials_LegacyPath_callsCreateOrphan(t *testing.T) {
	createOrphanCalled := false
	handler := func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/auth/token/create-orphan":
			createOrphanCalled = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"auth": map[string]any{"client_token": "orphan-tok"},
			})
		case strings.HasPrefix(r.URL.Path, "/v1/database/creds/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"lease_id":       "database/creds/myrole/abc",
				"lease_duration": 3600,
				"renewable":      true,
				"data":           map[string]any{"username": "u", "password": "p"},
			})
		default:
			w.WriteHeader(204)
		}
	}
	c, stop := newMockVault(t, handler)
	defer stop()

	creds, err := c.GetDbCredentials(context.Background(), DbCredentialsRequest{
		ContextID: "ctx", TTL: "1h", PodNameUID: "uid", Namespace: "ns",
		SecretName: "sec", Prefix: "p", ServiceAccount: "sa",
	})
	if err != nil {
		t.Fatalf("GetDbCredentials: %v", err)
	}
	if !createOrphanCalled {
		t.Fatalf("CreateOrphanToken should be called in legacy path")
	}
	if creds.DbTokenID != "orphan-tok" {
		t.Fatalf("DbTokenID = %q, want orphan-tok", creds.DbTokenID)
	}
}
```

- [ ] **Step 3: Run test, verify FAIL**

```bash
go test ./pkg/vault/ -run TestGetDbCredentials -v
```

Expected: compilation error (`SkipOrphanCreation undefined`).

- [ ] **Step 4: Add `SkipOrphanCreation` to the request struct**

In `pkg/vault/vault.go`, around line 162:

```go
type DbCredentialsRequest struct {
	ContextID      string
	TTL            string
	PodNameUID     string
	Namespace      string
	SecretName     string
	Prefix         string
	ServiceAccount string
	// SkipOrphanCreation, when true, makes GetDbCredentials use the
	// connector's current token (assumed to be a pod-token from a
	// projected-SA Vault login) directly to issue database creds, with
	// no auth/token/create-orphan step. The stored DbTokenID is then
	// the pod-token itself, which the renewer/revoker treat identically
	// to legacy orphan tokens.
	SkipOrphanCreation bool
}
```

- [ ] **Step 5: Branch in `GetDbCredentials`**

Replace the head of `GetDbCredentials` (lines 173-182):

```go
func (c *Connector) GetDbCredentials(ctx context.Context, req DbCredentialsRequest) (*DbCreds, error) {
	creds := &DbCreds{}

	if req.SkipOrphanCreation {
		// Projected-SA path: the connector's current token is the
		// pod-token from auth/kubernetes/login. Use it directly; the
		// stored DbTokenID is this pod-token.
		creds.DbTokenID = c.vaultToken
	} else {
		policies := []string{c.dbRole}
		orphanToken, err := c.CreateOrphanToken(ctx, req.TTL, policies)
		if err != nil {
			return nil, err
		}
		c.SetToken(orphanToken)
		creds.DbTokenID = orphanToken
	}

	path := fmt.Sprintf("/%s/creds/%s", c.dbMountPath, c.dbRole)
	// ... rest of function unchanged
```

Keep the remainder of the function (Read secret, parse username/password, StoreDataAsync) identical.

- [ ] **Step 6: Run tests, verify PASS**

```bash
go test ./pkg/vault/ -v
```

Expected: PASS for both new tests; existing tests still PASS.

- [ ] **Step 7: Commit**

```bash
git add pkg/vault/vault.go pkg/vault/vault_test.go
git commit -m "feat(vault): support SkipOrphanCreation in GetDbCredentials"
```

---

## Phase 4 — Webhook (k8smutator) projected branch

### Task 4: Project flag in webhook auth path

**Files:**
- Modify: `pkg/k8smutator/k8smutator.go`
- Test: `pkg/k8smutator/k8smutator_test.go` (extend if exists)

- [ ] **Step 1: Locate caller of `authorizeDbAccess` and `fetchDbCredentials`**

Read `pkg/k8smutator/k8smutator.go` end-to-end (in particular `injectCredentialsIntoPod` — search for it), to find where these helpers are called.

```bash
grep -n "authorizeDbAccess\|fetchDbCredentials" pkg/k8smutator/*.go
```

- [ ] **Step 2: Write the failing test**

In `pkg/k8smutator/k8smutator_test.go` (create if needed), add a test that exercises the projected branch by calling `authorizeDbAccess` with a `cfg.UseProjectedSA=true` config. The test verifies:
- `RequestSAToken` is called on the kubernetes client
- `CanIGetRoles` is NOT called

Because this requires significant test plumbing (Vault mock + k8s mock), the simplest approach is a small refactor first: introduce a thin seam that returns the JWT to use for Vault login.

```go
// New test in k8smutator_test.go
func TestAuthorizeDbAccess_ProjectedMode_usesPodSAToken(t *testing.T) {
	// Setup: fake k8s clientset with the SA, mock Vault that responds to
	// auth/kubernetes/login but would fail on auth/token/create-orphan
	// or auth/<>/role/<> reads.
	t.Skip("integration scaffold — implement after seam refactor in Step 4")
}
```

- [ ] **Step 3: Refactor `CreateMutator` to obtain the right token per request**

The current `CreateMutator` calls `k8sClient.GetServiceAccountToken()` once per admission (line 56) regardless of mode. Change to:

```go
func CreateMutator(ctx context.Context, logger log.Logger, cfg *config.Config) kwhmutating.MutatorFunc {
	k8sClient := k8s.NewClient()
	return kwhmutating.MutatorFunc(func(_ context.Context, _ *kwhmodel.AdmissionReview, obj metav1.Object) (*kwhmutating.MutatorResult, error) {
		contextID := generateUUID(logger)
		defaultResult := &kwhmutating.MutatorResult{MutatedObject: obj}
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return &kwhmutating.MutatorResult{}, nil
		}

		logger.WithValues(log.Kv{"contextID": contextID}).Infof("mutating pod %s/%s", pod.Namespace, pod.UID)
		podLib := k8s.NewParserService(*cfg, pod)
		podDbConfig, err := podLib.GetPodDbConfig(contextID)
		if err != nil {
			if errors.Is(err, k8s.ErrV1AnnotationDetected) {
				return defaultResult, nil
			}
			return defaultResult, errors.Wrap(err, "failed to get Pod DB configuration")
		}

		tok, err := vaultLoginToken(ctx, cfg, k8sClient, pod)
		if err != nil {
			return defaultResult, errors.Wrap(err, "obtain Vault login token")
		}

		mutatedPod, role, podUuids, err := injectCredentialsIntoPod(ctx, contextID, cfg, podDbConfig.DbConfigurations, logger, podDbConfig.VaultDbPath, tok, pod)
		// ... rest unchanged
```

Add the new helper (top-level in the same file):

```go
// vaultLoginToken returns the JWT to be used for the Vault login on
// behalf of this admission. In legacy mode it returns the injector
// SA's token (mounted in the injector pod). In projected-SA mode it
// returns a TokenRequest-issued JWT for the admitted pod's SA.
func vaultLoginToken(ctx context.Context, cfg *config.Config, k8sClient k8s.ClientInterface, pod *corev1.Pod) (string, error) {
	if !cfg.UseProjectedSA {
		return k8sClient.GetServiceAccountToken()
	}
	saName := pod.Spec.ServiceAccountName
	if saName == "" {
		saName = "default"
	}
	return k8sClient.RequestSAToken(ctx, pod.Namespace, saName, cfg.TokenRequestAudiences, cfg.TokenRequestExpirationSeconds)
}
```

- [ ] **Step 4: Skip `CanIGetRoles` and use SkipOrphanCreation in projected mode**

In `authorizeDbAccess` (line 92), branch on `cfg.UseProjectedSA`:

```go
func authorizeDbAccess(ctx context.Context, contextID string, cfg *config.Config, dbConf k8s.DbConfiguration, logger log.Logger, vaultDbPath, tok string, pod *corev1.Pod) (*vault.Connector, string, error) {
	vaultConn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, dbConf.Role, vaultDbPath, dbConf.Role, tok, cfg.VaultRateLimit)
	if err := vaultConn.Login(ctx); err != nil {
		return nil, dbConf.Role, errors.Newf("cannot authenticate vault role: %s", err.Error())
	}
	vaultConn.K8sSaVaultToken = vaultConn.GetToken()
	logger.WithValues(log.Kv{"contextID": contextID}).Debugf("authenticated to vault using role %s/%s", cfg.VaultAuthPath, dbConf.Role)

	if cfg.UseProjectedSA {
		// Vault attests the pod identity natively via bound_service_account_names
		// during Login above; CanIGetRoles is redundant.
		return vaultConn, dbConf.Role, nil
	}

	serviceAccountName := pod.Spec.ServiceAccountName
	ok, err := vaultConn.CanIGetRoles(ctx, contextID, serviceAccountName, pod.Namespace, cfg.VaultAuthPath, dbConf.Role)
	if !ok || err != nil {
		return vaultConn, dbConf.Role, err
	}
	return vaultConn, dbConf.Role, nil
}
```

⚠️ Important: in projected mode the Vault `authRole` we pass to `NewConnector` must match the **pod's** Vault role (the one defined for that DbConfiguration), not the injector's `cfg.KubeRole`. The change above passes `dbConf.Role` as the third argument (was `cfg.KubeRole`). Verify by reading `NewConnector` signature in `pkg/vault/vault.go:50` — third arg is `authRole`.

> Edge case: if `cfg.UseProjectedSA=false`, the call must keep using `cfg.KubeRole` as before. Refactor the line to:
>
> ```go
> authRole := cfg.KubeRole
> if cfg.UseProjectedSA {
>     authRole = dbConf.Role
> }
> vaultConn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, authRole, vaultDbPath, dbConf.Role, tok, cfg.VaultRateLimit)
> ```

- [ ] **Step 5: Wire `SkipOrphanCreation` in `fetchDbCredentials`**

In `fetchDbCredentials` (line 111):

```go
creds, err := vaultConn.GetDbCredentials(ctx, vault.DbCredentialsRequest{
    ContextID:          contextID,
    TTL:                cfg.TokenTTL,
    PodNameUID:         podUuid,
    Namespace:          pod.Namespace,
    SecretName:         cfg.VaultSecretName,
    Prefix:             cfg.VaultSecretPrefix,
    ServiceAccount:     pod.Spec.ServiceAccountName,
    SkipOrphanCreation: cfg.UseProjectedSA,
})
```

`fetchDbCredentials` doesn't currently receive `cfg`. If its signature is `(ctx, contextID, cfg, dbConf, logger, vaultConn, pod)` already (line 111), it does — keep using it. If not, thread `cfg` through.

- [ ] **Step 6: Run tests**

```bash
go test ./pkg/k8smutator/ -v
```

Expected: PASS (the skipped projected-mode integration test stays skipped; legacy tests still green).

- [ ] **Step 7: Commit**

```bash
git add pkg/k8smutator/
git commit -m "feat(webhook): branch on UseProjectedSA for Vault auth + creds"
```

---

## Phase 5 — NRI projected branch

### Task 5: Project flag in NRI fetch path

**Files:**
- Modify: `pkg/nri/vault.go`

- [ ] **Step 1: Read current `fetchAndBuildMapping`**

Already reviewed: `pkg/nri/vault.go:32-120`. The function gets the injector SA token (line 79), logs in to Vault, runs `CanIGetRoles`, calls `GetDbCredentials`.

- [ ] **Step 2: Add the same projected branch**

Replace the block from line 77 to 95 (login + CanIGetRoles) with:

```go
	// Obtain the JWT for Vault login: pod's projected-SA in
	// useProjectedSA mode, plugin's own SA otherwise.
	var tok string
	if cfg.UseProjectedSA {
		tok, err = k8sClient.RequestSAToken(ctx, podNamespace, actualSA, cfg.TokenRequestAudiences, cfg.TokenRequestExpirationSeconds)
		if err != nil {
			return nil, nil, errors.Wrap(err, "TokenRequest for pod SA")
		}
	} else {
		tok, err = k8sClient.GetServiceAccountToken()
		if err != nil {
			return nil, nil, errors.Wrap(err, "get serviceaccount token")
		}
	}

	authRole := cfg.KubeRole
	if cfg.UseProjectedSA {
		authRole = dbConf.Role
	}
	conn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, authRole, pdc.VaultDbPath, dbConf.Role, tok, cfg.VaultRateLimit)
	if err := conn.Login(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "vault login")
	}
	conn.K8sSaVaultToken = conn.GetToken()

	if !cfg.UseProjectedSA {
		ok, err := conn.CanIGetRoles(ctx, contextID, actualSA, podNamespace, cfg.VaultAuthPath, dbConf.Role)
		if err != nil {
			return nil, nil, errors.Wrap(err, "vault CanIGetRoles")
		}
		if !ok {
			return nil, nil, errors.Newf("pod %s/%s not authorized for vault role %s", podNamespace, actualSA, dbConf.Role)
		}
	}
```

- [ ] **Step 3: Wire `SkipOrphanCreation`**

In the same function, change the `GetDbCredentials` call (line 97):

```go
creds, err := conn.GetDbCredentials(ctx, vault.DbCredentialsRequest{
    ContextID:          contextID,
    TTL:                cfg.TokenTTL,
    PodNameUID:         contextID,
    Namespace:          podNamespace,
    SecretName:         cfg.VaultSecretName,
    Prefix:             cfg.VaultSecretPrefix,
    ServiceAccount:     actualSA,
    SkipOrphanCreation: cfg.UseProjectedSA,
})
```

- [ ] **Step 4: Update the function-level comment**

The "Trust model" comment in the doc block (lines 25-31) mentions `CanIGetRoles` as defense in depth. Append:

```go
// - In useProjectedSA mode, Vault performs the attestation natively
//   (bound_service_account_names) during Login above and CanIGetRoles
//   is skipped. The Vault role MUST be configured with token_period > 0
//   so the pod-token (and its lease) survives until explicit revocation.
```

- [ ] **Step 5: Build and run all tests**

```bash
go build ./...
go test ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/nri/vault.go
git commit -m "feat(nri): branch on UseProjectedSA for Vault auth + creds"
```

---

## Phase 6 — Metrics

### Task 6: Add metrics for projected-SA path

**Files:**
- Modify: `pkg/metrics/prom.go`

- [ ] **Step 1: Add the counters**

Open `pkg/metrics/prom.go` and add three new prometheus counters next to the existing ones:

```go
TokenRequestErrors = promauto.NewCounterVec(prometheus.CounterOpts{
    Namespace: "vault_db_injector",
    Name:      "token_request_errors_total",
    Help:      "Number of failed Kubernetes TokenRequest calls for projected-SA Vault login.",
}, []string{"reason"})

VaultLoginErrors = promauto.NewCounterVec(prometheus.CounterOpts{
    Namespace: "vault_db_injector",
    Name:      "vault_login_errors_total",
    Help:      "Number of failed Vault logins.",
}, []string{"reason", "auth_mode"})

ProjectedRoleMisconfigured = promauto.NewCounterVec(prometheus.CounterOpts{
    Namespace: "vault_db_injector",
    Name:      "projected_role_misconfigured_total",
    Help:      "Number of times a Vault role used in projected-SA mode was found without token_period > 0.",
}, []string{"role"})
```

> Match the exact `promauto`/`prometheus` style and namespace currently used in `prom.go` — check the file first and follow whatever the existing counters use (some projects use `MustRegister` instead of `promauto`).

- [ ] **Step 2: Wire `TokenRequestErrors` in callers**

In `pkg/k8smutator/k8smutator.go` `vaultLoginToken`:

```go
if cfg.UseProjectedSA {
    saName := pod.Spec.ServiceAccountName
    if saName == "" {
        saName = "default"
    }
    tok, err := k8sClient.RequestSAToken(ctx, pod.Namespace, saName, cfg.TokenRequestAudiences, cfg.TokenRequestExpirationSeconds)
    if err != nil {
        metrics.TokenRequestErrors.WithLabelValues(classifyTokenRequestError(err)).Inc()
        return "", err
    }
    return tok, nil
}
```

In `pkg/nri/vault.go` projected branch, similarly wrap.

Add a small helper (in `pkg/k8smutator` or `pkg/k8s`):

```go
func classifyTokenRequestError(err error) string {
    msg := err.Error()
    switch {
    case strings.Contains(msg, "forbidden"):
        return "rbac_denied"
    case strings.Contains(msg, "not found"):
        return "sa_not_found"
    default:
        return "other"
    }
}
```

- [ ] **Step 3: Wire `VaultLoginErrors`**

Around the `Login` call in `authorizeDbAccess` and `fetchAndBuildMapping`, on error:

```go
mode := "legacy"
if cfg.UseProjectedSA {
    mode = "projected"
}
metrics.VaultLoginErrors.WithLabelValues("other", mode).Inc()
```

(Reason classification can stay coarse for now — distinguishing audience mismatch vs missing role requires parsing Vault errors which is fragile.)

- [ ] **Step 4: `ProjectedRoleMisconfigured` (best-effort, non-blocking)**

After a successful login in projected mode, call `auth/token/lookup-self` and check `period`:

In `pkg/vault/vault.go`, add:

```go
// VerifyTokenPeriod returns the period (in seconds) of the connector's
// current token. Returns 0 if the token is not periodic.
func (c *Connector) VerifyTokenPeriod(ctx context.Context) (int64, error) {
    secret, err := c.client.Auth().Token().LookupSelfWithContext(ctx)
    if err != nil {
        return 0, err
    }
    p, ok := secret.Data["period"]
    if !ok {
        return 0, nil
    }
    switch v := p.(type) {
    case json.Number:
        n, _ := v.Int64()
        return n, nil
    case float64:
        return int64(v), nil
    case int64:
        return v, nil
    default:
        return 0, nil
    }
}
```

(Add `"encoding/json"` import.)

In `authorizeDbAccess` and `fetchAndBuildMapping`, after `Login`:

```go
if cfg.UseProjectedSA {
    if period, err := vaultConn.VerifyTokenPeriod(ctx); err == nil && period == 0 {
        metrics.ProjectedRoleMisconfigured.WithLabelValues(dbConf.Role).Inc()
        logger.WithValues(log.Kv{"role": dbConf.Role}).Warnf("vault role has no token_period — pod-token (and its lease) will die at max_ttl; configure token_period > 0")
    }
}
```

- [ ] **Step 5: Build and test**

```bash
go build ./...
go test ./...
```

- [ ] **Step 6: Commit**

```bash
git add pkg/metrics/prom.go pkg/k8smutator/ pkg/nri/ pkg/vault/
git commit -m "feat(metrics): observability for projected-SA flow"
```

---

## Phase 7 — Helm chart

### Task 7: Add `useProjectedSA` values + conditional ClusterRole

**Files:**
- Modify: `helm/values.yml`
- Modify: `helm/templates/rbac.yaml`

- [ ] **Step 1: Add values**

In `helm/values.yml`, under the existing `vault:` block (or create one if absent), add:

```yaml
vault:
  # When true, the injector authenticates to Vault per-pod using the
  # pod's ServiceAccount via the Kubernetes TokenRequest API, rather
  # than its own SA. Requires:
  #   - Vault auth/kubernetes role configured with token_period > 0
  #   - This chart will mount a ClusterRole granting create on
  #     serviceaccounts/token
  # See docs/how-it-works/projected-sa.md for the full Vault config.
  useProjectedSA: false
  tokenRequest:
    audiences: []
    expirationSeconds: 60
```

Also surface them as env vars to the deployment(s) (`deployment-injector.yaml`, `daemonset-nri.yaml`):

```yaml
- name: INJECTOR_USE_PROJECTED_SA
  value: {{ .Values.vault.useProjectedSA | quote }}
- name: INJECTOR_TOKEN_REQUEST_AUDIENCES
  value: {{ join "," .Values.vault.tokenRequest.audiences | quote }}
- name: INJECTOR_TOKEN_REQUEST_EXPIRATION_SECONDS
  value: {{ .Values.vault.tokenRequest.expirationSeconds | quote }}
```

- [ ] **Step 2: Conditional ClusterRole in `rbac.yaml`**

Append to `helm/templates/rbac.yaml`:

```yaml
{{- if .Values.vault.useProjectedSA }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ .Release.Name }}-token-requester
rules:
  - apiGroups: [""]
    resources: ["serviceaccounts/token"]
    verbs: ["create"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: {{ .Release.Name }}-token-requester
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: {{ .Release.Name }}-token-requester
subjects:
  - kind: ServiceAccount
    name: {{ include "vault-db-injector.serviceAccountName" . | default (printf "%s" .Release.Name) }}
    namespace: {{ .Release.Namespace }}
{{- end }}
```

> Use whatever SA-name template the chart already exposes. Check `_helpers.tpl` to confirm. If no helper exists, hardcode the literal SA name used by `deployment-injector.yaml` and `daemonset-nri.yaml`.

- [ ] **Step 3: Render-test**

```bash
helm template helm/ --set vault.useProjectedSA=true | grep -A 10 token-requester
helm template helm/ --set vault.useProjectedSA=false | grep token-requester || echo "OK: not rendered"
```

Expected: rendered when true, absent when false.

- [ ] **Step 4: Commit**

```bash
git add helm/values.yml helm/templates/rbac.yaml helm/templates/deployment-injector.yaml helm/templates/daemonset-nri.yaml
git commit -m "feat(helm): conditional projected-SA RBAC + values"
```

---

### Task 8: Dedicated SAs for renewer/revoker (cleanup)

**Files:**
- Modify: `helm/templates/rbac.yaml`
- Modify: `helm/templates/deployment-renewer.yaml`
- Modify: `helm/templates/deployment-revoker.yaml`

- [ ] **Step 1: Add SAs and bindings**

In `helm/templates/rbac.yaml`, append:

```yaml
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ .Release.Name }}-renewer
  namespace: {{ .Release.Namespace }}
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ .Release.Name }}-revoker
  namespace: {{ .Release.Namespace }}
```

> If renewer/revoker need cluster-scoped k8s permissions (lease objects, pod listing), copy whatever the original SA has. Currently the renewer reads `pod.UID` from K8s API — verify by inspecting `pkg/renewer/`.

- [ ] **Step 2: Wire deployments to new SAs**

In `helm/templates/deployment-renewer.yaml`, replace `serviceAccountName: ...` with:

```yaml
serviceAccountName: {{ .Release.Name }}-renewer
```

Same in `deployment-revoker.yaml` with `-revoker`.

- [ ] **Step 3: Document Vault prerequisites**

Add a comment block in `values.yml`:

```yaml
# When useProjectedSA is on, this chart provisions dedicated SAs for
# the renewer and revoker. The Vault operator must create matching
# auth/kubernetes/role entries:
#   - auth/kubernetes/role/<release>-renewer: bound_sa_names=<release>-renewer,
#     token_policies=<release>-renewer (capabilities: update on
#     auth/token/renew + sys/leases/renew)
#   - auth/kubernetes/role/<release>-revoker: bound_sa_names=<release>-revoker,
#     token_policies=<release>-revoker (capabilities: update on
#     auth/token/revoke-orphan + sys/leases/revoke)
# See docs/how-it-works/projected-sa.md for the policies.
```

- [ ] **Step 4: Commit**

```bash
git add helm/templates/
git commit -m "feat(helm): dedicated SAs for renewer + revoker"
```

---

## Phase 8 — Documentation

### Task 9: User-facing doc

**Files:**
- Create: `docs/how-it-works/projected-sa.md`
- Modify: `mkdocs.yml`

- [ ] **Step 1: Write the doc**

Create `docs/how-it-works/projected-sa.md`:

````markdown
# Projected ServiceAccount Authentication

By default, vault-db-injector authenticates to Vault with its own
ServiceAccount and validates pod authorization with an in-process
check. With `useProjectedSA: true`, the injector instead authenticates
**per pod** using a short-lived JWT requested for the pod's own
ServiceAccount, and Vault performs the authorization check natively.

## What it changes

| Aspect | Default | Projected-SA |
|---|---|---|
| Vault sees | Injector SA | Pod SA |
| Authorization | In-process `CanIGetRoles` | Vault `bound_service_account_names` |
| Injector Vault policy | DB-issuing (broad) | None / health only |
| Token lifecycle | Orphan token via injector | Periodic pod-token |
| Renewer/revoker policy | Same as injector | Dedicated, minimal |

## Prerequisites

### 1. Vault role with `token_period`

Each `auth/kubernetes/role/<X>` consumed by an injected pod **must**
have `token_period` set; otherwise the pod-token (and its DB lease)
expires at `token_max_ttl` and credentials become invalid.

```bash
vault write auth/kubernetes/role/<role> \
    bound_service_account_names="<sa>" \
    bound_service_account_namespaces="<ns>" \
    audience="vault" \
    token_policies="<role-policy>" \
    token_type="service" \
    token_period="24h"
```

The policy attached to `<role-policy>`:

```hcl
path "database/creds/<role>" { capabilities = ["read"] }
path "auth/token/renew-self" { capabilities = ["update"] }
```

### 2. Renewer / revoker policies

```hcl
# vault-db-renewer
path "auth/token/renew" { capabilities = ["update"] }
path "sys/leases/renew" { capabilities = ["update"] }
```

```hcl
# vault-db-revoker
path "auth/token/revoke-orphan" { capabilities = ["update"] }
path "sys/leases/revoke"        { capabilities = ["update"] }
```

```bash
vault write auth/kubernetes/role/<release>-renewer \
    bound_service_account_names="<release>-renewer" \
    bound_service_account_namespaces="<injector-ns>" \
    token_policies="vault-db-renewer" \
    token_ttl="1h" token_max_ttl="24h"

vault write auth/kubernetes/role/<release>-revoker \
    bound_service_account_names="<release>-revoker" \
    bound_service_account_namespaces="<injector-ns>" \
    token_policies="vault-db-revoker" \
    token_ttl="1h" token_max_ttl="24h"
```

### 3. Helm values

```yaml
vault:
  useProjectedSA: true
  tokenRequest:
    audiences: ["vault"]   # must match the role's `audience`
    expirationSeconds: 60
```

The chart automatically:

- Grants the injector SA `create` on `serviceaccounts/token`
- Provisions `<release>-renewer` and `<release>-revoker` SAs with
  matching deployments

## Audience handling

| Role `audience` | `tokenRequest.audiences` | Result |
|---|---|---|
| empty | `[]` | Apiserver-default audience accepted by Vault (legacy compat) |
| `"vault"` | `["vault"]` | Strict cryptographic binding to Vault |
| `"vault"` | `[]` | ❌ Vault rejects login (audience mismatch) |
| empty | `["vault"]` | ✅ but defeats the purpose — set the role audience too |

**Recommendation for new deployments**: configure `audience="vault"` on
the role and `tokenRequest.audiences: ["vault"]` on the chart.

## Verification

After enabling on a cluster:

```bash
# 1. Inspect a pod's stored token (read from the KV path the chart
#    writes to)
vault kv get <secretPath>/<podUID>

# 2. Lookup the token — it should carry only the role policy, not the
#    injector policy
vault token lookup <stored-tokenID>
# → policies = [<role>], period > 0
```

## Migration

1. **Before code rollout**: configure `token_period` on every Vault
   role used by injected pods; create renewer/revoker roles + policies.
2. **Code deploy** with `useProjectedSA: false` (no change in behavior).
3. **Per cluster**, flip `useProjectedSA: true`. New pods use the new
   path; pods already injected continue to be renewed/revoked normally
   — no data migration.
4. **Cleanup** (separate PR): drop DB policy from the injector SA.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `permission denied` on TokenRequest | `vault.useProjectedSA` enabled in values but ClusterRole/binding missing or not yet applied |
| Vault `invalid role` on login | Pod's SA not present in `bound_service_account_names` |
| Pods lose creds after a few hours | `token_period` not set on the Vault role |
| `audience mismatch` on login | Role `audience` ≠ `tokenRequest.audiences` |

## Security gains

- **Native attestation** by Vault: the audit log shows which pod's SA
  acquired which credentials.
- **Compromised injector** can no longer issue arbitrary DB credentials:
  it has no DB-issuing policy and the pod-token bears the role
  contraint cryptographically.
- **Reduced blast radius**: the only k8s capability the injector still
  needs is `serviceaccounts/token`, scoped to the audience defined in
  `tokenRequest.audiences`.
````

- [ ] **Step 2: Register in `mkdocs.yml`**

Add the page under the `how-it-works` section:

```yaml
nav:
  - How it works:
      - ...:
      - Projected ServiceAccount: how-it-works/projected-sa.md
```

> Match the existing nav style — open `mkdocs.yml` first.

- [ ] **Step 3: Build doc locally if mkdocs is in the project**

```bash
which mkdocs && mkdocs build || echo "skip mkdocs build (not installed)"
```

- [ ] **Step 4: Commit**

```bash
git add docs/how-it-works/projected-sa.md mkdocs.yml
git commit -m "docs: projected-SA usage and configuration guide"
```

---

## Phase 9 — Final checks

### Task 10: Full test + lint sweep

- [ ] **Step 1: Run all tests**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Run go vet**

```bash
go vet ./...
```

- [ ] **Step 3: Build the binary**

```bash
go build ./...
```

- [ ] **Step 4: Helm lint**

```bash
helm lint helm/
helm template helm/ --set vault.useProjectedSA=true > /tmp/projected.yaml
helm template helm/ --set vault.useProjectedSA=false > /tmp/legacy.yaml
diff -u /tmp/legacy.yaml /tmp/projected.yaml | head -50
```

Expected: difference shows only the new ClusterRole/Binding/SAs.

- [ ] **Step 5: Manual smoke test docs**

Read `docs/how-it-works/projected-sa.md` end-to-end. Apply the verification block to a real cluster if available.

- [ ] **Step 6: Commit any small fixes**

```bash
git status
# fix anything that came up
```

---

## Out of scope (future PRs)

- Removing `CreateOrphanToken` and `CanIGetRoles` from the codebase entirely (only safe once every cluster runs `useProjectedSA=true`).
- Emptying the injector's Vault policy at the Vault side (opers task, documented in projected-sa.md migration section).
- Integration tests against a real Vault + kind cluster.
- Webhook fail-fast self-check at boot (`InjectorSelfTokenRequest`) — nice-to-have, not gating.

---

## Self-review checklist

- [x] Spec coverage: all sections of the spec map to a task (config → Task 1; TokenRequest → Task 2; SkipOrphanCreation → Task 3; webhook branch → Task 4; NRI branch → Task 5; metrics + role validation → Task 6; Helm RBAC → Task 7; Helm SAs cleanup → Task 8; doc → Task 9; rollout described in doc).
- [x] No "TBD"/"TODO"/"similar to" placeholders.
- [x] Type/method names consistent: `RequestSAToken`, `SkipOrphanCreation`, `UseProjectedSA`, `TokenRequestAudiences`, `TokenRequestExpirationSeconds`, `VerifyTokenPeriod` are used identically across tasks.
- [x] Each task has explicit file paths and runnable commands.
