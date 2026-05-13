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
	// bookkeepingCache is shared across all admissions in this process so that
	// the injector-SA Vault login is performed at most once per 30 minutes
	// rather than once per dbConfig per admission (I4).
	bookkeepingCache := vault.NewBookkeepingTokenCache()
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

		// In projected-SA mode the pod-token (tok) has no KV-write capability.
		// Pre-fetch the injector's own SA token so authorizeDbAccess can do a
		// separate injector-identity login used exclusively by StoreDataAsync.
		var injectorSaToken string
		if cfg.UseProjectedSA {
			injectorSaToken, err = k8sClient.GetServiceAccountToken()
			if err != nil {
				return defaultResult, errors.Wrap(err, "read injector SA token for bookkeeping login")
			}
		}

		mutatedPod, role, podUuids, err := injectCredentialsIntoPod(ctx, contextID, cfg, podDbConfig.DbConfigurations, logger, podDbConfig.VaultDbPath, tok, injectorSaToken, pod, bookkeepingCache)
		if err != nil || mutatedPod == nil {
			metrics.MutatedPodWithErrorCount.WithLabelValues().Inc()
			return defaultResult, errors.Wrapf(err, "cannot get database credentials from role %s", role)
		}
		if mutatedPod.Annotations == nil {
			mutatedPod.Annotations = make(map[string]string)
		}
		// In NRI mode, podUuids holds one pre-generated UUID per dbConfig
		// (stamped by injectCredentialsIntoPod). The annotation lists them
		// comma-separated in the same order as the parser returns dbConfigs,
		// so the NRI plugin can key each KV entry distinctly per dbConfig.
		// In legacy (non-NRI) mode, podUuids holds the UUID from each actual
		// credential fetch. Either way, skip the annotation when empty.
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
// injectorSaToken is the injector binary's own SA token, used in projected-SA mode
// to perform a separate bookkeeping login (the pod-token has no KV-write capability).
// Pass an empty string in legacy mode where it is not used.
//
// bookCache is the process-wide BookkeepingTokenCache used to avoid a fresh
// injector-SA Vault login on every admission (I4). Pass nil to fall back to a
// direct LoginAsInjectorSA call (used in tests).
//
// The caller is responsible for revoking vaultConn.K8sSaVaultToken on error or after use.
func authorizeDbAccess(ctx context.Context, contextID string, cfg *config.Config, dbConf k8s.DbConfiguration, logger log.Logger, vaultDbPath, tok, injectorSaToken string, pod *corev1.Pod, bookCache *vault.BookkeepingTokenCache) (*vault.Connector, string, error) {
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
		metrics.VaultLoginErrors.WithLabelValues(vault.ClassifyLoginError(err), mode).Inc()
		return nil, dbConf.Role, errors.Wrapf(err, "cannot authenticate vault role")
	}
	if !cfg.UseProjectedSA {
		vaultConn.K8sSaVaultToken = vaultConn.GetToken()
	}
	logger.WithValues(log.Kv{"contextID": contextID}).Debugf("authenticated to vault using role %s/%s", cfg.VaultAuthPath, authRole)

	if cfg.UseProjectedSA {
		// Vault attests the pod identity natively via bound_service_account_names
		// during Login above; CanIGetRoles is redundant.
		if period, err := vaultConn.VerifyTokenPeriod(ctx); err != nil {
			logger.WithValues(log.Kv{"role": dbConf.Role}).Debugf("VerifyTokenPeriod lookup-self failed: %v", err)
		} else if period == 0 {
			metrics.ProjectedRoleMisconfigured.WithLabelValues(dbConf.Role).Inc()
			logger.WithValues(log.Kv{"role": dbConf.Role}).Warningf("vault role has no token_period — pod-token (and its lease) will die at max_ttl; configure token_period > 0")
		}

		// The pod-token has no KV-write capability. Pre-fetch a separate
		// injector-SA Vault token used by StoreDataAsync for bookkeeping writes.
		// See docs/how-it-works/vault-roles-and-policies.md §2a.
		// Webhook always uses the base kubeRole for bookkeeping logins.
		// Use the process-wide cache to bound Vault auth load (I4).
		var bookToken string
		var bookErr error
		if bookCache != nil {
			bookToken, bookErr = bookCache.Get(ctx, cfg, injectorSaToken, cfg.KubeRole)
		} else {
			bookToken, bookErr = vault.LoginAsInjectorSA(ctx, cfg, injectorSaToken, cfg.KubeRole)
		}
		if bookErr != nil {
			metrics.VaultLoginErrors.WithLabelValues(vault.ClassifyLoginError(bookErr), "projected_bookkeeping").Inc()
			return vaultConn, dbConf.Role, errors.Wrap(bookErr, "vault login as injector SA for bookkeeping")
		}
		vaultConn.K8sSaVaultToken = bookToken
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
		return nil, errors.Wrapf(err, "cannot get database credentials from role %s", dbConf.Role)
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

func injectCredentialsIntoPod(ctx context.Context, contextID string, cfg *config.Config, dbConfs *[]k8s.DbConfiguration, logger log.Logger, vaultDbPath, tok, injectorSaToken string, pod *corev1.Pod, bookCache *vault.BookkeepingTokenCache) (retPod *corev1.Pod, retRole string, retUUIDs []string, retErr error) {
	if len(*dbConfs) == 0 {
		return nil, "", nil, errors.Newf("No dbConfiguration has been provided %v", dbConfs)
	}

	// liveConns tracks every connector whose Login succeeded this call.
	// On partial failure the deferred block revokes previously issued tokens
	// so they don't leak until token_max_ttl.
	//
	// IMPORTANT: in projected-SA mode, c.K8sSaVaultToken is the SHARED
	// bookkeeping token from BookkeepingTokenCache, reused across concurrent
	// admissions. Revoking it here invalidates it for everyone else still
	// holding the cached value and kills subsequent KV writes with 403
	// "permission denied" until the cache entry expires (30 min). The
	// bookkeeping token's lifecycle is owned by the cache, not this function.
	// In legacy mode, c.K8sSaVaultToken == c.GetToken() (per-call login
	// token, safe to revoke).
	var liveConns []*vault.Connector
	defer func() {
		if retErr != nil {
			for _, c := range liveConns {
				if cfg.UseProjectedSA {
					if podTok := c.GetToken(); podTok != "" {
						if revokeErr := c.RevokeSelfToken(ctx, podTok); revokeErr != nil {
							logger.WithValues(log.Kv{"contextID": contextID}).Errorf("RevokeSelfToken (pod-token) failed during cleanup: %v", revokeErr)
						}
					}
				} else if c.K8sSaVaultToken != "" {
					if revokeErr := c.RevokeSelfToken(ctx, c.K8sSaVaultToken); revokeErr != nil {
						logger.WithValues(log.Kv{"contextID": contextID}).Errorf("RevokeSelfToken (legacy login token) failed during cleanup: %v", revokeErr)
					}
				}
			}
		}
	}()

	podUuids := make([]string, 0, len(*dbConfs))
	for _, dbConf := range *dbConfs {
		if err := checkConfiguration(dbConf); err != nil {
			logger.WithValues(log.Kv{"contextID": contextID}).Errorf("Their is an issue with the db Configuration")
			return nil, "db-role not found", nil, err
		}

		// Authorize the pod's SA against the requested Vault role. Always
		// runs — gives admission-time RBAC feedback in both legacy and NRI
		// modes.
		vaultConn, role, err := authorizeDbAccess(ctx, contextID, cfg, dbConf, logger, vaultDbPath, tok, injectorSaToken, pod, bookCache)
		if err != nil {
			return nil, role, nil, err
		}
		// Track so deferred cleanup covers this connector on any later error.
		liveConns = append(liveConns, vaultConn)

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
				return nil, role, nil, err
			}
		}

		if err := applyEnvToContainers(ctx, pod, dbConf, creds, vaultDbPath, cfg); err != nil {
			return nil, role, nil, err
		}

		// Best-effort: in legacy NRI mode, revoke webhook's own login token
		// now that it's no longer needed (the NRI plugin authenticates
		// independently at CreateContainer time).
		//
		// IMPORTANT: in projected-SA mode, vaultConn.K8sSaVaultToken is the
		// SHARED bookkeeping token returned by BookkeepingTokenCache and
		// reused across every concurrent admission. Revoking it here would
		// invalidate it Vault-side for every other in-flight admission and
		// kill subsequent KV bookkeeping writes with 403 until the cache
		// expires (30 min). The bookkeeping token's lifecycle is owned by
		// the cache, not by this function. Same root cause as
		// fetchAndBuildMapping's defer cleanup (see commit da90242).
		if cfg.NRI.Enabled && !cfg.UseProjectedSA && vaultConn.K8sSaVaultToken != "" {
			if revokeErr := vaultConn.RevokeSelfToken(ctx, vaultConn.K8sSaVaultToken); revokeErr != nil {
				logger.WithValues(log.Kv{"contextID": contextID}).Infof("RevokeSelfToken (NRI mode webhook, legacy) warning: %v", revokeErr)
			}
			// Clear token so deferred cleanup skips it (already revoked).
			vaultConn.K8sSaVaultToken = ""
		}

		if creds != nil {
			podUuids = append(podUuids, creds.PodUUID)
		} else if cfg.NRI.Enabled {
			// NRI mode: webhook didn't fetch creds, but we still need a stable
			// per-dbConfig UUID so the NRI plugin and renewer/revoker can key
			// KV bookkeeping per dbConfig. Without this, multi-dbConfig pods
			// collide on the same pod-UID key and only the last write wins.
			podUuids = append(podUuids, generateUUID(logger))
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
