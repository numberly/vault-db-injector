package k8s_test

import (
	"context"
	"strings"
	"testing"

	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/config"
	"gitlab.numberly.in/team-infrastructure/kube-vault-db-injector/pkg/k8s"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/stretchr/testify/assert"
)

func TestGetAllPodAndNamespace_NoPodsFound(t *testing.T) {
	ctx := context.TODO()
	cfg := &config.Config{}
	cfg.InjectorLabel = "vault-db-injector"
	clientset := fake.NewSimpleClientset() // This gives us a mock Kubernetes client

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
	clientset := fake.NewSimpleClientset(mockPod)

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

	clientset := fake.NewSimpleClientset()
	for _, pod := range pods {
		_, err := clientset.CoreV1().Pods(pod.Namespace).Create(ctx, &pod, metav1.CreateOptions{})
		assert.NoError(t, err)
	}

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
	clientset := fake.NewSimpleClientset(&nonAnnotatedPod)

	podService := k8s.NewPodService(clientset, cfg)

	result, err := podService.GetAllPodAndNamespace(ctx)
	assert.NoError(t, err)
	// We expect an empty list if the pod does not have the required annotation
	assert.Empty(t, result)
}
