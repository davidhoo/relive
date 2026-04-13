# People Worker Offline Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** Add an offline `people-worker` that runs on a Mac, downloads photos from the NAS backend, performs face detection and embedding extraction via local `relive-ml`, and submits detection results back to the NAS for storage, thumbnail generation, and clustering.

**Architecture:** Keep the NAS backend authoritative for `people_jobs`, `faces`, `people`, thumbnails, and clustering. Introduce a remote-worker lease protocol for people jobs, refactor `peopleService` so local and remote modes share the same result-application path, and add a standalone `people-worker` CLI that mirrors analyzer-style task fetch, heartbeat, release, and submit loops.

**Tech Stack:** Go, Gin, GORM, SQLite, existing auth middleware, existing analyzer client/task patterns, local `relive-ml` over HTTP, Makefile build targets, markdown docs.

---

## Phase 0: Prerequisite Bug Fixes

### Task 1: Fix Ghost Person Bug (processJob zero-faces path)

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: Write the failing test**

Add a test that reproduces the ghost person:

```go
func TestPeopleService_ProcessJobZeroFaces_CleansUpPerson(t *testing.T) {}
```

Setup: create a photo with existing faces assigned to a person → trigger processJob where ML returns 0 faces → assert that the person's `face_count`/`photo_count` are refreshed (or person is deleted if no remaining faces).

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_ProcessJobZeroFaces'`

Expected: FAIL because `syncPersonState` is not called in the zero-faces path.

**Step 3: Write minimal fix**

In `processJob`:
1. Move `previousPersonIDs := personIDsFromFaces(existingFaces)` to **before** the zero-faces check (before line 683)
2. In the zero-faces branch (after marking job completed, before return), add:
   ```go
   for _, pid := range previousPersonIDs {
       s.syncPersonState(pid)
   }
   ```
3. Also audit error-return paths (lines 706-753) for the same issue — if faces have been deleted in a transaction but an error occurs before `syncPersonState`, add cleanup there too.

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_ProcessJobZeroFaces'`

Expected: PASS

**Step 5: Commit**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_service_test.go
git commit -m "fix: syncPersonState in processJob zero-faces path to prevent ghost persons"
```

### Task 2: Raise Clustering Thresholds and Make Configurable

**Files:**
- Modify: `backend/internal/service/people_service.go` (constants → config-driven)
- Modify: `backend/pkg/config/config.go` (add threshold fields to PeopleConfig)
- Modify: `backend/config.dev.yaml`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: Write the failing test**

```go
func TestPeopleService_ClusteringUsesConfigThresholds(t *testing.T) {}
```

Test that clustering respects config values rather than hardcoded constants.

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_ClusteringUsesConfig'`

Expected: FAIL because thresholds are hardcoded constants.

**Step 3: Write minimal implementation**

1. Add to `PeopleConfig`:
   ```go
   LinkThreshold   float64 `yaml:"link_threshold" mapstructure:"link_threshold"`
   AttachThreshold float64 `yaml:"attach_threshold" mapstructure:"attach_threshold"`
   ```

2. In `peopleService`, read thresholds from config with defaults:
   - `link_threshold`: default `0.65` (up from hardcoded 0.35)
   - `attach_threshold`: default `0.70` (up from hardcoded 0.50)

3. Replace all uses of `peopleLinkThreshold` / `peopleAttachThreshold` constants with config-driven values.

4. Update `config.dev.yaml` with the new defaults:
   ```yaml
   people:
     ml_endpoint: "http://localhost:5050"
     timeout: 30
     link_threshold: 0.65
     attach_threshold: 0.70
   ```

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_ClusteringUsesConfig'`

Then run all people tests: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService'`

Expected: PASS

**Step 5: Commit**

```bash
git add backend/internal/service/people_service.go backend/pkg/config/config.go backend/config.dev.yaml backend/internal/service/people_service_test.go
git commit -m "fix: raise clustering thresholds (0.35→0.65, 0.50→0.70) and make configurable"
```

### Task 3: Add EnqueueUnprocessed API and Decouple Scan Trigger

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/router/router.go`
- Modify: `frontend/src/api/people.ts`
- Modify: `frontend/src/views/People/index.vue`

**Step 1: Add `EnqueueUnprocessed` service method**

```go
func (s *peopleService) EnqueueUnprocessed() (int, error) {
    photos, err := s.photoRepo.ListByFaceStatus("none")
    // enqueue each with source="manual", priority=80
}
```

Also add `ListByFaceStatus` to photo repository if not exists.

**Step 2: Add API endpoint**

```go
POST /api/v1/people/enqueue-unprocessed
```

Under JWT auth (same as other people management endpoints). Returns `{ "enqueued": 1234 }`.

Note: This endpoint only enqueues jobs. It does NOT start the background processor — that's the caller's responsibility (click "Start" button or run remote worker).

**Step 3: Add frontend button**

In `People/index.vue`, add a "检测未处理照片" button next to the existing "全量重建" button. On click:
1. Call `POST /api/v1/people/enqueue-unprocessed`
2. Show toast: "已入队 N 张照片"
3. Do not auto-start background processing (user decides to use local or remote worker)

**Step 4: Decouple RescanByPath**

Modify `RescanByPath` handler to only enqueue jobs, remove the implicit `StartBackground()` call. The user can start processing separately.

**Step 5: Verify and commit**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService'`
Run: `cd frontend && npx vue-tsc --noEmit`

```bash
git add backend/internal/service/people_service.go backend/internal/api/v1/handler/people_handler.go backend/internal/api/v1/router/router.go frontend/src/api/people.ts frontend/src/views/People/index.vue
git commit -m "feat: add EnqueueUnprocessed API, decouple scan trigger from execution"
```

---

## Phase 1: Backend Infrastructure

### Task 4: Add People Worker DTOs and Job Lease Fields

**Files:**
- Modify: `backend/internal/model/people_job.go`
- Create: `backend/internal/model/people_worker.go`

**Step 1: Add lease fields to `PeopleJob`**

Add remote worker lease fields to the `PeopleJob` struct:

```go
WorkerID        string     `json:"worker_id,omitempty" gorm:"type:varchar(100);index"`
LockExpiresAt   *time.Time `json:"lock_expires_at,omitempty" gorm:"index"`
LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
Progress        int        `json:"progress" gorm:"default:0"`
StatusMessage   string     `json:"status_message,omitempty" gorm:"type:text"`
```

Key constraint: local processing tasks have `worker_id = ""` (empty), remote tasks have a non-empty `worker_id`.

**Step 2: Create worker DTOs in `people_worker.go`**

All ID fields use `uint` to match `PeopleJob`:

```go
type PeopleWorkerTask struct {
	ID            uint       `json:"id"`
	JobID         uint       `json:"job_id"`
	PhotoID       uint       `json:"photo_id"`
	FilePath      string     `json:"file_path"`
	DownloadURL   string     `json:"download_url"`
	Width         int        `json:"width"`
	Height        int        `json:"height"`
	LockExpiresAt *time.Time `json:"lock_expires_at,omitempty"`
}
```

Also add: `PeopleWorkerTasksResponse`, `PeopleWorkerHeartbeatRequest/Response`, `PeopleWorkerReleaseTaskRequest`, `PeopleDetectionFace` (fields align with `mlclient.DetectedFace`), `PeopleDetectionResult`, `PeopleWorkerSubmitResultsRequest/Response`.

**Step 3: Verify compilation**

Run: `cd backend && go build ./...`

Expected: PASS

**Step 4: Commit**

```bash
git add backend/internal/model/people_job.go backend/internal/model/people_worker.go
git commit -m "feat: add people worker job lease fields and DTOs"
```

### Task 5: Add Remote Lease Semantics to `PeopleJobRepository`

**Files:**
- Modify: `backend/internal/repository/people_job_repo.go`
- Create: `backend/internal/repository/people_job_repo_remote_test.go`

**Step 1: Write the failing test**

Add repository tests for:

- remote claim returns highest-priority available jobs
- remote claim can reclaim expired remote processing jobs (where `worker_id != "" AND lock_expires_at < NOW()`)
- remote claim must NOT claim local processing jobs (`worker_id = ""`)
- wrong worker cannot heartbeat or release
- complete only succeeds for the current worker

Suggested test names:

```go
func TestPeopleJobRepository_ClaimNextRemote(t *testing.T) {}
func TestPeopleJobRepository_ClaimNextRemoteSkipsLocalProcessing(t *testing.T) {}
func TestPeopleJobRepository_ReclaimExpiredRemoteJob(t *testing.T) {}
func TestPeopleJobRepository_HeartbeatRemoteRejectsOtherWorker(t *testing.T) {}
func TestPeopleJobRepository_CompleteRemoteJob(t *testing.T) {}
```

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/repository -run 'TestPeopleJobRepository_(ClaimNextRemote|ReclaimExpired|HeartbeatRemote|CompleteRemote)'`

Expected: FAIL because the repository methods do not exist yet.

**Step 3: Write minimal implementation**

Extend the interface with methods:

```go
ClaimNextRemote(workerID string, limit int, lockUntil time.Time) ([]*model.PeopleJob, error)
HeartbeatRemote(id uint, workerID string, progress int, statusMsg string, lockUntil time.Time) error
ReleaseRemote(id uint, workerID string, reason string, retryLater bool) error
CompleteRemote(id uint, workerID string) error
```

`ClaimNextRemote` WHERE clause must be:

```sql
WHERE (status IN ('pending', 'queued'))
   OR (status = 'processing' AND worker_id != '' AND lock_expires_at < NOW())
-- NOT: worker_id = '' processing tasks (those are local backend tasks)
```

Implementation rules:

- only `pending/queued` or expired remote `processing` jobs are claimable
- local processing tasks (`worker_id = ""`) are never claimable
- heartbeat extends `lock_expires_at` and updates `progress`/`status_message`
- release: if `retryLater=true`, reset to `queued` with `worker_id=""`; otherwise increment `attempt_count`
- complete verifies `worker_id` matches

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -count=1 ./internal/repository -run 'TestPeopleJobRepository_(ClaimNextRemote|ReclaimExpired|HeartbeatRemote|CompleteRemote)'`

Expected: PASS

**Step 5: Commit**

```bash
git add backend/internal/repository/people_job_repo.go backend/internal/repository/people_job_repo_remote_test.go
git commit -m "feat: add remote people job lease repository methods"
```

### Task 6: Harden `InterruptNonTerminal` for Remote Workers

**Files:**
- Modify: `backend/internal/repository/people_job_repo.go`
- Modify: `backend/internal/repository/people_job_repo_remote_test.go`

**Step 1: Write the failing test**

Add tests:

```go
func TestPeopleJobRepository_InterruptNonTerminalPreservesRemoteJobs(t *testing.T) {}
func TestPeopleJobRepository_InterruptNonTerminalReclainsExpiredRemote(t *testing.T) {}
```

Test that `InterruptNonTerminal`:
- Cancels local `processing` jobs (`worker_id = ""`) — existing behavior
- Cancels `pending/queued` jobs — existing behavior
- Does NOT cancel remote `processing` jobs with unexpired lock (`worker_id != ""`, `lock_expires_at > NOW()`)
- Resets expired remote `processing` jobs to `queued` (not `cancelled`)

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/repository -run 'TestPeopleJobRepository_InterruptNonTerminal'`

Expected: FAIL because current `InterruptNonTerminal` cancels all non-terminal jobs indiscriminately.

**Step 3: Write minimal implementation**

Modify `InterruptNonTerminal` to:
1. Cancel `pending/queued` jobs and local `processing` jobs (`worker_id = ''`)
2. Reset expired remote `processing` jobs to `queued` (clear `worker_id`, `lock_expires_at`)
3. Leave unexpired remote `processing` jobs untouched

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -count=1 ./internal/repository -run 'TestPeopleJobRepository_InterruptNonTerminal'`

Expected: PASS

**Step 5: Commit**

```bash
git add backend/internal/repository/people_job_repo.go backend/internal/repository/people_job_repo_remote_test.go
git commit -m "fix: InterruptNonTerminal preserves active remote worker jobs"
```

### Task 7: Add People Runtime Lease and Integrate with `runBackground`

**Files:**
- Modify: `backend/internal/service/analysis_runtime_service.go` (or add people runtime methods)
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/model/analysis_runtime.go` (add `people_worker` to OwnerType CHECK)

**Step 1: Add `global_people` resource key and `people_worker` owner type**

- Add constant `PeopleRuntimeResource = "global_people"`
- Add `people_worker` to the `OwnerType` CHECK constraint in `analysis_runtime.go`
- Add people-specific runtime methods (or reuse existing with new resource key):
  - `AcquirePeopleRuntime(ownerType, ownerID)`
  - `HeartbeatPeopleRuntime(ownerType, ownerID)`
  - `ReleasePeopleRuntime(ownerType, ownerID)`

**Step 2: Integrate runtime lease with `runBackground`**

Modify `StartBackground` to acquire `global_people` lease (owner_type=`background`) before starting the processing loop. Modify `runBackground` to heartbeat the lease periodically. Modify `StopBackground` to release the lease.

This ensures that when a remote `people-worker` holds the people runtime, `StartBackground` will fail with "people runtime busy".

**Step 3: Verify**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService'`

Expected: PASS (existing tests should not break; new lease logic is additive)

**Step 4: Commit**

```bash
git add backend/internal/model/analysis_runtime.go backend/internal/service/analysis_runtime_service.go backend/internal/service/people_service.go
git commit -m "feat: add global_people runtime lease, integrate with runBackground"
```

### Task 8: Refactor `peopleService.processJob` to Reuse Result Application Logic

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify or create: `backend/internal/service/people_service_test.go`

**Step 1: Write regression tests for existing `processJob` behavior**

Before refactoring, add integration tests covering the three core paths of `processJob`:

```go
func TestPeopleService_ProcessJob_WithFaces(t *testing.T) {}
func TestPeopleService_ProcessJob_NoFaces(t *testing.T) {}
func TestPeopleService_ProcessJob_ManualLockedSkipsDetection(t *testing.T) {}
```

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_ProcessJob'`

Expected: PASS (these test the current behavior before refactoring).

**Step 2: Extract shared method**

Refactor `processJob` into three parts:

```go
// Pre-flight: check excluded/missing/manual_locked
func (s *peopleService) preflightCheck(job *model.PeopleJob, photo *model.Photo) (skip bool, err error)

// Local ML detection: resize, base64, call mlclient
func (s *peopleService) detectFacesLocally(ctx context.Context, photo *model.Photo) (*mlclient.DetectFacesResponse, error)

// Shared result application: delete old faces, create new, thumbnail, cluster, refresh
func (s *peopleService) applyDetectionResult(job *model.PeopleJob, photo *model.Photo, result *mlclient.DetectFacesResponse) error
```

Local `processJob` becomes: `preflightCheck` → `detectFacesLocally` → `applyDetectionResult`

**Step 3: Re-run regression tests**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_ProcessJob'`

Expected: PASS (refactoring should not change behavior)

**Step 4: Add tests for remote result application**

```go
func TestPeopleService_ApplyDetectionResultCreatesFaces(t *testing.T) {}
func TestPeopleService_ApplyDetectionResultMarksNoFace(t *testing.T) {}
func TestPeopleService_ApplyDetectionResultRejectsStaleWorker(t *testing.T) {}
```

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_ApplyDetectionResult'`

Expected: PASS

**Step 5: Commit**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_service_test.go
git commit -m "refactor: extract applyDetectionResult for local/remote shared path"
```

## Phase 2: Worker API Endpoints

### Task 9: Add People Worker API Endpoints

**Files:**
- Modify: `backend/internal/api/v1/router/router.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Create: `backend/internal/api/v1/handler/people_worker_handler_test.go`
- Modify: `backend/internal/api/v1/handler/handlers.go`

**Step 1: Write the failing test**

Add API handler tests for:

- `GET /api/v1/people/worker/tasks` — returns tasks with `download_url` from existing `/api/v1/photos/:id/file`
- `POST /api/v1/people/worker/tasks/:task_id/heartbeat`
- `POST /api/v1/people/worker/tasks/:task_id/release`
- `POST /api/v1/people/worker/results` — calls `applyDetectionResult`
- `POST /api/v1/people/runtime/acquire` — people runtime lease
- `POST /api/v1/people/runtime/heartbeat`
- `POST /api/v1/people/runtime/release`

Test:

- API key auth required (not JWT)
- task payload includes correctly formed `download_url`
- stale worker cannot heartbeat or submit
- tasks with `manual_locked` faces are not returned

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/api/v1/handler -run 'TestPeopleWorker'`

Expected: FAIL because the endpoints do not exist yet.

**Step 3: Write minimal implementation**

Add endpoints under API-key auth (same pattern as analyzer):

```go
// People worker endpoints (API key auth)
peopleWorker := v1.Group("/people/worker")
peopleWorker.Use(middleware.APIKeyAuth(services.Device))
{
    peopleWorker.GET("/tasks", handlers.People.GetWorkerTasks)
    peopleWorker.POST("/tasks/:task_id/heartbeat", handlers.People.HeartbeatWorkerTask)
    peopleWorker.POST("/tasks/:task_id/release", handlers.People.ReleaseWorkerTask)
    peopleWorker.POST("/results", handlers.People.SubmitWorkerResults)
}

// People runtime lease (API key auth)
peopleRuntime := v1.Group("/people/runtime")
peopleRuntime.Use(middleware.APIKeyAuth(services.Device))
{
    peopleRuntime.POST("/acquire", handlers.People.AcquirePeopleRuntime)
    peopleRuntime.POST("/heartbeat", handlers.People.HeartbeatPeopleRuntime)
    peopleRuntime.POST("/release", handlers.People.ReleasePeopleRuntime)
}
```

Handler implementation notes:
- `GetWorkerTasks`: filter out tasks where photo has `manual_locked` faces; build `download_url` as `{scheme}://{host}/api/v1/photos/{photo_id}/file`
- `SubmitWorkerResults`: convert `PeopleDetectionFace` → `mlclient.DetectedFace`, call `applyDetectionResult`
- Runtime handlers delegate to `analysis_runtime_service` with `global_people` resource key

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -count=1 ./internal/api/v1/handler -run 'TestPeopleWorker'`

Expected: PASS

**Step 5: Commit**

```bash
git add backend/internal/api/v1/router/router.go backend/internal/api/v1/handler/people_handler.go backend/internal/api/v1/handler/handlers.go backend/internal/api/v1/handler/people_worker_handler_test.go
git commit -m "feat: add people worker API endpoints with API key auth"
```

## Phase 3: People Worker CLI

### Task 10: Add `people-worker` CLI — Config and API Client

**Files:**
- Create: `backend/cmd/relive-people-worker/internal/config/config.go`
- Create: `backend/cmd/relive-people-worker/internal/client/api_client.go`
- Create: `backend/cmd/relive-people-worker/internal/client/api_client_test.go`
- Create: `backend/cmd/relive-people-worker/internal/client/task_manager.go`

**Step 1: Write the failing test**

Mirror analyzer client tests:

```go
func TestAPIClient_GetPeopleTasks(t *testing.T) {}
func TestAPIClient_PeopleHeartbeat(t *testing.T) {}
func TestAPIClient_PeopleReleaseTask(t *testing.T) {}
func TestAPIClient_SubmitPeopleResults(t *testing.T) {}
```

Use `httptest.NewServer` to mock NAS API responses.

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./cmd/relive-people-worker/internal/client`

Expected: FAIL because the package does not exist yet.

**Step 3: Write minimal implementation**

Config structure:

```yaml
server:
  endpoint: "http://nas:8080"
  api_key: "sk-..."
  timeout: 30

people_worker:
  worker_id: "mac-m4"
  workers: 4          # M4 Mac recommended
  fetch_limit: 10
  retry_count: 3
  retry_delay: 5

ml:
  endpoint: "http://localhost:5050"
  timeout: 15

download:
  temp_dir: "/tmp/relive-people-worker"
  timeout: 30
  max_concurrent: 4

logging:
  level: "info"
  console: true
```

API client mirrors analyzer's `api_client.go` pattern: retry, auth headers, error handling.

Task manager mirrors analyzer: local task queue, per-task heartbeat goroutines.

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -count=1 ./cmd/relive-people-worker/internal/client`

Expected: PASS

**Step 5: Commit**

```bash
git add backend/cmd/relive-people-worker/internal/
git commit -m "feat: add people worker config and API client"
```

### Task 11: Add `people-worker` CLI — Worker Loop and Entry Point

**Files:**
- Create: `backend/cmd/relive-people-worker/main.go`
- Create: `backend/cmd/relive-people-worker/worker_factory.go`
- Create: `backend/cmd/relive-people-worker/internal/download/downloader.go`
- Create: `backend/cmd/relive-people-worker/internal/worker/api_worker.go`

**Step 1: Implement worker loop**

Worker loop follows analyzer pattern:

1. Acquire `global_people` runtime lease
2. Start runtime heartbeat loop (every 10s)
3. Start fetchLoop: poll `GET /people/worker/tasks?limit=N` when local queue is low
4. Start processLoop with `workers` goroutines, each:
   - Dequeue task from local queue
   - Start per-task heartbeat
   - Download photo via `download_url` (API key in header)
   - Compress/resize with `util.NewImageProcessor(1024, 85)`
   - Base64 encode and call local `relive-ml` via `mlclient.Client`
   - Submit result to `POST /people/worker/results`
   - On failure: release task with `retry_later=true`
5. Graceful shutdown on SIGINT/SIGTERM: stop fetch, drain queue, release runtime

CLI commands:
- `people-worker run -config people-worker.yaml` — main worker loop
- `people-worker check -config people-worker.yaml` — test NAS + ML connectivity
- `people-worker gen-config` — output sample YAML
- `people-worker version` — show version

**Step 2: Verify build**

Run: `cd backend && go build -o bin/relive-people-worker ./cmd/relive-people-worker`

Expected: binary builds successfully.

**Step 3: Commit**

```bash
git add backend/cmd/relive-people-worker/main.go backend/cmd/relive-people-worker/worker_factory.go backend/cmd/relive-people-worker/internal/download/ backend/cmd/relive-people-worker/internal/worker/
git commit -m "feat: add people worker CLI with run/check/gen-config commands"
```

## Phase 4: Docs and Verification

### Task 12: Add Build Targets and User Docs

**Files:**
- Modify: `Makefile`
- Modify: `backend/Makefile`
- Create: `people-worker.yaml.example`
- Create: `docs/PEOPLE_WORKER_API_MODE.md`
- Modify: `README.md`
- Modify: `docs/QUICK_REFERENCE.md`

**Step 1: Add Makefile targets**

Root `Makefile`:
- `build-people-worker`: build the CLI binary

Backend `Makefile`:
- `build-people-worker`: `go build -o bin/relive-people-worker ./cmd/relive-people-worker`

**Step 2: Add sample config**

Create `people-worker.yaml.example` with documented fields and M4 Mac recommended values.

**Step 3: Add usage documentation**

Create `docs/PEOPLE_WORKER_API_MODE.md` documenting:
- Prerequisites (API key, relive-ml running on Mac)
- Config file setup
- Running: `./relive-people-worker check`, then `./relive-people-worker run`
- Monitoring progress via NAS web UI
- Troubleshooting: NAS restart, network interruption, ML service down

Update `README.md` and `docs/QUICK_REFERENCE.md` with people-worker references.

**Step 4: Verify**

Run:

```bash
make help | rg "build-people-worker"
cd backend && make build-people-worker
```

Expected: matches found and binary builds successfully.

**Step 5: Commit**

```bash
git add Makefile backend/Makefile people-worker.yaml.example docs/PEOPLE_WORKER_API_MODE.md README.md docs/QUICK_REFERENCE.md
git commit -m "docs: add people worker build targets and usage docs"
```

### Task 13: End-to-End Recovery Tests

**Files:**
- Modify: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/repository/people_job_repo_remote_test.go`

**Step 1: Write integration tests for recovery scenarios**

```go
// Repository level
func TestPeopleJobRepository_ExpiredRemoteLockBecomesClaimable(t *testing.T) {}
func TestPeopleJobRepository_NASRestartPreservesActiveRemoteJobs(t *testing.T) {}

// Service level
func TestPeopleService_StaleWorkerResultRejectedAfterReclaim(t *testing.T) {}
func TestPeopleService_EmptyFaceListCompletesSuccessfully(t *testing.T) {}
func TestPeopleService_DuplicateSubmissionIsIdempotent(t *testing.T) {}
```

**Step 2: Run tests**

Run:

```bash
cd backend && go test -count=1 ./internal/repository -run 'TestPeopleJobRepository_(ExpiredRemoteLock|NASRestart)'
cd backend && go test -count=1 ./internal/service -run 'TestPeopleService_(StaleWorker|EmptyFaceList|DuplicateSubmission)'
```

Expected: PASS

**Step 3: Run full test suite**

Run: `cd backend && go test -count=1 ./...`

Expected: PASS (no regressions)

**Step 4: Commit**

```bash
git add backend/internal/repository/people_job_repo_remote_test.go backend/internal/service/people_service_test.go
git commit -m "test: cover people worker recovery and idempotency flows"
```

---

## Manual End-to-End Verification Checklist

After all tasks are complete, manually verify:

### Phase 0 Verification
- [ ] 重新扫描一张有人脸照片（ML 返回 0 脸场景），确认关联 person 的 face_count 被正确刷新或 person 被删除
- [ ] 调整 `link_threshold` 为 0.65 后重建人物，确认不再出现几百张无关人脸聚成一个人物
- [ ] 人物管理页"检测未处理照片"按钮正常入队，不自动启动后台处理
- [ ] "人物重扫"按钮只入队不自动启动后台

### Phase 1-3 Verification
- [ ] NAS 启 backend，Mac 跑 `people-worker check` 确认连通
- [ ] Mac 跑 `people-worker run`，处理 1 张有人脸照片
- [ ] 处理 1 张无人脸照片，确认 `no_face` 状态
- [ ] 强制退出 worker (Ctrl+C)，确认 graceful shutdown 释放租约
- [ ] 强制 kill worker (kill -9)，确认任务锁过期后自动回收
- [ ] NAS 重启后确认远程未过期任务保持不变
- [ ] 本地 `StartBackground` 和远程 worker 互斥验证
- [ ] analyzer 与 people-worker 可以同时运行
- [ ] 前端显示"远程 worker 运行中"状态
