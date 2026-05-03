package nri

import (
	"context"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/vault"
)

// fetchAndBuildMapping authenticates to Vault as the plugin's own SA, runs
// CanIGetRoles for the target pod's identity, and creates dynamic database
// credentials. Returns a placeholder→real-value map ready for Substitute.
//
// This replaces the prior unwrap-only path: the wrap-token-as-bearer-credential
// vulnerability (Hunter finding #H5) is closed by never putting any Vault
// token in the pod annotation.
func fetchAndBuildMapping(ctx context.Context, cfg *config.Config, m k8s.NRIMapping, contextID string) (map[string]string, *vault.DbCreds, error) {
	if m.SchemaVersion != 2 {
		return nil, nil, errors.Newf("unsupported nri-mapping schema version %d (expected 2)", m.SchemaVersion)
	}
	if m.DbPath == "" || m.DbRole == "" {
		return nil, nil, errors.New("nri-mapping missing db_path or db_role")
	}
	if len(m.Placeholders) == 0 {
		return nil, nil, errors.New("nri-mapping has empty placeholders")
	}

	k8sClient := k8s.NewClient()
	tok, err := k8sClient.GetServiceAccountToken()
	if err != nil {
		return nil, nil, errors.Wrap(err, "get serviceaccount token")
	}
	conn := vault.NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, m.DbPath, m.DbRole, tok, cfg.VaultRateLimit)
	if err := conn.Login(ctx); err != nil {
		return nil, nil, errors.Wrap(err, "vault login")
	}
	conn.K8sSaVaultToken = conn.GetToken()

	// Re-verify pod's SA can use the role. Webhook already checked this at
	// admission, but re-running here defends against annotation forgery via
	// pods.update RBAC (an attacker who can update annotations but not
	// create SAs cannot bypass the Vault-side authorization).
	ok, err := conn.CanIGetRoles(ctx, contextID, m.PodServiceAccount, m.PodNamespace, cfg.VaultAuthPath, m.DbRole)
	if err != nil {
		return nil, nil, errors.Wrap(err, "vault CanIGetRoles")
	}
	if !ok {
		return nil, nil, errors.Newf("pod %s/%s not authorized for vault role %s", m.PodNamespace, m.PodServiceAccount, m.DbRole)
	}

	creds, err := conn.GetDbCredentials(ctx, vault.DbCredentialsRequest{
		ContextID:      contextID,
		TTL:            cfg.TokenTTL,
		PodNameUID:     contextID, // re-use contextID (= pod UID) so renewer/revoker can correlate by pod
		Namespace:      m.PodNamespace,
		SecretName:     cfg.VaultSecretName,
		Prefix:         cfg.VaultSecretPrefix,
		ServiceAccount: m.PodServiceAccount,
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
