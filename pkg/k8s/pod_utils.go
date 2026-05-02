package k8s

import (
	"context"
	"fmt"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/numberly/vault-db-injector/pkg/metrics"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

type PodService interface {
	GetAllPodAndNamespace(ctx context.Context) ([]PodInfo, error)
}

type KubernetesClient interface {
	CoreV1() v1.CoreV1Interface
	CoordinationV1() coordinationv1.CoordinationV1Interface
	GetServiceAccountToken() (string, error)
	RawClientset() kubernetes.Interface
}

type podServiceImpl struct {
	clientset KubernetesClient
	cfg       *config.Config
	log       logger.Logger
}

func NewPodService(clientset KubernetesClient, cfg *config.Config) PodService {
	return &podServiceImpl{
		clientset: clientset,
		log:       logger.GetLogger(),
		cfg:       cfg,
	}
}

type PodInfo struct {
	PodNameUUIDs       []string
	Namespace          string
	ServiceAccountName string
	PodName            string
	NodeName           string
}

func (p *podServiceImpl) GetAllPodAndNamespace(ctx context.Context) ([]PodInfo, error) {
	listOptions := metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=true", p.cfg.InjectorLabel),
	}
	pods, err := p.clientset.CoreV1().Pods("").List(ctx, listOptions)
	if err != nil {
		metrics.GetAllPodErrorCount.WithLabelValues().Inc()
		return nil, err
	}

	if len(pods.Items) == 0 {
		metrics.GetAllPodErrorCount.WithLabelValues().Inc()
		return nil, errors.Newf("no pods found in the cluster")
	}

	estimatedSize := len(pods.Items)
	podInfos := make([]PodInfo, 0, estimatedSize)

	for _, pod := range pods.Items {
		if uuid, exists := pod.GetAnnotations()[ANNOTATION_VAULT_POD_UUID]; exists {
			podInfos = append(podInfos, PodInfo{
				PodNameUUIDs:       strings.Split(uuid, ","),
				Namespace:          pod.Namespace,
				PodName:            pod.Name,
				NodeName:           pod.Spec.NodeName,
				ServiceAccountName: pod.Spec.ServiceAccountName,
			})
		}
	}

	metrics.GetAllPodSuccessCount.WithLabelValues().Inc()
	return podInfos, nil
}
