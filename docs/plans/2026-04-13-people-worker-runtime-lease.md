# People Worker Runtime Lease Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** Make remote `relive-people-worker` instances participate in the same `global_people` runtime lease as the local backend people background task.

**Architecture:** Reuse the existing `AnalysisRuntimeService` and `analysis_runtime_leases` table instead of inventing a separate people-worker lock. The people runtime endpoints will acquire, heartbeat, and release `model.GlobalPeopleResourceKey`, and worker task fetch will reject callers that do not currently own that lease.

**Tech Stack:** Go, Gin, GORM, SQLite, existing `AnalysisRuntimeService`

---

### Task 1: Add failing handler tests for people runtime lease behavior

**Files:**
- Modify: `backend/internal/api/v1/handler/people_handler_test.go`

**Step 1: Write the failing test**

Add handler coverage for:
- `POST /api/v1/people/runtime/acquire` acquires `global_people` for `people_worker`
- a second acquire conflicts with `409`
- `POST /api/v1/people/runtime/heartbeat` requires matching owner
- `POST /api/v1/people/runtime/release` requires matching owner
- `GET /api/v1/people/worker/tasks` returns `409` when the caller has not acquired the people runtime lease

**Step 2: Run test to verify it fails**

Run:
```bash
cd backend && go test -count=1 ./internal/api/v1/handler -run 'TestPeopleHandler(AcquirePeopleRuntime|PeopleRuntimeHeartbeatRequiresOwner|PeopleRuntimeReleaseRequiresOwner|GetWorkerTasksRequiresRuntimeLease)'
```

Expected: FAIL because the handler currently returns success without using `AnalysisRuntimeService`.

### Task 2: Wire `PeopleHandler` to the shared runtime service

**Files:**
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/handler.go`

**Step 1: Write minimal implementation**

- inject `service.AnalysisRuntimeService` into `PeopleHandler`
- implement acquire/heartbeat/release through `runtimeService.Acquire/Heartbeat/Release`
- use `model.GlobalPeopleResourceKey`
- use `model.AnalysisOwnerTypePeopleWorker`
- convert runtime conflicts into `409` responses with status payload

**Step 2: Run targeted tests**

Run the same command and expect the runtime endpoint tests to pass.

### Task 3: Enforce lease ownership before worker task fetch

**Files:**
- Modify: `backend/internal/api/v1/handler/people_handler.go`

**Step 1: Write minimal implementation**

Before `ClaimNextRemote(...)`:
- load runtime status for `model.GlobalPeopleResourceKey`
- require `status.IsActive`
- require `status.OwnerType == model.AnalysisOwnerTypePeopleWorker`
- require `status.OwnerID == workerID`
- otherwise return `409`

**Step 2: Run targeted tests**

Run:
```bash
cd backend && go test -count=1 ./internal/api/v1/handler -run 'TestPeopleHandler(AcquirePeopleRuntime|PeopleRuntimeHeartbeatRequiresOwner|PeopleRuntimeReleaseRequiresOwner|GetWorkerTasksRequiresRuntimeLease)'
```

Expected: PASS

### Task 4: Run broader regression verification

**Files:**
- No code changes

**Step 1: Verify handler package**

Run:
```bash
cd backend && go test -count=1 ./internal/api/v1/handler
```

**Step 2: Verify runtime service package**

Run:
```bash
cd backend && go test -count=1 ./internal/service -run 'TestAnalysisRuntimeService'
```

Expected: PASS
