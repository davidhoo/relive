# NAS Backup Tool Design

**Date:** 2026-07-03

**Status:** Approved

## Goal

Provide one repeatable command on the development machine:

```bash
make backup-nas
```

The command connects to the Relive NAS over SSH and creates a verified SQLite online-backup bundle without stopping or restarting containers. The tool standardizes the backup procedure previously performed manually while keeping deployment and restore as separate, explicit operations.

## Non-goals

- Do not run automatically from `deploy` or `deploy-image`.
- Do not stop, restart, pause, or mutate Relive containers.
- Do not copy the live SQLite database file with `cp`.
- Do not download the database to the development machine.
- Do not provide one-click restore or automatic rollback.
- Do not delete old backups unless retention is explicitly enabled.
- Do not print or export secret values to terminal output.

## Configuration

Use a dedicated local configuration file instead of the application `.env`:

```text
.env.backup          # real local values; ignored by Git
.env.backup.example  # documented placeholders; tracked by Git
```

The real file may contain:

```bash
RELIVE_NAS_HOST=david@example-nas
RELIVE_NAS_ROOT=/volume1/docker/relive
RELIVE_NAS_DB=/volume1/docker/relive/data/backend/relive.db
RELIVE_NAS_BACKUP_DIR=/volume1/docker/relive/backup
RELIVE_BACKUP_KEEP=0
```

Precedence, highest first:

1. Process environment variables supplied with the command.
2. Values from `.env.backup`.
3. Safe script defaults for paths and retention.

`RELIVE_NAS_HOST` has no personal tracked default. It must be supplied by the environment or `.env.backup`. SSH authentication continues to use the developer machine's normal SSH configuration and keys; passwords and private keys are never stored in the repository.

The loader accepts only documented `RELIVE_*` keys and simple `KEY=value` syntax. It must not blindly `source` arbitrary shell code from the file.

`RELIVE_BACKUP_LABEL` is normally supplied per invocation:

```bash
RELIVE_BACKUP_LABEL=pre-task14 make backup-nas
```

Labels are normalized to a conservative character set and bounded length before being passed to the NAS.

## Architecture

Add two scripts:

```text
scripts/backup-nas.sh
scripts/backup-nas-remote.sh
```

### Development-machine driver

`scripts/backup-nas.sh`:

1. Locates the repository root.
2. Loads the allowlisted `.env.backup` values.
3. Applies explicit environment overrides.
4. Validates host, paths, label, retention, and required local `ssh` command.
5. Verifies non-interactive SSH connectivity.
6. Sends `backup-nas-remote.sh` to `bash -s` over SSH with validated arguments.
7. Prints the final backup directory and verification summary returned by the NAS.

No database or configuration contents pass back through SSH. Only progress, sanitized metadata, checksums, and the final directory path are printed.

### NAS-side worker

`scripts/backup-nas-remote.sh` runs entirely on the NAS. It uses:

```text
/usr/bin/sqlite3
/usr/local/bin/docker
/usr/local/bin/git
tar
sha256sum
```

The script prepends known Synology binary paths to `PATH` but still validates every dependency before starting.

The worker creates an atomic lock directory under the backup root. A concurrent backup fails immediately with a clear message. The script never guesses that an existing lock is stale and never deletes another process's lock.

## Backup Bundle

Successful output directory:

```text
<backup-root>/YYYY-MM-DD-HHMMSS-<label>/
```

Contents:

```text
relive.db
schema.sql
config.tar.gz
repository.bundle
git-status.txt
runtime.txt
manifest.txt
SHA256SUMS
RESTORE.txt
```

### `relive.db`

Created only with SQLite's online backup command:

```sql
.backup '<temporary-path>/relive.db'
```

The worker must not copy `relive.db`, `relive.db-wal`, or `relive.db-shm` directly.

### `schema.sql`

Generated from the completed backup database rather than the live database.

### `config.tar.gz`

Includes only files that exist from this allowlist:

```text
.env
backend/config.prod.yaml
docker-compose.yml
docker-compose.prod.yml
VERSION
```

The archive contains secrets and therefore inherits file mode `0600`. `.env.backup` is deliberately excluded because it is a development-machine transport configuration, not Relive runtime state.

### `repository.bundle`

Created with `git bundle create ... --all`. It captures committed refs only. `git-status.txt` records branch, HEAD, and porcelain status but does not capture uncommitted diffs, avoiding accidental secret-bearing patch archives.

### `runtime.txt`

Contains only sanitized runtime metadata:

- hostname and backup timestamp;
- Relive container names and image identifiers;
- health, running status, start time, and restart count;
- Compose project/service status;
- repository HEAD and application version.

It must not contain Docker environment variables, full `docker inspect` output, mounted file contents, tokens, or API keys.

### `manifest.txt`

Records source paths, file sizes, SQLite verification result, Git commit, tool version, and completion timestamp. It must contain no secret values.

### `RESTORE.txt`

Provides a manual recovery checklist and prominently states that restore requires stopping Relive first. The backup tool itself never executes restore commands.

## Backup and Verification Flow

1. Validate arguments and dependencies.
2. Acquire the backup lock.
3. Confirm the source database and repository exist.
4. Check available space before writing. Require enough space for at least the live database size plus a conservative margin.
5. Create a `0700` directory whose name ends in `.partial`.
6. Run SQLite `.backup` into that directory.
7. Execute `PRAGMA quick_check` against the backup and require exactly `ok`.
8. Export schema from the backup.
9. Create the configuration archive, Git bundle, status, runtime metadata, manifest, and restore instructions.
10. Set directories to `0700` and regular files to `0600`.
11. Generate `SHA256SUMS` for every bundle file except `SHA256SUMS` itself.
12. Verify all checksums immediately.
13. Atomically rename the `.partial` directory to its final name.
14. Optionally apply retention only after the new backup is fully verified.
15. Release the lock through a trap on both success and failure.

If any step fails, the final directory is never created. The worker removes its own incomplete directory and returns non-zero. It never deletes a previously successful backup.

## Retention

Default:

```bash
RELIVE_BACKUP_KEEP=0
```

Zero means no automatic deletion.

When explicitly set to a positive integer, retention runs only after a successful new backup. It considers only directories directly under the configured backup root whose names exactly match the tool's timestamp-label pattern. It sorts by timestamp and retains the newest N. It ignores symlinks, files, `.partial` directories, manually named directories, and paths outside the configured backup root.

Retention failure reports an error but does not invalidate or remove the newly completed backup.

## Security

- `.env.backup` is ignored by Git.
- `.env.backup.example` contains placeholders only.
- SSH uses existing keys/configuration and non-interactive mode.
- All paths and labels are validated and shell-quoted.
- Runtime capture uses explicit Docker format strings instead of full inspect output.
- Config archive and database files use mode `0600`; bundle directories use `0700`.
- Terminal output never includes `.env` or YAML contents.
- The tool never invokes restore, container restart, migration, or deployment.

## Testing

Add:

```text
tests/scripts/test_backup_nas.sh
```

Tests use temporary directories and fake `ssh`, `sqlite3`, `docker`, and `git` executables. They cover:

- configuration precedence;
- missing `.env.backup`/host errors;
- rejection of unknown or executable config syntax;
- label and path validation;
- exact remote argument forwarding;
- online `.backup` use and prohibition of live DB copy;
- quick-check failure;
- checksum failure;
- partial-directory cleanup;
- lock contention;
- restrictive permissions;
- allowlisted config archive contents;
- sanitized runtime output;
- retention disabled by default;
- strict retention matching and post-success ordering;
- failure never replacing an existing successful backup.

Extend `tests/scripts/test_script_consistency.sh` to verify:

- `make help` advertises `make backup-nas`;
- the Make target invokes `scripts/backup-nas.sh`;
- deployment scripts do not invoke the backup tool automatically.

## User Interface

Makefile adds:

```text
make backup-nas
```

Expected successful output is concise:

```text
Backup complete
Directory: /volume1/docker/relive/backup/2026-07-03-103000-pre-task14
Database quick_check: ok
Checksums: verified
```

Detailed setup, environment variables, examples, bundle contents, retention behavior, and manual recovery steps are documented in `docs/NAS_BACKUP.md`.
