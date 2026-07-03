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
# Written for broad portability: avoids bash 4+ features so it runs under
# macOS bash 3.2 during local tests as well as on the NAS.

set -euo pipefail

# Synology ships some binaries under /usr/local/bin (docker, git) and
# /usr/bin. Prepend the known good paths but keep the inherited PATH too.
PATH="/usr/local/bin:/usr/bin:/bin:${PATH:-}"
export PATH

umask 077

# Placeholder — full implementation added in Task 2.
echo "backup-nas-remote: not implemented" >&2
exit 1
