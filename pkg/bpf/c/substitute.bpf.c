// SPDX-License-Identifier: GPL-2.0 OR Apache-2.0
//
// vault-db-injector BPF substitution program.
//
// This file is dual-licensed (GPL-2.0 OR Apache-2.0) so the BPF program
// can declare a GPL-compatible license to the kernel verifier (required
// for LSM programs and BPF helpers like bpf_probe_write_user) while the
// rest of the project remains Apache-2.0.
//
// Hook: lsm/bprm_check_security
// Fires synchronously after the kernel copies argv/envp to the new task's
// stack but before exec completes. Gives access to bprm->p (top of envp).
//
// Behavior: for each cgroup that has mappings registered in
// cgroup_mappings, scan envp memory byte-by-byte. For every byte run that
// exactly matches a placeholder, overwrite it with the real value (NUL-padded
// to the same length) using bpf_probe_write_user.
//
// Scan strategy: bpf_loop() (Linux 5.17+) iterates every byte offset in the
// envp region up to ENVP_SCAN_LIMIT. This is the only correct approach because
// env values like "KEY=VALUE" have no alignment guarantee — a placeholder
// starts at offset len("KEY="), which is never aligned to a power of two.
//
// All sizes are constants matching pkg/placeholder/placeholder.go and
// pkg/bpf/loader.go.

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_tracing.h>

// Dual BSD/GPL satisfies the kernel's GPL-compatible license check for
// LSM programs while remaining permissive for downstream BSD redistribution.
// The project's overall Apache-2.0 license is unchanged; this BPF source
// file is independently dual-licensed (see SPDX header above).
char LICENSE[] SEC("license") = "Dual BSD/GPL";

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

struct scan_ctx {
    void *base;                          // bprm->p (start of envp region)
    struct mappings_for_cgroup *mfc;
    char *buf;                           // per-cpu scratch buffer
};

// bpf_loop() callback: called once per byte offset i in [0, ENVP_SCAN_LIMIT).
// Reads PLACEHOLDER_LEN bytes at base+i, compares against each registered
// placeholder, and overwrites on match.
static long scan_callback(__u32 i, void *ctx_)
{
    struct scan_ctx *ctx = (struct scan_ctx *)ctx_;
    void *addr = (char *)ctx->base + i;

    if (bpf_probe_read_user(ctx->buf, PLACEHOLDER_LEN, addr) != 0)
        return 0; // unreadable page — continue

    for (__u32 j = 0; j < ctx->mfc->count && j < MAX_MAPPINGS_PER_CGROUP; j++) {
        struct mapping *m = &ctx->mfc->entries[j];

        // Byte-for-byte comparison of all PLACEHOLDER_LEN bytes.
        bool match = true;
        #pragma unroll
        for (int k = 0; k < PLACEHOLDER_LEN; k++) {
            if (ctx->buf[k] != m->placeholder[k]) {
                match = false;
                break;
            }
        }
        if (!match)
            continue;

        // Build NUL-padded replacement of exactly PLACEHOLDER_LEN bytes.
        char padded[PLACEHOLDER_LEN] = {0};
        __u32 len = m->value_len;
        if (len > VALUE_MAX) len = VALUE_MAX;
        #pragma unroll
        for (int k = 0; k < VALUE_MAX; k++) {
            if (k < len) padded[k] = m->value[k];
        }
        bpf_probe_write_user(addr, padded, PLACEHOLDER_LEN);
        break; // two distinct placeholders cannot match at the same byte offset
    }
    return 0; // always continue scanning — multiple placeholders at different offsets
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
    struct scan_ctx sctx = {
        .base = (void *)p,
        .mfc  = mfc,
        .buf  = buf,
    };

    // Walk every byte offset from 0 to ENVP_SCAN_LIMIT - PLACEHOLDER_LEN.
    // bpf_loop() (Linux 5.17+) is verifier-safe for large iteration counts
    // and avoids the #pragma unroll stack-pressure of an inlined 16K loop.
    bpf_loop(ENVP_SCAN_LIMIT - PLACEHOLDER_LEN, scan_callback, &sctx, 0);

    return 0;
}
