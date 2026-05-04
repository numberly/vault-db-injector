package k8s

import (
	"context"

	"github.com/cockroachdb/errors"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RequestSAToken issues a Kubernetes TokenRequest for the given
// ServiceAccount and returns the resulting JWT. Used to log in to
// Vault under the pod's identity rather than the injector's.
//
// expirationSeconds may be clamped up by the apiserver to
// --service-account-min-token-expiration (default 600s).
func (a *KubernetesClientAdapter) RequestSAToken(ctx context.Context, namespace, saName string, audiences []string, expirationSeconds int64) (string, error) {
	exp := expirationSeconds
	tr := &authv1.TokenRequest{
		Spec: authv1.TokenRequestSpec{
			Audiences:         audiences,
			ExpirationSeconds: &exp,
		},
	}
	out, err := a.Clientset.CoreV1().ServiceAccounts(namespace).CreateToken(ctx, saName, tr, metav1.CreateOptions{})
	if err != nil {
		return "", errors.Wrapf(err, "TokenRequest for %s/%s", namespace, saName)
	}
	if out.Status.Token == "" {
		return "", errors.Newf("TokenRequest for %s/%s returned empty token", namespace, saName)
	}
	return out.Status.Token, nil
}
