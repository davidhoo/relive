# Script Consistency Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make Relive’s shell scripts consistent and more reliable without changing user-facing workflows.

**Architecture:** Centralize city auto-import logic in `init-cities.sh` and have the entrypoint delegate to it, unify `.env` generation templates with `.env.example`, and align config-path conventions across scripts and docs. Keep interactivity and existing script roles intact.

**Tech Stack:** Bash/sh, Docker/Docker Compose, Go, Make.

---

### Task 1: Add a script consistency test harness

**Files:**
- Create: `tests/scripts/test_script_consistency.sh`

**Step 1: Write the failing test**

```bash
#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

# 1) Entry point should delegate to init-cities
grep -q "/app/init-cities.sh" "$ROOT/backend/scripts/docker-entrypoint.sh" \
  || fail "docker-entrypoint.sh does not call /app/init-cities.sh"

# 2) init-cities should gate on AUTO_IMPORT_CITIES
grep -q "AUTO_IMPORT_CITIES" "$ROOT/backend/scripts/init-cities.sh" \
  || fail "init-cities.sh does not check AUTO_IMPORT_CITIES"

# 3) init-cities should check cities500.txt existence
grep -q "cities500.txt" "$ROOT/backend/scripts/init-cities.sh" \
  || fail "init-cities.sh does not check cities500.txt"

# 4) init-cities should use deleted_at filter for count
grep -q "deleted_at IS NULL" "$ROOT/backend/scripts/init-cities.sh" \
  || fail "init-cities.sh does not use deleted_at IS NULL in count query"

# 5) deploy/install env templates should not mention QWEN/OPENAI keys
if rg -n "QWEN_API_KEY|OPENAI_API_KEY" "$ROOT/deploy.sh" "$ROOT/install.sh" >/dev/null; then
  fail "deploy.sh or install.sh mentions QWEN/OPENAI API keys"
fi

echo "OK: script consistency checks passed"
```

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`  
Expected: FAIL because entrypoint does not yet call `init-cities.sh` and init-cities lacks gating.

**Step 3: Commit**

```bash
git add tests/scripts/test_script_consistency.sh
git commit -m "test: add script consistency checks"
```

---

### Task 2: Consolidate city auto-import logic in init-cities

**Files:**
- Modify: `backend/scripts/init-cities.sh`
- Modify: `backend/scripts/docker-entrypoint.sh`
- Modify: `docs/docker-geocode.md` (if it documents entrypoint logic)

**Step 1: Write the failing test**

The test from Task 1 already covers this.

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`  
Expected: FAIL (same reason).

**Step 3: Write minimal implementation**

Update `init-cities.sh` to:
- Read `AUTO_IMPORT_CITIES` (default `true`).
- Skip when auto-import is disabled.
- Check `cities500.txt` exists and provide download instructions if missing.
- Count rows using `deleted_at IS NULL` and skip if count > 0.
- Run `/app/import-cities --file ... --config ...` on first import.

Update `docker-entrypoint.sh` to:
- Call `/app/init-cities.sh` unconditionally (it will self-gate).
- Remove duplicate import logic.

Update `docs/docker-geocode.md` to reflect the delegation.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`  
Expected: OK.

**Step 5: Commit**

```bash
git add backend/scripts/init-cities.sh backend/scripts/docker-entrypoint.sh docs/docker-geocode.md
git commit -m "fix: centralize city auto-import in init-cities"
```

---

### Task 3: Align `.env` generation templates with `.env.example`

**Files:**
- Modify: `deploy.sh`
- Modify: `install.sh`
- Modify: `.env.example` (if required for ordering/comments)

**Step 1: Write the failing test**

The test from Task 1 already checks for removed API key mentions. If needed, extend it to assert expected keys like `JWT_SECRET` and `RELIVE_PORT`.

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`  
Expected: FAIL if templates still diverge.

**Step 3: Write minimal implementation**

Update the here-doc templates in `deploy.sh` and `install.sh` to match `.env.example`:
- Same keys in the same order.
- Same comments (or a minimal subset that is identical).
- No AI provider keys.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`  
Expected: OK.

**Step 5: Commit**

```bash
git add deploy.sh install.sh .env.example
git commit -m "chore: align env templates with .env.example"
```

---

### Task 4: Standardize config path usage in geonames import script

**Files:**
- Modify: `backend/import-geonames.sh`
- Modify: `docs/IMPORT_CITIES.md` or relevant docs (if they mention usage)

**Step 1: Write the failing test**

Add a new check in `tests/scripts/test_script_consistency.sh` to assert that `import-geonames.sh` accepts a config path (e.g., `--config` or a second argument).

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`  
Expected: FAIL (new check added).

**Step 3: Write minimal implementation**

Update `backend/import-geonames.sh`:
- Keep first arg as dataset.
- Accept `--config <path>` or second positional arg for config.
- Default to `backend/config.dev.yaml` when not provided.
- Pass the config to `go run cmd/import-cities/main.go --file ... --config ...`.

Update docs to show the optional config argument.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`  
Expected: OK.

**Step 5: Commit**

```bash
git add backend/import-geonames.sh docs/IMPORT_CITIES.md tests/scripts/test_script_consistency.sh
git commit -m "chore: standardize config path in geonames import script"
```

---

### Task 5: Consistent error messaging and exit behavior

**Files:**
- Modify: `dev.sh`, `deploy.sh`, `install.sh`, `test-local.sh` (only where inconsistent)

**Step 1: Write the failing test**

Extend `tests/scripts/test_script_consistency.sh` with simple `rg` checks for:
- `set -e` or `set -euo pipefail` in each script.
- Known error messages for missing config.

**Step 2: Run test to verify it fails**

Run: `bash tests/scripts/test_script_consistency.sh`  
Expected: FAIL where mismatches exist.

**Step 3: Write minimal implementation**

Update scripts to:
- Use consistent `set -e` or `set -euo pipefail`.
- Use the same error message format for missing config or dependencies.

**Step 4: Run test to verify it passes**

Run: `bash tests/scripts/test_script_consistency.sh`  
Expected: OK.

**Step 5: Commit**

```bash
git add dev.sh deploy.sh install.sh test-local.sh tests/scripts/test_script_consistency.sh
git commit -m "chore: standardize script error handling"
```

