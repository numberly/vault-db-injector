package k8smutator

import (
	"context"
	"strings"
	"testing"

	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/placeholder"
	"github.com/numberly/vault-db-injector/pkg/vault"
	log "github.com/slok/kubewebhook/v2/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

// makePod builds a minimal Pod with the given containers and init containers.
func makePod(containers []corev1.Container, initContainers []corev1.Container) *corev1.Pod {
	return &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers:     containers,
			InitContainers: initContainers,
		},
	}
}

func fakeCreds(user, pass string) *vault.DbCreds {
	return &vault.DbCreds{Username: user, Password: pass}
}

func TestApplyEnvToContainers_ClassicMode(t *testing.T) {
	tests := []struct {
		name           string
		mode           string // "" or "classic"
		containers     int
		initContainers int
		userKey        string
		passKey        string
		multiUserKeys  string
		multiPassKeys  string
	}{
		{
			name:           "mode empty defaults to classic",
			mode:           "",
			containers:     1,
			initContainers: 0,
			userKey:        "DB_USER",
			passKey:        "DB_PASS",
		},
		{
			name:           "mode classic explicit",
			mode:           "classic",
			containers:     2,
			initContainers: 1,
			userKey:        "DB_USER",
			passKey:        "DB_PASS",
		},
		{
			name:           "multiple containers",
			mode:           "",
			containers:     3,
			initContainers: 2,
			userKey:        "DB_USER",
			passKey:        "DB_PASS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containers := make([]corev1.Container, tt.containers)
			initContainers := make([]corev1.Container, tt.initContainers)
			pod := makePod(containers, initContainers)

			dbConf := k8s.DbConfiguration{
				Mode:             tt.mode,
				DbUserEnvKey:     tt.userKey,
				DbPasswordEnvKey: tt.passKey,
			}
			creds := fakeCreds("alice", "s3cr3t")

			err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", false)
			require.NoError(t, err)

			for i := range pod.Spec.Containers {
				assertEnvVar(t, pod.Spec.Containers[i].Env, tt.userKey, "alice")
				assertEnvVar(t, pod.Spec.Containers[i].Env, tt.passKey, "s3cr3t")
			}
			for i := range pod.Spec.InitContainers {
				assertEnvVar(t, pod.Spec.InitContainers[i].Env, tt.userKey, "alice")
				assertEnvVar(t, pod.Spec.InitContainers[i].Env, tt.passKey, "s3cr3t")
			}
		})
	}
}

func TestApplyEnvToContainers_ClassicMode_MultipleKeys(t *testing.T) {
	pod := makePod([]corev1.Container{{}}, nil)
	dbConf := k8s.DbConfiguration{
		Mode:             "classic",
		DbUserEnvKey:     "USER_A,USER_B",
		DbPasswordEnvKey: "PASS_A,PASS_B",
	}
	creds := fakeCreds("bob", "p@ss")

	err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", false)
	require.NoError(t, err)

	env := pod.Spec.Containers[0].Env
	assertEnvVar(t, env, "USER_A", "bob")
	assertEnvVar(t, env, "USER_B", "bob")
	assertEnvVar(t, env, "PASS_A", "p@ss")
	assertEnvVar(t, env, "PASS_B", "p@ss")
}

func TestApplyEnvToContainers_URIMode(t *testing.T) {
	tests := []struct {
		name        string
		template    string
		uriEnvKey   string
		user        string
		pass        string
		expectInURI string
	}{
		{
			name:        "postgres DSN with existing user",
			template:    "postgres://olduser:oldpass@db.example.com:5432/mydb",
			uriEnvKey:   "DATABASE_URL",
			user:        "newuser",
			pass:        "newpass",
			expectInURI: "postgres://",
		},
		{
			name:        "mysql DSN",
			template:    "mysql://root:root@localhost:3306/app",
			uriEnvKey:   "MYSQL_URL",
			user:        "svc",
			pass:        "secret",
			expectInURI: "mysql://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := makePod([]corev1.Container{{}}, []corev1.Container{{}})
			dbConf := k8s.DbConfiguration{
				Mode:        "uri",
				Template:    tt.template,
				DbURIEnvKey: tt.uriEnvKey,
			}
			creds := fakeCreds(tt.user, tt.pass)

			err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", false)
			require.NoError(t, err)

			// The env var must exist in both containers and init-containers
			for _, c := range pod.Spec.Containers {
				val := findEnvVar(c.Env, tt.uriEnvKey)
				require.NotEmpty(t, val, "env var %s should be set in container", tt.uriEnvKey)
				assert.Contains(t, val, tt.expectInURI)
				assert.Contains(t, val, tt.user)
			}
			for _, ic := range pod.Spec.InitContainers {
				val := findEnvVar(ic.Env, tt.uriEnvKey)
				require.NotEmpty(t, val, "env var %s should be set in init container", tt.uriEnvKey)
				assert.Contains(t, val, tt.user)
			}
		})
	}
}

func TestApplyEnvToContainers_URIMode_MultipleKeys(t *testing.T) {
	pod := makePod([]corev1.Container{{}}, nil)
	dbConf := k8s.DbConfiguration{
		Mode:        "uri",
		Template:    "postgres://old:old@db:5432/app",
		DbURIEnvKey: "DSN_A,DSN_B",
	}
	creds := fakeCreds("u", "p")

	err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", false)
	require.NoError(t, err)

	env := pod.Spec.Containers[0].Env
	assert.NotEmpty(t, findEnvVar(env, "DSN_A"))
	assert.NotEmpty(t, findEnvVar(env, "DSN_B"))
}

func TestApplyEnvToContainers_URIMode_InvalidTemplate(t *testing.T) {
	pod := makePod([]corev1.Container{{}}, nil)
	// A URL that url.Parse considers invalid is hard to produce (it's quite lenient),
	// but we can test that a totally broken scheme causes no panic and returns an error.
	// url.Parse is lenient, so we exercise the happy path; the error path would require
	// injecting a custom URL parser — left to the existing code path test above.
	// Instead verify that an empty template doesn't panic and produces a valid (empty) URL.
	dbConf := k8s.DbConfiguration{
		Mode:        "uri",
		Template:    "",
		DbURIEnvKey: "DB_URI",
	}
	creds := fakeCreds("u", "p")
	err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", false)
	// Empty template is parsed as an empty URL — no error expected
	assert.NoError(t, err)
}

func TestApplyEnvToContainers_UnknownMode(t *testing.T) {
	pod := makePod([]corev1.Container{{}}, nil)
	dbConf := k8s.DbConfiguration{
		Mode: "file",
	}
	creds := fakeCreds("u", "p")

	err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mode not supported")
}

func TestApplyEnvToContainers_NoPodContainers(t *testing.T) {
	// Pod with no containers — should be a no-op, not a panic
	pod := makePod(nil, nil)
	dbConf := k8s.DbConfiguration{
		Mode:             "classic",
		DbUserEnvKey:     "DB_USER",
		DbPasswordEnvKey: "DB_PASS",
	}
	creds := fakeCreds("u", "p")

	err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", false)
	assert.NoError(t, err)
}

func TestCheckConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		dbConf    k8s.DbConfiguration
		expectErr bool
		errMsg    string
	}{
		{
			name:      "valid configuration",
			dbConf:    k8s.DbConfiguration{DbName: "mydb", Role: "myrole"},
			expectErr: false,
		},
		{
			name:      "missing DbName",
			dbConf:    k8s.DbConfiguration{Role: "myrole"},
			expectErr: true,
			errMsg:    "DbName",
		},
		{
			name:      "missing Role",
			dbConf:    k8s.DbConfiguration{DbName: "mydb"},
			expectErr: true,
			errMsg:    "Role",
		},
		{
			name:      "both missing",
			dbConf:    k8s.DbConfiguration{},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkConfiguration(tt.dbConf)
			if tt.expectErr {
				require.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// helpers

func assertEnvVar(t *testing.T, envs []corev1.EnvVar, key, expectedVal string) {
	t.Helper()
	val := findEnvVar(envs, key)
	assert.Equal(t, expectedVal, val, "env var %s", key)
}

func findEnvVar(envs []corev1.EnvVar, key string) string {
	for _, e := range envs {
		if e.Name == key {
			return e.Value
		}
	}
	return ""
}

func TestApplyEnvToContainers_NRIEnabled_Classic(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}},
		},
	}
	dbConf := k8s.DbConfiguration{
		DbName:           "main",
		Mode:             k8s.DbModeClassic,
		DbUserEnvKey:     "DB_USER",
		DbPasswordEnvKey: "DB_PASSWORD",
		Role:             "myrole",
	}
	creds := &vault.DbCreds{Username: "alice", Password: "supersecret"}

	err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", true)
	require.NoError(t, err)

	envs := pod.Spec.Containers[0].Env
	require.Len(t, envs, 2)
	for _, e := range envs {
		assert.NotEqual(t, "alice", e.Value)
		assert.NotEqual(t, "supersecret", e.Value)
		assert.True(t, placeholder.IsPlaceholder(e.Value), "env %s value %q is not a placeholder", e.Name, e.Value)
	}

	// Transparent NRI: webhook does NOT add any annotation. The pod's
	// existing annotations are unchanged.
	for k := range pod.Annotations {
		assert.NotContains(t, k, "nri-mapping", "no annotation key should reference nri-mapping")
	}
}

func TestApplyEnvToContainers_NRIEnabled_URI(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}},
		},
	}
	dbConf := k8s.DbConfiguration{
		DbName:      "main",
		Mode:        k8s.DbModeURI,
		Template:    "postgres://x:y@db.example/mydb",
		DbURIEnvKey: "DB_URI",
		Role:        "myrole",
	}
	creds := &vault.DbCreds{Username: "alice", Password: "supersecret"}

	err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", true)
	require.NoError(t, err)

	envs := pod.Spec.Containers[0].Env
	require.Len(t, envs, 1)
	assert.NotContains(t, envs[0].Value, "alice")
	assert.NotContains(t, envs[0].Value, "supersecret")
	// The DSN must contain both placeholders embedded in the URL userinfo
	assert.Contains(t, envs[0].Value, placeholder.Prefix)
}

func TestApplyEnvToContainers_NRIDisabled_Classic_Unchanged(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}}}
	dbConf := k8s.DbConfiguration{
		DbName: "main", Mode: k8s.DbModeClassic,
		DbUserEnvKey: "DB_USER", DbPasswordEnvKey: "DB_PASSWORD",
	}
	creds := &vault.DbCreds{Username: "alice", Password: "secret"}

	err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", false)
	require.NoError(t, err)

	got := map[string]string{}
	for _, e := range pod.Spec.Containers[0].Env {
		got[e.Name] = e.Value
	}
	assert.Equal(t, "alice", got["DB_USER"])
	assert.Equal(t, "secret", got["DB_PASSWORD"])
}

// TestNRIMode_MultiDbConfig_PlaceholdersAreDistinct verifies that when NRI mode
// is active and a pod has two dbConfig annotations, applyEnvToContainersWithNRI
// generates two independent placeholder pairs — one per dbConfig. This is the
// env-side precondition for the NRI plugin to resolve both credential sets.
func TestNRIMode_MultiDbConfig_PlaceholdersAreDistinct(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app"}},
		},
	}
	dbConf1 := k8s.DbConfiguration{
		DbName:           "db1",
		Mode:             k8s.DbModeClassic,
		DbUserEnvKey:     "DB1_USER",
		DbPasswordEnvKey: "DB1_PASS",
		Role:             "role1",
	}
	dbConf2 := k8s.DbConfiguration{
		DbName:           "db2",
		Mode:             k8s.DbModeClassic,
		DbUserEnvKey:     "DB2_USER",
		DbPasswordEnvKey: "DB2_PASS",
		Role:             "role2",
	}

	// In NRI mode the creds argument is nil; applyEnvToContainersWithNRI
	// generates fresh placeholders internally.
	err := applyEnvToContainersWithNRI(pod, dbConf1, nil, "databases", true)
	require.NoError(t, err)
	err = applyEnvToContainersWithNRI(pod, dbConf2, nil, "databases", true)
	require.NoError(t, err)

	env := pod.Spec.Containers[0].Env
	require.Len(t, env, 4, "expect 4 env vars: 2 per dbConfig")

	vals := map[string]string{}
	for _, e := range env {
		vals[e.Name] = e.Value
	}

	// All four must be valid placeholders.
	for _, key := range []string{"DB1_USER", "DB1_PASS", "DB2_USER", "DB2_PASS"} {
		require.True(t, placeholder.IsPlaceholder(vals[key]), "env %s value %q must be a placeholder", key, vals[key])
	}

	// Placeholders across configs must be distinct — collision would cause
	// the NRI plugin to substitute the same cred value into both env vars.
	allPH := []string{vals["DB1_USER"], vals["DB1_PASS"], vals["DB2_USER"], vals["DB2_PASS"]}
	seen := map[string]struct{}{}
	for _, ph := range allPH {
		_, dup := seen[ph]
		require.False(t, dup, "placeholder %q appears more than once — placeholders must be unique across dbConfigs", ph)
		seen[ph] = struct{}{}
	}
}

// TestGenerateUUID_Uniqueness verifies that successive generateUUID calls never
// return the same value. This is the basis for the per-dbConfig UUID annotation
// written by the webhook in NRI mode (used by the NRI plugin to key KV entries).
func TestGenerateUUID_Uniqueness(t *testing.T) {
	logger := &stubLogger{}
	seen := map[string]struct{}{}
	for range 100 {
		u := generateUUID(logger)
		require.NotEmpty(t, u)
		_, dup := seen[u]
		require.False(t, dup, "generateUUID returned duplicate value %q", u)
		seen[u] = struct{}{}
	}
}

// stubLogger satisfies the log.Logger interface with no-op methods so
// generateUUID can be called from tests without a real logger.
type stubLogger struct{}

func (s *stubLogger) Infof(format string, args ...interface{})                              {}
func (s *stubLogger) Warningf(format string, args ...interface{})                           {}
func (s *stubLogger) Errorf(format string, args ...interface{})                             {}
func (s *stubLogger) Debugf(format string, args ...interface{})                             {}
func (s *stubLogger) WithValues(kvs map[string]interface{}) log.Logger                     { return s }
func (s *stubLogger) WithCtxValues(ctx context.Context) log.Logger                         { return s }
func (s *stubLogger) SetValuesOnCtx(parent context.Context, kvs map[string]interface{}) context.Context {
	return parent
}

func TestApplyEnvToContainers_NRIEnabled_AcceptsLongCredentials(t *testing.T) {
	// NRI mode imposes no length cap — long credentials must flow through.
	longVal := strings.Repeat("x", 256)
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
	}
	dbConf := k8s.DbConfiguration{
		DbName: "main", Mode: k8s.DbModeClassic,
		DbUserEnvKey: "DB_USER", DbPasswordEnvKey: "DB_PASSWORD",
	}
	creds := &vault.DbCreds{Username: longVal, Password: longVal}

	err := applyEnvToContainersWithNRI(pod, dbConf, creds, "databases", true)
	require.NoError(t, err)
}
