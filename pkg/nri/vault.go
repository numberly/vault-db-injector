package nri

import (
	"context"

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
// - The webhook already ran CanIGetRoles at admission; we re-run it
//   here against the K8s-attested identity as defense in depth.
// - In useProjectedSA mode, Vault performs the attestation natively
//   (bound_service_account_names) during Login above and CanIGetRoles
//   is skipped. The Vault role MUST be configured with token_period > 0
//   so the pod-token (and its lease) survives until explicit revocation.
func fetchAndBuildMapping(ctx context.Context, cfg *config.Config, contextID, podNamespace, podName string) (mapping map[string]string, creds *vault.DbCreds, retErr error) {
	var (
		conn    *vault.Connector
		loginOK bool
	)
	defer func() {
		// In projected mode the pod-token is live in Vault after Login.
		// If we're returning an error we did not hand the token off to
		// the renewer, so revoke it now to avoid leaking until max_ttl.
		if loginOK && retErr != nil && cfg.UseProjectedSA {
			if revErr := conn.RevokeSelfToken(context.Background(), conn.GetToken()); revErr != nil {
				logger.GetLogger().Errorf("failed to revoke pod token on error path: %v", revErr)
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

	// Multi-DbConfiguration on a single pod is not supported in NRI mode
	// (one credential pair per pod). Pick the first; the webhook would
	// have rejected admission with multiple at this point too.
	dbConf := (*pdc.DbConfigurations)[0]
	if err := checkConfigurationLite(dbConf); err != nil {
		return nil, nil, err
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

	authRole := cfg.KubeRole
	if cfg.UseProjectedSA {
		authRole = dbConf.Role
	}
	conn = vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, authRole, pdc.VaultDbPath, dbConf.Role, tok, cfg.VaultRateLimit)
	if err := conn.Login(ctx); err != nil {
		mode := "legacy"
		if cfg.UseProjectedSA {
			mode = "projected"
		}
		metrics.VaultLoginErrors.WithLabelValues(vault.ClassifyLoginError(err), mode).Inc()
		return nil, nil, errors.Wrap(err, "vault login")
	}
	loginOK = true
	if cfg.UseProjectedSA {
		conn.PodVaultToken = conn.GetToken()
	} else {
		conn.K8sSaVaultToken = conn.GetToken()
	}

	if !cfg.UseProjectedSA {
		ok, err := conn.CanIGetRoles(ctx, contextID, actualSA, podNamespace, cfg.VaultAuthPath, dbConf.Role)
		if err != nil {
			return nil, nil, errors.Wrap(err, "vault CanIGetRoles")
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
		bookToken, err := vault.LoginAsInjectorSA(ctx, cfg, injectorSaToken)
		if err != nil {
			metrics.VaultLoginErrors.WithLabelValues(vault.ClassifyLoginError(err), "projected_bookkeeping").Inc()
			return nil, nil, errors.Wrap(err, "vault login as injector SA for bookkeeping")
		}
		conn.K8sSaVaultToken = bookToken
	}

	creds, err = conn.GetDbCredentials(ctx, vault.DbCredentialsRequest{
		ContextID:          contextID,
		TTL:                cfg.TokenTTL,
		PodNameUID:         contextID,
		Namespace:          podNamespace,
		SecretName:         cfg.VaultSecretName,
		Prefix:             cfg.VaultSecretPrefix,
		ServiceAccount:     actualSA,
		SkipOrphanCreation: cfg.UseProjectedSA,
	})
	if err != nil {
		return nil, nil, errors.Wrap(err, "vault GetDbCredentials")
	}
	creds.PodUUID = contextID

	// Walk the container env (across init + main containers) to find
	// placeholders that correspond to dbConf.DbUserEnvKey /
	// DbPasswordEnvKey / DbURIEnvKey, and map them to the correct
	// credential field.
	mapping = buildMappingFromPodEnv(pod, dbConf, creds)
	if len(mapping) == 0 {
		return nil, nil, errors.New("no placeholders matched the dbConfiguration env keys")
	}
	return mapping, creds, nil
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

