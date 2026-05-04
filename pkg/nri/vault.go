package nri

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/vault"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fetchAndBuildMapping authenticates to Vault as the plugin's own SA, runs
// CanIGetRoles for the target pod's identity, and creates dynamic database
// credentials. Returns a placeholder→real-value map ready for Substitute.
//
// Pod identity (namespace + serviceAccountName) is verified against the
// live K8s API — never trusted from the annotation. Otherwise an attacker
// with pods.create could claim a privileged identity in the annotation
// (e.g. ns=prod sa=trusted-app) while running their pod with their own
// unprivileged SA, and the plugin would fetch the privileged creds for
// the attacker's pod (Hunter finding #H6).
func fetchAndBuildMapping(ctx context.Context, cfg *config.Config, m k8s.NRIMapping, contextID, podNamespace, podName string) (map[string]string, *vault.DbCreds, error) {
	if m.SchemaVersion != 2 {
		return nil, nil, errors.Newf("unsupported nri-mapping schema version %d (expected 2)", m.SchemaVersion)
	}
	if m.DbPath == "" || m.DbRole == "" {
		return nil, nil, errors.New("nri-mapping missing db_path or db_role")
	}
	if len(m.Placeholders) == 0 {
		return nil, nil, errors.New("nri-mapping has empty placeholders")
	}

	// Resolve actual pod identity from the K8s API. The annotation's
	// pod_namespace / pod_service_account are NOT trusted — if they
	// disagree with what kube-apiserver records for this pod UID, refuse
	// to fetch credentials.
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
	// force-deleted between admission and CreateContainer, an attacker who
	// can recreate a pod with the same name+namespace would otherwise
	// hijack the credential fetch. NRI's contextID is the sandbox UID;
	// kube-apiserver's pod.UID is the API-recorded UID. They must match.
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
	if podNamespace != m.PodNamespace || actualSA != m.PodServiceAccount {
		return nil, nil, errors.Newf(
			"pod identity mismatch: actual %s/%s != annotated %s/%s — refusing to fetch credentials",
			podNamespace, actualSA, m.PodNamespace, m.PodServiceAccount,
		)
	}

	tok, err := k8sClient.GetServiceAccountToken()
	if err != nil {
		return nil, nil, errors.Wrap(err, "get serviceaccount token")
	}
	conn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, m.DbPath, m.DbRole, tok, cfg.VaultRateLimit)
	if err := conn.Login(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "vault login")
	}
	conn.K8sSaVaultToken = conn.GetToken()

	// Re-verify the (now-trusted) pod identity against the Vault auth role.
	ok, err := conn.CanIGetRoles(ctx, contextID, actualSA, podNamespace, cfg.VaultAuthPath, m.DbRole)
	if err != nil {
		return nil, nil, errors.Wrap(err, "vault CanIGetRoles")
	}
	if !ok {
		return nil, nil, errors.Newf("pod %s/%s not authorized for vault role %s", podNamespace, actualSA, m.DbRole)
	}

	creds, err := conn.GetDbCredentials(ctx, vault.DbCredentialsRequest{
		ContextID:      contextID,
		TTL:            cfg.TokenTTL,
		PodNameUID:     contextID, // re-use contextID (= pod UID) so renewer/revoker can correlate by pod
		Namespace:      podNamespace,
		SecretName:     cfg.VaultSecretName,
		Prefix:         cfg.VaultSecretPrefix,
		ServiceAccount: actualSA,
	})
	if err != nil {
		return nil, nil, errors.Wrap(err, "vault GetDbCredentials")
	}
	creds.PodUUID = contextID

	payload := map[string]string{
		"username": creds.Username,
		"password": creds.Password,
	}
	out := make(map[string]string, len(m.Placeholders))
	for ph, key := range m.Placeholders {
		v, ok := payload[key]
		if !ok {
			return nil, nil, errors.Newf("credential payload missing key %q", key)
		}
		out[ph] = v
	}
	return out, creds, nil
}
