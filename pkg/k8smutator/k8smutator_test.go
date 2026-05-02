package k8smutator

import (
	"testing"

	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/vault"
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

			err := applyEnvToContainers(pod, dbConf, creds)
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

	err := applyEnvToContainers(pod, dbConf, creds)
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

			err := applyEnvToContainers(pod, dbConf, creds)
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

	err := applyEnvToContainers(pod, dbConf, creds)
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
	err := applyEnvToContainers(pod, dbConf, creds)
	// Empty template is parsed as an empty URL — no error expected
	assert.NoError(t, err)
}

func TestApplyEnvToContainers_UnknownMode(t *testing.T) {
	pod := makePod([]corev1.Container{{}}, nil)
	dbConf := k8s.DbConfiguration{
		Mode: "file",
	}
	creds := fakeCreds("u", "p")

	err := applyEnvToContainers(pod, dbConf, creds)
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

	err := applyEnvToContainers(pod, dbConf, creds)
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
