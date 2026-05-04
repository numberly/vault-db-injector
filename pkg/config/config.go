package config

import (
	"os"

	"github.com/cockroachdb/errors"

	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// Mode represents the operational mode of the vault-db-injector.
type Mode string

const (
	ModeInjector Mode = "injector"
	ModeRenewer  Mode = "renewer"
	ModeRevoker  Mode = "revoker"
	ModeNRI      Mode = "nri"
	ModeAll      Mode = "all"
)

// NRIConfig holds the configuration for the NRI plugin credential layer.
// When Enabled is false, the webhook produces literal env values (legacy
// behavior). When true, the webhook wraps every credential and the NRI
// DaemonSet substitutes placeholders at CreateContainer time.
type NRIConfig struct {
	Enabled    bool   `yaml:"enabled" envconfig:"nri_enabled"`
	SocketPath string `yaml:"socketPath" envconfig:"nri_socket_path"`
	// CachePath is the on-disk JSON cache that persists unwrapped credentials
	// across plugin DS restarts (hostPath tmpfs). Survives DS pod restart;
	// cleared on node reboot. Must be writable by the plugin user.
	CachePath string `yaml:"cachePath" envconfig:"nri_cache_path"`
	// PluginName is the NRI plugin name used at registration. Must be
	// unique per containerd instance — running multiple injector
	// releases (prod + dev) on the same cluster requires distinct names
	// (set via helm to {{ .Release.Name }}).
	PluginName string `yaml:"pluginName" envconfig:"nri_plugin_name"`
	// PluginIndex is the NRI plugin priority — must also be unique per
	// containerd instance when multiple plugins coexist.
	PluginIndex string `yaml:"pluginIndex" envconfig:"nri_plugin_index"`
	// PodLabel is the pod label key the plugin filters on. Pods missing
	// this label (or with value != "true") are not processed. With
	// multiple injector releases, set this to the release-specific label
	// the matching webhook's objectSelector uses (e.g.
	// "vault-db-injector" or "vault-db-injector-dev"). Empty value
	// disables the filter and processes every pod that has placeholders.
	PodLabel string `yaml:"podLabel" envconfig:"nri_pod_label"`
}

type Config struct {
	CertFile          string    `yaml:"certFile" envconfig:"cert_file"`
	KeyFile           string    `yaml:"keyFile" envconfig:"key_file"`
	VaultAddress      string    `yaml:"vaultAddress" envconfig:"vault_address"`
	VaultAuthPath     string    `yaml:"vaultAuthPath" envconfig:"vault_auth_path"`
	LogLevel          string    `yaml:"logLevel" envconfig:"log_level"`
	KubeRole          string    `yaml:"kubeRole" envconfig:"kube_role"`
	TokenTTL          string    `yaml:"tokenTTL" envconfig:"token_ttl"`
	VaultSecretName   string    `yaml:"vaultSecretName" envconfig:"vault_secret_name"`
	VaultSecretPrefix string    `yaml:"vaultSecretPrefix" envconfig:"vault_secret_prefix"`
	Mode              Mode      `yaml:"mode" envconfig:"mode"`
	Sentry            bool      `yaml:"sentry" envconfig:"sentry"`
	SentryDsn         string    `yaml:"sentryDsn" envconfig:"sentry_dsn"`
	SentryEnvironment string    `yaml:"sentryEnvironment" envconfig:"sentry_environment"`
	SentrySampleRate  float64   `yaml:"sentrySampleRate" envconfig:"sentry_sample_rate"`
	SyncTTLSecond     int       `yaml:"syncTTLSecond" envconfig:"sync_ttl_second"`
	InjectorLabel     string    `yaml:"injectorLabel" envconfig:"injector_label"`
	DefaultEngine     string    `yaml:"defaultEngine" envconfig:"default_engine"`
	VaultRateLimit    int       `yaml:"vaultRateLimit" envconfig:"vault_rate_limit"`
	NRI               NRIConfig `yaml:"nri" envconfig:"nri"`
}

func NewConfig(configFile string) (*Config, error) {
	cfg := &Config{
		CertFile:          "",
		KeyFile:           "",
		VaultAddress:      "",
		VaultAuthPath:     "",
		LogLevel:          "info",
		KubeRole:          "",
		TokenTTL:          "768h",
		VaultSecretName:   "",
		VaultSecretPrefix: "",
		Mode:              ModeAll,
		Sentry:            false,
		SentryDsn:         "",
		SentryEnvironment: "production",
		SentrySampleRate:  1.0,
		SyncTTLSecond:     300,
		InjectorLabel:     "vault-db-injector",
		DefaultEngine:     "databases",
		VaultRateLimit:    30,
		NRI: NRIConfig{
			SocketPath:  "/var/run/nri/nri.sock",
			CachePath:   "/run/vault-db-injector/nri/cache.json",
			PluginName:  "vault-db-injector",
			PluginIndex: "10",
			PodLabel:    "vault-db-injector",
		},
	}
	if configFile != "" {
		data, err := os.ReadFile(configFile)
		if err != nil {
			return nil, err
		}

		err = yaml.Unmarshal([]byte(data), cfg)
		if err != nil {
			return nil, err
		}
	}
	err := envconfig.Process("INJECTOR", cfg)
	if err != nil {
		return nil, errors.Newf("error processing environment variables for prefix %s: %v", "INJECTOR_", err)
	}

	err = cfg.Validate()
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate verifies all properties of config struct are intialized
func (cfg *Config) Validate() error {
	checks := []struct {
		bad    bool
		errMsg string
	}{
		{cfg.Mode != ModeAll && cfg.Mode != ModeInjector && cfg.Mode != ModeRenewer && cfg.Mode != ModeRevoker && cfg.Mode != ModeNRI, "Wrong Mode : should be injector/renewer/revoker/nri/all"},
		{(cfg.Mode == ModeAll || cfg.Mode == ModeInjector) && cfg.CertFile == "", "no certFile specified"},
		{(cfg.Mode == ModeAll || cfg.Mode == ModeInjector) && cfg.KeyFile == "", "no keyFile specified"},
		{cfg.VaultAddress == "", "no vaultAddress specified"},
		{cfg.VaultAuthPath == "", "no vaultAuthPath specified"},
		{cfg.KubeRole == "", "no kubeRole specified"},
		// VaultSecretName / VaultSecretPrefix are required everywhere now:
		// the NRI plugin (pull-not-push design) creates dynamic credentials
		// at CreateContainer time and stamps lease metadata into Vault KV
		// for the renewer/revoker — same KV path the legacy webhook used.
		{cfg.VaultSecretName == "", "no vaultSecretName specified"},
		{cfg.VaultSecretPrefix == "", "no vaultSecretPrefix specified"},
		{cfg.Sentry && cfg.SentryDsn == "", "no sentryDsn specified"},
	}

	for _, check := range checks {
		if check.bad {
			return errors.Newf("invalid config: %s", check.errMsg)
		}
	}
	return nil
}

func GetLogLevel(level string) (logrus.Level, error) {
	m := map[string]logrus.Level{
		"debug": logrus.DebugLevel,
		"info":  logrus.InfoLevel,
		"warn":  logrus.WarnLevel,
	}

	l, ok := m[level]
	if !ok {
		return logrus.InfoLevel, errors.Newf("unsupported log level: %s", level)
	}
	return l, nil
}

func GetHAEnvs() (string, string, error) {
	podName := os.Getenv("POD_NAME")
	podNamespace := os.Getenv("POD_NAMESPACE")

	if podName == "" || podNamespace == "" {
		return "", "", errors.New("environment variables POD_NAME or POD_NAMESPACE are not set")
	}

	return podName, podNamespace, nil
}
