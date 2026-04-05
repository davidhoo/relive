# People Manual Feedback Recluster Design

**Goal:** 让人物详情页中的手工合并、拆分、移动操作不再被同步全局重聚类阻塞；核心数据变更立即完成并返回，后续低置信度人脸重评估改为后台串行执行。

**Scope:** `backend/internal/service/people_service.go`、`backend/internal/service/people_service_test.go`、`backend/internal/repository/face_repo.go`、`backend/pkg/database/database.go`、`frontend/src/views/People/Detail.vue`；本次不重写聚类算法，不新增独立后台任务表，不改现有 merge/split/move 请求结构。

## Current Problem

当前人物手工操作链路是：

- 执行合并/拆分/移动
- 同步刷新人物与照片统计
- 同步调用 `triggerRecluster()`

问题不在“手工改了几张脸”，而在 `triggerRecluster()` 的工作范围：

- 扫描全库低置信度、未手工锁定的人脸
- 重置候选为 pending
- 重新跑增量聚类
- 同步写回人物状态与照片人物分类

在大库场景下，这会导致：

1. **接口耗时和全局人脸库规模绑定**
   - 点一次“合并人物”，实际做的是一次全局后处理

2. **SQLite 写锁竞争放大波动**
   - 扫描完成后人物后台任务可能仍在跑
   - 手工操作与后台任务都写 `faces` / `people` / `photos`
   - WAL 允许并发读，但写仍然串行，用户体感表现为“有时候特别慢”

## Alternatives Considered

### Option 1: 仅补索引

优点：
- 改动小

缺点：
- 仍然保留同步全局重聚类
- 仍然会被 SQLite 写锁放大
- 只能降耗，不能解决按钮阻塞

### Option 2: 手工操作后改为后台异步、串行、可合并的重聚类

优点：
- 直接消除前台接口等待
- 保留“人工修正带动系统纠偏”的能力
- 实现复杂度可控

缺点：
- 用户不能在当前请求中立刻看到 `recluster_evaluated/reassigned`
- 需要增加服务内调度状态

### Option 3: 彻底关闭手工操作后的自动重聚类

优点：
- 止血最快

缺点：
- 直接丢失反馈闭环能力
- 后续聚类质量提升路径被切断

**Decision:** 采用 Option 2，并顺手补最小必要索引。

## Design

### Backend Behavior

`MergePeople()`、`SplitPerson()`、`MoveFaces()` 调整为：

- 保留现有核心数据修改
- 保留人物状态同步与照片人物分类刷新
- 不再同步调用 `triggerRecluster()`
- 改为调用 `scheduleFeedbackRecluster()`

接口响应仍返回 `ReclusterResult`，但在异步模式下固定返回零值：

- `recluster_evaluated = 0`
- `recluster_reassigned = 0`
- `recluster_iterations = 0`

这样可以避免扩大 API 变更面，前端只需调整文案。

### Async Scheduling

在 `peopleService` 内新增一个轻量级的反馈重聚类调度器状态：

- 同一时刻最多一个反馈重聚类 goroutine 在执行
- 如果执行中又收到新的手工操作，只置一个 `pending` 标记
- 当前轮结束后若发现 `pending=true`，再补跑一轮

该设计的目标不是“每次手工操作都精确对应一轮重聚类”，而是：

- 前台操作立即返回
- 后台最终至少再收敛一次
- 多次连续手工修正时避免重复风暴

### Coordination with People Background Jobs

反馈重聚类在启动前检查人物后台任务是否正处于 `running/stopping` 且存在活动 worker：

- 若后台人物任务在跑，反馈重聚类先延后并保留 `pending`
- 后台 worker 空闲后再执行

这样可以避免 `processJob()` 与反馈重聚类同时抢写锁。

本次不引入跨进程锁，也不新增数据库表；范围限定在单进程服务内调度。

### Indexing

为 `faces` 增加最小必要索引，降低后台反馈重聚类成本：

- 面向低置信度候选筛选的复合索引
- 面向按人物取原型并排序的复合索引

索引目标是减少全表扫描和临时排序成本，但不改变功能语义。

### Frontend Messaging

人物详情页保持当前操作流程，但提示文案改为：

- 基础成功提示仍保留，例如“人物已合并”
- 当本次请求未同步返回重聚类结果时，补一句“后台将继续重新评估不确定人脸”

不新增轮询，不新增任务状态 UI。

## Non-Goals

本次不做：

- 不改造 `triggerRecluster()` 的聚类算法范围
- 不把反馈重聚类拆成持久化任务表
- 不给前端增加实时重聚类进度展示
- 不处理多进程/多实例间的反馈调度协同

## Testing Strategy

### Backend

新增服务层测试验证：

- 手工合并不会同步阻塞在 `triggerRecluster()` 上
- 连续多次调度只会合并为有限后台轮次
- 后台人物 worker 运行时，反馈重聚类会延后而不是立即执行

### Frontend

构建验证：

- `cd frontend && npm run build`

手工操作成功提示应兼容“同步结果为零值”的响应。

### Verification

至少执行：

- `cd backend && go test -run 'TestPeopleService_(MergePeopleSchedulesFeedbackReclusterAsync|FeedbackReclusterCoalescesRequests|FeedbackReclusterDefersWhileBackgroundRunning)' -v ./internal/service`
- `cd backend && go test ./internal/service ./internal/repository`
- `cd frontend && npm run build`
