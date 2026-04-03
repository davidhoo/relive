# Immich-lite People Clustering Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将当前人物归属逻辑从“单脸阈值匹配”升级为更接近 Immich 的增量聚类流程，提高自动聚类准确率并减少碎片人物。

**Architecture:** 把现有人物后台拆成“人脸提取”和“增量聚类”两段。新脸先进入待聚类池，再通过批次内相似图、已有 `Person` 原型和密度规则决定自动并入、新建或延后，而不是立即用单阈值新建人物。

**Tech Stack:** Go、Gin、GORM、SQLite、testify

---

### Task 1: 为 `Face` 增加聚类状态字段

**Files:**
- Modify: `backend/internal/model/face.go`
- Modify: `backend/pkg/database/database.go`（如需迁移参与点）
- Test: `backend/internal/testutil/db.go`

**Step 1: 写失败测试**

- 在 `backend/internal/service/people_service_test.go` 或新的 model/repository 测试中，断言新字段可读写

**Step 2: 运行测试确认失败**

Run: `cd backend && go test -run TestFaceClusterStatusFields -v ./internal/service`

**Step 3: 增加字段**

- `cluster_status`
- `cluster_score`
- `clustered_at`

**Step 4: 运行测试确认通过**

Run: `cd backend && go test -run TestFaceClusterStatusFields -v ./internal/service`

### Task 2: 扩展 `FaceRepository`

**Files:**
- Modify: `backend/internal/repository/face_repo.go`
- Modify: `backend/internal/repository/face_repo_test.go`

**Step 1: 写失败测试**

- `ListPending(limit)`
- `ListTopByPersonIDs(ids, perPerson)`
- `UpdateClusterFields(ids, fields)`

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestFaceRepository_(ListPending|ListTopByPersonIDs|UpdateClusterFields)' -v ./internal/repository`

**Step 3: 实现最小 repository 方法**

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestFaceRepository_(ListPending|ListTopByPersonIDs|UpdateClusterFields)' -v ./internal/repository`

### Task 3: 提取人物原型选择逻辑

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 写失败测试**

- `top-k` 原型优先级：`manual_locked > quality_score > confidence`
- 每人最多 `k=3`

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestPeopleService_(SelectPersonPrototypes)' -v ./internal/service`

**Step 3: 实现 `selectPersonPrototypes(...)`**

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestPeopleService_(SelectPersonPrototypes)' -v ./internal/service`

### Task 4: 实现批次内相似图构建

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 写失败测试**

- 脸与脸在 `link_threshold` 之上才连边
- 组件划分正确

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestPeopleService_(BuildFaceGraph|FindFaceComponents)' -v ./internal/service`

**Step 3: 实现**

- `buildFaceGraph(...)`
- `findConnectedComponents(...)`

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestPeopleService_(BuildFaceGraph|FindFaceComponents)' -v ./internal/service`

### Task 5: 实现组件附着到已有 `Person`

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 写失败测试**

- 组件与某个人物原型集合足够接近时，整组并入同一人物
- 仅高于 `attach_threshold` 时才允许附着

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestPeopleService_(AttachComponentToExistingPerson)' -v ./internal/service`

**Step 3: 实现**

- `scoreComponentAgainstPerson(...)`
- `attachComponentToExistingPerson(...)`

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestPeopleService_(AttachComponentToExistingPerson)' -v ./internal/service`

### Task 6: 实现“新建人物”与“延后挂起”规则

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 写失败测试**

- 组件大小 `< min_cluster_faces` 时保持 `pending`
- 组件大小 `>= min_cluster_faces` 且不能附着时创建新人物

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestPeopleService_(PendingComponent|CreatePersonFromComponent)' -v ./internal/service`

**Step 3: 实现**

- `markComponentPending(...)`
- `createPersonFromComponent(...)`

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestPeopleService_(PendingComponent|CreatePersonFromComponent)' -v ./internal/service`

### Task 7: 重构 `processJob`

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 写失败测试**

- 新脸提取后先写入 `pending`
- 聚类后可变为 `assigned` 或保持 `pending`
- 不再在每张新脸上直接调用 `ensurePersonForDetectedFace`

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestPeopleService_(ProcessJobUsesIncrementalClustering|SingleUncertainFaceStaysPending)' -v ./internal/service`

**Step 3: 实现最小重构**

- 保留检测与缩略图生成逻辑
- 用新的增量聚类入口替换 `ensurePersonForDetectedFace`

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestPeopleService_(ProcessJobUsesIncrementalClustering|SingleUncertainFaceStaysPending)' -v ./internal/service`

### Task 8: 保持手动操作兼容

**Files:**
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 写失败测试**

- `manual_locked` 人脸不会被自动重分配
- merge/split/move 后原型选择仍正确

**Step 2: 跑测试确认失败**

Run: `cd backend && go test -run 'TestPeopleService_(ManualLockedFacesAreStable|PrototypeRefreshAfterManualOps)' -v ./internal/service`

**Step 3: 实现最小兼容**

**Step 4: 跑测试确认通过**

Run: `cd backend && go test -run 'TestPeopleService_(ManualLockedFacesAreStable|PrototypeRefreshAfterManualOps)' -v ./internal/service`

### Task 9: 验证现有聚类行为与新行为

**Files:**
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 增加回归测试**

- 两张彼此接近的新脸一起形成新人物
- 单张不稳定脸不会立即新建人物
- 多张后来加入后可把 `pending` 转成已归属

**Step 2: 跑整组人物服务测试**

Run: `cd backend && go test ./internal/service`

Expected: 全部通过

### Task 10: 全量验证

**Files:**
- Test: `backend/internal/repository/face_repo_test.go`
- Test: `backend/internal/service/people_service_test.go`

**Step 1: 跑 repository 与 service**

Run: `cd backend && go test ./internal/repository ./internal/service`

Expected: PASS

**Step 2: 视情况跑 handler**

Run: `cd backend && go test ./internal/api/v1/handler`

Expected: PASS
