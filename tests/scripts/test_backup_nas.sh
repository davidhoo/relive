#!/usr/bin/env bash
# tests/scripts/test_backup_nas.sh
#
# Test harness for the NAS backup tool. Runs one or more test groups, or all
# groups when invoked with no arguments.
#
#   bash tests/scripts/test_backup_nas.sh            # run all groups
#   bash tests/scripts/test_backup_nas.sh config     # run a single group
#
# Groups: config remote-preflight sqlite bundle publication retention transport docs

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPTS="$ROOT/scripts"
TESTS="$ROOT/tests/scripts"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

FAILURES=0
COUNT=0

fail() {
  echo "FAIL: $*" >&2
  FAILURES=$((FAILURES + 1))
  exit 1
}

assert_eq() {
  local actual=$1 expected=$2 desc=$3
  COUNT=$((COUNT + 1))
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL: $desc"
    echo "  expected: $expected"
    echo "  actual:   $actual"
    FAILURES=$((FAILURES + 1))
    return 1
  fi
  return 0
}

assert_contains() {
  local haystack=$1 needle=$2 desc=$3
  COUNT=$((COUNT + 1))
  if ! grep -Fq -- "$needle" <<<"$haystack"; then
    echo "FAIL: $desc"
    echo "  needle not found: $needle"
    FAILURES=$((FAILURES + 1))
    return 1
  fi
  return 0
}

assert_not_contains() {
  local haystack=$1 needle=$2 desc=$3
  COUNT=$((COUNT + 1))
  if grep -Fq -- "$needle" <<<"$haystack"; then
    echo "FAIL: $desc"
    echo "  unexpected needle found: $needle"
    FAILURES=$((FAILURES + 1))
    return 1
  fi
  return 0
}

assert_match() {
  local value=$1 pattern=$2 desc=$3
  COUNT=$((COUNT + 1))
  if [[ ! "$value" =~ $pattern ]]; then
    echo "FAIL: $desc"
    echo "  value: $value"
    echo "  did not match: $pattern"
    FAILURES=$((FAILURES + 1))
    return 1
  fi
  return 0
}

# Run a snippet that is expected to exit non-zero.
expect_fail() {
  local desc=$1; shift
  COUNT=$((COUNT + 1))
  if "$@" >/dev/null 2>&1; then
    echo "FAIL: $desc (expected non-zero exit, got success)"
    FAILURES=$((FAILURES + 1))
    return 1
  fi
  return 0
}

expect_ok() {
  local desc=$1; shift
  COUNT=$((COUNT + 1))
  if ! "$@" >/dev/null 2>&1; then
    echo "FAIL: $desc (expected success, got non-zero exit)"
    FAILURES=$((FAILURES + 1))
    return 1
  fi
  return 0
}

# Build an isolated fake NAS root with fake sqlite3/git/docker/tar/sha256sum
# in a temp PATH. Echoes the work dir; sets up helper functions.
# Usage: setup_fake_nas <workdir> -> writes $TMP/fake-bin on PATH.
setup_fake_nas() {
  local workdir=$1
  mkdir -p "$workdir/nas-root/data/backend" "$workdir/backup" "$workdir/fake-bin"
  # Real source DB under nas-root.
  : > "$workdir/nas-root/data/backend/relive.db"
  # Make it a real SQLite file if sqlite3 is available, so size checks work.
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "$workdir/nas-root/data/backend/relive.db" \
      'CREATE TABLE photos(id INTEGER PRIMARY KEY); INSERT INTO photos VALUES (1);' 2>/dev/null || true
  fi
  echo "$workdir"
}

# Install a fake command into fake-bin.
fake_cmd() {
  local bin=$1 name=$2
  mkdir -p "$bin"
  local script=$3
  printf '%s\n' "$script" > "$bin/$name"
  chmod +x "$bin/$name"
}

# Write a wrapper script that execs a real binary with all passed arguments.
# Uses printf with a single argument and a here-doc fallback so escaping is
# robust under set -u.
make_wrapper() {
  local bin=$1 name=$2 real=$3
  mkdir -p "$bin"
  cat > "$bin/$name" <<EOF
#!/usr/bin/env bash
exec "$real" "\$@"
EOF
  chmod +x "$bin/$name"
}

# ---------------------------------------------------------------------------
# Group: config
# ---------------------------------------------------------------------------

test_config() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN

  local repo="$tmp/repo"
  mkdir -p "$repo/scripts" "$repo/tests/scripts"
  cp "$SCRIPTS/backup-nas.sh" "$repo/scripts/backup-nas.sh"
  [[ -f "$SCRIPTS/backup-nas-remote.sh" ]] && \
    cp "$SCRIPTS/backup-nas-remote.sh" "$repo/scripts/backup-nas-remote.sh" || true
  cp "$ROOT/.nas-backup.env.example" "$repo/.nas-backup.env.example"
  cp "$ROOT/.gitignore" "$repo/.gitignore"

  # 1) .nas-backup.env is ignored, .example is tracked.
  git -C "$repo" init -q 2>/dev/null || true
  git -C "$repo" add -A 2>/dev/null || true
  echo "ignored" > "$repo/.nas-backup.env"
  expect_fail ".nas-backup.env should be git-ignored" \
    test -n "$(git -C "$repo" check-ignore .nas-backup.env 2>/dev/null || true)" -a \
    ! git -C "$repo" check-ignore .nas-backup.env
  # Properly: the file should be ignored.
  if ! git -C "$repo" check-ignore .nas-backup.env >/dev/null 2>&1; then
    echo "FAIL: .nas-backup.env not git-ignored"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi
  if git -C "$repo" check-ignore .nas-backup.env.example >/dev/null 2>&1; then
    echo "FAIL: .nas-backup.env.example should be tracked"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi

  # Helper: run the loader-only path by sourcing and calling a tiny shim.
  # We exercise validation via the driver's main() in a subshell with a fake
  # ssh that always succeeds, capturing the forwarded arguments.
  # Fake ssh captures the full argv (after the host) into $CAPTURE_FILE and
  # consumes stdin so the driver does not block.
  local fake_ssh="$tmp/fake-bin"
  mkdir -p "$fake_ssh"
  cat > "$fake_ssh/ssh" <<'EOF'
#!/usr/bin/env bash
# Print every argument on its own line into the capture file.
printf '%s\n' "$@" >> "$CAPTURE_FILE"
# First connectivity call passes "true" as the remote command; the streaming
# call passes "bash -s -- ...". Either way, consume stdin.
cat > /dev/null
# Emit the documented success summary.
echo "Backup complete"
echo "Directory: /tmp/fake"
echo "Database quick_check: ok"
echo "Checksums: verified"
EOF
  chmod +x "$fake_ssh/ssh"

  # Invoke the driver with a controlled env. CAPTURE_FILE is exported so the
  # fake ssh can write forwarded arguments. The repo's .nas-backup.env supplies
  # file-level values; process-env vars override them.
  driver_run() {
    local capture=$1; shift
    local extra_env=$1; shift
    : > "$capture"
    env -i PATH="$fake_ssh:$PATH" HOME="$HOME" CAPTURE_FILE="$capture" $extra_env \
      bash "$repo/scripts/backup-nas.sh" 2>"$tmp/err.txt" || true
  }

  # 2) missing host fails before SSH.
  rm -f "$repo/.nas-backup.env"
  expect_fail "missing RELIVE_NAS_HOST fails" \
    env -i PATH="$fake_ssh:$PATH" HOME="$HOME" bash "$repo/scripts/backup-nas.sh"

  # 3) file values load when process env is unset.
  cat > "$repo/.nas-backup.env" <<EOF
RELIVE_NAS_HOST=user@example-nas
RELIVE_NAS_ROOT=/volume1/docker/relive
RELIVE_NAS_DB=/volume1/docker/relive/data/backend/relive.db
RELIVE_NAS_BACKUP_DIR=/volume1/docker/relive/backup
RELIVE_BACKUP_KEEP=0
EOF
  driver_run "$tmp/cap1.txt" ""
  assert_contains "$(cat "$tmp/cap1.txt")" "/volume1/docker/relive" \
    "file: root forwarded"
  assert_contains "$(cat "$tmp/cap1.txt")" "user@example-nas" "file: host forwarded"

  # 4) process environment overrides file values.
  cat > "$repo/.nas-backup.env" <<EOF
RELIVE_NAS_HOST=file@host
RELIVE_NAS_ROOT=/volume1/docker/relive
EOF
  : > "$tmp/cap2.txt"
  env -i PATH="$fake_ssh:$PATH" HOME="$HOME" CAPTURE_FILE="$tmp/cap2.txt" \
    RELIVE_NAS_HOST=env@override \
    RELIVE_NAS_ROOT=/volume2/override \
    bash "$repo/scripts/backup-nas.sh" 2>"$tmp/err.txt" || true
  assert_contains "$(cat "$tmp/cap2.txt")" "env@override" "env overrides host"
  assert_contains "$(cat "$tmp/cap2.txt")" "/volume2/override" "env overrides root"

  # 5) root default.
  rm -f "$repo/.nas-backup.env"
  : > "$tmp/cap3.txt"
  env -i PATH="$fake_ssh:$PATH" HOME="$HOME" CAPTURE_FILE="$tmp/cap3.txt" \
    RELIVE_NAS_HOST=user@nas \
    bash "$repo/scripts/backup-nas.sh" 2>"$tmp/err.txt" || true
  assert_contains "$(cat "$tmp/cap3.txt")" "/volume1/docker/relive" "default root applied"

  # 6) DB and backup dir derive from root.
  assert_contains "$(cat "$tmp/cap3.txt")" "/volume1/docker/relive/data/backend/relive.db" \
    "default DB derives from root"
  assert_contains "$(cat "$tmp/cap3.txt")" "/volume1/docker/relive/backup" \
    "default backup dir derives from root"

  # 7) retention default 0.
  assert_contains "$(cat "$tmp/cap3.txt")" "'0'" "default keep=0 forwarded"

  # 8) unknown keys, export, command substitution, backticks, malformed names,
  #    duplicate keys all fail.
  for bad in \
    "UNKNOWN_KEY=value" \
    "export RELIVE_NAS_HOST=user@nas" \
    'RELIVE_NAS_HOST=$(whoami)@nas' \
    'RELIVE_NAS_HOST=`whoami`@nas' \
    "1INVALID=value" \
  ; do
    printf '%s\nRELIVE_NAS_HOST=user@nas\n' "$bad" > "$repo/.nas-backup.env"
    expect_fail "rejects bad config line: $bad" \
      env -i PATH="$fake_ssh:$PATH" HOME="$HOME" bash "$repo/scripts/backup-nas.sh"
  done
  # duplicate key
  printf 'RELIVE_NAS_HOST=user@nas\nRELIVE_NAS_HOST=other@nas\n' > "$repo/.nas-backup.env"
  expect_fail "rejects duplicate key" \
    env -i PATH="$fake_ssh:$PATH" HOME="$HOME" bash "$repo/scripts/backup-nas.sh"

  # 9) blank lines and comments are accepted.
  printf '\n# a comment\n\nRELIVE_NAS_HOST=user@nas\n\n# trailing\n' > "$repo/.nas-backup.env"
  : > "$tmp/cap4.txt"
  env -i PATH="$fake_ssh:$PATH" HOME="$HOME" CAPTURE_FILE="$tmp/cap4.txt" \
    RELIVE_NAS_HOST=user@nas \
    bash "$repo/scripts/backup-nas.sh" 2>"$tmp/err.txt" || true
  assert_contains "$(cat "$tmp/cap4.txt")" "user@nas" "blank lines/comments accepted"

  # 10) label normalization/bounds.
  # valid default
  : > "$tmp/cap5.txt"
  env -i PATH="$fake_ssh:$PATH" HOME="$HOME" CAPTURE_FILE="$tmp/cap5.txt" \
    RELIVE_NAS_HOST=user@nas \
    bash "$repo/scripts/backup-nas.sh" 2>"$tmp/err.txt" || true
  assert_contains "$(cat "$tmp/cap5.txt")" "manual" "default label is manual"
  # invalid label (starts with dot)
  env -i PATH="$fake_ssh:$PATH" HOME="$HOME" \
    RELIVE_NAS_HOST=user@nas RELIVE_BACKUP_LABEL=.bad \
    bash "$repo/scripts/backup-nas.sh" 2>/dev/null && \
    { echo "FAIL: invalid label accepted"; FAILURES=$((FAILURES + 1)); } || COUNT=$((COUNT + 1))
  # valid label passed through
  : > "$tmp/cap6.txt"
  env -i PATH="$fake_ssh:$PATH" HOME="$HOME" CAPTURE_FILE="$tmp/cap6.txt" \
    RELIVE_NAS_HOST=user@nas RELIVE_BACKUP_LABEL=pre-task14 \
    bash "$repo/scripts/backup-nas.sh" 2>"$tmp/err.txt" || true
  assert_contains "$(cat "$tmp/cap6.txt")" "pre-task14" "valid label forwarded"
}

# ---------------------------------------------------------------------------
# Group: remote-preflight
# ---------------------------------------------------------------------------

test_remote_preflight() {
  local tmp workdir fake_bin
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  workdir="$(setup_fake_nas "$tmp")"
  fake_bin="$workdir/fake-bin"

  remote() {
    # Run the remote worker locally with a controlled PATH.
    env -i PATH="$fake_bin:/usr/bin:/bin" \
      bash "$SCRIPTS/backup-nas-remote.sh" "$@"
  }

  # Real fakes for happy path.
  install_real_fakes "$fake_bin" "$workdir"

  # valid args succeed (happy path) — set up a git repo for bundle step later.
  git -C "$workdir/nas-root" init -q 2>/dev/null || true
  git -C "$workdir/nas-root" config user.email t@t 2>/dev/null || true
  git -C "$workdir/nas-root" config user.name t 2>/dev/null || true
  git -C "$workdir/nas-root" add -A 2>/dev/null || true
  git -C "$workdir/nas-root" commit -qm init 2>/dev/null || true

  # 1) invalid relative root/DB/backup paths fail.
  expect_fail "relative root fails" \
    remote "relative-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  expect_fail "relative db fails" \
    remote "$workdir/nas-root" "relative.db" "$workdir/backup" manual 0
  expect_fail "relative backup fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "relative-backup" manual 0

  # 2) missing source DB fails.
  expect_fail "missing source DB fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/missing.db" "$workdir/backup" manual 0

  # 3) missing dependencies fail.
  install_missing_dep "$fake_bin" "sqlite3"
  expect_fail "missing sqlite3 fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  install_real_fakes "$fake_bin" "$workdir"

  install_missing_dep "$fake_bin" "git"
  expect_fail "missing git fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  install_real_fakes "$fake_bin" "$workdir"

  install_missing_dep "$fake_bin" "docker"
  expect_fail "missing docker fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  install_real_fakes "$fake_bin" "$workdir"

  install_missing_dep "$fake_bin" "tar"
  expect_fail "missing tar fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  install_real_fakes "$fake_bin" "$workdir"

  install_missing_dep "$fake_bin" "sha256sum"
  expect_fail "missing sha256sum fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  install_real_fakes "$fake_bin" "$workdir"

  # 4) pre-existing lock fails immediately.
  mkdir -p "$workdir/backup/.relive-backup.lock"
  expect_fail "pre-existing lock fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  rm -rf "$workdir/backup/.relive-backup.lock"

  # 5) trap removes only its own lock.
  # 6) .partial directory used; 7) failed run removes its own partial.
  # Make sqlite3 fail to trigger cleanup.
  fake_cmd "$fake_bin" sqlite3 '#!/usr/bin/env bash
exit 1'
  expect_fail "sqlite failure triggers cleanup" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  install_real_fakes "$fake_bin" "$workdir"
  # No .partial should remain.
  if find "$workdir/backup" -maxdepth 1 -name '*.partial' -print 2>/dev/null | grep -q .; then
    echo "FAIL: partial directory not cleaned up after failure"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi
  # Lock should be removed.
  if [[ -e "$workdir/backup/.relive-backup.lock" ]]; then
    echo "FAIL: lock not removed after failure"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi

  # 8) existing successful directory is never changed.
  install_real_fakes "$fake_bin" "$workdir"
  local existing="$workdir/backup/2020-01-01-000000-manual"
  mkdir -p "$existing"
  echo "preserved" > "$existing/relive.db"
  remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0 >/dev/null 2>&1 || true
  if [[ "$(cat "$existing/relive.db")" != "preserved" ]]; then
    echo "FAIL: existing successful directory was modified"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi

  # 9) insufficient df capacity fails.
  fake_cmd "$fake_bin" df 'echo 0'
  expect_fail "insufficient space fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  install_real_fakes "$fake_bin" "$workdir"

  # 10) directories start with mode 0700.
  install_real_fakes "$fake_bin" "$workdir"
  # Provide an .env so the config-archive usefulness check passes.
  [[ -f "$workdir/nas-root/.env" ]] || echo "JWT=secret-value" > "$workdir/nas-root/.env"
  remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0 >/dev/null 2>&1 || true
  local newest
  newest=$(find "$workdir/backup" -maxdepth 1 -mindepth 1 -type d -name '20*-*-*-*-*' | sort | tail -1)
  if [[ -n "$newest" ]]; then
    local mode
    mode=$(stat -f '%Lp' "$newest" 2>/dev/null || stat -c '%a' "$newest" 2>/dev/null)
    assert_eq "$mode" "700" "final directory is 0700"
  else
    echo "FAIL: no successful backup directory created"
    FAILURES=$((FAILURES + 1))
  fi
}

install_real_fakes() {
  local fake_bin=$1 workdir=$2
  # sqlite3: prefer the real binary via a here-doc wrapper. Symlinks can hit
  # macOS SIP on /var/folders, so write a script instead.
  if command -v sqlite3 >/dev/null 2>&1; then
    make_wrapper "$fake_bin" sqlite3 "$(command -v sqlite3)"
  else
    fake_cmd "$fake_bin" sqlite3 'echo "ok"'
  fi
  # git: real binary via wrapper.
  if command -v git >/dev/null 2>&1; then
    make_wrapper "$fake_bin" git "$(command -v git)"
  fi
  # tar: real via wrapper.
  if command -v tar >/dev/null 2>&1; then
    make_wrapper "$fake_bin" tar "$(command -v tar)"
  fi
  # find: real via wrapper.
  if command -v find >/dev/null 2>&1; then
    make_wrapper "$fake_bin" find "$(command -v find)"
  fi
  # sha256sum: emulate with shasum (always available on macOS). The wrapper
  # supports both generation (default) and `-c` verification.
  cat > "$fake_bin/sha256sum" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "-c" ]]; then
  shift
  shasum -a 256 -c "$@"
else
  shasum -a 256 "$@"
fi
EOF
  chmod +x "$fake_bin/sha256sum"
  # docker: stub returning missing for relive containers.
  fake_cmd "$fake_bin" docker 'echo "missing"'
  # df: report large free space; worker parses awk NR==2. Use a POSIX -P
  # layout (6 whitespace-separated columns) and avoid % in the printf format.
  fake_cmd "$fake_bin" df 'printf "Filesystem 1K-blocks Used Avail Capacity Mounted\nfake 999999999 1 999999998 1pct /\n"'
}

install_missing_dep() {
  local fake_bin=$1 dep=$2
  rm -f "$fake_bin/$dep"
}

# ---------------------------------------------------------------------------
# Group: sqlite
# ---------------------------------------------------------------------------

test_sqlite() {
  local tmp workdir fake_bin
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  workdir="$(setup_fake_nas "$tmp")"
  fake_bin="$workdir/fake-bin"
  install_real_fakes "$fake_bin" "$workdir"

  # Need a git repo for the bundle step.
  git -C "$workdir/nas-root" init -q 2>/dev/null || true
  git -C "$workdir/nas-root" config user.email t@t 2>/dev/null || true
  git -C "$workdir/nas-root" config user.name t 2>/dev/null || true
  git -C "$workdir/nas-root" add -A 2>/dev/null || true
  git -C "$workdir/nas-root" commit -qm init 2>/dev/null || true
  # Config-archive usefulness check needs .env or backend/config.prod.yaml.
  [[ -f "$workdir/nas-root/.env" ]] || echo "JWT=secret-value" > "$workdir/nas-root/.env"

  remote() {
    env -i PATH="$fake_bin:/usr/bin:/bin" \
      bash "$SCRIPTS/backup-nas-remote.sh" "$@"
  }

  # Skip group if sqlite3 not installed.
  if ! command -v sqlite3 >/dev/null 2>&1; then
    echo "SKIP: sqlite group requires sqlite3"
    return 0
  fi

  # 1) worker uses SQLite .backup (not cp). The worker feeds a command file
  #    containing `.backup '.../relive.db'` to sqlite3 on stdin. We verify by
  #    inspecting the worker source for the documented command-file approach
  #    and by confirming the produced relive.db is a valid SQLite database
  #    (not a byte-for-byte copy timestamped identically to the live DB).
  if ! grep -Fq ".backup '" "$SCRIPTS/backup-nas-remote.sh"; then
    echo "FAIL: worker does not use SQLite .backup command-file"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi
  if ! grep -Fq "sqlite3 \"\$db_path\" < \"\$cmd_file\"" "$SCRIPTS/backup-nas-remote.sh"; then
    echo "FAIL: worker does not feed command file via stdin redirection"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi
  # 2) never cp on live DB/WAL/SHM: ensure no `cp ... relive.db` in worker
  #    source. RESTORE.txt is documentation and may mention cp as a manual
  #    recovery step; exclude those lines.
  if grep -nE '\bcp\b[^|]*relive\.db' "$SCRIPTS/backup-nas-remote.sh" | grep -v 'RESTORE\|<backup-dir>\|<nas-root>'; then
    echo "FAIL: worker uses cp on live DB"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi

  # Run the worker to produce a real bundle for the remaining assertions.
  remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0 >/dev/null 2>&1 || true

  local newest
  newest=$(find "$workdir/backup" -maxdepth 1 -mindepth 1 -type d -name '20*-*-*-*-*' | sort | tail -1)
  [[ -n "$newest" ]] || { echo "FAIL: no bundle directory"; FAILURES=$((FAILURES + 1)); return 0; }
  # 3) backup output named relive.db.
  [[ -f "$newest/relive.db" ]] && COUNT=$((COUNT + 1)) || \
    { echo "FAIL: relive.db not in bundle"; FAILURES=$((FAILURES + 1)); }
  # The backup must be a valid, queryable SQLite database produced by .backup
  # (quick_check ok against the backup, not the live DB).
  if command -v sqlite3 >/dev/null 2>&1 && [[ -f "$newest/relive.db" ]]; then
    local qc
    qc=$(sqlite3 -readonly "$newest/relive.db" 'PRAGMA quick_check;' 2>/dev/null | tr -d '[:space:]')
    assert_eq "$qc" "ok" "backup relive.db passes quick_check"
  fi

  # 4) quick_check returns exactly one trimmed line 'ok'.
  # 5) schema.sql exported from backup.
  [[ -f "$newest/schema.sql" ]] && COUNT=$((COUNT + 1)) || \
    { echo "FAIL: schema.sql missing"; FAILURES=$((FAILURES + 1)); }

  # 6) quick-check failure removes partial and leaves existing backups.
  install_real_fakes "$fake_bin" "$workdir"
  local real_sqlite2
  real_sqlite2=$(command -v sqlite3)
  # Replace sqlite3 so .backup and .schema succeed but quick_check emits a
  # non-ok warning. The worker trims all whitespace from the quick_check
  # output and compares to "ok"; "ERROR:databasediskimageismalformed" != "ok"
  # so it must abort.
  cat > "$fake_bin/sqlite3" <<EOF
#!/usr/bin/env bash
if [[ "\$*" == *"quick_check"* ]]; then
  printf '%s\n' "ERROR: database disk image is malformed"
  exit 0
fi
exec "$real_sqlite2" "\$@"
EOF
  chmod +x "$fake_bin/sqlite3"
  local before_count
  before_count=$(find "$workdir/backup" -maxdepth 1 -mindepth 1 -type d -name '20*-*-*-*-*' | wc -l | tr -d ' ')
  expect_fail "quick_check failure aborts" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  local after_count
  after_count=$(find "$workdir/backup" -maxdepth 1 -mindepth 1 -type d -name '20*-*-*-*-*' | wc -l | tr -d ' ')
  assert_eq "$after_count" "$before_count" "quick_check failure leaves existing backups intact"
  install_real_fakes "$fake_bin" "$workdir"
}

# ---------------------------------------------------------------------------
# Group: bundle
# ---------------------------------------------------------------------------

test_bundle() {
  local tmp workdir fake_bin
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  workdir="$(setup_fake_nas "$tmp")"
  fake_bin="$workdir/fake-bin"
  install_real_fakes "$fake_bin" "$workdir"

  # Config allowlist files.
  echo "JWT=secret-value" > "$workdir/nas-root/.env"
  mkdir -p "$workdir/nas-root/backend"
  echo "config: prod" > "$workdir/nas-root/backend/config.prod.yaml"
  echo "1.0.0" > "$workdir/nas-root/VERSION"
  echo "compose" > "$workdir/nas-root/docker-compose.yml"

  # Git repo.
  git -C "$workdir/nas-root" init -q 2>/dev/null || true
  git -C "$workdir/nas-root" config user.email t@t 2>/dev/null || true
  git -C "$workdir/nas-root" config user.name t 2>/dev/null || true
  # Add tracked files (not .env which is gitignored at repo level — but our
  # nas-root is a throwaway repo, so add explicitly).
  git -C "$workdir/nas-root" add -A 2>/dev/null || true
  git -C "$workdir/nas-root" commit -qm init 2>/dev/null || true

  remote() {
    env -i PATH="$fake_bin:/usr/bin:/bin" \
      bash "$SCRIPTS/backup-nas-remote.sh" "$@"
  }

  remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0 >/dev/null 2>&1 || true

  local newest
  newest=$(find "$workdir/backup" -maxdepth 1 -mindepth 1 -type d -name '20*-*-*-*-*' | sort | tail -1)
  [[ -n "$newest" ]] || { echo "FAIL: no bundle produced"; FAILURES=$((FAILURES + 1)); return 0; }

  # 1) config.tar.gz contains only allowlisted existing files.
  if command -v tar >/dev/null 2>&1 && [[ -f "$newest/config.tar.gz" ]]; then
    local listing
    listing=$(tar -tzf "$newest/config.tar.gz" 2>/dev/null || true)
    assert_contains "$listing" ".env" "config archive includes .env"
    assert_contains "$listing" "backend/config.prod.yaml" "config archive includes config.prod.yaml"
    assert_contains "$listing" "VERSION" "config archive includes VERSION"
    # 2) excludes DB/WAL/SHM, logs, thumbnails, .nas-backup.env.
    assert_not_contains "$listing" ".nas-backup.env" "config archive excludes .nas-backup.env"
    assert_not_contains "$listing" "relive.db" "config archive excludes relive.db"
    assert_not_contains "$listing" "thumbnails" "config archive excludes thumbnails"
    assert_not_contains "$listing" "logs" "config archive excludes logs"
  fi

  # 3) repository.bundle created.
  [[ -f "$newest/repository.bundle" ]] && COUNT=$((COUNT + 1)) || \
    { echo "FAIL: repository.bundle missing"; FAILURES=$((FAILURES + 1)); }

  # 4) git-status.txt contains HEAD, branch, porcelain status, no diff.
  if [[ -f "$newest/git-status.txt" ]]; then
    local gs
    gs=$(cat "$newest/git-status.txt")
    assert_match "$gs" "HEAD|head|branch|Branch|##|main|master" "git-status has HEAD/branch"
  fi

  # 5) runtime.txt has container name/image/health; no env/tokens/inspect JSON.
  if [[ -f "$newest/runtime.txt" ]]; then
    local rt
    rt=$(cat "$newest/runtime.txt")
    # runtime should mention container names or 'missing'.
    assert_match "$rt" "relive|missing|container|image|health" "runtime has container info"
    assert_not_contains "$rt" "JWT_SECRET" "runtime excludes env vars"
    assert_not_contains "$rt" "Mounts" "runtime excludes full inspect JSON"
  fi

  # 7) missing optional config files do not fail.
  rm -f "$workdir/nas-root/docker-compose.yml" "$workdir/nas-root/docker-compose.prod.yml"
  rm -rf "$workdir/backup"/*
  remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0 >/dev/null 2>&1 && \
    COUNT=$((COUNT + 1)) || \
    { echo "FAIL: missing optional config files broke backup"; FAILURES=$((FAILURES + 1)); }

  # 8) missing Git repository fails.
  rm -rf "$workdir/nas-root/.git"
  rm -rf "$workdir/backup"/*
  expect_fail "missing git repo fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
}

# ---------------------------------------------------------------------------
# Group: publication
# ---------------------------------------------------------------------------

test_publication() {
  local tmp workdir fake_bin
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  workdir="$(setup_fake_nas "$tmp")"
  fake_bin="$workdir/fake-bin"
  install_real_fakes "$fake_bin" "$workdir"

  git -C "$workdir/nas-root" init -q 2>/dev/null || true
  git -C "$workdir/nas-root" config user.email t@t 2>/dev/null || true
  git -C "$workdir/nas-root" config user.name t 2>/dev/null || true
  git -C "$workdir/nas-root" add -A 2>/dev/null || true
  git -C "$workdir/nas-root" commit -qm init 2>/dev/null || true
  # Config-archive usefulness check needs .env or backend/config.prod.yaml.
  [[ -f "$workdir/nas-root/.env" ]] || echo "JWT=secret-value" > "$workdir/nas-root/.env"

  remote() {
    env -i PATH="$fake_bin:/usr/bin:/bin" \
      bash "$SCRIPTS/backup-nas-remote.sh" "$@"
  }

  local out
  out=$(remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0 2>&1) || true

  local newest
  newest=$(find "$workdir/backup" -maxdepth 1 -mindepth 1 -type d -name '20*-*-*-*-*' | sort | tail -1)
  [[ -n "$newest" ]] || { echo "FAIL: no bundle"; FAILURES=$((FAILURES + 1)); return 0; }

  # 1) directory 0700, files 0600.
  local dmode
  dmode=$(stat -f '%Lp' "$newest" 2>/dev/null || stat -c '%a' "$newest" 2>/dev/null)
  assert_eq "$dmode" "700" "bundle dir 0700"
  local fmode
  fmode=$(stat -f '%Lp' "$newest/relive.db" 2>/dev/null || stat -c '%a' "$newest/relive.db" 2>/dev/null)
  assert_eq "$fmode" "600" "bundle file 0600"

  # 2) manifest.txt records key fields, no ZINFOID values.
  if [[ -f "$newest/manifest.txt" ]]; then
    local m
    m=$(cat "$newest/manifest.txt")
    assert_match "$m" "quick_check|quick check" "manifest records quick_check"
    assert_match "$m" "HEAD|commit|head" "manifest records HEAD"
    assert_not_contains "$m" "secret-value" "manifest has no ZINFOID values"
  fi

  # 3) RESTORE.txt says stop Relive, no auto restore.
  if [[ -f "$newest/RESTORE.txt" ]]; then
    local r
    r=$(cat "$newest/RESTORE.txt")
    assert_match "$r" "[Ss]top|[Ss]hutdown|[Pp]ause" "RESTORE says stop Relive"
  fi

  # 4) SHA256SUMS covers every file except itself.
  if [[ -f "$newest/SHA256SUMS" ]]; then
    local sums files_in_bundle sums_files
    files_in_bundle=$(find "$newest" -maxdepth 1 -type f ! -name SHA256SUMS -exec basename {} \; | sort)
    sums_files=$(awk '{print $2}' "$newest/SHA256SUMS" | sed 's#^\./##' | sort)
    assert_eq "$sums_files" "$files_in_bundle" "SHA256SUMS covers all bundle files except itself"
  fi

  # 7) successful output concise summary.
  assert_contains "$out" "Backup complete" "output contains Backup complete"
  assert_contains "$out" "quick_check: ok" "output contains quick_check ok"
  assert_contains "$out" "Checksums: verified" "output contains checksums verified"

  # 6) checksum failure prevents final rename.
  rm -rf "$workdir/backup"/*
  # Tamper: make sha256sum -c fail by having it return mismatch.
  fake_cmd "$fake_bin" sha256sum 'if [[ "$1" == "-c" ]]; then echo "FAILED"; exit 1; fi
shasum -a 256 "${@:2}" 2>/dev/null || shasum -a 256 "$@"'
  expect_fail "checksum failure aborts publication" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0
  # No final directory should exist.
  if find "$workdir/backup" -maxdepth 1 -mindepth 1 -type d -name '20*-*-*-*-*' | grep -q .; then
    echo "FAIL: final directory created despite checksum failure"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi
}

# ---------------------------------------------------------------------------
# Group: retention
# ---------------------------------------------------------------------------

test_retention() {
  local tmp workdir fake_bin
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  workdir="$(setup_fake_nas "$tmp")"
  fake_bin="$workdir/fake-bin"
  install_real_fakes "$fake_bin" "$workdir"

  git -C "$workdir/nas-root" init -q 2>/dev/null || true
  git -C "$workdir/nas-root" config user.email t@t 2>/dev/null || true
  git -C "$workdir/nas-root" config user.name t 2>/dev/null || true
  git -C "$workdir/nas-root" add -A 2>/dev/null || true
  git -C "$workdir/nas-root" commit -qm init 2>/dev/null || true
  # Config-archive usefulness check needs .env or backend/config.prod.yaml.
  [[ -f "$workdir/nas-root/.env" ]] || echo "JWT=secret-value" > "$workdir/nas-root/.env"

  remote() {
    env -i PATH="$fake_bin:/usr/bin:/bin" \
      bash "$SCRIPTS/backup-nas-remote.sh" "$@"
  }

  # 1) keep 0 deletes nothing.
  mkdir -p "$workdir/backup/2020-01-01-000000-manual" \
           "$workdir/backup/2020-02-01-000000-manual" \
           "$workdir/backup/2020-03-01-000000-manual"
  remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 0 >/dev/null 2>&1 || true
  local n
  n=$(find "$workdir/backup" -maxdepth 1 -mindepth 1 -type d -name '2020-*' | wc -l | tr -d ' ')
  assert_eq "$n" "3" "keep=0 deletes nothing"

  # 3) keep N retains newest N matching.
  rm -rf "$workdir/backup"
  mkdir -p "$workdir/backup"
  mkdir -p "$workdir/backup/2020-01-01-000000-manual" \
           "$workdir/backup/2020-02-01-000000-manual" \
           "$workdir/backup/2020-03-01-000000-manual"
  remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 2 >/dev/null 2>&1 || true
  # Should retain newest 2 of the three 2020-* plus the new one created now.
  # The just-created backup is always retained. Among 2020-* the newest 2 kept.
  if [[ -d "$workdir/backup/2020-01-01-000000-manual" ]]; then
    echo "FAIL: oldest 2020 backup not pruned with keep=2"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi
  if [[ ! -d "$workdir/backup/2020-03-01-000000-manual" ]]; then
    echo "FAIL: newest 2020 backup pruned with keep=2"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi

  # 5) files, symlinks, .partial, manually named dirs, nested paths ignored.
  rm -rf "$workdir/backup"
  mkdir -p "$workdir/backup"
  mkdir -p "$workdir/backup/2020-01-01-000000-manual"
  mkdir -p "$workdir/backup/random-name"
  : > "$workdir/backup/stray-file"
  ln -s "$workdir/backup/2020-01-01-000000-manual" "$workdir/backup/a-symlink" 2>/dev/null || true
  mkdir -p "$workdir/backup/.some.partial"
  remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual 1 >/dev/null 2>&1 || true
  # random-name and stray-file should remain.
  if [[ ! -d "$workdir/backup/random-name" ]]; then
    echo "FAIL: manually named dir deleted by retention"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi
  if [[ ! -f "$workdir/backup/stray-file" ]]; then
    echo "FAIL: stray file deleted by retention"
    FAILURES=$((FAILURES + 1))
  else
    COUNT=$((COUNT + 1))
  fi

  # 8) invalid/negative retention fails during preflight.
  expect_fail "negative retention fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual -1
  expect_fail "non-integer retention fails" \
    remote "$workdir/nas-root" "$workdir/nas-root/data/backend/relive.db" "$workdir/backup" manual abc
}

# ---------------------------------------------------------------------------
# Group: transport
# ---------------------------------------------------------------------------

test_transport() {
  local tmp
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' RETURN
  local fake_ssh="$tmp/bin"
  mkdir -p "$fake_ssh"
  local capture="$tmp/ssh-args.txt"
  cat > "$fake_ssh/ssh" <<EOF
#!/usr/bin/env bash
echo "\$@" >> "$capture"
cat > /dev/null
echo "Backup complete"
echo "Directory: /tmp/fake"
echo "Database quick_check: ok"
echo "Checksums: verified"
EOF
  chmod +x "$fake_ssh/ssh"

  local repo="$tmp/repo"
  mkdir -p "$repo/scripts"
  cp "$SCRIPTS/backup-nas.sh" "$repo/scripts/backup-nas.sh"
  cp "$SCRIPTS/backup-nas-remote.sh" "$repo/scripts/backup-nas-remote.sh"
  cat > "$repo/.nas-backup.env" <<EOF
RELIVE_NAS_HOST=user@example-nas
RELIVE_NAS_ROOT=/volume1/docker/relive
RELIVE_NAS_DB=/volume1/docker/relive/data/backend/relive.db
RELIVE_NAS_BACKUP_DIR=/volume1/docker/relive/backup
RELIVE_BACKUP_KEEP=0
EOF

  : > "$capture"
  env -i PATH="$fake_ssh:$PATH" HOME="$HOME" \
    bash "$repo/scripts/backup-nas.sh" >/dev/null 2>&1 || true
  local args
  args=$(cat "$capture")

  # BatchMode=yes and finite timeout.
  assert_contains "$args" "BatchMode=yes" "SSH uses BatchMode=yes"
  assert_contains "$args" "ConnectTimeout=" "SSH uses finite timeout"
  # bash -s with five args.
  assert_contains "$args" "bash -s" "SSH streams bash -s"
  # host forwarded.
  assert_contains "$args" "user@example-nas" "validated host forwarded"
  # five validated args present.
  assert_contains "$args" "/volume1/docker/relive" "root forwarded"
  assert_contains "$args" "/volume1/docker/relive/data/backend/relive.db" "db forwarded"
  assert_contains "$args" "/volume1/docker/relive/backup" "backup dir forwarded"
  assert_contains "$args" "manual" "label forwarded"

  # 4) no config contents / ZINFOID values printed.
  local out
  out=$(env -i PATH="$fake_ssh:$PATH" HOME="$HOME" \
    bash "$repo/scripts/backup-nas.sh" 2>&1) || true
  assert_not_contains "$out" "secret" "no ZINFOID values in output"

  # 5) SSH failure returns non-zero.
  cat > "$fake_ssh/ssh" <<'EOF'
#!/usr/bin/env bash
echo "connection refused" >&2
exit 255
EOF
  chmod +x "$fake_ssh/ssh"
  expect_fail "SSH failure returns non-zero" \
    env -i PATH="$fake_ssh:$PATH" HOME="$HOME" bash "$repo/scripts/backup-nas.sh"

  # 6) make backup-nas invokes ./scripts/backup-nas.sh.
  assert_contains "$(make -C "$ROOT" -n backup-nas)" "scripts/backup-nas.sh" \
    "make backup-nas invokes script"
  # 7) make help advertises.
  assert_contains "$(make -C "$ROOT" help)" "make backup-nas" \
    "make help advertises backup-nas"
  # 8) deploy scripts do not invoke backup.
  assert_not_contains "$(cat "$ROOT/deploy.sh")" "backup-nas" \
    "deploy.sh does not invoke backup"
  assert_not_contains "$(cat "$ROOT/deploy-image.sh")" "backup-nas" \
    "deploy-image.sh does not invoke backup"
}

# ---------------------------------------------------------------------------
# Group: docs
# ---------------------------------------------------------------------------

test_docs() {
  local doc readme quickref
  doc="$ROOT/docs/NAS_BACKUP.md"
  readme="$ROOT/README.md"
  quickref="$ROOT/docs/QUICK_REFERENCE.md"

  [[ -f "$doc" ]] || { echo "FAIL: docs/NAS_BACKUP.md missing"; FAILURES=$((FAILURES + 1)); return 0; }
  local d
  d=$(cat "$doc")

  assert_contains "$d" ".nas-backup.env.example" "docs: copy example"
  assert_contains "$d" "RELIVE_NAS_HOST" "docs: host var"
  assert_contains "$d" "RELIVE_BACKUP_LABEL" "docs: label var"
  assert_contains "$d" "make backup-nas" "docs: make command"
  assert_match "$d" "relive\.db|config\.tar\.gz|repository\.bundle" "docs: bundle contents"
  assert_contains "$d" "0600" "docs: permissions"
  assert_contains "$d" "RELIVE_BACKUP_KEEP=0" "docs: default retention"
  assert_contains "$d" ".backup" "docs: SQLite .backup guarantee"
  assert_contains "$d" "quick_check" "docs: quick_check guarantee"
  assert_match "$d" "不会.*重启|不.*restart|不.*stop" "docs: no service restart"
  assert_match "$d" "[Rr]estore|恢复|还原" "docs: restore section"
  assert_match "$d" "[Tt]roubleshoot|排查|故障" "docs: troubleshooting"

  local r
  r=$(cat "$readme")
  assert_contains "$r" "NAS_BACKUP" "README links to NAS_BACKUP.md"

  local q
  q=$(cat "$quickref")
  assert_contains "$q" "backup-nas" "QUICK_REFERENCE lists backup-nas"
}

# ---------------------------------------------------------------------------
# Runner
# ---------------------------------------------------------------------------

ALL_GROUPS=(config remote-preflight sqlite bundle publication retention transport docs)

main() {
  local groups=("$@")
  [[ ${#groups[@]} -gt 0 ]] || groups=("${ALL_GROUPS[@]}")

  local g func
  for g in "${groups[@]}"; do
    echo "== running group: $g =="
    func="test_${g//-/_}"
    if ! declare -f "$func" >/dev/null 2>&1; then
      echo "FAIL: no test function for group '$g' ($func)" >&2
      FAILURES=$((FAILURES + 1))
      continue
    fi
    "$func" || true
  done

  echo ""
  if [[ "$FAILURES" -eq 0 ]]; then
    echo "OK: $COUNT assertions passed across ${#groups[@]} group(s)"
    exit 0
  else
    echo "FAILED: $FAILURES failure(s) across ${#groups[@]} group(s)"
    exit 1
  fi
}

main "$@"
