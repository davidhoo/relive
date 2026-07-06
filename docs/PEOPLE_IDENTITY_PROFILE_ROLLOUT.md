# 人物身份画像上线手册（PEOPLE_IDENTITY_PROFILE_ROLLOUT）

本文档指导人物身份画像（identity profile）从 `legacy` 到 `shadow`、再到 `rescue` 的渐进上线流程，配合 `relive-identity-profile-report` 只读校准报告工具与 `person_identity_profile_benchmark_test.go` 代表规模基准测试，为人工准入评审提供证据。

> **重要：Task15 完成不代表可以启用 rescue。** 生产 rescue 必须经过 shadow 数据收集、人工校准评审、NAS 性能确认和回滚演练。本工具只提供证据，不替代人工评审，也不会输出 `safe_to_enable_rescue=true` 之类的自动结论。

---

## 1. 从 NAS 在线备份取得一致的数据库副本

校准报告只能针对**备份或复制出来的 SQLite 数据库**运行，**严禁**直接连接 NAS 正在写入的生产数据库。

使用仓库已有的 NAS 在线备份工具（见 `docs/NAS_BACKUP.md`）：

```bash
# 在开发机执行，生成已校验的在线备份包
make backup-nas
```

备份包位于 `<backup-root>/YYYY-MM-DD-HHMMSS-<label>/relive.db`，由 SQLite `.backup` 命令生成，保证事务一致性。备份工具不会 `cp` 活库、`-wal`、`-shm`，也不会停止或重启 Relive 容器。

取得副本后，将其复制到工作机本地（例如 `~/relive-calibration.db`），**所有报告与 smoke test 都在副本上运行**：

```bash
# 从备份包复制到本地（权限 0600）
cp <backup-root>/YYYY-MM-DD-HHMMSS-<label>/relive.db ~/relive-calibration.db
chmod 600 ~/relive-calibration.db

# 校验副本完整性
sqlite3 -readonly ~/relive-calibration.db 'PRAGMA quick_check;'
# 期望输出：ok
```

> 副本必须与生产隔离。报告工具以 `mode=ro` + `PRAGMA query_only=ON` 打开，绝不写入；但即便如此，也只对副本运行，避免任何对生产库的误操作。

---

## 2. 构建和运行报告命令

### 构建

```bash
cd backend
go build -o relive-identity-profile-report ./cmd/relive-identity-profile-report
```

### 运行

```bash
# text 报告（默认）
./relive-identity-profile-report -db ~/relive-calibration.db -format text

# JSON 报告（便于脚本解析）
./relive-identity-profile-report -db ~/relive-calibration.db -format json > report.json
```

### 命令行合同

| 项 | 说明 |
|----|------|
| `-db` | 必填。指向数据库副本路径。 |
| `-format` | `text`（默认）或 `json`。其他值拒绝。 |
| 打开方式 | `file:<path>?mode=ro&_query_only=true`，并执行 `PRAGMA query_only=ON`。 |
| 文件不存在 | 直接失败，**不创建空数据库**。 |
| 缺表 | 返回清晰错误，不尝试修复。 |
| 输出 | 报告写 stdout，错误写 stderr。退出码：成功 `0`，参数或数据库错误非零。 |
| JSON 稳定性 | 空集合输出 `[]` 或 `{}`，永不输出 `null`。两次运行（除 `generated_at` 外）字节级一致。 |

报告工具不调用 `database.Init`，不触发 `AutoMigrate`，不加载城市数据，不修改任何业务数据。

---

## 3. text / JSON 字段含义

### text 报告分节

| 节 | 内容 |
|----|------|
| `[1] Data Coverage` | 时间范围、feedback / decision / suggestion 计数、算法版本分布、跳过记录数。 |
| `[2] Human Feedback` | 按事件类型统计，区分 `manual` / `suggestion-v1` / 未知算法版本。 |
| `[3] Suggestion Effect` | 建议驱动的确认/拒绝数、acceptance/rejection rate、identity_profile/legacy 来源命中数、Recall@1/5/10/20、未关联反馈数。 |
| `[4] Identity Decision` | 按 decision/mode/reason 汇总，disagreement / legacy-miss-profile-hit / profile-unavailable / profile-blocked 比例（含分子分母），rescue 应用计数。 |
| `[5] Score & Margin Distribution` | legacy / profile-best / profile-second 分数分位数（P50/P90/P95/P99），margin（P10/P25/P50/P90/P95），耗时（P50/P90/P95/P99/max）。 |
| `[6] Warnings` | 数据不足提示。 |

### JSON 字段

JSON 顶层结构：

```jsonc
{
  "database_path": "...",
  "generated_at": "2026-07-06T03:30:47Z",
  "coverage": { ... },            // 数据覆盖范围
  "feedback": { ... },            // 人工反馈统计
  "suggestion_effect": { ... },   // 合并建议效果
  "identity_decision": { ... },   // identity decision 统计
  "score_distribution": { ... },  // 分数与 margin 分布
  "warnings": [ ... ]             // 数据不足提示
}
```

关键子结构语义：

- `acceptance_rate` / `rejection_rate`：`{numerator, denominator, value}`。`value` 为 `"not_available"` 表示分母为 0。
- `recall_at_1/5/10/20`：`{hits, evaluable, total, value, coverage}`。
  - `hits`：rank ≤ K 的可关联确认事件数。
  - `evaluable`：可关联到建议项的确认事件数（Recall 分母）。
  - `total`：suggestion 驱动的确认事件总数（覆盖率分母，含未关联）。
  - `value`：`hits / evaluable`，分母为 0 时 `not_available`。
  - `coverage`：`evaluable / total`，衡量 Recall 可计算的比例。
- `disagreement_rate` 等：`{numerator, denominator, value}`，分母为 decision 总数。
- 分数分布：`{samples, ignored, percentiles: {P50, P90, P95, P99}}`。`ignored` 计入 nil/NaN/Inf。
- `elapsed_ms`：`{samples, ignored, percentiles, max}`。
- `not_available`：覆盖率不足或样本不可靠时的占位标记。

> 隐私保护：报告仅输出聚合统计，不包含 embedding、人名、文件路径、缩略图路径、原始 similarity snapshot JSON、Face ID / Person ID 明细、component hash、decision key。

---

## 4. 如何判断样本覆盖是否足够

报告的 `[6] Warnings` 节会在以下任一情况出现时给出明确提示。**任一 warning 存在，都说明数据尚不足以支撑 rescue 准入评审**：

| Warning | 含义 |
|---------|------|
| `no shadow decisions` | 未收集 shadow 遥测，无法对比 profile vs legacy。 |
| `no positive feedback (merge_confirmed)` | 无正向反馈，acceptance 无法评估。 |
| `no negative feedback (merge_rejected)` | 无负向反馈，rejection 无法评估。 |
| `Recall coverage insufficient` | 无可关联到建议项的确认合并，Recall 不可计算。 |
| `Recall coverage low` | Recall 覆盖率 < 50%，样本代表性不足。 |
| `identity decision sample size N < 30` | decision 样本不足，分布不具代表性。 |
| `no representative legacy miss events` | 无 legacy_miss_profile_hit/miss 事件，profile 相对 legacy 的增益无法衡量。 |
| `data only from legacy mode` | 仅有 legacy 模式数据，shadow/rescue 对比不可用。 |
| `<N> feedback events could not be linked to suggestions` | 存在无法关联的反馈事件，已计入 unmatched，不用于 Recall。 |

**最低准入门槛（人工评审参考，非自动判定）**：

- shadow decision 样本 ≥ 30（最好 ≥ 100）。
- 同时存在正向与负向反馈。
- Recall 覆盖率 ≥ 50%。
- 存在代表性 legacy miss 事件（legacy_miss_profile_hit + legacy_miss_profile_miss ≥ 10）。
- 数据不全部来自 legacy 模式。

---

## 5. 如何运行 benchmark

Benchmark 位于 `backend/internal/service/person_identity_profile_benchmark_test.go`，覆盖：

| 类别 | 规模 |
|------|------|
| 画像构建 | 10 / 100 / 1,000 / 7,000 faces |
| ANN snapshot | 100 / 1,000 / 7,000 centers |
| ANN + 精确评分 | 20 / 50 / 200 候选 |
| Delta 查询 | 0 / 100 / 500 delta center |

### 运行命令

```bash
cd backend

# 仅运行 Task15 新增的 benchmark（1x 烟雾验证）
go test ./internal/service \
  -run '^$' \
  -bench '^BenchmarkIdentityProfile' \
  -benchtime=1x \
  -benchmem

# 完整 benchmark（多轮，取稳定值）
go test ./internal/service \
  -run '^$' \
  -bench '^BenchmarkIdentityProfile(Build|ANNSnapshot|ANNExactScore|ANNDelta)' \
  -benchtime=2s \
  -benchmem
```

### 输出说明

每个 benchmark 输出 `ns/op`、`B/op`、`allocs/op`，以及自定义 metric（`faces` / `centers` / `candidates` / `delta_centers_*`）。

- `BenchmarkIdentityProfileBuild*`：画像构建耗时与分配。
- `BenchmarkIdentityProfileANNSnapshot*`：HNSW 索引重建耗时。
- `BenchmarkIdentityProfileANNExactScore*`：ANN 召回 + 精确加权评分耗时。
- `BenchmarkIdentityProfileANNDelta*`：增量 delta 中心下 Search 耗时。

> **不设硬耗时断言**。benchmark 不进入普通测试执行时间（`-run '^$'`），不修改全局生产配置，不写入真实数据库。delta 容量上限为 256（`identityProfileANNDeltaMax`），`Delta500` 超过上限时退化为纯 snapshot 搜索并在 metric 中标注 `delta_centers_capped=256`。

### 合成向量说明

Benchmark 使用固定随机种子（splitmix64）生成归一化 synthetic vectors，维度固定为 128（`identityProfileBenchmarkDim`）。生产真实维度由 ML 端点决定，通过 `DecodeEmbedding` 解出的 `len()` 体现；benchmark 维度仅用于可复现的代表规模测试，不代表生产维度约束。合成向量编码后会校验解码一致性，跳过极罕见的被 `DecodeEmbedding` 误判为 legacy JSON 的编码。

---

## 6. 如何记录 NAS CPU、内存、SQLite 写入和 backfill 时间

在生产 NAS 上启用 shadow 前，需记录资源基线。以下命令在 NAS 上执行（通过 SSH）。

### CPU 与内存

```bash
# 容器资源使用（每 5 秒采样，持续 5 分钟）
while true; do
  docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' relive-backend
  sleep 5
done | tee nas-shadow-cpu-mem.log
```

### SQLite 写入量

```bash
# 监控 WAL 文件增长（反映写入压力）
ls -l <nas-root>/data/backend/relive.db-wal
# 或持续采样
while true; do
  stat -c '%s %Y' <nas-root>/data/backend/relive.db-wal
  sleep 10
done | tee nas-shadow-wal-growth.log
```

### Backfill 时间

启用 shadow 后首次构建全部人物画像的 backfill 耗时，通过 people worker 日志或 `/api/v1/people/identity-profile/stats` 接口（`backfill_cursor` / `backfill_completed`）观察：

```bash
# 轮询 backfill 进度
while true; do
  curl -s http://<nas>:8080/api/v1/people/identity-profile/stats | \
    jq '{total, ready, backfill_cursor, backfill_completed}'
  sleep 30
done | tee nas-shadow-backfill.log
```

记录从启用 shadow 到 `backfill_completed=true` 的总耗时，以及期间 NAS CPU 峰值与内存峰值。

---

## 7. copied-DB shadow smoke test 步骤

在将生产切换到 shadow 之前，先在**数据库副本**上验证 shadow 决策逻辑不崩溃、不修改业务数据。

### 前置

- 已取得一致性数据库副本（见第 1 节）。
- 已构建 `relive-identity-profile-report`。

### 步骤

1. **在副本上运行报告，记录基线**：

   ```bash
   ./relive-identity-profile-report -db ~/relive-calibration.db -format json > before.json
   ```

2. **校验只读保护**：

   ```bash
   # 验证报告前后业务表行数不变（测试已覆盖，可手动复核）
   sqlite3 -readonly ~/relive-calibration.db \
     'SELECT (SELECT COUNT(*) FROM people_feedback_events), (SELECT COUNT(*) FROM people_identity_decisions), (SELECT COUNT(*) FROM person_merge_suggestions), (SELECT COUNT(*) FROM faces);'
   ```

3. **在副本上模拟 shadow 决策（只读分析，不落库）**：

   shadow smoke test 的核心是验证：给定生产数据，shadow 决策逻辑能正常运行且不修改 `faces.person_id`。由于报告工具本身只读，shadow 决策的实际运行需在隔离环境（如本地开发库 + 副本数据导入）进行：

   ```bash
   # 将副本数据导入本地隔离开发库
   cp ~/relive-calibration.db /tmp/relive-shadow-smoke.db

   # 启动本地后端，配置 identity_profile_mode: shadow（仅本地，不影响生产）
   # 在 backend/config.dev.yaml 中设：
   # people:
   #   identity_profile_mode: shadow
   cd backend && make run
   ```

   观察日志中 shadow 决策遥测写入 `people_identity_decisions`（mode=shadow），确认：
   - 无 panic 或死锁。
   - `faces.person_id` 不被修改（shadow 只记录决策，不执行合并）。
   - decision 分布合理（agree / disagree / legacy_miss_profile_hit 等）。

4. **再次运行报告，对比 shadow 遥测**：

   ```bash
   ./relive-identity-profile-report -db /tmp/relive-shadow-smoke.db -format json > after.json
   diff <(jq -S . after.json) <(jq -S . before.json)  # 关注 identity_decision 节变化
   ```

5. **清理隔离环境**，确认生产 NAS 仍保持 `legacy`。

---

## 8. 生产切换 shadow 前的检查清单

在 NAS 生产环境将 `people.identity_profile_mode` 从 `legacy` 改为 `shadow` 前，逐项确认：

- [ ] 已取得最新一致性数据库副本，并在副本上完成 smoke test（第 7 节）。
- [ ] 报告工具在副本上运行成功，`[6] Warnings` 中无 `data only from legacy mode` 之外的致命项（首次切 shadow 前无 shadow 数据是正常的）。
- [ ] benchmark 在 NAS 同型号硬件（或更弱）上跑通，画像构建与 ANN 重建耗时在可接受范围。
- [ ] NAS CPU、内存、磁盘余量充足（backfill 期间不触发 OOM 或磁盘告警）。
- [ ] 已知会用户：shadow 期间不改变 `faces.person_id`，仅收集遥测。
- [ ] 已备份当前生产配置（`config.prod.yaml`）与数据库（`make backup-nas`）。
- [ ] 已确认回滚方法（第 10 节），可在 5 分钟内回退到 `legacy`。
- [ ] shadow 期间监控 dashboard 与 people worker 日志，关注 backfill 进度与错误率。

切换命令（在 NAS 上）：

```bash
# 编辑生产配置
# backend/config.prod.yaml:
# people:
#   identity_profile_mode: shadow

# 重启 backend 容器使配置生效
docker compose restart backend
```

---

## 9. rescue 准入检查清单

> **Task15 完成不代表可以启用 rescue。** 生产 rescue 必须经过 shadow 数据收集、人工校准评审、NAS 性能确认和回滚演练。

在将 `identity_profile_mode` 从 `shadow` 改为 `rescue` 前，逐项确认：

- [ ] shadow 已稳定运行足够时间（建议 ≥ 2 周），积累代表性样本。
- [ ] 报告 `[6] Warnings` 全部清零（或剩余项经人工评估可接受）。
- [ ] shadow decision 样本 ≥ 100，且同时存在正向与负向反馈。
- [ ] Recall 覆盖率 ≥ 50%，Recall@1 / @5 数值合理（人工设定阈值，**不由工具自动推荐**）。
- [ ] 存在代表性 legacy miss 事件，`legacy_miss_profile_hit` 比例显示 profile 相对 legacy 有正向增益。
- [ ] `disagreement_rate` 在可接受范围（profile 与 legacy 冲突频率不高）。
- [ ] `profile_unavailable_rate` 与 `profile_blocked_rate` 低（画像索引可用性高）。
- [ ] NAS 性能确认：backfill 完成耗时、shadow 期间 CPU/内存峰值、SQLite 写入压力均在基线范围内。
- [ ] 已完成回滚演练（第 10 节），确认可在 5 分钟内回退。
- [ ] margin 分布（P10/P25/P50）经人工评审，**rescue threshold 由人工根据证据设定，不由分位数自动推导**。
- [ ] 已知会用户：rescue 模式下 profile 决策可影响 `faces.person_id`（自动合并/拒绝）。

> 报告明确**不**输出 `safe_to_enable_rescue=true`。所有阈值校准与准入决策由人工根据报告证据做出。

---

## 10. 回滚到 legacy 的配置与验证方法

若 shadow 或 rescue 期间出现异常，立即回滚到 `legacy`。

### 回滚步骤

1. **修改生产配置**：

   ```yaml
   # backend/config.prod.yaml
   people:
     identity_profile_mode: legacy
   ```

2. **重启 backend 容器**：

   ```bash
   docker compose restart backend
   ```

3. **验证回滚成功**：

   ```bash
   # 确认配置生效
   curl -s http://<nas>:8080/api/v1/system/health | jq '.data.version'
   # 确认 identity decision 不再新增 mode=shadow/rescue 记录
   # （legacy 模式下 service 返回零值结构，不查询数据库写入决策遥测）
   ```

4. **运行报告确认状态**：

   ```bash
   # 取回滚后的副本
   make backup-nas
   cp <backup-root>/.../relive.db ~/relive-rollback-check.db
   ./relive-identity-profile-report -db ~/relive-rollback-check.db -format text
   # 确认 decision_by_mode 中无新的 shadow/rescue 记录
   ```

### 回滚后处理

- `legacy` 模式下，`faces.person_id` 始终由 legacy 聚类器指派，画像中心与 ANN 缓存不参与决策。
- rescue 期间若已自动合并人物，回滚**不会**自动拆分已合并的人物；需人工评估是否拆分（通过管理界面手动操作）。
- 已写入的 `people_identity_decisions` 遥测记录保留，作为后续校准证据。

---

## 参考

- `docs/NAS_BACKUP.md` — NAS 在线备份工具。
- `backend/cmd/relive-identity-profile-report/` — 只读校准报告工具源码。
- `backend/internal/service/person_identity_profile_benchmark_test.go` — 代表规模基准测试。
- `backend/internal/model/people_identity_decision.go` — identity decision 表结构与枚举。
- `backend/internal/model/person_merge_suggestion.go` — 合并建议表与 Rank 字段。
