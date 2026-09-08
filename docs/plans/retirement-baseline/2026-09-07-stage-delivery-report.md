# 阶段性交付报告：下线人脸质检 / 保留人工排除

> 2026-09-08 更新：用户已授权提交并合并本地 main，本文原有“不要合并”和待授权描述仅代表当时状态，不再作为合并前置条件。三项审查问题已修复，后端全量测试、构建和前端 196 个测试及构建通过。生产数据迁移仍未执行；§3.4 的关联统计与缓存刷新在 2026-09-08 NAS 部署准备中补齐，仍须以真实副本验证和维护窗口验收作为生产迁移依据。

> 日期：2026-09-07
> 工作区：`.worktrees/retire-face-quality`
> 分支：`feature/retire-face-quality-preserve-manual-exclusion`（基于 `c8b6d38`）
> 状态：**代码侧可交付审查；真实库预演/apply 硬阻塞，需 human 输入**

本文是实施进度与证据，**不代表**生产已迁移、已合并 main、已推送。

---

## 1. 总览

| 任务 | 状态 | 说明 |
|---|---|---|
| 1 只读基线与迁移清单 | 部分完成 | 代码/schema/本地旧库核对已做；**步骤 3–5 缺含质检表的真实库** |
| 2 人工排除独立化 | 完成 | source 字段、重检只认 exclusion、质检未初始化可测 |
| 3 停自动质检入口 | 完成（已放行） | NewServices 三件套 nil、无 worker Run、路由 410、filter 透传 |
| 4 前端下线 | 完成（已放行） | 质检页 stub、导航移除、照片详情保留恢复 |
| 5 历史迁移 | 骨架完成 | 合成库 dry-run/apply/orphan/mirror/RestoreAuto 防误转存；**真实库未跑** |

**建议：** 任务 2/3/4 相关改动**可以 commit**；**不要 push、不要合并 main**，直到真实库预演清单审查通过。

---

## 2. 已完成证据（任务 2/3/4）

### 2.1 人工排除独立（任务 2）

- `face_exclusions.source`：`manual` / `auto` / `unknown`；历史迁移默认 `unknown`
- `UpdateFaceExclusion` 始终写 `source=manual`（去掉同理由幂等跳过）
- `ApplyDetectionResult` 只认 `face_exclusions`；不读质检事件、不自动裁决、不写自动质检事件
- 质检审核排除写 exclusion `source=manual`；rescore enforce 写 `source=auto`
- 关键测试：`face_exclusion_independence_test.go`（含 nil 质检 repo、质检页排除→删事件→重检仍保留）

### 2.2 自动质检入口停用（任务 3）

- `NewServices`：`FaceQuality` / `Backfill` / `Rescore` = `nil`
- `main.go`：无 backfill/rescore `Run()`
- `filterDetectionsByIndependentVerification`：透传 no-op；生产路径无调用
- 静态路由保留在 `/:id` 前；handler 一律 **410** + `FACE_QUALITY_RETIRED`
- `FaceQualityMode` 废弃忽略；旧 enforce 不能重新启用
- 硬验收：`TestNewServices_FaceQualityWorkersNotWired`、`TestRouter_FaceQualityRetired_NoWorkerWiringAndStaticGone`（审查者已亲跑 PASS）

### 2.3 前端下线（任务 4）

- `FaceQualityReview.vue` → 已下线提示（无轮询）
- 路由 `hidden: true`；导航入口移除；质检 API/类型删除
- 人物管理两排除选项保留
- **撤销入口**：`Photos/Detail.vue`「已排除样本」→「恢复」（审查者已接受：排除后人脸离开人物）

### 2.4 本地验证命令（worktree `backend/`）

```bash
go test ./internal/service ./internal/api/v1/handler ./internal/api/v1/router
go build ./cmd/relive
go build ./cmd/retire-face-quality
```

前端（曾跑）：`vue-tsc`、`FaceQualityReview` stub vitest。

---

## 3. 任务 5 骨架（合成库）

### 3.1 CLI

```bash
# 默认只读预演
retire-face-quality -db /path/to/copy.db [-out plan.json]

# 写入：必须读取已审查清单；mirror 是计划副本，不是数据库备份
retire-face-quality -db /path/to/copy.db -apply -plan plan.json -mirror before.json
```

- dry-run：`mode=ro` + `PRAGMA query_only=ON`
- apply：fingerprint 复检；幂等标记 `migration.face_quality_retirement_v1`
- **不调 ML、不全库重聚类**

### 3.2 分类规则（保守）

| 类别 | 条件（摘要） | apply 行为 |
|---|---|---|
| keep_manual | exclusion.source=manual | 不改 |
| promote_manual_source | source=unknown + 当前 manual **排除**事件 | source→manual |
| transfer_manual_orphan | 脸 excluded、无 exclusion、当前 manual **排除**事件 | 创建 source=manual exclusion |
| clear_auto_exclusion | source=auto + 当前 auto 排除（或无行但有 auto 排除事件） | 删 exclusion，脸→pending |
| clear_auto_review | review_required + 当前 auto review | 脸→pending（不清有效归属字段以外的误清策略见代码） |
| unknown | 证据不足 / 冲突（含 RestoreAuto 的 manual+restore） | **保留并列出，不计入清理完成** |
| archive_rescore_run | queued/running/paused | → cancelled |

硬约束已落地：

- `unknown` + 仅有 auto 事件 → **不恢复**
- `source=manual` + `review_action=restore` → **unknown**，不转存（防 RestoreAuto）

### 3.3 真实库 apply 前置（必须）

合成测试**不能**证明真实库 manual 事件语义完整。apply 前须在副本上跑分布核查，例如：

```sql
SELECT source, decision, review_action, COUNT(*)
FROM face_quality_events
WHERE is_current = 1
GROUP BY 1, 2, 3
ORDER BY 4 DESC;
```

确认所有 `(source, decision, review_action)` 组合都落在现有分支内；未知组合进 unknown 清单，不得默认可清。

### 3.4 原有缺口及部署准备更新（计划 §七 执行要求 6）

2026-09-08 更新：离线迁移已在事务内重算受影响照片分类及计数、刷新仍可定位的人物统计和头像、将相关身份画像及合并建议标记待更新；已有人工锁定且有效的头像保留。进程内缓存由维护窗口后的服务重启重建。新增回归测试及服务层测试通过，Dockerfile 已交付迁移工具。以下为原始阶段记录。

当前 apply **只重算受影响照片 `face_count`**。尚未做：

- 人物 `face_count` / 头像重选
- 身份画像缓存失效
- 合并建议 dirty

**接口设计（真实库前敲定，本阶段不实现）：**

```go
// FaceQualityRetirementSideEffects 退役 apply 后的受影响范围副作用。
// 由 CLI/迁移层在事务提交成功后调用；禁止触发全库重聚类。
type FaceQualityRetirementSideEffects interface {
    // AfterRetirementApply photoIDs 为本次变更触及的照片；
    // personIDs 为曾关联或需刷新统计的人物（可空）。
    AfterRetirementApply(ctx context.Context, photoIDs, personIDs []uint) error
}

// 建议实现落在 peopleService（复用已有）：
// - photoRepo.RecomputeTopPersonCategory(photoIDs)  // 已含 face_count 规则
// - syncPersonState / 头像重选（按现有 exclusion 路径）
// - invalidateIdentityProfiles(...)
// - markMergeSuggestionsDirty / markProtoCacheDirty
```

接线时机：拿到真实库、预演清单审查通过后，在 `-apply` 成功路径挂上上述 hook；合成阶段可继续用轻量 `face_count` SQL。

---

## 4. 任务 1 阻塞明细

本地已查库均为质检上线前快照或空库，**无** `face_exclusions` / `face_quality_events` / rescore 表业务数据。
见：`docs/plans/retirement-baseline/2026-09-07-task1-readonly-baseline.md`

因此未完成：

- 人脸状态 / 排除理由 / 事件来源 / 任务状态统计
- 分类清单（有效人工 / 纯自动 / 不明）
- 逐条预演 CSV（含无法恢复的旧人物归属）

---

## 5. 需要 human 提供（审查补充后定稿）

### 5.1 立刻需要

1. **含质检表的 SQLite WAL 一致性备份**（须含 `face_exclusions` / `face_quality_events` / rescore 相关表）
   - **正确**：`sqlite3 relive.db ".backup 'relive-consistent.db'"`（服务可继续跑），或**停服**后拷贝 `.db` + `-wal` + `-shm` 三件套。
   - **禁止**：NAS/进程仍在写时只 `cp` 单个活动 `.db`（易得到不一致快照）。
   - 给路径时注明用的是哪种方式。
2. **线上当前运行二进制的 commit / 版本号**，以及与分支 `feature/retire-face-quality-preserve-manual-exclusion` 的对应关系。
   - 回滚不能靠旧 `face_quality_mode=disabled`；必须落到「禁止自动质检启动」的代码保护版本。
   - 若线上是质检上线后的版本，禁止简单回退到质检上线前的旧二进制（会丢掉本任务的下线保护）。
3. （可选）是否授权 worktree **只 commit、不 push、不合 main**——用于锁定代码进度。

### 5.2 授权必须拆开（不能一次全包）

| 层级 | 含义 | 何时授权 |
|---|---|---|
| A. 副本只读 dry-run | 出清单 + manual 事件分布 | 备份到位即可 |
| B. 副本可写 apply | 验证事务/幂等/回滚，**不能用线上只读预演代替** | 审完清单后 |
| C. 线上只读预演 | 对生产库只读跑 plan | B 通过后 |
| D. 生产维护窗口 apply | 真实写入 | 单独获准；本报告 ≠ 授权 |

### 5.3 human 审清单时重点看

- manual 事件 `(source, decision, review_action)` **全分布表是否完整**
- 有无未知 `review_action` 落入误判分支
- 来源不明清单、无法恢复的旧人物归属清单
- 「清理完成」计数是否混入了 unknown（禁止）

### 5.4 备份到位后的执行顺序

1. 副本 dry-run + manual 事件分布核查 → 清单
2. human 审清单（见 5.3）
3. 授权 B：副本可写 apply 验证
4. 部署下线代码并确认 worker 不 Run
5. 授权 C：线上只读预演
6. 授权 D：维护窗口 `-apply -mirror`
7. 抽样验收 → 再考虑 merge / push

---

## 6. 变更文件清单（worktree，相对分支起点）

**后端核心：**
`cmd/relive/main.go`，`internal/service/{service,people_service,face_exclusion,face_quality_*}.go`，
`face_quality_retirement.go` + tests，`face_exclusion_independence_test.go`，
`face_quality_retired_wiring_test.go`，`cmd/retire-face-quality/`，
`api/v1/handler/{handler,people_handler,...}`，`pkg/{config,database}`，`model/face.go`

**前端：**
`FaceQualityReview.vue`（stub），`router`，`MainLayout`，`api/people.ts`，`types/people.ts`，`Photos/Detail.vue`

**文档：**
本计划、`retirement-baseline/`、本报告

---

## 7. 完成标准对照（第十节）

| 标准 | 代码/测试 | 线上生效 |
|---|---|---|
| 质检模块与自动入口下线 | 是 | 未部署 |
| 检测/识别/聚类保持 | 单测覆盖路径 | 未做实际路径验收 |
| 两排除选项与计数语义 | 是 | 未验收 |
| 两入口人工排除保护 | 是（含质检页→exclusion 端到端） | 历史孤儿依赖任务 5 真实库 |
| 历史自动影响按证据处理 | 合成规则就绪 | **未预演** |
| 迁移预演/备份/幂等/回滚证据 | CLI+测试具备能力 | **无真实备份** |
| 未全库重检/重聚类 | 是 | — |
| 生产未执行如实标记 | **是：生产未执行** | — |

---

## 8. Break 建议

合成数据上可做工作已收口。继续改只会过度设计。
**请求 dual-confirm break**，待 human 提供真实库 WAL 备份后再开任务 1 清单与任务 5 真实预演。

## 2026-09-08 NAS 部署与生产迁移记录

- 镜像：`relive:retire-quality-20260908`（`30fcf4267bba`）已切换为 `relive:local`；回滚标签 `relive:local-pre-retire-20260908`。
- 代码下线已验证：`/api/v1/people/face-quality/*` 返回 `410 FACE_QUALITY_RETIRED`；人物列表正常；`face_exclusions.source` 已补齐。
- 生产迁移结果（停机后一致性备份 + 已审查清单 apply）：
  - `promote_manual_source`: 2555（保留排除，source→manual）
  - `unknown`: 35810（**未恢复**，避免误伤人物管理人工排除）
  - `clear_*`: 0
- 备份目录：`/volume1/docker/relive/backup/retire-quality-20260908/`（含 `prod-pre-switch.db`、`prod-pre-migrate.db`、`prod-approved-plan.json`）。
- 后续可选：对 `unknown` 做更细的人工/自动区分后再清理；当前不影响质检模块下线与人工排除保留。
