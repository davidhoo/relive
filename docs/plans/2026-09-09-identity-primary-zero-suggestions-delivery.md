# Delivery: primary 零合并推荐修复与身份匹配统一

日期：2026-09-09  
分支：`fix/identity-primary-zero-suggestions`  
工作树：`.worktrees/fix/identity-primary-zero-suggestions`  
计划：`docs/plans/2026-09-09-identity-primary-zero-suggestions-fix.md`  
引擎版本：`identity-engine-v3`（本修复前问题二进制为 v1/v2 构建）

## 状态总览

| 门禁 | 状态 |
| --- | --- |
| 代码（本地可审阅） | 是 |
| 统一评分核心（组件+人物） | 是 |
| 副本四对链路复现 + 召回修复 | 是（ExactK+保留席位后 5/5 稳定 4/4） |
| `go test ./...` | 通过（2026-09-09T14:20Z） |
| 身份相关 `-race` | 通过；无 DATA RACE。全包 race 下有既有超时 flake（事件聚类/PeopleServiceCluster），非本次路径 |
| 前端 `npm run build` | 通过（需先 `npm install`） |
| 已部署生产 | **否**（本文不授权上线） |
| 线上内存 ANN 当时是否召回四对 | **待证实** |

## 1. 修改清单（按主题）

### 统一引擎与策略
- `identity_matching_engine.go` / `score.go` / `strategy.go` / `types.go`
- 组件走 `ScoreComponentAgainstPerson`（单向、`SingleDirection`、真实 `MinSupportCount`）
- 人物对走 `ScoreIdentityEvidence`（双向取 min）
- 指纹含 `engine_version`、`recall_ann_k`、`recall_exact_k`

### 召回
- ANN：`CenterID` 排序插入 + 固定 `Rng`（减方差；**不能**声称 HNSW 完全确定性，见 §3）
- `ExactTopPeople` 有界精确 cosine 补召（默认 ExactK=100）
- 截断时对精确补召命中保留席位（避免 ANN 并集噪声挤掉阈值邻近对）
- 零命中 ≠ 索引不可用（`ready` 显式信号）
- ID 不再决定截断集合（仅同分稳定排序）
- shadow matcher（rescue 遥测路径）仍为 ANNK-only，未加 ExactK——只做遥测、不影响归属/推荐决策

### 推荐巡检 / 重试 / stale
- 不可用 ≠ 空成功；失败目标进有界重试；耗尽标记 `stale_reason=retry_exhausted_needs_revalidation`
- `ApplySuggestion` 硬拦非空 `StaleReason`
- 任务 `partial` + 前端「部分完成」pill

### 诊断
- `DiagnoseMergeSuggestionTargets` + `cmd/relive-merge-suggestion-diagnose`
- 可选：`cmd/relive-identity-agg-sample`（聚合论证样本）

### 前端
- `people.ts` / `peopleHelpers.ts` / `People/index.vue`：partial、重试计数、stale 展示

## 2. 逐项根因（已证实 / 待证实）

### 已证实

1. **评分/推荐策略不是「零 pending」的充分解释**  
   副本上四对精确分与基线 §2.2 一致（约 0.55–0.59），suggest 阈值 0.55 下策略均接受。

2. **问题版本在新鲜 ANN（仅 ANNK=50、无 ExactK）下，四对主要丢在召回**  
   DropStage=`recall`（非硬阻断、非阈值、非持久化空写）。`trunc=false` 表明丢在 ANN top-50，不是 MaxCandidates=200 的 ID 截断（后者仍是正确卫生修复，但不是该副本丢失主因）。

3. **HNSW 边界召回不稳定**  
   - 插入无序 + `coder/hnsw` 默认 `Rng=time.Now` + **`layer.entry()` 按 map 迭代** → 同库多次重建召回可漂移。  
   - 这解释了基线 §2.2「前三对已召回」与早期诊断「0/4 召回」的并存：** nondeterminism，不是两份报告互斥错误**。

4. **仅抬 ANNK（50→100→200→400）不能稳定召回四对**；墙钟几乎不变（重建主导，~5.5s/两目标）。

5. **ExactK=100 + 精确补召保留席位** 后，副本上 5 次重建 **5/5（4/4 accepted）稳定**；未降阈值。

6. **`ApplySuggestion` 过去可不校验 stale**；现已硬拦。

### 待证实（部署后阶段统计才能坐实）

1. 线上进程**当时**内存 ANN 是否召回过这四对（索引代次 / invalid / delta）。  
2. 线上是否另有持久化/去重/版本失效静默丢建议（副本链路在修复后可写出，不等于线上当时写过）。  
3. ExactK 默认 100 在全量 172 目标巡检上的耗时与内存峰值（副本两目标 ~5.9s，含重建；生产热索引上每目标 ExactK×中心数，需有界观测）。

## 3. 基线 HNSW 矛盾的正式口径

| 来源 | 结论 | 如何理解 |
| --- | --- | --- |
| 基线独立 HNSW（§2.2） | 前三对「已召回」 | 某次建图下的近似结果 |
| 修复前诊断 CLI | 常 0/4 `recall` | 另一次建图；382656 曾 rank=1 再消失 |
| 排序+固定 Rng 后仍漂移 | 证实 | `entry()` map 迭代使图入口非确定性 |
| ExactK 路径 | 5/5 稳定 4/4 | **不依赖** HNSW 边界，作为生产召回补强 |

交付要求：不要写「线上唯一根因=召回漏」；写「副本+当前代码下丢失在召回；线上运行时索引状态待部署后证实」。

## 4. 行为变化清单

| 项 | 旧 | 新 |
| --- | --- | --- |
| 引擎版本 | v2（及更早） | `identity-engine-v3` |
| 默认召回 | ANNK=50 并集截断 200 | + ExactK=100 精确补召 + 保留席位 |
| 配置指纹 | 不含召回预算 | 含 `recall_ann_k` / `recall_exact_k` → 旧 pending 应按版本失效有界再生 |
| 目标无画像 | 易被当成无候选空成功 | `unavailable` + 重试，不空替换 pending |
| 就绪零命中 | 曾误报 unavailable | `no_candidate` |
| stale pending | 可直接 Apply | 拒绝，需重跑巡检刷新 |
| 任务完成态 | 有失败也像完成 | `partial` + 重试计数 |
| 组件打分 | 曾伪双向/MinSupport=1 | 单向 + 真实支撑数；IncompleteEvidence → wait |

**未做**：自动合并四对；降 suggest 阈值；全库两两比较；切换模式离开 primary。

## 5. 组件单向聚合论证（副本样本，2026-09-09）

工具：`go run ./cmd/relive-identity-agg-sample /tmp/relive-diag-slim2.db`（只读）。

| 样本 | 结果 | 含义 |
| --- | --- | --- |
| 265250（多中心）↔265274 双向 | score=0.5878（fwd≈rev 较弱侧） | 合并推荐必须双向覆盖，避免单侧高分 |
| 取 265250 的 1 个中心作组件 → 自身 | score=1.0，single=true，support=77 | 聚类吸附不要求反向盖住人物全部外观 |
| 同组件 → 265274 | 单向 0.6224 > 双向人物对 0.5878 | 单向可高于双向；自动归属另有 margin/阈值/支撑 |
| 同组件 → 巨人物 271594（截断 30 中心） | 单向 0.205 | 未到自动门槛；不会「一个中心贴上混合身份」 |
| 265250 ↔ 271594 双向 | 0.0908（rev 更弱） | 混合身份负例：双向取 min 压分 |
| 回归测试 | `TestScoreComponentAgainstPerson_DoesNotRequireReverseCoverage` | 正交第二中心拉低双向、不拉低组件单向 |

结论：人物合并用双向 min；组件聚类用单向真实支撑。策略层（rescue≥suggest、margin、MinCenterFaces）继续把「仅推荐门槛」挡在自动归属外。

## 6. 测试与副本成本记录

- `go test ./... -count=1`：通过  
- `go test ./internal/service/ -race -run 'TestRecall|TestDiagnose|TestApplySuggestion_RejectsStale|TestIdentityProfileANN_|TestPrimaryAssignments|TestScoreComponent|TestEngine_|TestMarkPending|TestSelectPerson'`：通过，0 DATA RACE  
- 全包 `-race`：`TestEventClustering_AutoIncremental_DeferredWhenForegroundActive`、`TestPeopleServiceCluster/高置信度并入已有人物` 超时 flake（既有慢测试+race 减速）；**无 DATA RACE 报告**  
- 前端：`npm install` + `npm run build` 通过  
- 副本诊断（14137 centers，两目标）：重建+诊断 ~5.5–5.9s；ExactK 相对关补召无明显墙钟膨胀（重建仍主导）  
- Exact 扫描复杂度：每查询向量 O(中心数)；默认 ExactK 只截断结果人数，扫描仍走全 snapshot centers（有界：中心全集，非常规全人物两两 ComparePeople）

## 7. 部署与回退（待授权）

### 部署前
1. `sqlite3 ... ".backup"` 一致性备份并校验  
2. 构建明确标记产物（含 `identity-engine-v3`）  
3. 确认 `identity_profile_mode: primary`、suggest=0.55、rescue≥suggest  

### 部署后验证
1. 诊断/阶段统计：焦点四对 DropStage（期望可 `accepted` 或由统计解释）  
2. pending 可读、元数据含 engine/strategy/fingerprint  
3. 不自动 apply；不重置人工约束  

### 回退
1. 保留前一镜像与 config  
2. 停/排空合并推荐任务后回退二进制或模式  
3. 策略/引擎版本不兼容的 pending：标记失效有界再生；**保留** applied/dismissed 人工反馈  
4. 回退不撤销已发生自动归属；若需修复用既有批次预演/撤销  

### 配置迁移
- 旧 fingerprint 建议在巡检中自然过期；可选有界 `MarkPendingStaleReason` / 再生  
- **不要**把阈值从 0.55 下调当作本修复的一部分  

## 8. 明确未宣称完成的事项

- 生产部署与线上阶段统计坐实  
- Apply 时现场 `ComparePeople` 重验（当前仅 stale 硬拦；增强记后续）  
- 全库历史重聚类 / 换 embedding / 自动执行合并  

**完成标准对齐**：缺失路径有证据与回归、评分共用、不可用可恢复、接受结果可持久化——本地+副本已满足可审阅交付；**上线验证仍待授权**。
