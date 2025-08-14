package vault

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	promInjector "github.com/numberly/vault-db-injector/pkg/prometheus"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

type KeyInformation struct {
	PodNameUID     string
	LeaseId        string
	TokenId        string
	Namespace      string
	PodName        string
	NodeName       string
	ServiceAccount string
}

func NewKeyInformation(podUuid, leaseId, tokenId, namespace, serviceAccount string, podName ...string) *KeyInformation {
	var pn string
	var nn string
	if len(podName) > 0 {
		pn = podName[0]
	}
	if len(podName) > 1 {
		nn = podName[1]
	}
	return &KeyInformation{
		PodNameUID:     podUuid,
		LeaseId:        leaseId,
		TokenId:        tokenId,
		Namespace:      namespace,
		PodName:        pn,
		NodeName:       nn,
		ServiceAccount: serviceAccount,
	}
}

func (c *Connector) StoreData(ctx context.Context, contextId string, vaultInformation *KeyInformation, secretName, uuid, namespace, prefix string) (string, error) {
	data := map[string]interface{}{
		"LeaseId":            vaultInformation.LeaseId,
		"TokenId":            vaultInformation.TokenId,
		"Namespace":          vaultInformation.Namespace,
		"ServiceAccountName": vaultInformation.ServiceAccount,
		"PodName":            vaultInformation.PodName,
		"NodeName":           vaultInformation.NodeName,
	}

	kv := c.client.KVv2(secretName)
	fullPath := fmt.Sprintf("%s/%s", prefix, vaultInformation.PodNameUID)

	_, err := kv.Put(ctx, fullPath, data)
	if err != nil {
		c.Log.WithFields(logrus.Fields{"contextId": contextId}).Errorf("Vault Information couldn't be stored in Vault KV: %v", err)
		promInjector.DataErrorDeletedCount.WithLabelValues(uuid, namespace).Inc()
		return "Error !", err
	}

	promInjector.DataStoredCount.WithLabelValues().Inc()
	return "Success !", nil
}

func (c *Connector) StoreDataAsync(ctx context.Context, contextId string, vaultInformation *KeyInformation, secretName, uuid, namespace, prefix string) {
	go func() {
		start := time.Now()
		asyncCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		asyncConn := &Connector{
			address:        c.address,
			authPath:       c.authPath,
			dbRole:         c.dbRole,
			k8sSaToken:     c.k8sSaToken,
			authRole:       c.authRole,
			dbMountPath:    c.dbMountPath,
			Log:            c.Log,
			VaultRateLimit: c.VaultRateLimit,
		}

		if err := asyncConn.Login(asyncCtx); err != nil {
			c.Log.Errorf("Failed to login for async operation: %v", err)
			return
		}

		var policies []string
		policies = append(policies, c.authRole)
		_, err := asyncConn.CreateOrphanToken(asyncCtx, "5m", policies)
		if err != nil {
			c.Log.WithFields(logrus.Fields{"contextId": contextId}).Errorf("Failed to create token for async operation: %v", err)
			return
		}

		status, err := asyncConn.StoreData(asyncCtx, contextId, vaultInformation, secretName, uuid, namespace, prefix)
		if err != nil {
			c.Log.WithFields(logrus.Fields{"contextId": contextId}).Errorf("Async store operation failed: %v", err)
			asyncConn.RevokeOrphanToken(asyncCtx, asyncConn.vaultToken, uuid, namespace)
			return
		}

		asyncConn.RevokeOrphanToken(asyncCtx, asyncConn.vaultToken, uuid, namespace)
		duration := time.Since(start)
		durationMs := float64(duration.Microseconds()) / 1000.0
		c.Log.WithFields(
			logrus.Fields{
				"duration_in_ms": fmt.Sprintf("%.2f", durationMs),
				"contextId":      contextId,
			},
		).Infof("Async store operation completed: %s", status)

	}()
}

func (c *Connector) DeleteData(ctx context.Context, podName, secretName, uuid, namespace, prefix string) (string, error) {
	kv := c.client.KVv2(secretName)
	fullPath := fmt.Sprintf("%s/%s", prefix, podName)
	c.Log.Debugf("Full path for deleting data is : %s", fullPath)

	err := kv.Delete(ctx, fullPath)
	if err != nil {
		promInjector.DataErrorDeletedCount.WithLabelValues(uuid, namespace).Inc()
		return "Error !", err
	}

	err = kv.DeleteMetadata(ctx, fullPath)
	if err != nil {
		promInjector.DataErrorDeletedCount.WithLabelValues(uuid, namespace).Inc()
		return "Error !", err
	}

	promInjector.DataDeletedCount.WithLabelValues(uuid, namespace).Inc()
	return "Success !", nil
}

func safeString(v interface{}) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func (c *Connector) GetKeyInformations(ctx context.Context, podName, uuid, path, prefix string) (*KeyInformation, error) {
	dataPath := fmt.Sprintf("%s/data/%s/%s", path, prefix, uuid)
	podSecret, err := c.client.Logical().ReadWithContext(ctx, dataPath)
	if err != nil {
		c.Log.Errorf("Error while trying to recover data informations for : %s: %v", uuid, err)
		return nil, err
	}
	if podSecret == nil || podSecret.Data == nil || podSecret.Data["data"] == nil {
		c.Log.Errorf("No data has been found for uuid %s and pod %s", uuid, podName)
		return nil, err
	}

	dataMap, ok := podSecret.Data["data"].(map[string]interface{})
	if !ok {
		c.Log.Errorf("Invalid data format for %s", uuid)
		return nil, err
	}
	keyInfo := NewKeyInformation(
		uuid,
		safeString(dataMap["LeaseId"]),
		safeString(dataMap["TokenId"]),
		safeString(dataMap["Namespace"]),
		safeString(dataMap["ServiceAccountName"]),
		safeString(dataMap["PodName"]),
		safeString(dataMap["NodeName"]),
	)

	return keyInfo, nil
}

func (c *Connector) ListKeyInformations(ctx context.Context, path, prefix string) ([]*KeyInformation, error) {
	// Utiliser le préfixe pour lister les clés dans KV v2
	kvPath := fmt.Sprintf("%s/metadata/%s", path, prefix)

	secret, err := c.client.Logical().ListWithContext(ctx, kvPath)
	if err != nil {
		return nil, err
	}

	if secret == nil || secret.Data["keys"] == nil {
		return []*KeyInformation{}, nil
	}

	keys := secret.Data["keys"].([]interface{})
	var wg sync.WaitGroup
	keyInformationsChan := make(chan *KeyInformation, len(keys))

	// Create a rate limiter
	rateLimit := rate.Limit(c.VaultRateLimit) // requests per second
	limiter := rate.NewLimiter(rateLimit, 1)

	for _, k := range keys {
		wg.Add(1)
		go func(k interface{}) {
			defer wg.Done()

			// Wait for the rate limiter
			if err := limiter.Wait(ctx); err != nil {
				c.Log.Errorf("Rate limiter error: %v", err)
				return
			}

			podName := strings.TrimSuffix(k.(string), "/")

			// Utiliser le préfixe pour lire les données
			dataPath := fmt.Sprintf("%s/data/%s/%s", path, prefix, podName)
			podSecret, err := c.client.Logical().ReadWithContext(ctx, dataPath)
			if err != nil {
				c.Log.Errorf("Error while trying to recover data informations for: %s: %v", podName, err)
				return
			}

			if podSecret == nil || podSecret.Data == nil || podSecret.Data["data"] == nil {
				status, err := c.DeleteData(ctx, podName, path, podName, "", prefix)
				if err != nil {
					c.Log.Errorf("Data for %s can't be deleted: %s with error: %s", podName, status, err.Error())
				}
				return
			}

			dataMap, ok := podSecret.Data["data"].(map[string]interface{})
			if !ok {
				c.Log.Errorf("Invalid data format for %s", podName)
				return
			}
			keyInfo := NewKeyInformation(
				podName,
				safeString(dataMap["LeaseId"]),
				safeString(dataMap["TokenId"]),
				safeString(dataMap["Namespace"]),
				safeString(dataMap["ServiceAccountName"]),
				safeString(dataMap["PodName"]),
				safeString(dataMap["NodeName"]),
			)
			keyInformationsChan <- keyInfo
		}(k)
	}

	wg.Wait()
	close(keyInformationsChan)

	var keyInformations []*KeyInformation
	for keyInfo := range keyInformationsChan {
		keyInformations = append(keyInformations, keyInfo)
	}

	return keyInformations, nil
}

func (c *Connector) HandlePodDeletionToken(ctx context.Context, keysInformation *KeyInformation, secretName, prefix string) error {
	err := c.RevokeOrphanToken(ctx, keysInformation.TokenId, keysInformation.PodNameUID, keysInformation.Namespace)
	if err != nil {
		c.Log.Errorf("Can't revok Token with UUID : %s", keysInformation.PodNameUID)
		c.RevokeSelfToken(ctx, c.client.Token(), "", "")
		c.SetToken(c.K8sSaVaultToken)
		return err
	}
	promInjector.LeaseExpirationInTime.DeleteLabelValues(keysInformation.PodNameUID, keysInformation.Namespace)
	promInjector.TokenExpirationInTime.DeleteLabelValues(keysInformation.PodNameUID, keysInformation.Namespace)
	c.Log.Infof("Token with uuid %s has been revoked : Success !", keysInformation.PodNameUID)
	return nil
}

func (c *Connector) HandleTokens(ctx context.Context, cfg *config.Config, keysInformations []*KeyInformation, secretName, prefix string, clientset k8s.KubernetesClient, SyncTTLSecond int) bool {
	podServer := k8s.NewPodService(clientset, cfg)
	podsInformations, err := podServer.GetAllPodAndNamespace(ctx)
	if err != nil {
		c.Log.Errorf("Error while trying to get Pod from Kubernetes %v", err)
		return false
	}

	// Create a map for quick lookup of pod information
	podInfoMap := make(map[string]k8s.PodInformations)
	for _, pi := range podsInformations {
		for _, uuid := range pi.PodNameUUIDs {
			podInfoMap[uuid] = pi
		}
	}

	var KubePolicies []string
	KubePolicies = append(KubePolicies, c.authRole)
	_, err = c.CreateOrphanToken(ctx, "1h", KubePolicies)
	if err != nil {
		c.Log.Errorf("Can't create orphan ticket: %v", err)
		c.Log.Error("Token renew has been cancelled")
		return false
	}

	// Create a rate limiter
	rateLimit := rate.Limit(cfg.VaultRateLimit) // requests per second
	limiter := rate.NewLimiter(rateLimit, 1)

	var wg sync.WaitGroup
	var isOk bool = true

	for _, ki := range keysInformations {
		wg.Add(1)
		go func(ki *KeyInformation) {
			defer wg.Done()

			// Wait for the rate limiter
			if err := limiter.Wait(ctx); err != nil {
				c.Log.Errorf("Rate limiter error: %v", err)
				isOk = false
				return
			}

			if _, found := podInfoMap[ki.PodNameUID]; found {
				err := c.RenewToken(ctx, ki.TokenId, ki.PodNameUID, ki.Namespace, SyncTTLSecond)
				if err != nil {
					c.Log.Errorf("Can't renew Token with pod UUID: %s", ki.PodNameUID)
					isOk = false
					return
				}
				err = c.RenewLease(ctx, ki.LeaseId, 86400*5, ki.PodNameUID, ki.Namespace) // Renew for 1 week
				if err != nil {
					c.Log.Errorf("Can't renew Lease with pod UUID: %s", ki.PodNameUID)
					isOk = false
					return
				}
				if ki.ServiceAccount == "" || ki.NodeName == "" || ki.PodName == "" {
					fullyKiInformations := NewKeyInformation(ki.PodNameUID, ki.LeaseId, ki.TokenId, ki.Namespace, podInfoMap[ki.PodNameUID].ServiceAccountName, podInfoMap[ki.PodNameUID].PodName, podInfoMap[ki.PodNameUID].NodeName)
					c.Log.Debugf("Renewing information for UUID %s", ki.PodNameUID)
					status, err := c.StoreData(ctx, "id-handle-token", fullyKiInformations, secretName, ki.PodNameUID, ki.Namespace, prefix)
					if err != nil {
						c.Log.Infof("%s : Extended vault information could not been saved, process will continue : %v", status, err)
					}
				}
			} else {
				leaseTooYoung, err := c.isLeaseTooYoung(ctx, ki.LeaseId)
				if err != nil {
					c.Log.Debug("Error while trying to retrieve lease age, lease will be cleaned")
				}
				if leaseTooYoung {
					c.Log.Infof("This lease: %s is too young to be cleaned up.", ki.LeaseId)
					return
				}
				err = c.RevokeOrphanToken(ctx, ki.TokenId, ki.PodNameUID, ki.Namespace)
				if err != nil {
					c.Log.Errorf("Can't revoke Token with UUID: %s", ki.PodNameUID)
					isOk = false
					return
				}
				status, err := c.DeleteData(ctx, ki.PodNameUID, secretName, ki.PodNameUID, ki.Namespace, prefix)
				if err != nil {
					c.Log.Errorf("Data for %s can't be deleted: %s with error: %s", ki.PodNameUID, status, err.Error())
					isOk = false
					return
				}
				promInjector.LeaseExpirationInTime.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				promInjector.TokenExpirationInTime.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				promInjector.RenewLeaseCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				promInjector.RenewTokenCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				promInjector.DataDeletedCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				c.Log.Infof("Token has been revoked and data deleted: %s", status)
			}
		}(ki)
	}

	wg.Wait()
	c.RevokeSelfToken(ctx, c.client.Token(), "", "")
	c.SetToken(c.K8sSaVaultToken)
	return isOk
}

func (c *Connector) RevokeOrphanToken(ctx context.Context, tokenId, uuid, namespace string) error {
	// Revok token
	err := c.client.Auth().Token().RevokeOrphanWithContext(ctx, tokenId)
	if err != nil {
		if strings.Contains(err.Error(), "token to revoke not found") {
			c.Log.Debugf("Token for uuid %s has already been revoked by the revoker", uuid)
			//promInjector.RevokeTokenErrorCount.WithLabelValues(uuid, namespace).Inc()
			return nil
		}
		promInjector.RevokeTokenErrorCount.WithLabelValues(uuid, namespace).Inc()
		c.Log.Errorf("error while revoking token: %v", err)
		return err
	}
	promInjector.RevokeTokenCount.WithLabelValues(namespace).Inc()
	promInjector.TokenExpirationInTime.DeleteLabelValues(uuid, namespace)
	return nil
}

func (c *Connector) RevokeSelfToken(ctx context.Context, tokenId, uuid, namespace string) {
	// Revok token
	err := c.client.Auth().Token().RevokeSelfWithContext(ctx, tokenId)
	if err != nil {
		promInjector.RevokeTokenErrorCount.WithLabelValues(uuid, namespace).Inc()
		c.Log.Errorf("error while revoking token: %v", err)
	}
	promInjector.RevokeTokenCount.WithLabelValues(namespace).Inc()
	promInjector.TokenExpirationInTime.DeleteLabelValues(uuid, namespace)
}

func (c *Connector) RenewToken(ctx context.Context, tokenId, uuid, namespace string, SyncTTLSecond int) error {
	// Renew token
	tokenRenew, err := c.client.Auth().Token().RenewWithContext(ctx, tokenId, SyncTTLSecond)
	if err != nil {
		if strings.Contains(err.Error(), "token not found") {
			c.Log.Debugf("can't renew revoked token for uuid %s : it has been revoked by the revoker", uuid)
			return nil
		}
		c.Log.Errorf("error while renewing token: %v", err)
		promInjector.RenewTokenErrorCount.WithLabelValues(uuid, namespace).Inc()
		return err
	}
	promInjector.RenewTokenCount.WithLabelValues(uuid, namespace).Inc()
	tokenDuration, err := tokenRenew.TokenTTL()
	if err != nil {
		c.Log.Errorf("error retriving token ttl: %v", err)
		return nil
	}
	currentTime := time.Now()
	expirationTime := currentTime.Add(tokenDuration)
	expirationEpoch := expirationTime.Unix()
	promInjector.TokenExpirationInTime.WithLabelValues(uuid, namespace).Set(float64(expirationEpoch))
	return nil
}

func (c *Connector) RenewSelfToken(ctx context.Context) error {
	// Renew token
	_, err := c.client.Auth().Token().RenewSelfWithContext(ctx, 60*60*24) // renew for 1 day
	if err != nil {
		c.Log.Errorf("error while renewing token: %v", err)
		return err
	}
	return nil
}

// When you create a pod, sometime, it is not scheduled directly due to some cluster issue such has limit range / ...
// To avoid the token / leaseId to be deleted, we decide that YoungToken should not be deleted by the Renewer
// This doesnt change how the revoker work.
// The value is totally arbitrary, this is to avoid corner case where pod are not scheduled direcly and their creds are deleted before the scheduler schedule them.
func (c *Connector) isLeaseTooYoung(ctx context.Context, leaseId string) (bool, error) {
	leaseInformations, err := c.client.Sys().LookupWithContext(ctx, leaseId)
	if err != nil {
		return false, err
	}
	issueTime := leaseInformations.Data["issue_time"].(string)
	issueTimeParsed, err := time.Parse(time.RFC3339, issueTime)
	if err != nil {
		return false, fmt.Errorf("error parsing issue time: %w", err)
	}
	timeSinceIssue := time.Since(issueTimeParsed)
	isYoungerThanTenMinutes := timeSinceIssue < 10*time.Minute

	return isYoungerThanTenMinutes, nil
}

func (c *Connector) RenewLease(ctx context.Context, leaseID string, leaseTTL int, uuid, namespace string) error {
	// Renew the lease
	secret, err := c.client.Sys().RenewWithContext(ctx, leaseID, leaseTTL)
	if err != nil {
		if strings.Contains(err.Error(), "lease not found") {
			c.Log.Debugf("Lease for uuid %s has been revoked by the revoker", uuid)
			return nil
		}
		c.Log.Errorf("error while renewing lease: %v", err)
		promInjector.RenewLeaseErrorCount.WithLabelValues(uuid, namespace).Inc()
		return err
	}

	promInjector.RenewLeaseCount.WithLabelValues(uuid, namespace).Inc()

	leaseDuration := time.Duration(secret.LeaseDuration) * time.Second
	currentTime := time.Now()
	expirationTime := currentTime.Add(leaseDuration)
	expirationEpoch := expirationTime.Unix()

	promInjector.LeaseExpirationInTime.WithLabelValues(uuid, namespace).Set(float64(expirationEpoch))
	return nil
}

// Permit to the renewer to renew is self token using to connect on Vault
func (c *Connector) StartTokenRenewal(ctx context.Context, cfg *config.Config) {
	go func() {
		ticker := time.NewTicker(c.RenewalInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				// Context cancelled, stop renewing
				c.Log.Info("Stopping token renewal due to context cancellation")
				return
			case <-ticker.C:
				// Attempt to renew the token
				c.Log.Debug("Attempting to renew Vault token")
				err := c.RenewSelfToken(ctx)
				if err != nil {
					c.Log.Errorf("Failed to renew Vault token: %v", err)
					c.Log.Info("Trying to reconnect to Vault")
					newConn, err := ConnectToVault(ctx, cfg)
					if err != nil {
						c.Log.Fatalf("Can't reconnect to VAULT: %v", err)
					}
					c.K8sSaVaultToken = newConn.K8sSaVaultToken
					c.SetToken(newConn.K8sSaVaultToken)
				}
				c.Log.Debug("Token has been renewed succefully !")
			}
		}
	}()
}
