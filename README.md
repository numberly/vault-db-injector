# Vault Database Injector

The Vault DB Injector relies on the database engine from Vault to generate credentials, distribute them to Kubernetes applications and handle their lifecycle.

##  1. <a name='Feature'></a>Feature
- Generate credentials through Vault Database Engine
- Distribute credentials to workload using annotations and Kubernetes mutating webhook
- Renew credentials when necessary
- Revoke credentials when application pod is deleted
- Optionally protect credentials at the Kubernetes API layer using an NRI plugin substitution layer

##  2. <a name='Documentation'></a>Documentation

Checkout the [Vault DB Injector documentation](https://numberly.github.io/vault-db-injector) for more informations.

##  3. <a name='TalkDemo'></a>Cloud Native Days France – Talk & Demo

A production feedback session presenting **Vault DB Injector**, its design decisions, trade-offs, and lessons learned after running it in production at scale.

The talk covers:
- why static database credentials become a problem
- how ephemeral credentials are injected into Kubernetes workloads
- operational feedback from real-world usage
- a live demonstration

The demo environment is based on:
- [**OpenBao**](https://github.com/openbao/openbao) (Vault-compatible secrets management)
- [**CloudNativePG (CNPG)**](https://github.com/cloudnative-pg/cloudnative-pg) for PostgreSQL on Kubernetes

📺 **Replay:** https://youtu.be/QhOEMqbrFBk

🧪 **Demo code used during the talk:**
https://github.com/SoulKyu/vault-db-injector-cnd

##  4. <a name='Contribution'></a>Contribution

Contributions to the vault-db-injector are welcome. Please submit your pull requests or issues to the project's GitLab repository.

## 5. <a name='Tool Comparison'></a>Projects Comparison

Here you can find a comparison with many vault injector projects : [Comparaison](https://numberly.github.io/vault-db-injector/getting-started/comparison/)

## 6. <a name='OpenBao'></a>OpenBao Compatibility

The Vault DB Injector is fully compatible with OpenBao, a community-driven fork of HashiCorp Vault. Since OpenBao maintains API compatibility with Vault, you can seamlessly use this injector with your OpenBao installation without any code modifications.

All the Vault APIs used by this project work out of the box with OpenBao, including:
- Kubernetes authentication
- Database secrets engine
- Token management and renewal
- KV v2 secrets engine for metadata storage
- Lease management

To use the injector with OpenBao, simply point the `vaultAddress` configuration to your OpenBao instance and ensure your OpenBao setup includes the necessary authentication backends, database engine configuration, and policies that match your deployment requirements.

##  7. <a name='Acknowledgements'></a>Acknowledgements

Special thanks to the contributors and maintainers of the project.

---

