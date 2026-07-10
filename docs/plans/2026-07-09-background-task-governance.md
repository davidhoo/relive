# Relive 后台任务治理开发计划

> **给执行 agent：** 必须使用 superpowers:executing-plans，并按本计划逐任务执行。

**目标：** 在不改变现有功能结果的前提下，引入系统级后台任务治理，让前台用户操作保持最高优先级，后台自动维护只在资源和前台状态允许时运行。

**架构：** 新增轻量级进程内 `BackgroundTaskCoordinator`，作为自动后台 slice 的前台优先准入控制器。第一波只接入 People 相关重路径，并把 protoCache、ANN rebuild 等长耗时工作移出前台阻塞区；不改变识别阈值、合并建议评分、展示策略或现有 API 语义。SQLite 只继续用于已有持久化任务表和低频 checkpoint，自动维护使用内存 coalescing、dirty flag、cooldown 和状态快照。

**技术栈：** Go、Gin、GORM、SQLite、Vue 3、TypeScript、Element Plus、现有 `database.WriteQueue`、现有 People service/repository 分层模式。

---

## 一、不可突破的硬约束

1. P0 前台操作不能等待 P2 自动后台维护。
2. P2 自动后台维护必须可合并、可限流、可观测，并且允许落后。
3. SQLite 不能被改造成高频队列状态存储。
4. 每个长耗时后台任务必须有执行预算或安全停止点。
5. 本计划不改变现有用户可见语义。
6. 现有 API response shape 必须保持向后兼容；新增字段只能是可选字段。
7. 手动前台操作永远比后台自动维护权威。

## 二、行为等价范围

本计划是调度、幂等和可观测性计划，不是算法改造计划。除“重复请求导致的意外重复副作用”外，不允许改变产品行为或算法输出。

允许修改：

- 阻止重复 split/move 请求造成重复副作用。
- 让前台人物操作进入统一 foreground scope。
- 延迟、跳过、合并或 cooldown P2 自动维护。
- 把昂贵读取或缓存 rebuild 移出前台阻塞临界区。
- 新增可选状态 API 和紧凑运维 UI。

禁止修改：

- 不改 face embedding 距离阈值、identity profile 阈值、ANN match 阈值、merge suggestion scoring。
- 不改候选排序、相似度计算、cannot-link 语义、合并确认/拒绝语义。
- 不改 People 列表/详情展示策略、hidden/category 行为、URL ID、照片筛选行为。
- 不改首次成功 split/move/merge 的最终人物归属结果。
- 不引入 PostgreSQL、Redis、RabbitMQ、Kafka、Temporal 或任何外部队列。
- 不把 SQLite 用作高 churn 自动后台任务队列。

每个实现任务必须用测试证明以下至少一项：

- 前台功能结果不变，只移除重复副作用。
- 自动后台任务被跳过或合并时，不会把持久化任务错误标记为完成。

## 三、优先级模型

- P0 前台：页面请求、split、move、merge、rename、hide、avatar update、category change、config change。
- P1 用户可见持久任务：scan、analyze、thumbnail rebuild、geocode rebuild、people detection job、显式 rebuild 按钮。
- P2 自动维护：People clustering、feedback recluster、proto cache refresh、identity profile build、identity ANN rebuild、merge suggestion refresh、derived-stat refresh、cleanup。

## 四、实施波次

第一波是本计划的开发目标，必须先完成并验收：

- Phase 0：现状刻画测试和耗时日志。
- Phase 1：split/move 重复请求安全和前端防重复提交。
- Phase 2：统一 coordinator 和 foreground scope。
- Phase 3：People protoCache、identity ANN、merge suggestion 的后台治理。
- Phase 4：状态 API 和 best-effort 资源背压。

第二波只做后续计划，不在第一波直接改代码：

- Phase 5：为 thumbnail/geocode、AI/event curation 单独写后续治理计划。

不要把第二波代码改动混进第一波 commit。如果第一波实现过程中发现必须修改 thumbnail/geocode/AI/event 代码，先停止并写设计说明。

---

## 阶段 0：现状刻画测试和指标

Phase 0 只能增加 characterization test 和日志，不改变调度或业务结果。

### 任务 1：为现有 People clustering coordinator 增加前后台回归测试

**文件：**

- 修改：`backend/internal/service/people_clustering_coordinator_test.go`
- 阅读：`backend/internal/service/people_clustering_coordinator.go`
- 阅读：`backend/internal/service/people_service.go`

**步骤 1：写现状刻画测试**

新增 `TestPeopleClusteringCoordinator_RunningBatchStillBlocksForeground`。

测试要求：

- 启动一个 background cluster batch。
- 让 batch 持有 `writeGate.RLock()`。
- 同时尝试一个 foreground `writeGate.Lock()`。
- 断言 foreground 会被运行中的 batch 阻塞。

这是现状刻画测试，今天就应该通过。不要把它写成失败测试。

**步骤 2：增加目标行为测试，并先 skip**

新增 `TestPeopleClusteringCoordinator_RefreshWorkDoesNotHoldWriteGate`。

目标行为：

- 模拟一个慢速 proto-cache refresh。
- refresh 运行期间，foreground mutation 能立即拿到 `writeGate.Lock()`。

先加：

```go
t.Skip("enabled after proto cache refresh is moved outside writeGate")
```

**步骤 3：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestPeopleClusteringCoordinator_(RunningBatchStillBlocksForeground|RefreshWorkDoesNotHoldWriteGate)' -count=1
```

预期：

- 现状刻画测试通过。
- 目标行为测试 skipped。

**步骤 4：提交**

```bash
git add backend/internal/service/people_clustering_coordinator_test.go
git commit -m "test(people): characterize foreground blocking by clustering batch"
```

### 任务 2：为前台 People 操作增加耗时日志字段

**文件：**

- 修改：`backend/internal/service/people_service.go`
- 测试：`backend/internal/service/people_service_test.go`

**步骤 1：为 timing helper 写测试**

如有必要，提取一个小 helper：

```go
type peopleMutationTiming struct {
    Operation string
    TargetID  uint
    FaceCount int
    GateWait  time.Duration
    Business  time.Duration
}
```

测试日志字段稳定包含：

- `operation`
- `writeGateWaitMs`
- `businessMs`
- `totalMs`
- `faceCount` 或 `sourceCount`

**步骤 2：加 instrumentation**

覆盖：

- `SplitPerson`
- `MoveFaces`
- `AssignFacePerson`
- `DissolvePerson`

`MergePeople` 已有部分 timing，保留结构但对齐字段名。

**步骤 3：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestPeople.*Timing|TestPeopleService' -count=1
```

**步骤 4：提交**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_service_test.go
git commit -m "chore(people): log foreground mutation timing"
```

---

## 阶段 1：前台操作即时安全

Phase 1 是唯一允许改变前台行为的阶段，但变化只限于重复请求安全。第一次合法请求的最终结果必须和当前行为一致。

### 任务 3：为 split 请求增加后端幂等保护

**文件：**

- 修改：`backend/internal/service/people_service.go`
- 测试：`backend/internal/service/people_service_test.go`
- 阅读：`backend/internal/model/people_feedback_event.go`
- 阅读：`backend/internal/repository/people_feedback_event_repo.go`

**步骤 1：写失败测试**

新增 `TestPeopleService_SplitPersonRepeatedFaceSetReturnsExistingPerson`。

场景：

- Person A 有 faces 1、2、3。
- 调用 `SplitPerson([]uint{2,3})`，创建 Person B。
- 再次调用 `SplitPerson([]uint{2,3})`。

预期：

- 第二次返回 Person B。
- 不创建新 person。
- 不新增第二条 `person_split` event。

新增 `TestPeopleService_SplitPersonRepeatedFaceSetRequiresMatchingSplitEvent`。

场景：

- Person A 有 faces 1、2、3。
- 通过移动或手工归属，让 faces 2、3 进入另一个人物，但没有精确匹配该 face set 的 `person_split` event。
- 调用 `SplitPerson([]uint{2,3})`。

预期：

- 返回 conflict 或 validation error。
- 不创建新 person。
- 不能把无关已有 person 静默当作幂等 split 结果。

**步骤 2：实现 split 幂等**

在 `SplitPerson` 加载 faces 后：

- 归一化 `faceIDs`。
- 如果所有请求 faces 已经属于同一个非 source person，并且 `manual_lock_reason = "split"`，只有在持久化 `person_split` 反馈事件能证明这是同一次 split 时，才返回该已有 person。
- 如果请求 faces 已经不再属于同一个 person，返回 conflict，不要继续创建新 person。

保持纯 DB-backed，不新增表。

匹配的 `person_split` event 至少需要：

- `event_type = person_split`
- `target_person_id = 当前 shared person ID`
- `face_ids = repository.MarshalFeedbackIDs(normalizedFaceIDs)`
- 如果能推断 original source person，则 `source_person_ids` 包含 original source person ID

如果当前 API 不携带 original source person ID，最低安全证明是精确匹配 `face_ids + target_person_id + event_type`。不能只依赖 `manual_lock_reason = "split"`。

**步骤 3：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestPeopleService_SplitPersonRepeatedFaceSetReturnsExistingPerson|TestPeopleService_SplitPersonRepeatedFaceSetRequiresMatchingSplitEvent|TestEmitsFeedbackSplitPersonRecordsSourceNewAndFaceIDs' -count=1
```

**步骤 4：提交**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_service_test.go
git commit -m "fix(people): make repeated split requests idempotent"
```

### 任务 4：为 move 请求增加后端幂等保护

**文件：**

- 修改：`backend/internal/service/people_service.go`
- 测试：`backend/internal/service/people_service_test.go`

**步骤 1：写失败测试**

新增：

- `TestPeopleService_MoveFacesRepeatedFaceSetIsNoOp`
- `TestPeopleService_MoveFacesMixedAlreadyMovedAndOtherFacesConflicts`

预期：

- 重复提交同一个 `face_ids + target_person_id` 是 no-op success。
- 包含已经移动到不同非目标人物的 stale repeat 返回 conflict，不继续 mutate。

**步骤 2：实现最小幂等**

在 `MoveFaces` 写入前：

- 如果所有请求 face 已经是 `person_id = targetPersonID`，返回空 recluster result，不记录 feedback。
- 如果部分 face 已经在 target，部分仍在原 source，允许只移动剩余 face。
- 如果请求 faces 跨多个非 target source person，只有当前语义已经支持时才保持；否则返回清晰 conflict。

不要改变首次 move 行为。今天能成功的相同输入，仍应产生相同最终归属、相同 target person sync、相同 merge-suggestion dirty marking。

**步骤 3：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestPeopleService_MoveFaces.*|TestEmitsFeedbackMoveFaces' -count=1
```

**步骤 4：提交**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_service_test.go
git commit -m "fix(people): make repeated move requests idempotent"
```

### 任务 5：加固前端前台操作 UX

**文件：**

- 修改：`frontend/src/views/People/Detail.vue`
- 修改：`frontend/src/views/Photos/Detail.vue`
- 修改：`frontend/src/api/people.ts`
- 测试：现有 frontend type check

**步骤 1：增加本地 in-flight guard**

在 `People/Detail.vue`：

- 任一 foreground mutation 进行中时，禁用 split、move、merge、similarity 操作。
- 保留 `splitting` 和 `moving`，新增共享 computed：`foregroundBusy`。
- 只有 timeout-like error 显示“操作可能仍在后台处理中，请稍后刷新”。
- validation error、4xx response、有 response body 的明确 5xx，继续显示后端真实错误。

在 `Photos/Detail.vue`：

- 对单 face split 和 face assignment 加同样 guard。

**步骤 2：避免双击重复提交**

调用 `peopleApi.split` 或 `peopleApi.moveFaces` 前，如果对应 loading flag 已经是 true，直接 return。

**步骤 3：运行 type check**

```bash
cd frontend
npx vue-tsc --noEmit
```

**步骤 4：手工浏览器检查**

- 双击 split/move 按钮，只提交一个请求。
- 模拟 timeout，显示“可能仍在后台处理中”。
- 模拟后端 validation error，仍显示真实错误。

**步骤 5：提交**

```bash
git add frontend/src/views/People/Detail.vue frontend/src/views/Photos/Detail.vue frontend/src/api/people.ts
git commit -m "fix(frontend): prevent duplicate people foreground actions"
```

---

## 阶段 2：引入 BackgroundTaskCoordinator

Phase 2 引入统一准入控制器，但不改变任何后台计算结果。被拒绝的 P2 自动任务只能是 skip/defer，不能被标记为成功完成。

### 任务 6：新增 coordinator 核心类型

**文件：**

- 新建：`backend/internal/service/background_task_coordinator.go`
- 新建：`backend/internal/service/background_task_coordinator_test.go`

**步骤 1：写 admission control 测试**

新增：

- `TestBackgroundTaskCoordinator_AllowsP2WhenIdle`
- `TestBackgroundTaskCoordinator_BlocksP2WhenForegroundActive`
- `TestBackgroundTaskCoordinator_BlocksP2DuringCooldown`
- `TestBackgroundTaskCoordinator_CoalescesDedupeKeys`

**步骤 2：实现 coordinator**

核心类型：

```go
type BackgroundTaskPriority string

const (
    BackgroundPriorityUser      BackgroundTaskPriority = "user"
    BackgroundPriorityAutomatic BackgroundTaskPriority = "automatic"
)

type BackgroundTaskClass string

const (
    BackgroundTaskPeopleClustering     BackgroundTaskClass = "people_clustering"
    BackgroundTaskFeedbackRecluster    BackgroundTaskClass = "people_feedback_recluster"
    BackgroundTaskProtoCacheRefresh    BackgroundTaskClass = "people_proto_cache_refresh"
    BackgroundTaskIdentityProfileBuild BackgroundTaskClass = "identity_profile_build"
    BackgroundTaskIdentityANNRebuild   BackgroundTaskClass = "identity_ann_rebuild"
    BackgroundTaskMergeSuggestion      BackgroundTaskClass = "merge_suggestion_refresh"
)

type BackgroundTaskRequest struct {
    Class      BackgroundTaskClass
    Priority   BackgroundTaskPriority
    DedupeKey  string
    MaxRuntime time.Duration
}
```

核心方法：

- `BeginForeground() func()`
- `ForegroundActive() bool`
- `CanRun(req BackgroundTaskRequest) (BackgroundTaskDecision, bool)`
- `Begin(req BackgroundTaskRequest) (release func(), decision BackgroundTaskDecision, ok bool)`
- `Cooldown(class BackgroundTaskClass, duration time.Duration, reason string)`
- `Snapshot() BackgroundTaskSnapshot`

Phase 2 先不读取 host CPU/iowait，只做进程内 foreground 和 cooldown gating。

**步骤 3：运行测试**

```bash
cd backend
go test ./internal/service -run TestBackgroundTaskCoordinator -count=1
```

**步骤 4：提交**

```bash
git add backend/internal/service/background_task_coordinator.go backend/internal/service/background_task_coordinator_test.go
git commit -m "feat(service): add background task coordinator"
```

### 任务 7：把 coordinator 接入 service 构造和 scheduler

**文件：**

- 修改：`backend/internal/service/service.go`
- 修改：`backend/internal/service/scheduler.go`
- 修改：`backend/internal/service/scheduler_test.go`

**步骤 1：写 scheduler 测试**

新增 stub 测试证明：

- coordinator 拒绝 automatic task 时，不调用 `identityProfileService.RunBackgroundSlice()`。
- 同样不调用 `mergeSuggestionService.RunBackgroundSlice()`。
- 拒绝不等于把 dirty/background state 标记为 clean。

**步骤 2：修改 `TaskScheduler`**

更新 `NewTaskScheduler(...)`，接收 `*BackgroundTaskCoordinator`。

用 `Begin(BackgroundTaskRequest{Priority: Automatic, Class: ...})` 包裹：

- `runMergeSuggestionSlice`
- `runIdentityProfileSlice`

**步骤 3：在 `NewServices` 构造 coordinator**

在 `backend/internal/service/service.go` 创建一个 coordinator。

注入到：

- `TaskScheduler`
- `peopleService`
- identity profile service/coordinator
- merge suggestion service

同时更新现有 identity profile foreground hook。当前 `service.go` 使用：

```go
peopleSvc.(*peopleService).clusteringCoordinator.foregroundWaiterCount() > 0
```

引入 coordinator 后必须改为：

```go
ips.SetForegroundBusyFn(func() bool {
    return backgroundCoordinator.ForegroundActive() ||
        peopleSvc.(*peopleService).clusteringCoordinator.foregroundWaiterCount() > 0
})
```

Task 8 完成前保留 `foregroundWaiterCount()` bridge。新增测试证明 `BackgroundTaskCoordinator.BeginForeground()` active 时，identity profile slice 会 skip。

**步骤 4：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestTaskScheduler|TestBackgroundTaskCoordinator' -count=1
```

**步骤 5：提交**

```bash
git add backend/internal/service/service.go backend/internal/service/scheduler.go backend/internal/service/scheduler_test.go
git commit -m "feat(service): gate scheduled background slices"
```

### 任务 8：用 coordinator foreground scope 接管 People 前台状态

**文件：**

- 修改：`backend/internal/service/people_service.go`
- 修改：`backend/internal/service/people_clustering_coordinator.go`
- 测试：`backend/internal/service/people_clustering_coordinator_test.go`

**步骤 1：写测试**

新增或调整测试证明：

- `SplitPerson`、`MoveFaces`、`MergePeople`、`DissolvePerson`、`AssignFacePerson` 即使 error exit，也会正确进入并释放 foreground scope。
- foreground scope active 时，P2 clustering 不启动。

**步骤 2：保留现有 `foregroundWaiters` 兼容桥**

不要马上删除 `foregroundWaiters`。让 `beginForegroundMutation()` 同时调用：

- coordinator foreground scope
- 现有 clustering coordinator foreground waiter

`beginForegroundMutation()` 必须用 defer-safe 顺序释放两个 scope。测试要覆盖 split/move 的 error exit。

Task 8 之后，canonical foreground busy source 是 `BackgroundTaskCoordinator.ForegroundActive()`。`foregroundWaiters` 只作为现有 people clustering worker 的兼容机制保留，后续单独清理。

**步骤 3：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestPeopleClusteringCoordinator|TestPeopleService_.*Foreground' -count=1
```

**步骤 4：提交**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_clustering_coordinator.go backend/internal/service/people_clustering_coordinator_test.go
git commit -m "feat(people): register foreground mutations with background coordinator"
```

---

## 阶段 3：把重型 rebuild 移出前台阻塞路径

Phase 3 是性能核心阶段。它只能改变缓存构建时机和锁持有方式，不能改变相同缓存内容下的 clustering decision。

### 任务 9：把 protoCache refresh 从 cluster batch 临界区拆出

**文件：**

- 修改：`backend/internal/service/people_service.go`
- 修改：`backend/internal/service/people_clustering_coordinator.go`
- 测试：`backend/internal/service/people_clustering_coordinator_test.go`
- 测试：`backend/internal/service/people_service_test.go`

**步骤 1：启用 Task 1 的目标测试**

移除 `TestPeopleClusteringCoordinator_RefreshWorkDoesNotHoldWriteGate` 的 skip。

实现前预期：

- FAIL，因为 proto cache refresh 仍在 `writeGate.RLock()` 下执行。

**步骤 2：提取 proto cache builder**

在 `people_service.go` 提取：

```go
func (s *peopleService) buildClustProtoCache() (*clustProtoCache, error)
```

该方法：

- 列出 assigned person IDs。
- 列出 prototype embeddings。
- 选择 prototypes。
- 解码 embeddings。
- 不持有 `writeGate`。
- 不写 DB。
- 如果调用方提供 context 或 callback，在主要 read/CPU 步骤之间检查 foreground/cancel 状态。

**步骤 3：保持 protoCache single-owner**

除非后续任务明确引入锁，否则保持这个不变量：

```go
// protoCache is read and assigned only by peopleClusteringCoordinator's worker.
```

不要创建单独 goroutine 写 `s.protoCache`。不要让 scheduler goroutine 或 HTTP handler 读写 `s.protoCache`。如果未来选择独立 refresh goroutine，必须先把 `s.protoCache` 改成 mutex 或 `atomic.Value` 保护，并增加 `go test -race` 覆盖。

**步骤 4：增加冷缓存和过期缓存策略**

修改 `runIncrementalClustering()`：

- 如果 `protoCache == nil`，coordinator worker 可以在获取 `writeGate.RLock()` 前同步构建一次。
- 如果冷缓存构建开始前发现 foreground active，跳过构建并 return no work。
- 如果长时间冷缓存构建中途发现 foreground active，在下一个安全 checkpoint 停止并 return no work，不触碰 `writeGate`。
- 如果 cache stale 但 non-nil，本 batch 继续用旧缓存，并标记 refresh pending。
- 永远不要在 `writeGate.RLock()` 内同步 rebuild。

这避免冷启动时 `protoCache == nil` 一直 return no work，而唯一能刷新它的 worker 永远没有真正执行 refresh。

**步骤 5：增加 refresh path，但不改变 protoCache 所有权**

使用现有 people clustering coordinator worker 执行 refresh：

- stale-cache refresh 是 P2 work item，class 为 `people_proto_cache_refresh`。
- 在 `writeGate` 外运行 `buildClustProtoCache()`。
- 只在 coordinator worker 上执行 `s.protoCache = newCache`。
- swap 是极短的内存赋值。除非现有测试证明需要短锁，否则不持有 `writeGate`。
- 如果 coordinator 因 foreground active 拒绝 refresh，保持 refresh pending，不标记完成。

**步骤 6：增加行为等价测试**

新增测试证明：

- 相同 pending faces 和相同 prototype cache，在 refactor 前后产生相同 assigned person IDs。
- 冷缓存构建发生在 `writeGate.RLock()` 外。
- stale-cache path 使用旧 cache 跑当前 batch，不阻塞 foreground。

**步骤 7：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestPeopleClusteringCoordinator_RefreshWorkDoesNotHoldWriteGate|TestPeopleService.*ProtoCache|TestPeopleClusteringCoordinator' -count=1
go test -race ./internal/service -run 'TestPeopleClusteringCoordinator|TestPeopleService.*ProtoCache' -count=1
```

**步骤 8：提交**

```bash
git add backend/internal/service/people_service.go backend/internal/service/people_clustering_coordinator.go backend/internal/service/people_clustering_coordinator_test.go backend/internal/service/people_service_test.go
git commit -m "fix(people): refresh prototype cache outside write gate"
```

### 任务 10：为 protoCache refresh 增加 cooldown 和 coalescing

**文件：**

- 修改：`backend/internal/service/people_clustering_coordinator.go`
- 修改：`backend/internal/service/people_service.go`
- 测试：`backend/internal/service/people_clustering_coordinator_test.go`

**步骤 1：写测试**

新增测试证明：

- 多次 stale detection 合并成一次 refresh。
- refresh 失败后进入 cooldown，不 spin。
- foreground active 阻止 refresh startup。

**步骤 2：实现 coalescing**

使用内存 pending flag，不写 DB。

规则：

- 同时最多一个 proto cache refresh running。
- 同时最多一个 pending refresh。
- 成功后最小间隔：10 分钟。
- 失败或 DB locked 后 cooldown：2 分钟。
- pending/cooldown 状态归 `peopleClusteringCoordinator` 内存持有。
- 被拒绝的 refresh 不能清 pending flag。

**步骤 3：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestPeopleClusteringCoordinator.*ProtoCache|TestBackgroundTaskCoordinator' -count=1
```

**步骤 4：提交**

```bash
git add backend/internal/service/people_clustering_coordinator.go backend/internal/service/people_service.go backend/internal/service/people_clustering_coordinator_test.go
git commit -m "feat(people): coalesce prototype cache refreshes"
```

### 任务 11：限制 identity ANN full rebuild 频率

**文件：**

- 修改：`backend/internal/service/identity_profile_coordinator.go`
- 修改：`backend/internal/service/person_identity_profile_service.go`
- 测试：`backend/internal/service/identity_profile_coordinator_test.go`
- 测试：`backend/internal/service/person_identity_profile_service_test.go`

**步骤 1：写失败测试**

新增：

- `TestIdentityProfileCoordinator_AnnRebuildUsesCooldown`
- `TestIdentityProfileCoordinator_DeltaFullCoalescesRebuildRequest`

预期：

- cooldown 内多个 `delta_full` 不触发多次 full rebuild。
- `RebuildRequested` 保持 true，直到下一次允许运行。

**步骤 2：实现 cooldown**

在 identity coordinator 增加字段：

- `annRebuildCooldownUntil time.Time`
- `annRebuildMinInterval time.Duration`

默认最小间隔：10 分钟。

规则：

- 如果 rebuild 已请求但 cooldown active，记录 skip reason 并 return。
- 保持 request pending。
- 不阻塞 foreground。

**步骤 3：接入 BackgroundTaskCoordinator**

调用 `owner.rebuildANNFromCoordinator()` 前，请求 `BackgroundTaskIdentityANNRebuild`。

如果被拒绝，保持 rebuild pending。

**步骤 4：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestIdentityProfileCoordinator_Ann|TestPersonIdentityProfileService.*ANN' -count=1
```

**步骤 5：提交**

```bash
git add backend/internal/service/identity_profile_coordinator.go backend/internal/service/person_identity_profile_service.go backend/internal/service/identity_profile_coordinator_test.go backend/internal/service/person_identity_profile_service_test.go
git commit -m "fix(people): coalesce identity ANN rebuilds"
```

### 任务 12：治理 merge suggestion background slice

**文件：**

- 修改：`backend/internal/service/person_merge_suggestion_service.go`
- 测试：`backend/internal/service/person_merge_suggestion_service_test.go`

**步骤 1：写测试**

新增：

- `TestMergeSuggestionBackgroundSlice_SkipsWhenCoordinatorBusy`
- `TestMergeSuggestionBackgroundSlice_KeepsDirtyWhenSkipped`
- `TestMergeSuggestionBackgroundSlice_AllowsCheapStaleDirtyMarkBeforeHeavyWork`

**步骤 2：实现 coordinator check**

在 `RunBackgroundSlice()` 中：

- 保留轻量 stale/dirty state detection，前提是它只是低频状态更新，并且属于当前已有行为。
- 在 heavy target listing、assignment building、similarity calculation、suggestion writes 之前，请求 `BackgroundTaskMergeSuggestion`。
- 如果被拒绝，记录 skip 并 return nil。
- skipped 时不能 mark clean，不能 advance cursor。

不要把 check 放得太早，导致 stale merge suggestions 永远无法 mark dirty。也不要放得太晚，导致忙碌时已经跑了 `listSuggestionTargets` 或 `buildAssignments`。

**步骤 3：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestMergeSuggestionBackgroundSlice|TestPersonMergeSuggestion' -count=1
```

**步骤 4：提交**

```bash
git add backend/internal/service/person_merge_suggestion_service.go backend/internal/service/person_merge_suggestion_service_test.go
git commit -m "feat(people): gate merge suggestion background slices"
```

---

## 阶段 4：资源背压和状态 API

Phase 4 增加 best-effort 资源信号和可见性。不能让正确性依赖 host-level CPU/iowait 精度，因为容器和 NAS 环境里的测量可能只是近似值。

### 任务 13：新增轻量 runtime load sampler

**文件：**

- 新建：`backend/internal/service/background_load_sampler.go`
- 新建：`backend/internal/service/background_load_sampler_test.go`

**步骤 1：写 parser 测试**

测试中不要 shell out。只测试解析：

- `/proc/loadavg`
- `/proc/stat` delta
- `/proc/meminfo`

iowait 可选；不可用时返回 unknown。

**步骤 2：实现 sampler**

暴露：

```go
type BackgroundLoadSnapshot struct {
    Load1        float64
    CPUUserPct   float64
    CPUSystemPct float64
    CPUIOWaitPct float64
    MemUsedPct   float64
    CapturedAt   time.Time
}
```

Linux-only best effort；不支持的平台返回 unknown，不阻塞 background work。

sampler 只是 advisory。不能把它作为唯一保护机制；Phase 2/3 的 foreground scope 和 cooldown 规则仍是权威机制。

**步骤 3：运行测试**

```bash
cd backend
go test ./internal/service -run TestBackgroundLoadSampler -count=1
```

**步骤 4：提交**

```bash
git add backend/internal/service/background_load_sampler.go backend/internal/service/background_load_sampler_test.go
git commit -m "feat(service): sample runtime load for background throttling"
```

### 任务 14：把资源背压接入 coordinator

**文件：**

- 修改：`backend/internal/service/background_task_coordinator.go`
- 修改：`backend/internal/service/background_task_coordinator_test.go`
- 修改：`backend/pkg/config/config.go`
- 修改：`backend/config.prod.yaml.example`
- 修改：`backend/config.dev.yaml`
- 测试：`backend/pkg/config/config_test.go`

**步骤 1：增加配置测试**

在 `background:` 下增加：

```yaml
background:
  auto_tasks_enabled: true
  cpu_pause_threshold: 70
  iowait_pause_threshold: 15
  memory_pause_threshold: 85
  db_locked_cooldown_seconds: 120
```

测试 defaults 和 validation。

默认 rollout 行为：

- `auto_tasks_enabled: true`
- CPU/iowait/memory 只有在 sampler value known 时才启用 threshold。
- unknown sampler value 不能单独拒绝 P2。

**步骤 2：增加 coordinator 测试**

测试：

- CPU 超阈值时拒绝 P2。
- iowait 超阈值时拒绝 P2。
- P1 允许运行，但可标记 throttled。
- P0 foreground scope 永远不能被拒绝。

**步骤 3：实现 thresholds**

coordinator 在 automatic task begin 时评估 sampled load。

decision reasons：

- `foreground_active`
- `cooldown`
- `cpu_high`
- `iowait_high`
- `memory_high`
- `automatic_disabled`

不要 throttle P0 前台操作。不要在前台 request handler 中因为 load pressure sleep。

**步骤 4：运行测试**

```bash
cd backend
go test ./internal/service -run TestBackgroundTaskCoordinator -count=1
go test ./pkg/config -run TestConfig -count=1
```

**步骤 5：提交**

```bash
git add backend/internal/service/background_task_coordinator.go backend/internal/service/background_task_coordinator_test.go backend/pkg/config/config.go backend/pkg/config/config_test.go backend/config.dev.yaml backend/config.prod.yaml.example
git commit -m "feat(service): throttle automatic background tasks by load"
```

### 任务 15：把 SQLite busy/locked 反馈给 coordinator

**文件：**

- 修改：`backend/internal/service/people_service.go`
- 修改：`backend/internal/service/person_identity_profile_service.go`
- 修改：`backend/internal/service/person_merge_suggestion_service.go`
- 修改：`backend/internal/service/background_task_coordinator.go`
- 测试：`backend/internal/service/background_task_coordinator_test.go`

**步骤 1：写测试**

新增 `TestBackgroundTaskCoordinator_DatabaseLockedStartsCooldown`。

**步骤 2：新增 helper**

创建 helper：

```go
func isSQLiteBusyOrLocked(err error) bool
```

放在 shared service file 或 database helper。

helper 必须处理：

- 尽量使用 `errors.Is` / `errors.As`。
- `sqlite3.ErrBusy`
- `sqlite3.ErrLocked`
- GORM-wrapped SQLite busy/locked errors。
- 字符串 `database is locked` 只能作为 fallback。

**步骤 3：报告 DB busy**

当 P2 automatic background code 遇到 SQLite locked/busy：

- 调用 `coordinator.ReportDBBusy(err)`。
- coordinator 进入 automatic-task cooldown。

不要让前台操作因此 sleep。

前台代码可以上报 busy telemetry，但绝不能因为 coordinator 而等待、长 sleep retry，或改变用户可见错误。

**步骤 4：运行测试**

```bash
cd backend
go test ./internal/service -run 'TestBackgroundTaskCoordinator|TestPeople|TestIdentity|TestMergeSuggestion' -count=1
```

**步骤 5：提交**

```bash
git add backend/internal/service/people_service.go backend/internal/service/person_identity_profile_service.go backend/internal/service/person_merge_suggestion_service.go backend/internal/service/background_task_coordinator.go backend/internal/service/background_task_coordinator_test.go
git commit -m "feat(service): back off automatic tasks after sqlite busy"
```

### 任务 16：新增后台状态 API

**文件：**

- 新建：`backend/internal/model/background_task.go`
- 新建：`backend/internal/api/v1/handler/background_handler.go`
- 修改：`backend/internal/api/v1/handler/handler.go`
- 修改：`backend/internal/api/v1/router/router.go`
- 测试：`backend/internal/api/v1/handler/background_handler_test.go`

**步骤 1：写 handler 测试**

新增测试：

- `GET /api/v1/background/status` 返回 coordinator snapshot。
- snapshot 包含 running tasks、pause reasons、foreground count、cooldowns、load snapshot。

**步骤 2：实现 DTO**

新增：

- `BackgroundStatusResponse`
- `BackgroundTaskRuntime`
- `BackgroundPauseReason`

**步骤 3：实现 handler 和 route**

添加 route：

```go
api.GET("/background/status", handlers.Background.GetStatus)
```

同时在 `Handlers` 结构体和 `NewHandlers` 中接入 `BackgroundHandler`。

**步骤 4：运行测试**

```bash
cd backend
go test ./internal/api/v1/handler -run TestBackgroundHandler -count=1
go test ./internal/api/v1/router -count=1
```

**步骤 5：提交**

```bash
git add backend/internal/model/background_task.go backend/internal/api/v1/handler/background_handler.go backend/internal/api/v1/handler/handler.go backend/internal/api/v1/router/router.go backend/internal/api/v1/handler/background_handler_test.go
git commit -m "feat(api): expose background task status"
```

### 任务 17：前端展示后台状态

**文件：**

- 新建：`frontend/src/api/background.ts`
- 新建或修改：`frontend/src/types/background.ts`
- 修改：`frontend/src/views/People/index.vue`
- 测试：frontend type check

**步骤 1：新增 API client**

创建 `backgroundApi.getStatus()`。

**步骤 2：在 People 页面增加紧凑状态条**

在 People 页面任务区域展示：

- 当前 running automatic task class。
- pause reason。
- foreground 是否 active。
- CPU/iowait snapshot，若 available。

UI 保持紧凑，不做 marketing-style panel。

**步骤 3：保守轮询**

仅 People 页面打开时轮询，频率最多每 30 秒一次。

**步骤 4：运行 type check**

```bash
cd frontend
npx vue-tsc --noEmit
```

**步骤 5：提交**

```bash
git add frontend/src/api/background.ts frontend/src/types/background.ts frontend/src/views/People/index.vue
git commit -m "feat(frontend): show background task pressure status"
```

---

## 阶段 5：后续计划，不在第一波直接改代码

Phase 5 必须等 Phase 0-4 本地和 NAS 验收通过后再开始。Phase 5 是后续计划边界，不属于第一波实现。

修改任何非 People worker 前，必须先：

- 写 worker-specific 设计说明。
- 判断该工作是 P1 用户触发持久任务，还是 P2 自动维护。
- 找到最小安全 slice boundary。
- 证明 skipped P2 work 仍 pending，不会被标记 completed。
- 证明 worker 输出语义不变。

### 任务 18：为 thumbnail/geocode 写后续治理计划

**文件：**

- 阅读：`backend/internal/service/thumbnail_service.go`
- 阅读：`backend/internal/service/geocode_task_service.go`
- 新建：`docs/plans/YYYY-MM-DD-thumbnail-geocode-background-governance.md`

**步骤 1：梳理当前 worker 行为**

找到最小安全单元：

- one thumbnail job
- one geocode job

区分：

- 用户显式触发的 rebuild job
- 自动维护/retry loop
- startup catch-up 行为

**步骤 2：写后续计划**

后续计划必须包含测试证明：

- coordinator 拒绝 P2 时，automatic worker skip。
- 用户触发的 P1 job 仍可用。
- skipped work 仍 pending。
- 相同输入下 thumbnail/geocode 输出 byte-for-byte 或 field-for-field 等价。

**步骤 3：只提交计划**

本任务不修改 thumbnail/geocode 代码。

```bash
git add docs/plans/YYYY-MM-DD-thumbnail-geocode-background-governance.md
git commit -m "docs(background): plan thumbnail and geocode governance"
```

### 任务 19：为 AI analysis 和 event curation 写后续治理计划

**文件：**

- 阅读：`backend/internal/service/analysis_runtime_service.go`
- 阅读：`backend/internal/service/analysis_service.go`
- 阅读：`backend/internal/service/event_*`
- 新建：`docs/plans/YYYY-MM-DD-analysis-curation-background-governance.md`

**步骤 1：分类工作**

分类：

- 用户触发 AI analysis 是 P1。
- 自动 event clustering/curation 是 P2。
- startup 或 scheduled curation 默认视为 P2，除非它明确由用户动作触发。

**步骤 2：写后续计划**

后续计划必须包含测试证明：

- coordinator 拒绝 P2 时，automatic curation skip。
- 用户触发 analysis 不被当作 P2 拒绝。
- analysis prompts、provider selection、tags、captions、event clustering rules、curation ranking 语义不变。

**步骤 3：只提交计划**

本任务不修改 AI/event 代码。

```bash
git add docs/plans/YYYY-MM-DD-analysis-curation-background-governance.md
git commit -m "docs(background): plan analysis and curation governance"
```

---

## 阶段 6：本地和 NAS 验证

### 任务 20：本地回归测试

运行：

```bash
cd backend
go test ./...
cd ../frontend
npx vue-tsc --noEmit
npm run build
```

预期：

- 后端测试通过。
- 前端 type check 通过。
- 前端 production build 通过。

测试修复必须单独 commit，不要混入功能 commit。

### 任务 21：部署前 NAS 只读验证

先做只读检查：

```bash
ssh davidnas.tailb2206.ts.net "/usr/local/bin/docker stats --no-stream relive relive-ml"
ssh davidnas.tailb2206.ts.net "/usr/local/bin/docker logs --since 30m --timestamps relive | tail -200"
```

记录 baseline：

- relive CPU
- relive memory
- iowait，若可用
- `protoCache rebuilt` 次数
- `database is locked` 次数
- 前台 split/move 请求耗时

### 任务 22：部署后 NAS 验证

部署后验证：

1. 打开 People 页面。
2. 对一个小测试人物触发一次 split/move。
3. 确认 API 快速返回，或前端显示 processing state 且不会重复提交。
4. 确认不会创建 repeated split chain。
5. 确认 `protoCache rebuilt` 不再以“长时间持有前台阻塞路径”的形式出现。
6. 确认 `GET /api/v1/background/status` 返回 pause/running reasons。
7. 确认 CPU/iowait high state 会暂停 P2 automatic work。

使用：

```bash
ssh davidnas.tailb2206.ts.net "/usr/local/bin/docker logs --since 20m --timestamps relive | grep -E 'background|protoCache|writeGate|people clustering|identity profile|database is locked|POST     \"/api/v1/people/(split|move-faces)'"
```

---

## 验收标准

- 首次合法 split/move/merge 行为不变。
- 重复相同 split 请求不会创建人物链。
- 重复相同 move 请求不会产生重复副作用。
- split 幂等必须依赖匹配的 `person_split` feedback event 或更强幂等证明；不能只依赖 `manual_lock_reason = "split"`。
- 前台 split/move/merge 不会等待多分钟 proto-cache refresh。
- `protoCache` cold build 和 stale refresh 都在 `writeGate.RLock()` 外执行。
- `protoCache` 保持 people clustering coordinator worker single-owner；如果实现选择多 goroutine，必须引入显式锁并通过 race test。
- Identity ANN full rebuild 请求被合并并受 cooldown 限制。
- Merge suggestion refresh 在系统忙时跳过重型工作，但 dirty/stale 状态必须保留。
- P2 automatic tasks 在 foreground active、已知 CPU/iowait/memory 超阈值、SQLite busy/locked 时暂停。
- 后台状态可通过 API 和 People UI 看到。
- SQLite task-state 写入保持低频，不引入高 churn DB 队列。
- Phase 5 第一波只产出后续计划，不修改 thumbnail/geocode/AI/event 代码。

## 明确不做

- 不在本计划中把 SQLite 迁移到 PostgreSQL。
- 不引入 Redis、RabbitMQ、Kafka、Temporal 或其他外部队列。
- 不重写 face recognition、identity scoring、merge-suggestion thresholds。
- 不把所有后台任务持久化到 DB。
- 不以“最快完成后台任务”为优化目标。
- 不改变现有 URL ID 或 People merge 语义。
- 不改变 People 列表/详情展示策略、hidden/category 行为或照片筛选行为。

## Agent 交接规则

每个接手本计划任务的 agent 必须：

- 先阅读对应 task、`行为等价范围` 和每个列出文件附近的当前代码。
- 除 instrumentation-only 任务外，先写或更新测试，再改生产代码。
- 运行 task 中列出的精确 targeted tests。
- 每个 commit 只覆盖一个 task。
- 如果实现需要改变 scoring threshold、展示语义、API response shape、高 churn 队列表结构，或在第一波修改非 People worker 代码，必须停止并请求计划修订。
