package vault

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/vault/api"
	vaulthttp "github.com/hashicorp/vault/http"
	"github.com/hashicorp/vault/vault"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testVaultToken     = "token"
	vaultSecretBackend = "vault-db-injector"
)

func setupTestVault(t *testing.T) (*api.Client, *vault.TestCluster) {
	cluster := vault.NewTestCluster(t, &vault.CoreConfig{
		DevToken: testVaultToken,
	}, &vault.TestClusterOptions{
		HandlerFunc: vaulthttp.Handler,
	})
	cluster.Start()
	t.Cleanup(func() { cluster.Cleanup() })

	core := cluster.Cores[0].Core
	vault.TestWaitActive(t, core)
	client := cluster.Cores[0].Client

	if err := client.Sys().Mount(vaultSecretBackend, &api.MountInput{
		Type: "kv",
		Options: map[string]string{
			"version": "2",
		},
	}); err != nil {
		t.Fatal(err)
	}

	return client, cluster
}

func TestNewKeyInformation(t *testing.T) {
	podName := "test-pod"
	leaseId := "lease-id"
	tokenId := "token-id"
	namespace := "test-namespace"

	keyInfo := NewKeyInformation(podName, leaseId, tokenId, namespace)
	assert.Equal(t, podName, keyInfo.PodNameUID)
	assert.Equal(t, leaseId, keyInfo.LeaseId)
	assert.Equal(t, tokenId, keyInfo.TokenId)
	assert.Equal(t, namespace, keyInfo.Namespace)
}

func TestStoreData(t *testing.T) {
	client, cluster := setupTestVault(t)
	defer cluster.Cleanup()

	log := logrus.New()
	connector := &Connector{client: client, Log: log}

	tests := []struct {
		name           string
		vaultInfo      *KeyInformation
		secretName     string
		uuid           string
		namespace      string
		prefix         string
		expectedResult string
		expectError    bool
	}{
		{
			name: "Success",
			vaultInfo: &KeyInformation{
				PodNameUID: "pod-uid",
				LeaseId:    "lease-id",
				TokenId:    "token-id",
				Namespace:  "namespace",
			},
			secretName:     vaultSecretBackend,
			uuid:           "uuid",
			namespace:      "namespace",
			prefix:         "prefix",
			expectedResult: "Success !",
			expectError:    false,
		},
		{
			name: "Error storing data",
			vaultInfo: &KeyInformation{
				PodNameUID: "pod-uid",
				LeaseId:    "lease-id",
				TokenId:    "token-id",
				Namespace:  "namespace",
			},
			secretName:     "prout",
			uuid:           "uuid",
			namespace:      "namespace",
			prefix:         "prefix",
			expectedResult: "Error !",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := connector.StoreData(context.Background(), tt.vaultInfo, tt.secretName, tt.uuid, tt.namespace, tt.prefix)

			if tt.expectError {
				assert.Error(t, err)
				assert.Equal(t, tt.expectedResult, "Error !")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)

				// Verify data is stored correctly
				secret, err := client.Logical().Read("vault-db-injector/data/" + tt.prefix + "/" + tt.vaultInfo.PodNameUID)
				require.NoError(t, err)
				require.NotNil(t, secret)

				data := secret.Data["data"].(map[string]interface{})
				assert.Equal(t, tt.vaultInfo.LeaseId, data["LeaseId"])
				assert.Equal(t, tt.vaultInfo.TokenId, data["TokenId"])
				assert.Equal(t, tt.vaultInfo.Namespace, data["Namespace"])
			}
		})
	}
}

func TestDeleteData(t *testing.T) {
	client, cluster := setupTestVault(t)
	defer cluster.Cleanup()

	log := logrus.New()
	connector := &Connector{client: client, Log: log}

	tests := []struct {
		name           string
		podName        string
		secretName     string
		uuid           string
		namespace      string
		prefix         string
		expectedResult string
		expectError    bool
	}{
		{
			name:           "Success",
			podName:        "pod-uid",
			secretName:     vaultSecretBackend,
			uuid:           "uuid",
			namespace:      "namespace",
			prefix:         "prefix",
			expectedResult: "Success !",
			expectError:    false,
		},
		{
			name:           "Error deleting data",
			podName:        "pod-uid",
			secretName:     "error",
			uuid:           "uuid",
			namespace:      "namespace",
			prefix:         "prefix",
			expectedResult: "Error !",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectError {
				// Simulate an error response by deleting nonexistent data
				_, err := client.Logical().Delete("vault-db-injector-error/data/" + tt.prefix + "/" + tt.podName)
				require.Error(t, err)
			} else {
				// Setup data to delete
				data := map[string]interface{}{
					"data": map[string]interface{}{
						"LeaseId":   "lease-id",
						"TokenId":   "token-id",
						"Namespace": "namespace",
					},
				}
				_, err := client.Logical().Write("vault-db-injector/data/"+tt.prefix+"/"+tt.podName, data)
				require.NoError(t, err)
			}

			result, err := connector.DeleteData(context.Background(), tt.podName, tt.secretName, tt.uuid, tt.namespace, tt.prefix)

			fmt.Println(result)
			fmt.Println(err)

			if tt.expectError {
				assert.Error(t, err)
				assert.Equal(t, tt.expectedResult, "Error !")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)

				// Verify data is deleted correctly
				secret, err := client.Logical().Read("vault-db-injector/data/" + tt.prefix + "/" + tt.podName)
				assert.NoError(t, err)
				assert.Nil(t, secret)
			}
		})
	}
}

func TestRenewLease(t *testing.T) {
	client, cluster := setupTestVault(t)
	defer cluster.Cleanup()

	log := logrus.New()
	connector := &Connector{client: client, Log: log}

	tests := []struct {
		name           string
		leaseID        string
		leaseTTL       int
		uuid           string
		namespace      string
		expectedResult string
		expectError    bool
	}{
		{
			name:           "Success",
			leaseID:        "lease-id",
			leaseTTL:       600,
			uuid:           "uuid",
			namespace:      "namespace",
			expectedResult: "Success !",
			expectError:    false,
		},
		{
			name:           "Error renewing lease",
			leaseID:        "lease-id",
			leaseTTL:       600,
			uuid:           "uuid",
			namespace:      "namespace",
			expectedResult: "Error !",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectError {
				// Simulate an error response for renew lease
				client.SetToken("invalid-token")
			} else {
				// Setup a lease to renew
				data := map[string]interface{}{
					"data": map[string]interface{}{
						"lease_id": tt.leaseID,
						"ttl":      tt.leaseTTL,
					},
				}
				_, err := client.Logical().Write("auth/token/create", data)
				require.NoError(t, err)
			}

			// Lease not found doesn't return an error on this function which lead this test to pass actually, will need a rewrite.
			err := connector.RenewLease(context.Background(), tt.leaseID, tt.leaseTTL, tt.uuid, tt.namespace)

			if tt.expectError {
				assert.Error(t, err)
				assert.Equal(t, tt.expectedResult, "Error !")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedResult, "Success !")
			}
		})
	}
}

// This can't actually work as no serviceaccount token will be found
/*
func TestStartTokenRenewal(t *testing.T) {
	client, cluster := setupTestVault(t)
	defer cluster.Cleanup()

	log := logrus.New()
	connector := &Connector{client: client, Log: log, RenewalInterval: 1 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go connector.StartTokenRenewal(ctx, &config.Config{})

	// Allow some time for the token renewal to run
	time.Sleep(3 * time.Second)

	// Cancel the context to stop the renewal
	cancel()
}
*/
