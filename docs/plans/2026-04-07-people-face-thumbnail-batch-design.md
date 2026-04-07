# People Face Thumbnail Batch Design

**Date:** 2026-04-07

**Problem:** `ApplyDetectionResult` currently regenerates each face thumbnail by reopening the same source image once per detected face. On NAS hardware, this adds avoidable CPU cost during offline people processing.

**Goal:** Decode each source photo once per detection result and reuse that decoded image for all face thumbnail crops, without changing thumbnail paths, crop rules, or clustering behavior.

## Current Flow

`ApplyDetectionResult` loops through detected faces and calls `generateFaceThumbnail(...)` for each one. That helper delegates to `util.GenerateFaceThumbnail(...)`, which calls `OpenImage(filePath)` every time.

For a photo with N faces, the backend therefore reopens and decodes the same image N times after detection completes.

## Chosen Design

Use a single-open batch path in the backend:

1. `ApplyDetectionResult` opens the source image once.
2. The decoded `image.Image` is reused to generate all face thumbnails for that photo.
3. Existing single-face helper remains available as a wrapper for other call sites.

## Implementation Shape

- Add an image-based helper in `backend/internal/util/face_thumbnail.go` that accepts a decoded `image.Image`, source file path, output root, and bbox values.
- Add a batch helper in the same file to generate multiple thumbnails from one decoded image.
- Keep existing crop rectangle and output path logic unchanged.
- Update `ApplyDetectionResult` in `backend/internal/service/people_service.go` to:
  - open the photo once
  - batch-generate thumbnail paths
  - assign returned paths to created faces

## Non-Goals

- No async thumbnail generation redesign
- No path format changes
- No clustering changes
- No schema changes

## Validation

- Util test proves batch generation opens the image only once for multiple faces.
- Service test proves multi-face detection still creates all face rows and thumbnail paths successfully.
- Existing people service tests remain green.
