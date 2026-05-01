package k8smutator

import (
	"context"
	"net/url"
	"strings"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"github.com/numberly/vault-db-injector/pkg/vault"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/slok/kubewebhook/v2/pkg/log"

	kwhmodel "github.com/slok/kubewebhook/v2/pkg/model"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func generateUUID(logger log.Logger) string {
	newUUID, err := uuid.NewUUID()
	if err != nil {
		logger.Infof("Error while generating UUID : %v", err)
	}
	return newUUID.String()
}

func CreateMutator(ctx context.Context, logger log.Logger, cfg *config.Config) kwhmutating.MutatorFunc {
	k8sClient := k8s.NewClient()
	return kwhmutating.MutatorFunc(func(_ context.Context, _ *kwhmodel.AdmissionReview, obj metav1.Object) (*kwhmutating.MutatorResult, error) {

		contextID := generateUUID(logger)

		defaultResult := &kwhmutating.MutatorResult{
			MutatedObject: obj,
		}
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

		tok, err := k8sClient.GetServiceAccountToken()
		if err != nil {
			return defaultResult, errors.Wrap(err, "cannot get ServiceAccount token")
		}
		logger.WithValues(log.Kv{"contextID": contextID}).Debugf("got token from serviceAccount Successfully")

		mutatedPod, role, podUuids, err := injectCredentialsIntoPod(ctx, contextID, cfg, podDbConfig.DbConfigurations, logger, podDbConfig.VaultDbPath, tok, pod)
		if err != nil || mutatedPod == nil {
			metrics.MutatedPodWithErrorCount.WithLabelValues().Inc()
			return defaultResult, errors.Wrapf(err, "cannot get database credentials from role %s", role)
		}
		if mutatedPod.Annotations == nil {
			mutatedPod.Annotations = make(map[string]string)
		}
		mutatedPod.Annotations["db-creds-injector.numberly.io/uuid"] = strings.Join(podUuids, ",")

		logger.WithValues(log.Kv{"contextID": contextID}).Infof("returning injected pod %s", mutatedPod.Namespace)
		metrics.MutatedPodWithSuccessCount.WithLabelValues().Inc()
		return &kwhmutating.MutatorResult{
			MutatedObject: mutatedPod,
		}, nil
	})
}

// acquireDbCredentials authenticates to Vault, verifies RBAC, and retrieves database credentials.
// The caller is responsible for revoking vaultConn.K8sSaVaultToken on error or after use.
func acquireDbCredentials(ctx context.Context, contextID string, cfg *config.Config, dbConf k8s.DbConfiguration, logger log.Logger, vaultDbPath, tok string, pod *corev1.Pod) (*vault.DbCreds, *vault.Connector, string, error) {
	vaultConn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, vaultDbPath, dbConf.Role, tok, cfg.VaultRateLimit)
	if err := vaultConn.Login(ctx); err != nil {
		return nil, nil, dbConf.Role, errors.Newf("cannot authenticate vault role: %s", err.Error())
	}
	vaultConn.K8sSaVaultToken = vaultConn.GetToken()
	logger.WithValues(log.Kv{"contextID": contextID}).Debugf("authenticated to vault using role %s/%s", cfg.VaultAuthPath, dbConf.Role)

	serviceAccountName := pod.Spec.ServiceAccountName
	ok, err := vaultConn.CanIGetRoles(ctx, contextID, serviceAccountName, pod.Namespace, cfg.VaultAuthPath, dbConf.Role)
	if !ok || err != nil {
		return nil, vaultConn, dbConf.Role, err
	}

	podUuid := generateUUID(logger)
	creds, err := vaultConn.GetDbCredentials(ctx, vault.DbCredentialsRequest{
		ContextID:      contextID,
		TTL:            cfg.TokenTTL,
		PodNameUID:     podUuid,
		Namespace:      pod.Namespace,
		SecretName:     cfg.VaultSecretName,
		Prefix:         cfg.VaultSecretPrefix,
		ServiceAccount: pod.Spec.ServiceAccountName,
	})
	if err != nil {
		return nil, vaultConn, dbConf.Role, errors.Newf("cannot get database credentials from role %s: %s", dbConf.Role, err.Error())
	}
	logger.WithValues(log.Kv{"contextID": contextID}).Debugf("got DB credentials using role %s", dbConf.Role)
	creds.PodUUID = podUuid
	return creds, vaultConn, dbConf.Role, nil
}

func applyEnvToContainers(pod *corev1.Pod, dbConf k8s.DbConfiguration, creds *vault.DbCreds) error {
	mode := strings.ToLower(dbConf.Mode)
	switch mode {
	case "", k8s.DbModeClassic:
		dbUserKeys := strings.Split(dbConf.DbUserEnvKey, ",")
		dbPasswordKeys := strings.Split(dbConf.DbPasswordEnvKey, ",")
		for i := range pod.Spec.InitContainers {
			for _, userKey := range dbUserKeys {
				pod.Spec.InitContainers[i].Env = append(pod.Spec.InitContainers[i].Env, corev1.EnvVar{Name: userKey, Value: creds.Username})
			}
			for _, passKey := range dbPasswordKeys {
				pod.Spec.InitContainers[i].Env = append(pod.Spec.InitContainers[i].Env, corev1.EnvVar{Name: passKey, Value: creds.Password})
			}
		}
		for i := range pod.Spec.Containers {
			for _, userKey := range dbUserKeys {
				pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, corev1.EnvVar{Name: userKey, Value: creds.Username})
			}
			for _, passKey := range dbPasswordKeys {
				pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, corev1.EnvVar{Name: passKey, Value: creds.Password})
			}
		}

	case k8s.DbModeURI:
		dsnURL, err := url.Parse(dbConf.Template)
		if err != nil {
			return errors.Wrap(err, "error parsing DSN template")
		}
		dsnURL.User = url.UserPassword(creds.Username, creds.Password)
		updatedDSN := dsnURL.String()
		dbUriEnvKey := strings.Split(dbConf.DbURIEnvKey, ",")
		for i := range pod.Spec.InitContainers {
			for _, dbUri := range dbUriEnvKey {
				pod.Spec.InitContainers[i].Env = append(pod.Spec.InitContainers[i].Env, corev1.EnvVar{Name: dbUri, Value: updatedDSN})
			}
		}
		for i := range pod.Spec.Containers {
			for _, dbUri := range dbUriEnvKey {
				pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, corev1.EnvVar{Name: dbUri, Value: updatedDSN})
			}
		}

	default:
		return errors.Newf("mode not supported : %s", dbConf.Mode)
	}
	return nil
}

func injectCredentialsIntoPod(ctx context.Context, contextID string, cfg *config.Config, dbConfs *[]k8s.DbConfiguration, logger log.Logger, vaultDbPath, tok string, pod *corev1.Pod) (*corev1.Pod, string, []string, error) {
	if len(*dbConfs) == 0 {
		return nil, "", nil, errors.Newf("No dbConfiguration has been provided %v", dbConfs)
	}

	podUuids := make([]string, 0, len(*dbConfs))
	for _, dbConf := range *dbConfs {
		if err := checkConfiguration(dbConf); err != nil {
			logger.WithValues(log.Kv{"contextID": contextID}).Errorf("Their is an issue with the db Configuration")
			return nil, "db-role not found", nil, err
		}

		creds, vaultConn, role, err := acquireDbCredentials(ctx, contextID, cfg, dbConf, logger, vaultDbPath, tok, pod)
		if err != nil {
			if vaultConn != nil {
				if revokeErr := vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken); revokeErr != nil {
					logger.WithValues(log.Kv{"contextID": contextID}).Errorf("RevokeSelfToken failed: %v", revokeErr)
				}
			}
			return nil, role, nil, err
		}

		logger.WithValues(log.Kv{"contextID": contextID}).Infof("DbConfMode is equal to : %s", dbConf.Mode)

		if err := applyEnvToContainers(pod, dbConf, creds); err != nil {
			if revokeErr := vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken); revokeErr != nil {
				logger.WithValues(log.Kv{"contextID": contextID}).Errorf("RevokeSelfToken failed: %v", revokeErr)
			}
			return nil, role, nil, err
		}

		podUuids = append(podUuids, creds.PodUUID)
	}
	return pod, "", podUuids, nil
}

func checkConfiguration(dbConf k8s.DbConfiguration) error {
	if dbConf.DbName == "" {
		return errors.New("missing required database configuration: DbName must be specified")
	}
	if dbConf.Role == "" {
		return errors.New("missing required database configuration: Role must be specified")
	}
	return nil
}
