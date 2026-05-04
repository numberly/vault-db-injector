package k8s

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestRequestSAToken_PassesAudiencesAndTTL(t *testing.T) {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "team-x"},
	}
	cs := fake.NewSimpleClientset(sa)

	cs.PrependReactor("create", "serviceaccounts", func(action clienttesting.Action) (bool, runtime.Object, error) {
		ca, ok := action.(clienttesting.CreateAction)
		if !ok || ca.GetSubresource() != "token" {
			return false, nil, nil
		}
		tr, ok := ca.GetObject().(*authv1.TokenRequest)
		require.True(t, ok)
		assert.Equal(t, []string{"vault"}, tr.Spec.Audiences)
		require.NotNil(t, tr.Spec.ExpirationSeconds)
		assert.EqualValues(t, 60, *tr.Spec.ExpirationSeconds)
		return true, &authv1.TokenRequest{Status: authv1.TokenRequestStatus{Token: "fake-jwt"}}, nil
	})

	a := NewKubernetesClientAdapter(cs)
	tok, err := a.RequestSAToken(context.Background(), "team-x", "myapp", []string{"vault"}, 60)
	require.NoError(t, err)
	assert.Equal(t, "fake-jwt", tok)
}
