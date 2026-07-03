#!/usr/bin/env bash
# scripts/backup-nas.sh
#
# Development-machine driver for the Relive NAS backup tool.
#
# Loads allowlisted values from .nas-backup.env, applies process-environment
# overrides, validates them, verifies non-interactive SSH connectivity, then
# streams scripts/backup-nas-remote.sh to `bash -s` over SSH with the five
# validated positional arguments.
#
# No database or configuration contents pass back through SSH. Only progress,
# sanitized metadata, checksums, and the final directory path are printed.
#
# Written for broad portability: avoids bash 4+ features (no associative
# arrays) so it runs on the macOS system bash 3.2 as well as modern bash.

set -euo pipefail

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="$REPO_ROOT/.nas-backup.env"
REMOTE_WORKER="$SCRIPT_DIR/backup-nas-remote.sh"

# Allowlist of accepted keys. Order is significant for stable manifest output.
ALLOWED_KEYS=(
  RELIVE_NAS_HOST
  RELIVE_NAS_ROOT
  RELIVE_NAS_DB
  RELIVE_NAS_BACKUP_DIR
  RELIVE_BACKUP_KEEP
  RELIVE_BACKUP_LABEL
)
N_KEYS=${#ALLOWED_KEYS[@]}

DEFAULT_ROOT="/volume1/docker/relive"
DEFAULT_KEEP="0"
DEFAULT_LABEL="manual"

SSH_CONNECT_TIMEOUT=15

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

die() {
  echo "backup-nas: error: $*" >&2
  exit 1
}

log() {
  echo "backup-nas: $*"
}

# Shell-quote a value for safe inclusion in a remote bash command. Uses single
# quotes with embedded single quotes escaped as '\''.
shell_quote() {
  local s=$1
  s=${s//\'/\'\'}
  printf "'%s'" "$s"
}

# Index of an allowlist key. Echoes the 0-based index or returns 1 if absent.
key_index() {
  local needle=$1 i
  for ((i = 0; i < N_KEYS; i++)); do
    if [[ "${ALLOWED_KEYS[$i]}" == "$needle" ]]; then
      echo "$i"
      return 0
    fi
  done
  return 1
}

# Validate a host string: non-empty, no whitespace/control chars, not starting
# with '-'.
validate_host() {
  local host=$1
  [[ -n "$host" ]] || die "RELIVE_NAS_HOST is empty"
  [[ "$host" != -* ]] || die "RELIVE_NAS_HOST must not begin with '-'"
  # Reject any whitespace or control characters.
  if [[ "$host" =~ [[:space:][:cntrl:]] ]]; then
    die "RELIVE_NAS_HOST contains whitespace or control characters"
  fi
}

# Validate a filesystem path: absolute, restricted charset, no '..' segment.
validate_path() {
  local name=$1
  local value=$2
  [[ "$value" == /* ]] || die "$name must be an absolute path: $value"
  [[ "$value" =~ ^[A-Za-z0-9._/-]+$ ]] || die "$name contains disallowed characters: $value"
  # Reject '..' as a full path segment.
  local old_ifs=$IFS
  IFS=/
  local seg
  for seg in $value; do
    [[ "$seg" != ".." ]] || die "$name must not contain a '..' segment: $value"
  done
  IFS=$old_ifs
}

# Validate retention: non-negative integer.
validate_keep() {
  local keep=$1
  [[ "$keep" =~ ^[0-9]+$ ]] || die "RELIVE_BACKUP_KEEP must be a non-negative integer: $keep"
}

# Validate / normalize label. Echoes the normalized label.
validate_label() {
  local label=$1
  if [[ -z "$label" ]]; then
    echo "$DEFAULT_LABEL"
    return
  fi
  [[ "$label" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,49}$ ]] \
    || die "RELIVE_BACKUP_LABEL must match [A-Za-z0-9][A-Za-z0-9._-]{0,49}: $label"
  echo "$label"
}

# ---------------------------------------------------------------------------
# Configuration loader
#
# Bash 3.2 has no associative arrays. We use two parallel plain arrays indexed
# by allowlist position: env_file_values[i] holds the file value (empty if
# absent) and env_file_seen[i] is "1" if the key was present in the file.
# ---------------------------------------------------------------------------

env_file_values=()
env_file_seen=()
init_loader_arrays() {
  local i
  for ((i = 0; i < N_KEYS; i++)); do
    env_file_values[i]=""
    env_file_seen[i]=""
  done
}

# Load allowlisted KEY=value pairs from the env file using strict parsing.
load_env_file() {
  local file=$1
  [[ -f "$file" ]] || return 0

  local lineno=0
  local line key value idx
  while IFS= read -r line || [[ -n "$line" ]]; do
    lineno=$((lineno + 1))
    # Strip trailing carriage returns.
    line=${line%$'\r'}
    # Skip blank lines and comments.
    if [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]]; then
      continue
    fi
    # No leading whitespace allowed around the directive itself.
    if [[ "$line" =~ ^[[:space:]] ]]; then
      die "$file:$lineno: indented lines are not allowed"
    fi
    # Reject `export`, `source`, command substitution, backticks.
    if [[ "$line" =~ ^(export[[:space:]]|[[:space:]]*source[[:space:]]) ]]; then
      die "$file:$lineno: shell directives (export/source) are not allowed"
    fi
    if [[ "$line" =~ \$\(|\` ]]; then
      die "$file:$lineno: command substitution/backticks are not allowed"
    fi
    # Must be KEY=value.
    if [[ ! "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]]; then
      die "$file:$lineno: malformed line (expected KEY=value): $line"
    fi
    key=${BASH_REMATCH[1]}
    value=${BASH_REMATCH[2]}
    # Detect duplicate keys.
    if idx=$(key_index "$key"); then
      if [[ -n "${env_file_seen[$idx]}" ]]; then
        die "$file:$lineno: duplicate key: $key"
      fi
    else
      die "$file:$lineno: unknown key: $key"
    fi
    env_file_values[$idx]=$value
    env_file_seen[$idx]=1
  done < "$file"
}

# Resolve a value by key index with precedence:
#   1. process environment (if the var was set when the driver started)
#   2. file value
#   3. supplied default (may be empty)
# Globals:
#   proc_seen[] — "1" if the process env supplied the key at start.
proc_seen=()
init_proc_seen() {
  local i k
  for ((i = 0; i < N_KEYS; i++)); do
    k=${ALLOWED_KEYS[$i]}
    if [[ -n "${!k+x}" ]]; then
      proc_seen[$i]=1
    else
      proc_seen[$i]=""
    fi
  done
}

# Resolve and echo the final value for a key index.
resolve() {
  local idx=$1 default=$2
  local k=${ALLOWED_KEYS[$idx]}
  if [[ -n "${proc_seen[$idx]}" ]]; then
    # Use the process-env value (may be empty).
    printf '%s' "${!k:-}"
  elif [[ -n "${env_file_seen[$idx]}" ]]; then
    printf '%s' "${env_file_values[$idx]}"
  else
    printf '%s' "$default"
  fi
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

main() {
  init_loader_arrays
  init_proc_seen

  load_env_file "$ENV_FILE"

  local host_idx root_idx db_idx backup_idx keep_idx label_idx
  host_idx=$(key_index RELIVE_NAS_HOST)
  root_idx=$(key_index RELIVE_NAS_ROOT)
  db_idx=$(key_index RELIVE_NAS_DB)
  backup_idx=$(key_index RELIVE_NAS_BACKUP_DIR)
  keep_idx=$(key_index RELIVE_BACKUP_KEEP)
  label_idx=$(key_index RELIVE_BACKUP_LABEL)

  local host root db backup_dir keep label
  host=$(resolve "$host_idx" "")
  root=$(resolve "$root_idx" "$DEFAULT_ROOT")
  # DB and backup dir defaults derive from the final root.
  db=$(resolve "$db_idx" "$root/data/backend/relive.db")
  backup_dir=$(resolve "$backup_idx" "$root/backup")
  keep=$(resolve "$keep_idx" "$DEFAULT_KEEP")
  label=$(resolve "$label_idx" "")

  # Validate host first — it has no default and must be supplied.
  if [[ -z "$host" ]]; then
    die "RELIVE_NAS_HOST is not set. Set it in .nas-backup.env or the process environment."
  fi
  validate_host "$host"
  validate_path RELIVE_NAS_ROOT "$root"
  validate_path RELIVE_NAS_DB "$db"
  validate_path RELIVE_NAS_BACKUP_DIR "$backup_dir"
  validate_keep "$keep"
  label=$(validate_label "$label")

  # Required local command.
  command -v ssh >/dev/null 2>&1 || die "local 'ssh' command not found"

  [[ -f "$REMOTE_WORKER" ]] || die "missing remote worker: $REMOTE_WORKER"

  log "host=$host"
  log "validating SSH connectivity..."

  # Non-interactive connectivity check. BatchMode=yes prevents passphrase and
  # key-agent dialogs from hanging the run.
  if ! ssh -o BatchMode=yes -o ConnectTimeout="$SSH_CONNECT_TIMEOUT" \
          -o StrictHostKeyChecking=accept-new \
          "$host" true 2>/dev/null; then
    die "SSH connectivity check failed for $host"
  fi

  log "starting remote backup worker..."

  # Stream the worker to bash -s over SSH with the five validated arguments.
  # Arguments are passed as separate argv elements to `bash -s --`, so quoting
  # is handled by ssh itself rather than by string concatenation. The remote
  # command string is built with shell_quote to remain safe under set -u.
  local remote_cmd
  remote_cmd="bash -s -- $(shell_quote "$root") $(shell_quote "$db") \
$(shell_quote "$backup_dir") $(shell_quote "$label") $(shell_quote "$keep")"

  # shellcheck disable=SC2086
  ssh -o BatchMode=yes -o ConnectTimeout="$SSH_CONNECT_TIMEOUT" \
    "$host" "$remote_cmd" < "$REMOTE_WORKER"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
