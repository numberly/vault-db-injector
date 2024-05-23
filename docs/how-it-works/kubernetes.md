# Kubernetes Integration

<!-- vscode-markdown-toc -->
* 1. [How It Works:](#HowItWorks:)
* 2. [Key Responsibilities:](#KeyResponsibilities:)
* 3. [Benefits:](#Benefits:)
	* 3.1. [Annotations :](#Annotations:)

<!-- vscode-markdown-toc-config
	numbering=true
	autoSave=true
	/vscode-markdown-toc-config -->
<!-- /vscode-markdown-toc -->

**Key Files:** `pkg/k8s/connect.go`, `pkg/k8s/pod_utils.go`, `pkg/k8s/parse_annotations.go`

##  1. <a name='HowItWorks:'></a>How It Works:

Kubernetes Integration is a fundamental feature that enables the application to interact with the Kubernetes API. This integration allows the application to manage and manipulate Kubernetes resources, facilitating tasks such as credential injection, pod management, and more.

##  2. <a name='KeyResponsibilities:'></a>Key Responsibilities:

1. **Kubernetes Client Initialization:**
   - The application initializes a Kubernetes client that can interact with the Kubernetes API. This client is used to perform various operations such as reading pod annotations, accessing secrets, and more.

2. **Service Account Token Retrieval:**
   - The application retrieves the service account token from the Kubernetes environment. This token is used for authenticating API requests made by the application.

3. **CA Certificate Retrieval:**
   - The application retrieves the Kubernetes CA certificate. This certificate is used to establish secure communication with the Kubernetes API server.

4. **Pod Annotation Parsing:**
   - The application reads and parses annotations from Kubernetes pods. These annotations can include instructions for credential injection and other custom configurations.

##  3. <a name='Benefits:'></a>Benefits:

- **Seamless Kubernetes Operations:**
  - By integrating with the Kubernetes API, the application can perform a wide range of operations directly within the Kubernetes cluster. This seamless integration simplifies the management of Kubernetes resources.

- **Security:**
  - Using service account tokens and CA certificates ensures that all interactions with the Kubernetes API are secure and authenticated. This enhances the security of the application's operations.

- **Dynamic Configuration:**
  - Parsing pod annotations allows the application to dynamically configure itself based on the specific needs of each pod. This dynamic configuration capability is particularly useful in diverse and changing environments.

###  3.1. <a name='Annotations:'></a>Annotations :

Each injector annotation are read by the Injector pod and permit to configure properly how Database Credetials need to be handled.