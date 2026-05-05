package k8s_test

import (
	"testing"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPodDbConfigWithoutAnnotations(t *testing.T) {
	cfg := config.Config{
		DefaultEngine: "default-engine-path",
	}
	pod := &corev1.Pod{} // Mock pod without any annotations

	service := k8s.NewParserService(cfg, pod)
	podDbConfig, err := service.GetPodDbConfig("id-1")

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

	service := k8s.NewParserService(cfg, pod)
	podDbConfig, err := service.GetPodDbConfig("id-1")

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

	service := k8s.NewParserService(cfg, pod)
	podDbConfig, err := service.GetPodDbConfig("id-1")

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

// TestGetPodDbConfig_DeterministicOrder verifies that GetPodDbConfig always
// returns dbConfigurations sorted by DbName regardless of Go's map iteration
// order. This is a regression guard for I1: the webhook and NRI plugin run in
// separate processes and must agree on the slice index → UUID mapping encoded
// in the pod annotation.
func TestGetPodDbConfig_DeterministicOrder(t *testing.T) {
	initTestLogger()
	cfg := config.Config{}

	// Five dbConfigs in a map that Go will iterate in random order.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				k8s.ANNOTATION_ROLE: "db-role",
				// zebra, alpha, mango, beta, kiwi — intentionally unsorted
				"db-creds-injector.numberly.io/zebra.env-key-dbuser":      "ZEBRA_USER",
				"db-creds-injector.numberly.io/zebra.env-key-dbpassword":  "ZEBRA_PASS",
				"db-creds-injector.numberly.io/alpha.env-key-dbuser":     "ALPHA_USER",
				"db-creds-injector.numberly.io/alpha.env-key-dbpassword": "ALPHA_PASS",
				"db-creds-injector.numberly.io/mango.env-key-dbuser":     "MANGO_USER",
				"db-creds-injector.numberly.io/mango.env-key-dbpassword": "MANGO_PASS",
				"db-creds-injector.numberly.io/beta.env-key-dbuser":      "BETA_USER",
				"db-creds-injector.numberly.io/beta.env-key-dbpassword":  "BETA_PASS",
				"db-creds-injector.numberly.io/kiwi.env-key-dbuser":      "KIWI_USER",
				"db-creds-injector.numberly.io/kiwi.env-key-dbpassword":  "KIWI_PASS",
			},
		},
	}

	wantOrder := []string{"alpha", "beta", "kiwi", "mango", "zebra"}

	// Call 50 times: Go randomises map iteration, so a non-sorted
	// implementation will produce different orders across calls.
	var first []string
	service := k8s.NewParserService(cfg, pod)
	for iter := 0; iter < 50; iter++ {
		pdc, err := service.GetPodDbConfig("det-order-test")
		require.NoError(t, err)
		require.NotNil(t, pdc.DbConfigurations)
		got := *pdc.DbConfigurations
		require.Len(t, got, 5)

		names := make([]string, len(got))
		for i, dc := range got {
			names[i] = dc.DbName
		}

		if iter == 0 {
			first = names
			assert.Equal(t, wantOrder, names, "iteration 0: not sorted by DbName")
		} else {
			assert.Equal(t, first, names, "iteration %d: order changed across calls", iter)
		}
	}
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

	service := k8s.NewParserService(cfg, pod)
	podDbConfig, err := service.GetPodDbConfig("id-1")

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
