# People Identity Profile Task 7: Center ANN Snapshot and Delta Index Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 为人物多中心身份画像建立可并发查询、可原子替换的 HNSW 快照与有界增量索引，为后续画像匹配提供候选人物召回能力，同时保持现有聚类行为完全不变。

**Architecture:** 使用不可变 HNSW snapshot 承载最近一次完整索引，使用内存 delta 承载 snapshot 之后激活的新中心，并用 invalid 集合屏蔽旧 generation 和已删除人物。完整重建在锁外完成，校验成功后通过 `atomic.Pointer` 一次性切换；查询阶段合并 snapshot 与 delta、按人物去重，并在任何不确定状态下返回 `ready=false`，由后续调用方走精确回退。

**Tech Stack:** Go、GORM、SQLite、`github.com/coder/hnsw`、`sync/atomic`、Go race detector

---

## 一、任务边界

### 本任务包含

1. 新建人物画像中心专用 ANN 组件。
2. 从活动 generation 的中心构建完整 snapshot。
3. 支持 generation 激活后的 delta 增量更新和旧中心失效。
4. 支持人物删除、generation 过期、模型签名变化的 fail-closed 过滤。
5. 将画像构建成功后的 generation 激活接入 delta 更新。
6. 在非 `legacy` 模式的后台切片中完成首次构建及按需重建。
7. 增加并发、竞态、失败回退和最小 benchmark 覆盖。

### 本任务不包含

- 不把 ANN 结果接入现有自动聚类、增量聚类或人物合并建议。
- 不改变任何现有聚类阈值、候选召回、重试或人物归属规则。
- 不启用 `shadow`、`rescue` 或 `primary`；部署配置继续保持 `legacy`。
- 不实现最终画像相似度、margin、负证据和 cannot-link 判断，这些属于 Task 8。
- 不新增 API、前端页面或运维开关。
- 不复用或修改现有 `person_merge_suggestion_ann.go` 的索引实例；仅复用其 HNSW 使用经验。
- 不创建独立常驻 goroutine，不允许无界 delta、无界重试或前台请求触发全量重建。

## 二、部署与兼容性硬约束

1. `identity_profile_mode=legacy` 时：
   - 不创建 ANN 实例；
   - 不读取中心表；
   - 不构建 snapshot；
   - 不分配 delta/invalid 大型 map；
   - 不启动任何 ANN 后台工作；
   - 现有聚类结果、时序和性能保持不变。
2. `shadow/rescue/primary` 才允许初始化 ANN；本任务虽然支持这些模式，但不负责切换部署配置。
3. ANN 是派生缓存，不是真相来源。数据库中的活动 profile generation 始终是权威数据。
4. ANN 不可确认完整正确时必须返回 `ready=false`，不得返回看似正常的部分候选。
5. ANN 更新失败不得回滚已经成功提交的画像 generation，也不得破坏旧 snapshot；应标记不可用并请求重建。

## 三、文件范围

**Create:**

- `backend/internal/service/person_identity_profile_ann.go`
- `backend/internal/service/person_identity_profile_ann_test.go`

**Modify:**

- `backend/internal/service/person_identity_profile_service.go`
- `backend/internal/service/person_identity_profile_service_test.go`
- `backend/internal/repository/person_identity_profile_repo.go`
- `backend/internal/repository/person_identity_profile_repo_test.go`

除非测试证明必要，不修改其他生产文件。

## 四、核心数据结构与接口

实现时可调整未导出命名，但必须保持以下语义：

```go
type profileCenterVector struct {
	CenterID  uint
	PersonID  uint
	Generation int
	Embedding []float32
}

type identityCenterIndex struct {
	graph       *hnsw.Graph[uint]
	centerOwner map[uint]uint
	generation  map[uint]int
	model       string
}

type identityProfileANN struct {
	snapshot atomic.Pointer[identityCenterIndex]

	// coder/hnsw 的 Search 不是并发安全的，snapshot 查询必须串行化。
	searchMu sync.Mutex

	deltaMu         sync.RWMutex
	delta           map[uint]profileCenterVector
	invalid         map[uint]struct{}
	activeGeneration map[uint]int
	revision         uint64

	unavailable atomic.Bool
	rebuildRequested atomic.Bool
}
```

建议提供以下未导出能力：

```go
func newIdentityProfileANN(model string) *identityProfileANN
func (a *identityProfileANN) Search(query []float32, k int, model string) ([]uint, bool)
func (a *identityProfileANN) Rebuild(centers []*model.PersonIdentityCenter, model string) error
func (a *identityProfileANN) Activate(personID uint, generation int, centers []*model.PersonIdentityCenter) error
func (a *identityProfileANN) InvalidatePerson(personID uint)
func (a *identityProfileANN) RequestRebuild()
```

约束：

- 使用独立常量名，例如 `identityProfileANNM`、`identityProfileANNEfSearch`，避免与现有包级 `annSearchK` 等名称冲突。
- `Search` 返回按候选质量稳定排序的唯一人物 ID；相同得分以 `person_id ASC` 打破平局，保证测试可重复。
- `k <= 0`、空向量、维度不一致、NaN/Inf、零范数均 fail closed。
- 不向日志输出 embedding、图片路径、API key 或中心 BLOB。

## 五、实施步骤

### Step 1：先补活动中心查询的精度护栏

先写失败测试，再修改 `ListAllActiveCenters`，使完整 snapshot 的输入天然满足：

- center generation 等于 profile 的 `active_generation`；
- profile 的 `embedding_model` 等于当前服务模型签名；
- 对应人物仍存在于 `people` 表；
- 只返回活动中心，按 `person_id ASC, ordinal ASC` 排序。

推荐将接口改为：

```go
ListAllActiveCenters(embeddingModel string) ([]*model.PersonIdentityCenter, error)
```

查询必须通过 JOIN 在数据库侧过滤，不允许先全量加载再在 Go 内过滤。

运行：

```bash
cd backend && go test ./internal/repository -run 'TestPersonIdentityProfileRepository_ListAllActiveCenters' -v
```

预期：新增测试先失败，最小实现后通过。

### Step 2：实现 snapshot 构建和输入校验

测试至少覆盖：

- 最近中心能召回其所属人物；
- 同一人物多个中心只返回一个人物 ID；
- 空中心集合构建出“已就绪但无结果”的合法 snapshot；
- 重复 center ID、零 ID、零 person ID、非法 generation 被拒绝；
- embedding 解码失败、维度不一致、NaN/Inf、零范数被拒绝；
- 构建模型签名与 ANN 当前模型不一致时拒绝切换；
- 构建失败后旧 snapshot 仍可查询，但组件标记重建失败时对外 `ready=false`。

实现要求：

1. 在锁外完成 BLOB 解码、校验、HNSW 建图和 metadata map 构造。
2. 只有所有节点和元数据校验通过后才允许切换 snapshot。
3. `graph.Distance = hnsw.CosineDistance`。
4. 建图过程中不得持有 `deltaMu` 或 `searchMu`。
5. 成功 snapshot 必须是只读对象，发布后不得修改其 graph 和 map。

运行：

```bash
cd backend && go test ./internal/service -run 'TestIdentityProfileANN_(Snapshot|Validation|Model)' -v
```

### Step 3：实现 delta、失效集合和 generation 防护

测试至少覆盖：

- 已有 snapshot 后，新激活中心无需完整重建即可被查询；
- 新 generation 激活后，同一人物旧 generation 的中心立即失效；
- snapshot 中已失效中心不会泄漏到结果；
- delta 中被再次替换的中心不会泄漏；
- 删除人物后，其 snapshot 和 delta 中心均不可返回；
- snapshot 与 delta 命中同一人物时只返回一次；
- delta 达到内部上限后请求重建，不允许继续无界增长；
- delta 更新中途失败时，设置 `unavailable=true` 和 `rebuildRequested=true`，查询返回 `ready=false`。

查询流程：

1. 原子读取 snapshot；缺失则返回 `ready=false`。
2. 校验请求模型签名和 snapshot 模型签名；不一致返回 `ready=false`。
3. 在短暂 RLock 下复制查询需要的 delta/invalid/generation 元数据，随后释放锁。
4. 在 `searchMu` 下调用 HNSW `Search`。
5. 对 delta 做精确 cosine 计算。
6. 合并两路结果，过滤 invalid、非活动 generation 和无效 owner，按人物去重并稳定排序。

不要在持有 `deltaMu` 时执行 HNSW Search 或遍历大向量集合。

### Step 4：保证重建与并发激活不丢更新

完整重建开始时记录 `revision`，完成后再进入短临界区：

- 若 revision 未变化：切换 snapshot，并安全清空已被 snapshot 覆盖的 delta/invalid。
- 若 revision 已变化：仍可切换已验证 snapshot，但必须保留构建期间产生的 delta/invalid；下一次干净重建再压缩。
- snapshot 切换必须通过 `atomic.Pointer.Store` 完成，旧 snapshot 由 Go 可达性保证供在途读者使用。
- 并发查询只能看到完整旧 snapshot 或完整新 snapshot，不能看到半建图状态。

测试使用 goroutine 持续查询，同时循环重建和激活 generation，并在 `-race` 下验证：

- 无 data race；
- 无 panic；
- 不返回已失效人物；
- 不出现部分 metadata；
- 不丢失重建期间的 delta 更新。

### Step 5：接入画像后台服务，但不接入聚类

修改 `personIdentityProfileService`：

1. 新增 ANN 字段；仅非 `legacy` 模式初始化。
2. 非 `legacy` 初始化时将 `rebuildRequested` 设为 true；首次 ANN 构建只在现有后台切片中执行，不阻塞服务构造和 HTTP 请求。
3. `RunBackgroundSlice` 在已有 cooldown、专用后台 DB 和单次切片约束内，按需调用 `ListAllActiveCenters(currentModel)` 并重建。
4. `ReplaceGeneration` 成功后：
   - 从已持久化的 active build 获取真实 center ID 和 generation；
   - 调用 `Activate` 使旧 generation 失效并写入新 delta；
   - ANN 更新失败只标记 ANN 不可用并请求重建，不回滚数据库 generation。
5. 人物在构建期间被删除时，清理画像后调用 `InvalidatePerson`。
6. `legacy` 的现有 no-op 测试扩展为断言 ANN 不初始化、Repository 的 `ListAllActiveCenters` 调用次数为 0。

注意：`ReplaceGeneration` 会把 builder ordinal 重映射成真实 center ID，因此不得直接把写入前的 `build.Centers` 放入 ANN。必须在事务成功后重新读取 `GetActive(personID)`，或通过等价方式取得数据库真实 ID。

### Step 6：失败恢复规则

必须实现以下状态语义：

- 从未成功构建 snapshot：`ready=false`。
- 成功构建空 snapshot：`ready=true`，候选为空。
- 最近一次安全 delta 更新成功：snapshot + delta 可查询。
- delta 更新失败或状态无法证明完整：`ready=false`，等待完整重建。
- 完整重建失败：不发布半成品，`ready=false`，保留旧 snapshot 供诊断但不得对外声称可用。
- 后续完整重建成功：清除 `unavailable`，恢复 `ready=true`。
- 模型签名变化：旧 snapshot 不得查询，必须重新构建。

当前 Task 7 没有真实调用方；Task 8 以后看到 `ready=false` 时必须走精确匹配回退，而不是当作“无匹配”。

### Step 7：完整测试和 benchmark

运行：

```bash
cd backend && go test ./internal/repository -run 'IdentityProfile' -v
cd backend && go test ./internal/service -run 'IdentityProfileANN|PersonIdentityProfileService' -race -count=1 -v
cd backend && go test ./internal/service -run '^$' -bench 'BenchmarkIdentityProfileANN' -benchtime=1x
cd backend && go test ./... -count=1
```

benchmark 至少包含：

- 代表性 center 数量的 snapshot 构建；
- 单 query 的 snapshot 查询；
- snapshot + 小规模 delta 查询。

benchmark 只作烟雾验证，不在本任务设置未经真实 NAS 数据校准的硬性能阈值；必须输出 center 数量和 `ns/op`，且不能出现随查询次数持续增长的内存。

## 六、验收标准

- [ ] `legacy` 模式对 ANN、Repository 和现有聚类调用链完全 no-op。
- [ ] Task 7 没有修改现有聚类、合并建议或人物归属结果。
- [ ] snapshot 构建和切换是原子的，并通过 `-race`。
- [ ] delta 能召回 snapshot 之后的新中心，且大小有明确上限。
- [ ] 旧 generation、删除人物、错误模型和失效中心均不会进入候选结果。
- [ ] 同一人物的多个中心在结果中严格去重。
- [ ] 任一不完整或不可证明安全的状态返回 `ready=false`。
- [ ] ANN 更新失败不回滚已成功激活的数据库 generation。
- [ ] 首次/按需重建只发生在非 `legacy` 后台路径，不阻塞 HTTP 请求。
- [ ] repository、service、race、全量测试和 benchmark 命令全部通过。
- [ ] 未记录或暴露 embedding、图片路径及其他敏感数据。

## 七、提交要求

实现完成并通过全部验收后再提交：

```bash
git add \
  backend/internal/repository/person_identity_profile_repo.go \
  backend/internal/repository/person_identity_profile_repo_test.go \
  backend/internal/service/person_identity_profile_ann.go \
  backend/internal/service/person_identity_profile_ann_test.go \
  backend/internal/service/person_identity_profile_service.go \
  backend/internal/service/person_identity_profile_service_test.go
git commit -m "feat(people): index identity centers with snapshot and delta"
```

提交前确认 `git diff --check` 无输出，且不要把部署配置从 `legacy` 改为其他模式。
