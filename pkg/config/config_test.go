package config

import (
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// NewConfig()
// ---------------------------------------------------------------------------

func TestNewConfig_NoFile_EnvVars(t *testing.T) {
	// Set all mandatory env vars (envconfig prefix = INJECTOR_)
	t.Setenv("INJECTOR_MODE", "renewer")
	t.Setenv("INJECTOR_VAULT_ADDRESS", "http://vault:8200")
	t.Setenv("INJECTOR_VAULT_AUTH_PATH", "auth/kubernetes")
	t.Setenv("INJECTOR_KUBE_ROLE", "my-role")
	t.Setenv("INJECTOR_VAULT_SECRET_NAME", "vault-secret")
	t.Setenv("INJECTOR_VAULT_SECRET_PREFIX", "prefix/")
	// renewer/revoker don't need cert/key

	cfg, err := NewConfig("")
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, ModeRenewer, cfg.Mode)
	assert.Equal(t, "http://vault:8200", cfg.VaultAddress)
}

func TestNewConfig_WithYAMLFile(t *testing.T) {
	yaml := `
mode: revoker
vaultAddress: http://vault:8200
vaultAuthPath: auth/kubernetes
kubeRole: my-role
vaultSecretName: vault-secret
vaultSecretPrefix: prefix/
`
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(yaml)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// envconfig overrides YAML when the env var is non-empty.
	// Unset all INJECTOR_ vars so the YAML values are used.
	for _, k := range []string{
		"INJECTOR_MODE", "INJECTOR_VAULT_ADDRESS", "INJECTOR_VAULT_AUTH_PATH",
		"INJECTOR_KUBE_ROLE", "INJECTOR_VAULT_SECRET_NAME", "INJECTOR_VAULT_SECRET_PREFIX",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Cleanup(func() {}) // t.Setenv already restores on cleanup

	cfg, err := NewConfig(f.Name())
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, ModeRevoker, cfg.Mode)
}

func TestNewConfig_MissingFile(t *testing.T) {
	_, err := NewConfig("/nonexistent/path/config.yaml")
	require.Error(t, err)
}

func TestNewConfig_InvalidYAML(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(":::invalid yaml:::")
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = NewConfig(f.Name())
	require.Error(t, err)
}

func TestNewConfig_ValidationError_NoVaultAddress(t *testing.T) {
	yaml := `
mode: renewer
vaultAuthPath: auth/kubernetes
kubeRole: my-role
vaultSecretName: vault-secret
vaultSecretPrefix: prefix/
`
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(yaml)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	for _, k := range []string{"INJECTOR_VAULT_ADDRESS", "INJECTOR_MODE", "INJECTOR_VAULT_AUTH_PATH",
		"INJECTOR_KUBE_ROLE", "INJECTOR_VAULT_SECRET_NAME", "INJECTOR_VAULT_SECRET_PREFIX"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	_, err = NewConfig(f.Name())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vaultAddress")
}

// ---------------------------------------------------------------------------
// Validate()
// ---------------------------------------------------------------------------

func baseValidConfig() *Config {
	return &Config{
		Mode:              ModeAll,
		CertFile:          "cert.pem",
		KeyFile:           "key.pem",
		VaultAddress:      "http://vault:8200",
		VaultAuthPath:     "auth/kubernetes",
		KubeRole:          "my-role",
		VaultSecretName:   "vault-secret",
		VaultSecretPrefix: "prefix/",
		Sentry:            false,
	}
}

func TestValidate_ValidConfigs(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
	}{
		{
			name: "mode=all with cert/key",
			cfg:  baseValidConfig(),
		},
		{
			name: "mode=injector with cert/key",
			cfg: func() *Config {
				c := baseValidConfig()
				c.Mode = ModeInjector
				return c
			}(),
		},
		{
			name: "mode=renewer no cert/key required",
			cfg: func() *Config {
				c := baseValidConfig()
				c.Mode = ModeRenewer
				c.CertFile = ""
				c.KeyFile = ""
				return c
			}(),
		},
		{
			name: "mode=revoker no cert/key required",
			cfg: func() *Config {
				c := baseValidConfig()
				c.Mode = ModeRevoker
				c.CertFile = ""
				c.KeyFile = ""
				return c
			}(),
		},
		{
			name: "sentry enabled with dsn",
			cfg: func() *Config {
				c := baseValidConfig()
				c.Sentry = true
				c.SentryDsn = "https://key@sentry.io/123"
				return c
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			assert.NoError(t, err)
		})
	}
}

func TestValidate_InvalidMode(t *testing.T) {
	c := baseValidConfig()
	c.Mode = Mode("unknown")
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Wrong Mode")
}

func TestValidate_MissingCertFile_InjectorMode(t *testing.T) {
	c := baseValidConfig()
	c.Mode = ModeInjector
	c.CertFile = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certFile")
}

func TestValidate_MissingKeyFile_InjectorMode(t *testing.T) {
	c := baseValidConfig()
	c.Mode = ModeInjector
	c.KeyFile = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyFile")
}

func TestValidate_MissingCertFile_AllMode(t *testing.T) {
	c := baseValidConfig()
	c.Mode = ModeAll
	c.CertFile = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "certFile")
}

func TestValidate_MissingKeyFile_AllMode(t *testing.T) {
	c := baseValidConfig()
	c.Mode = ModeAll
	c.KeyFile = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keyFile")
}

func TestValidate_MissingVaultAddress(t *testing.T) {
	c := baseValidConfig()
	c.VaultAddress = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vaultAddress")
}

func TestValidate_MissingVaultAuthPath(t *testing.T) {
	c := baseValidConfig()
	c.VaultAuthPath = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vaultAuthPath")
}

func TestValidate_MissingKubeRole(t *testing.T) {
	c := baseValidConfig()
	c.KubeRole = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubeRole")
}

func TestValidate_MissingVaultSecretName(t *testing.T) {
	c := baseValidConfig()
	c.VaultSecretName = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vaultSecretName")
}

func TestValidate_MissingVaultSecretPrefix(t *testing.T) {
	c := baseValidConfig()
	c.VaultSecretPrefix = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vaultSecretPrefix")
}

func TestValidate_SentryEnabledWithoutDsn(t *testing.T) {
	c := baseValidConfig()
	c.Sentry = true
	c.SentryDsn = ""
	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sentryDsn")
}

// ---------------------------------------------------------------------------
// GetLogLevel()
// ---------------------------------------------------------------------------

func TestGetLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected logrus.Level
		wantErr  bool
	}{
		{"debug", logrus.DebugLevel, false},
		{"info", logrus.InfoLevel, false},
		{"warn", logrus.WarnLevel, false},
		// levels not in the map → error + default info
		{"trace", logrus.InfoLevel, true},
		{"error", logrus.InfoLevel, true},
		{"fatal", logrus.InfoLevel, true},
		{"panic", logrus.InfoLevel, true},
		{"unknown", logrus.InfoLevel, true},
		{"", logrus.InfoLevel, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := GetLogLevel(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unsupported log level")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.expected, got)
		})
	}
}

// ---------------------------------------------------------------------------
// GetHAEnvs()
// ---------------------------------------------------------------------------

func TestGetHAEnvs_BothSet(t *testing.T) {
	t.Setenv("POD_NAME", "my-pod")
	t.Setenv("POD_NAMESPACE", "my-ns")

	name, ns, err := GetHAEnvs()
	require.NoError(t, err)
	assert.Equal(t, "my-pod", name)
	assert.Equal(t, "my-ns", ns)
}

func TestGetHAEnvs_MissingPodName(t *testing.T) {
	t.Setenv("POD_NAME", "")
	t.Setenv("POD_NAMESPACE", "my-ns")

	_, _, err := GetHAEnvs()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POD_NAME")
}

func TestGetHAEnvs_MissingPodNamespace(t *testing.T) {
	t.Setenv("POD_NAME", "my-pod")
	t.Setenv("POD_NAMESPACE", "")

	_, _, err := GetHAEnvs()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "POD_NAMESPACE")
}

func TestGetHAEnvs_BothMissing(t *testing.T) {
	t.Setenv("POD_NAME", "")
	t.Setenv("POD_NAMESPACE", "")

	_, _, err := GetHAEnvs()
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// ModeBPF + BPFConfig
// ---------------------------------------------------------------------------

func TestModeBPF_Validates(t *testing.T) {
	cfg := &Config{
		Mode:              ModeBPF,
		VaultAddress:      "https://vault",
		VaultAuthPath:     "kubernetes",
		KubeRole:          "x",
		DefaultEngine:     "db",
		VaultSecretName:   "vault-secret",
		VaultSecretPrefix: "prefix/",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("ModeBPF should validate, got %v", err)
	}
}

func newConfigForBPFTest(t *testing.T) *Config {
	t.Helper()
	// Use a minimal YAML that satisfies Validate (renewer doesn't need cert/key).
	y := `
mode: renewer
vaultAddress: https://vault
vaultAuthPath: kubernetes
kubeRole: x
vaultSecretName: secret
vaultSecretPrefix: prefix
`
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	require.NoError(t, err)
	_, err = f.WriteString(y)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	for _, k := range []string{
		"INJECTOR_MODE", "INJECTOR_VAULT_ADDRESS", "INJECTOR_VAULT_AUTH_PATH",
		"INJECTOR_KUBE_ROLE", "INJECTOR_VAULT_SECRET_NAME", "INJECTOR_VAULT_SECRET_PREFIX",
		"INJECTOR_BPF_WRAP_TOKEN_TTL", "INJECTOR_BPF_TMPFS_PATH", "INJECTOR_BPF_MAX_MAPPINGS_PER_NODE",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cfg, err := NewConfig(f.Name())
	require.NoError(t, err)
	return cfg
}

func TestBPFConfig_Defaults(t *testing.T) {
	cfg := newConfigForBPFTest(t)
	assert.Equal(t, 5*time.Minute, cfg.BPF.WrapTokenTTL)
	assert.Equal(t, "/run/vault-db-injector/bpf", cfg.BPF.TmpfsPath)
	assert.Equal(t, 4096, cfg.BPF.MaxMappingsPerNode)
}

func TestBPFConfig_LoadsExplicitValues(t *testing.T) {
	tmpfile, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	require.NoError(t, err)
	y := `
mode: renewer
vaultAddress: https://vault
vaultAuthPath: kubernetes
kubeRole: x
vaultSecretName: secret
vaultSecretPrefix: prefix
bpf:
  enabled: true
  wrapTokenTTL: 10m
  tmpfsPath: /custom/path
  maxMappingsPerNode: 100
`
	_, err = tmpfile.WriteString(y)
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	for _, k := range []string{
		"INJECTOR_MODE", "INJECTOR_VAULT_ADDRESS", "INJECTOR_VAULT_AUTH_PATH",
		"INJECTOR_KUBE_ROLE", "INJECTOR_VAULT_SECRET_NAME", "INJECTOR_VAULT_SECRET_PREFIX",
		"INJECTOR_BPF_ENABLED", "INJECTOR_BPF_WRAP_TOKEN_TTL", "INJECTOR_BPF_TMPFS_PATH", "INJECTOR_BPF_MAX_MAPPINGS_PER_NODE",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	cfg, err := NewConfig(tmpfile.Name())
	require.NoError(t, err)
	assert.True(t, cfg.BPF.Enabled)
	assert.Equal(t, 10*time.Minute, cfg.BPF.WrapTokenTTL)
	assert.Equal(t, "/custom/path", cfg.BPF.TmpfsPath)
	assert.Equal(t, 100, cfg.BPF.MaxMappingsPerNode)
}
