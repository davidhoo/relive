# GeoNames China Admin1 Mapping Fix Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Correct offline geocoding province names for Chinese coordinates, including Issue #11.

**Architecture:** Preserve the embedded GeoNames city data and database schema. Correct the runtime `admin1` lookup table, then verify the mapping and end-to-end offline provider result with focused regression tests.

**Tech Stack:** Go, GORM, SQLite, testify

---

### Task 1: Add regression coverage

**Files:**
- Modify: `backend/internal/geocode/offline_test.go`

1. Replace the incorrect China province expectations with table-driven GeoNames admin1 expectations.
2. Add an in-memory SQLite provider test for `(30.292125, 120.378533)` and a nearby `Xiasha` city row.
3. Run `go test ./internal/geocode -run 'TestGetProvinceName_China|TestOfflineProvider_Issue11' -count=1` and verify it fails because `02` resolves to Tianjin.

### Task 2: Correct the mapping

**Files:**
- Modify: `backend/internal/geocode/offline.go`

1. Replace `chinaProvinceNames` with the complete official GeoNames China admin1 mapping.
2. Run the focused regression tests and verify they pass.
3. Run `gofmt` on modified Go files.

### Task 3: Verify the fix

**Files:**
- Verify: `backend/internal/geocode/offline.go`
- Verify: `backend/internal/geocode/offline_test.go`

1. Run `go test ./internal/geocode ./pkg/geodata -count=1`.
2. Run `go test ./... -count=1`.
3. Confirm the diff is limited to the mapping, regression tests, and these plan documents.
