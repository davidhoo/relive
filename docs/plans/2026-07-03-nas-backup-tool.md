# NAS Backup Tool Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a `make backup-nas` command that runs from the development machine, connects to the configured Relive NAS over SSH, and creates a verified, permission-restricted SQLite online-backup bundle without restarting services.

**Architecture:** A local driver loads allowlisted values from an untracked `.nas-backup.env`, validates overrides, and streams a tracked NAS-side worker to `bash -s` over non-interactive SSH. The worker creates the complete bundle in a locked `.partial` directory, validates SQLite and checksums, then atomically publishes it; deployment and restore remain separate operations.

**Tech Stack:** Bash, GNU Make, SSH, SQLite CLI, Docker CLI, Git bundle, tar, sha256sum

---

### Task 1: Add dedicated backup configuration loading

**Files:**
- Create: `.nas-backup.env.example`
- Modify: `.gitignore`
- Create: `scripts/backup-nas.sh`
- Create: `tests/scripts/test_backup_nas.sh`

**Step 1: Write failing configuration tests**

Create a shell test harness with `fail`, `assert_eq`, `assert_contains`, and isolated temporary repository/config directories. Add cases for:

- `.nas-backup.env` is ignored while `.nas-backup.env.example` is tracked;
- missing `RELIVE_NAS_HOST` fails before SSH;
- file values load when the process environment is unset;
- process environment overrides file values;
- root defaults to `/volume1/docker/relive`;
- DB and backup directory defaults derive from the final root;
- retention defaults to `0`;
- unknown keys, `export`, command substitution, backticks, malformed names, and duplicate keys fail;
- blank lines and `#` comments are accepted;
- labels are normalized/bounded or rejected according to the design.

Use a source-safe main guard in the future driver:

```bash
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
```

Run:

```bash
bash tests/scripts/test_backup_nas.sh config
```

Expected: FAIL because `.nas-backup.env.example` and the loader do not exist.

**Step 2: Add the ignored real file and tracked template**

Add to `.gitignore`:

```gitignore
.nas-backup.env
!.nas-backup.env.example
```

Create `.nas-backup.env.example` with placeholders only:

```bash
RELIVE_NAS_HOST=user@nas.example.com
RELIVE_NAS_ROOT=/volume1/docker/relive
RELIVE_NAS_DB=/volume1/docker/relive/data/backend/relive.db
RELIVE_NAS_BACKUP_DIR=/volume1/docker/relive/backup
RELIVE_BACKUP_KEEP=0
```

Do not include a real hostname, user, token, key path, or password.

**Step 3: Implement the strict allowlist loader**

The driver must accept only:

```text
RELIVE_NAS_HOST
RELIVE_NAS_ROOT
RELIVE_NAS_DB
RELIVE_NAS_BACKUP_DIR
RELIVE_BACKUP_KEEP
RELIVE_BACKUP_LABEL
```

Parse plain `KEY=value` lines without `source`, `eval`, or command substitution. Capture which values were supplied by the process environment before reading the file so environment values retain precedence. Apply derived path defaults only after file parsing.

Validate:

```text
host: non-empty, no whitespace/control characters, does not begin with '-'
paths: absolute, characters [A-Za-z0-9._/-], no '..' segment
keep: integer >= 0
label: [A-Za-z0-9][A-Za-z0-9._-]{0,49}, default manual
```

**Step 4: Run configuration tests**

```bash
bash tests/scripts/test_backup_nas.sh config
```

Expected: PASS.

**Step 5: Commit**

```bash
git add .gitignore .nas-backup.env.example scripts/backup-nas.sh tests/scripts/test_backup_nas.sh
git commit -m "feat(ops): load dedicated NAS backup config"
```

### Task 2: Implement NAS-side preflight, locking, and atomic workspace

**Files:**
- Create: `scripts/backup-nas-remote.sh`
- Modify: `tests/scripts/test_backup_nas.sh`

**Step 1: Write failing remote preflight tests**

Add tests that execute the remote script locally against a temporary fake NAS root with fake commands first in `PATH`. Cover:

- invalid relative root/DB/backup paths fail;
- missing source DB or repository fails;
- missing `sqlite3`, `git`, `docker`, `tar`, or `sha256sum` fails before creating a final backup;
- a pre-existing lock causes immediate failure;
- the script removes only its own lock through `trap`;
- a `.partial` directory is used;
- a failed run removes its own partial directory;
- an existing successful directory is never changed;
- insufficient `df` capacity fails before SQLite backup;
- directories start with mode `0700`.

Run:

```bash
bash tests/scripts/test_backup_nas.sh remote-preflight
```

Expected: FAIL because the remote worker does not exist.

**Step 2: Implement argument and dependency validation**

The remote worker accepts exactly five positional arguments:

```text
NAS root
database path
backup root
label
retention count
```

Set:

```bash
set -euo pipefail
PATH="/usr/local/bin:/usr/bin:/bin:${PATH:-}"
umask 077
```

Revalidate every argument on the NAS even though the local driver already validated it.

**Step 3: Implement the lock and workspace lifecycle**

Use:

```text
<backup-root>/.relive-backup.lock/
<backup-root>/.YYYY-MM-DD-HHMMSS-label.partial/
<backup-root>/YYYY-MM-DD-HHMMSS-label/
```

Acquire the lock with atomic `mkdir`. Never auto-delete a pre-existing lock. A trap removes only the lock and partial directory created by the current run. Refuse to overwrite any final directory.

**Step 4: Add disk-space preflight**

Calculate the live DB byte size and available bytes under the backup filesystem. Require at least:

```text
database size + max(20% of database size, 256 MiB)
```

Do not use broad filesystem scans.

**Step 5: Run remote preflight tests**

```bash
bash tests/scripts/test_backup_nas.sh remote-preflight
```

Expected: PASS.

**Step 6: Commit**

```bash
git add scripts/backup-nas-remote.sh tests/scripts/test_backup_nas.sh
git commit -m "feat(ops): prepare atomic NAS backup workspace"
```

### Task 3: Create and verify the SQLite online backup

**Files:**
- Modify: `scripts/backup-nas-remote.sh`
- Modify: `tests/scripts/test_backup_nas.sh`

**Step 1: Write failing SQLite tests**

Cover:

- the worker invokes SQLite `.backup` against the configured live DB;
- it never uses `cp` on the live DB, WAL, or SHM files;
- backup output is named `relive.db`;
- `PRAGMA quick_check` must return exactly one trimmed line `ok`;
- empty, multiple, warning-prefixed, or non-`ok` quick-check output fails;
- quick-check runs against the backup, not the live DB;
- `schema.sql` is exported from the backup;
- SQLite failure removes the partial directory and leaves existing backups intact.

Run:

```bash
bash tests/scripts/test_backup_nas.sh sqlite
```

Expected: FAIL.

**Step 2: Implement safe `.backup` quoting**

Because paths are restricted to a conservative character set, construct a SQLite command file inside the protected partial directory:

```text
.timeout 60000
.backup '<partial>/relive.db'
```

Run it with:

```bash
sqlite3 "$db_path" < "$command_file"
```

Remove the command file before checksum generation.

**Step 3: Implement integrity and schema verification**

Run:

```bash
sqlite3 -readonly "$partial/relive.db" 'PRAGMA quick_check;'
sqlite3 -readonly "$partial/relive.db" '.schema'
```

Require quick-check `ok` and non-empty `schema.sql`. Do not run `VACUUM`, migration, or repair statements.

**Step 4: Run SQLite tests**

```bash
bash tests/scripts/test_backup_nas.sh sqlite
```

Expected: PASS.

**Step 5: Commit**

```bash
git add scripts/backup-nas-remote.sh tests/scripts/test_backup_nas.sh
git commit -m "feat(ops): create verified SQLite online backups"
```

### Task 4: Capture the protected configuration and repository state

**Files:**
- Modify: `scripts/backup-nas-remote.sh`
- Modify: `tests/scripts/test_backup_nas.sh`

**Step 1: Write failing bundle-content tests**

Cover:

- `config.tar.gz` contains only existing files from the allowlist;
- `.nas-backup.env`, DB/WAL/SHM, logs, thumbnails, and arbitrary extra files are excluded;
- `repository.bundle` is created with `git bundle create --all`;
- `git-status.txt` contains HEAD, branch, and porcelain status but no diff;
- runtime output contains container name/image/health/start/restart and Compose status;
- runtime output does not contain environment variables, tokens, secrets, mount contents, or full inspect JSON;
- missing optional compose/config files do not fail the backup;
- missing Git repository or bundle verification failure does fail.

Run:

```bash
bash tests/scripts/test_backup_nas.sh bundle
```

Expected: FAIL.

**Step 2: Implement the allowlisted config archive**

From the configured NAS root, add only files that exist:

```text
.env
backend/config.prod.yaml
docker-compose.yml
docker-compose.prod.yml
VERSION
```

Fail if neither `.env` nor `backend/config.prod.yaml` exists because the result would not be operationally useful. Never print archive contents to stdout.

**Step 3: Implement Git bundle and status capture**

Use the configured root with `safe.directory` scoped to that command. Run `git bundle verify` before publication. Store only sanitized command output in the manifest; do not include patches.

**Step 4: Implement sanitized runtime capture**

Use explicit `docker ps` and `docker inspect -f` format strings for `relive` and `relive-ml`. Do not call unformatted `docker inspect`. If a container is absent, record `missing` without failing the database backup.

**Step 5: Run bundle tests**

```bash
bash tests/scripts/test_backup_nas.sh bundle
```

Expected: PASS.

**Step 6: Commit**

```bash
git add scripts/backup-nas-remote.sh tests/scripts/test_backup_nas.sh
git commit -m "feat(ops): capture protected Relive backup metadata"
```

### Task 5: Add manifest, permissions, checksums, and manual restore notes

**Files:**
- Modify: `scripts/backup-nas-remote.sh`
- Modify: `tests/scripts/test_backup_nas.sh`

**Step 1: Write failing publication tests**

Cover:

- bundle directories are `0700` and regular files `0600`;
- `manifest.txt` records timestamp, source paths, sizes, Git HEAD, version, and `quick_check=ok` without secret values;
- `RESTORE.txt` says Relive must be stopped and does not contain executable automatic restore behavior;
- `SHA256SUMS` covers every bundle file except itself in stable filename order;
- `sha256sum -c` is run before publication;
- checksum failure prevents final rename;
- final rename happens only after every validation;
- successful output contains only the concise completion summary.

Run:

```bash
bash tests/scripts/test_backup_nas.sh publication
```

Expected: FAIL.

**Step 2: Implement protected metadata files**

Write manifest and restore notes with fixed templates. Values inserted into them must already be validated or sanitized. Do not include `.env` values, YAML contents, Docker environment, person/face records, or SQL row data.

**Step 3: Implement permissions and checksums**

Before checksums:

```bash
find "$partial" -type d -exec chmod 700 {} +
find "$partial" -type f -exec chmod 600 {} +
```

Generate checksums from the partial directory using null-safe/stable filenames or the already restricted fixed bundle names. Verify them immediately, then atomically rename the directory.

**Step 4: Run publication tests**

```bash
bash tests/scripts/test_backup_nas.sh publication
```

Expected: PASS.

**Step 5: Commit**

```bash
git add scripts/backup-nas-remote.sh tests/scripts/test_backup_nas.sh
git commit -m "feat(ops): publish verified NAS backup bundles"
```

### Task 6: Add strict opt-in retention

**Files:**
- Modify: `scripts/backup-nas-remote.sh`
- Modify: `tests/scripts/test_backup_nas.sh`

**Step 1: Write failing retention tests**

Cover:

- keep `0` deletes nothing;
- retention executes only after a successful final rename;
- keep `N` retains the newest N matching successful directories;
- matching requires `YYYY-MM-DD-HHMMSS-<valid-label>` exactly;
- files, symlinks, `.partial`, lock, manually named directories, nested paths, and paths outside the configured root are ignored;
- retention never deletes the just-created backup;
- deletion failure reports a warning/error but leaves the new backup valid;
- invalid/negative retention fails during preflight.

Run:

```bash
bash tests/scripts/test_backup_nas.sh retention
```

Expected: FAIL.

**Step 2: Implement matching and pruning**

Use a non-recursive directory listing under the configured backup root. Reject symlinks and operate on basenames only. Sort matching names lexicographically, which matches timestamp order, and remove only entries beyond the newest N after rechecking their parent and type.

**Step 3: Run retention tests**

```bash
bash tests/scripts/test_backup_nas.sh retention
```

Expected: PASS.

**Step 4: Commit**

```bash
git add scripts/backup-nas-remote.sh tests/scripts/test_backup_nas.sh
git commit -m "feat(ops): add opt-in NAS backup retention"
```

### Task 7: Wire the SSH driver and Make command

**Files:**
- Modify: `scripts/backup-nas.sh`
- Modify: `tests/scripts/test_backup_nas.sh`
- Modify: `Makefile`
- Modify: `tests/scripts/test_script_consistency.sh`

**Step 1: Write failing transport and Make tests**

Use a fake `ssh` executable to capture arguments and stdin. Cover:

- SSH uses `BatchMode=yes` and a finite connection timeout;
- `--` separates SSH options from the validated host where supported;
- remote script bytes are sent on stdin;
- the five validated arguments arrive in the documented order;
- no config contents or secret values are printed;
- SSH failure returns non-zero and does not claim completion;
- `make backup-nas` invokes `./scripts/backup-nas.sh`;
- `make help` advertises the command;
- `deploy.sh` and `deploy-image.sh` do not invoke backup automatically.

Run:

```bash
bash tests/scripts/test_backup_nas.sh transport
bash tests/scripts/test_script_consistency.sh
```

Expected: FAIL.

**Step 2: Implement non-interactive SSH execution**

Validate connectivity before streaming the worker. Quote each remote argument with a dedicated shell-quoting helper; never concatenate raw config into a command. The driver should pass remote output through unchanged only after ensuring the worker emits sanitized output.

**Step 3: Add the Make target**

Add `backup-nas` to `.PHONY`, help, and:

```make
backup-nas:
	./scripts/backup-nas.sh
```

Do not add it as a dependency of deployment targets.

**Step 4: Run transport and consistency tests**

```bash
bash tests/scripts/test_backup_nas.sh transport
bash tests/scripts/test_script_consistency.sh
```

Expected: PASS.

**Step 5: Commit**

```bash
git add scripts/backup-nas.sh tests/scripts/test_backup_nas.sh Makefile tests/scripts/test_script_consistency.sh
git commit -m "feat(ops): expose NAS backup over SSH"
```

### Task 8: Document setup, operation, and recovery boundaries

**Files:**
- Create: `docs/NAS_BACKUP.md`
- Modify: `README.md`
- Modify: `docs/QUICK_REFERENCE.md`
- Modify: `tests/scripts/test_backup_nas.sh`

**Step 1: Write failing documentation assertions**

Assert documentation includes:

- copying `.nas-backup.env.example` to `.nas-backup.env`;
- every supported variable and precedence;
- `make backup-nas` and labeled examples;
- bundle contents and permissions;
- default retention `0` semantics;
- SQLite `.backup` and quick-check guarantees;
- no service restart/download/automatic restore;
- manual restore prerequisites and verification commands;
- troubleshooting for SSH, lock, disk space, quick-check, and checksum failure.

Run:

```bash
bash tests/scripts/test_backup_nas.sh docs
```

Expected: FAIL.

**Step 2: Write the operational documentation**

Keep the README addition short and link to `docs/NAS_BACKUP.md`. The detailed document must use placeholders only and warn that `config.tar.gz` contains secrets.

**Step 3: Run documentation and shell syntax checks**

```bash
bash tests/scripts/test_backup_nas.sh docs
bash -n scripts/backup-nas.sh scripts/backup-nas-remote.sh tests/scripts/test_backup_nas.sh
git diff --check
```

Expected: PASS.

**Step 4: Commit**

```bash
git add docs/NAS_BACKUP.md README.md docs/QUICK_REFERENCE.md tests/scripts/test_backup_nas.sh
git commit -m "docs(ops): document verified NAS backups"
```

### Task 9: Run full verification and a read-only NAS preflight

**Files:**
- Modify only if verification reveals an issue.

**Step 1: Run the complete backup test suite**

```bash
bash tests/scripts/test_backup_nas.sh
bash tests/scripts/test_script_consistency.sh
```

Expected: PASS.

**Step 2: Run shell static checks**

```bash
bash -n scripts/backup-nas.sh scripts/backup-nas-remote.sh tests/scripts/test_backup_nas.sh
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck scripts/backup-nas.sh scripts/backup-nas-remote.sh tests/scripts/test_backup_nas.sh
fi
```

Expected: syntax PASS; shellcheck PASS when installed.

**Step 3: Run existing repository checks**

```bash
make test
cd frontend && npm run build
```

Expected: PASS.

**Step 4: Perform NAS preflight without creating a backup**

Use SSH manually/read-only to confirm the configured database, backup root parent, `sqlite3`, `docker`, `git`, `tar`, `sha256sum`, and available space exist. Do not invoke `make backup-nas` until the user explicitly authorizes creating a real backup.

Expected: all dependencies and paths resolve; no NAS state changes.

**Step 5: Review scope**

```bash
git status --short
git diff --stat
git log --oneline main..HEAD
```

Confirm no `.nas-backup.env`, database, backup bundle, secret, or unrelated Task 14 file is staged.

**Step 6: Commit only verification fixes if needed**

```bash
git add <verified-fix-files>
git commit -m "fix(ops): address NAS backup verification findings"
```
