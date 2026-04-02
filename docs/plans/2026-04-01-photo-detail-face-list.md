# Photo Detail Face List Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Show detected face crops in the photo detail page sidebar, with confidence and clustering status.

**Architecture:** Reuse the existing `/faces/photo/:photo_id` API for face metadata and the authenticated `/faces/:id/thumbnail` image endpoint for crop delivery. Keep the feature local to the photo detail view and add a tiny helper module for face display formatting.

**Tech Stack:** Vue 3, TypeScript, Vite, Element Plus, Gin

---

### Task 1: Add face display helper coverage

**Files:**
- Create: `frontend/src/views/Photos/faceDetailHelpers.ts`
- Create: `frontend/tests/faceDetailHelpers.test.ts`

**Step 1: Write the failing test**

Cover:
- thumbnail URL generation
- clustered/unclustered label formatting
- confidence formatting

**Step 2: Run test to verify it fails**

Run:
`cd frontend && rm -rf .tmp-tests && npx tsc tests/faceDetailHelpers.test.ts src/views/Photos/faceDetailHelpers.ts --module nodenext --moduleResolution nodenext --target es2022 --outDir .tmp-tests && node --test .tmp-tests/tests/faceDetailHelpers.test.js`

Expected:
- fail because helper module does not exist yet

**Step 3: Write minimal implementation**

Implement the three helper functions used by the detail page.

**Step 4: Run test to verify it passes**

Run the same command and expect pass.

### Task 2: Wire faces into photo detail

**Files:**
- Modify: `frontend/src/views/Photos/Detail.vue`

**Step 1: Load faces for the current photo**

Use `faceApi.getFacesByPhoto(photoId)` and refresh on initial load and route param changes.

**Step 2: Render the right-side face list**

Add a new section below EXIF info:
- crop image
- confidence
- `人物 #ID` or `未聚类`

**Step 3: Add minimal fallback behavior**

If face crop loading fails, fall back to a neutral placeholder icon.

**Step 4: Verify**

Run:
- `cd frontend && npm exec vue-tsc -- --noEmit`
- `cd frontend && npm run build`

### Task 3: Regression verification

**Files:**
- Reuse existing backend files only

**Step 1: Verify related backend routes still pass**

Run:
`cd backend && go test -count=1 ./internal/service/... ./internal/api/v1/...`

**Step 2: Manual sanity expectation**

Photo detail page should show:
- detected faces when available
- `未识别到人脸` when none exist
- stable refresh behavior when navigating prev/next photos
