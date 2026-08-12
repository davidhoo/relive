# 人脸质检：历史证据补齐与审核体验改造 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让无模型证据的历史人脸不再占用人工审核队列；以不重建人脸、不全量重聚类的方式补齐模型质检证据，并完善审核页的预览、分页与批量选择体验。

**Architecture:** 当前 `face_quality_events` 的 `review_required` 同时混入“模型有证据的灰区”和“历史回填缺证据”两类记录。本计划给事件增加显式的证据来源/状态，按该元数据分流队列；另建可暂停、可恢复进度的历史重评分运行及目标快照，调用新增的“已知框评分”ML 接口，只更新证据、审计结论和必要的局部排除状态。它绝不调用 `ApplyDetectionResult`，从而不删除重建 `faces`、不重新分配人物。

**Tech Stack:** Go + Gin + GORM + SQLite、Vue 3 + TypeScript + Element Plus、Python FastAPI + InsightFace、Vitest、Go test、pytest。

---

## 1. 背景、已完成项与问题定义

### 1.1 已确认的生产事实

- NAS 中当前约有 **273,219 张 Face / 79,751 张照片**属于 `source=auto`、`decision=review_required` 且没有可解析 `evidence_json` 的历史回填记录；它们不是模型实际判为 0 分。
- 现有 `FaceQualityBackfill`（`backend/internal/service/face_quality_backfill.go`）只回放旧 `Face` 的快照、写审计事件，不改变人物归属。对 `QualityRuleVersion=='' && FaceValidityScore==0` 的旧记录会写 `review_required`，因而把“待补证据”错误混入“待人工审核”。
- 当前 ML 服务只有 `POST /api/v1/detect-faces`。普通人员强制重检最终会调用 `peopleService.ApplyDetectionResult`，该路径会删除并重建照片的 `faces`，继而影响人物归属和聚类，不能用于本任务。
- 既有人脸框与裁剪使用已旋转的展示缩略图坐标系。`peopleService.detectFacesLocally` 优先把该缩略图作为 Base64 传给 ML；历史重评分必须沿用同一坐标系。

### 1.2 已完成，保留且不重复实现

提交 `4b3e815` 已完成以下基线修复，本任务只补齐其遗留问题：

1. 详情图 URL 已改为受权限保护的 `GET /api/v1/photos/:id/thumbnail`，不再拼接 NAS 存储相对路径。
2. `quality_evidence_available` 已在后端按可解析的 `evidence_json` 计算；前端无证据显示“有效性未采集”，真实模型 0 分仍显示 `0%`。
3. 本页全选、反选、清空选择和跨已浏览页累计选择已存在。

本计划**不回退**上述行为。详情照片仍存在“有地址但显示不完整”的布局问题；选择框热区与卡片详情点击区重叠；固定每页 24 条、无 Shift 连续选择和队列混淆仍未解决。

## 2. 已确定目标与非协商约束

### 2.1 目标

1. 详情抽屉里的原照片横图、竖图均完整 `contain` 展示；点击选择热区永不打开详情。
2. 审核页可选每页 `24 / 48 / 96` 条，默认 48；支持本页全选、反选、跨页累计及 Shift 连续选择。
3. “待人工审核”只展示已有真实模型证据的灰区；历史缺证据改入“历史人脸待补证据”；无法完成重评分的技术问题进入独立“待重试/处理异常”。
4. 对历史缺证据人脸进行模型补证据：有效样本保留当前人物归属；灰区进入人工审核；高确定性非人脸/严重低质量可在受控全量阶段自动隔离且可按运行批次恢复。

### 2.2 强制边界

- 历史重评分**不得**调用 `ApplyDetectionResult`、`EnqueuePhoto(... force=true)`、删除/插入 `Face`、重建 embedding、更新 `photos.face_process_status`，或启动全库聚类。
- 不能把“模型没有找到与旧框匹配的人脸”、原图/缩略图不可读、ML 超时或 JSON 异常当作 `non_face`。这些只能进入可重试技术状态。
- 人工结论最高优先级。运行中某目标被人工处理后，worker 必须跳过该目标；不得用自动重评分覆盖人工接受、人工排除或人工恢复。
- 自动排除必须复用既有 `face_exclusions`、局部人物状态同步、身份画像失效和 merge-suggestion 脏标记链路；恢复后仍保持当前产品语义：Face 回 `pending`，**不**自动恢复旧 `person_id`。
- 首次校准批次固定为 `shadow`，只写证据和候选，不自动排除。只有人工核验校准结果并显式启动全量 `enforce` 运行后才允许自动隔离。

## 3. 数据模型、队列语义与兼容性

### 3.1 `face_quality_events` 新字段

在 `backend/internal/model/face.go` 的 `FaceQualityEvent` 添加以下字段，并在 `backend/pkg/database/database.go` 中实现幂等迁移和索引：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `evidence_origin` | `varchar(32)` | `realtime`、`historical_backfill`、`historical_rescore`。保留现有 `source=auto/manual` 的“谁作出最终结论”语义，不滥用 `source`。 |
| `evidence_state` | `varchar(24)` | `available`、`missing`、`retryable_error`、`unmatched`。它是队列分流的权威字段，不能根据分数推断。 |
| `rescore_run_id` | nullable `uint` | 产生该次历史重评分结论的运行 ID；实时与旧回填为空。 |

新增索引：`(is_current, evidence_origin, evidence_state, id DESC)`、`rescore_run_id`。字段默认空字符串仅为旧客户端/旧行兼容；新写入路径必须总是填写有效枚举。

### 3.2 旧数据一次性标记

在同一迁移中，以 `app_config` 幂等键 `migration.face_quality_evidence_origin_v1` 完成以下**一次性、可审计**分类：

```sql
UPDATE face_quality_events
SET evidence_origin = 'historical_backfill',
    evidence_state = 'missing'
WHERE is_current = 1
  AND source = 'auto'
  AND decision = 'review_required'
  AND TRIM(COALESCE(evidence_json, '')) = ''
  AND evidence_origin = '';
```

这条迁移的目标正是本次已核验的历史回填集合。部署完成后，`FaceQualityBackfill` 每次新写事件都必须显式写 `historical_backfill`：有有效 JSON 为 `available`，缺失 JSON 为 `missing`。实时检测/实时自动质检写 `realtime + available`；实时技术失败写明确的失败状态，不能留下空字段。

不得以 `face_validity_score=0`、`rule_version=v1` 或 `created_at` 时间范围判断历史来源。

### 3.3 审核页状态映射

扩展 `FaceQualityReviewQuery.State`、`FaceQualityState` 和统计响应。最终状态含义如下：

| 页面状态 | 精确过滤条件 | 是否要求人工逐张处理 |
| --- | --- | --- |
| `pending_review`（待人工审核） | `is_current=1 AND evidence_state='available' AND decision='review_required'` | 是 |
| `historical_missing_evidence`（历史人脸待补证据） | `is_current=1 AND evidence_origin='historical_backfill' AND evidence_state='missing'` | 否 |
| `rescore_retryable`（待重试/处理异常） | `is_current=1 AND evidence_origin='historical_rescore' AND evidence_state IN ('retryable_error','unmatched')` | 否，修复条件后重试 |
| `auto_excluded`（自动隔离） | `is_current=1 AND source='auto' AND decision IN ('non_face','low_quality')` | 否，可恢复 |
| `manual_confirmed`（已人工确认） | `is_current=1 AND source='manual'` | 已完成 |

`FaceQualityStatsResponse` 和前端 `FaceQualityStats` 增加 `historical_missing_evidence`、`rescore_retryable`。标题和 Tab 的数量必须使用这些新字段；原 `pending_review` 数量不得再包含历史缺证据样本。

### 3.4 历史重评分运行与目标快照

新增两张表，避免只用 `app_config` 的单一游标导致无法复现、恢复或审计：

1. `face_quality_rescore_runs`
   - `id`、时间戳、`mode`（`calibration`/`full`）、`apply_mode`（`shadow`/`enforce`）、`status`（`queued`/`running`/`paused`/`completed`/`failed`/`cancelled`）；
   - `target_photo_count`、`target_face_count`、`processed_photo_count`、`processed_face_count`、`accepted_count`、`review_required_count`、`auto_excluded_count`、`retryable_count`、`last_error`、`started_at`、`completed_at`；
   - 校准选择策略/种子和当次 rule/model 版本快照。
2. `face_quality_rescore_items`
   - `run_id`、`photo_id`、`face_id`、开始时快照的归一化 BBox、`baseline_event_id`、`status`（`pending`/`processing`/`processed`/`superseded_manual`/`retryable_error`/`unmatched`）、`attempt_count`、`last_error`、`matched_iou`、时间戳；
   - `(run_id, face_id)` 唯一，`(run_id, status, photo_id)` 索引。

创建运行时只快照当前 `historical_backfill + missing` 的 Face，不扫描已人工确认/已自动隔离/已完成重评分的记录。按照片处理，但每个 Face 的 BBox 与起始事件 ID 都必须保存在 item 中。这样，暂停、重启、人工并发操作和回滚都不会改变该运行的目标集合。

## 4. ML 重评分设计

### 4.1 新增内部 ML 接口，不重用强制重检写路径

新增 `POST /api/v1/score-known-faces`，只供后端 ML client 调用，不暴露给浏览器。

请求：

```json
{
  "image_base64": "...",
  "targets": [
    {"face_id": 42, "bbox": {"x": 0.12, "y": 0.20, "width": 0.18, "height": 0.25}}
  ]
}
```

响应按 target 保序：

```json
{
  "results": [
    {
      "face_id": 42,
      "status": "matched",
      "matched_iou": 0.81,
      "evidence": {"face_validity_score": 0.93, "...": "..."},
      "quality_score": 0.88
    }
  ],
  "rule_version": "v1",
  "model_version": "insightface-buffalo-sc-v1"
}
```

ML 端仍在同一张已经 EXIF/手动旋转校正的展示图上运行 InsightFace，使用检测结果和既有 `FaceDetector._build_evidence` 生成证据；再与请求的 BBox 做一对一最高 IoU 匹配。阈值与现有 `exclusionIoUThreshold=0.3` 保持一致。没有匹配、一个检测竞争多个目标、图像读取失败或推理异常，都返回具体非判定状态，不能伪造空证据或自动判为非人脸。

后端新增 `ScoreKnownFaces` 到 `backend/internal/mlclient/client.go` 与 `PeopleMLClient`。历史 worker 使用 `peopleService.displayThumbnailPath` 优先读取展示缩略图并发送 Base64；缩略图缺失时使用现有 `ImageProcessor.ProcessForAI` 的定向、缩放输出。不得直接把未经方向校正的原图文件路径和旧框混用。

### 4.2 结果写入规则

每个已匹配且有可解析 evidence 的 item，在一个受写队列保护的短事务中：

1. 重新读取对应 Face、当前事件和 item；若当前事件已是 `source=manual` 或 `baseline_event_id` 不再是目标当前结论，标为 `superseded_manual`，不覆盖。
2. 只更新现有 Face 的质检快照字段：`face_validity_score`、`quality_score`、`quality_reasons`、`quality_rule_version`、`quality_model_version`。不得改 BBox、embedding、缩略图、`person_id`、`cluster_status`。
3. 调用既有 `evaluateFaceQuality(evidence, run.apply_mode)`，写一条 `source=auto`、`evidence_origin=historical_rescore`、`evidence_state=available`、`rescore_run_id=run.ID` 的追加审计事件。仅匹配的旧历史缺证据事件置为 `is_current=false`。
4. `accepted`：保留 Face 当前人物归属和聚类状态；只完成证据补齐。
5. `review_required`：保留人物归属和聚类状态，成为真正的人工审核项。不得因灰区把历史已归属 Face 清空或重聚类。
6. `exclude`：仅在 `apply_mode=enforce` 时，复用当前 `faceQualityService` 内的排除事务逻辑，创建/更新 `face_exclusions`，清空该 Face 的 `person_id`，置 `cluster_status=excluded`，并在事务后只刷新受影响人物、照片计数、画像/ANN/proto cache 与 merge suggestion。

`shadow` 运行即使策略结果为高置信排除，也只写 `review_required` 候选事件/统计，`auto_excluded_count` 必须为 0。

对于 `unmatched`、文件缺失、缩略图生成失败、ML 超时或不可解析响应：不改 Face 数值快照、不使旧事件失效；写对应 item 失败状态，并将其当前事件标为 `historical_rescore + retryable_error|unmatched`，使其从“历史待补证据”移入“待重试/处理异常”。重试成功后才将该失败事件置为非当前并写真实 evidence 事件。

### 4.3 调度、吞吐与安全

- 新建 `FaceQualityRescoreWorker`，每次只领取一个照片的小批 item。它必须注册 `BackgroundTaskClass`（新增 `face_quality_rescore`）并以 `automatic` 优先级接受 `BackgroundTaskCoordinator` 的前台、CPU、iowait、内存、SQLite busy cooldown 限制。
- 每张照片完成后持久化 item/run 计数；暂停只在照片批次边界生效。进程重启时 `processing` item 回到 `pending`，不丢失进度。
- 同时只允许一个 `running` 或 `paused` run；创建第二个运行返回 409。用户可暂停/继续；取消只停止未处理 item，不删除审计记录。
- 默认单照片串行；并发参数不可由浏览器任意传入。校准完成后，根据 NAS 负载和 ML 容器延迟设定受配置约束的并发，且不超过 1，直到另有基准证明可安全提高。
- 每 100 张照片记录一次结构化日志：run ID、照片/Face 进度、匹配/灰区/排除/失败数量、平均 ML 耗时、最近 backpressure 原因；日志不得写图像 Base64、完整路径或人脸 embedding。

## 5. 交互和前端需求

### 5.1 详情图片完整展示

修改 `frontend/src/views/People/FaceQualityReview.vue`：

- 继续使用现有 `photoThumbnail(photoId, eventId)` 和受保护缩略图接口；不使用 `photo_thumbnail`/`photo_file_path` 作为浏览器 URL。
- 用确定高度的 `.detail-photo-frame`（桌面 220px；窄抽屉可降到 180px）包住 `<el-image>`，移除只限制 `max-height: 120px` 的布局。
- `.detail-photo` 与 `:deep(.el-image__inner)` 均为 `width: 100%; height: 100%; object-fit: contain`；背景留白可见，不允许 `cover`、overflow 裁切或按原始高度挤压抽屉。
- 保留图片加载失败态和 `/photos/:photo_id` 链接；添加 Element Plus 预览，预览源使用受保护的 `/photos/:id/image`，不把文件路径暴露到 DOM。

### 5.2 分页和时间文案

- `pageSize` 改为 `ref`，可选 `24 / 48 / 96`；未存储偏好时默认 `48`，使用 `localStorage` 保存用户选择。
- `el-pagination` 启用 `sizes` 和 `@size-change`；切换页大小时回到第 1 页、保留当前 Tab 与筛选、保留已选 ID、重置 Shift 锚点。
- 后端已有 `parsePagination` 和 repository 最大 200 的限制，保持不变；前端不提供大于 96 的选项。
- 时间筛选前增加文字“质检事件时间”，placeholder 改为“事件开始时间 / 事件结束时间”，并在控件旁标注“不是照片拍摄时间”。`start_time`/`end_time` 仍映射 `face_quality_events.created_at`，不改变 API 语义。

### 5.3 Tab、统计和重评分控制

- Tabs 顺序：`待人工审核`、`历史人脸待补证据`、`待重试/处理异常`、`自动隔离`、`已人工确认`。各标签显示对应统计数。
- “历史人脸待补证据”空态解释“此类记录没有模型证据，不需要人工逐张确认”。
- 增加“历史补证据任务”状态卡：显示运行 mode、状态、目标/已处理照片和 Face 数、候选/自动隔离/失败数、最近错误、暂停/继续按钮。创建校准任务必须二次确认并固定显示“只写证据，不自动排除”。
- 全量 `enforce` 启动入口必须要求输入本次 calibration run ID，并显示“仅高置信非人脸/严重低质量会移出人物聚类；可按本运行恢复”。不提供“启动后自动全量运行”的链路。

### 5.4 选择热区与 Shift 连选

保留点击卡片图片/其余大区域打开详情，但替换目前左上角狭小的绝对定位 `el-checkbox` 为独立选择热区：

- 使用语义正确、可聚焦的 button（`role="checkbox"`、`aria-checked`、动态 `aria-label`）或等价原生 checkbox；可点击区域至少 `40 × 40px`，视觉 checkbox 居中，并有半透明圆形/方形底和 focus-visible 外框。
- 热区层级高于图片，点击、鼠标按下和键盘事件必须 `stopPropagation`；任意点在热区边缘也只能切换选择，不能执行 `openDetail`。
- 普通点击：切换该项并把其设为 `selectionAnchorId`。
- Shift 点击：仅当锚点和目标都在当前 `items`、当前排序序列中时，按两者 index 的闭区间批量操作；目标原本未选则整段选中，目标原本已选则整段取消。页外已选项不变。
- 翻页、切 Tab、筛选、排序、刷新、页大小变更、批量操作成功和清空选择时重置锚点；翻页和页大小变更不清除既有 `selectedIds`。
- `Space` 与 `Enter` 在热区聚焦时执行同一选择逻辑；键盘路径没有 Shift 锚点时按普通选择处理。

## 6. API 契约

### 6.1 读取接口扩展

`GET /api/v1/people/face-quality/reviews`：

```text
state = pending_review | historical_missing_evidence | rescore_retryable |
        auto_excluded | manual_confirmed
```

每个 `items[]` 增加向后兼容字段：

```json
{
  "evidence_origin": "historical_rescore",
  "evidence_state": "available",
  "rescore_run_id": 7,
  "quality_evidence_available": true
}
```

`GET /api/v1/people/face-quality/stats` 增加：

```json
{
  "historical_missing_evidence": 0,
  "rescore_retryable": 0
}
```

### 6.2 新增运行接口

```text
POST /api/v1/people/face-quality/rescore-runs
GET  /api/v1/people/face-quality/rescore-runs
GET  /api/v1/people/face-quality/rescore-runs/:id
POST /api/v1/people/face-quality/rescore-runs/:id/pause
POST /api/v1/people/face-quality/rescore-runs/:id/resume
POST /api/v1/people/face-quality/rescore-runs/:id/cancel
POST /api/v1/people/face-quality/rescore-runs/:id/restore-auto
```

创建请求：

```json
{"mode":"calibration","photo_limit":1000}
```

校准请求一律被服务端归一化为 `apply_mode=shadow`；`mode=full, apply_mode=enforce` 仅在提供已经 `completed` 的 calibration run ID 时接受。服务端应在创建时返回运行 ID、实际快照的照片/Face 数以及 apply mode，前端不得自行推算。

`restore-auto` 只能恢复 `rescore_run_id=:id AND source='auto' AND decision IN ('non_face','low_quality')` 的自动排除，不能批量恢复同一 rule version 下其他实时自动结论。

## 7. 实施任务（测试先行）

### Task 1：为证据来源和历史队列建立迁移

**Files:**

- Modify: `backend/internal/model/face.go`
- Modify: `backend/pkg/database/database.go`
- Modify: `backend/pkg/database/database_test.go`
- Modify: `backend/internal/service/face_quality_backfill.go`
- Test: `backend/internal/service/face_quality_backfill_test.go`

**Step 1:** 写失败的迁移测试：旧空 evidence 的 auto/review_required 事件迁移为 `historical_backfill/missing`；有 evidence、手工事件、未来 realtime 空错误事件不被该 SQL 误标。

**Step 2:** 运行：

```bash
cd backend && go test ./pkg/database -run FaceQualityEvidenceOrigin -count=1
```

预期：失败，字段/迁移尚不存在。

**Step 3:** 添加模型字段、AutoMigrate 及带 `app_config` 标志的幂等迁移；为回填新事件写明确 origin/state。

**Step 4:** 运行上述测试及：

```bash
cd backend && go test ./internal/service -run FaceQualityBackfill -count=1
```

预期：通过，且回填仍不改变 Face 的 person/cluster 状态。

### Task 2：按显式证据状态分流审核查询与统计

**Files:**

- Modify: `backend/internal/model/dto.go`
- Modify: `backend/internal/repository/face_quality_repo.go`
- Modify: `backend/internal/service/face_quality_service.go`
- Modify: `backend/internal/service/face_quality_service_test.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`

**Step 1:** 写失败测试，覆盖五个 state 的互斥过滤及统计：历史 missing 不计入 pending_review；具有可解析证据的 review_required 才计入 pending；rescore 技术失败只进入 retryable。

**Step 2:** 实现 repository 的 origin/state 过滤、DTO、统计字段和 `repositoryFaceQualityQuery` 映射。保持旧 `quality_evidence_available` 的真实 0 分语义。

**Step 3:** 运行：

```bash
cd backend && go test ./internal/service -run 'FaceQuality.*(Review|Stats|Evidence)' -count=1
```

**Step 4:** 提交：

```bash
git add backend/internal/model/face.go backend/internal/model/dto.go backend/pkg/database/database.go backend/pkg/database/database_test.go backend/internal/repository/face_quality_repo.go backend/internal/service/face_quality_backfill.go backend/internal/service/face_quality_backfill_test.go backend/internal/service/face_quality_service.go backend/internal/service/face_quality_service_test.go backend/internal/api/v1/handler/people_handler.go
git commit -m "feat(people): split historical face-quality evidence queue"
```

### Task 3：实现仅评分已知人脸框的 ML 协议

**Files:**

- Modify: `ml-service/app/schemas.py`
- Modify: `ml-service/app/models/face.py`
- Modify: `ml-service/app/routers/faces.py`
- Test: `ml-service/tests/test_face_router.py`
- Modify: `backend/internal/mlclient/client.go`
- Test: `backend/internal/mlclient/client_test.go`
- Modify: `backend/internal/service/people_service.go`

**Step 1:** 写 pytest：空白图 target 返回 `unmatched`；可控 mock 检测结果以最大 IoU、一对一规则匹配目标；响应含 evidence 和匹配 IoU。

**Step 2:** 先运行：

```bash
cd ml-service && pytest tests/test_face_router.py -q
```

预期：失败，因为接口和 schema 尚不存在。

**Step 3:** 以已有 `FaceDetector._detect_faces`、`_build_evidence` 为唯一证据生成实现新 endpoint；禁止复制另一套质量阈值。然后扩展 Go client 和 `PeopleMLClient`。

**Step 4:** 为 `ScoreKnownFaces` 写 Go HTTP 请求/响应/超时测试，再运行：

```bash
cd backend && go test ./internal/mlclient -count=1
cd ../ml-service && pytest tests/test_face_router.py tests/test_face_model.py -q
```

### Task 4：实现可恢复的历史重评分运行与 worker

**Files:**

- Create: `backend/internal/model/face_quality_rescore.go`
- Create: `backend/internal/repository/face_quality_rescore_repo.go`
- Create: `backend/internal/repository/face_quality_rescore_repo_test.go`
- Create: `backend/internal/service/face_quality_rescore.go`
- Create: `backend/internal/service/face_quality_rescore_test.go`
- Modify: `backend/internal/service/background_task_coordinator.go`
- Modify: `backend/internal/service/service.go`
- Modify: `backend/cmd/relive/main.go`

**Step 1:** 写失败测试：创建 calibration 时冻结 1,000 张照片的历史缺证据目标；暂停/重启后 item 能恢复；已有人工结论的 item 标为 superseded；unmatched 不排除；shadow 永不排除；enforce 高置信排除只影响关联人物且不触发重新检测。

**Step 2:** 实现 run/item 模型、查询/领取和短事务。worker 从 `displayThumbnailPath` 或 `ProcessForAI` 读取输入，调用 `ScoreKnownFaces`；不要调用 `detectFacesLocally` 后的 `ApplyDetectionResult`。

**Step 3:** 将自动排除的公共事务从人工动作中提取为内部 helper，确保 run 级恢复可精确回滚；加入 `face_quality_rescore` coordinator class、iowait/前台/cooldown 让步、单 run 互斥和进度日志。

**Step 4:** 运行：

```bash
cd backend && go test ./internal/repository -run FaceQualityRescore -count=1
cd backend && go test ./internal/service -run 'FaceQualityRescore|FaceQuality.*Restore' -count=1
```

**Step 5:** 提交：

```bash
git add backend/internal/model/face_quality_rescore.go backend/internal/repository/face_quality_rescore_repo.go backend/internal/repository/face_quality_rescore_repo_test.go backend/internal/service/face_quality_rescore.go backend/internal/service/face_quality_rescore_test.go backend/internal/service/background_task_coordinator.go backend/internal/service/service.go backend/cmd/relive/main.go backend/internal/service/face_quality_service.go backend/internal/service/face_quality_service_test.go
git commit -m "feat(people): add controlled historical face-quality rescore"
```

### Task 5：暴露并保护重评分运行 API

**Files:**

- Modify: `backend/internal/model/dto.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/handler.go`
- Modify: `backend/internal/api/v1/router/router.go`
- Test: `backend/internal/api/v1/handler/people_handler_test.go`（若缺失则新建同目录专用测试）

**Step 1:** 写 handler 测试：校准强制 shadow；没有完成 calibration 的 full/enforce 返回 409；第二个活跃 run 返回 409；pause/resume/restore-auto 不越权影响其他 run。

**Step 2:** 实现 DTO、服务注入、路由和 handler。返回统一 `model.Response`，错误码区分 `RESCORE_RUN_CONFLICT`、`RESCORE_CALIBRATION_REQUIRED`、`RESCORE_NOT_FOUND`。

**Step 3:** 运行：

```bash
cd backend && go test ./internal/api/v1/handler -run FaceQualityRescore -count=1
```

### Task 6：完善审核页展示、分页、选择和运行面板

**Files:**

- Modify: `frontend/src/types/people.ts`
- Modify: `frontend/src/api/people.ts`
- Modify: `frontend/src/views/People/FaceQualityReview.vue`
- Modify: `frontend/src/views/People/FaceQualityReview.spec.ts`

**Step 1:** 先扩展失败测试：

1. 横图和竖图的详情 `.el-image__inner` 均处于确定高度容器且 `object-fit: contain`；不再断言仅 URL 存在。
2. 单击 40px 选择热区边缘不调用 `openDetail`；单击图片非热区调用一次 `openDetail`。
3. 普通选择建立锚点；Shift 选中与 Shift 取消当前页闭区间；翻页后保留选中但重置锚点。
4. `Space`/`Enter` 可选择；筛选/Tab/刷新/成功批量操作清空选择和锚点。
5. 默认请求 `page_size=48`；修改为 96 后重置 page=1、保留筛选和已选 ID。
6. 各队列 Tab 使用正确 `state`；历史/失败队列文案不显示为人工待审核。

**Step 2:** 运行：

```bash
cd frontend && npm test -- --run src/views/People/FaceQualityReview.spec.ts
```

预期：因新增行为尚未实现而失败。

**Step 3:** 最小实现 UI 与 API 类型；详情预览改为固定 frame；选择控件使用独立可访问热区；添加 rescore run 状态卡和严格的校准/全量确认对话。

**Step 4:** 运行：

```bash
cd frontend && npm test -- --run src/views/People/FaceQualityReview.spec.ts
cd frontend && npm run typecheck
cd frontend && npm run build
```

**Step 5:** 提交：

```bash
git add frontend/src/types/people.ts frontend/src/api/people.ts frontend/src/views/People/FaceQualityReview.vue frontend/src/views/People/FaceQualityReview.spec.ts
git commit -m "feat(people): improve face-quality review workflow"
```

### Task 7：全量回归、迁移演练与校准上线

**Files:**

- Modify: `docs/plans/2026-08-12-face-quality-historical-evidence-and-review-experience.md`（只记录实际校准结果和 Go/No-Go 结论）

**Step 1:** 在本地 SQLite fixture 中混入历史空证据、真实 0 分、有人工结论、原图缺失和一张多脸照片，跑 AutoMigrate 两次，验证迁移幂等和所有不变量。

**Step 2:** 运行完整测试：

```bash
cd backend && go test ./...
cd frontend && npm test && npm run typecheck && npm run build
cd ml-service && pytest -q
```

**Step 3:** 生产前创建一致性 SQLite 备份（含 WAL），验证 `PRAGMA integrity_check`，再按“上线顺序”部署。先只创建 1,000 照片的 calibration/shadow run，不允许在未审阅结果时开启 full/enforce。

**Step 4:** 记录 calibration 的 run ID、目标照片/Face 数、匹配率、retryable/unmatched 比例、灰区/候选排除抽样以及 NAS CPU/iowait/ML 耗时。由业务方明确 Go 后，才创建 full/enforce run。

## 8. 验收标准

1. 历史无证据项不再进入“待人工审核”，统计中 `pending_review` 与 `historical_missing_evidence` 数量相互独立；真实模型 0 分仍显示 0%。
2. 详情照片通过 `/photos/:id/thumbnail` 完整显示，横图竖图没有裁切；预览失败态与照片详情入口可用。
3. 选择热区至少 40px，点热区的任意位置不打开详情；图片其他区域可打开详情；Shift 区间、键盘与跨页选择符合第 5.4 节。
4. 每页可选 24/48/96，默认 48，后端仍拒绝或截断大于 200 的请求，切换页大小不会丢失筛选。
5. 校准 run 对任何样本都不产生 `face_exclusions` 或 `cluster_status=excluded` 的新变化；其结果可复查。
6. full/enforce run 仅对高确定性结果自动隔离；有效样本和灰区不改 `person_id`、BBox、embedding 或聚类状态；失败/未匹配样本永不自动排除。
7. 人工在 worker 运行中作出结论时，该 Face 不会被自动结果覆盖。
8. 标准照片重检和全库聚类在整个运行期间均没有被触发；自动排除只产生关联人物的局部刷新。
9. 可以按 rescore run 精确恢复自动隔离，且恢复不会影响实时自动隔离、人工结论或其他 run。

## 9. 上线、监控与回滚

### 上线顺序

1. 对 NAS 数据库做可校验的一致性备份；记录运行前的队列数量和 `face_quality_events` 统计。
2. 部署含 `score-known-faces` 的 ML 服务，health check 后部署后端迁移和 API，最后部署前端。
3. 验证迁移只标记目标历史集合、UI 队列数量符合预期、未创建 rescore run 时没有额外模型调用。
4. 从 UI/API 创建一次 `calibration + shadow + 1000 photos`；运行完成后人工抽样核验。
5. 仅在明确 Go 后启动 `full + enforce`；运行期间持续观察 iowait、SQLite busy、ML 延迟、retryable 比例和人物前台操作延迟，任一异常可暂停。

### 回滚

- UI/API 代码回滚不删除新字段或审计事件；旧客户端忽略新增响应字段。
- 校准 run 没有排除副作用，取消或保留记录均可。
- full/enforce 出现误杀时，先暂停 run，再调用该 run 的 `restore-auto`；恢复使用现有“回 pending、不恢复旧 person_id”语义。
- 若需要回到运行前的所有数据库状态，使用已验证的一致性备份，并在恢复前停服务；这是最后手段，不替代 run 级恢复。

## 10. 不包含

- 不重新设计 People 列表、人物详情、合并建议页面或截图排除规则。
- 不改变现有质量阈值、`quality_score` 的定义、实时质检策略，或用历史结果训练/微调模型。
- 不实现跨“全部筛选结果”的服务端全选快照；本任务的选择范围仍是用户已浏览页面的 ID 集合。
- 不物理删除照片、人脸、人物、embedding、历史质检事件或缩略图。
- 不自动启动全量 enforce，也不承诺未经校准的历史队列全部可被模型判定。
- 不通过浏览器暴露 NAS 绝对路径、原始图片 Base64、Face embedding 或 ML 内部文件路径。

---

## 11. 实施记录与校准上线（Task 7）

### 11.1 实施完成状态

Task 1–6 全部实现并提交，全量回归绿：

- **后端** `go test ./...`：全部包通过（含 `pkg/database`、`repository`、`service`、`api/v1/...`、`mlclient`、`cmd/relive`）。
- **前端** `npm test`：11 文件 209 测试通过；`npm run typecheck` 通过；`npm run build` 通过（`FaceQualityReview.vue` 独立 chunk 20.84 kB）。
- **ML** `pytest -q`：15 测试通过。

### 11.2 迁移演练

本地 SQLite fixture（历史空证据 + 真实 0 分 + 人工确认）跑 `Init` + 二次 `AutoMigrate`：
- 两次迁移无报错，幂等。
- `face_quality_rescore_runs` / `face_quality_rescore_items` 表存在。
- `PRAGMA integrity_check` = `ok`。
- 一次性 backfill 行为（historical_backfill/missing 标记、available state 回填）由 `TestMigrateFaceQualityEvidenceOrigin` 单测覆盖。

### 11.3 校准上线 Go/No-Go

**状态：代码就绪，等待生产部署与业务方 Go。** 尚未在生产 NAS 执行校准 run。

上线前必做（由运维/业务方执行，非代码任务）：
1. NAS 数据库一致性备份（含 WAL），`PRAGMA integrity_check` 验证。
2. 记录运行前队列数量与 `face_quality_events` 统计（预期 `historical_missing_evidence ≈ 273k Face / 79.7k 照片`）。
3. 部署含 `score-known-faces` 的 ML 服务 → 后端迁移+API → 前端。
4. 验证迁移只标记目标历史集合、UI 队列数量符合预期、未创建 rescore run 时无额外模型调用。
5. 创建一次 `calibration + shadow + 1000 photos`，完成后人工抽样核验。

校准 run 完成后需记录（填入下方，由业务方提供）：
- run ID：______
- 目标照片/Face 数：______
- 匹配率（matched / target）：______
- retryable / unmatched 比例：______
- 灰区 / 候选排除抽样：______
- NAS CPU / iowait / ML 平均耗时：______

**No-Go 条件**（任一命中则不启动 full/enforce）：
- 匹配率异常低（大面积 unmatched，说明坐标系或缩略图问题）。
- retryable 比例高（ML 超时/读图失败普遍）。
- iowait / SQLite busy / 人物前台操作延迟异常。
- 校准抽样发现灰区判定与人工预期不符。

业务方明确 Go 后，方可创建 `full + enforce` run；运行期间持续观察上述指标，任一异常可暂停，并用该 run 的 `restore-auto` 回滚自动隔离。

### 11.4 已知遗留（非阻塞）

- **Retryable items 无重试入口**：`markBaselineEventFailed` 把 baseline 从 `historical_backfill/missing` 改成 `historical_rescore/retryable_error|unmatched`，但新 run 的 `snapshotTargets` 只找 `historical_backfill+missing`。这是计划层设计缺口（codex 第 4 轮 review 指出），需后续补「按 run 重试」或「retryable 重新入队」入口。不影响首期校准/全量运行。
- **`prepareScoreImageBase64` 测试注入**用包级变量 `imageForTest`，生产路径 nil 走真实缩略图/`ProcessForAI`。service 测试串行，无并发隐患；若后续并行化需改构造函数注入。

