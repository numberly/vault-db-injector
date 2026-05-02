// SPDX-License-Identifier: GPL-2.0 OR Apache-2.0
//
// vault-db-injector BPF substitution program (tracepoint variant).
//
// This file is dual-licensed (GPL-2.0 OR Apache-2.0) so the BPF program
// can declare a GPL-compatible license to the kernel verifier while the
// rest of the project remains Apache-2.0.
//
// Hook: tracepoint/syscalls/sys_enter_execve
// Why tracepoint and not LSM: bpf_probe_write_user is only allowed in
// KPROBE/TRACEPOINT program types. LSM programs cannot call it (verifier
// rejects with "unknown func bpf_probe_write_user"). The tracepoint fires
// before the kernel copies envp from user memory to the new process's
// stack, so writes here propagate to the post-execve process.
//
// (Discovered 2026-05-02 during runtime validation in K3D — verifier
// rejected the original lsm/bprm_check_security version.)
//
// Scan strategy:
//   Walk envp[] pointer-by-pointer. For each env string, use bpf_loop() to
//   iterate byte offsets 0..ENVP_SCAN_LIMIT. At each offset, read a
//   PLACEHOLDER_LEN window directly from user memory into a small stack buffer
//   (bpf_probe_read_user: user pointer + variable offset is accepted by the
//   verifier; map_value + variable offset is not, so we do NOT copy the whole
//   string into a map first). Compare the stack window byte-for-byte against
//   each registered placeholder and write back on match.
//
// All sizes are constants matching pkg/placeholder/placeholder.go and
// pkg/bpf/loader.go.

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_core_read.h>
#include <bpf/bpf_tracing.h>

// Dual BSD/GPL satisfies the kernel's GPL-compatible license check while
// remaining permissive for downstream redistribution. The project's overall
// Apache-2.0 license is unchanged; this BPF source file is independently
// dual-licensed (see SPDX header above).
char LICENSE[] SEC("license") = "Dual BSD/GPL";

// Must match pkg/placeholder.Length and the loader's struct mapping.
#define PLACEHOLDER_LEN 77
#define VALUE_MAX 73                  // 73 + NUL = 74; fits in placeholder span
#define MAX_MAPPINGS_PER_CGROUP 8
#define MAX_CGROUPS 4096
#define MAX_ENVP_ENTRIES 256          // max envp[] entries we walk
// Per env-string: scan at most ENVP_SCAN_LIMIT byte offsets.
// This bounds the bpf_loop() inner call count.
#define ENVP_SCAN_LIMIT 256

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

// Context passed to the inner per-byte-offset callback.
// user_str is a plain user-space pointer — the verifier allows
// user_pointer + variable_offset (via bpf_probe_read_user), unlike
// map_value + variable_offset which is rejected on kernels ≤ 6.8.
struct offset_scan_ctx {
    void *user_str;                    // user pointer to the env string
    struct mappings_for_cgroup *mfc;
};

// Context passed to the outer per-envp-entry callback.
struct envp_scan_ctx {
    void *envp_user;                   // user pointer to envp[] array
    struct mappings_for_cgroup *mfc;
};

// bpf_loop() callback: called once per byte offset i in [0, ENVP_SCAN_LIMIT).
// Reads PLACEHOLDER_LEN bytes at user_str+i from user space into a stack buf,
// then compares against each registered placeholder.
//
// Stack note: we reuse 'buf' for both the comparison window and the padded
// replacement so that only one PLACEHOLDER_LEN-byte array is live at a time.
// This keeps the stack frame well below 512 bytes.
static long scan_offset(__u32 i, void *ctx_)
{
    struct offset_scan_ctx *ctx = (struct offset_scan_ctx *)ctx_;

    // Read a PLACEHOLDER_LEN window from user memory at offset i.
    // user_pointer + runtime_variable_offset is accepted by the verifier;
    // map_value + variable_offset is not (rejected on kernels ≤ 6.8).
    char buf[PLACEHOLDER_LEN];
    if (bpf_probe_read_user(buf, sizeof(buf),
                            (const char *)ctx->user_str + i) != 0)
        return 0; // unreadable — continue

    __u32 count = ctx->mfc->count;
    if (count > MAX_MAPPINGS_PER_CGROUP)
        count = MAX_MAPPINGS_PER_CGROUP;

    for (__u32 m = 0; m < count; m++) {
        struct mapping *map = &ctx->mfc->entries[m];

        // Compare buf against the placeholder using __builtin_memcmp.
        // Unlike #pragma unroll, this does not spill per-byte comparison state
        // to the BPF stack, keeping the frame under 512 bytes.
        if (__builtin_memcmp(buf, map->placeholder, PLACEHOLDER_LEN) != 0)
            continue;

        // Match found. Reuse buf (stack, 77 bytes) as the NUL-padded replacement:
        // clear it, then copy vlen value bytes into [0..vlen). No second large
        // local needed — buf is fully owned here since comparison is done.
        __u32 vlen = map->value_len;
        if (vlen > VALUE_MAX) vlen = VALUE_MAX;
        __builtin_memset(buf, 0, PLACEHOLDER_LEN);
        if (vlen > 0)
            bpf_probe_read_kernel(buf, vlen, map->value);

        bpf_probe_write_user((char *)ctx->user_str + i, buf, PLACEHOLDER_LEN);
        break; // only one placeholder can match at this byte offset
    }
    return 0; // always continue — other placeholders may appear at later offsets
}

// bpf_loop() callback: called once per envp index i in [0, MAX_ENVP_ENTRIES).
// Reads envp[i] (a user pointer to an env string) and runs the per-offset scan.
static long scan_envp_entry(__u32 i, void *ctx_)
{
    struct envp_scan_ctx *ctx = (struct envp_scan_ctx *)ctx_;

    // Read envp[i]: user pointer to a NUL-terminated env string.
    void *env_str_user = NULL;
    if (bpf_probe_read_user(&env_str_user, sizeof(env_str_user),
                            (char *)ctx->envp_user + i * sizeof(void *)) != 0)
        return 1; // failed read — stop walking
    if (env_str_user == NULL)
        return 1; // NULL sentinel — end of envp

    struct offset_scan_ctx osctx = {
        .user_str = env_str_user,
        .mfc      = ctx->mfc,
    };
    bpf_loop(ENVP_SCAN_LIMIT, scan_offset, &osctx, 0);
    return 0; // continue to next envp entry
}

SEC("tracepoint/syscalls/sys_enter_execve")
int sys_enter_execve(struct trace_event_raw_sys_enter *ctx)
{
    __u64 cg = bpf_get_current_cgroup_id();
    struct mappings_for_cgroup *mfc = bpf_map_lookup_elem(&cgroup_mappings, &cg);
    if (!mfc)
        return 0;

    // tracepoint/syscalls/sys_enter_execve: args[0]=filename, args[1]=argv, args[2]=envp
    void *envp_user = (void *)ctx->args[2];
    if (envp_user == NULL)
        return 0;

    struct envp_scan_ctx sctx = {
        .envp_user = envp_user,
        .mfc       = mfc,
    };
    bpf_loop(MAX_ENVP_ENTRIES, scan_envp_entry, &sctx, 0);

    return 0;
}
