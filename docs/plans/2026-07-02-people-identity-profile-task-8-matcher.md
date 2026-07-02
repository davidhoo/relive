# People Identity Profile Task 8: Robust Matcher and Negative Evidence Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 基于人物多中心画像实现稳健、确定性、fail-closed 的精确匹配器，在扩大同一人物召回能力的同时，以绝对分数、中心分布、margin、cannot-link 和同照片共现共同守住自动聚合精度。

**Architecture:** ANN 只负责有界候选召回，最终判断必须从数据库批量读取当前活动 generation 的中心并重新做精确余弦评分。匹配器先为每张查询人脸选择候选人物的最佳稳定中心，再按组件大小使用单脸分数、质量加权中位数或截尾加权均值聚合；负证据和数据不可用只会关闭 `AutoEligible` 或返回 `Available=false`，不会放宽阈值。

**Tech Stack:** Go、GORM、SQLite、Task 7 identity-profile ANN、Go race detector

---

## 一、前置条件

- Task 7 提交 `32c96a8 feat(people): index identity centers with snapshot and delta` 已存在。
- 当前生产配置继续保持 `people.identity_profile_mode: legacy`。
- 本任务只实现 matcher 与批量数据查询，不接入现有聚类或合并建议调用链。
- 实现时必须从包含 Task 7 的分支开始；不要复制或另写第二套 ANN。

## 二、任务边界

### 本任务包含

1. 新建身份画像 matcher 及其纯评分辅助函数。
2. 使用 Task 7 ANN 对组件中每张有效人脸做候选召回。
3. 批量加载候选人物的活动中心，避免逐候选 N+1 查询。
4. 实现单脸、2–4 脸、5 脸以上三种稳健聚合规则。
5. 实现全局安全阈值、最佳/次佳 margin 和中心 P10 边界判断。
6. 实现 manual singleton 仅召回、不可自动吸附的限制。
7. 实现 cannot-link 与同照片共现负证据。
8. 保证 retry count 不参与画像阈值或评分。
9. 增加确定性、批量 SQL、错误回退和竞态测试。

### 本任务不包含

- 不修改 `peopleService` 的现有聚类决策。
- 不把 matcher 接入人物合并建议；该接入属于 Task 10。
- 不把 matcher 接入增量聚类 shadow；该接入属于 Task 11。
- 不自动移动、合并、创建人物，不写 `faces.person_id`。
- 不记录 shadow decision；该能力属于 Task 9。
- 不启用 `shadow`、`rescue` 或 `primary`。
- 不根据重试次数降低阈值，不复用 legacy 的 retry threshold decay。
- 不在 matcher 中更新中心、profile generation 或 ANN delta。
- 不新增未经校准的运行时配置项；使用 Task 1 已有 margin、rescue threshold 和中心稳定性配置。

## 三、文件范围

**Create:**

- `backend/internal/service/person_identity_profile_matcher.go`
- `backend/internal/service/person_identity_profile_matcher_test.go`

**Modify:**

- `backend/internal/repository/face_repo.go`
- `backend/internal/repository/face_repo_test.go`
- `backend/internal/repository/person_identity_profile_repo.go`
- `backend/internal/repository/person_identity_profile_repo_test.go`

除非测试证明必要，不修改 `people_service.go`、`service.go`、API、前端或配置文件。

## 四、结果结构与固定语义

实现以下结果类型；字段可补充内部诊断信息，但不得删除核心语义：

```go
type IdentityProfileMatch struct {
	Available      bool
	PersonID       uint
	Score          float64
	SecondPersonID uint
	SecondScore    float64
	Margin         float64
	CenterIDs      []uint
	AutoEligible   bool
	BlockReason    string
}
```

字段语义：

- `Available=false`：画像索引、活动中心或关键负证据不可安全使用。调用方必须走 legacy/精确回退，不能解释成“没有相似人物”。
- `Available=true, PersonID=0`：matcher 正常完成，但没有达到可报告的候选。
- `PersonID!=0, AutoEligible=false`：可以用于 shadow 分析或人工建议，但禁止自动吸附。
- `CenterIDs`：最佳人物实际参与每张脸最佳匹配的中心 ID，去重后升序排列。
- `BlockReason`：使用稳定枚举字符串，不拼接人名、路径、embedding 或数据库错误全文。

建议固定以下原因：

```text
index_unavailable
invalid_query
profile_unavailable
score_below_threshold
margin_too_small
below_center_boundary
unstable_center
cannot_link
same_photo_cooccurrence
negative_evidence_unavailable
```

如果多个护栏同时失败，按以下优先级选择 `BlockReason`：

```text
unavailable > cannot_link > same_photo_cooccurrence > unstable_center
> below_center_boundary > score_below_threshold > margin_too_small
```

## 五、匹配算法合同

### 1. 查询输入清洗

- 忽略 nil face、空 embedding、解码失败、维度不一致、NaN/Inf、零范数的人脸。
- 相同 Face ID 只计一次；按 Face ID 升序进入评分，保证浮点累加确定性。
- 如果没有任何有效人脸，返回 `Available=false, BlockReason=invalid_query`。
- `RetryCount` 不得用于筛选、权重、分数、阈值或 margin。

查询权重只使用人脸质量与人工可信度：

```text
manual_locked: weight = 1.0
automatic:     weight = clamp(quality_score, 0.05, 1.0)
```

质量为 NaN/Inf/负数时该人脸无效。不要使用 `ClusterScore`，避免历史聚类状态反向改变身份匹配阈值。

### 2. ANN 候选召回

- 对每张有效查询向量调用 Task 7 `identityProfileANN.Search`。
- 任一查询返回 `ready=false` 时，整个 matcher 返回 `Available=false`，禁止使用部分候选。
- 每张脸最多请求 50 个候选人物；组件候选并集最多保留 200 人。
- 超过 200 时按“最小 ANN rank 升序、命中脸数降序、person_id 升序”稳定截断。
- ANN 只决定候选集合，ANN 顺序和距离不得直接成为最终身份分数。

内部常量使用独立前缀，例如：

```go
const (
	identityProfileMatcherANNK          = 50
	identityProfileMatcherMaxCandidates = 200
)
```

### 3. 批量加载活动中心

向 `PersonIdentityProfileRepository` 增加批量方法，名称可调整但必须保持语义：

```go
ListActiveCentersByPersonIDs(
	personIDs []uint,
	embeddingModel string,
) (map[uint][]*model.PersonIdentityCenter, error)
```

查询要求：

- 使用 JOIN 保证 `center.generation = profile.active_generation`；
- profile 状态为 ready，活动 generation 大于 0；
- profile embedding model 等于 matcher 当前模型；
- 对应人物仍存在于 `people`；
- 输入 ID 去重、忽略 0、按 SQLite 参数上限分块；
- 每个分块使用一次批量查询，不允许每个人物调用一次 `GetActive`；
- 返回中心按 `person_id ASC, ordinal ASC, id ASC`；
- 空输入直接返回空 map，不访问数据库。

如果 ANN 返回了候选，但批量读取失败，返回 `Available=false, BlockReason=profile_unavailable`。如果候选全部因 generation/model/person 不合法而消失，也按索引与数据库不一致处理为 unavailable，并请求 ANN 重建；不得把它当作正常 miss。

### 4. 每张人脸对候选人物的精确评分

对候选人物的每张查询脸：

1. 解码并验证全部活动中心向量。
2. 计算该脸与每个中心的精确 cosine similarity。
3. 选择分数最高的中心；同分时选择较小 center ID。
4. 记录该脸的 `best_similarity`、中心 ID 和该中心 `SimilarityP10`。

任一中心数据非法时，不得静默忽略后继续自动匹配；该候选人物视为不可安全使用，并触发 `profile_unavailable`。

### 5. 组件聚合规则

设每张有效查询脸对某候选人物的最佳相似度为 `sᵢ`，质量权重为 `wᵢ`：

- 1 张脸：`score = s₁`。
- 2–4 张脸：使用质量加权中位数。
- 5 张及以上：按相似度排序，对权重分布两端各截去 10%，对剩余权重计算加权均值。

截尾必须允许边界样本只贡献剩余部分权重，而不是因样本数量较少就不截尾。所有排序同分时以 Face ID 作为稳定次序。

使用完全相同的聚合方式，对每张脸命中中心的 `SimilarityP10` 计算组件边界 `boundary`：

```text
center_fit_ok = score >= boundary
```

不得为了通过边界而添加未经校准的 epsilon 或 retry discount；仅允许极小的浮点比较容差用于测试一致性，且不能改变业务阈值。

### 6. 最佳/次佳与 margin

- 对全部候选完成精确评分后，按 `score DESC, person_id ASC` 排序。
- 最佳候选写入 `PersonID/Score`，次佳写入 `SecondPersonID/SecondScore`。
- 没有次佳时 `SecondPersonID=0, SecondScore=-1`。
- `Margin = Score - SecondScore`。
- 自动资格要求 `Score >= IdentityProfileRescueThreshold`。
- 自动资格要求 `Margin >= IdentityProfileMargin`。
- 分数或 margin 恰好等于阈值时允许通过。

禁止先过滤被负证据阻断的最高分候选、再自动选择低分候选。最高原始身份候选被阻断时，应保留该候选用于诊断并令 `AutoEligible=false`，避免把同一组件错误吸附到次佳人物。

### 7. 中心稳定性

命中中心可用于 ANN 召回和人工建议，但只有稳定中心可支持自动吸附：

- `SupportCount >= IdentityProfileMinCenterFaces` 才是自动稳定中心；
- `Confirmed=true` 但支持数不足的 manual singleton/小中心仍是 retrieval-only；
- 最佳人物中任何对最终稳健分数有有效权重贡献的中心不稳定时，设置 `AutoEligible=false, BlockReason=unstable_center`。

Task 4 builder 已保证自动中心的最小照片数；matcher 不重新全量扫描成员来重复证明该不变量。后续如果数据校验发现不变量被破坏，应返回 unavailable，而不是放宽规则。

### 8. cannot-link

- 从查询组件当前/历史 `PersonID` 收集非零 source person IDs。
- 对每个唯一 source person 调用 `CannotLinkRepository.ListByPersonID`，合并 blocked person 集合。
- 最佳候选位于 blocked 集合时，保留分数供 shadow/人工诊断，但 `AutoEligible=false, BlockReason=cannot_link`。
- cannot-link 查询失败属于安全证据不可用：返回匹配结果但禁止自动吸附，原因 `negative_evidence_unavailable`。
- source person 集合为空时，不执行 cannot-link 查询。

### 9. 同照片共现

向 `FaceRepository` 增加批量查询，名称可调整：

```go
ListPersonIDsCooccurringWithPhotos(
	photoIDs []uint,
	candidatePersonIDs []uint,
) ([]uint, error)
```

SQL 语义：

```sql
SELECT DISTINCT person_id
FROM faces
WHERE photo_id IN (...)
  AND person_id IN (...)
  AND person_id IS NOT NULL;
```

要求：

- photo/person ID 均去重并忽略 0；
- 同时对两组参数分块，保证每条 SQL 总参数数不超过 repository 安全上限；
- 使用现有 `idx_face_photo` / `idx_face_person`，不做全表加载；
- 返回人物 ID 去重升序；
- 任一输入为空时直接返回空结果；
- 不允许每个候选执行一次 SQL。

最佳候选存在同照片共现时：

- 仍保留 `PersonID/Score/CenterIDs`，供人工建议显示警告；
- `AutoEligible=false`；
- `BlockReason=same_photo_cooccurrence`。

查询失败时按 `negative_evidence_unavailable` fail closed。

## 六、TDD 实施步骤

### Step 1：为批量活动中心查询写失败测试

覆盖：

- 只返回指定人物的活动 generation；
- 排除历史 generation、非 ready profile、错误模型、已删除人物；
- 输入去重、0 过滤和空输入零 SQL；
- 超过 SQLite 安全参数数量时正确分块；
- 返回顺序稳定；
- query count 随 chunk 数增长，而不是随人物数增长。

运行：

```bash
cd backend && go test ./internal/repository -run 'TestPersonIdentityProfileRepository_ListActiveCentersByPersonIDs' -v
```

预期：新增测试先失败，实现后通过。

### Step 2：为同照片共现查询写失败测试

覆盖：

- 找到查询照片中已经属于候选人物的人脸；
- 忽略非候选人物和其他照片；
- 多张脸/多张照片只返回一次人物 ID；
- nil、空输入和 0 ID；
- photo/person 两个维度均超过分块边界；
- 不产生逐候选 N+1 查询。

运行：

```bash
cd backend && go test ./internal/repository -run 'TestFaceRepository_ListPersonIDsCooccurringWithPhotos' -v
```

### Step 3：为纯聚合函数写失败测试

不依赖数据库，表驱动覆盖：

- 单脸使用其最佳中心分数；
- 2、3、4 张脸使用质量加权中位数；
- 5 张及以上使用双侧 10% 截尾加权均值；
- 极低质量离群脸不会主导分数；
- 极高相似度单脸不能把整体分数虚高；
- 输入顺序变化结果不变；
- Face ID/center ID 同分规则确定；
- NaN/Inf/零范数/维度不一致 fail closed；
- RetryCount 从 0 改为任意值，结果完全相同。

运行：

```bash
cd backend && go test ./internal/service -run 'TestIdentityProfileMatcher_(Aggregate|Deterministic|InvalidInput)' -v
```

### Step 4：实现候选召回和批量精确评分

先写端到端 matcher 失败测试，覆盖：

- ANN 能召回非主外观的次级中心；
- 同一人物多个中心只产生一个候选；
- ANN 未就绪或中途返回 false 时整体 unavailable；
- ANN 候选超过 200 时稳定截断；
- 最终分数来自数据库活动中心，不使用 ANN 距离；
- stale generation/model mismatch 被批量查询过滤并触发 unavailable；
- 候选活动中心只通过批量 SQL 加载。

运行：

```bash
cd backend && go test ./internal/service -run 'TestIdentityProfileMatcher_(Retrieval|ExactScoring|Unavailable)' -v
```

### Step 5：实现自动资格护栏

测试覆盖：

- 达到绝对分数、margin、P10 和稳定中心要求时 `AutoEligible=true`；
- 分数低于全局阈值；
- margin 小于阈值；
- 分数低于聚合后的中心 P10 边界；
- manual singleton 可返回候选但不能自动吸附；
- cannot-link 阻断；
- 同照片共现阻断自动吸附但保留人工候选；
- 负证据查询失败时 fail closed；
- 最高分候选被阻断时不会退而自动选择次佳人物；
- 多个失败原因遵循固定优先级。

运行：

```bash
cd backend && go test ./internal/service -run 'TestIdentityProfileMatcher_(Eligibility|Margin|P10|CannotLink|Cooccurrence)' -v
```

### Step 6：运行竞态、全量相关测试和基准烟雾

```bash
cd backend && go test ./internal/repository -run 'IdentityProfile|Cooccurr' -count=1 -v
cd backend && go test ./internal/service -run 'IdentityProfileANN|IdentityProfileMatcher|PersonIdentityProfileService' -race -count=1 -v
cd backend && go test ./... -count=1
```

可添加轻量 benchmark，但不能替代 Task 15 的代表规模校准：

```bash
cd backend && go test ./internal/service -run '^$' -bench 'BenchmarkIdentityProfileMatcher' -benchtime=1x
```

## 七、验收标准

- [ ] matcher 未接入现有聚类、合并建议和人物写入路径。
- [ ] `legacy` 部署行为完全不变。
- [ ] ANN 仅召回候选，最终判断始终使用数据库活动中心精确评分。
- [ ] 活动中心与同照片共现均使用批量查询，没有逐候选 N+1。
- [ ] 单脸、质量加权中位数和截尾加权均值规则均有确定性测试。
- [ ] 全局阈值、margin、P10、稳定中心四项缺一不可。
- [ ] manual singleton 只能召回/建议，不能支持自动吸附。
- [ ] cannot-link 和同照片共现均能阻止自动吸附。
- [ ] 关键数据或负证据不可用时 fail closed。
- [ ] RetryCount 不影响任何画像分数、权重和阈值。
- [ ] 被阻断的最高分候选不会被次佳候选替代后自动吸附。
- [ ] 所有结果排序、中心 ID 和浮点聚合在相同输入下可重复。
- [ ] focused、race 和完整 backend 测试通过。
- [ ] 不记录或输出 embedding、人名、图片路径或敏感配置。

## 八、提交要求

完成实现并通过全部验收后再提交：

```bash
git add \
  backend/internal/repository/face_repo.go \
  backend/internal/repository/face_repo_test.go \
  backend/internal/repository/person_identity_profile_repo.go \
  backend/internal/repository/person_identity_profile_repo_test.go \
  backend/internal/service/person_identity_profile_matcher.go \
  backend/internal/service/person_identity_profile_matcher_test.go
git commit -m "feat(people): score identity profiles with conservative guards"
```

提交前确认：

```bash
git diff --check
git status --short
```

不要把任务说明文档、部署配置或其他任务的未提交文件误纳入实现提交。
