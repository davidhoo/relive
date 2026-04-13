# People Face Thumbnail Batch Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** Reuse one decoded source image per photo when generating multiple face thumbnails during people detection result application.

**Architecture:** Keep thumbnail crop and output path semantics unchanged. Add image-based and batch helpers in the util layer, then switch `ApplyDetectionResult` to the batch path so one photo decode serves all face crops.

**Tech Stack:** Go, GORM, `disintegration/imaging`, existing people service tests

---

### Task 1: Add util coverage for single-open batch thumbnail generation

**Files:**
- Modify: `backend/internal/util/face_thumbnail.go`
- Create or Modify: `backend/internal/util/face_thumbnail_test.go`

**Step 1: Write the failing test**

Add a util test that:
- overrides the image opener through a test seam
- calls a new batch thumbnail helper with multiple bbox entries
- asserts the opener is called exactly once
- asserts all output paths are returned and files exist

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -run TestGenerateFaceThumbnailsOpensImageOnce -v ./internal/util`

Expected: FAIL because the batch helper or opener seam does not exist yet.

**Step 3: Write minimal implementation**

Implement:
- an internal image-opener seam for tests
- an image-based thumbnail generator
- a batch thumbnail helper that opens the file once and generates all requested thumbnails

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -run TestGenerateFaceThumbnailsOpensImageOnce -v ./internal/util`

Expected: PASS

### Task 2: Switch people detection result handling to batch thumbnail generation

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: Write the failing test**

Add a service test that:
- applies a detection result containing multiple faces for one photo
- verifies all created faces have non-empty thumbnail paths
- verifies the thumbnail files exist on disk

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -run TestPeopleService_ApplyDetectionResult_WithMultipleFacesCreatesAllThumbnails -v ./internal/service`

Expected: FAIL because the new multi-face thumbnail expectation is not covered by the current implementation path.

**Step 3: Write minimal implementation**

Update `ApplyDetectionResult` to:
- collect bbox inputs first
- call the new util batch helper once per photo
- map returned thumbnail paths back onto created face rows

Do not change crop rules, path generation, face persistence, or clustering logic.

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -run TestPeopleService_ApplyDetectionResult_WithMultipleFacesCreatesAllThumbnails -v ./internal/service`

Expected: PASS

### Task 3: Verify no regressions in people processing

**Files:**
- No code changes expected

**Step 1: Run focused util and service suites**

Run:
- `cd backend && go test ./internal/util/...`
- `cd backend && go test ./internal/service/...`

Expected: PASS

**Step 2: Run clustering benchmark sanity check**

Run: `cd backend && go test -bench BenchmarkPeopleClustering -run ^$ -benchmem ./internal/service`

Expected: benchmark remains in the optimized range and no unexpected regression appears.
