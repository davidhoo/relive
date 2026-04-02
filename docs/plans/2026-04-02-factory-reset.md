# Factory Reset Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace table-by-table system reset with a SQLite file deletion flow that runs on next startup and returns the system to a true factory state.

**Architecture:** Add a small `factoryreset` package responsible for reset marker management and startup cleanup. The reset endpoint will only schedule the reset and trigger process exit; startup will perform the destructive cleanup before opening the database, then reuse existing migration and default-user bootstrap logic.

**Tech Stack:** Go, Gin, GORM, SQLite, Vue 3, TypeScript, Element Plus

---

### Task 1: Add failing tests for pending-reset startup cleanup

**Files:**
- Create: `backend/internal/factoryreset/factoryreset_test.go`
- Reference: `backend/internal/api/v1/handler/system.go`

**Step 1: Write the failing test**

Add tests that:

- create a temporary SQLite path plus `-wal` and `-shm`
- create thumbnail, display-batch, and cache directories with files inside
- create a reset marker file
- call the startup cleanup entrypoint
- assert the marker is removed
- assert database files are removed
- assert managed directories still exist but are empty

Add a second test that verifies no files are removed when no marker exists.

**Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/factoryreset -run TestApplyPending -v`
Expected: FAIL because the package and entrypoint do not exist yet.

**Step 3: Write minimal implementation**

Create the new package with:

- marker path resolution from SQLite DB path
- pending-reset detection
- startup cleanup that deletes DB/WAL/SHM and clears managed directories

**Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/factoryreset -run TestApplyPending -v`
Expected: PASS

### Task 2: Add failing tests for reset endpoint scheduling and response

**Files:**
- Modify: `backend/internal/api/v1/handler/system_test.go`
- Modify: `backend/internal/api/v1/handler/system.go`

**Step 1: Write the failing test**

Add tests that:

- post `{"confirm_text":"RESET"}` to the handler
- assert a reset marker file is created
- assert the response is success
- assert the response message now describes restart semantics
- assert an injected exit callback is scheduled instead of calling real `os.Exit`

Add a second test for non-SQLite config returning an error.

**Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/api/v1/handler -run 'TestSystemHandlerReset' -v`
Expected: FAIL because the handler still performs inline DB/table cleanup and has no exit scheduling hook.

**Step 3: Write minimal implementation**

Refactor the handler to:

- call the new reset scheduler
- stop returning file-deletion detail flags tied to inline cleanup
- send a factory-reset scheduled response
- invoke an injected delayed-exit hook after the response is written

**Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/api/v1/handler -run 'TestSystemHandlerReset' -v`
Expected: PASS

### Task 3: Run pending reset before opening the database

**Files:**
- Modify: `backend/cmd/relive/main.go`
- Reference: `backend/pkg/database/database.go`

**Step 1: Write the failing test**

Rely on the `factoryreset` package tests from Task 1 and add one assertion if needed for the public startup entrypoint.

**Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/factoryreset ./internal/api/v1/handler -v`
Expected: FAIL until startup wires the new cleanup phase correctly.

**Step 3: Write minimal implementation**

In `main.go`, after config and logger initialization but before `database.Init`:

- check for a pending factory reset
- apply cleanup if present
- abort startup if cleanup fails

Keep the existing DB migration and service startup sequence unchanged after cleanup.

**Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/factoryreset ./internal/api/v1/handler -v`
Expected: PASS

### Task 4: Align API and frontend reset messaging with restart semantics

**Files:**
- Modify: `backend/internal/model/dto.go`
- Modify: `frontend/src/types/system.ts`
- Modify: `frontend/src/views/System/index.vue`
- Modify: `frontend/src/api/system.ts`

**Step 1: Write the failing test**

Use existing TypeScript type-check/build validation as the regression gate.

**Step 2: Run test to verify it fails**

Run: `cd frontend && npm run build`
Expected: FAIL if API fields or text flow are not aligned.

**Step 3: Write minimal implementation**

Update response typing and UI copy so that:

- success text says the system is being reset and restarted
- frontend clears local auth state even if logout request fails during restart
- no UI still claims immediate in-process cleanup happened

**Step 4: Run test to verify it passes**

Run: `cd frontend && npm run build`
Expected: PASS

### Task 5: Verify the full factory-reset path

**Files:**
- No file changes required

**Step 1: Run backend tests**

Run: `cd backend && go test ./internal/factoryreset ./internal/api/v1/handler -v`
Expected: PASS

**Step 2: Run focused frontend verification**

Run: `cd frontend && npm run build`
Expected: PASS

**Step 3: Run broader backend regression if needed**

Run: `cd backend && go test ./...`
Expected: PASS or clear note of unrelated failures
