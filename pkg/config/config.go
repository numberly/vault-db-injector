package config

import (
	"os"

	"github.com/cockroachdb/errors"

	"github.com/kelseyhightower/envconfig"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

type Config struct {
	CertFile          string  `yaml:"certFile" envconfig:"cert_file"`
	KeyFile           string  `yaml:"keyFile" envconfig:"key_file"`
	VaultAddress      string  `yaml:"vaultAddress" envconfig:"vault_address"`
	VaultAuthPath     string  `yaml:"vaultAuthPath" envconfig:"vault_auth_path"`
	LogLevel          string  `yaml:"logLevel" envconfig:"log_level"`
	KubeRole          string  `yaml:"kubeRole" envconfig:"kube_role"`
	TokenTTL          string  `yaml:"tokenTTL" envconfig:"token_ttl"`
	VaultSecretName   string  `yaml:"vaultSecretName" envconfig:"vault_secret_name"`
	VaultSecretPrefix string  `yaml:"vaultSecretPrefix" envconfig:"vault_secret_prefix"`
	Mode              string  `yaml:"mode" envconfig:"mode"`
	Sentry            bool    `yaml:"sentry" envconfig:"sentry"`
	SentryDsn         string  `yaml:"sentryDsn" envconfig:"sentry_dsn"`
	SentryEnvironment string  `yaml:"sentryEnvironment" envconfig:"sentry_environment"`
	SentrySampleRate  float64 `yaml:"sentrySampleRate" envconfig:"sentry_sample_rate"`
	SyncTTLSecond     int     `yaml:"syncTTLSecond" envconfig:"sync_ttl_second"`
	InjectorLabel     string  `yaml:"injectorLabel" envconfig:"injector_label"`
	DefaultEngine     string  `yaml:"defaultEngine" envconfig:"default_engine"`
	VaultRateLimit    int     `yaml:"vaultRateLimit" envconfig:"vault_rate_limit"`
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
		Mode:              "all",
		Sentry:            false,
		SentryDsn:         "",
		SentryEnvironment: "production",
		SentrySampleRate:  1.0,
		SyncTTLSecond:     300,
		InjectorLabel:     "vault-db-injector",
		DefaultEngine:     "databases",
		VaultRateLimit:    30,
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
		{cfg.Mode != "all" && cfg.Mode != "injector" && cfg.Mode != "renewer" && cfg.Mode != "revoker", "Wrong Mode : should be injector/renewer/all"},
		{(cfg.Mode == "all" || cfg.Mode == "injector") && cfg.CertFile == "", "no certFile specified"},
		{(cfg.Mode == "all" || cfg.Mode == "injector") && cfg.KeyFile == "", "no keyFile specified"},
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

func GetLogLevel(level string) logrus.Level {
	m := map[string]logrus.Level{
		"debug": logrus.DebugLevel,
		"info":  logrus.InfoLevel,
		"warn":  logrus.WarnLevel,
	}

	l, ok := m[level]
	if !ok {
		panic(1)
	}
	return l
}

func GetHAEnvs() (string, string, error) {
	podName := os.Getenv("POD_NAME")
	podNamespace := os.Getenv("POD_NAMESPACE")

	if podName == "" || podNamespace == "" {
		return "", "", errors.New("environment variables POD_NAME or POD_NAMESPACE are not set")
	}

	return podName, podNamespace, nil
}
