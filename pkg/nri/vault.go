package nri

import (
	"context"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"github.com/numberly/vault-db-injector/pkg/vault"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fetchAndBuildMapping looks up the pod from the K8s API to get its
// authoritative identity (UID, namespace, serviceAccountName) and
// configuration annotations, authenticates to Vault as the plugin's
// own SA, runs CanIGetRoles for the actual pod identity, and creates
// dynamic database credentials.
//
// Returns a placeholder→real-value map. The plugin caller scans the
// container env for placeholders that correspond to the user's
// env-key-dbuser / env-key-dbpassword / env-key-uri annotations and
// substitutes them in.
//
// Trust model:
// - We never trust pod identity from any annotation. The pod's
//   namespace and serviceAccountName come from kube-apiserver.
// - The K8s API pod.UID must match the NRI sandbox UID (closes the
//   name-reuse race documented at #CRIT-1).
// - In legacy mode (UseProjectedSA=false), the webhook ran CanIGetRoles
//   at admission and we re-run it here against the K8s-attested identity
//   as defense in depth.
// - In projected mode (UseProjectedSA=true), Vault performs the equivalent
//   attestation natively via bound_service_account_names during the per-pod
//   login. CanIGetRoles is not called. The Vault role MUST be configured
//   with token_period > 0 so the pod-token (and its lease) survives until
//   explicit revocation.
func fetchAndBuildMapping(ctx context.Context, cfg *config.Config, contextID, podNamespace, podName string, bookCache *vault.BookkeepingTokenCache) (mapping map[string]string, creds *vault.DbCreds, retErr error) {
	// liveConns accumulates every connector whose Login succeeded.
	// On partial failure (iteration K fails after 0..K-1 succeeded) the
	// deferred cleanup revokes all pod-tokens and bookkeeping tokens that
	// were already issued, preventing silent leaks until token_max_ttl.
	var liveConns []*vault.Connector
	defer func() {
		if retErr != nil && cfg.UseProjectedSA {
			for _, c := range liveConns {
				// Revoke the pod-token (current token after Login).
				if podTok := c.GetToken(); podTok != "" {
					if revErr := c.RevokeSelfToken(context.Background(), podTok); revErr != nil {
						logger.GetLogger().Warnf("RevokeSelfToken (pod-token) failed during multi-dbConfig cleanup: %v", revErr)
					}
				}
				// Revoke the bookkeeping injector-SA token separately.
				if c.K8sSaVaultToken != "" {
					if revErr := c.RevokeSelfToken(context.Background(), c.K8sSaVaultToken); revErr != nil {
						logger.GetLogger().Warnf("RevokeSelfToken (bookkeeping) failed during multi-dbConfig cleanup: %v", revErr)
					}
				}
			}
		}
	}()

	k8sClient := k8s.NewClient()
	clientset, err := k8sClient.GetKubernetesClient()
	if err != nil {
		return nil, nil, errors.Wrap(err, "k8s clientset")
	}
	pod, err := clientset.CoreV1().Pods(podNamespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return nil, nil, errors.Wrapf(err, "get pod %s/%s", podNamespace, podName)
	}
	// UID equality closes a name-reuse race: if the original pod was
	// force-deleted between admission and CreateContainer, an attacker
	// could otherwise hijack the credential fetch with a recreated pod
	// of the same name+namespace.
	if string(pod.UID) != contextID {
		return nil, nil, errors.Newf(
			"pod UID mismatch: NRI sandbox UID %s != API pod UID %s for %s/%s — refusing to fetch credentials",
			contextID, string(pod.UID), podNamespace, podName,
		)
	}
	actualSA := pod.Spec.ServiceAccountName
	if actualSA == "" {
		if cfg.UseProjectedSA {
			return nil, nil, errors.Newf(
				"pod %s/%s has empty serviceAccountName — refusing to TokenRequest in projected-SA mode",
				podNamespace, podName,
			)
		}
		actualSA = "default"
	}

	// Parse the user's existing db-creds-injector.numberly.io/*
	// annotations. Same parser the webhook uses at admission, no
	// duplication of logic.
	parser := k8s.NewParserService(*cfg, pod)
	pdc, err := parser.GetPodDbConfig(contextID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse pod db config")
	}
	if pdc.DbConfigurations == nil || len(*pdc.DbConfigurations) == 0 {
		return nil, nil, errors.New("pod has placeholders in env but no db-creds-injector annotations")
	}

	// Read the per-dbConfig UUIDs the webhook stamped at admission. Order
	// MUST match the dbConfigs returned by the parser (both the webhook
	// and parser iterate the same annotation set in the same order).
	var podUuids []string
	if uuidAnno := pod.Annotations["db-creds-injector.numberly.io/uuid"]; uuidAnno != "" {
		podUuids = strings.Split(uuidAnno, ",")
	}
	// Legacy path (pod admitted before this fix, or single-dbConfig pod
	// with no uuid annotation): fall back to pod UID for the first dbConfig.
	if len(podUuids) == 0 {
		podUuids = []string{contextID}
	}

	// Obtain the JWT for Vault login: pod's projected-SA in
	// useProjectedSA mode, plugin's own SA otherwise.
	tok, err := k8s.VaultLoginToken(ctx, k8sClient, pod, cfg.UseProjectedSA, cfg.TokenRequestAudiences, cfg.TokenRequestExpirationSeconds)
	if err != nil {
		metrics.TokenRequestErrors.WithLabelValues(k8s.ClassifyTokenRequestError(err)).Inc()
		return nil, nil, errors.Wrap(err, "vault login token")
	}

	// In projected-SA mode we also need the injector's own SA token for the
	// bookkeeping login. GetServiceAccountToken reads the binary's mounted SA,
	// which is always available regardless of mode.
	var injectorSaToken string
	if cfg.UseProjectedSA {
		injectorSaToken, err = k8sClient.GetServiceAccountToken()
		if err != nil {
			return nil, nil, errors.Wrap(err, "read injector SA token for bookkeeping login")
		}
	}

	// Merge placeholder→value maps from all dbConfigs into a single map.
	// If ANY dbConfig fails, return the error to fail the whole CreateContainer
	// (partial substitution is worse than no substitution).
	mergedMapping := map[string]string{}
	var lastCreds *vault.DbCreds

	// tok is the pod's TokenRequest JWT, valid for cfg.TokenRequestExpirationSeconds
	// (default 600s). All dbConfig iterations reuse the same JWT — if the loop
	// exceeds the JWT TTL (extreme edge case under Vault rate-limiting), later
	// iterations will fail with "jwt expired". The loop duration should be far
	// below 600s in practice.
	for i, dbConf := range *pdc.DbConfigurations {
		if err := checkConfigurationLite(dbConf); err != nil {
			return nil, nil, err
		}

		// Use the per-dbConfig UUID from the annotation when available;
		// fall back to contextID for out-of-range index (pre-fix pod).
		podNameUID := contextID
		if i < len(podUuids) {
			podNameUID = podUuids[i]
		}

		authRole := cfg.KubeRole
		if cfg.UseProjectedSA {
			authRole = dbConf.Role
		}
		conn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, authRole, pdc.VaultDbPath, dbConf.Role, tok, cfg.VaultRateLimit)
		if err := conn.Login(ctx); err != nil {
			mode := "legacy"
			if cfg.UseProjectedSA {
				mode = "projected"
			}
			metrics.VaultLoginErrors.WithLabelValues(vault.ClassifyLoginError(err), mode).Inc()
			return nil, nil, errors.Wrapf(err, "vault login for dbConfig %d (role %s)", i, dbConf.Role)
		}
		// Track this connector so the deferred cleanup can revoke its tokens
		// if a subsequent iteration fails.
		liveConns = append(liveConns, conn)
		if !cfg.UseProjectedSA {
			// In legacy mode the same login token serves both KV bookkeeping
			// and DB credential fetching. Record it so the renewer/revoker
			// pipeline can re-authenticate using this token.
			conn.K8sSaVaultToken = conn.GetToken()
		}

		if !cfg.UseProjectedSA {
			ok, err := conn.CanIGetRoles(ctx, contextID, actualSA, podNamespace, cfg.VaultAuthPath, dbConf.Role)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "vault CanIGetRoles for dbConfig %d (role %s)", i, dbConf.Role)
			}
			if !ok {
				return nil, nil, errors.Newf("pod %s/%s not authorized for vault role %s", podNamespace, actualSA, dbConf.Role)
			}
		} else {
			// Vault attests the pod identity natively via bound_service_account_names
			// during Login above; CanIGetRoles is redundant. Check token_period in projected mode.
			if period, err := conn.VerifyTokenPeriod(ctx); err != nil {
				logger.GetLogger().WithFields(map[string]interface{}{"role": dbConf.Role}).Debugf("VerifyTokenPeriod lookup-self failed: %v", err)
			} else if period == 0 {
				metrics.ProjectedRoleMisconfigured.WithLabelValues(dbConf.Role).Inc()
				logger.GetLogger().WithFields(map[string]interface{}{"role": dbConf.Role}).Warnf("vault role has no token_period — pod-token (and its lease) will die at max_ttl; configure token_period > 0")
			}

			// The pod-token has no KV-write capability. Perform a separate
			// injector-SA login to get a token used exclusively by StoreDataAsync
			// for bookkeeping writes. See vault-roles-and-policies.md §2a.
			// Use KubeRoleNri when configured to enable privilege separation
			// between webhook and NRI DaemonSet; fall back to KubeRole.
			nriBookRole := cfg.KubeRoleNri
			if nriBookRole == "" {
				nriBookRole = cfg.KubeRole
			}
			var bookToken string
			var bookErr error
			if bookCache != nil {
				bookToken, bookErr = bookCache.Get(ctx, cfg, injectorSaToken, nriBookRole)
			} else {
				bookToken, bookErr = vault.LoginAsInjectorSA(ctx, cfg, injectorSaToken, nriBookRole)
			}
			if bookErr != nil {
				metrics.VaultLoginErrors.WithLabelValues(vault.ClassifyLoginError(bookErr), "projected_bookkeeping").Inc()
				return nil, nil, errors.Wrapf(bookErr, "vault login as injector SA for bookkeeping (dbConfig %d)", i)
			}
			conn.K8sSaVaultToken = bookToken
		}

		creds, err = conn.GetDbCredentials(ctx, vault.DbCredentialsRequest{
			ContextID:          contextID,
			TTL:                cfg.TokenTTL,
			PodNameUID:         podNameUID,
			Namespace:          podNamespace,
			SecretName:         cfg.VaultSecretName,
			Prefix:             cfg.VaultSecretPrefix,
			ServiceAccount:     actualSA,
			SkipOrphanCreation: cfg.UseProjectedSA,
		})
		if err != nil {
			return nil, nil, errors.Wrapf(err, "vault GetDbCredentials for dbConfig %d (role %s)", i, dbConf.Role)
		}
		creds.PodUUID = podNameUID

		// Walk the container env (across init + main containers) to find
		// placeholders that correspond to dbConf.DbUserEnvKey /
		// DbPasswordEnvKey / DbURIEnvKey, and map them to the correct
		// credential field.
		partial := buildMappingFromPodEnv(pod, dbConf, creds)
		for ph, val := range partial {
			mergedMapping[ph] = val
		}
		lastCreds = creds
	}

	if len(mergedMapping) == 0 {
		return nil, nil, errors.New("no placeholders matched any dbConfiguration env keys")
	}
	return mergedMapping, lastCreds, nil
}

func buildMappingFromPodEnv(pod *corev1.Pod, dbConf k8s.DbConfiguration, creds *vault.DbCreds) map[string]string {
	out := map[string]string{}
	all := []corev1.EnvVar{}
	for _, c := range pod.Spec.InitContainers {
		all = append(all, c.Env...)
	}
	for _, c := range pod.Spec.Containers {
		all = append(all, c.Env...)
	}
	envLines := make([]string, 0, len(all))
	for _, e := range all {
		envLines = append(envLines, e.Name+"="+e.Value)
	}
	field := extractPlaceholdersFromEnv(envLines, dbConf)
	for ph, key := range field {
		switch key {
		case "username":
			out[ph] = creds.Username
		case "password":
			out[ph] = creds.Password
		}
	}
	return out
}

func checkConfigurationLite(dbConf k8s.DbConfiguration) error {
	if dbConf.Role == "" {
		return errors.New("dbConfiguration missing role")
	}
	return nil
}

