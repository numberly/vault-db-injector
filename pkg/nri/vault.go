package nri

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
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
func fetchAndBuildMapping(ctx context.Context, cfg *config.Config, contextID, podNamespace, podName string) (map[string]string, *vault.DbCreds, error) {
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

	// Authenticate to Vault using the plugin's own SA. Re-run CanIGetRoles
	// for the K8s-attested pod identity.
	tok, err := k8sClient.GetServiceAccountToken()
	if err != nil {
		return nil, nil, errors.Wrap(err, "get serviceaccount token")
	}
	conn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, pdc.VaultDbPath, dbConf.Role, tok, cfg.VaultRateLimit)
	if err := conn.Login(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "vault login")
	}
	conn.K8sSaVaultToken = conn.GetToken()

	ok, err := conn.CanIGetRoles(ctx, contextID, actualSA, podNamespace, cfg.VaultAuthPath, dbConf.Role)
	if err != nil {
		return nil, nil, errors.Wrap(err, "vault CanIGetRoles")
	}
	if !ok {
		return nil, nil, errors.Newf("pod %s/%s not authorized for vault role %s", podNamespace, actualSA, dbConf.Role)
	}

	creds, err := conn.GetDbCredentials(ctx, vault.DbCredentialsRequest{
		ContextID:      contextID,
		TTL:            cfg.TokenTTL,
		PodNameUID:     contextID,
		Namespace:      podNamespace,
		SecretName:     cfg.VaultSecretName,
		Prefix:         cfg.VaultSecretPrefix,
		ServiceAccount: actualSA,
	})
	if err != nil {
		return nil, nil, errors.Wrap(err, "vault GetDbCredentials")
	}
	creds.PodUUID = contextID

	// Walk the container env (across init + main containers) to find
	// placeholders that correspond to dbConf.DbUserEnvKey /
	// DbPasswordEnvKey / DbURIEnvKey, and map them to the correct
	// credential field.
	mapping := buildMappingFromPodEnv(pod, dbConf, creds)
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
