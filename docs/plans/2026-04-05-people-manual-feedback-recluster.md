# People Manual Feedback Recluster Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.
>
> **Status:** Completed
> **Note:** Implemented on `main`; retained for historical traceability.

**Goal:** 让人物手工合并、拆分、移动操作立即返回，避免同步全局重聚类阻塞页面，同时保留后台反馈式重聚类能力。

**Architecture:** 后端把手工操作后的 `triggerRecluster()` 改为服务内异步、串行、可合并的后台调度；当前请求仅完成核心人物/人脸/照片状态更新。顺手补 `faces` 相关索引降低后台成本，前端文案改为告知“后台继续评估”。

**Tech Stack:** Go、Gin、GORM、SQLite、Vue 3、TypeScript、Element Plus

---

### Task 1: 写失败测试锁定异步行为

**Files:**
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: Write the failing test**

- 新增测试覆盖：
  - `MergePeople()` 不应同步调用重型反馈重聚类
  - 连续调度反馈重聚类应合并执行
  - 人物后台任务运行时，反馈重聚类应延后

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -run 'TestPeopleService_(MergePeopleSchedulesFeedbackReclusterAsync|FeedbackReclusterCoalescesRequests|FeedbackReclusterDefersWhileBackgroundRunning)' -v ./internal/service`

Expected: FAIL，因为当前实现仍是同步 `triggerRecluster()`

**Step 3: Write minimal implementation**

- 先不动现有聚类算法，只引入异步调度入口和测试钩子

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -run 'TestPeopleService_(MergePeopleSchedulesFeedbackReclusterAsync|FeedbackReclusterCoalescesRequests|FeedbackReclusterDefersWhileBackgroundRunning)' -v ./internal/service`

Expected: PASS

### Task 2: 实现服务内异步反馈重聚类

**Files:**
- Modify: `backend/internal/service/people_service.go`

**Step 1: Write the failing test**

- 复用 Task 1 的测试，确保实现前已有失败基线

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -run 'TestPeopleService_(MergePeopleSchedulesFeedbackReclusterAsync|FeedbackReclusterCoalescesRequests|FeedbackReclusterDefersWhileBackgroundRunning)' -v ./internal/service`

**Step 3: Write minimal implementation**

- 在 `peopleService` 增加反馈调度状态与互斥
- 新增 `scheduleFeedbackRecluster()` / `runFeedbackReclusterLoop()`
- `MergePeople()`、`SplitPerson()`、`MoveFaces()` 改为调度后台任务并返回零值 `ReclusterResult`

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -run 'TestPeopleService_(MergePeopleSchedulesFeedbackReclusterAsync|FeedbackReclusterCoalescesRequests|FeedbackReclusterDefersWhileBackgroundRunning)' -v ./internal/service`

Expected: PASS

### Task 3: 降低后台重聚类查询成本

**Files:**
- Modify: `backend/pkg/database/database.go`
- Modify: `backend/internal/repository/face_repo.go`
- Modify: `backend/pkg/database/database_test.go` or relevant migration tests if needed

**Step 1: Write the failing test**

- 为索引迁移补最小断言，确保新索引存在

**Step 2: Run test to verify it fails**

Run: `cd backend && go test -run 'TestAutoMigrateAddsPeopleFeedbackIndexes' -v ./pkg/database`

Expected: FAIL

**Step 3: Write minimal implementation**

- 在迁移阶段增加新索引
- 保持现有查询语义不变

**Step 4: Run test to verify it passes**

Run: `cd backend && go test -run 'TestAutoMigrateAddsPeopleFeedbackIndexes' -v ./pkg/database`

Expected: PASS

### Task 4: 调整前端提示文案

**Files:**
- Modify: `frontend/src/views/People/Detail.vue`

**Step 1: Write the smallest failing signal**

- 先按新的零值响应路径调整提示逻辑，令旧文案检查不再满足当前预期

**Step 2: Run build to verify failure if applicable**

Run: `cd frontend && npm run build`

Expected: If no failure, proceed; this task is primarily behavior-focused

**Step 3: Write minimal implementation**

- 成功后提示“后台将继续重新评估不确定人脸”
- 保持已有页面刷新逻辑

**Step 4: Run build to verify it passes**

Run: `cd frontend && npm run build`

Expected: PASS

### Task 5: 全量验证

**Files:**
- Test: `backend/internal/service/people_service_test.go`
- Test: `backend/pkg/database/database_test.go`
- Test: `frontend/src/views/People/Detail.vue`

**Step 1: Run targeted backend tests**

Run: `cd backend && go test -run 'TestPeopleService_(MergePeopleSchedulesFeedbackReclusterAsync|FeedbackReclusterCoalescesRequests|FeedbackReclusterDefersWhileBackgroundRunning)' -v ./internal/service`

Expected: PASS

**Step 2: Run service and migration packages**

Run: `cd backend && go test ./internal/service ./pkg/database ./internal/repository`

Expected: PASS

**Step 3: Run frontend build**

Run: `cd frontend && npm run build`

Expected: PASS
