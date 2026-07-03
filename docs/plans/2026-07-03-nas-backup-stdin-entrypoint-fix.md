# NAS Backup stdin Entrypoint Fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make the NAS backup worker start correctly when the local driver streams it to remote `bash -s`.

**Architecture:** Preserve the existing SSH transport and backup flow. Add a transport-level regression test that executes the worker through stdin, then make the dedicated remote worker invoke `main` unconditionally instead of inspecting `BASH_SOURCE`, which is unset in stdin mode.

**Tech Stack:** Bash 3.2-compatible shell scripts and the repository's shell test harness.

---

### Task 1: Cover and fix stdin execution

**Files:**
- Modify: `tests/scripts/test_backup_nas.sh`
- Modify: `scripts/backup-nas-remote.sh`

**Step 1: Write the failing test**

In the transport test group, pipe `scripts/backup-nas-remote.sh` into `bash -s --` with an invalid relative NAS root and assert that the worker reaches `main` and reports the path validation error rather than failing while reading `BASH_SOURCE[0]`.

**Step 2: Run the test to verify it fails**

Run: `bash tests/scripts/test_backup_nas.sh transport`

Expected: FAIL because stdin execution currently reports `BASH_SOURCE[0]: unbound variable`.

**Step 3: Write the minimal implementation**

Replace the remote worker's `BASH_SOURCE` direct-execution guard with:

```bash
main "$@"
```

The worker is a dedicated executable/streamed entrypoint and is not sourced by production or tests.

**Step 4: Run focused and complete verification**

Run:

```bash
bash tests/scripts/test_backup_nas.sh transport
bash tests/scripts/test_backup_nas.sh
bash -n scripts/backup-nas.sh scripts/backup-nas-remote.sh tests/scripts/test_backup_nas.sh
```

Expected: all assertions pass and Bash syntax validation exits zero.

