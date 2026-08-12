# 人脸质检历史重评分：校准失败修复与 Enforce 安全门禁 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 修复历史人脸校准把有效 BBox 写成零框、全失败仍显示完成并放行全量 enforce 的问题；为运行 #1 的 4,733 条失败记录提供可审计、无副作用的重试路径。

**Architecture:** 历史重评分继续以追加式 face_quality_events 和可复现的 face_quality_rescore_runs/items 为事实来源。修复任务快照的列映射，并在调用 ML 前验证归一化 BBox；失败记录留在既有 historical_rescore + retryable_error|unmatched 队列，通过“按运行重试”创建新的 shadow 校准运行。运行完成状态、统计和 full/enforce 门禁由后端统一裁决，前端仅呈现并显式确认。

**Tech Stack:** Go + Gin + GORM + SQLite、Vue 3 + TypeScript + Element Plus、Vitest、Go test。

---

## 1. 背景与线上事实

生产 NAS 运行 #1（calibration + shadow）的实际结果：

- 目标 1,000 张照片、4,733 张 Face；
- item 状态为 retryable_error=4,733，每项仅尝试 1 次，错误均为 score known faces returned status 422；
- 所有 face_quality_rescore_items 的 bbox_x/y/width/height 均为 0.0；
- 同一 Face 在 faces 的 b_box_x/y/width/height 和 baseline face_quality_events 的 bbox_x/y/width/height 中均为有效归一化框；
- ML 的 BoundingBox 契约要求 width 和 height 大于 0，因此 422 发生在请求校验阶段，**没有模型推理、没有真实质量结论**；
- 本次运行没有自动隔离。失败路径只更新当前审计事件为 historical_rescore + retryable_error，不修改 person_id、cluster_status、embedding 或 BBox。

根因位于 backend/internal/service/face_quality_rescore.go 的 snapshotTargets：SQL 选择 bbox_x 等列，但临时扫描结构体的 BBoxX 等字段没有 gorm column 标签。GORM 默认将 BBoxX 映射为 b_box_x；扫描不到 bbox_x 后字段保留 Go 零值，零框随后被写入 item。

关联缺陷：

1. refreshRunCounts 将 retryable_error 和 unmatched 计入 processed_face_count，并只按 decision=review_required 统计灰区，所以同一 Face 被错误显示为“已处理、灰区、失败”三次。
2. maybeCompleteRun 只检查 pending/processing 是否清空，全部失败仍会写 completed；HasCompletedCalibration 又只检查任一 completed 校准，错误放行 full/enforce。
3. 前端要求输入“校准运行 ID”，但创建 full 的请求实际仅发送 mode=full；后端 DTO 也没有该字段，输入框没有安全作用。
4. #1 的失败事件已从 historical_backfill + missing 移入 historical_rescore + retryable_error。普通“启动校准任务”只会选择新的历史缺证据样本，不能重试这 4,733 条。

## 2. 已确定目标与不变量

### 2.1 目标

1. 进入 score-known-faces 的每个目标框都与 baseline 事件一致、合法且非零；非法框绝不发送给 ML。
2. “获得模型证据”“人工覆盖”“待重试/未匹配”互斥统计；#1 必须显示为“完成但有错误”，不可被当成成功校准。
3. 通过一个新的 calibration + shadow 运行精确重试 #1 的失败事件；不手改、删除或回退审计记录。
4. full/enforce 必须引用服务端验证通过的具体校准运行：目标非空、无技术失败、且每个目标都有模型证据或被人工覆盖。
5. 修复上线后，任何有技术失败的校准都不能解锁或创建 full/enforce。

### 2.2 强制边界

- 校准和重试运行固定为 shadow：绝不创建 face_exclusions，绝不把 Face 改为 excluded。
- 失败、未匹配、文件读取失败、非法 BBox 与 ML 422 都是技术状态，绝不解释为 non_face 或 low_quality。
- 人工结论优先；重试成功前只将失败 baseline 事件置为非当前，不覆盖人工结论。
- 不调用 ApplyDetectionResult、EnqueuePhoto(force=true)、重新检测写路径、全量聚类或 embedding 重建。
- 不用 SQL 伪造 #1 的 item 成功，也不把其事件改回 historical_backfill + missing。

## 3. 最终数据、状态与接口契约

### 3.1 运行状态与计数

新增终态 completed_with_errors。completed 只代表无技术错误、可作为校准候选的终态；completed_with_errors 表示队列已耗尽但存在 retryable_error 或 unmatched，不能放行 full/enforce。

FaceQualityRescoreRun 新增字段：

| 字段 | 语义 |
| --- | --- |
| superseded_manual_count | worker 发现人工已覆盖而跳过的 Face 数；安全终态，但没有新增模型证据。 |
| retry_of_run_id | 新建 shadow 校准运行重试的来源 run；普通校准为空。 |
| calibration_run_id | full/enforce 运行引用并通过验证的校准 run；校准运行为空。 |

保留 processed_face_count，但重定义为仅 item.status=processed 的“已获得模型证据”数，不再包含 retryable、unmatched 或 superseded。retryable_count 始终为 retryable 与 unmatched 之和。review_required_count 只统计本运行当前 evidence_state=available 且 decision=review_required 的真实灰区。

processed_photo_count 统计已到终态的照片数，前端标签改为“终态照片”，不得称为“成功处理”。last_error 更新为运行中最近一条非空 item 错误。

数据库初始化增加幂等修复：

- 已有 status=completed 且 retryable_count>0 的运行改为 completed_with_errors；
- last_error 为空时，从该运行任一失败 item 回填错误。

该修复只更新 run 元数据，不触碰 Face、排除或审计证据。它必须使线上 #1 在发布后显示为“完成但有错误”。

### 3.2 BBox 快照与请求前校验

snapshotTargets 的临时 row 必须精确声明 GORM 列名：

~~~
BBoxX      float64 `gorm:"column:bbox_x"`
BBoxY      float64 `gorm:"column:bbox_y"`
BBoxWidth  float64 `gorm:"column:bbox_width"`
BBoxHeight float64 `gorm:"column:bbox_height"`
~~~

实现内部 helper isValidNormalizedBBox，要求值有限，x/y 在 [0,1]，width/height 在 (0,1]，且 x+width、y+height 不超过 1。

在 processOneBatch 构造 ScoreKnownFaceTarget 前验证所有 item。任一目标不合法时，该照片的相应 item 写 retryable_error 和 last_error=invalid normalized bbox，当前事件转入既有 historical_rescore + retryable_error；不得调用 ML。这使真实历史坏数据不会再次触发批量 422。

### 3.3 按运行重试

新增受保护接口：

~~~
POST /api/v1/people/face-quality/rescore-runs/:id/retry
~~~

仅允许来源 run 同时满足：

- mode=calibration；
- 已是 completed 或 completed_with_errors；
- 至少有一条当前 historical_rescore + retryable_error|unmatched 事件。

接口创建新的 mode=calibration、apply_mode=shadow、retry_of_run_id=:id 运行。它只快照该来源 run 的当前失败事件，保存其 photo_id、face_id、baseline event 和有效 BBox；空集合返回 409，不创建空 run。

运行 #1 的恢复就是调用该接口，不是普通“启动校准任务”。新 run 成功后，#1 的失败事件才失活，并写入新 run 的 historical_rescore + available 事件；审计链 #1 → retry run 完整保留。

### 3.4 Full/enforce 门禁

扩展创建请求：

~~~json
{"mode":"full","calibration_run_id":2}
~~~

mode=full 时 calibration_run_id 必填。服务端读取该运行并要求：

- mode=calibration、apply_mode=shadow、status=completed；
- target_face_count 大于 0；
- retryable_count=0；
- processed_face_count + superseded_manual_count = target_face_count；
- 不存在 pending 或 processing item。

不满足返回 409 RESCORE_CALIBRATION_REQUIRED；不得退化为“任一 completed 校准即可”。验证通过后，full run 持久化 calibration_run_id，供审计追溯。保留 full/enforce 的二次确认；本任务不自动启动它。

运行列表/详情响应新增 eligible_for_enforce、retry_of_run_id、calibration_run_id 和 superseded_manual_count。eligible_for_enforce 必须由后端计算，前端不得以 status=completed 自行推断。

## 4. 前端交互

修改 frontend/src/views/People/FaceQualityReview.vue：

- 状态卡展示目标 Face/照片、已获证据、人工覆盖、待重试/未匹配、真实灰区、自动隔离、终态照片；不能让同一 Face 同时显示为已处理、灰区和失败。
- completed_with_errors 显示“完成但有错误”。若 retryable_count 大于 0，显示“重试运行 #ID”，调用新 retry 接口；确认文案明确“只重试技术失败样本，仍为 shadow，不自动隔离”。
- full/enforce 入口仅在列表存在 eligible_for_enforce=true 的校准 run 时出现。对话框改用 el-select 选择这些 run，不允许自由输入任意 ID。
- 创建 full 请求必须发送 calibration_run_id；被后端拒绝时展示服务端错误，不在前端绕过。
- #1 在修复上线后必须显示“完成但有错误（4,733 待重试）”，且 full/enforce 入口不可用。

## 5. 实施任务（测试先行）

### Task 1：修复 BBox 扫描并阻断非法请求

**Files:**

- Modify: backend/internal/service/face_quality_rescore.go
- Modify: backend/internal/service/face_quality_rescore_test.go

**Step 1: 写失败测试**

- 在 TestRescore_CreateCalibrationFreezesTargets 中断言每个 item 的四个 BBox 与 baseline event 完全一致且 width/height 大于 0；当前实现必须失败，复现零框。
- 新增非法 BBox 测试：fake ML client 调用次数为 0；item 为 retryable_error；事件为 historical_rescore + retryable_error；Face 人物归属和聚类状态不变。

**Step 2: 验证测试先失败**

~~~bash
cd backend
go test ./internal/service -run 'TestRescore_(CreateCalibrationFreezesTargets|InvalidBBox)' -count=1
~~~

**Step 3: 最小实现**

- 给 row 的四个 BBox 字段补 gorm column 标签，不改 baseline BBox 的来源和排序。
- 实现 isValidNormalizedBBox，并在发 ML 请求前阻断非法项。

**Step 4: 验证通过**

~~~bash
cd backend
go test ./internal/service -run 'TestRescore_(CreateCalibrationFreezesTargets|InvalidBBox)' -count=1
~~~

### Task 2：修正运行终态、计数和既有 #1 元数据

**Files:**

- Modify: backend/internal/model/face_quality_rescore.go
- Modify: backend/internal/model/dto.go
- Modify: backend/pkg/database/database.go
- Modify: backend/pkg/database/database_test.go
- Modify: backend/internal/service/face_quality_rescore.go
- Modify: backend/internal/service/face_quality_rescore_test.go

**Step 1: 写失败测试**

- 全部 item retryable 的 run：processed_face_count=0、review_required_count=0、retryable_count=target、last_error 非空、终态 completed_with_errors。
- 只有 evidence_state=available 的 review_required 计入灰区；retryable_error + decision=review_required 不计入灰区。
- legacy completed + retryable_count>0 的 fixture 初始化后变为 completed_with_errors 并回填错误；无失败的 completed 不变。

**Step 2: 最小实现**

- 加入新状态和三个 run 字段，保持 GORM 自动迁移幂等。
- refreshRunCounts 按第 3.1 节重算计数与最近错误；maybeCompleteRun 根据技术错误选择 completed 或 completed_with_errors。
- 数据库初始化中加入只更新运行元数据的幂等修复。

**Step 3: 验证**

~~~bash
cd backend
go test ./pkg/database -run 'Test.*FaceQuality.*Rescore' -count=1
go test ./internal/service -run 'TestRescore_' -count=1
~~~

### Task 3：实现按失败来源精确重试

**Files:**

- Modify: backend/internal/repository/face_quality_rescore_repo.go
- Modify: backend/internal/repository/face_quality_rescore_repo_test.go
- Modify: backend/internal/service/face_quality_rescore.go
- Modify: backend/internal/service/face_quality_rescore_test.go
- Modify: backend/internal/api/v1/handler/people_handler.go
- Modify: backend/internal/api/v1/handler/people_rescore_handler_test.go
- Modify: backend/internal/api/v1/router/router.go

**Step 1: 写失败测试**

- 来源 run #1 有两个 retryable、一个成功、一个人工覆盖：重试只快照两个当前 retryable 事件，新 run 为 shadow 且 retry_of_run_id=1。
- 来源不是校准、仍在运行、没有失败集合或不存在时返回 409/404，不创建 run。
- 成功重试只失活对应失败 baseline，不能修改无关运行、人工事件或 Face 归属。

**Step 2: 最小实现**

- repository 增加按 rescore_run_id、is_current 和 evidence_state 查询重试目标的窄查询，禁止扫全部历史缺证据。
- 服务新增 RetryRun(sourceRunID)，复用创建校准运行、单活跃 run 互斥、worker 和审计写入。
- 增加 retry handler、路由和错误码映射。

**Step 3: 验证**

~~~bash
cd backend
go test ./internal/repository -run FaceQualityRescore -count=1
go test ./internal/service -run 'TestRescore_(Retry|Shadow)' -count=1
go test ./internal/api/v1/handler -run Rescore -count=1
~~~

### Task 4：以指定合格校准运行保护 full/enforce

**Files:**

- Modify: backend/internal/model/dto.go
- Modify: backend/internal/repository/face_quality_rescore_repo.go
- Modify: backend/internal/repository/face_quality_rescore_repo_test.go
- Modify: backend/internal/service/face_quality_rescore.go
- Modify: backend/internal/service/face_quality_rescore_test.go
- Modify: backend/internal/api/v1/handler/people_handler.go
- Modify: backend/internal/api/v1/handler/people_rescore_handler_test.go

**Step 1: 写失败测试**

- full 缺 calibration_run_id 返回 400；ID 不存在或不是 calibration 返回 409/404。
- #1 类型的 completed_with_errors 或 legacy completed + retryable_count>0 返回 409。
- 空校准、仍有 pending/processing 或计数不闭合的校准均返回 409。
- 合格 shadow 校准可以创建 full/enforce，且 calibration_run_id 被持久化。

**Step 2: 最小实现**

- 用 GetEligibleCalibration(runID) 取代 HasCompletedCalibration()，服务端执行第 3.4 节全部检查。
- handler 将 DTO 的 calibration_run_id 传给 service；eligible_for_enforce 只由后端填充。

**Step 3: 验证**

~~~bash
cd backend
go test ./internal/repository -run 'Test.*Eligible.*Calibration' -count=1
go test ./internal/service -run 'TestRescore_.*(Calibration|Enforce)' -count=1
go test ./internal/api/v1/handler -run Rescore -count=1
~~~

### Task 5：修正运行状态卡和 Enforce 交互

**Files:**

- Modify: frontend/src/types/people.ts
- Modify: frontend/src/api/people.ts
- Modify: frontend/src/views/People/FaceQualityReview.vue
- Modify: frontend/src/views/People/FaceQualityReview.spec.ts

**Step 1: 写失败测试**

- 输入 completed_with_errors、retryable_count=4733 的 run：显示“完成但有错误”、已获证据 0、待重试 4733；不显示灰区 4733，full/enforce 入口不可见。
- 点击重试 #1 调用 retry API，不调用普通创建接口。
- full 只显示后端 eligible_for_enforce=true 的校准项，提交 body 含正确 calibration_run_id。

**Step 2: 最小实现**

- 扩展 TypeScript 类型、API 方法与状态文案；旧字段缺失时按不合格处理。
- 用下拉选择替代无效自由输入，保持二次确认。
- 状态卡使用第 4 节的互斥计数和最近错误。

**Step 3: 验证**

~~~bash
cd frontend
npm test -- src/views/People/FaceQualityReview.spec.ts
npm run typecheck
npm run build
~~~

### Task 6：全量回归与生产恢复演练

**Files:**

- Modify: docs/plans/2026-08-12-face-quality-rescore-calibration-repair.md（仅记录实际结果）

**Step 1: 本地全量验证**

~~~bash
cd backend && go test ./...
cd ../frontend && npm test && npm run typecheck && npm run build
cd ../ml-service && pytest -q
~~~

**Step 2: NAS 部署前检查**

- 创建一致性 SQLite 备份并确认 PRAGMA integrity_check 为 ok。
- 记录 #1 的 item/事件计数：4,733 retryable_error、0 自动隔离。
- 部署后确认 #1 变为 completed_with_errors，且以 #1 创建 full/enforce 返回 409。

**Step 3: 生产恢复**

1. 在审核页对 #1 点击“重试运行”；确认新 run 为 calibration + shadow + retry_of_run_id=1，目标仍为 4,733 Face / 1,000 照片。
2. 运行中确认 relive-ml 不再出现零宽高导致的 422；抽样比对请求目标框与 Face/event 原始 BBox。
3. 结束后确认技术失败为 0，抽样核验 accepted、真实灰区和 shadow 候选，并确认无新增 face_exclusions。
4. 仅在业务方明确 Go 后，选择该合格 retry calibration 创建 full/enforce。任何错误、异常 iowait、SQLite busy、ML 超时或抽样偏差都是 No-Go。

## 6. 验收标准

1. 本地和生产创建的 item BBox 与 baseline event 一致，四个字段不再系统性为零。
2. 非法 BBox 永不发送 ML，只进入技术失败，不影响 Face 归属或聚类。
3. 全失败运行显示 completed_with_errors、已获证据=0、真实灰区=0、待重试=target，不能显示为成功。
4. #1 能被精确重试；普通新校准不能吞掉或替代其 4,733 条失败样本。
5. 重试成功后审计链可追溯：#1 的失败事件被新 run 的 available 事件替代，无关事件不变。
6. full/enforce 必须携带、保存并服务端验证明确的合格校准 ID；#1 和任何技术失败 run 都返回 409。
7. 校准/重试期间没有新增 face_exclusions、cluster_status=excluded、Face 重建或全库聚类。
8. 后端全量测试、前端测试/类型检查/构建和 ML 测试全部通过；生产恢复结果记录在 Task 6。

## 7. 上线、回滚与不包含

### 上线顺序

1. 部署后端自动迁移、服务/API 和 ML 客户端修复；确认 #1 已标为 completed_with_errors，无法创建 full/enforce。
2. 部署前端，确认状态卡和 retry/enforce 选择均由后端结果驱动。
3. 执行 #1 的 shadow retry 校准；业务抽样通过后才允许显式 full/enforce。

### 回滚

- shadow retry 异常时暂停或取消新 retry run；它不产生自动隔离，保留失败审计继续排查。
- 不回滚或删除 #1、retry run、item 或事件；它们是审计事实。
- full/enforce 已经由业务明确启动且出现误隔离时，使用既有按 run restore-auto；恢复到 pending，不恢复旧 person_id。
- 数据库备份恢复是最后手段，不能用作常规重试。

### 不包含

- 不调整质检阈值、ML 模型、IoU 阈值、质量原因码或自动排除策略。
- 不将 retryable/unmatched 变成人工审核任务，也不要求人工逐张审核失败队列。
- 不重跑普通人脸识别、照片扫描、缩略图全量重建、embedding 或全库聚类。
- 不修改已完成的详情图片、分页、全选/反选、Shift 连选等审核页体验需求，除本计划所需的运行状态卡和 full/retry 控制。
- 不自动创建 full/enforce，也不允许浏览器状态绕过服务端门禁。

---

## 8. 实施实际结果（2026-08-12）

### 本地全量验证（Step 1）

- 后端：`cd backend && go test ./...` —— 全部通过（service 24.9s、repository 5.3s、pkg/database 3.6s、handler/router/middleware 等均 ok）。
- 前端：`cd frontend && npm test` —— 11 文件 211 测试全过；`npm run build`（vue-tsc -b 类型检查 + 构建）成功。
- ML：`cd ml-service && pytest -q` —— 15 passed。

### 任务交付摘要

- **Task 1**：`snapshotTargets` 临时 row 的 BBoxX/Y/Width/Height 补 `gorm:"column:bbox_*"` 标签，消除零框；新增 `isValidNormalizedBBox` 在 `processOneBatch` 调 ML 前阻断非法项（写 retryable_error + `last_error=invalid normalized bbox`，事件转 historical_rescore + retryable_error，不调 ML）。测试 `TestRescore_CreateCalibrationFreezesTargets`（断言四字段与 baseline 一致且 >0）与 `TestRescore_InvalidBBoxBlockedBeforeML`（fake ML callCount=0）均先红后绿。
- **Task 2**：新增 `completed_with_errors` 终态与 `SupersededManualCount`/`RetryOfRunID`/`CalibrationRunID` 三字段；`refreshRunCounts` 重定义计数（processed 仅含 processed、retryable 含 unmatched、灰区仅计 available+review_required）；`maybeCompleteRun` 按 retryable_count 选 completed 或 completed_with_errors；`migrateFaceQualityRescoreRunMeta` 幂等修复线上 #1（completed+retryable>0 → completed_with_errors，空 last_error 从失败 item 回填）。
- **Task 3**：repo 新增 `ListRetryableTargets`（按 rescore_run_id + is_current + historical_rescore + retryable/unmatched 窄查询）与 `CountPendingOrProcessing`；service 新增 `RetryRun(sourceRunID)`（只快照来源 run 当前失败事件，新建 calibration+shadow+retry_of_run_id，空集合 409）；handler 新增 `RetryFaceQualityRescoreRun` 与 `POST /rescore-runs/:id/retry` 路由，错误码 `RESCORE_RETRY_SOURCE_INVALID`。
- **Task 4**：`CreateRun` 签名加 `calibrationRunID uint`，full/enforce 必须指定合格校准 run（`getEligibleCalibration` 逐项验证：calibration+shadow+completed、target>0、retryable=0、processed+superseded=target、无 pending/processing）；`HasCompletedCalibration` 降级为 deprecated；handler 列表/详情填充 `eligible_for_enforce`；DTO 新增 `calibration_run_id`、`retry_of_run_id`、`superseded_manual_count`、`eligible_for_enforce`。
- **Task 5**：前端类型扩展（`completed_with_errors`、`eligible_for_enforce` 等）；状态卡改用互斥计数（已获证据/人工覆盖/待重试/真实灰区/自动隔离/终态照片）；completed_with_errors 显示“完成但有错误”+重试入口；full/enforce 改用 el-select 选择后端 eligible_for_enforce=true 的校准 run，禁止自由输入；提交 body 含 `calibration_run_id`。Vitest 16 测试全过。

### 生产恢复演练状态

- Step 2/3（NAS 部署前检查与生产恢复）**尚未执行**——需在 NAS 上部署新版本后人工执行：备份 SQLite → 确认 #1 变 completed_with_errors → 对 #1 调 retry → 抽样核验无 422 → 业务 Go 后才 full/enforce。本会话仅完成代码与本地验证，生产操作待业务窗口。

