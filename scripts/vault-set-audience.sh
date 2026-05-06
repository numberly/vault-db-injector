#!/usr/bin/env bash
# Set (or clear) the `audience` field on every Vault role under a given
# auth/kubernetes mount. Used to migrate to projected-SA mode where each
# JWT must be cryptographically bound to a specific audience.
#
# IMPORTANT: vault write on auth/kubernetes/role/<name> is CREATE-or-REPLACE,
# NOT a partial update. This script reads each role's current config first
# and re-writes it in full with the audience changed, preserving all other
# fields (bound_service_account_names/_namespaces, policies, ttl, period,
# alias_name_source, token_type, etc.).
#
# Required env:
#   VAULT_ADDR   — e.g., https://vault.example.com:8200
#   VAULT_TOKEN  — token with capabilities: list+read+update on
#                  auth/<MOUNT>/role/*
#
# Args:
#   $1 — auth/kubernetes mount path (e.g., "kubernetes")
#   $2 — audience value to set (e.g., "vault"). Pass "" to clear.
#   $3 — (optional) "--dry-run" to preview without writing
#
# Idempotent. Safe to re-run.
#
# Examples:
#   ./vault-set-audience.sh kubernetes vault
#   ./vault-set-audience.sh kubernetes vault --dry-run
#   ./vault-set-audience.sh kubernetes ""             # clear all audiences

set -euo pipefail

MOUNT="${1:-}"
AUDIENCE="${2-}"
DRY_RUN="${3:-}"

if [ -z "$MOUNT" ]; then
  cat >&2 <<EOF
Usage: $(basename "$0") <mount> <audience> [--dry-run]

  mount     auth/kubernetes mount name (e.g., "kubernetes")
  audience  value to set on every role (e.g., "vault"); "" to clear
  --dry-run optional: preview changes without applying

Required env: VAULT_ADDR, VAULT_TOKEN
EOF
  exit 1
fi

if [ -z "${VAULT_ADDR:-}" ] || [ -z "${VAULT_TOKEN:-}" ]; then
  echo "ERROR: VAULT_ADDR and VAULT_TOKEN must be set" >&2
  exit 2
fi

for cmd in vault jq; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "ERROR: '$cmd' not found in PATH" >&2
    exit 2
  fi
done

is_dry_run=0
if [ "$DRY_RUN" = "--dry-run" ]; then
  is_dry_run=1
  echo "[DRY-RUN] no changes will be applied"
fi

# Fields the auth/kubernetes role API accepts. We re-pass every populated
# one so vault write doesn't reset them. List taken from:
#   https://developer.hashicorp.com/vault/api-docs/auth/kubernetes#create-role
PRESERVED_FIELDS=(
  bound_service_account_names
  bound_service_account_namespaces
  bound_service_account_namespace_selector
  alias_name_source
  token_ttl
  token_max_ttl
  token_policies
  token_bound_cidrs
  token_explicit_max_ttl
  token_no_default_policy
  token_num_uses
  token_period
  token_type
)

# Build a `vault write k=v` argument list from the role's current data,
# overriding the `audience` field. Returns the args on stdout, one per line.
build_args() {
  local data_json="$1"
  local new_audience="$2"

  # First the new audience (always set, possibly empty to clear).
  printf 'audience=%s\n' "$new_audience"

  # Then every preserved field that is currently populated.
  local field val
  for field in "${PRESERVED_FIELDS[@]}"; do
    val=$(echo "$data_json" | jq -r --arg f "$field" '
      .data[$f] |
      if   . == null      then empty
      elif type == "array" then map(tostring) | join(",")
      else tostring
      end
    ')
    if [ -n "$val" ]; then
      printf '%s=%s\n' "$field" "$val"
    fi
  done
}

echo "Listing roles under auth/${MOUNT}/role/ ..."
roles=$(vault list -format=json "auth/${MOUNT}/role" 2>/dev/null | jq -r '.[]')

if [ -z "$roles" ]; then
  echo "No roles found at auth/${MOUNT}/role/. Aborting."
  exit 0
fi

count_total=0
count_updated=0
count_skipped=0
count_failed=0

while IFS= read -r role; do
  [ -z "$role" ] && continue
  count_total=$((count_total + 1))

  current=$(vault read -format=json "auth/${MOUNT}/role/${role}" 2>/dev/null || echo "")
  if [ -z "$current" ]; then
    echo "  ${role}: FAILED to read"
    count_failed=$((count_failed + 1))
    continue
  fi

  current_audience=$(echo "$current" | jq -r '.data.audience // ""')

  if [ "$current_audience" = "$AUDIENCE" ]; then
    echo "  ${role}: already has audience='${AUDIENCE}', skipping"
    count_skipped=$((count_skipped + 1))
    continue
  fi

  if [ "$is_dry_run" -eq 1 ]; then
    echo "  ${role}: would change audience '${current_audience}' → '${AUDIENCE}'"
    count_updated=$((count_updated + 1))
    continue
  fi

  # Re-write the role in full with the new audience. Preserves everything
  # else by passing each currently-populated field back to Vault.
  mapfile -t args < <(build_args "$current" "$AUDIENCE")

  if vault write "auth/${MOUNT}/role/${role}" "${args[@]}" >/dev/null 2>&1; then
    echo "  ${role}: audience '${current_audience}' → '${AUDIENCE}' OK"
    count_updated=$((count_updated + 1))
  else
    echo "  ${role}: FAILED to update" >&2
    count_failed=$((count_failed + 1))
  fi
done <<< "$roles"

echo
echo "Summary: ${count_total} total, ${count_updated} updated, ${count_skipped} unchanged, ${count_failed} failed"

if [ "$count_failed" -gt 0 ]; then
  exit 3
fi
