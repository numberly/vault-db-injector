package k8s_test

import (
	"context"
	"strings"
	"testing"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	corev1iface "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/stretchr/testify/assert"
)

// fakeKubernetesClientAdapter wraps fake.Clientset to satisfy k8s.KubernetesClient.
type fakeKubernetesClientAdapter struct {
	inner *fake.Clientset
}

func (f *fakeKubernetesClientAdapter) CoreV1() corev1iface.CoreV1Interface {
	return f.inner.CoreV1()
}

func (f *fakeKubernetesClientAdapter) CoordinationV1() coordinationv1.CoordinationV1Interface {
	return f.inner.CoordinationV1()
}

func (f *fakeKubernetesClientAdapter) GetServiceAccountToken() (string, error) {
	return "fake-token", nil
}

var _ k8s.KubernetesClient = (*fakeKubernetesClientAdapter)(nil)

func TestGetAllPodAndNamespace_NoPodsFound(t *testing.T) {
	ctx := context.TODO()
	cfg := &config.Config{}
	cfg.InjectorLabel = "vault-db-injector"
	clientset := &fakeKubernetesClientAdapter{inner: fake.NewSimpleClientset()}

	podService := k8s.NewPodService(clientset, cfg)

	_, err := podService.GetAllPodAndNamespace(ctx)
	assert.Error(t, err)
	assert.Equal(t, "no pods found in the cluster", err.Error())
}

func TestGetAllPodAndNamespace_PodsFound(t *testing.T) {
	ctx := context.TODO()
	cfg := &config.Config{}
	cfg.InjectorLabel = "vault-db-injector"
	// Create a mock pod with the expected annotation
	mockPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			Annotations: map[string]string{
				k8s.ANNOTATION_VAULT_POD_UUID: "uuid1234",
			},
			Labels: map[string]string{
				cfg.InjectorLabel: "true",
			},
		},
	}

	// Create a fake clientset with the mock pod
	clientset := &fakeKubernetesClientAdapter{inner: fake.NewSimpleClientset(mockPod)}

	podService := k8s.NewPodService(clientset, cfg)

	result, err := podService.GetAllPodAndNamespace(ctx)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, []string{"uuid1234"}, result[0].PodNameUUIDs)
	assert.Equal(t, "default", result[0].Namespace)
}

func TestGetAllPodAndNamespace_PodsWithAnnotations(t *testing.T) {
	ctx := context.TODO()

	cfg := &config.Config{}
	cfg.InjectorLabel = "vault-db-injector"
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-pod-1",
				Namespace:   "default",
				Annotations: map[string]string{k8s.ANNOTATION_VAULT_POD_UUID: "uuid1234,uuid1235"},
				Labels: map[string]string{
					cfg.InjectorLabel: "true",
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-pod-2",
				Namespace:   "production",
				Annotations: map[string]string{k8s.ANNOTATION_VAULT_POD_UUID: "uuid5678"},
				Labels: map[string]string{
					cfg.InjectorLabel: "true",
				},
			},
		},
	}

	inner := fake.NewSimpleClientset()
	for _, pod := range pods {
		_, err := inner.CoreV1().Pods(pod.Namespace).Create(ctx, &pod, metav1.CreateOptions{})
		assert.NoError(t, err)
	}
	clientset := &fakeKubernetesClientAdapter{inner: inner}

	podService := k8s.NewPodService(clientset, cfg)

	result, err := podService.GetAllPodAndNamespace(ctx)
	assert.NoError(t, err)
	assert.Len(t, result, len(pods))
	// Check for presence of pod names and annotations in the result

	for i, res := range result {
		uuids := strings.Join(res.PodNameUUIDs, ",")
		assert.Equal(t, pods[i].ObjectMeta.Annotations[k8s.ANNOTATION_VAULT_POD_UUID], uuids)
		assert.Equal(t, pods[i].Namespace, res.Namespace)
	}
}

func TestGetAllPodAndNamespace_PodsWithoutAnnotations(t *testing.T) {
	ctx := context.TODO()
	cfg := &config.Config{}
	cfg.InjectorLabel = "vault-db-injector"
	// Pod without the required annotation
	nonAnnotatedPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-no-annotation",
			Namespace: "default",
			Labels: map[string]string{
				cfg.InjectorLabel: "true",
			},
		},
	}

	// Create a fake clientset with the non-annotated pod
	clientset := &fakeKubernetesClientAdapter{inner: fake.NewSimpleClientset(&nonAnnotatedPod)}

	podService := k8s.NewPodService(clientset, cfg)

	result, err := podService.GetAllPodAndNamespace(ctx)
	assert.NoError(t, err)
	// We expect an empty list if the pod does not have the required annotation
	assert.Empty(t, result)
}
