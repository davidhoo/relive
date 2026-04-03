# People Clustering Cross-Photo Create Guard Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 防止同一张照片里的多张人脸仅凭单图内部证据就自动创建新人物。

**Architecture:** 保留当前 `pending -> component -> attach/create/pending` 流程，只收紧 `createPersonFromComponent(...)` 的触发条件。组件仍然可以附着到已有 `Person`，但只有跨至少两张照片的组件才允许创建新人物。

**Tech Stack:** Go、GORM、SQLite、testify

---

### Task 1: 提取组件跨照片计数 helper

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 写失败测试**

- 为组件增加 distinct `photo_id` 计数测试
- 断言同图组件计数为 `1`
- 断言跨图组件计数为 `2`

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestPeopleService_(ComponentPhotoCount)' -v ./internal/service`

**Step 3: 实现最小 helper**

- 实现 `componentPhotoCount(...)`

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestPeopleService_(ComponentPhotoCount)' -v ./internal/service`

### Task 2: 收紧“创建新人物”分支

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 写失败测试**

- 同图两张新脸、无法附着已有 `Person` 时保持 `pending`
- 同图两张新脸、可以附着已有 `Person` 时仍然 `assigned`
- 跨图两张新脸、无法附着已有 `Person` 时创建新人物

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestPeopleService_(SamePhotoComponentStaysPending|SamePhotoComponentCanStillAttach|CrossPhotoComponentCreatesPerson)' -v ./internal/service`

**Step 3: 实现最小逻辑**

- 在 `runIncrementalClustering(...)` 中保留 attach 分支
- 将创建新人物条件从 `len(component) >= peopleMinClusterFaces` 改为：
  - `len(component) >= peopleMinClusterFaces`
  - `componentPhotoCount(component) >= 2`
- 不满足跨照片条件时落回 `markComponentPending(...)`

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestPeopleService_(SamePhotoComponentStaysPending|SamePhotoComponentCanStillAttach|CrossPhotoComponentCreatesPerson)' -v ./internal/service`

### Task 3: 回归验证现有流程

**Files:**
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 校验已有回归**

- 确认单张不稳定脸仍保持 `pending`
- 确认跨照片后续证据仍能把 `pending` 转成 `assigned`
- 确认已有人物附着测试不回归

**Step 2: 跑服务测试**

Run: `cd backend && go test ./internal/service`

Expected: PASS

### Task 4: 全量验证

**Files:**
- Test: `backend/internal/repository/face_repo_test.go`
- Test: `backend/internal/service/people_service_test.go`
- Test: `backend/internal/api/v1/handler`

**Step 1: 跑 repository 与 service**

Run: `cd backend && go test ./internal/repository ./internal/service`

Expected: PASS

**Step 2: 跑 handler**

Run: `cd backend && go test ./internal/api/v1/handler`

Expected: PASS

**Step 3: 跑 backend 全量**

Run: `cd backend && go test ./...`

Expected: PASS
