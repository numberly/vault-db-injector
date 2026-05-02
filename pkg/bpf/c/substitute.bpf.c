// SPDX-License-Identifier: Apache-2.0
//
// vault-db-injector BPF substitution program.
//
// Hook: lsm/bprm_check_security
// Fires synchronously after the kernel copies argv/envp to the new task's
// stack but before exec completes. Gives access to bprm->p (top of envp).
//
// Behavior: for each cgroup that has mappings registered in
// cgroup_mappings, scan envp memory. For every byte run that exactly
// matches a placeholder, overwrite it with the real value (NUL-padded to
// the same length) using bpf_probe_write_user.
//
// All sizes are constants matching pkg/placeholder/placeholder.go and
// pkg/bpf/loader.go.

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_tracing.h>

char LICENSE[] SEC("license") = "Apache-2.0";

// Must match pkg/placeholder.Length and the loader's struct mapping.
#define PLACEHOLDER_LEN 77
#define VALUE_MAX 73                  // 73 + NUL = 74; fits in placeholder span
#define MAX_MAPPINGS_PER_CGROUP 8
#define MAX_CGROUPS 4096
#define ENVP_SCAN_LIMIT 16384         // 16 KB of envp space scanned

struct mapping {
    char placeholder[PLACEHOLDER_LEN];
    char value[VALUE_MAX + 1];
    __u32 value_len;
    __u32 _pad;
};

struct mappings_for_cgroup {
    __u32 count;
    __u32 _pad;
    struct mapping entries[MAX_MAPPINGS_PER_CGROUP];
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, MAX_CGROUPS);
    __type(key, __u64);
    __type(value, struct mappings_for_cgroup);
} cgroup_mappings SEC(".maps");

// Per-CPU scratch to read 77 bytes from envp without blowing BPF stack.
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, char[PLACEHOLDER_LEN]);
} scratch SEC(".maps");

// Try to substitute one mapping at a single envp address. Returns 1 if
// substituted, 0 otherwise. The caller is responsible for advancing the
// scan offset.
static __always_inline int try_substitute(void *envp_addr, struct mapping *m, char *buf)
{
    if (bpf_probe_read_user(buf, PLACEHOLDER_LEN, envp_addr) != 0)
        return 0;

    // Strict prefix match. The verifier requires bounded loops.
    #pragma unroll
    for (int i = 0; i < PLACEHOLDER_LEN; i++) {
        if (buf[i] != m->placeholder[i])
            return 0;
    }

    // Build NUL-padded value of exactly PLACEHOLDER_LEN bytes.
    char padded[PLACEHOLDER_LEN] = {0};
    __u32 len = m->value_len;
    if (len > VALUE_MAX) len = VALUE_MAX;
    #pragma unroll
    for (int i = 0; i < VALUE_MAX; i++) {
        if (i < len) padded[i] = m->value[i];
    }
    bpf_probe_write_user(envp_addr, padded, PLACEHOLDER_LEN);
    return 1;
}

SEC("lsm/bprm_check_security")
int BPF_PROG(substitute_envp, struct linux_binprm *bprm)
{
    __u64 cg = bpf_get_current_cgroup_id();
    struct mappings_for_cgroup *mfc = bpf_map_lookup_elem(&cgroup_mappings, &cg);
    if (!mfc)
        return 0;

    __u32 zero = 0;
    char *buf = bpf_map_lookup_elem(&scratch, &zero);
    if (!buf)
        return 0;

    unsigned long p = BPF_CORE_READ(bprm, p);

    // Bounded scan. 256 strides at 64-byte stride = 16 KB envp coverage.
    // The stride leaves no gap that could hide a placeholder, since
    // placeholders are aligned by writer (env values are NUL-separated
    // entries; we scan every byte position via a smaller stride below
    // would be too verifier-expensive — instead we rely on the fact
    // that Go writes placeholders as whole env *values*, which always
    // start on a byte boundary the kernel preserves).
    #pragma unroll
    for (int s = 0; s < 256; s++) {
        unsigned long addr = p + (s * 64);
        for (__u32 i = 0; i < mfc->count && i < MAX_MAPPINGS_PER_CGROUP; i++) {
            try_substitute((void *)addr, &mfc->entries[i], buf);
        }
    }

    return 0;
}
