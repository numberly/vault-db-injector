package k8s

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

// VaultLoginToken returns the JWT to be used for Vault login on
// behalf of the given pod. In legacy mode it returns the caller's
// SA token (the injector or NRI plugin's own SA, mounted at
// /var/run/secrets/kubernetes.io/serviceaccount/token). In
// projected-SA mode it returns a TokenRequest-issued JWT for the
// pod's own SA.
func VaultLoginToken(ctx context.Context, client ClientInterface, pod *corev1.Pod, useProjectedSA bool, audiences []string, expirationSeconds int64) (string, error) {
	if !useProjectedSA {
		return client.GetServiceAccountToken()
	}
	saName := pod.Spec.ServiceAccountName
	if saName == "" {
		saName = "default"
	}
	return client.RequestSAToken(ctx, pod.Namespace, saName, audiences, expirationSeconds)
}
