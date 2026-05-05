package vault

import (
	"context"
	"time"

	"github.com/cockroachdb/errors"
	vault "github.com/hashicorp/vault/api"
	vault_auth_k8s "github.com/hashicorp/vault/api/auth/kubernetes"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

// LoginAsInjectorSA performs a fresh Vault login using the injector
// binary's own ServiceAccount token (read from
// /var/run/secrets/kubernetes.io/serviceaccount/token) and the
// configured kubeRole. Returns the resulting Vault token.
//
// Used in projected-SA mode where the connector's main token is the
// per-pod token (which intentionally has no KV-write capability), so
// we need a distinct injector identity for credential bookkeeping
// writes via StoreDataAsync.
func LoginAsInjectorSA(ctx context.Context, cfg *config.Config, k8sSaToken string) (string, error) {
	if k8sSaToken == "" {
		return "", errors.New("LoginAsInjectorSA: empty k8s SA token")
	}
	conn := NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, "", "", k8sSaToken, cfg.VaultRateLimit)
	if err := conn.Login(ctx); err != nil {
		return "", errors.Wrap(err, "vault login as injector SA")
	}
	return conn.GetToken(), nil
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
