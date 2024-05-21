package k8s

import (
	"crypto/x509"
	"os"
	"path/filepath"

	"github.com/cockroachdb/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

type Client struct{}

type ClientInterface interface {
	GetServiceAccountToken() (string, error)
	GetKubernetesCACert() (*x509.CertPool, error)
	GetKubernetesClient() (*kubernetes.Clientset, error)
}

const tokenFilePath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

func NewClient() *Client {
	return &Client{}
}

func (c *Client) GetServiceAccountToken() (string, error) {
	return GetServiceAccountTokenImpl(tokenFilePath)
}

func GetServiceAccountTokenImpl(tokenFilePath string) (string, error) {
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
