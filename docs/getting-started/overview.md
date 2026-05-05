# Overview

**Audience:** Anyone

vault-db-injector issues short-lived database credentials from HashiCorp Vault (or OpenBao) and delivers them to Kubernetes pods at runtime. It manages the full credential lifecycle: issuance at pod start, periodic renewal while the pod runs, and revocation when the pod dies. Applications read credentials from environment variables; they never call Vault directly.

This guide follows the **NRI + Projected-SA** path, the recommended approach for new deployments as of v3.0. In this mode, credentials never appear in the PodSpec, etcd, or audit logs. The webhook places opaque placeholders in the pod's environment; a node-local NRI plugin substitutes the real values at container creation, before the process starts. Vault authenticates each pod by its own ServiceAccount, so the audit log shows which pod's identity acquired which credentials — not a shared injector identity.

## What you will achieve

- Vault configured with the required mounts, policies, and roles for NRI + Projected-SA
- Kubernetes cluster verified for NRI support; injector namespace created
- Database server prepared with a Vault admin account and owner role
- vault-db-injector installed via Helm with NRI and Projected-SA enabled
- A working injected pod whose application reads real database credentials from env vars

## Estimated time

60–90 minutes for someone with the prerequisites already running (a reachable Vault instance, a live cluster, and a database server).

## Next

[Prerequisites](prerequisites.md)
