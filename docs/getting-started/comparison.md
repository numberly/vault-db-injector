# Comparison

A vault injector retrieves credentials from a vault database engine and injects them into pods' environment variables. This document compares different existing tools with the new vault injector being developed.

## Summary

##  1. <a name='WhyVault-Db-Injector'></a>Why Vault-Db-Injector?

Before comparing Vault-Db-Injector with existing tools, we would like to share that we initially investigated various vault injector solutions designed to fetch credentials from Vault.

After extensive research, we found no tools that matched our needs, and most of them were difficult to contribute to.

Vault-Db-Injector is not a replacement for any existing vault injector but a tool more focused on security and the database engine.

We didn't intend to reinvent the wheel but designed a tool that perfectly matches our needs and shared it with those who might be interested.

##  2. <a name='ToolsComparison'></a>Tools Comparison

Here are the major tools that we compare our injector to:

- [Vault Agent Injector](https://developer.hashicorp.com/vault/docs/platform/k8s/injector)
- [Bank Vault](https://github.com/bank-vaults/bank-vaults)
- [Vals Operator](https://github.com/digitalis-io/vals-operator)
- [Vault CSI Provider](https://developer.hashicorp.com/vault/docs/platform/k8s/csi)

##  3. <a name='Ourneeds'></a>Our needs

Here are our needs by importance in our research : 

- Handle database engine
- Injection through environment variables
- Easy to use for developpers
- Audit logging
- Lease can be automatically renewed and revoked
- State is available for debugging purpose and manual revocation also
- Working with a single deployment


##  4. <a name='ComparisonTable'></a>Comparison Table

| Feature                              | Vault-Db-Injector     | Vault Agent Injector                | Bank Vault (webhook)               | Vals Operator                   | Vault CSI Provider              |
|--------------------------------------|-----------------------|-------------------------------------|------------------------------------|---------------------------------|---------------------------------|
| **Credential Source**                | Vault Database Engine | Multiple Engines                    | Secret Engine                      | Multiple Engine                 | K/V                             |
| **Engine**                           | Database              | All                                 | K/V                                | Database and K/V                | K/V                             |
| **Injection Method**                 | Pod Environment Vars  | Sidecar Container / Init Container  | Init Container (in-memory)         | Kubernetes Secrets              | CSI Volume                      |
| **Dynamic Secret Rotation**          | 🚫 Not needed         | ✅ Yes                              | ✅ Yes                             | ❌ No                           | ✅ Yes                           |
| **Access Control**                   | Role-Based Policies   | Role-Based Policies                 | Role-Based Policies                | Role-Based Policies             | Role-Based Policies             |
| **Configuration Complexity**         | 🟢 Low                | 🔴 Very High                        | 🟢 Low                             | 🟠 Moderate                     | 🟠 Moderate                     |
| **User Complexity**                  | 🟢 Low                | 🔴 Very High                        | 🟢 Low                             | 🟠 Moderate                     | 🟢 Low                          |
| **Operation Mode**                   | Deployment            | Deployment                          | Deployment                         | Operator                        | Operator                        |
| **Configuration Mode**               | Annotations           | Annotations                         | Through Env                        | CRDS                            | CRDS                            |
| **Handle Environment**               | ✅ Yes                | ❌ No                               | ✅ Yes                             | ✅ Yes                          | ✅ Yes (secretRef)              |
| **Secret Encryption**                | ✅ Yes                | ✅ Yes                              | ✅ Yes                             | ✅ Yes                          | ✅ Yes                           |
| **Audit Logging**                    | ✅ Yes                | ✅ Yes                              | ✅ Yes                             | ✅ Yes                          | ✅ Yes                           |
| **Accessible state**                 | ✅ Yes                | ❌ No                               | ❌ No                              | ❌ No                           | ❌ No                            |
| **Lease Renew**                      | ✅ Yes                | ✅ Yes                              | -                                  | 🤔 With restarting              | -                                |
| **Lease Revocation**                 | ✅ Yes                | ❌ No                               | -                                  | ❌ No                           | -                                |
| **Community Support**                | 🌱 Growing            | 🟢 Established                      | 🟠 Moderate                        | 🟠 Moderate                     | 🟢 Established                   |
| **Credentials invisible at K8s API layer (PodSpec / etcd / audit logs / GitOps)** | ✅ Yes (with BPF mode) | ❌ No                          | ❌ No                              | ❌ No                           | ❌ No                            |

###  4.1. <a name='Key'></a>Key

- ✅ Yes
- ❌ No
- 🤔 Consideration (Intermediate)
- 🚫 Not Needed
- 🟢 Low
- 🟠 Moderate
- 🔴 High

##  5. <a name='Conclusion'></a>Conclusion

This comparison highlights the unique features and capabilities of the new vault injector. While similar in many ways to existing solutions, the new tool offers dynamic secret rotation without requiring pod restarts, moderate configuration complexity, and robust access control, making it a compelling choice for managing secrets in Kubernetes environments.
