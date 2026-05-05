package vault

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cockroachdb/errors"
	vault "github.com/hashicorp/vault/api"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	"github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// ErrKeyNotFound is returned by GetKeyInfo when no vault data exists for the requested key.
var ErrKeyNotFound = errors.New("key information not found")

// KV map key constants for vault-stored credential metadata.
// NOTE: these string values intentionally differ from the struct field names (LeaseID/TokenID)
// because existing production data was written with camelCase keys ("LeaseId"/"TokenId").
// Do NOT change these constants without a migration of existing KV entries.
const (
	kvKeyLeaseID           = "LeaseId"
	kvKeyTokenID           = "TokenId"
	kvKeyNamespace         = "Namespace"
	kvKeyServiceAccountName = "ServiceAccountName"
	kvKeyPodName           = "PodName"
	kvKeyNodeName          = "NodeName"
)

type KeyInfo struct {
	PodNameUID     string
	LeaseID        string
	TokenID        string
	Namespace      string
	PodName        string
	NodeName       string
	ServiceAccount string
}

func NewKeyInfo(podUuid, leaseID, tokenID, namespace, serviceAccount, podName, nodeName string) *KeyInfo {
	return &KeyInfo{
		PodNameUID:     podUuid,
		LeaseID:        leaseID,
		TokenID:        tokenID,
		Namespace:      namespace,
		PodName:        podName,
		NodeName:       nodeName,
		ServiceAccount: serviceAccount,
	}
}

func (c *Connector) StoreData(ctx context.Context, contextID string, vaultInformation *KeyInfo, secretName, uuid, namespace, prefix string) error {
	data := map[string]any{
		kvKeyLeaseID:            vaultInformation.LeaseID,
		kvKeyTokenID:            vaultInformation.TokenID,
		kvKeyNamespace:          vaultInformation.Namespace,
		kvKeyServiceAccountName: vaultInformation.ServiceAccount,
		kvKeyPodName:            vaultInformation.PodName,
		kvKeyNodeName:           vaultInformation.NodeName,
	}

	kv := c.client.KVv2(secretName)
	fullPath := fmt.Sprintf("%s/%s", prefix, vaultInformation.PodNameUID)

	_, err := kv.Put(ctx, fullPath, data)
	if err != nil {
		c.Log.WithFields(logrus.Fields{"contextID": contextID}).Errorf("Vault Information couldn't be stored in Vault KV: %v", err)
		metrics.DataErrorDeletedCount.WithLabelValues(uuid, namespace).Inc()
		return err
	}

	metrics.DataStoredCount.WithLabelValues().Inc()
	return nil
}

// StoreDataAsync fires-and-forgets a goroutine to persist vault credential metadata.
// Errors are logged and counted via prometheus but NOT propagated to the caller.
//
// Intentional semantics: credential delivery to the pod is not gated on KV persistence.
// This keeps the hot path latency low. The trade-off is that if the async write fails,
// the renewer/revoker will be unable to manage the credential on the next cycle.
//
// The goroutine uses c.K8sSaVaultToken directly — the injector-SA Vault token that
// the caller must populate before calling GetDbCredentials. In projected-SA mode this
// token comes from LoginAsInjectorSA (the pod-token has no KV-write capability). In
// legacy mode it is the injector's own login token, already set by ConnectToVault.
//
// A fresh Vault client is constructed per call so the goroutine does not race with
// concurrent admissions that mutate the shared connector's token.
//
// Callers that require guaranteed persistence (e.g. integration tests, migration tools)
// MUST use StoreData (synchronous) instead of this function.
func (c *Connector) StoreDataAsync(ctx context.Context, contextID string, vaultInformation *KeyInfo, secretName, uuid, namespace, prefix string) {
	go func() {
		start := time.Now()
		asyncCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		if c.K8sSaVaultToken == "" {
			c.Log.WithFields(logrus.Fields{
				"contextID": contextID,
				"uuid":      uuid,
				"namespace": namespace,
			}).Errorf("StoreDataAsync: K8sSaVaultToken is empty — caller must populate it before GetDbCredentials")
			metrics.DataErrorStoredCount.WithLabelValues(uuid, namespace).Inc()
			return
		}

		// Build a fresh Vault client so we do not race with concurrent
		// admissions that may call SetToken on the shared connector.
		asyncCfg := vault.DefaultConfig()
		asyncCfg.Address = c.address
		asyncClient, err := vault.NewClient(asyncCfg)
		if err != nil {
			c.Log.WithFields(logrus.Fields{"contextID": contextID}).Errorf("StoreDataAsync: client init failed: %v", err)
			metrics.DataErrorStoredCount.WithLabelValues(uuid, namespace).Inc()
			return
		}
		asyncClient.SetToken(c.K8sSaVaultToken)

		asyncConn := &Connector{
			address:    c.address,
			client:     asyncClient,
			vaultToken: c.K8sSaVaultToken,
			Log:        c.Log,
		}

		if err := asyncConn.StoreData(asyncCtx, contextID, vaultInformation, secretName, uuid, namespace, prefix); err != nil {
			c.Log.WithFields(logrus.Fields{"contextID": contextID}).Errorf("Async store operation failed: %v", err)
			return
		}

		duration := time.Since(start)
		durationMs := float64(duration.Microseconds()) / 1000.0
		c.Log.WithFields(
			logrus.Fields{
				"duration_in_ms": fmt.Sprintf("%.2f", durationMs),
				"contextID":      contextID,
			},
		).Infof("Async store operation completed")
	}()
}

func (c *Connector) DeleteData(ctx context.Context, secretName, uuid, namespace, prefix string) error {
	kv := c.client.KVv2(secretName)
	fullPath := fmt.Sprintf("%s/%s", prefix, uuid)
	c.Log.Debugf("Full path for deleting data is : %s", fullPath)

	if err := kv.Delete(ctx, fullPath); err != nil {
		if !isNotFound(err) {
			metrics.DataErrorDeletedCount.WithLabelValues(uuid, namespace).Inc()
			return errors.Wrapf(err, "kv.Delete failed for path %s", fullPath)
		}
		c.Log.Debugf("kv.Delete: path %s already gone (idempotent success)", fullPath)
	}

	if err := kv.DeleteMetadata(ctx, fullPath); err != nil {
		if !isNotFound(err) {
			metrics.DataErrorDeletedCount.WithLabelValues(uuid, namespace).Inc()
			return errors.Wrapf(err, "kv.DeleteMetadata failed for path %s", fullPath)
		}
		c.Log.Debugf("kv.DeleteMetadata: path %s already gone (idempotent success)", fullPath)
	}

	metrics.DataDeletedCount.WithLabelValues(uuid, namespace).Inc()
	return nil
}

// isNotFound reports whether a Vault SDK error represents a 404 Not Found.
// Used to treat concurrent double-delete (renewer + revoker racing on the
// same gone pod's KV entry) as idempotent success (I9).
func isNotFound(err error) bool {
	var ve *vault.ResponseError
	return errors.As(err, &ve) && ve.StatusCode == 404
}

func safeString(v any) string {
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func keyInfoFromMap(uuid string, m map[string]any) *KeyInfo {
	return NewKeyInfo(
		uuid,
		safeString(m[kvKeyLeaseID]),
		safeString(m[kvKeyTokenID]),
		safeString(m[kvKeyNamespace]),
		safeString(m[kvKeyServiceAccountName]),
		safeString(m[kvKeyPodName]),
		safeString(m[kvKeyNodeName]),
	)
}

func (c *Connector) GetKeyInfo(ctx context.Context, podName, uuid, path, prefix string) (*KeyInfo, error) {
	dataPath := fmt.Sprintf("%s/data/%s/%s", path, prefix, uuid)
	podSecret, err := c.client.Logical().ReadWithContext(ctx, dataPath)
	if err != nil {
		c.Log.Errorf("Error while trying to recover data informations for : %s: %v", uuid, err)
		return nil, err
	}
	if podSecret == nil || podSecret.Data == nil || podSecret.Data["data"] == nil {
		c.Log.Errorf("No data has been found for uuid %s and pod %s", uuid, podName)
		return nil, errors.Wrapf(ErrKeyNotFound, "no vault data found for uuid %s", uuid)
	}

	dataMap, ok := podSecret.Data["data"].(map[string]any)
	if !ok {
		c.Log.Errorf("Invalid data format for %s", uuid)
		return nil, errors.Newf("invalid data format for uuid %s", uuid)
	}
	return keyInfoFromMap(uuid, dataMap), nil
}

// isLeaseUnrecoverable reports whether a Vault renew/lookup error
// indicates the lease can never be renewed again, so the only sane
// follow-up is to revoke the token + delete the KV bookkeeping entry.
//
// Two distinct upstream conditions land here, both as HTTP 400 from Vault:
//
//   - "invalid lease": Vault has no record of the lease at all (already
//     expired or revoked).
//   - "could not find role": the lease is still tracked by Vault, but
//     the database role it was issued under has since been deleted, so
//     Vault cannot regenerate or extend the credential. From the
//     renewer's perspective, the lease is just as dead.
//
// The status-code guard (HTTP 400 only) reduces the risk of a proxy- or
// WAF-injected error string triggering spurious KV cleanup. Errors from
// other status codes (403, 429, 5xx) propagate to the caller unmodified.
func isLeaseUnrecoverable(err error) bool {
	if err == nil {
		return false
	}
	var ve *vault.ResponseError
	if !errors.As(err, &ve) {
		return false
	}
	if ve.StatusCode != 400 {
		return false
	}
	body := strings.Join(ve.Errors, "\n")
	return strings.Contains(body, "invalid lease") ||
		strings.Contains(body, "could not find role")
}

// ListKeyInfo lists all KeyInfo entries under the given path/prefix.
// Partial results: on per-key fetch failures, the function returns both a non-nil partial
// slice (successfully fetched entries) and a non-nil error (joined errors from failed keys).
// Callers MUST check and use the partial slice even when err != nil.
func (c *Connector) ListKeyInfo(ctx context.Context, path, prefix string) ([]*KeyInfo, error) {
	kvPath := fmt.Sprintf("%s/metadata/%s", path, prefix)

	secret, err := c.client.Logical().ListWithContext(ctx, kvPath)
	if err != nil {
		return nil, err
	}

	if secret == nil || secret.Data["keys"] == nil {
		return []*KeyInfo{}, nil
	}

	keys, ok := secret.Data["keys"].([]any)
	if !ok {
		return nil, errors.Newf("unexpected type for keys in vault list response at path %s", kvPath)
	}
	var wg sync.WaitGroup
	keyInfoChan := make(chan *KeyInfo, len(keys))
	errChan := make(chan error, len(keys))

	// Create a rate limiter
	rateLimit := rate.Limit(c.VaultRateLimit) // requests per second
	limiter := rate.NewLimiter(rateLimit, 1)

	for _, k := range keys {
		wg.Add(1)
		go func(k any) {
			defer wg.Done()

			// Wait for the rate limiter
			if err := limiter.Wait(ctx); err != nil {
				c.Log.Errorf("Rate limiter error: %v", err)
				errChan <- errors.Newf("rate limiter error: %v", err)
				return
			}

			kStr, ok := k.(string)
			if !ok {
				err := errors.Newf("unexpected type %T for key in vault list response", k)
				c.Log.Errorf("%v", err)
				errChan <- err
				return
			}
			podName := strings.TrimSuffix(kStr, "/")

			dataPath := fmt.Sprintf("%s/data/%s/%s", path, prefix, podName)
			podSecret, err := c.client.Logical().ReadWithContext(ctx, dataPath)
			if err != nil {
				c.Log.Errorf("Error while trying to recover data informations for: %s: %v", podName, err)
				errChan <- errors.Newf("failed to read data for %s: %v", podName, err)
				return
			}

			if podSecret == nil || podSecret.Data == nil || podSecret.Data["data"] == nil {
				c.Log.Debugf("No data found for key %s, skipping", podName)
				return
			}

			dataMap, ok := podSecret.Data["data"].(map[string]any)
			if !ok {
				err := errors.Newf("invalid data format for %s", podName)
				c.Log.Errorf("%v", err)
				errChan <- err
				return
			}
			keyInfoChan <- keyInfoFromMap(podName, dataMap)
		}(k)
	}

	wg.Wait()
	close(keyInfoChan)
	close(errChan)

	var keyInfos []*KeyInfo
	for ki := range keyInfoChan {
		keyInfos = append(keyInfos, ki)
	}

	var errs []error
	for e := range errChan {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		return keyInfos, errors.Join(errs...)
	}

	return keyInfos, nil
}

func (c *Connector) HandlePodDeletionToken(ctx context.Context, keysInformation *KeyInfo, secretName, prefix string) error {
	if err := c.RevokeOrphanToken(ctx, keysInformation.TokenID, keysInformation.PodNameUID, keysInformation.Namespace); err != nil {
		c.Log.Errorf("Can't revoke Token with UUID %s: %v", keysInformation.PodNameUID, err)
		return err
	}

	// Token revocation succeeded — purge the KV bookkeeping entry too,
	// otherwise the renewer would re-discover it next cycle and
	// re-purge via the isLeaseUnrecoverable fast-path. The revoker
	// owns the full lifecycle of this entry; do the cleanup here.
	if err := c.DeleteData(ctx, secretName, keysInformation.PodNameUID, keysInformation.Namespace, prefix); err != nil {
		c.Log.Errorf("KV delete after revoke failed for uuid %s: %v", keysInformation.PodNameUID, err)
		return err
	}

	metrics.LeaseExpirationInTime.DeleteLabelValues(keysInformation.PodNameUID, keysInformation.Namespace)
	metrics.TokenExpirationInTime.DeleteLabelValues(keysInformation.PodNameUID, keysInformation.Namespace)
	c.Log.Infof("Token with uuid %s revoked and KV entry deleted", keysInformation.PodNameUID)
	return nil
}

func (c *Connector) SyncAndCleanupTokens(ctx context.Context, cfg *config.Config, keysInformations []*KeyInfo, secretName, prefix string, podService k8s.PodService, syncTTLSeconds int) bool {
	podsInformations, err := podService.GetAllPodAndNamespace(ctx)
	if err != nil {
		c.Log.Errorf("Error while trying to get Pod from Kubernetes %v", err)
		return false
	}

	// Create a map for quick lookup of pod information
	podInfoMap := make(map[string]k8s.PodInfo)
	for _, pi := range podsInformations {
		for _, uuid := range pi.PodNameUUIDs {
			podInfoMap[uuid] = pi
		}
	}

	// Use the renewer's own login token (set by ConnectAndRenew) for all
	// renew/revoke/KV operations. The previous version created an orphan
	// token here purely as a side-effect of the legacy broad-policy era;
	// in projected-SA mode the renewer's policy is dedicated and minimal,
	// and create-orphan is intentionally NOT granted to it.

	// Create a rate limiter
	rateLimit := rate.Limit(cfg.VaultRateLimit) // requests per second
	limiter := rate.NewLimiter(rateLimit, 1)

	var wg sync.WaitGroup
	var isOk atomic.Bool
	isOk.Store(true)

	for _, ki := range keysInformations {
		wg.Add(1)
		go func(ki *KeyInfo) {
			defer wg.Done()

			// Wait for the rate limiter
			if err := limiter.Wait(ctx); err != nil {
				c.Log.Errorf("Rate limiter error: %v", err)
				isOk.Store(false)
				return
			}

			if _, found := podInfoMap[ki.PodNameUID]; found {
				err := c.RenewToken(ctx, ki.TokenID, ki.PodNameUID, ki.Namespace, syncTTLSeconds)
				if err != nil {
					c.Log.Errorf("Can't renew Token with pod UUID: %s", ki.PodNameUID)
					isOk.Store(false)
					return
				}
				err = c.RenewLease(ctx, ki.LeaseID, 86400*5, ki.PodNameUID, ki.Namespace) // Renew for 1 week
				if err != nil {
					if isLeaseUnrecoverable(err) {
						// Pod is still scheduled but its DB role no longer
						// exists in Vault — keeping the KV entry would
						// log-spam every cycle. Revoke (best-effort) and
						// purge so the operator's next sync run is clean.
						c.Log.Infof("Lease %s unrecoverable for uuid %s (%v), purging KV entry", ki.LeaseID, ki.PodNameUID, err)
						if revErr := c.RevokeOrphanToken(ctx, ki.TokenID, ki.PodNameUID, ki.Namespace); revErr != nil {
							c.Log.Warnf("Token revoke for unrecoverable lease %s failed (continuing): %v", ki.LeaseID, revErr)
						}
						if delErr := c.DeleteData(ctx, secretName, ki.PodNameUID, ki.Namespace, prefix); delErr != nil {
							c.Log.Errorf("Data for %s can't be deleted: %v", ki.PodNameUID, delErr)
							isOk.Store(false)
							return
						}
						metrics.LeaseExpirationInTime.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						metrics.TokenExpirationInTime.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						metrics.RenewLeaseCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						metrics.RenewTokenCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						metrics.DataDeletedCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						return
					}
					c.Log.Errorf("Can't renew Lease with pod UUID: %s", ki.PodNameUID)
					isOk.Store(false)
					return
				}
				if ki.ServiceAccount == "" || ki.NodeName == "" || ki.PodName == "" {
					fullyKiInfo := NewKeyInfo(ki.PodNameUID, ki.LeaseID, ki.TokenID, ki.Namespace, podInfoMap[ki.PodNameUID].ServiceAccountName, podInfoMap[ki.PodNameUID].PodName, podInfoMap[ki.PodNameUID].NodeName)
					c.Log.Debugf("Renewing information for UUID %s", ki.PodNameUID)
					if err := c.StoreData(ctx, "id-handle-token", fullyKiInfo, secretName, ki.PodNameUID, ki.Namespace, prefix); err != nil {
						c.Log.Infof("Extended vault information could not been saved, process will continue : %v", err)
					}
				}
			} else {
				leaseTooYoung, err := c.isLeaseTooYoung(ctx, ki.LeaseID)
				if err != nil {
					// "invalid lease" means Vault no longer knows this lease
					// (already expired or revoked). The KV entry is otherwise
					// unmanageable and would log-spam every cycle, so finish
					// the cleanup: revoke the orphan token (no-ops if gone)
					// and delete the KV entry.
					if isLeaseUnrecoverable(err) {
						c.Log.Infof("Lease %s already gone for uuid %s (%v), purging KV entry", ki.LeaseID, ki.PodNameUID, err)
						if revErr := c.RevokeOrphanToken(ctx, ki.TokenID, ki.PodNameUID, ki.Namespace); revErr != nil {
							c.Log.Errorf("Can't revoke Token with UUID: %s: %v", ki.PodNameUID, revErr)
							isOk.Store(false)
							return
						}
						if delErr := c.DeleteData(ctx, secretName, ki.PodNameUID, ki.Namespace, prefix); delErr != nil {
							c.Log.Errorf("Data for %s can't be deleted: %v", ki.PodNameUID, delErr)
							isOk.Store(false)
							return
						}
						metrics.LeaseExpirationInTime.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						metrics.TokenExpirationInTime.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						metrics.RenewLeaseCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						metrics.RenewTokenCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						metrics.DataDeletedCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
						return
					}
					c.Log.Warnf("Cannot determine lease age for %s, skipping cleanup: %v", ki.LeaseID, err)
					return
				}
				if leaseTooYoung {
					c.Log.Infof("This lease: %s is too young to be cleaned up.", ki.LeaseID)
					return
				}
				err = c.RevokeOrphanToken(ctx, ki.TokenID, ki.PodNameUID, ki.Namespace)
				if err != nil {
					c.Log.Errorf("Can't revoke Token with UUID: %s", ki.PodNameUID)
					isOk.Store(false)
					return
				}
				if err := c.DeleteData(ctx, secretName, ki.PodNameUID, ki.Namespace, prefix); err != nil {
					c.Log.Errorf("Data for %s can't be deleted: %v", ki.PodNameUID, err)
					isOk.Store(false)
					return
				}
				metrics.LeaseExpirationInTime.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				metrics.TokenExpirationInTime.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				metrics.RenewLeaseCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				metrics.RenewTokenCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				metrics.DataDeletedCount.DeleteLabelValues(ki.PodNameUID, ki.Namespace)
				c.Log.Infof("Token has been revoked and data deleted")
			}
		}(ki)
	}

	wg.Wait()
	// The legacy code revoked the per-cycle orphan token here and
	// reset c.vaultToken to K8sSaVaultToken. With the orphan dance
	// removed, c.vaultToken IS the renewer's own login token —
	// revoking it would 403 every subsequent operation (and force a
	// reconnect every cycle). The login token's lifetime is managed
	// by StartTokenRenewal (RenewSelfToken), so leave it intact.
	return isOk.Load()
}

func (c *Connector) RevokeOrphanToken(ctx context.Context, tokenID, uuid, namespace string) error {
	err := c.client.Auth().Token().RevokeOrphanWithContext(ctx, tokenID)
	if err != nil {
		if strings.Contains(err.Error(), "token to revoke not found") {
			c.Log.Debugf("Token for uuid %s has already been revoked by the revoker", uuid)
			//metrics.RevokeTokenErrorCount.WithLabelValues(uuid, namespace).Inc()
			return nil
		}
		metrics.RevokeTokenErrorCount.WithLabelValues(uuid, namespace).Inc()
		c.Log.Errorf("error while revoking token: %v", err)
		return err
	}
	metrics.RevokeTokenCount.WithLabelValues(namespace).Inc()
	metrics.TokenExpirationInTime.DeleteLabelValues(uuid, namespace)
	return nil
}

// RevokeSelfToken revokes the given tokenID by building a fresh Vault client
// authenticated as that token and calling auth/token/revoke-self.
// This avoids the SDK footgun where RevokeSelfWithContext ignores the tokenID
// argument and always revokes whatever token the caller's client currently holds.
func (c *Connector) RevokeSelfToken(ctx context.Context, tokenID string) error {
	if tokenID == "" {
		return nil
	}
	cli, err := vault.NewClient(&vault.Config{Address: c.address})
	if err != nil {
		return errors.Wrap(err, "RevokeSelfToken: build client")
	}
	cli.SetToken(tokenID)
	abbrev := tokenID
	if len(tokenID) > 8 {
		abbrev = tokenID[:8]
	}
	if err := cli.Auth().Token().RevokeSelfWithContext(ctx, ""); err != nil {
		metrics.RevokeTokenErrorCount.WithLabelValues("", "").Inc()
		c.Log.Errorf("error while revoking token: %v", err)
		return errors.Wrapf(err, "RevokeSelfToken: revoke %s", abbrev)
	}
	metrics.RevokeTokenCount.WithLabelValues("").Inc()
	return nil
}

func (c *Connector) RenewToken(ctx context.Context, tokenID, uuid, namespace string, syncTTLSeconds int) error {
	tokenRenew, err := c.client.Auth().Token().RenewWithContext(ctx, tokenID, syncTTLSeconds)
	if err != nil {
		if strings.Contains(err.Error(), "token not found") {
			c.Log.Debugf("can't renew revoked token for uuid %s : it has been revoked by the revoker", uuid)
			return nil
		}
		c.Log.Errorf("error while renewing token: %v", err)
		metrics.RenewTokenErrorCount.WithLabelValues(uuid, namespace).Inc()
		return err
	}
	metrics.RenewTokenCount.WithLabelValues(uuid, namespace).Inc()
	tokenDuration, err := tokenRenew.TokenTTL()
	if err != nil {
		c.Log.Errorf("error retriving token ttl: %v", err)
		return nil
	}
	currentTime := time.Now()
	expirationTime := currentTime.Add(tokenDuration)
	expirationEpoch := expirationTime.Unix()
	metrics.TokenExpirationInTime.WithLabelValues(uuid, namespace).Set(float64(expirationEpoch))
	return nil
}

func (c *Connector) RenewSelfToken(ctx context.Context) error {
	_, err := c.client.Auth().Token().RenewSelfWithContext(ctx, 60*60*24) // renew for 1 day
	if err != nil {
		c.Log.Errorf("error while renewing token: %v", err)
		return err
	}
	return nil
}

// When you create a pod, sometime, it is not scheduled directly due to some cluster issue such has limit range / ...
// To avoid the token / leaseID to be deleted, we decide that YoungToken should not be deleted by the Renewer
// This doesnt change how the revoker work.
// The value is totally arbitrary, this is to avoid corner case where pod are not scheduled direcly and their creds are deleted before the scheduler schedule them.
func (c *Connector) isLeaseTooYoung(ctx context.Context, leaseID string) (bool, error) {
	leaseInformations, err := c.client.Sys().LookupWithContext(ctx, leaseID)
	if err != nil {
		// Conservative: treat lookup failure as "too young" to avoid premature revocation.
		return true, errors.Wrapf(err, "sys.Lookup failed for lease %s", leaseID)
	}
	issueTime, ok := leaseInformations.Data["issue_time"].(string)
	if !ok {
		return false, errors.Newf("unexpected type for issue_time in lease %s", leaseID)
	}
	issueTimeParsed, err := time.Parse(time.RFC3339, issueTime)
	if err != nil {
		return false, errors.Wrap(err, "error parsing issue time")
	}
	timeSinceIssue := time.Since(issueTimeParsed)
	isYoungerThanTenMinutes := timeSinceIssue < 10*time.Minute

	return isYoungerThanTenMinutes, nil
}

func (c *Connector) RenewLease(ctx context.Context, leaseID string, leaseTTL int, uuid, namespace string) error {
	secret, err := c.client.Sys().RenewWithContext(ctx, leaseID, leaseTTL)
	if err != nil {
		if strings.Contains(err.Error(), "lease not found") {
			c.Log.Debugf("Lease for uuid %s has been revoked by the revoker", uuid)
			return nil
		}
		// Unrecoverable errors (lease gone, role deleted) are handled by
		// the caller via isLeaseUnrecoverable + KV purge. Log them at
		// debug level here so Sentry doesn't fire on the way up the
		// stack — the caller's purge log line covers operator visibility.
		if isLeaseUnrecoverable(err) {
			c.Log.Debugf("RenewLease unrecoverable for uuid %s (caller will purge): %v", uuid, err)
			metrics.RenewLeaseErrorCount.WithLabelValues(uuid, namespace).Inc()
			return err
		}
		c.Log.Errorf("error while renewing lease: %v", err)
		metrics.RenewLeaseErrorCount.WithLabelValues(uuid, namespace).Inc()
		return err
	}

	metrics.RenewLeaseCount.WithLabelValues(uuid, namespace).Inc()

	leaseDuration := time.Duration(secret.LeaseDuration) * time.Second
	currentTime := time.Now()
	expirationTime := currentTime.Add(leaseDuration)
	expirationEpoch := expirationTime.Unix()

	metrics.LeaseExpirationInTime.WithLabelValues(uuid, namespace).Set(float64(expirationEpoch))
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
					newConn, err := ConnectToVault(ctx, cfg, c.k8sSaToken)
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
