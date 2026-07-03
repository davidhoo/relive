#!/usr/bin/env bash
# scripts/backup-nas-remote.sh
#
# NAS-side worker for the Relive backup tool. Streamed to `bash -s` over SSH
# by scripts/backup-nas.sh with five positional arguments:
#
#   $1 NAS root        (validated absolute path)
#   $2 database path   (validated absolute path)
#   $3 backup root     (validated absolute path)
#   $4 label           (validated [A-Za-z0-9][A-Za-z0-9._-]{0,49})
#   $5 retention count (validated non-negative integer)
#
# Revalidates every argument locally, then performs the full backup flow
# described in docs/plans/2026-07-03-nas-backup-tool-design.md.
#
# Written for broad portability: avoids bash 4+ features (no associative
# arrays) so it runs under macOS bash 3.2 during local tests as well as on the
# Synology NAS.

set -euo pipefail

# Synology ships docker/git under /usr/local/bin and sqlite3 under /usr/bin.
# Append the known good system paths so callers (and tests) can inject their
# own tooling ahead of them via PATH, while still resolving the NAS binaries.
PATH="${PATH:-}:/usr/local/bin:/usr/bin:/bin"
# De-duplicate PATH entries to avoid unbounded growth when streamed repeatedly.
PATH=$(printf '%s' "$PATH" | awk -v RS=: 'NF && !seen[$0]++ {if(out)out=out":";out=out$0} END{print out}')
export PATH

umask 077

# ---------------------------------------------------------------------------
# Globals set during a run. The trap uses them to clean up only this run's
# artifacts.
# ---------------------------------------------------------------------------
RUN_LOCK_DIR=""
RUN_PARTIAL_DIR=""
RUN_FINAL_DIR=""

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

die() {
  echo "backup-nas-remote: error: $*" >&2
  exit 1
}

remote_log() {
  echo "backup-nas-remote: $*"
}

# Validate a filesystem path: absolute, restricted charset, no '..' segment.
validate_path() {
  local name=$1 value=$2
  [[ "$value" == /* ]] || die "$name must be an absolute path: $value"
  [[ "$value" =~ ^[A-Za-z0-9._/-]+$ ]] || die "$name contains disallowed characters: $value"
  local old_ifs=$IFS
  IFS=/
  local seg
  for seg in $value; do
    [[ "$seg" != ".." ]] || die "$name must not contain a '..' segment: $value"
  done
  IFS=$old_ifs
}

validate_label() {
  local label=$1
  [[ "$label" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,49}$ ]] \
    || die "label must match [A-Za-z0-9][A-Za-z0-9._-]{0,49}: $label"
}

validate_keep() {
  local keep=$1
  [[ "$keep" =~ ^[0-9]+$ ]] || die "retention must be a non-negative integer: $keep"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

# Trap: remove only this run's lock and partial directory. Never touch a
# pre-existing lock that this run did not create, and never delete a final
# (successful) directory.
cleanup() {
  local rc=$?
  if [[ -n "$RUN_PARTIAL_DIR" && -d "$RUN_PARTIAL_DIR" ]]; then
    rm -rf "$RUN_PARTIAL_DIR"
  fi
  if [[ -n "$RUN_LOCK_DIR" && -d "$RUN_LOCK_DIR" ]]; then
    rmdir "$RUN_LOCK_DIR" 2>/dev/null || true
  fi
  exit "$rc"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

main() {
  [[ $# -eq 5 ]] || die "expected 5 arguments, got $#"

  local nas_root=$1 db_path=$2 backup_root=$3 label=$4 keep=$5

  # Revalidate every argument on the NAS even though the driver already did.
  validate_path "NAS root" "$nas_root"
  validate_path "database path" "$db_path"
  validate_path "backup root" "$backup_root"
  validate_label "$label"
  validate_keep "$keep"

  # The backup root must be distinct from the live DB location to avoid the
  # worker writing into the directory it is backing up.
  case "$backup_root/" in
    "$db_path"/*|"$db_path") die "backup root must not contain the live database: $backup_root" ;;
  esac

  # Required dependencies. Validated before any backup artifact is created.
  require_cmd sqlite3
  require_cmd git
  require_cmd docker
  require_cmd tar
  require_cmd sha256sum
  require_cmd find

  # Source database and repository must exist.
  [[ -f "$db_path" ]] || die "source database not found: $db_path"
  [[ -d "$nas_root" ]] || die "NAS root not found: $nas_root"

  # Ensure the backup root exists.
  mkdir -p "$backup_root"

  # Acquire the lock with an atomic mkdir. Never auto-delete a pre-existing
  # lock — a concurrent backup fails immediately with a clear message.
  RUN_LOCK_DIR="$backup_root/.relive-backup.lock"
  if ! mkdir "$RUN_LOCK_DIR" 2>/dev/null; then
    die "another backup is already running (lock exists): $RUN_LOCK_DIR"
  fi

  # Build the timestamped names. Use a fixed format that sorts lexically the
  # same as chronologically.
  local ts
  ts=$(date '+%Y-%m-%d-%H%M%S')
  RUN_PARTIAL_DIR="$backup_root/.${ts}-${label}.partial"
  RUN_FINAL_DIR="$backup_root/${ts}-${label}"

  # Refuse to overwrite any final directory.
  if [[ -e "$RUN_FINAL_DIR" ]]; then
    die "final backup directory already exists: $RUN_FINAL_DIR"
  fi

  # Disk-space preflight. Require at least the live DB size plus a conservative
  # margin: max(20% of DB size, 256 MiB). Avoid broad filesystem scans.
  local db_size avail required margin
  db_size=$(stat -f '%z' "$db_path" 2>/dev/null || stat -c '%s' "$db_path" 2>/dev/null || echo 0)
  [[ "$db_size" =~ ^[0-9]+$ ]] || db_size=0
  margin=$((db_size / 5))
  if [[ $margin -lt 268435456 ]]; then
    margin=268435456
  fi
  required=$((db_size + margin))
  avail=$(df -kP "$backup_root" 2>/dev/null | awk 'NR==2 && $4 ~ /^[0-9]+$/ {print $4 * 1024; exit}')
  [[ -n "$avail" && "$avail" =~ ^[0-9]+$ ]] || avail=0
  if [[ $avail -lt $required ]]; then
    die "insufficient disk space: need $required bytes, have $avail bytes in $backup_root"
  fi

  # Create the 0700 partial directory.
  mkdir -p "$RUN_PARTIAL_DIR"
  chmod 700 "$RUN_PARTIAL_DIR"

  # ---- SQLite online backup ----------------------------------------------
  # Use a SQLite command file inside the protected partial directory so the
  # .backup target path is quoted by SQLite itself. Never cp the live DB,
  # WAL, or SHM files.
  local cmd_file="$RUN_PARTIAL_DIR/.backup.cmd"
  cat > "$cmd_file" <<EOF
.timeout 60000
.backup '$RUN_PARTIAL_DIR/relive.db'
EOF
  sqlite3 "$db_path" < "$cmd_file" \
    || die "SQLite .backup failed"
  rm -f "$cmd_file"

  [[ -f "$RUN_PARTIAL_DIR/relive.db" ]] || die "SQLite .backup produced no database"

  # Integrity check against the BACKUP, not the live DB. Require exactly one
  # line equal to "ok" (trimmed).
  local quick_out quick
  quick_out=$(sqlite3 -readonly "$RUN_PARTIAL_DIR/relive.db" 'PRAGMA quick_check;' 2>/dev/null || true)
  # Collapse to a single trimmed token for comparison.
  quick=$(printf '%s' "$quick_out" | tr -d '[:space:]')
  [[ "$quick" == "ok" ]] \
    || die "SQLite quick_check failed (expected 'ok', got: $quick_out)"

  # Schema export from the backup.
  sqlite3 -readonly "$RUN_PARTIAL_DIR/relive.db" '.schema' \
    > "$RUN_PARTIAL_DIR/schema.sql" 2>/dev/null \
    || die "schema export failed"
  [[ -s "$RUN_PARTIAL_DIR/schema.sql" ]] || die "schema export was empty"

  # ---- Config archive (allowlisted files only) ---------------------------
  local config_files=()
  local f rel
  for f in .env backend/config.prod.yaml docker-compose.yml docker-compose.prod.yml VERSION; do
    if [[ -f "$nas_root/$f" ]]; then
      config_files+=("$f")
    fi
  done
  # Fail if neither .env nor backend/config.prod.yaml exists: the archive
  # would not be operationally useful.
  local has_env=0 has_cfg=0
  for f in "${config_files[@]:-}"; do
    [[ "$f" == ".env" ]] && has_env=1
    [[ "$f" == "backend/config.prod.yaml" ]] && has_cfg=1
  done
  if [[ $has_env -eq 0 && $has_cfg -eq 0 ]]; then
    die "neither .env nor backend/config.prod.yaml exists; config archive would be useless"
  fi
  if [[ ${#config_files[@]} -gt 0 ]]; then
    ( cd "$nas_root" && tar czf "$RUN_PARTIAL_DIR/config.tar.gz" "${config_files[@]}" ) \
      || die "config archive creation failed"
  fi

  # ---- Git bundle + status -----------------------------------------------
  if [[ -d "$nas_root/.git" ]]; then
    git -C "$nas_root" -c safe.directory="$nas_root" bundle create \
      "$RUN_PARTIAL_DIR/repository.bundle" --all \
      || die "git bundle create failed"
    git -C "$nas_root" -c safe.directory="$nas_root" bundle verify \
      "$RUN_PARTIAL_DIR/repository.bundle" >/dev/null 2>&1 \
      || die "git bundle verify failed"

    {
      echo "## HEAD"
      git -C "$nas_root" -c safe.directory="$nas_root" rev-parse HEAD 2>/dev/null || true
      echo "## branch"
      git -C "$nas_root" -c safe.directory="$nas_root" rev-parse --abbrev-ref HEAD 2>/dev/null || true
      echo "## status (porcelain)"
      git -C "$nas_root" -c safe.directory="$nas_root" status --porcelain 2>/dev/null || true
    } > "$RUN_PARTIAL_DIR/git-status.txt"
  else
    die "not a git repository: $nas_root"
  fi

  # ---- Sanitized runtime metadata ----------------------------------------
  local head_sha version
  head_sha=$(git -C "$nas_root" -c safe.directory="$nas_root" rev-parse --short HEAD 2>/dev/null || echo unknown)
  version="(unknown)"
  [[ -f "$nas_root/VERSION" ]] && version=$(cat "$nas_root/VERSION" 2>/dev/null | tr -d '[:space:]')

  runtime_capture() {
    local name=$1
    local state image health started restarts
    state=$(docker inspect -f '{{.State.Status}}' "$name" 2>/dev/null || echo missing)
    if [[ "$state" == "missing" ]]; then
      echo "container=$name status=missing"
      return
    fi
    image=$(docker inspect -f '{{.Config.Image}}' "$name" 2>/dev/null || echo unknown)
    health=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$name" 2>/dev/null || echo none)
    started=$(docker inspect -f '{{.State.StartedAt}}' "$name" 2>/dev/null || echo unknown)
    restarts=$(docker inspect -f '{{.RestartCount}}' "$name" 2>/dev/null || echo 0)
    echo "container=$name status=$state image=$image health=$health started=$started restarts=$restarts"
  }

  {
    echo "hostname=$(hostname 2>/dev/null || echo unknown)"
    echo "backup_timestamp=$ts"
    echo "head=$head_sha"
    echo "version=$version"
    runtime_capture relive
    runtime_capture relive-ml
    echo "## compose status"
    if [[ -f "$nas_root/docker-compose.prod.yml" ]]; then
      docker compose -f "$nas_root/docker-compose.prod.yml" ps 2>/dev/null || echo "compose ps unavailable"
    elif [[ -f "$nas_root/docker-compose.yml" ]]; then
      docker compose -f "$nas_root/docker-compose.yml" ps 2>/dev/null || echo "compose ps unavailable"
    else
      echo "compose file absent"
    fi
  } > "$RUN_PARTIAL_DIR/runtime.txt"

  # ---- Manifest ----------------------------------------------------------
  {
    echo "Relive NAS backup manifest"
    echo "timestamp=$ts"
    echo "label=$label"
    echo "nas_root=$nas_root"
    echo "source_db=$db_path"
    echo "backup_root=$backup_root"
    echo "head=$head_sha"
    echo "version=$version"
    echo "quick_check=ok"
    echo "tool=backup-nas-remote.sh"
    echo "completed_at=$(date '+%Y-%m-%dT%H:%M:%S%z' 2>/dev/null || echo unknown)"
    echo "## file sizes (bytes)"
    local bf
    for bf in relive.db schema.sql config.tar.gz repository.bundle git-status.txt runtime.txt RESTORE.txt; do
      if [[ -f "$RUN_PARTIAL_DIR/$bf" ]]; then
        local sz
        sz=$(stat -f '%z' "$RUN_PARTIAL_DIR/$bf" 2>/dev/null || stat -c '%s' "$RUN_PARTIAL_DIR/$bf" 2>/dev/null || echo 0)
        echo "$bf=$sz"
      fi
    done
  } > "$RUN_PARTIAL_DIR/manifest.txt"

  # ---- RESTORE notes -----------------------------------------------------
  cat > "$RUN_PARTIAL_DIR/RESTORE.txt" <<'EOF'
Relive backup — manual recovery checklist

WARNING: Restore requires STOPPING Relive first. Do not restore into a live
running deployment; the SQLite online backup is only safe to swap in while the
backend is stopped.

Prerequisites:
  1. SSH to the NAS.
  2. Stop Relive:   docker compose -f <nas-root>/docker-compose.prod.yml down
  3. Keep a copy of the current live database before replacing it.

Verify the backup before restoring:
  sqlite3 -readonly <backup-dir>/relive.db 'PRAGMA quick_check;'
  sha256sum -c <backup-dir>/SHA256SUMS

Restore the database (example):
  cp <backup-dir>/relive.db <nas-root>/data/backend/relive.db
  rm -f <nas-root>/data/backend/relive.db-wal <nas-root>/data/backend/relive.db-shm

Restart Relive:
  docker compose -f <nas-root>/docker-compose.prod.yml up -d

This tool never executes restore, container restart, migration, or deployment.
config.tar.gz contains secrets — handle it with the same care as .env.
EOF

  # ---- Permissions -------------------------------------------------------
  find "$RUN_PARTIAL_DIR" -type d -exec chmod 700 {} +
  find "$RUN_PARTIAL_DIR" -type f -exec chmod 600 {} +

  # ---- Checksums ---------------------------------------------------------
  # Generate SHA256SUMS for every bundle file except SHA256SUMS itself, in
  # stable filename order. Verify immediately.
  ( cd "$RUN_PARTIAL_DIR" && \
    find . -maxdepth 1 -type f ! -name SHA256SUMS -exec basename {} \; \
      | sort | xargs sha256sum > SHA256SUMS )
  ( cd "$RUN_PARTIAL_DIR" && sha256sum -c SHA256SUMS ) >/dev/null 2>&1 \
    || die "checksum verification failed before publication"

  # ---- Atomic publication ------------------------------------------------
  # Tighten permissions on the final directory itself before publishing.
  chmod 700 "$RUN_PARTIAL_DIR"
  mv "$RUN_PARTIAL_DIR" "$RUN_FINAL_DIR" || die "final rename failed"
  RUN_PARTIAL_DIR=""

  # ---- Retention (opt-in, post-success only) -----------------------------
  if [[ "$keep" -gt 0 ]]; then
    apply_retention "$backup_root" "$keep" "$RUN_FINAL_DIR" || \
      remote_log "warning: retention reported an error (new backup is intact)"
  fi

  # ---- Concise success summary ------------------------------------------
  local final_db_size
  final_db_size=$(stat -f '%z' "$RUN_FINAL_DIR/relive.db" 2>/dev/null || stat -c '%s' "$RUN_FINAL_DIR/relive.db" 2>/dev/null || echo 0)
  echo "Backup complete"
  echo "Directory: $RUN_FINAL_DIR"
  echo "Database quick_check: ok"
  echo "Checksums: verified"
  remote_log "done (db_size=$final_db_size bytes)"
}

# ---------------------------------------------------------------------------
# Retention
#
# Consider only directories directly under the backup root whose names exactly
# match the tool's timestamp-label pattern. Sort lexicographically (= timestamp
# order) and remove entries beyond the newest N, never deleting the just-created
# backup. Ignore symlinks, files, .partial dirs, manually named dirs, and paths
# outside the configured root. Deletion failure reports an error but leaves the
# new backup valid.
# ---------------------------------------------------------------------------
apply_retention() {
  local backup_root=$1 keep=$2 just_created=$3
  local pattern='^[0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6}-[A-Za-z0-9][A-Za-z0-9._-]{0,49}$'

  # Non-recursive listing of matching directory basenames.
  local matches=()
  local entry name
  while IFS= read -r entry; do
    [[ -n "$entry" ]] || continue
    name=$(basename "$entry")
    [[ "$name" =~ $pattern ]] || continue
    # Skip symlinks and non-directories.
    [[ -d "$entry" && ! -L "$entry" ]] || continue
    matches+=("$name")
  done < <(find "$backup_root" -maxdepth 1 -mindepth 1 2>/dev/null || true)

  # Sort lexicographically (matches timestamp order). Keep newest N.
  local sorted=()
  if [[ ${#matches[@]} -gt 0 ]]; then
    while IFS= read -r line; do
      sorted+=("$line")
    done < <(printf '%s\n' "${matches[@]}" | LC_ALL=C sort)
  fi

  local total=${#sorted[@]}
  [[ $total -le $keep ]] && return 0

  local i to_remove=$((total - keep))
  for ((i = 0; i < to_remove; i++)); do
    local victim_name="${sorted[$i]}"
    local victim_path="$backup_root/$victim_name"
    # Never delete the just-created backup.
    [[ "$victim_path" == "$just_created" ]] && continue
    # Recheck parent and type before removing.
    if [[ -d "$victim_path" && ! -L "$victim_path" ]]; then
      rm -rf "$victim_path" || {
        remote_log "warning: failed to prune old backup: $victim_path"
      }
    fi
  done
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
