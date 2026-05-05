package k8smutator

import (
	"context"
	"net/url"
	"strings"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"github.com/numberly/vault-db-injector/pkg/placeholder"
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
	return kwhmutating.MutatorFunc(func(admCtx context.Context, _ *kwhmodel.AdmissionReview, obj metav1.Object) (*kwhmutating.MutatorResult, error) {

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

		tok, err := k8s.VaultLoginToken(admCtx, k8sClient, pod, cfg.UseProjectedSA, cfg.TokenRequestAudiences, cfg.TokenRequestExpirationSeconds)
		if err != nil {
			metrics.TokenRequestErrors.WithLabelValues(k8s.ClassifyTokenRequestError(err)).Inc()
			return defaultResult, errors.Wrap(err, "obtain Vault login token")
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
		// In NRI transparent mode no creds are fetched at admission so podUuids
		// is empty — writing "" would collide every NRI pod onto a single map
		// key in the renewer's lookup. Skip the annotation entirely; the
		// renewer falls back to pod.UID (which the NRI plugin uses as
		// PodNameUID when storing the KV entry).
		if len(podUuids) > 0 {
			mutatedPod.Annotations["db-creds-injector.numberly.io/uuid"] = strings.Join(podUuids, ",")
		}

		logger.WithValues(log.Kv{"contextID": contextID}).Infof("returning injected pod %s", mutatedPod.Namespace)
		metrics.MutatedPodWithSuccessCount.WithLabelValues().Inc()
		return &kwhmutating.MutatorResult{
			MutatedObject: mutatedPod,
		}, nil
	})
}

// authorizeDbAccess authenticates to Vault and verifies the pod's SA can use the role.
// In NRI mode this is the only Vault interaction at admission time — the actual
// credential is fetched by the plugin at CreateContainer.
//
// The caller is responsible for revoking vaultConn.K8sSaVaultToken on error or after use.
func authorizeDbAccess(ctx context.Context, contextID string, cfg *config.Config, dbConf k8s.DbConfiguration, logger log.Logger, vaultDbPath, tok string, pod *corev1.Pod) (*vault.Connector, string, error) {
	authRole := cfg.KubeRole
	if cfg.UseProjectedSA {
		authRole = dbConf.Role
	}
	vaultConn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, authRole, vaultDbPath, dbConf.Role, tok, cfg.VaultRateLimit)
	if err := vaultConn.Login(ctx); err != nil {
		mode := "legacy"
		if cfg.UseProjectedSA {
			mode = "projected"
		}
		metrics.VaultLoginErrors.WithLabelValues("other", mode).Inc()
		return nil, dbConf.Role, errors.Newf("cannot authenticate vault role: %s", err.Error())
	}
	if cfg.UseProjectedSA {
		vaultConn.PodVaultToken = vaultConn.GetToken()
	} else {
		vaultConn.K8sSaVaultToken = vaultConn.GetToken()
	}
	logger.WithValues(log.Kv{"contextID": contextID}).Debugf("authenticated to vault using role %s/%s", cfg.VaultAuthPath, authRole)

	if cfg.UseProjectedSA {
		// Vault attests the pod identity natively via bound_service_account_names
		// during Login above; CanIGetRoles is redundant.
		if period, err := vaultConn.VerifyTokenPeriod(ctx); err == nil && period == 0 {
			metrics.ProjectedRoleMisconfigured.WithLabelValues(dbConf.Role).Inc()
			logger.WithValues(log.Kv{"role": dbConf.Role}).Warningf("vault role has no token_period — pod-token (and its lease) will die at max_ttl; configure token_period > 0")
		}
		return vaultConn, dbConf.Role, nil
	}

	serviceAccountName := pod.Spec.ServiceAccountName
	ok, err := vaultConn.CanIGetRoles(ctx, contextID, serviceAccountName, pod.Namespace, cfg.VaultAuthPath, dbConf.Role)
	if !ok || err != nil {
		return vaultConn, dbConf.Role, err
	}
	return vaultConn, dbConf.Role, nil
}

// fetchDbCredentials creates a dynamic database credential via Vault (legacy
// non-NRI path). Returns the credentials with a generated podUUID stamped
// into the lease metadata so the renewer/revoker can track the lease.
func fetchDbCredentials(ctx context.Context, contextID string, cfg *config.Config, dbConf k8s.DbConfiguration, logger log.Logger, vaultConn *vault.Connector, pod *corev1.Pod) (*vault.DbCreds, error) {
	podUuid := generateUUID(logger)
	creds, err := vaultConn.GetDbCredentials(ctx, vault.DbCredentialsRequest{
		ContextID:          contextID,
		TTL:                cfg.TokenTTL,
		PodNameUID:         podUuid,
		Namespace:          pod.Namespace,
		SecretName:         cfg.VaultSecretName,
		Prefix:             cfg.VaultSecretPrefix,
		ServiceAccount:     pod.Spec.ServiceAccountName,
		SkipOrphanCreation: cfg.UseProjectedSA,
	})
	if err != nil {
		return nil, errors.Newf("cannot get database credentials from role %s: %s", dbConf.Role, err.Error())
	}
	logger.WithValues(log.Kv{"contextID": contextID}).Debugf("got DB credentials using role %s", dbConf.Role)
	creds.PodUUID = podUuid
	return creds, nil
}

// applyEnvToContainers is the entry point used by injectCredentialsIntoPod.
//
// In legacy mode (cfg.NRI.Enabled=false) creds are pre-fetched by the
// caller and we put them directly in the env (cleartext in PodSpec).
//
// In NRI mode creds is nil — the webhook does not fetch credentials.
// We generate placeholders and put them in env. The plugin scans env
// at CreateContainer time, finds placeholders by their fixed shape,
// reads the user's existing db-creds-injector.numberly.io/* annotations
// to know which Vault role to use, fetches credentials, and substitutes.
// No additional annotation is added to the pod — NRI is transparent.
func applyEnvToContainers(_ context.Context, pod *corev1.Pod, dbConf k8s.DbConfiguration, creds *vault.DbCreds, vaultDbPath string, cfg *config.Config) error {
	return applyEnvToContainersWithNRI(pod, dbConf, creds, vaultDbPath, cfg.NRI.Enabled)
}

func applyEnvToContainersWithNRI(pod *corev1.Pod, dbConf k8s.DbConfiguration, creds *vault.DbCreds, vaultDbPath string, nriEnabled bool) error {
	mode := strings.ToLower(dbConf.Mode)
	switch mode {
	case "", k8s.DbModeClassic:
		return applyClassic(pod, dbConf, creds, vaultDbPath, nriEnabled)
	case k8s.DbModeURI:
		return applyURI(pod, dbConf, creds, vaultDbPath, nriEnabled)
	default:
		return errors.Newf("mode not supported : %s", dbConf.Mode)
	}
}

func applyClassic(pod *corev1.Pod, dbConf k8s.DbConfiguration, creds *vault.DbCreds, vaultDbPath string, nriEnabled bool) error {
	var userVal, passVal string
	if nriEnabled {
		userVal, passVal = generatePlaceholders()
	} else {
		userVal = creds.Username
		passVal = creds.Password
	}

	dbUserKeys := strings.Split(dbConf.DbUserEnvKey, ",")
	dbPasswordKeys := strings.Split(dbConf.DbPasswordEnvKey, ",")
	for i := range pod.Spec.InitContainers {
		for _, k := range dbUserKeys {
			pod.Spec.InitContainers[i].Env = append(pod.Spec.InitContainers[i].Env, corev1.EnvVar{Name: k, Value: userVal})
		}
		for _, k := range dbPasswordKeys {
			pod.Spec.InitContainers[i].Env = append(pod.Spec.InitContainers[i].Env, corev1.EnvVar{Name: k, Value: passVal})
		}
	}
	for i := range pod.Spec.Containers {
		for _, k := range dbUserKeys {
			pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, corev1.EnvVar{Name: k, Value: userVal})
		}
		for _, k := range dbPasswordKeys {
			pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, corev1.EnvVar{Name: k, Value: passVal})
		}
	}
	return nil
}

func applyURI(pod *corev1.Pod, dbConf k8s.DbConfiguration, creds *vault.DbCreds, vaultDbPath string, nriEnabled bool) error {
	dsnURL, err := url.Parse(dbConf.Template)
	if err != nil {
		return errors.Wrap(err, "error parsing DSN template")
	}

	var user, pass string
	if nriEnabled {
		user, pass = generatePlaceholders()
	} else {
		user = creds.Username
		pass = creds.Password
	}

	dsnURL.User = url.UserPassword(user, pass)
	updatedDSN := dsnURL.String()
	dbUriEnvKey := strings.Split(dbConf.DbURIEnvKey, ",")
	for i := range pod.Spec.InitContainers {
		for _, k := range dbUriEnvKey {
			pod.Spec.InitContainers[i].Env = append(pod.Spec.InitContainers[i].Env, corev1.EnvVar{Name: k, Value: updatedDSN})
		}
	}
	for i := range pod.Spec.Containers {
		for _, k := range dbUriEnvKey {
			pod.Spec.Containers[i].Env = append(pod.Spec.Containers[i].Env, corev1.EnvVar{Name: k, Value: updatedDSN})
		}
	}
	return nil
}

// generatePlaceholders returns a fresh (user, password) placeholder pair.
// In NRI mode the webhook puts these into env in lieu of the real
// credentials; the plugin scans env at CreateContainer time, finds them
// by their fixed shape, and substitutes the real values it fetched from
// Vault. No annotation is added to the pod — NRI is transparent.
func generatePlaceholders() (userPH, passPH string) {
	return placeholder.Generate(), placeholder.Generate()
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

		// Authorize the pod's SA against the requested Vault role. Always
		// runs — gives admission-time RBAC feedback in both legacy and NRI
		// modes.
		vaultConn, role, err := authorizeDbAccess(ctx, contextID, cfg, dbConf, logger, vaultDbPath, tok, pod)
		if err != nil {
			if vaultConn != nil && vaultConn.K8sSaVaultToken != "" {
				if revokeErr := vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken); revokeErr != nil {
					logger.WithValues(log.Kv{"contextID": contextID}).Errorf("RevokeSelfToken failed: %v", revokeErr)
				}
			}
			return nil, role, nil, err
		}

		logger.WithValues(log.Kv{"contextID": contextID}).Infof("DbConfMode is equal to : %s", dbConf.Mode)

		// In NRI mode, do NOT fetch credentials at admission — the plugin
		// fetches them at CreateContainer time using its own SA. This
		// eliminates the wrap-token-bearer-credential vulnerability where
		// any pods.get RBAC + vault network access could exfiltrate creds
		// from the pod annotation.
		var creds *vault.DbCreds
		if !cfg.NRI.Enabled {
			creds, err = fetchDbCredentials(ctx, contextID, cfg, dbConf, logger, vaultConn, pod)
			if err != nil {
				if vaultConn.K8sSaVaultToken != "" {
					if revokeErr := vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken); revokeErr != nil {
						logger.WithValues(log.Kv{"contextID": contextID}).Errorf("RevokeSelfToken failed: %v", revokeErr)
					}
				}
				return nil, role, nil, err
			}
		}

		if err := applyEnvToContainers(ctx, pod, dbConf, creds, vaultDbPath, cfg); err != nil {
			if vaultConn.K8sSaVaultToken != "" {
				if revokeErr := vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken); revokeErr != nil {
					logger.WithValues(log.Kv{"contextID": contextID}).Errorf("RevokeSelfToken failed: %v", revokeErr)
				}
			}
			return nil, role, nil, err
		}

		// Best-effort: revoke webhook's own vault SA token. Plugin will
		// authenticate independently when it fetches creds.
		if cfg.NRI.Enabled && vaultConn.K8sSaVaultToken != "" {
			if revokeErr := vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken); revokeErr != nil {
				logger.WithValues(log.Kv{"contextID": contextID}).Infof("RevokeSelfToken (NRI mode webhook) warning: %v", revokeErr)
			}
		}

		if creds != nil {
			podUuids = append(podUuids, creds.PodUUID)
		}
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
