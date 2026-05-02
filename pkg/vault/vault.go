package vault

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/sirupsen/logrus"

	vault "github.com/hashicorp/vault/api"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
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
	c.vaultToken = token
	c.client.SetToken(token)
}

type DbCreds struct {
	Username    string
	Password    string
	DbLeaseID   string
	AuthLeaseID string
	DbTokenID   string
	PodUUID     string
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

// Clone returns a new Connector with all configuration fields copied from the receiver.
// The clone has no active vault client or token; callers must call Login() before use.
func (c *Connector) Clone() *Connector {
	return &Connector{
		address:        c.address,
		authPath:       c.authPath,
		dbRole:         c.dbRole,
		k8sSaToken:     c.k8sSaToken,
		authRole:       c.authRole,
		dbMountPath:    c.dbMountPath,
		Log:            c.Log,
		VaultRateLimit: c.VaultRateLimit,
	}
}

// CreateOrphanToken creates a Vault orphan token and returns it without mutating connector state.
// Callers that need to use the orphan token must call c.SetToken(orphanToken) explicitly.
func (c *Connector) CreateOrphanToken(ctx context.Context, ttl string, policies []string) (string, error) {
	secret, err := c.client.Auth().Token().CreateOrphanWithContext(ctx, &vault.TokenCreateRequest{
		Period:      ttl,
		DisplayName: "injector-orphan-token",
		Policies:    policies,
	})
	if err != nil {
		metrics.OrphanErrorTicketCreatedCount.WithLabelValues().Inc()
		return "", errors.Newf("failed to create orphan token: %v", err)
	}

	metrics.OrphanTicketCreatedCount.WithLabelValues().Inc()
	return secret.Auth.ClientToken, nil
}

func (c *Connector) CanIGetRoles(ctx context.Context, contextID, serviceAccountName, namespace, vaultAuthPath, dbRole string) (bool, error) {
	rolePath := fmt.Sprintf("auth/%s/role/%s", vaultAuthPath, dbRole)
	role, err := c.client.Logical().ReadWithContext(ctx, rolePath)
	if err != nil {
		c.Log.WithFields(logrus.Fields{
			"contextID": contextID,
		}).Errorf("error reading role from Vault: %v", err)
		return false, err
	}
	if role == nil {
		c.Log.WithFields(logrus.Fields{
			"contextID": contextID,
		}).Errorf("role %s not found in Vault", dbRole)
		theError := fmt.Sprintf("role %s not found in Vault", dbRole)
		return false, errors.Newf(theError)
	}

	rawNames, ok := role.Data["bound_service_account_names"].([]any)
	if !ok {
		return false, errors.Newf("unexpected type for bound_service_account_names in role %s", dbRole)
	}
	boundServiceAccountNames, err := sliceToStrings(rawNames)
	if err != nil {
		return false, errors.Newf("invalid bound_service_account_names in role %s: %v", dbRole, err)
	}

	rawNamespaces, ok := role.Data["bound_service_account_namespaces"].([]any)
	if !ok {
		return false, errors.Newf("unexpected type for bound_service_account_namespaces in role %s", dbRole)
	}
	boundServiceAccountNamespaces, err := sliceToStrings(rawNamespaces)
	if err != nil {
		return false, errors.Newf("invalid bound_service_account_namespaces in role %s: %v", dbRole, err)
	}

	rawPolicies, ok := role.Data["token_policies"].([]any)
	if !ok {
		return false, errors.Newf("unexpected type for token_policies in role %s", dbRole)
	}
	tokenPolicies, err := sliceToStrings(rawPolicies)
	if err != nil {
		return false, errors.Newf("invalid token_policies in role %s: %v", dbRole, err)
	}

	if !stringInSlice(dbRole, tokenPolicies) {
		metrics.ServiceAccountDenied.WithLabelValues(serviceAccountName, namespace, dbRole, "RoleNotInAssumeRole").Inc()
		c.Log.WithFields(logrus.Fields{"contextID": contextID}).Errorf("the serviceAccount %s can't assume vault role : %s", serviceAccountName, dbRole)
		theError := fmt.Sprintf("serviceAccount not allowed, the Role is not in the AssumeRole, %s =/= %s", serviceAccountName, dbRole)
		return false, errors.New(theError)
	}
	if !stringInSlice(serviceAccountName, boundServiceAccountNames) {
		metrics.ServiceAccountDenied.WithLabelValues(serviceAccountName, namespace, dbRole, "ServiceAccountNameNotInRole").Inc()
		c.Log.WithFields(logrus.Fields{"contextID": contextID}).Errorf("the serviceAccount %s can't assume vault role : %s", serviceAccountName, dbRole)
		theError := fmt.Sprintf("serviceAccount not allowed, the serviceAccount is not in the bound_service_account_names in the Vault Kubernetes Auth Dedicated Backend, %s =/= %s", serviceAccountName, boundServiceAccountNames)
		return false, errors.New(theError)
	}
	if !stringInSlice(namespace, boundServiceAccountNamespaces) {
		metrics.ServiceAccountDenied.WithLabelValues(serviceAccountName, namespace, dbRole, "NamespaceNotInRole").Inc()
		c.Log.WithFields(logrus.Fields{"contextID": contextID}).Errorf("the serviceAccount %s can't assume vault role : %s", serviceAccountName, dbRole)
		theError := fmt.Sprintf("serviceAccount not allowed, the namespace is not in the bound_service_account_namespaces in the Vault Kubernetes Auth Dedicated Backend, %s =/= %s", namespace, boundServiceAccountNamespaces)
		return false, errors.New(theError)
	}

	metrics.ServiceAccountAuthorized.WithLabelValues().Inc()
	return true, nil
}

// DbCredentialsRequest groups the per-call identity and storage parameters for GetDbCredentials.
type DbCredentialsRequest struct {
	ContextID      string
	TTL            string
	PodNameUID     string
	Namespace      string
	SecretName     string
	Prefix         string
	ServiceAccount string
}

func (c *Connector) GetDbCredentials(ctx context.Context, req DbCredentialsRequest) (*DbCreds, error) {
	policies := []string{c.dbRole}
	orphanToken, err := c.CreateOrphanToken(ctx, req.TTL, policies)
	if err != nil {
		return nil, err
	}
	c.SetToken(orphanToken)

	creds := &DbCreds{}
	creds.DbTokenID = orphanToken
	path := fmt.Sprintf("/%s/creds/%s", c.dbMountPath, c.dbRole)
	c.Log.WithFields(logrus.Fields{"contextID": req.ContextID}).Infof("Get credentials from Vault database engine")
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
			"contextID":      req.ContextID,
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

	usernameStr, ok := username.(string)
	if !ok {
		return nil, errors.Newf("vault returned non-string %s", "username")
	}
	passwordStr, ok := password.(string)
	if !ok {
		return nil, errors.Newf("vault returned non-string %s", "password")
	}
	creds.Username = usernameStr
	creds.Password = passwordStr
	creds.DbLeaseID = secret.LeaseID

	vaultInformation := NewKeyInfo(req.PodNameUID, creds.DbLeaseID, creds.DbTokenID, req.Namespace, req.ServiceAccount, "", "")

	c.StoreDataAsync(ctx, req.ContextID, vaultInformation, req.SecretName, req.PodNameUID, req.Namespace, req.Prefix)

	c.Log.WithFields(logrus.Fields{"contextID": req.ContextID}).Infof("Async store operation initiated")
	return creds, nil
}

func stringInSlice(a string, list []string) bool {
	return slices.Contains(list, a)
}

func sliceToStrings(slice []any) ([]string, error) {
	var stringSlice []string
	for _, item := range slice {
		switch v := item.(type) {
		case string:
			stringSlice = append(stringSlice, v)
		case int, int32, int64, float32, float64, bool:
			stringSlice = append(stringSlice, fmt.Sprintf("%v", v))
		default:
			return nil, errors.Newf("unexpected type %T in slice element", v)
		}
	}
	return stringSlice, nil
}
