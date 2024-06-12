package k8smutator

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	promInjector "github.com/numberly/vault-db-injector/pkg/prometheus"
	"github.com/numberly/vault-db-injector/pkg/vault"

	"github.com/cockroachdb/errors"
	"github.com/google/uuid"
	"github.com/slok/kubewebhook/v2/pkg/log"

	kwhmodel "github.com/slok/kubewebhook/v2/pkg/model"
	kwhmutating "github.com/slok/kubewebhook/v2/pkg/webhook/mutating"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func CreateMutator(ctx context.Context, logger log.Logger, cfg *config.Config) kwhmutating.MutatorFunc {
	k8sClient := k8s.NewClient()
	// Create mutator.
	return kwhmutating.MutatorFunc(func(_ context.Context, _ *kwhmodel.AdmissionReview, obj metav1.Object) (*kwhmutating.MutatorResult, error) {

		defaultResult := &kwhmutating.MutatorResult{
			MutatedObject: obj,
		}
		// Get pod
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			return &kwhmutating.MutatorResult{}, nil
		}
		logger.Infof("mutating pod %s/%s", pod.Namespace, pod.UID)
		// Get config from pod annotations
		podLib := k8s.NewService(*cfg, pod)
		podDbConfig, err := podLib.GetPodDbConfig()
		if err != nil {
			if err.Error() == "this pod is going to be ignored, old annotation from vault injector v1 detected" {
				return defaultResult, nil
			}
			return defaultResult, errors.Wrap(err, "failed to get Pod DB configuration")
		}

		// Request token from k8s serviceAccount
		tok, err := k8sClient.GetServiceAccountToken()
		if err != nil {
			return defaultResult, errors.Wrap(err, "cannot get ServiceAccount token")
		}
		logger.Debugf("got token from serviceAccount Successfully")

		mutatedPod, role, podUuids, err := handlePodConfiguration(ctx, cfg, podDbConfig.DbConfigurations, logger, podDbConfig.VaultDbPath, tok, pod)
		if err != nil || mutatedPod == nil {
			promInjector.MutatedPodWithErrorCount.WithLabelValues().Inc()
			return defaultResult, errors.Wrapf(err, "cannot get database credentials from role %s", role)
		}
		// Inject creds into containers env

		if mutatedPod.Annotations == nil {
			mutatedPod.Annotations = make(map[string]string)
		}
		mutatedPod.Annotations["db-creds-injector.numberly.io/uuid"] = strings.Join(podUuids, ",")

		logger.Infof("returning injected pod %s", mutatedPod.Namespace)
		promInjector.MutatedPodWithSucessCount.WithLabelValues().Inc()
		return &kwhmutating.MutatorResult{
			MutatedObject: mutatedPod,
		}, nil
	})
}

func generateUUID(logger log.Logger) string {
	newUUID, err := uuid.NewUUID()
	if err != nil {
		logger.Infof("Error while generating UUID : %v", err)
	}
	return newUUID.String()
}

func handlePodConfiguration(ctx context.Context, cfg *config.Config, dbConfs *[]k8s.DbConfiguration, logger log.Logger, vaultDbPath, tok string, pod *corev1.Pod) (*corev1.Pod, string, []string, error) {
	if len(*dbConfs) > 0 {
		podUuids := make([]string, 0, len(*dbConfs))
		for _, dbConf := range *dbConfs {
			// Configure vault connection using serviceAccount token
			err := checkConfiguration(dbConf)
			if err != nil {
				logger.Errorf("Their is an issue with the db Configuration")
				return nil, "db-role not found", nil, err
			}
			vaultConn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, vaultDbPath, dbConf.Role, tok, cfg.VaultRateLimit)
			if err := vaultConn.Login(ctx); err != nil {
				return nil, dbConf.Role, nil, errors.Newf("cannot authenticate vault role: %s", err.Error())
			}
			vaultConn.K8sSaVaultToken = vaultConn.GetToken()
			logger.Debugf("authenticated to vault using role %s/%s", cfg.VaultAuthPath, dbConf.Role)

			serviceAccountName := pod.Spec.ServiceAccountName
			ok, err := vaultConn.CanIGetRoles(serviceAccountName, pod.Namespace, cfg.VaultAuthPath, dbConf.Role)
			if !ok || err != nil {
				vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken, "", "")
				return nil, dbConf.Role, nil, err
			}
			podUuid := generateUUID(logger)
			podUuids = append(podUuids, podUuid)
			// Request temporary database credentials from vault using configured role
			creds, err := vaultConn.GetDbCredentials(ctx, cfg.TokenTTL, podUuid, pod.Namespace, cfg.VaultSecretName, cfg.VaultSecretPrefix, pod.Spec.ServiceAccountName)
			if err != nil {
				vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken, "", "")
				return nil, dbConf.Role, nil, errors.Newf("cannot get database credentials from role %s: %s", dbConf.Role, err.Error())
			}
			logger.Debugf("got DB credentials using role %s", dbConf.Role)

			logger.Infof("DbConfMode is equal to : %s", dbConf.Mode)

			if dbConf.Mode == "" || dbConf.Mode == "classic" {
				dbUserKeys := strings.Split(dbConf.DbUserEnvKey, ",")
				dbPasswordKeys := strings.Split(dbConf.DbPasswordEnvKey, ",")

				for i := range pod.Spec.InitContainers {
					for _, userKey := range dbUserKeys {
						envVar := corev1.EnvVar{Name: userKey, Value: creds.Username}
						pod.Spec.InitContainers[i].Env = append(pod.Spec.InitContainers[i].Env, envVar)
					}
					for _, passKey := range dbPasswordKeys {
						envVar := corev1.EnvVar{Name: passKey, Value: creds.Password}
						pod.Spec.InitContainers[i].Env = append(pod.Spec.InitContainers[i].Env, envVar)
					}
				}

				for i := range pod.Spec.Containers {
					for _, userKey := range dbUserKeys {
						envVar := corev1.EnvVar{Name: userKey, Value: creds.Username}
						pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, envVar)
					}
					for _, passKey := range dbPasswordKeys {
						envVar := corev1.EnvVar{Name: passKey, Value: creds.Password}
						pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, envVar)
					}
				}
			} else if dbConf.Mode == "uri" || dbConf.Mode == "URI" || dbConf.Mode == "Uri" {

				dsnURL, err := url.Parse(dbConf.Template)
				if err != nil {
					fmt.Printf("Error parsing DSN: %v\n", err)
					vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken, "", "")
					return nil, dbConf.Role, nil, err
				}
				dsnURL.User = url.UserPassword(creds.Username, creds.Password)
				updatedDSN := dsnURL.String()
				dbUriEnvKey := strings.Split(dbConf.DbURIEnvKey, ",")

				// Append the constructed URI to all init containers and containers
				for i := range pod.Spec.InitContainers {
					for _, dbUri := range dbUriEnvKey {
						envVar := corev1.EnvVar{Name: dbUri, Value: updatedDSN}
						pod.Spec.InitContainers[i].Env = append(pod.Spec.InitContainers[i].Env, envVar)
					}
				}
				for i := range pod.Spec.Containers {
					for _, dbUri := range dbUriEnvKey {
						envVar := corev1.EnvVar{Name: dbUri, Value: updatedDSN}
						pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, envVar)
					}
				}
			} else {
				vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken, "", "")
				return nil, dbConf.Role, nil, errors.Newf("mode not supported : %s", dbConf.Mode)
			}
		}
		return pod, "", podUuids, nil
	}
	return nil, "", nil, errors.Newf("No dbConfiguration has been provided %v", dbConfs)
}

func checkConfiguration(dbConf k8s.DbConfiguration) error {
	if dbConf.DbName == "" {
		// Failing if either DbAddress or DbName is empty
		return errors.New("missing required database configuration: DbName must be specified")
	}
	if dbConf.Role == "" {
		// Failing if either DbAddress or DbName is empty
		return errors.New("missing required database configuration: Role must be specified")
	}
	return nil
}
