# Graceful Restart Draining Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** Add low-cost graceful restart support with draining/readiness semantics and orderly shutdown of in-memory background runtimes.

**Architecture:** Introduce a lightweight process lifecycle state shared by `main.go` and `SystemHandler`, keep `/health` as liveness, add a readiness endpoint that flips during shutdown, and extend the shutdown sequence to stop AI background analysis, event clustering, and people feedback scheduling before HTTP shutdown. Do not convert these runtimes into durable DB-backed tasks.

**Tech Stack:** Go, Gin, GORM, SQLite, Docker Compose

---

### Task 1: Add failing readiness handler coverage

**Files:**
- Modify: `backend/internal/api/v1/handler/system_test.go`
- Modify: `backend/internal/api/v1/handler/system.go`
- Modify: `backend/internal/api/v1/router/router.go`
- Create or Modify: lifecycle state file under `backend/internal/`

**Step 1: Write the failing tests**

Add handler tests for:

- `GET /api/v1/system/readiness` returns `200` when not draining
- `GET /api/v1/system/readiness` returns `503` when draining
- `GET /api/v1/system/health` still returns `200` while draining if DB ping succeeds

**Step 2: Run test to verify it fails**

Run:
```bash
cd backend && go test -count=1 ./internal/api/v1/handler -run 'TestSystemHandler(Readiness|Health)'
```

Expected: FAIL because readiness endpoint and lifecycle state do not exist yet.

**Step 3: Write minimal implementation**

- Add a lightweight lifecycle state holder
- Inject it into `SystemHandler`
- Implement `Readiness`
- Register `/api/v1/system/readiness`

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

### Task 2: Add failing coverage for people feedback shutdown behavior

**Files:**
- Modify: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/service/people_service.go`

**Step 1: Write the failing test**

Add a service test proving:

- scheduling feedback recluster while `backgroundBusy=true` keeps it pending
- calling `HandleShutdown()` before the loop runs prevents a later recluster execution

**Step 2: Run test to verify it fails**

Run:
```bash
cd backend && go test -count=1 ./internal/service -run TestPeopleService_HandleShutdownStopsPendingFeedbackRecluster
```

Expected: FAIL because `HandleShutdown()` currently does not stop feedback scheduling.

**Step 3: Write minimal implementation**

- add shutdown-aware feedback loop state
- make `HandleShutdown()` stop future feedback scheduling
- do not redesign `triggerRecluster()` into a durable task

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

### Task 3: Add failing shutdown orchestration coverage for `main`

**Files:**
- Create or Modify: `backend/cmd/relive/main_test.go`
- Modify: `backend/cmd/relive/main.go`

**Step 1: Write the failing test**

Extract small helper functions from `main.go` and test that:

- shutdown begins by marking lifecycle state as draining
- AI background analysis receives a stop request when running
- event clustering receives a stop request when running
- photo / thumbnail / geocode / people services receive `HandleShutdown()`

Use small stub interfaces local to `package main`; do not instantiate the full app.

**Step 2: Run test to verify it fails**

Run:
```bash
cd backend && go test -count=1 ./cmd/relive -run 'Test(NotifyShutdown|WaitForShutdownDrain)'
```

Expected: FAIL because the helper functions and orchestration do not exist yet.

**Step 3: Write minimal implementation**

- extract shutdown-notify and wait helpers from `main.go`
- call AI/event stop paths only when currently active
- keep behavior idempotent and best-effort

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

### Task 4: Extend runtime wait logic and timeouts

**Files:**
- Modify: `backend/cmd/relive/main.go`
- Modify: `backend/cmd/relive/main_test.go`

**Step 1: Write the failing test**

Add tests proving:

- wait logic returns when AI/event/task services reach terminal states
- timeout path does not block forever

**Step 2: Run test to verify it fails**

Run:
```bash
cd backend && go test -count=1 ./cmd/relive -run 'TestWaitForShutdownDrain'
```

Expected: FAIL because no wait helper exists yet.

**Step 3: Write minimal implementation**

- add bounded polling helpers for AI, event clustering, and task services
- increase application shutdown timeout beyond 5 seconds
- keep shutdown order: draining -> stop requests -> wait -> HTTP shutdown

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

### Task 5: Align deployment grace period

**Files:**
- Modify: `docker-compose.yml`
- Modify: `docker-compose.prod.yml.example`

**Step 1: Add a small regression check**

Use a lightweight grep-based assertion or existing script coverage to ensure:

- `relive` service has a non-trivial `stop_grace_period`

**Step 2: Run check to verify it fails**

Run:
```bash
rg -n "stop_grace_period" docker-compose.yml docker-compose.prod.yml.example
```

Expected: FAIL or incomplete coverage for the `relive` service.

**Step 3: Write minimal implementation**

- add `stop_grace_period: 60s` for `relive`
- do not change unrelated compose semantics

**Step 4: Run check to verify it passes**

Run the same command and expect PASS.

### Task 6: Broader verification

**Files:**
- No code changes expected

**Step 1: Run targeted handler tests**

Run:
```bash
cd backend && go test -count=1 ./internal/api/v1/handler -run 'TestSystemHandler(Readiness|Health)'
```

**Step 2: Run targeted service tests**

Run:
```bash
cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_(HandleShutdownStopsPendingFeedbackRecluster|FeedbackRecluster)'
```

**Step 3: Run command package tests**

Run:
```bash
cd backend && go test -count=1 ./cmd/relive -run 'Test(NotifyShutdown|WaitForShutdownDrain)'
```

**Step 4: Run broader backend verification**

Run:
```bash
cd backend && go test -count=1 ./internal/api/v1/handler ./internal/service ./cmd/relive
```

Expected: PASS
