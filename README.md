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

## 3.5. <a name='SecurityNRI'></a>Security: NRI mode hardening

NRI mode requires the plugin DaemonSet to mount `/var/run/nri/nri.sock` —
the same socket containerd uses for plugin registration. Any pod that
mounts this hostPath can register as an NRI plugin and mutate every
container created on the node (env, mounts, capabilities, args).

This is **inherent to NRI**, not specific to this project. The cluster
admin must restrict who can mount these paths.

**Required mitigations** (in order of strength):

1. **PodSecurityAdmission `restricted` or `baseline`** on user namespaces:
   both forbid hostPath volumes. The plugin DS must run in a namespace
   labeled `pod-security.kubernetes.io/enforce=privileged`.
2. **Kyverno ClusterPolicy** that blocks `/var/run/nri` and `/opt/nri`
   hostPath mounts outside the trusted namespace. A reference policy is
   provided at [helm/policies/kyverno-restrict-nri-socket.yaml](helm/policies/kyverno-restrict-nri-socket.yaml).
3. **SELinux/AppArmor**: on RHEL/CoreOS, leave SELinux enforcing;
   do not run the plugin pod with `seLinuxOptions.type: spc_t`. The
   default `container_runtime_t` socket label prevents user pods from
   connecting even if they bypass the hostPath check.

See [docs/how-it-works/nri-mode.md](docs/how-it-works/nri-mode.md) for
the complete threat model.

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

