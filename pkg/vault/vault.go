package vault

import (
	"context"
	"fmt"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/sirupsen/logrus"

	vault "github.com/hashicorp/vault/api"
	vault_auth_k8s "github.com/hashicorp/vault/api/auth/kubernetes"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/logger"
	promInjector "github.com/numberly/vault-db-injector/pkg/prometheus"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

type Connector struct {
	address         string
	authPath        string
	dbRole          string
	k8sSaToken      string
	K8sSaVaultToken string
	vaultToken      string
	authRole        string
	dbMountPath     string
	client          *vault.Client
	RenewalInterval time.Duration
	Log             logger.Logger
	VaultRateLimit  int
}

func (c *Connector) GetToken() string {
	return c.vaultToken
}

func (c *Connector) SetToken(token string) {
	c.client.SetToken(token)
}

type DbCreds struct {
	Username    string
	Password    string
	DbLeaseId   string
	AuthLeaseId string
	DbTokenId   string
}

func NewConnector(address string, authPath string, authRole string, dbMountPath string, dbRole string, token string, VaultRateLimit int) *Connector {
	return &Connector{
		address:        address,
		authPath:       authPath,
		dbRole:         dbRole,
		dbMountPath:    dbMountPath,
		k8sSaToken:     token,
		authRole:       authRole,
		Log:            logger.GetLogger(),
		VaultRateLimit: VaultRateLimit,
	}
}

func ConnectToVault(ctx context.Context, cfg *config.Config) (*Connector, error) {
	// Request token from k8s serviceAccount
	k8sClient := k8s.NewClient()
	tok, err := k8sClient.GetServiceAccountToken()
	if err != nil {
		promInjector.ConnectVaultError.WithLabelValues().Inc()
		return nil, errors.Newf("cannot get ServiceAccount token: %s", err.Error())
	}
	// Configure vault connection using serviceAccount token
	vaultConn := NewConnector(cfg.VaultAddress, cfg.VaultAuthPath, cfg.KubeRole, "random", "random", tok, cfg.VaultRateLimit)
	if err := vaultConn.Login(ctx); // Assuming Login is modified to accept a context
	err != nil {
		promInjector.ConnectVaultError.WithLabelValues().Inc()
		return nil, errors.Newf("cannot authenticate vault role: %s", err.Error())
	}
	vaultConn.K8sSaVaultToken = vaultConn.client.Token()
	promInjector.ConnectVault.WithLabelValues().Inc()
	return vaultConn, nil
}

func (c *Connector) Login(ctx context.Context) error {
	config := vault.DefaultConfig()
	config.Address = c.address
	client, err := vault.NewClient(config)
	if err != nil {
		return err
	}

	// Use the passed context instead of creating a new one
	k8sAuth, err := vault_auth_k8s.NewKubernetesAuth(
		c.authRole,
		vault_auth_k8s.WithServiceAccountToken(c.k8sSaToken),
		vault_auth_k8s.WithMountPath(c.authPath),
	)
	if err != nil {
		return err
	}

	_, err = client.Auth().Login(ctx, k8sAuth) // Use the ctx passed to Login
	if err != nil {
		return err
	}

	c.client = client
	c.vaultToken = client.Token()
	return nil
}

func (c *Connector) CheckHealth(ctx context.Context) error {
	config := vault.DefaultConfig()
	config.Address = c.address
	client, err := vault.NewClient(config)
	if err != nil {
		return fmt.Errorf("failed to create Vault client: %v", err)
	}

	health, err := client.Sys().HealthWithContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to check Vault health: %v", err)
	}

	if !health.Initialized {
		return fmt.Errorf("vault is not initialized")
	}

	if health.Sealed {
		return fmt.Errorf("vault is sealed")
	}

	return nil
}

func (c *Connector) CreateOrphanToken(ctx context.Context, ttl string, policies []string) (string, error) {
	// Create an orphan token
	secret, err := c.client.Auth().Token().CreateOrphanWithContext(ctx, &vault.TokenCreateRequest{
		Period:      ttl,
		DisplayName: "injector-orphan-token",
		Policies:    policies,
	})
	if err != nil {
		promInjector.OrphanErrorTicketCreatedCount.WithLabelValues().Inc()
		return "", errors.Newf("failed to create orphan token: %v", err)
	}

	// Update our client with the new orphan token
	orphanToken := secret.Auth.ClientToken
	c.SetToken(orphanToken)
	c.vaultToken = orphanToken
	promInjector.OrphanTicketCreatedCount.WithLabelValues().Inc()
	return secret.LeaseID, nil
}

func (c *Connector) CanIGetRoles(contextId, serviceAccountName, namespace, vaultAuthPath, dbRole string) (bool, error) {
	rolePath := fmt.Sprintf("auth/%s/role/%s", vaultAuthPath, dbRole)
	role, err := c.client.Logical().Read(rolePath)
	if err != nil {
		c.Log.WithFields(logrus.Fields{
			"contextId": contextId,
		}).Errorf("error reading role from Vault: %v", err)
		return false, err
	}
	if role == nil {
		c.Log.WithFields(logrus.Fields{
			"contextId": contextId,
		}).Errorf("role %s not found in Vault", dbRole)
		theError := fmt.Sprintf("role %s not found in Vault", dbRole)
		return false, errors.Newf(theError)
	}

	boundServiceAccountNames := sliceToStrings(role.Data["bound_service_account_names"].([]interface{}))
	boudServiceAccountNamespaces := sliceToStrings(role.Data["bound_service_account_namespaces"].([]interface{}))
	tokenPolicies := sliceToStrings(role.Data["token_policies"].([]interface{}))

	if !stringInSlice(dbRole, tokenPolicies) {
		promInjector.ServiceAccountDenied.WithLabelValues(serviceAccountName, namespace, dbRole, "RoleNotInAssumeRole").Inc()
		c.Log.WithFields(logrus.Fields{"contextId": contextId}).Errorf("the serviceAccount %s can't assume vault role : %s", serviceAccountName, dbRole)
		theError := fmt.Sprintf("serviceAccount not allowed, the Role is not in the AssumeRole, %s =/= %s", serviceAccountName, dbRole)
		return false, errors.New(theError)
	}
	if !stringInSlice(serviceAccountName, boundServiceAccountNames) {
		promInjector.ServiceAccountDenied.WithLabelValues(serviceAccountName, namespace, dbRole, "ServiceAccountNameNotInRole").Inc()
		c.Log.WithFields(logrus.Fields{"contextId": contextId}).Errorf("the serviceAccount %s can't assume vault role : %s", serviceAccountName, dbRole)
		theError := fmt.Sprintf("serviceAccount not allowed, the serviceAccount is not in the bound_service_account_names in the Vault Kubernetes Auth Dedicated Backend, %s =/= %s", serviceAccountName, boundServiceAccountNames)
		return false, errors.New(theError)
	}
	if !stringInSlice(namespace, boudServiceAccountNamespaces) {
		promInjector.ServiceAccountDenied.WithLabelValues(serviceAccountName, namespace, dbRole, "NamespaceNotInRole").Inc()
		c.Log.WithFields(logrus.Fields{"contextId": contextId}).Errorf("the serviceAccount %s can't assume vault role : %s", serviceAccountName, dbRole)
		theError := fmt.Sprintf("serviceAccount not allowed, the namespace is not in the bound_service_account_namespaces in the Vault Kubernetes Auth Dedicated Backend, %s =/= %s", namespace, boudServiceAccountNamespaces)
		return false, errors.New(theError)
	}

	promInjector.ServiceAccountAuthorized.WithLabelValues().Inc()
	return true, nil
}

func (c *Connector) GetDbCredentials(ctx context.Context, contextId, ttl, PodNameUID, namespace, secretName, prefix, serviceAccount string) (*DbCreds, error) {
	// Create orphan token before retrieving BDD IDs
	var policies []string
	policies = append(policies, c.dbRole)
	authLeaseId, err := c.CreateOrphanToken(ctx, ttl, policies)
	if err != nil {
		return nil, err
	}

	creds := &DbCreds{}
	creds.AuthLeaseId = authLeaseId
	path := fmt.Sprintf("/%s/creds/%s", c.dbMountPath, c.dbRole)
	c.Log.WithFields(logrus.Fields{"contextId": contextId}).Infof("Get credentials from Vault database engine")
	start := time.Now()
	secret, err := c.client.Logical().Read(path)
	duration := time.Since(start)
	durationMs := float64(duration.Microseconds()) / 1000.0
	if err != nil {
		return nil, err
	}
	c.Log.WithFields(
		logrus.Fields{
			"duration_in_ms": fmt.Sprintf("%.2f", durationMs),
			"contextId":      contextId,
		},
	).Infof("Credentials successfully retrieved")

	username, ok := secret.Data["username"]
	if !ok {
		return nil, errors.Newf("cannot get username from vault creds response")
	}
	password, ok := secret.Data["password"]
	if !ok {
		return nil, errors.Newf("cannot get password from vault creds response")
	}

	creds.Username = username.(string)
	creds.Password = password.(string)
	creds.DbLeaseId = secret.LeaseID
	creds.DbTokenId = c.vaultToken

	vaultInformation := NewKeyInformation(PodNameUID, creds.DbLeaseId, creds.DbTokenId, namespace, serviceAccount, "", "")

	c.StoreDataAsync(ctx, contextId, vaultInformation, secretName, PodNameUID, namespace, prefix)

	c.Log.WithFields(logrus.Fields{"contextId": contextId}).Infof("Async store operation initiated")
	return creds, nil
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if b == a {
			return true
		}
	}
	return false
}

func sliceToStrings(slice []interface{}) []string {
	var stringSlice []string
	for _, item := range slice {
		switch v := item.(type) {
		case string:
			stringSlice = append(stringSlice, v)
		case int, int32, int64, float32, float64, bool:
			// Convertir les types primitifs en string
			stringSlice = append(stringSlice, fmt.Sprintf("%v", v))
		default:
			// Retourner une erreur si le type ne peut pas être converti en string
			return nil
		}
	}
	return stringSlice
}
