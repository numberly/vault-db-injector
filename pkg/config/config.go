package config

import (
	"os"
	"time"

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
	ModeBPF      Mode = "bpf"
	ModeAll      Mode = "all"
)

// BPFConfig holds the configuration for the eBPF credential protection layer.
// When Enabled is false, the webhook produces literal env values (legacy
// behavior). When true, the webhook wraps every credential and the bpf-mode
// DaemonSet substitutes placeholders at execve time.
type BPFConfig struct {
	Enabled            bool          `yaml:"enabled" envconfig:"bpf_enabled"`
	WrapTokenTTL       time.Duration `yaml:"wrapTokenTTL" envconfig:"bpf_wrap_token_ttl"`
	TmpfsPath          string        `yaml:"tmpfsPath" envconfig:"bpf_tmpfs_path"`
	MaxMappingsPerNode int           `yaml:"maxMappingsPerNode" envconfig:"bpf_max_mappings_per_node"`
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
	BPF               BPFConfig `yaml:"bpf" envconfig:"bpf"`
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
		BPF: BPFConfig{
			WrapTokenTTL:       5 * time.Minute,
			TmpfsPath:          "/run/vault-db-injector/bpf",
			MaxMappingsPerNode: 4096,
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
		{cfg.Mode != ModeAll && cfg.Mode != ModeInjector && cfg.Mode != ModeRenewer && cfg.Mode != ModeRevoker && cfg.Mode != ModeBPF, "Wrong Mode : should be injector/renewer/revoker/bpf/all"},
		{(cfg.Mode == ModeAll || cfg.Mode == ModeInjector) && cfg.CertFile == "", "no certFile specified"},
		{(cfg.Mode == ModeAll || cfg.Mode == ModeInjector) && cfg.KeyFile == "", "no keyFile specified"},
		{cfg.VaultAddress == "", "no vaultAddress specified"},
		{cfg.VaultAuthPath == "", "no vaultAuthPath specified"},
		{cfg.KubeRole == "", "no kubeRole specified"},
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
