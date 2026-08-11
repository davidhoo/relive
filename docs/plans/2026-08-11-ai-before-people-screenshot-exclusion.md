# AI-First People Scanning and Screenshot Exclusion Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make automatic people detection wait for a committed AI analysis result and permanently keep screenshot-category photos out of the People pipeline while retaining them in the photo library.

**Architecture:** Persist photo-level People eligibility on `photos`, remove People dispatch from scan ingestion, and publish a post-commit analysis-completed callback from both local and offline AI result paths. `peopleService` owns enqueue, screenshot cleanup, startup reconciliation, and defense-in-depth eligibility checks for local and remote workers.

**Tech Stack:** Go, GORM, SQLite, Gin, testify.

---

### Task 1: Persist photo-level People exclusion

**Files:**
- Modify: `backend/internal/model/photo.go`
- Modify: `backend/pkg/database/database_test.go`
- Test: `backend/internal/model/photo_test.go`

**Step 1: Write the failing tests**

- Assert `Photo` exposes `PeopleExcluded` and `PeopleExclusionReason` through JSON/GORM.
- Assert `database.AutoMigrate` adds both columns and the exclusion index to an existing `photos` table.
- Assert a shared eligibility function rejects inactive, unanalyzed, explicitly excluded, and `main_category='截屏'` photos.

**Step 2: Run tests to verify RED**

Run:

```bash
cd backend
go test ./internal/model ./pkg/database -run 'Test(PhotoPeopleEligibility|AutoMigrateAddsPhotoPeopleExclusion)' -count=1
```

Expected: compilation or assertion failure because the fields and eligibility function do not exist.

**Step 3: Implement the minimal model and migration behavior**

- Add `PeopleExcluded bool` and `PeopleExclusionReason string` to `Photo`.
- Add constants for `screenshot` and a pure `IsPhotoEligibleForPeople(*Photo) bool` helper.
- Let `AutoMigrate` add the columns and index without changing `photos.status`.

**Step 4: Run tests to verify GREEN**

Run the Task 1 command and expect PASS.

**Step 5: Commit**

```bash
git add backend/internal/model backend/pkg/database
git commit -m "feat(people): persist photo-level people eligibility"
```

### Task 2: Stop scan ingestion from dispatching People jobs

**Files:**
- Modify: `backend/internal/service/photo_scan_service.go`
- Modify: `backend/internal/service/photo_service_test.go`

**Step 1: Write the failing tests**

- Add/adjust tests for new-photo, rebuild, and changed-hash scan paths.
- Assert thumbnail/geocode dispatch remains unchanged and People enqueue count stays zero.
- Assert scan completion no longer starts the People background task solely because scanning completed.

**Step 2: Run tests to verify RED**

```bash
cd backend
go test ./internal/service -run 'TestPhotoService.*Scan.*DoesNotEnqueuePeople' -count=1
```

Expected: FAIL because scan currently calls `enqueuePeopleForPhoto`.

**Step 3: Remove scan-time People dispatch**

- Remove People enqueue calls from create/rebuild/hash-change branches.
- Remove scan-completion auto-start of People background.
- Retain `SetPeopleService` because manual category changes and service reconciliation still need the dependency.

**Step 4: Run tests to verify GREEN**

Run Task 2 tests and the existing photo-service suite.

**Step 5: Commit**

```bash
git add backend/internal/service/photo_scan_service.go backend/internal/service/photo_service_test.go
git commit -m "fix(people): stop dispatching face scans before AI analysis"
```

### Task 3: Publish committed AI analysis results to People

**Files:**
- Modify: `backend/internal/service/ai_service.go`
- Modify: `backend/internal/service/analysis_service.go`
- Modify: `backend/internal/service/ai_service_test.go`
- Modify: `backend/internal/service/analysis_service_test.go`

**Step 1: Write failing callback tests**

- Introduce an `AnalysisCompletedHandler` test fake recording photo IDs.
- Assert local single-photo analysis invokes it only after the photo update succeeds.
- Assert local batch analysis invokes it for each committed result.
- Assert offline `SubmitResultsDirectly` invokes it only for accepted, committed results.
- Assert write failure never invokes the callback.

**Step 2: Run tests to verify RED**

```bash
cd backend
go test ./internal/service -run 'Test(AIService|AnalysisService).*AnalysisCompleted' -count=1
```

Expected: FAIL because no callback contract exists.

**Step 3: Implement the callback and transactional exclusion fields**

- Add `AnalysisCompletedHandler` and setter methods to `AIService` and `AnalysisService`.
- Include `people_excluded` and `people_exclusion_reason` in every AI result update, derived from `main_category`.
- Invoke the callback only after the database write/transaction commits.
- Log callback failures without rolling back a committed AI result; reconciliation is the durable retry path.

**Step 4: Run tests to verify GREEN**

Run Task 3 tests plus all AI/analysis service tests.

**Step 5: Commit**

```bash
git add backend/internal/service/ai_service.go backend/internal/service/analysis_service.go backend/internal/service/*service_test.go
git commit -m "feat(people): dispatch people work after AI commit"
```

### Task 4: Reconcile eligible photos and clean screenshot-derived faces

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/repository/photo_repo.go`
- Modify: `backend/internal/repository/people_job_repo.go`
- Modify: `backend/internal/repository/face_exclusion_repo.go`
- Modify: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/repository/photo_repo_test.go`

**Step 1: Write failing People lifecycle tests**

- AI-completed non-screenshot photo creates one active People job.
- Repeated completion is idempotent.
- AI-completed screenshot cancels an active job, removes its faces/exclusions, clears photo People fields, and repairs affected persons.
- Unanalyzed or screenshot photos are skipped by direct, path, and unprocessed enqueue paths.
- Startup reconciliation repairs historical screenshots and enqueues eligible analyzed `none` photos.

**Step 2: Run tests to verify RED**

```bash
cd backend
go test ./internal/service ./internal/repository -run 'TestPeopleService_(HandleAnalysisCompleted|ReconcileAnalysisEligibility|EnqueueRequiresAI)' -count=1
```

Expected: FAIL because the lifecycle methods and filtered repository reads are missing.

**Step 3: Implement People-owned reconciliation**

- Make `peopleService` implement `AnalysisCompletedHandler`.
- Add transaction-safe cancellation/cleanup for screenshot photos.
- Refresh affected persons, photo top-person category, profile invalidation, prototype cache, and merge-suggestion dirty state after commit.
- Add indexed repository queries for inconsistent screenshots and eligible analyzed unprocessed photos.
- Keep cleanup and reconciliation idempotent.

**Step 4: Run tests to verify GREEN**

Run Task 4 tests plus the complete People and repository suites.

**Step 5: Commit**

```bash
git add backend/internal/service/people_service.go backend/internal/repository backend/internal/service/people_service_test.go
git commit -m "feat(people): reconcile AI eligibility and clean screenshots"
```

### Task 5: Gate local and remote execution and wire services

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/people_handler_test.go`
- Modify: `backend/internal/service/service.go`
- Modify: `backend/internal/api/v1/handler/config_handler.go`
- Modify: `backend/internal/api/v1/handler/config_handler_test.go`
- Modify: `backend/cmd/relive/main.go`

**Step 1: Write failing boundary and wiring tests**

- Local preflight cancels tasks when AI is incomplete or the photo is People-excluded.
- Remote task delivery releases ineligible claimed jobs and never returns them.
- Remote result submission rejects a result if eligibility changed after claim.
- Service construction injects the same People completion handler into local AI, offline analysis, and hot-reloaded AI services.
- Startup invokes reconciliation before scheduler/result-queue/server work begins.

**Step 2: Run tests to verify RED**

```bash
cd backend
go test ./internal/service ./internal/api/v1/handler ./internal/api/v1/router ./cmd/relive -run 'Test.*(PeopleEligibility|AnalysisCompletedWiring|ScreenshotWorker)' -count=1
```

Expected: FAIL on missing guards or wiring.

**Step 3: Implement defense-in-depth and wiring**

- Apply the shared eligibility helper in local preflight and result application.
- Apply it in remote claim response construction and result submission.
- Inject the handler in `NewServices` and AI hot-reload paths.
- Run startup reconciliation before background components start.
- Return an explicit conflict/bad-request response for manual face detection before AI analysis or for screenshots.

**Step 4: Run tests to verify GREEN**

Run Task 5 tests and affected package suites.

**Step 5: Commit**

```bash
git add backend/cmd/relive backend/internal/api/v1/handler backend/internal/service
git commit -m "fix(people): enforce AI-first eligibility at every worker boundary"
```

### Task 6: Verify the complete change

**Files:**
- Review: all changed files

**Step 1: Format and inspect**

```bash
cd backend
gofmt -w <changed-go-files>
git diff --check
```

**Step 2: Run focused regression tests**

```bash
cd backend
go test ./internal/model ./pkg/database ./internal/repository ./internal/service ./internal/api/v1/handler ./internal/api/v1/router ./cmd/relive -count=1
```

Expected: PASS.

**Step 3: Run the full backend suite**

```bash
cd backend
go test ./... -count=1
```

Expected: PASS with exit code 0.

**Step 4: Review scope and migration safety**

- Confirm scan no longer emits People jobs.
- Confirm every AI result path updates exclusion fields and publishes after commit.
- Confirm screenshot cleanup is idempotent and no global photo exclusion is used.
- Confirm no unrelated face-quality, duplicate, or reclustering changes are present.

**Step 5: Commit any verification-only corrections**

```bash
git add <corrected-files>
git commit -m "test(people): cover AI-first screenshot exclusion"
```
