# 任务 1 只读基线（2026-09-07）

> 状态：代码与本地库结构已核对；**逐条迁移清单因本地无质检业务表而阻塞**。

## 0. 版本与工作区

| 项 | 值 |
|---|---|
| Git HEAD | `c8b6d388735e5c9a08d69e58fb049106c162b18d` |
| 分支（主仓） | `main` @ origin/main |
| 工作分支 / worktree | `feature/retire-face-quality-preserve-manual-exclusion` @ `.worktrees/retire-face-quality` |
| VERSION | `1.8.0` |
| 主仓未跟踪 | `docs/plans/2026-09-07-retire-face-quality-preserve-manual-exclusion.md`（已复制进 worktree） |
| 部署版本 | 本地无法从现有文件确认线上镜像 tag；需运维/部署侧另行核对 |

## 1. 本地 SQLite 实表核对（PRAGMA）

已检查路径：

- `backend/data/relive.db`（31M，2025-08-11）
- `data/backend/relive.db`（31M，2025-07-09）
- `data/relive.db`（0B 空文件）
- `test-data/relive.db`（164K，无 faces 质检相关表）

**结论：上述库均无 `face_exclusions` / `face_quality_events` / `face_quality_rescore_*` 表。**

`backend/data/relive.db` 的 `faces` 仅有到 `retry_count`，**无** `exclusion_reason` / `excluded_at` / `face_validity_score` 等列。

当前该库 faces 粗统计：

| cluster_status | count |
|---|---|
| assigned | 284 |
| pending | 132 |
| 合计 | 416 |

→ 这是质检功能上线前的旧快照，**不能**作为迁移预演数据源。

## 2. 代码模型中的权威表结构（实施应对齐）

### `face_exclusions`（`model.FaceExclusion`）

| 字段 | 说明 |
|---|---|
| id, created_at, updated_at | 主键/时间 |
| photo_id | 索引 |
| source_face_id | 排除时的 face id |
| reason | `non_face` / `low_quality` |
| bbox_x/y/width/height | 重检 IoU 匹配用 |

**当前无来源字段**（manual/auto/unknown）——任务 2 需新增，历史默认 `unknown`。

### `faces`（质检相关列，由 AutoMigrate / migrateFaceExclusionColumns 补齐）

- `cluster_status` 含 `excluded` / `review_required`
- `exclusion_reason`, `excluded_at`
- `face_validity_score`, `quality_reasons`, `quality_rule_version`, `quality_model_version`（基础质量评分保留，不因下线删除）

### `face_quality_events`（追加审计）

关键字段：`decision`, `reason`, `source`(auto/manual), `review_action`, `is_current`, `evidence_origin`, `evidence_state`, `evidence_pipeline`, `rescore_run_id`, bbox 列, `exclusion_id`, `face_id`

### 重评分表

- `face_quality_rescore_runs` / `face_quality_rescore_items`（见 `model/face_quality_rescore.go`）

## 3. 代码路径风险复核（对照计划第二节）

| # | 计划陈述 | 本地代码复核 |
|---|---|---|
| 1 | `UpdateFaceExclusion` 直接改 exclusion+faces，不写质检事件；同理由幂等跳过 | **确认**（`face_exclusion.go` ~181-184 continue） |
| 2 | 质检人工审核写 `source=manual` 并改同一套排除 | **确认**（`face_quality_service.go` upsertExclusionTx） |
| 3 | manual 事件 ≠ 全部人工排除；RestoreAuto 也可写 manual | **确认**（RestoreAuto 注释与实现） |
| 4 | `face_exclusions` 无来源字段 | **确认** |
| 5 | 有 auto 事件 ≠ 排除由自动造成 | **确认**（人物管理也可写 exclusion 且不留事件） |
| 6 | disabled 后实时链路仍可能强制 review_required | **确认**（`people_service.go` v2 严格准入强制 review_required） |
| 7 | 自动隔离清空归属；半清理会残留 | **确认** |

## 4. 迁移分类规则（算法草案，待真实库跑）

对每个「当前有效」业务对象（优先：`faces.cluster_status IN ('excluded','review_required')` 或存在 `face_exclusions` 行）：

1. 取同 photo+bbox（IoU≥0.3）下 `is_current=1` 的最新质检事件（若有）。
2. **有效人工排除（保留）** 当且仅当满足任一：
   - 最新有效操作为人物管理排除，且之后无人工恢复；或
   - 最新 current 事件为 `source=manual` 且 `decision IN (non_face,low_quality)`，且 `review_action` 为确认/标记排除类，且 `restored_at` 空；或
   - 能用时间线证明「人工在自动之后再次确认」——若无法证明 → **unknown**
3. **可靠纯自动影响（可撤销）** 当且仅当：
   - 存在 exclusion 或 `excluded`/`review_required` 状态；且
   - 有 current auto 排除/待审事件；且
   - **不存在**任何可证明的人工排除路径（人物管理 exclusion 无法区分时不得归入此类）；且
   - 无后续 manual restore/accept 最终意图冲突
4. **来源不明 / 冲突（保留并列出）**：其余全部。包括「仅有 exclusion、无事件」「有 auto 事件但 exclusion 可能来自人物管理」「manual 事件实为 RestoreAuto」等。
5. **人工恢复/接受最终意图**：不得重新施加旧排除；不清正常人物归属。
6. **无法恢复的旧人物归属**：自动隔离曾清空的 `person_id`，无快照则单独清单，禁止猜测回填。

**硬约束执行提醒：** 清理完成计数 **不得** 含 unknown。

## 5. 逐条预演清单

**未生成。** 原因：本地无含质检表的数据库副本。

需要人类提供其一：

1. 线上/准生产一致性备份（WAL 安全备份后的 `.db`），或
2. 可只读访问的维护库路径

拿到后在维护库上执行统计 SQL（示例）：

```sql
PRAGMA table_info(face_exclusions);
PRAGMA table_info(face_quality_events);
SELECT cluster_status, exclusion_reason, COUNT(*) FROM faces GROUP BY 1,2;
SELECT reason, COUNT(*) FROM face_exclusions GROUP BY 1;
SELECT source, decision, review_action, is_current, COUNT(*) FROM face_quality_events GROUP BY 1,2,3,4;
SELECT status, COUNT(*) FROM face_quality_rescore_runs GROUP BY 1;
```

并输出逐条 CSV：对象 ID、分类、依据、当前状态、拟动作、photo_id、旧 person_id。

## 6. 本轮建议（给审查者）

1. **阻塞项**：无真实库则任务 1 步骤 3–5 与任务 5 预演不可验收。
2. **可并行**：任务 2–4 可基于单元测试与合成库推进（先加 source 字段、去质检依赖、停入口、下线前端）。
3. **反对**在无清单情况下对任何真实库执行 apply。
4. **反对**把「仅有 auto 回填事件」当成可恢复依据。

## 7. 变更文件（本轮）

- 新建 worktree / 分支：`.worktrees/retire-face-quality` → `feature/retire-face-quality-preserve-manual-exclusion`
- 复制计划文档进 worktree
- 新增本基线文件：`docs/plans/retirement-baseline/2026-09-07-task1-readonly-baseline.md`

未改业务代码。
