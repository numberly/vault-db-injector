# BPF Mode Requirements

Before enabling `bpf.enabled: true` in the Helm chart, verify that every
node in your cluster meets the requirements below.

---

## Kernel requirements

BPF mode requires **Linux ≥ 5.17**. The envp scan uses `bpf_loop()` (added in
5.17) to walk every byte of envp without verifier complexity limits — earlier
kernels are not supported. Three kernel configuration options must be present:

| Kernel option | Purpose |
|---------------|---------|
| `CONFIG_BPF_LSM=y` | Enables the BPF Linux Security Module framework that the substitution hook attaches to. |
| `lsm=...,bpf` (kernel cmdline) | Activates BPF LSM at boot; the option must appear in the kernel command line, not just be compiled in. |
| `CONFIG_DEBUG_INFO_BTF=y` | Provides BTF (BPF Type Format) metadata required for CO-RE (Compile Once – Run Everywhere) portability. |

### How to verify

```bash
# Must contain "bpf"
cat /sys/kernel/security/lsm

# Example output on a ready node:
# lockdown,capability,landlock,yama,apparmor,bpf

# Verify BTF is available
ls /sys/kernel/btf/vmlinux
```

If `bpf` is absent from `/sys/kernel/security/lsm`, the DaemonSet will
exit at startup with an explicit error — it does not silently fall back to
classic mode.

---

## Container runtime

BPF mode relies on cgroup-v2 path naming to resolve a pod's cgroup ID:

- **containerd** or **cri-o** (both supported)
- **cgroup-v2** with the **systemd** cgroup driver

Verify cgroup-v2:

```bash
stat -f --format="%T" /sys/fs/cgroup
# Must return "cgroup2fs"
```

Docker's cgroupfs driver with cgroup-v1 is not supported.

---

## Capabilities

The BPF DaemonSet requires three Linux capabilities on its container. Full
`CAP_SYS_ADMIN` is **not** required.

| Capability | Why needed |
|-----------|------------|
| `CAP_BPF` | Load and manage BPF programs and maps. |
| `CAP_PERFMON` | Read BPF perf ring buffer (substitution counter metrics). |
| `CAP_SYS_RESOURCE` | Raise `RLIMIT_MEMLOCK` for BPF map memory. |

The Helm chart sets these automatically when `bpf.enabled: true`.

---

## Vault policies

BPF mode requires two additional Vault policy entries beyond the existing
webhook configuration.

**Webhook service account** (in addition to existing `database/creds/<role>`):

```hcl
path "sys/wrapping/wrap" {
  capabilities = ["update"]
}
```

**DaemonSet service account** (only this):

```hcl
path "sys/wrapping/unwrap" {
  capabilities = ["update"]
}
```

The DaemonSet has no KV read or write access. It can only consume wrap
tokens that the webhook has already issued.

---

## Tested distributions

The following distributions have been validated in CI or manual testing:

### Confirmed working

| Distribution | Notes |
|-------------|-------|
| **Bottlerocket** (EKS) | BPF LSM enabled by default. Recommended for EKS clusters. |
| **Talos Linux** | BPF LSM enabled by default. Recommended for self-managed clusters. |
| **Ubuntu 22.04+** | BPF LSM compiled in; requires cmdline adjustment (see below). |

### Documented compatible (community-validated)

| Distribution | Notes |
|-------------|-------|
| GKE Container-Optimized OS (COS) | BPF LSM available on COS-101+. Verify with the kernel check above. |
| AKS Ubuntu | BPF LSM available on Ubuntu 22.04 node pools. |
| Flatcar Container Linux | BPF LSM supported since channel stable-3760+. |

### Documented incompatible

| Environment | Reason |
|------------|--------|
| **kind** | Docker-based; the nested kernel does not have `lsm=...,bpf` in its cmdline. Integration tests requiring a real kernel cannot run in kind. |
| **minikube** | Same as kind for the VM-less (Docker/Podman) drivers. The VM drivers (KVM, VirtualBox) may work with manual kernel reconfiguration but are not tested or supported. |
| Older managed offerings (pre-2022 EKS AMIs, GKE <1.26 with older OS) | Kernel too old or BPF LSM not compiled in. Check your node kernel version. |

---

## Enabling BPF LSM on Ubuntu 22.04+

Ubuntu 22.04 ships with `CONFIG_BPF_LSM=y` compiled in, but the LSM is not
activated in the default kernel cmdline.

```bash
# 1. Edit the GRUB configuration
sudo nano /etc/default/grub

# 2. Append to GRUB_CMDLINE_LINUX (add "bpf" to the lsm= list, or add the
#    full lsm= option if it doesn't exist):
#
#    GRUB_CMDLINE_LINUX="... lsm=lockdown,capability,landlock,yama,apparmor,bpf"

# 3. Apply and reboot
sudo update-grub
sudo reboot

# 4. Verify after reboot
cat /sys/kernel/security/lsm
# Must contain "bpf"
```

For cloud instances, if GRUB is not used (e.g., direct kernel boot), set
the boot parameter via your cloud provider's instance configuration or the
VM firmware settings.

---

## Credential length constraint

The fixed-length placeholder (`__VDBI_PH_<32-hex>___`, 74 bytes) limits
real credential values to a maximum of **73 bytes** (74 bytes minus the
trailing NUL). The webhook validates this at admission time and rejects
pods if the generated value exceeds the limit.

To stay within the constraint, configure the Vault Database Engine role
with a `password_policy` that bounds password length:

```hcl
password_policy "vault-db-injector-policy" {
  length = 64
  # ... character rules
}
```

Most Vault-generated usernames and passwords are well within this range
by default.
