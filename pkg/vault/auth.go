package vault

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"github.com/cockroachdb/errors"
	vault "github.com/hashicorp/vault/api"
	vault_auth_k8s "github.com/hashicorp/vault/api/auth/kubernetes"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

// debugJWTClaims temporarily decodes the JWT payload (no signature check)
// and returns the raw JSON for logging. Used to investigate audience /
// alias_name_source mismatches during the 403 bookkeeping login bug.
// REMOVE after the investigation closes.
func debugJWTClaims(jwt string) string {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return "<malformed JWT>"
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if p2, err2 := base64.StdEncoding.DecodeString(parts[1]); err2 == nil {
			return string(p2)
		}
		return "<decode error: " + err.Error() + ">"
	}
	return string(payload)
}

// LoginAsInjectorSA performs a fresh Vault login using the injector
// binary's own ServiceAccount token (read from
// /var/run/secrets/kubernetes.io/serviceaccount/token) and the given
// kubeRole. When kubeRole is empty it falls back to cfg.KubeRole.
//
// Used in projected-SA mode where the connector's main token is the
// per-pod token (which intentionally has no KV-write capability), so
// we need a distinct injector identity for credential bookkeeping
// writes via StoreDataAsync.
//
// The kubeRole parameter allows callers to use a role with elevated
// bookkeeping-write privileges (e.g. cfg.KubeRoleNri for the NRI
// DaemonSet) while the webhook uses the base cfg.KubeRole.
func LoginAsInjectorSA(ctx context.Context, cfg *config.Config, k8sSaToken, kubeRole string) (string, error) {
	if k8sSaToken == "" {
		return "", errors.New("LoginAsInjectorSA: empty k8s SA token")
	}
	if kubeRole == "" {
		kubeRole = cfg.KubeRole
	}

	// DEBUG: dump JWT claims + login parameters so we can correlate with the
	// audit log when Vault returns 403. Token prefix only — no full secret.
	log := logger.GetLogger()
	jwtClaims := debugJWTClaims(k8sSaToken)
	if len(jwtClaims) > 800 {
		jwtClaims = jwtClaims[:800] + "...[truncated]"
	}
	jwtPrefix := k8sSaToken
	if len(jwtPrefix) > 20 {
		jwtPrefix = jwtPrefix[:20] + "..."
	}
	log.WithFields(map[string]interface{}{
		"authPath":   cfg.VaultAuthPath,
		"role":       kubeRole,
		"jwt_prefix": jwtPrefix,
		"jwt_claims": jwtClaims,
	}).Infof("[debug] LoginAsInjectorSA attempting Vault login")

	conn := NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, kubeRole, "", "", k8sSaToken, cfg.VaultRateLimit)
	if err := conn.Login(ctx); err != nil {
		log.WithFields(map[string]interface{}{
			"authPath": cfg.VaultAuthPath,
			"role":     kubeRole,
		}).Errorf("[debug] LoginAsInjectorSA login failed: %v", err)
		return "", errors.Wrap(err, "vault login as injector SA")
	}

	// DEBUG: lookup-self on the freshly-minted token to verify policies BEFORE
	// the cache stores it.
	got := conn.GetToken()
	tokPrefix := got
	if len(tokPrefix) > 20 {
		tokPrefix = tokPrefix[:20] + "..."
	}
	if lookup, lookupErr := conn.client.Auth().Token().LookupSelf(); lookupErr != nil {
		log.WithFields(map[string]interface{}{
			"role":         kubeRole,
			"token_prefix": tokPrefix,
		}).Errorf("[debug] LoginAsInjectorSA: lookup-self on fresh token FAILED: %v", lookupErr)
	} else {
		log.WithFields(map[string]interface{}{
			"role":         kubeRole,
			"token_prefix": tokPrefix,
			"policies":     lookup.Data["policies"],
			"display_name": lookup.Data["display_name"],
			"meta":         lookup.Data["meta"],
			"ttl":          lookup.Data["ttl"],
		}).Infof("[debug] LoginAsInjectorSA fresh token policies confirmed")
	}

	return got, nil
}

// ConnectAndRenew authenticates to Vault and starts background token renewal.
// It is the canonical bootstrap used by renewer and revoker job entry points.
func ConnectAndRenew(ctx context.Context, cfg *config.Config, saToken string) (*Connector, error) {
	vaultConn, err := ConnectToVault(ctx, cfg, saToken)
	if err != nil {
		return nil, err
	}
	vaultConn.RenewalInterval = 600 * time.Second
	vaultConn.StartTokenRenewal(ctx, cfg)
	return vaultConn, nil
}

// ConnectToVault authenticates to Vault using the provided Kubernetes SA token
// and returns an authenticated Connector.
func ConnectToVault(ctx context.Context, cfg *config.Config, saToken string) (*Connector, error) {
	vaultConn := NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, "random", "random", saToken, cfg.VaultRateLimit)
	if err := vaultConn.Login(ctx); err != nil {
		metrics.ConnectVaultError.WithLabelValues().Inc()
		metrics.VaultLoginErrors.WithLabelValues(ClassifyLoginError(err), "legacy").Inc()
		return nil, errors.Wrapf(err, "cannot authenticate vault role")
	}
	vaultConn.K8sSaVaultToken = vaultConn.client.Token()
	metrics.ConnectVault.WithLabelValues().Inc()
	return vaultConn, nil
}

// Login authenticates to Vault using Kubernetes auth and populates c.client.
func (c *Connector) Login(ctx context.Context) error {
	config := vault.DefaultConfig()
	config.Address = c.address
	client, err := vault.NewClient(config)
	if err != nil {
		return err
	}

	k8sAuth, err := vault_auth_k8s.NewKubernetesAuth(
		c.authRole,
		vault_auth_k8s.WithServiceAccountToken(c.k8sSaToken),
		vault_auth_k8s.WithMountPath(c.authPath),
	)
	if err != nil {
		return err
	}

	_, err = client.Auth().Login(ctx, k8sAuth)
	if err != nil {
		return err
	}

	c.client = client
	c.vaultToken = client.Token()
	return nil
}

// CheckVaultConnectivity checks whether the Vault server at the given address is initialized and unsealed.
// It does not require authentication.
func CheckVaultConnectivity(ctx context.Context, address string) error {
	return (&Connector{address: address}).CheckHealth(ctx)
}

// CheckHealth verifies that Vault is initialized and unsealed. Does not require authentication.
func (c *Connector) CheckHealth(ctx context.Context) error {
	config := vault.DefaultConfig()
	config.Address = c.address
	client, err := vault.NewClient(config)
	if err != nil {
		return errors.Wrap(err, "failed to create Vault client")
	}

	health, err := client.Sys().HealthWithContext(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to check Vault health")
	}

	if !health.Initialized {
		return errors.New("vault is not initialized")
	}

	if health.Sealed {
		return errors.New("vault is sealed")
	}

	return nil
}
