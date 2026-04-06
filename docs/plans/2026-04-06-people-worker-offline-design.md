# People Worker Offline Design

**Date:** 2026-04-06

**Problem**

当前人物处理链路默认由 NAS 上的后端直接调用 `relive-ml` 做人脸检测与 embedding 提取。这个模式在“`relive-ml` 跟后端部署在同机或同 Compose 网络”时可用，但它天然偏向 NAS 本地算力。

这带来两个现实问题：

- NAS 的 CPU/GPU 往往弱于本地 Mac，人物检测与 embedding 提取会成为人物处理链路中最重的一段。
- 现有 HTTP 接入虽然已经通过 `people.ml_endpoint` 做了进程外解耦，但实现上仍保留了本地文件路径 fallback，导致“跨机器常驻 ML 服务”仍隐含共享文件系统假设。

本设计不再优先解决“远端常驻 `relive-ml` 服务”问题，而是转向更贴合现有使用场景的方案：

- NAS 继续作为唯一业务后端和数据权威
- Mac 作为离线人物 worker，通过 API 从 NAS 拉任务、下载图片、做推理、提交结果

这与当前 `relive-analyzer` 的 API 模式一致，符合“照片存储在 NAS，算力在另一台机器”的实际部署方式。

**Goals**

- 让 Mac 承担人物检测与 embedding 提取的主要算力负载。
- 保持 NAS 后端对 `faces`、`people`、`people_jobs`、缩略图和聚类状态的唯一写入权。
- 复用现有 analyzer 的”任务领取 + 心跳 + 结果提交 + 租约恢复”交互模式。
- 保留现有 NAS 本地人物模式，不强制所有用户切到离线 worker。
- 让 worker 仅通过 API 工作，不依赖共享照片目录挂载。
- 修复离线 worker 前置依赖的已知 bug（幽灵人物、聚类质量）。

**Non-Goals**

- 不引入向量数据库。
- 不把人物聚类迁移到 Mac。
- 不让 worker 直接访问 SQLite 或任何数据库。
- 不让 worker 生成人脸缩略图并回传文件。
- 不把 `relive-analyzer` 和人物 worker 合并成一个统一大 CLI。

## Alternatives Considered

### Option 1: 继续使用远端常驻 `relive-ml`

做法：

- 后端继续以 `people.ml_endpoint` 调用 `relive-ml`
- 只解决跨机器网络与鉴权问题

优点：

- 改动面最小
- 后端当前调用链可部分复用

缺点：

- 仍需要解决“远端是否能访问原图路径”的共享文件系统问题
- 仍是服务到服务的强耦合调用，不适合笔记本/桌面机按需上线离线跑任务
- 失败恢复、断网恢复、任务回收都不如 worker 模式自然

### Option 2: 扩展现有 `relive-analyzer`

做法：

- 在 `relive-analyzer` 中新增 `people` 子命令或双模式运行
- 复用现有配置、心跳、任务领取和下载逻辑

优点：

- 复用现有离线分析基础设施最多

缺点：

- 一个 CLI 同时承担照片内容分析和人物检测，语义开始混杂
- 配置项、运行时租约、日志和排障路径会越来越难理解
- 人物处理与 AI 文本分析虽然交互模式相似，但模型、结果结构和错误类型不同

### Option 3: 独立的 `people-worker` CLI

做法：

- 新增独立的离线人物 worker
- 交互模式参考 analyzer，但保持独立命名、独立配置、独立任务协议

优点：

- 角色单一，职责清晰
- 与现有人物系统边界吻合
- 可最大限度复用 analyzer 的协议思路，而不把两个领域强绑在一起

缺点：

- 需要新增一套 worker API 和 CLI 骨架

**Decision:** 采用 Option 3。

## Approved Approach

新增一个运行在 Mac 上的 `people-worker`：

1. 通过 API Key 向 NAS 后端获取人物运行租约
2. 拉取待处理人物任务
3. 下载照片原图
4. 调用本机 `relive-ml` 做人脸检测与 embedding 提取
5. 将结构化检测结果提交回 NAS

NAS 后端继续负责：

- 任务排队与租约管理
- 结果验收与落库
- 人脸缩略图生成
- 增量聚类
- 人物状态同步
- 手工纠错后的重聚类

核心原则是：

**把最重的推理放到 Mac，把最依赖数据库状态和本地存储的业务编排留在 NAS。**

## Prerequisite Fixes

离线 worker 依赖人物处理链路的正确性。以下三个已知问题必须在 worker 开发前修复，否则 worker 提交的结果也会产生同样的问题。

### Bug 1: 幽灵人物（Ghost Person）

**现象**：人物列表显示"未命名人物 #719 路人，234 张照片 534 人脸"，点进去 0 照片 0 人脸。

**根因**：`processJob` 中 ML 检测返回 0 张人脸时（`people_service.go:683-702`）：
1. 删除该照片的所有旧 `faces` 记录
2. 更新 photo 状态为 `no_face`
3. 标记 job completed
4. **直接 return**

但 `previousPersonIDs` 的计算和 `syncPersonState` 循环在 return 之后（line 704+），永远不会执行。失去所有人脸的 person 变成幽灵——`people` 表的 `face_count`/`photo_count` 缓存值未刷新，person 也未被清理。

**修复**：
- 将 `previousPersonIDs := personIDsFromFaces(existingFaces)` 移到 zero-faces 分支之前
- 在 zero-faces 分支的 return 前加入 `syncPersonState` 循环
- 同时检查 line 706-753 之间的错误返回路径，确保 faces 已删除的情况下也触发 syncPersonState

### Bug 2: 垃圾人物聚类（Garbage Cluster Reformation）

**现象**：某个人物下几百个人脸样本，互相差异极大，根本不是同一个人。解散后重新扫描，又会慢慢聚成类似的垃圾人物。

**三重根因叠加**：

1. **聚类阈值严重偏离设计值**

   | 参数 | 设计文档值 | 实际代码值 |
   |------|-----------|-----------|
   | `peopleLinkThreshold`（建边） | 0.72 | **0.35** |
   | `peopleAttachThreshold`（挂靠） | 0.86 | **0.50** |

   0.35 的余弦相似度意味着几乎任何两张脸都能建边。通过连通分量的传递性（A→B→C→...），完全不相关的人脸被链成一个巨型连通分量 → 创建为一个垃圾 person。

2. **解散后无防重聚约束**
   - `DissolvePerson` 将所有人脸设为 `cluster_status=pending, person_id=NULL`
   - 同时**删除**该人物的所有 `cannot_link` 约束
   - 重置 `recluster_generation=0`
   - 系统完全失忆，无法阻止同一批人脸重新聚在一起

3. **`runIncrementalClustering` 一次处理所有 pending 人脸**
   - `ListPending(0)` 无上限，一次捞出全部 pending 人脸
   - 解散 200 张人脸 → 全部 pending → 0.35 阈值下建图 → 一个大连通分量 → 又创建同一个垃圾 person

**修复策略**：

- **提高聚类阈值**：`peopleLinkThreshold` 从 0.35 提升到 0.65（保守起步，避免一步到位 0.72 导致过度分裂）。`peopleAttachThreshold` 从 0.50 提升到 0.70。两个阈值改为配置项（`config.yaml` 的 `people` 段），方便调优。
- **解散后植入 `cannot_link` 种子**：`DissolvePerson` 不再删除 `cannot_link` 约束，而是从被解散的人脸中采样代表性人脸对，写入人脸级 `cannot_link`（当前 `cannot_link` 是 person 级，需要评估是否扩展到 face 级，或在解散时保留一个"虚拟 person ID"作为约束锚点）。**此项复杂度较高，可作为二期优化，一期先通过提高阈值缓解。**
- **可选**：`runIncrementalClustering` 对 pending 人脸做批次处理（每批 50-100），避免一次性构建超大图。但提高阈值后巨型连通分量问题应显著缓解。

### 人物扫描触发方式改进

**现状问题**：

当前人物扫描只有三种触发方式，缺少"只处理未检测照片"的非破坏性入口：

| 触发方式 | 入口 | source | 行为 |
|---------|------|--------|------|
| 照片扫描自动触发 | 新照片/hash 变化 | `scan` | 自动入队 + 自动启动后台 |
| 按路径人物重扫 | 照片管理页 → "人物重扫" | `manual` | 路径下所有照片重新入队 |
| 全量重建 | 人物管理页 → "全量重建" | `reset` | **破坏性**，删除所有人脸/人物数据 |

有了离线 worker 后，需要"批量首次检测"入口——把所有 `face_process_status=none` 的照片入队，然后由 Mac worker 消费。

**改进方案**：

1. **入队与执行分离**
   - 现有 `RescanByPath` 内部先入队再 `StartBackground()`，耦合在一起
   - 改为：入队只创建 `people_jobs`，不关心谁来执行
   - 本地后台 / 远程 worker 各自竞争 `global_people` lease 来消费

2. **新增 `EnqueueUnprocessed` 接口**

   ```go
   // 只入队 face_process_status = 'none' 的照片
   POST /api/v1/people/enqueue-unprocessed
   ```

   人物管理页新增"检测未处理照片"按钮（非破坏性，与"全量重建"并列）。

3. **前端感知 worker 模式**
   - 当远程 worker 持有 `global_people` lease 时，前端的"开始/停止"按钮应显示"远程 worker 运行中"
   - 进度展示改为读取 `people_jobs` 聚合统计（已有 `GetStats`），不再只看本地 task 内存状态

4. **触发场景总览**

   | 场景 | 操作 | 执行者 |
   |------|------|--------|
   | 日常新照片 | 扫描自动入队 + 自动启动本地后台 | NAS 本地 |
   | 批量首次检测 | 人物页点"检测未处理照片" → Mac 跑 worker | 离线 worker |
   | 按路径重扫 | 照片页"人物重扫"入队 → 谁持有 lease 谁执行 | 都可以 |
   | 全量重建 | 人物页"全量重建"（破坏性） | 都可以 |

## Architecture

### High-level Flow

1. NAS 扫描或手动触发后，创建 `people_jobs`
2. `people-worker` 获取全局人物运行租约
3. `people-worker` 拉取任务列表，后端为每个任务设置 worker 租约
4. `people-worker` 通过鉴权下载图片
5. `people-worker` 调用本机 `relive-ml`
6. `people-worker` 提交检测结果：
   - `bbox`
   - `confidence`
   - `quality_score`
   - `embedding`
   - `processing_time_ms`
7. NAS 后端在本地：
   - 清理该照片旧人脸记录
   - 写入新 `faces`
   - 生成 face thumbnail
   - 执行 `runIncrementalClustering`
   - 刷新 `photo.face_process_status` / `face_count` / `top_person_category`
   - 刷新 `person` 统计与代表脸

### Responsibility Split

**Mac `people-worker`**

- 任务拉取
- 下载原图
- 心跳续约
- 调用本机 `relive-ml`
- 提交检测结果
- 释放失败任务

**NAS backend**

- `people_jobs` 真正的队列状态机
- 结果校验与幂等处理
- `faces` / `people` / `photo` 写入
- 缩略图生成
- 增量聚类与反馈重聚类

**`relive-ml`**

- 保持纯推理服务
- 不直接接触任务队列
- 不直接写库

## API Design

第一版新增独立人物 worker API，语义参考 analyzer，但路径与 payload 独立：

- `POST /api/v1/people/runtime/acquire`
- `POST /api/v1/people/runtime/heartbeat`
- `POST /api/v1/people/runtime/release`
- `GET /api/v1/people/worker/tasks?limit=10`
- `POST /api/v1/people/worker/tasks/:task_id/heartbeat`
- `POST /api/v1/people/worker/tasks/:task_id/release`
- `POST /api/v1/people/worker/results`

认证方式沿用 analyzer：

- `Authorization: Bearer <api_key>`
- 或 `X-API-Key: <api_key>`

图片下载复用现有端点：

- `GET /api/v1/photos/:id/file`（已支持 API Key 鉴权，PhotoAuth 中间件）
- `download_url` 由后端在返回 task payload 时拼接，worker 直接使用

### Task Payload

建议响应字段：

- `id` (uint，与 `PeopleJob.ID` 一致)
- `job_id` (uint)
- `photo_id` (uint)
- `file_path` (string，仅日志用)
- `download_url` (string，后端拼接的 `/api/v1/photos/:id/file` 完整 URL)
- `width` (int)
- `height` (int)
- `lock_expires_at` (time)

说明：

- `download_url` 是 worker 拉图的唯一数据入口，复用现有 `/api/v1/photos/:id/file` 端点
- `file_path` 仅用于日志与排障，不要求 worker 能访问
- 后端下发任务时应过滤掉照片已有 `manual_locked` 人脸的任务（不下发给 worker）

### Result Payload

第一版只接受检测结果，不接受聚类结果：

- `photo_id`
- `task_id`
- `faces[]`
  - `bbox`
  - `confidence`
  - `quality_score`
  - `embedding`
- `processing_time_ms`

这样 worker 不需要理解 `Person`、`manual_locked`、`cannot_link` 或 `top_person_category`。

## Data Model Changes

### `people_jobs`

当前 `PeopleJob` 仅适用于本地后台线程 `ClaimNextJob()` 模式，不足以支撑远程 worker。第一版应新增远程租约字段：

- `worker_id` (varchar, nullable)
- `lock_expires_at` (datetime, nullable, indexed)
- `last_heartbeat_at` (datetime, nullable)
- `progress` (int, default 0)
- `status_message` (text, nullable)

保留现有字段：

- `status`
- `attempt_count`
- `last_error`
- `queued_at`
- `started_at`
- `completed_at`

说明：

- `status` 仍然保持 `pending/queued/processing/completed/failed/cancelled`
- 远程 worker 的”持有任务”通过 `worker_id + lock_expires_at` 表达
- 本地后台处理的任务 `worker_id = NULL`（用于区分本地/远程）
- 过期任务必须可重新领取

### `ClaimNextRemote` 查询语义

`ClaimNextRemote` 必须精确区分本地与远程任务：

```sql
WHERE (status IN ('pending', 'queued'))
   OR (status = 'processing' AND worker_id IS NOT NULL AND lock_expires_at < NOW())
-- 注意：不能领取 worker_id IS NULL 的 processing 任务（那是 NAS 本地在处理）
```

### New DTOs

建议新增人物 worker 专用 DTO（`model/people_worker.go`），而不是复用 analyzer DTO：

- `PeopleWorkerTask`（所有 ID 字段使用 `uint`，与 `PeopleJob` 一致）
- `PeopleWorkerTasksResponse`
- `PeopleWorkerHeartbeatRequest`
- `PeopleWorkerHeartbeatResponse`
- `PeopleWorkerReleaseTaskRequest`
- `PeopleDetectionFace`（字段对齐 `mlclient.DetectedFace`，方便 NAS 端直接转为 `mlclient.DetectFacesResponse` 进入 `applyDetectionResult`）
- `PeopleDetectionResult`
- `PeopleWorkerSubmitResultsRequest`
- `PeopleWorkerSubmitResultsResponse`

## Service Changes

### Refactor `peopleService`

当前 `processJob` 将”调用 ML”和”应用检测结果”耦合在同一条链路里（约 180 行）。第一版需要拆成三段：

1. `preflightCheck(job, photo) (skip bool, err error)`
   前置检查（从 `processJob` 提取）：
   - 照片是否 excluded/missing → cancel job
   - 照片是否已有 `manual_locked` 人脸 → skip detection, mark completed
   - 设置 `face_process_status = processing`

2. `detectFacesLocally(photo) (*mlclient.DetectFacesResponse, error)`
   本地模式保留，负责：
   - 图片压缩/resize（`util.NewImageProcessor(1024, 85)`）
   - base64 编码
   - 调用本地 `PeopleMLClient`

3. `applyDetectionResult(job *model.PeopleJob, photo *model.Photo, result *mlclient.DetectFacesResponse) error`
   远程 worker 回传结果与本地模式共用后半段：
   - 删除旧 `faces`
   - 写入新 `faces`（含 embedding）
   - 生成 face thumbnail（NAS 本地文件操作）
   - 执行 `runIncrementalClustering`
   - 更新 `photo.face_process_status` / `face_count` / `top_person_category`
   - 刷新 `person` 统计与代表脸
   - 标记 `people_job` completed

本地模式流程：`preflightCheck` → `detectFacesLocally` → `applyDetectionResult`
远程模式流程：`preflightCheck`（在任务下发时过滤）→ worker 推理 → `applyDetectionResult`

**注意**：远程模式中，worker 需要自行对下载的原图做压缩/base64 编码后再送入 `relive-ml`。这部分逻辑复用 `internal/util` 包的 `ImageProcessor`（同 module 内 `cmd/` 可 import `internal/`）。

这样能避免本地模式与离线模式维护两套人物后处理逻辑。

**重构安全策略**：先为现有 `processJob` 补齐集成测试（覆盖有脸/无脸/手动锁三个场景），再做 extract method，最后跑同一批测试确认无回归。

### Runtime Lease

人物 worker 不应与 NAS 本地后台人物任务并发消费同一队列。第一版使用独立的 `global_people` 资源键，复用现有 `analysis_runtime_leases` 表：

- 资源键：`global_people`（独立于 analyzer 的 `global_analysis`，两者可以并行）
- `OwnerType` CHECK 约束新增 `people_worker` 值
- 本地后台人物任务运行时，以 `owner_type=background` 占用 `global_people`
- 远程 `people-worker` 运行时，以 `owner_type=people_worker` 占用 `global_people`

**改造 `runBackground`**：当前本地后台人物处理通过内存 `isRunning` 标志控制，没有数据库级互斥。需要改造为：
- `StartBackground` 先 acquire `global_people` 租约
- `runBackground` 循环中定期 heartbeat
- `StopBackground` 释放租约

语义：

- 同一时间只能存在一个人物任务消费者（本地后台 OR 远程 worker）
- 防止 NAS 本地后台与 Mac worker 同时抢 `people_jobs`
- 人物 worker 与 analyzer 互不影响，可以同时运行

### NAS 重启安全

当前 `NewPeopleService` 构造函数调用 `InterruptNonTerminal()`，会把所有 `pending/queued/processing` 的任务一律 cancel。这对远程 worker 是灾难性的——NAS 重启会杀掉 worker 正在处理的任务。

**改造策略**：
- `InterruptNonTerminal` 只 cancel **本地任务**（`worker_id IS NULL` 的 `processing` 任务）
- 远程 worker 持有的任务（`worker_id IS NOT NULL`）：
  - 锁未过期 → 保持 `processing`，等 worker 继续处理
  - 锁已过期 → 回退为 `queued`，可被重新领取

## Failure Handling

第一版采用“租约驱动恢复”：

- worker 崩溃或断网：
  - 任务不立即失败
  - 超过 `lock_expires_at` 后自动回到可领取状态
- 心跳丢失后晚到结果：
  - 后端必须拒绝 stale result
- `relive-ml` 不可用、下载失败、超时：
  - `retry_later=true`
  - 任务回到待领取状态
- 永久错误（损坏图、非法结果）：
  - 增加 `attempt_count`
  - 达到阈值后标记 `failed`
- `faces=[]`：
  - 视为正常完成
  - `photo.face_process_status=no_face`

后端重启策略：

- 不能简单取消所有非终态远程任务（现有 `InterruptNonTerminal` 需改造，见 Service Changes 章节）
- 本地 processing 任务（`worker_id IS NULL`）→ cancel（与现有行为一致）
- 远程 processing 任务（`worker_id IS NOT NULL`）：
  - 锁未过期 → 保持不变
  - 锁已过期 → 回退为 `queued`

## Testing Strategy

### Repository

- 远程 claim 顺序正确
- 非持有者不能 heartbeat/release/complete
- 锁过期后可重新领取

### Service

- `applyDetectionResult` 正常写入人脸与聚类
- 空人脸结果写 `no_face`
- stale result 被拒绝
- 重复提交具备幂等保护

### Handler

- 任务领取接口返回正确 download URL
- 心跳、释放、提交接口的鉴权与状态码正确
- 转发后的 host/proto 行为与 analyzer 一致

### Worker Client

- 任务拉取
- 心跳续约
- 任务释放
- 结果提交

### Manual End-to-End

- NAS 启 backend，Mac 跑 `people-worker`
- 正常处理 1 张有人脸照片
- 正常处理 1 张无人脸照片
- 强制退出 worker，确认任务自动回收
- NAS 重启后确认过期任务能恢复

## Worker Concurrency

`people-worker` 进程内支持多 goroutine 并发处理：

- 配置项 `workers: 4`（M4 Mac 推荐 4，NAS CPU 推荐 1-2）
- 采用与 analyzer 相同的 fetchLoop + processLoop 模式：
  - fetchLoop：定期检查本地队列深度，不足时调用 `GET /tasks?limit=N` 补充
  - processLoop：从本地队列消费，每个 goroutine 独立发送 per-task heartbeat
- `ClaimNextRemote` 一次领取 `limit` 个任务，减少 HTTP 往返
- 每个 worker goroutine 独立完成：下载 → 压缩 → 推理 → 提交

**`relive-ml` 侧并发**：worker 应配置 `relive-ml` 的并发上限（默认与 workers 数一致），避免 ML 服务过载。M4 16G 统一内存下，4 路并发推理不会有内存压力（人脸模型通常 < 500MB）。

## Rollout Plan

建议分四步发布：

1. **前置 bug 修复**：幽灵人物 + 聚类阈值提升 + 阈值可配置化
2. **后端改造**：`InterruptNonTerminal` 安全化 + `runBackground` 接入 runtime lease + `processJob` 拆分 + `EnqueueUnprocessed` 接口
3. **后端新增**：远程 worker API 端点 + 前端感知 worker 状态
4. **发布 `people-worker` CLI** 与示例配置

这样可以先把 NAS 端兼容能力做好，再接入 Mac worker 做端到端联调。

## Open Questions Resolved

- 是否采用 API 拉图而不是共享挂载？
  采用 API 拉图，复用现有 `GET /api/v1/photos/:id/file` 端点。

- 聚类应放在哪一侧？
  放在 NAS。

- 缩略图应放在哪一侧？
  放在 NAS。

- 是否合并进 `relive-analyzer`？
  不合并，独立 `people-worker`。

- Runtime lease 是否与 analyzer 共享？
  不共享。使用独立资源键 `global_people`，人物 worker 与 analyzer 可并行运行。

- worker 如何复用 `mlclient`？
  同一 Go module 内 `cmd/` 可 import `internal/` 包，直接复用 `internal/service/mlclient` 和 `internal/util`（ImageProcessor）。

- M4 Mac 推荐并发数？
  `workers: 4`，16G 统一内存足够。

- 垃圾聚类解散后 `cannot_link` 约束怎么处理？
  一期通过提高阈值缓解（0.35→0.65），二期再考虑 face 级 cannot_link 约束。

- 聚类阈值是否可配置？
  是，改为 `config.yaml` 的 `people.link_threshold` 和 `people.attach_threshold` 配置项。
