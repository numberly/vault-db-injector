package k8s

import (
	"context"
	"crypto/x509"
	"os"
	"path/filepath"

	"github.com/cockroachdb/errors"
	"k8s.io/client-go/kubernetes"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type Client struct{}

type ClientInterface interface {
	GetServiceAccountToken() (string, error)
	GetKubernetesCACert() (*x509.CertPool, error)
	GetKubernetesClient() (*kubernetes.Clientset, error)
	RequestSAToken(ctx context.Context, namespace, saName string, audiences []string, expirationSeconds int64) (string, error)
}

// KubernetesClientAdapter wraps a kubernetes.Interface and adds
// GetServiceAccountToken so it satisfies KubernetesClient.
type KubernetesClientAdapter struct {
	Clientset kubernetes.Interface
}

// NewKubernetesClientAdapter returns a KubernetesClientAdapter for the given clientset.
// It accepts kubernetes.Interface so both real and fake clientsets can be used.
func NewKubernetesClientAdapter(cs kubernetes.Interface) *KubernetesClientAdapter {
	return &KubernetesClientAdapter{Clientset: cs}
}

func (a *KubernetesClientAdapter) CoreV1() v1.CoreV1Interface {
	return a.Clientset.CoreV1()
}

func (a *KubernetesClientAdapter) CoordinationV1() coordinationv1.CoordinationV1Interface {
	return a.Clientset.CoordinationV1()
}

func (a *KubernetesClientAdapter) GetServiceAccountToken() (string, error) {
	return getServiceAccountTokenImpl(tokenFilePath)
}

func (a *KubernetesClientAdapter) RawClientset() kubernetes.Interface {
	return a.Clientset
}

var _ KubernetesClient = (*KubernetesClientAdapter)(nil)

const tokenFilePath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

func NewClient() *Client {
	return &Client{}
}

func (c *Client) GetServiceAccountToken() (string, error) {
	return getServiceAccountTokenImpl(tokenFilePath)
}

func getServiceAccountTokenImpl(tokenFilePath string) (string, error) {
	token, err := os.ReadFile(tokenFilePath)
	if err != nil {
		return "", errors.Newf("failed to read service account token: %w", err)
	}
	return string(token), nil
}

func (c *Client) GetKubernetesCACert() (*x509.CertPool, error) {
	caCertPath := "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	caCert, err := os.ReadFile(caCertPath)
	if err != nil {
		return nil, err
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)
	return caCertPool, nil
}

func (c *Client) RequestSAToken(ctx context.Context, namespace, saName string, audiences []string, expirationSeconds int64) (string, error) {
	cs, err := c.GetKubernetesClient()
	if err != nil {
		return "", err
	}
	return NewKubernetesClientAdapter(cs).RequestSAToken(ctx, namespace, saName, audiences, expirationSeconds)
}

func (c *Client) GetKubernetesClient() (*kubernetes.Clientset, error) {
	var kubeconfig string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	config, err := rest.InClusterConfig()
	if err != nil {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, err
		}
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientset, nil
}
