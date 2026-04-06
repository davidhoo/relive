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
- 复用现有 analyzer 的“任务领取 + 心跳 + 结果提交 + 租约恢复”交互模式。
- 保留现有 NAS 本地人物模式，不强制所有用户切到离线 worker。
- 让 worker 仅通过 API 工作，不依赖共享照片目录挂载。

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

### Task Payload

建议响应字段：

- `id`
- `job_id`
- `photo_id`
- `file_path`
- `download_url`
- `width`
- `height`
- `lock_expires_at`

说明：

- `download_url` 是 worker 拉图的唯一数据入口
- `file_path` 仅用于日志与排障，不要求 worker 能访问

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

- `worker_id`
- `lock_expires_at`
- `last_heartbeat_at`
- `progress`
- `status_message`

保留现有字段：

- `status`
- `attempt_count`
- `last_error`
- `queued_at`
- `started_at`
- `completed_at`

说明：

- `status` 仍然保持 `pending/queued/processing/completed/failed/cancelled`
- 远程 worker 的“持有任务”通过 `worker_id + lock_expires_at` 表达
- 过期任务必须可重新领取

### New DTOs

建议新增人物 worker 专用 DTO，而不是复用 analyzer DTO：

- `PeopleWorkerTask`
- `PeopleWorkerTasksResponse`
- `PeopleWorkerHeartbeatRequest`
- `PeopleWorkerHeartbeatResponse`
- `PeopleWorkerReleaseTaskRequest`
- `PeopleDetectionFace`
- `PeopleDetectionResult`
- `PeopleWorkerSubmitResultsRequest`
- `PeopleWorkerSubmitResultsResponse`

## Service Changes

### Refactor `peopleService`

当前 `processJob` 将“调用 ML”和“应用检测结果”耦合在同一条链路里。第一版需要拆成两段：

1. `detectFacesLocally(photo)`  
   本地模式保留，调用本地 `PeopleMLClient`

2. `applyDetectionResult(job, photo, result)`  
   远程 worker 回传结果与本地模式共用后半段：
   - 删除旧 `faces`
   - 写入新 `faces`
   - 生成 thumbnail
   - 执行增量聚类
   - 更新 `photo` / `people_job` / `person`

这样能避免本地模式与离线模式维护两套人物后处理逻辑。

### Runtime Lease

人物 worker 不应与 NAS 本地后台人物任务并发消费同一队列。第一版需要增加一个与 analyzer 类似的全局租约：

- 本地后台人物任务运行时，占用 `people runtime`
- 远程 `people-worker` 运行时，也占用 `people runtime`

语义：

- 同一时间只能存在一个人物任务消费者
- 防止 NAS 本地后台与 Mac worker 同时抢 `people_jobs`

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

- 不能简单取消所有非终态远程任务
- 只回收锁已过期的远程 `processing` 任务

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

## Rollout Plan

建议分两步发布：

1. 后端先支持远程 worker API 与本地/远程双模式
2. 再发布 `people-worker` CLI 与示例配置

这样可以先把 NAS 端兼容能力做好，再接入 Mac worker 做端到端联调。

## Open Questions Resolved

- 是否采用 API 拉图而不是共享挂载？  
  采用 API 拉图。

- 聚类应放在哪一侧？  
  放在 NAS。

- 缩略图应放在哪一侧？  
  放在 NAS。

- 是否合并进 `relive-analyzer`？  
  不合并，独立 `people-worker`。
