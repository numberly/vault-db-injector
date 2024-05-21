package k8s_test

import (
	"testing"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
)

func TestGetPodDbConfigWithoutAnnotations(t *testing.T) {
	cfg := config.Config{
		DefaultEngine: "default-engine-path",
	}
	pod := &corev1.Pod{} // Mock pod without any annotations

	service := k8s.NewService(cfg, pod)
	podDbConfig, err := service.GetPodDbConfig()

	assert.NoError(t, err)
	// The VaultDbPath should default to cfg.DefaultEngine when no annotation is present.
	assert.Equal(t, "default-engine-path", podDbConfig.VaultDbPath)
	assert.Empty(t, podDbConfig.DbConfigurations)
}

func initTestLogger() {
	// Example configuration setup for testing
	testConfig := config.Config{
		LogLevel: "info", // Or whatever log level is appropriate for testing
	}

	// Initialize the logger with the test configuration
	logger.Initialize(testConfig)
}

func TestGetPodDbConfigWithAnnotationsModeURI(t *testing.T) {
	initTestLogger()
	cfg := config.Config{}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				k8s.ANNOTATION_VAULT_DB_PATH:                       "custom-engine-path",
				k8s.ANNOTATION_ROLE:                                "db-role",
				"db-creds-injector.numberly.io/testdb.env-key-uri": "TESTDB_URI",
				"db-creds-injector.numberly.io/testdb.template":    "TEMPLATE_STRING",
				"db-creds-injector.numberly.io/testdb.mode":        "uri",
			},
		},
	}

	service := k8s.NewService(cfg, pod)
	podDbConfig, err := service.GetPodDbConfig()

	assert.NoError(t, err)
	assert.Equal(t, "custom-engine-path", podDbConfig.VaultDbPath)
	assert.NotEmpty(t, podDbConfig.DbConfigurations)

	dbConfigs := *podDbConfig.DbConfigurations
	assert.Len(t, dbConfigs, 1)
	assert.Equal(t, "db-role", dbConfigs[0].Role)
	assert.Equal(t, "TESTDB_URI", dbConfigs[0].DbURIEnvKey)
	assert.Equal(t, "TEMPLATE_STRING", dbConfigs[0].Template)
	assert.Equal(t, "uri", dbConfigs[0].Mode)
	assert.Equal(t, "testdb", dbConfigs[0].DbName)
}

func TestGetPodDbConfigWithAnnotationsModeClassic(t *testing.T) {
	initTestLogger()
	cfg := config.Config{}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				k8s.ANNOTATION_VAULT_DB_PATH:                              "custom-engine-path",
				k8s.ANNOTATION_ROLE:                                       "db-role",
				"db-creds-injector.numberly.io/testdb.env-key-dbuser":     "TESTDB_USER",
				"db-creds-injector.numberly.io/testdb.env-key-dbpassword": "TESTDB_PASSWORD",
				"db-creds-injector.numberly.io/testdb.mode":               "classic",
			},
		},
	}

	service := k8s.NewService(cfg, pod)
	podDbConfig, err := service.GetPodDbConfig()

	assert.NoError(t, err)
	assert.Equal(t, "custom-engine-path", podDbConfig.VaultDbPath)
	assert.NotEmpty(t, podDbConfig.DbConfigurations)

	dbConfigs := *podDbConfig.DbConfigurations
	assert.Len(t, dbConfigs, 1)
	assert.Equal(t, "db-role", dbConfigs[0].Role)
	assert.Equal(t, "TESTDB_USER", dbConfigs[0].DbUserEnvKey)
	assert.Equal(t, "TESTDB_PASSWORD", dbConfigs[0].DbPasswordEnvKey)
	assert.Equal(t, "classic", dbConfigs[0].Mode)
	assert.Equal(t, "testdb", dbConfigs[0].DbName)
}

func TestGetPodDbConfigWithAnnotationsModeClassicWithoutDbPath(t *testing.T) {
	initTestLogger()
	cfg := config.Config{}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				k8s.ANNOTATION_ROLE: "db-role",
				"db-creds-injector.numberly.io/testdb.env-key-dbuser":     "TESTDB_USER",
				"db-creds-injector.numberly.io/testdb.env-key-dbpassword": "TESTDB_PASSWORD",
				"db-creds-injector.numberly.io/testdb.mode":               "classic",
			},
		},
	}

	service := k8s.NewService(cfg, pod)
	podDbConfig, err := service.GetPodDbConfig()

	assert.NoError(t, err)
	assert.NotEmpty(t, podDbConfig.DbConfigurations)

	dbConfigs := *podDbConfig.DbConfigurations
	assert.Len(t, dbConfigs, 1)
	assert.Equal(t, "db-role", dbConfigs[0].Role)
	assert.Equal(t, "TESTDB_USER", dbConfigs[0].DbUserEnvKey)
	assert.Equal(t, "TESTDB_PASSWORD", dbConfigs[0].DbPasswordEnvKey)
	assert.Equal(t, "classic", dbConfigs[0].Mode)
	assert.Equal(t, "testdb", dbConfigs[0].DbName)
}
