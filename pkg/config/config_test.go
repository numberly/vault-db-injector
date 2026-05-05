package config

import (
	"os"
	"testing"

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
// ProjectedSA + TokenRequest config
// ---------------------------------------------------------------------------

func TestConfig_ProjectedSADefaults(t *testing.T) {
	t.Setenv("INJECTOR_VAULT_ADDRESS", "http://vault:8200")
	t.Setenv("INJECTOR_VAULT_AUTH_PATH", "kubernetes")
	t.Setenv("INJECTOR_KUBE_ROLE", "test")
	t.Setenv("INJECTOR_VAULT_SECRET_NAME", "n")
	t.Setenv("INJECTOR_VAULT_SECRET_PREFIX", "p")
	t.Setenv("INJECTOR_CERT_FILE", "c")
	t.Setenv("INJECTOR_KEY_FILE", "k")

	cfg, err := NewConfig("")
	require.NoError(t, err)
	assert.False(t, cfg.UseProjectedSA)
	assert.EqualValues(t, 600, cfg.TokenRequestExpirationSeconds)
	assert.Empty(t, cfg.TokenRequestAudiences)
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
	require.NoError(t, err)
	assert.True(t, cfg.UseProjectedSA)
	assert.EqualValues(t, 120, cfg.TokenRequestExpirationSeconds)
	assert.Equal(t, []string{"vault", "extra"}, cfg.TokenRequestAudiences)
}

// TestValidate_ProjectedSARequiresAudiences verifies that useProjectedSA=true
// without tokenRequestAudiences is rejected at config validation time.
func TestValidate_ProjectedSARequiresAudiences(t *testing.T) {
	c := baseValidConfig()
	c.UseProjectedSA = true
	c.TokenRequestAudiences = []string{}

	err := c.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tokenRequestAudiences")
}

// TestValidate_ProjectedSAWithAudiences verifies that useProjectedSA=true
// with audiences passes validation.
func TestValidate_ProjectedSAWithAudiences(t *testing.T) {
	c := baseValidConfig()
	c.UseProjectedSA = true
	c.TokenRequestAudiences = []string{"vault"}
	c.TokenRequestExpirationSeconds = 600

	err := c.Validate()
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// ModeNRI + NRIConfig
// ---------------------------------------------------------------------------

func TestModeNRI_Validates(t *testing.T) {
	cfg := &Config{
		Mode:              ModeNRI,
		VaultAddress:      "https://vault",
		VaultAuthPath:     "kubernetes",
		KubeRole:          "x",
		DefaultEngine:     "db",
		VaultSecretName:   "vault-secret",
		VaultSecretPrefix: "prefix/",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("ModeNRI should validate, got %v", err)
	}
}

func newConfigForNRITest(t *testing.T) *Config {
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
		"INJECTOR_NRI_WRAP_TOKEN_TTL", "INJECTOR_NRI_SOCKET_PATH",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	cfg, err := NewConfig(f.Name())
	require.NoError(t, err)
	return cfg
}

func TestNRIConfig_Defaults(t *testing.T) {
	cfg := newConfigForNRITest(t)
	assert.Equal(t, "/var/run/nri/nri.sock", cfg.NRI.SocketPath)
	assert.Equal(t, "/run/vault-db-injector/nri/cache.json", cfg.NRI.CachePath)
}

func TestNRIConfig_LoadsExplicitValues(t *testing.T) {
	tmpfile, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	require.NoError(t, err)
	y := `
mode: renewer
vaultAddress: https://vault
vaultAuthPath: kubernetes
kubeRole: x
vaultSecretName: secret
vaultSecretPrefix: prefix
nri:
  enabled: true
  socketPath: /custom/nri.sock
`
	_, err = tmpfile.WriteString(y)
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	for _, k := range []string{
		"INJECTOR_MODE", "INJECTOR_VAULT_ADDRESS", "INJECTOR_VAULT_AUTH_PATH",
		"INJECTOR_KUBE_ROLE", "INJECTOR_VAULT_SECRET_NAME", "INJECTOR_VAULT_SECRET_PREFIX",
		"INJECTOR_NRI_ENABLED", "INJECTOR_NRI_SOCKET_PATH",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	cfg, err := NewConfig(tmpfile.Name())
	require.NoError(t, err)
	assert.True(t, cfg.NRI.Enabled)
	assert.Equal(t, "/custom/nri.sock", cfg.NRI.SocketPath)
}

// Regression: envconfig tags inside NRIConfig must NOT repeat the
// "nri_" prefix — the parent tag adds it. Helm posts
// INJECTOR_NRI_ENABLED=true on the injector deployment; if our tag
// resolves to INJECTOR_NRI_NRI_ENABLED, the env var is silently
// ignored and the webhook stays in legacy mode.
func TestNRIConfig_EnvVarOverride(t *testing.T) {
	tmpfile, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	require.NoError(t, err)
	y := `
mode: injector
certFile: /tmp/cert
keyFile: /tmp/key
vaultAddress: https://vault
vaultAuthPath: kubernetes
kubeRole: x
vaultSecretName: secret
vaultSecretPrefix: prefix
nri:
  enabled: false
  socketPath: /var/run/nri/nri.sock
`
	_, err = tmpfile.WriteString(y)
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	for _, k := range []string{
		"INJECTOR_MODE", "INJECTOR_VAULT_ADDRESS", "INJECTOR_VAULT_AUTH_PATH",
		"INJECTOR_KUBE_ROLE", "INJECTOR_VAULT_SECRET_NAME", "INJECTOR_VAULT_SECRET_PREFIX",
		"INJECTOR_CERT_FILE", "INJECTOR_KEY_FILE",
	} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}

	// helm sets INJECTOR_NRI_ENABLED=true on the injector deployment
	// when nri.enabled: true. The env var must override the YAML's
	// nri.enabled: false.
	t.Setenv("INJECTOR_NRI_ENABLED", "true")
	t.Setenv("INJECTOR_NRI_SOCKET_PATH", "/env/nri.sock")
	t.Setenv("INJECTOR_NRI_POD_LABEL", "my-release")
	t.Setenv("INJECTOR_NRI_PLUGIN_NAME", "my-release-plugin")
	t.Setenv("INJECTOR_NRI_PLUGIN_INDEX", "11")
	t.Setenv("INJECTOR_NRI_CACHE_PATH", "/run/my-release/cache.json")

	cfg, err := NewConfig(tmpfile.Name())
	require.NoError(t, err)
	assert.True(t, cfg.NRI.Enabled, "INJECTOR_NRI_ENABLED env var must override yaml")
	assert.Equal(t, "/env/nri.sock", cfg.NRI.SocketPath)
	assert.Equal(t, "my-release", cfg.NRI.PodLabel)
	assert.Equal(t, "my-release-plugin", cfg.NRI.PluginName)
	assert.Equal(t, "11", cfg.NRI.PluginIndex)
	assert.Equal(t, "/run/my-release/cache.json", cfg.NRI.CachePath)
}
