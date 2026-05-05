# Setup: Kubernetes cluster

**Audience:** Platform operator

## Required cluster capabilities

### Mutating Admission Webhooks

Mutating admission webhooks are enabled by default in all modern Kubernetes distributions (kubeadm, EKS, GKE, AKS, k3s, and others). No action is required unless your cluster was started with `--disable-admission-plugins=MutatingAdmissionWebhook`.

Verify:

```bash
kubectl api-versions | grep admissionregistration
```

Expected output includes `admissionregistration.k8s.io/v1`.

### NRI in containerd

Open `/etc/containerd/config.toml` on each node and confirm the NRI section exists and is not disabled:

```toml
[plugins."io.containerd.nri.v1.nri"]
  disable = false
  disable_connections = false
  plugin_registration_timeout = "5s"
  plugin_request_timeout = "2m"
  socket_path = "/var/run/nri/nri.sock"
  state_dir = "/var/run/nri"
```

If the section is absent, add it and restart containerd:

```bash
sudo systemctl restart containerd
```

For CRI-O, NRI support is built in from version 1.26 and requires no configuration change.

### Pod Security Admission for the injector namespace

The NRI DaemonSet needs to mount the containerd NRI socket from the host. The `restricted` PSA level blocks `hostPath` mounts, so the injector namespace must use `privileged`.

## Verify NRI

Run these checks before proceeding.

Confirm nodes are reachable:
```bash
kubectl get nodes -o wide
```

Check the NRI socket exists on each node (run on the node directly, or via a debug pod):
```bash
ls /var/run/nri/nri.sock
```

If the path is missing, containerd NRI is disabled or the runtime version is too old.

If you have `nerdctl` on the node:
```bash
nerdctl --namespace=k8s.io system info | grep -i nri
```

Expected output includes a line referencing the NRI plugin registration.

## Create the injector namespace

```bash
kubectl create namespace vault-db-injector
kubectl label namespace vault-db-injector \
    pod-security.kubernetes.io/enforce=privileged \
    pod-security.kubernetes.io/enforce-version=latest
```

!!! warning "Why privileged?"
    The NRI DaemonSet runs as root and mounts `/var/run/nri/nri.sock` from the host. This is the containerd socket for the NRI protocol — without it, the plugin cannot intercept container creation. User workload namespaces remain at `restricted` or `baseline`; only the injector's own namespace needs `privileged`.

## Next

[Setup: Vault](setup-vault.md)
