# Injector

**Key File:** `pkg/injector/injector.go`

##  1. <a name='HowItWorks:'></a>How It Works:

The injector is responsible for injecting database credentials into Kubernetes Pods using a Mutating Admission Webhook.

![Diagram](images/vault-injector-schema.png)

- **Webhook Initialization:**
  - The injector sets up a webhook server that listens for Pod creation requests.
  - When a Pod is created, the webhook intercepts the request and modifies the Pod specification to include environment variables with the credentials.

- **Credential Injection:**
  - The injector retrieves credentials from Vault using the configured secrets path.
  - It then injects these credentials into the Pod’s environment variables.

##  2. <a name='Benefits:'></a>Benefits:

- **Automatic Management:**
  - By automating the injection of credentials, the injector ensures that Pods have the necessary credentials without storing them statically, enhancing security.

- **Transparent Operation:**
  - The Mutating Admission Webhook operates transparently, modifying Pod specifications on-the-fly without manual intervention.

- **Security:**
  - Dynamic injection of credentials reduces the risk of credential leakage and ensures they are always fresh.
