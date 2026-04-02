# People System Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the first production-ready people system for Relive: async face detection, person clustering and correction, people-first management UI, photo-detail people visibility, and photo-level display weighting.

**Architecture:** Add authoritative `faces` and `people` tables plus a `people_jobs` async queue, keep face embeddings in normal SQLite storage instead of `sqlite-vec`, expose a people-first API from the Go backend, scaffold a small CPU-first `ml-service`, and feed a derived `photos.top_person_category` field into display selection and curated people nomination.

**Tech Stack:** Go, Gin, GORM, SQLite, Vue 3, TypeScript, Element Plus, Python/FastAPI, ONNX Runtime

---

### Task 1: Add people schema and migration contract

**Files:**
- Create: `backend/internal/model/face.go`
- Create: `backend/internal/model/person.go`
- Create: `backend/internal/model/people_job.go`
- Modify: `backend/internal/model/photo.go`
- Modify: `backend/internal/model/dto.go`
- Modify: `backend/pkg/database/database.go`
- Modify: `backend/pkg/database/database_test.go`

**Step 1: Write the failing test**

Add database migration coverage that asserts:

- tables `faces`, `people`, `people_jobs` exist
- `photos` gains `face_process_status`, `face_count`, `top_person_category`
- `people.category` defaults to `stranger`
- `people_jobs.status` follows the same pending/queued/processing/completed/failed/cancelled shape used by thumbnail/geocode jobs

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./pkg/database -run 'TestAutoMigrateAddsPeopleTables|TestAutoMigrateAddsPeopleColumns'`

Expected: FAIL because the new tables and columns do not exist yet.

**Step 3: Write minimal implementation**

Add:

- `Face` model with normalized bbox, confidence, embedding payload, thumbnail path, `person_id`, and manual-lock metadata
- `Person` model with fixed category enum, optional name, representative face, and simple counters
- `PeopleJob` model mirroring current async-job patterns
- `Photo` fields for face processing status, face count, and top person category
- DTOs for people task/status/stats and people API payloads
- `database.AutoMigrate` registration for the new models

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

**Step 5: Commit**

```bash
git add backend/internal/model backend/pkg/database
git commit -m "feat: add people schema and migrations"
```

### Task 2: Add repositories for faces, people, jobs, and derived photo updates

**Files:**
- Create: `backend/internal/repository/face_repo.go`
- Create: `backend/internal/repository/face_repo_test.go`
- Create: `backend/internal/repository/person_repo.go`
- Create: `backend/internal/repository/person_repo_test.go`
- Create: `backend/internal/repository/people_job_repo.go`
- Create: `backend/internal/repository/people_job_repo_test.go`
- Modify: `backend/internal/repository/photo_repo.go`
- Modify: `backend/internal/repository/repository.go`

**Step 1: Write the failing test**

Add repository coverage for:

- creating faces and people
- claiming the next queued `people_job`
- listing faces by photo and by person
- merging people and updating affected face ownership
- recomputing `photos.top_person_category` for a set of affected photo IDs

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/repository -run 'TestFaceRepository|TestPersonRepository|TestPeopleJobRepository|TestPhotoRepositoryRecomputeTopPersonCategory'`

Expected: FAIL because the repositories do not exist yet.

**Step 3: Write minimal implementation**

Implement dedicated repositories for `faces`, `people`, and `people_jobs`, then extend `PhotoRepository` with a focused helper that updates `top_person_category` for affected photos after merges, splits, moves, or category changes.

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

**Step 5: Commit**

```bash
git add backend/internal/repository
git commit -m "feat: add people repositories"
```

### Task 3: Scaffold the ML sidecar and Go client contract

**Files:**
- Create: `ml-service/requirements.txt`
- Create: `ml-service/Dockerfile`
- Create: `ml-service/app/main.py`
- Create: `ml-service/app/config.py`
- Create: `ml-service/app/schemas.py`
- Create: `ml-service/app/models/face.py`
- Create: `ml-service/app/routers/health.py`
- Create: `ml-service/app/routers/faces.py`
- Create: `ml-service/tests/test_face_model.py`
- Create: `ml-service/tests/test_face_router.py`
- Create: `backend/internal/mlclient/client.go`
- Create: `backend/internal/mlclient/client_test.go`
- Modify: `backend/pkg/config/config.go`
- Modify: `backend/config.dev.yaml.example`
- Modify: `backend/config.prod.yaml.example`
- Modify: `docker-compose.yml.example`
- Modify: `docker-compose.prod.yml.example`

**Step 1: Write the failing test**

Define two contracts:

- Python sidecar tests cover health and face-detection response shape
- Go client tests cover request building, timeout behavior, and decoding the face list

**Step 2: Run test to verify it fails**

Run:

- `cd ml-service && pytest -q tests/test_face_model.py tests/test_face_router.py`
- `cd backend && go test -count=1 ./internal/mlclient`

Expected: FAIL because the sidecar source and client do not exist yet.

**Step 3: Write minimal implementation**

Create a minimal CPU-first FastAPI service that returns normalized face boxes, confidence, quality score, and embedding vectors, then add a Go client and config/compose wiring so backend code can call it without introducing `sqlite-vec`.

**Step 4: Run test to verify it passes**

Run the same commands and expect PASS.

**Step 5: Commit**

```bash
git add ml-service backend/internal/mlclient backend/pkg/config/config.go backend/config.dev.yaml.example backend/config.prod.yaml.example docker-compose.yml.example docker-compose.prod.yml.example
git commit -m "feat: scaffold people ml sidecar"
```

### Task 4: Build the async people queue and auto-enqueue from scan/rebuild

**Files:**
- Create: `backend/internal/service/people_service.go`
- Create: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/service/service.go`
- Modify: `backend/internal/service/photo_service.go`
- Modify: `backend/internal/service/photo_scan_service.go`
- Modify: `backend/internal/model/dto.go`

**Step 1: Write the failing test**

Add service coverage that asserts:

- scan/rebuild auto-enqueues active photos into `people_jobs`
- `excluded` photos are never enqueued
- background start/stop/task/log APIs behave like the current thumbnail/geocode services
- photos with no faces finish in a terminal “no face” state instead of looping forever

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleServiceBackground|TestPhotoScanStartsPeopleBackground|TestPeopleServiceMarksNoFaceReady'`

Expected: FAIL because the queue and service do not exist yet.

**Step 3: Write minimal implementation**

Implement `PeopleService` as the background worker for `people_jobs`, wire it into `service.NewServices`, and hook `photo_scan_service.go` so the people worker starts automatically after successful scan/rebuild just like thumbnails and geocode.

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

**Step 5: Commit**

```bash
git add backend/internal/service backend/internal/model/dto.go
git commit -m "feat: add async people processing service"
```

### Task 5: Implement clustering, correction rules, avatar selection, and photo backfill

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`
- Modify: `backend/internal/repository/face_repo.go`
- Modify: `backend/internal/repository/person_repo.go`
- Modify: `backend/internal/repository/photo_repo.go`

**Step 1: Write the failing test**

Add domain tests for:

- confident face assignment joins an existing person
- uncertain faces create a new person
- merge keeps manual decisions authoritative
- split creates a new person from selected faces
- move reassigns selected faces to an existing person
- category changes recompute `photos.top_person_category` for all historical related photos
- manual avatar selection is not overwritten by later auto-selection

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestPeopleServiceCluster|TestPeopleServiceMerge|TestPeopleServiceSplit|TestPeopleServiceMoveFaces|TestPeopleServiceCategoryBackfillsPhotos|TestPeopleServiceManualAvatarWins'`

Expected: FAIL because clustering and correction behavior is not implemented yet.

**Step 3: Write minimal implementation**

Implement the people-domain rules:

- balanced-but-slightly-conservative clustering
- manual lock semantics on corrected faces
- fixed category inheritance
- representative avatar auto-pick with manual override
- immediate historical photo backfill through `top_person_category`

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

**Step 5: Commit**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_service_test.go backend/internal/repository
git commit -m "feat: add people clustering and correction rules"
```

### Task 6: Expose people APIs and authenticated face thumbnails

**Files:**
- Create: `backend/internal/api/v1/handler/people_handler.go`
- Create: `backend/internal/api/v1/handler/people_handler_test.go`
- Modify: `backend/internal/api/v1/handler/handler.go`
- Modify: `backend/internal/api/v1/router/router.go`

**Step 1: Write the failing test**

Add handler coverage for these endpoints:

- `GET /api/v1/people`
- `GET /api/v1/people/:id`
- `GET /api/v1/people/:id/photos`
- `GET /api/v1/people/:id/faces`
- `PATCH /api/v1/people/:id/category`
- `PATCH /api/v1/people/:id/name`
- `PATCH /api/v1/people/:id/avatar`
- `POST /api/v1/people/merge`
- `POST /api/v1/people/split`
- `POST /api/v1/people/move-faces`
- `GET /api/v1/people/task`
- `GET /api/v1/people/stats`
- `GET /api/v1/people/background/logs`
- `GET /api/v1/photos/:id/people`
- `GET /api/v1/faces/:id/thumbnail`

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/api/v1/handler -run 'TestPeopleHandler'`

Expected: FAIL because the handler and routes do not exist yet.

**Step 3: Write minimal implementation**

Add a dedicated `PeopleHandler`, wire it into the handler registry and router, keep the API people-first, and expose low-level face thumbnails only as an authenticated helper endpoint for UI crops.

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

**Step 5: Commit**

```bash
git add backend/internal/api/v1/handler backend/internal/api/v1/router/router.go
git commit -m "feat: expose people api"
```

### Task 7: Build the people management frontend

**Files:**
- Create: `frontend/src/types/people.ts`
- Create: `frontend/src/api/people.ts`
- Create: `frontend/src/views/People/index.vue`
- Create: `frontend/src/views/People/Detail.vue`
- Create: `frontend/src/views/People/peopleHelpers.ts`
- Create: `frontend/tests/peopleHelpers.test.ts`
- Modify: `frontend/src/router/index.ts`
- Modify: `frontend/src/layouts/MainLayout.vue`

**Step 1: Write the failing test**

Add helper coverage for:

- fixed category label mapping
- category sort order `家人 > 亲友 > 熟人 > 路人`
- task-status pill mapping
- avatar fallback display rules

**Step 2: Run test to verify it fails**

Run:

- `cd frontend && rm -rf .tmp-tests && npx tsc tests/peopleHelpers.test.ts src/views/People/peopleHelpers.ts --module nodenext --moduleResolution nodenext --target es2022 --outDir .tmp-tests && node --test .tmp-tests/tests/peopleHelpers.test.js`
- `cd frontend && npm exec vue-tsc -- --noEmit`

Expected: FAIL because the helper module, route wiring, and pages do not exist yet.

**Step 3: Write minimal implementation**

Build:

- `/people` as the main people page with list and task-status tabs
- `/people/:id` as person detail
- a dedicated `peopleApi`
- router/menu wiring using existing `PageHeader` and `SectionHeader` patterns

**Step 4: Run test to verify it passes**

Run the same commands and expect PASS.

**Step 5: Commit**

```bash
git add frontend/src/types/people.ts frontend/src/api/people.ts frontend/src/views/People frontend/src/router/index.ts frontend/src/layouts/MainLayout.vue frontend/tests/peopleHelpers.test.ts
git commit -m "feat: add people management ui"
```

### Task 8: Extend photo detail to show people and face samples

**Files:**
- Modify: `frontend/src/types/photo.ts`
- Create: `frontend/src/views/Photos/photoPeopleHelpers.ts`
- Create: `frontend/tests/photoPeopleHelpers.test.ts`
- Modify: `frontend/src/views/Photos/Detail.vue`

**Step 1: Write the failing test**

Add helper coverage for:

- grouping the photo-level people response
- display labels for `未检测到人脸 / 路人 / 家人...`
- face-crop thumbnail URL generation

**Step 2: Run test to verify it fails**

Run:

- `cd frontend && rm -rf .tmp-tests && npx tsc tests/photoPeopleHelpers.test.ts src/views/Photos/photoPeopleHelpers.ts --module nodenext --moduleResolution nodenext --target es2022 --outDir .tmp-tests && node --test .tmp-tests/tests/photoPeopleHelpers.test.js`
- `cd frontend && npm exec vue-tsc -- --noEmit`

Expected: FAIL because the helper module and detail-page people section do not exist yet.

**Step 3: Write minimal implementation**

Update the photo detail page so it:

- loads `GET /photos/:id/people`
- shows the people present in the photo
- shows face samples needed for inspection
- keeps the people section local to photo detail instead of creating a separate face-management page

**Step 4: Run test to verify it passes**

Run:

- the same helper test command
- `cd frontend && npm exec vue-tsc -- --noEmit`
- `cd frontend && npm run build`

Expected: PASS.

**Step 5: Commit**

```bash
git add frontend/src/types/photo.ts frontend/src/views/Photos/Detail.vue frontend/src/views/Photos/photoPeopleHelpers.ts frontend/tests/photoPeopleHelpers.test.ts
git commit -m "feat: show people in photo detail"
```

### Task 9: Integrate people priority into display selection and real people spotlight

**Files:**
- Modify: `backend/internal/repository/photo_repo.go`
- Modify: `backend/internal/repository/event_repo.go`
- Modify: `backend/internal/service/display_algorithm.go`
- Modify: `backend/internal/service/display_service.go`
- Modify: `backend/internal/service/event_curation_service.go`
- Modify: `backend/internal/service/display_service_test.go`
- Modify: `backend/internal/service/display_daily_service_test.go`

**Step 1: Write the failing test**

Add display coverage that asserts:

- photos with `family` outrank otherwise similar `stranger` photos
- `friend` outranks `acquaintance`
- no-face photos remain neutral
- `people_spotlight` no longer depends only on tag heuristics when real people data exists

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -count=1 ./internal/service -run 'TestSelectTopPhotosPrefersPeoplePriority|TestCurationPeopleSpotlightUsesRealPeopleData'`

Expected: FAIL because display ranking and people nomination still use the old logic.

**Step 3: Write minimal implementation**

Use `photos.top_person_category` as the photo-layer people signal, apply built-in category weighting in display ranking, and change `people_spotlight` nomination to prefer real people-backed events/photos over `PrimaryTag`-only guesses.

**Step 4: Run test to verify it passes**

Run the same command and expect PASS.

**Step 5: Commit**

```bash
git add backend/internal/repository/photo_repo.go backend/internal/repository/event_repo.go backend/internal/service/display_algorithm.go backend/internal/service/display_service.go backend/internal/service/event_curation_service.go backend/internal/service/display_service_test.go backend/internal/service/display_daily_service_test.go
git commit -m "feat: wire people priority into display strategy"
```

### Task 10: Refresh docs and verify the full stack

**Files:**
- Modify: `docs/INDEX.md`
- Modify: `docs/PROJECT_STATUS.md`
- Modify: `docs/BACKEND_API.md`
- Modify: `docs/QUICK_REFERENCE.md`

**Step 1: Update docs**

Refresh the user/developer truth so it matches the finished feature:

- routes and APIs
- people pages
- background task endpoints
- display-strategy behavior

**Step 2: Run backend verification**

Run: `cd backend && go test -count=1 ./...`

Expected: PASS.

**Step 3: Run frontend and ML verification**

Run:

- `cd frontend && npm exec vue-tsc -- --noEmit`
- `cd frontend && npm run build`
- `cd ml-service && pytest -q`

Expected: PASS.

**Step 4: Run manual sanity checks**

Verify:

- scan creates people jobs automatically
- task page shows progress and recent logs
- people list and person detail load correctly
- photo detail shows people for the selected photo
- changing a person to `家人` changes later display preview results

**Step 5: Commit**

```bash
git add docs/INDEX.md docs/PROJECT_STATUS.md docs/BACKEND_API.md docs/QUICK_REFERENCE.md
git commit -m "docs: refresh people system docs"
```
