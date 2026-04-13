# People Clustering Drain And Visibility Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** 让人物后台在没有新 `people_jobs` 时也能持续清空 `pending faces`，并在后台任务页展示聚类 backlog 与当前阶段。

**Architecture:** 复用现有 `peopleService` 后台循环，不新建独立聚类任务系统。后端通过 `FaceRepository` 暴露 pending 聚类统计并在 idle 分支主动调用 `runIncrementalClustering()`；前端继续轮询 `/people/task` 与 `/people/stats`，新增聚类 backlog 面板和阶段文案。

**Tech Stack:** Go, GORM, SQLite, Vue 3, TypeScript, Element Plus

---

### Task 1: Add Failing Backend Tests

**Files:**
- Modify: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/repository/face_repo_test.go`

**Step 1: Write the failing tests**

- Add a service test proving that when there are no queued `people_jobs` but there are `pending` faces, `StartBackground()` eventually drains clustering work and updates `clustered_at` / `cluster_status`.
- Add a service test proving `GetStats()` returns pending face backlog counts.

**Step 2: Run tests to verify they fail**

Run:

```bash
cd backend
go test ./internal/service -run 'TestPeopleService_(BackgroundDrainsPendingFacesWithoutJobs|GetStatsIncludesPendingFaceBacklog)' -count=1
```

Expected: FAIL because current background loop goes idle when no jobs exist and stats omit clustering backlog.

**Step 3: Add or adjust repository-level tests if needed**

- If new repository API is added for pending face counts, add a focused repository test first.

**Step 4: Run repository test to verify it fails**

Run:

```bash
cd backend
go test ./internal/repository -run TestFaceRepository_ -count=1
```

Expected: FAIL only for the new behavior under test.

### Task 2: Implement Backend Clustering Drain And Stats

**Files:**
- Modify: `backend/internal/repository/face_repo.go`
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/model/dto.go`

**Step 1: Add minimal repository support**

- Extend `FaceRepository` with a small pending stats query, returning:
  - total pending faces
  - pending faces with `clustered_at IS NULL`
  - pending faces with `clustered_at IS NOT NULL`

**Step 2: Update service DTOs**

- Extend `model.PeopleTask` with:
  - `current_phase`
  - `current_message`
- Extend `model.PeopleStatsResponse` with:
  - `pending_faces_total`
  - `pending_faces_never_clustered`
  - `pending_faces_retried`

**Step 3: Update `peopleService.GetStats()`**

- Merge existing `people_jobs` stats with the new pending face backlog stats.

**Step 4: Update background loop**

- In `runBackground`, when `ClaimNextJob()` returns `nil`, check pending face backlog.
- If backlog exists:
  - mark task phase as `clustering`
  - set a useful `current_message`
  - call `runIncrementalClustering()`
  - keep the worker alive instead of immediately idling
- If no backlog exists, keep current idle behavior.

**Step 5: Keep logs and counters pragmatic**

- Append short background logs for clustering-drain cycles.
- Avoid spamming logs every idle tick.

**Step 6: Run the focused tests**

Run:

```bash
cd backend
go test ./internal/service -run 'TestPeopleService_(BackgroundDrainsPendingFacesWithoutJobs|GetStatsIncludesPendingFaceBacklog)' -count=1
go test ./internal/repository -run TestFaceRepository_ -count=1
```

Expected: PASS

### Task 3: Expose Clustering Backlog In People Admin UI

**Files:**
- Modify: `frontend/src/types/people.ts`
- Modify: `frontend/src/views/People/index.vue`
- Modify: `frontend/src/views/People/peopleHelpers.ts`

**Step 1: Write the UI/type expectation**

- Update TS interfaces to match new backend fields.
- Keep API wiring unchanged unless a new endpoint becomes necessary.

**Step 2: Implement minimal UI changes**

- In the “后台任务” card, keep the current detection queue block.
- Add a second block for clustering backlog:
  - pending total
  - never clustered
  - retried pending
- Update task status/meta to reflect:
  - `detecting`
  - `clustering`
  - `idle`

**Step 3: Build-check the frontend**

Run:

```bash
cd frontend
npm run build
```

Expected: PASS

### Task 4: Full Verification

**Files:**
- No new files

**Step 1: Run backend full test suite**

```bash
cd backend
go test ./...
```

Expected: PASS

**Step 2: Run frontend build**

```bash
cd frontend
npm run build
```

Expected: PASS

**Step 3: Commit**

```bash
git add backend/internal/repository/face_repo.go \
        backend/internal/repository/face_repo_test.go \
        backend/internal/service/people_service.go \
        backend/internal/service/people_service_test.go \
        backend/internal/model/dto.go \
        frontend/src/types/people.ts \
        frontend/src/views/People/index.vue \
        frontend/src/views/People/peopleHelpers.ts \
        docs/plans/2026-04-07-people-clustering-drain-and-visibility.md
git commit -m "fix: drain pending face clustering backlog"
```
