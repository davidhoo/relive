# People Merge Suggestions Design

**Date:** 2026-04-20
> **Status:** Approved

## Problem

Relive 当前已经具备：

- 基于 `Face -> Person` 的人物聚类
- 人物列表、人物详情、人物后台任务
- 手工合并、拆分、移动、改类别、改头像
- `cannot_link_constraints`，用于记录“这两个人物不能再被自动并到一起”

但当前仍缺一个面向“家人 / 亲友”人物的半自动审核层：

- 系统不会主动为家人/亲友找出可能被误拆成多个 `Person` 的候选
- 用户只能靠人物列表和详情页手工排查
- 现有合并入口是“用户已经知道要合谁”时才高效，不适合“系统先提议，再人工审核”的场景

本需求的目标不是让系统自动合并人物，而是新增一个持续、低资源运行的“人物合并建议”后台能力：

- 只为 `family / friend` 人物生成建议
- 建议对象是已经聚类出的人物，不是单张人脸
- 相似度阈值比初始自动聚类更宽松，只用于建议，不用于自动合并
- 人工可剔除部分候选后，再确认合并
- 人工剔除要被长期记住，后续不再重复建议

## Current Codebase Truth

当前代码中的相关事实：

- `backend/internal/service/people_service.go` 已负责人物检测、聚类、手工合并/拆分/移动，以及人工反馈后异步重聚类
- `backend/internal/repository/cannot_link_repo.go` 已支持人物级 `cannot-link` 约束
- `backend/internal/service/photo_scan_service.go` 会在扫描完成后自动启动人物后台任务
- `frontend/src/views/People/index.vue` 当前包含两个标签页：
  - `人物列表`
  - `后台任务`
- 当前没有：
  - 人物合并建议的数据模型
  - 人物合并建议的后台任务
  - 人物合并建议的 API
  - 人物列表页中的建议审核区块

因此，本设计是在已有 People System 上新增一个独立子系统，而不是重写现有人物聚类链路。

## Approved Product Rules

经确认，本功能必须满足以下产品规则：

- 一条建议固定为：`1 个目标人物 + 1~N 个候选人物`
- `target person` 只能是 `family / friend`
- `candidate person` 可以来自任意类别：`family / friend / acquaintance / stranger`
- 合并确认后，保留目标人物的类别
- 一个候选人物同一时刻只能出现在一条待审核建议里；若同时相似于多个目标，只保留相似度最高的一条
- 用户可以先剔除部分候选，再确认合并其余候选
- 剔除后的“不能合并”结论必须被长期记住
- 建议阈值必须比初始自动聚类阈值更宽松
- 这是独立后台任务，不复用现有人物检测/聚类后台任务
- 该任务默认自动、全天低速运行，不追求实时
- 前端审核入口在人物列表下面新增“人物合并建议”区块；没有待审核建议时完全隐藏

## Decision Summary

采用以下总体方案：

- 新增持久化的“建议 + 候选项”模型，而不是临时现算
- 新增独立的后台服务，常驻低速巡检已聚类人物
- 只在人物层做建议，不在单张人脸层做建议
- 复用现有 `cannot-link` 作为人工剔除后的长期否决事实
- 复用现有人物原型与 embedding 相似度机制来打分，只是使用更宽松的建议阈值
- 前端在人物列表页中新增建议区块，点击卡片后通过弹窗/抽屉完成审核

## Data Model

### 1. New Tables

新增两张表：

- `person_merge_suggestions`
- `person_merge_suggestion_items`

#### person_merge_suggestions

一条记录代表“一条待审核或已处理的建议”。

建议字段：

- `id`
- `created_at`
- `updated_at`
- `reviewed_at`
- `target_person_id`
- `target_category_snapshot`
- `status`
- `candidate_count`
- `top_similarity`

状态建议值：

- `pending`
- `applied`
- `dismissed`
- `obsolete`

#### person_merge_suggestion_items

一条记录代表建议中的一个候选人物。

建议字段：

- `id`
- `created_at`
- `updated_at`
- `suggestion_id`
- `candidate_person_id`
- `similarity_score`
- `rank`
- `status`

状态建议值：

- `pending`
- `excluded`
- `merged`
- `obsolete`

### 2. Existing Cannot-Link Reuse

人工剔除候选时，不单纯修改建议项状态，还要写入：

- `cannot_link_constraints(target_person_id, candidate_person_id)`

后续后台生成建议时，必须永久跳过这对人物。

这使得“剔除候选”成为长期有效的人工事实，而不是一次性 UI 操作。

### 3. Candidate Uniqueness

系统必须保证：

- 同一 `candidate_person_id`
- 在同一时刻
- 最多只能出现在一条 `pending` 建议里

如果多个目标人物都命中该候选，只保留最高相似度目标。

## Background Task Design

### 1. Independent Service

新增独立服务，例如：

- `PersonMergeSuggestionService`

它不复用现有 `backend/internal/service/people_service.go` 的后台 worker，不参与人脸检测或聚类，只做：

- 扫描 `family / friend` 目标人物
- 为其生成或刷新建议
- 管理建议任务状态、日志、统计、游标

### 2. Trigger Model

相关事件不直接启动全量重算，只做“标脏”：

- 人物聚类产生或更新人物
- 人物类别变更
- 手工合并 / 拆分 / 移动
- 建议审核完成后

系统只需知道“建议数据需要继续刷新”，而不需要实时算完。

### 3. Always-On Low-Speed Worker

任务运行模型是：

- 全天低速常驻
- 每轮只处理少量目标人物
- 每轮后休眠
- 系统忙时自动降速
- 系统闲时多推进一点

它不是“等人物聚类完全空闲后再跑”，否则在 pending faces 或 people jobs 长期积压时会被永久饿死。

### 4. Progress Persistence

由于任务可能持续数天，必须持久化：

- `dirty` 标记
- 当前扫描游标，例如 `last_target_person_id`
- 最近活动时间
- 本轮统计
- `paused / running / idle / failed` 等任务状态

服务重启后应能续跑，而不是从头开始。

### 5. Dynamic Throttling

新任务与现有人物后台任务不是硬互斥，而是协作降速：

- 当 `people_jobs` 积压高、`pending_faces_total` 高时
- 建议任务减小批次、拉长 sleep
- 必要时临时跳过几轮，但保留 dirty 标记

目标是“不抢资源”和“不会永远不启动”同时成立。

### 6. Manual Controls

虽然默认自动运行，但仍提供人工控制：

- `暂停`
- `继续`
- `立即重跑`

语义如下：

- `暂停`：停止继续推进，但保留现有建议和游标
- `继续`：从当前游标继续
- `立即重跑`：重置扫描游标，重新巡检一轮，但不清空已审核结论

## Suggestion Generation Rules

### 1. Person-Level Only

建议只针对 `Person`，不是 `Face`：

- 输入：已聚类人物及其原型人脸
- 输出：`target person + candidate persons`

这与“建议针对人物，而不是人脸”的产品要求一致。

### 2. Target Scope

只扫描以下目标人物：

- `family`
- `friend`

以下类别不能作为建议目标：

- `acquaintance`
- `stranger`

但它们都可以作为候选人物。

### 3. Candidate Filters

候选人物必须满足：

- 不等于目标人物
- 不存在 `cannot-link`
- 仍然存在，且不是空人物
- 拥有可用于打分的 embedding 原型
- 当前未被更高分目标人物占用为另一条 `pending` 建议

此外，允许实现层增加最小保守过滤，例如：

- 极弱的单脸低质量候选默认跳过，除非分数特别高

### 4. Similarity Scoring

应复用现有人物聚类中的以下核心逻辑：

- 人物原型选取
- 基于 embedding 的 cosine 相似度打分

实现思路：

- 为 `target person` 选择一组 prototypes
- 为 `candidate person` 选择一组 prototypes
- 计算“candidate prototypes 对 target prototypes 的平均最佳相似度”作为主分数

不新造第二套人物相似度定义，避免和当前聚类逻辑脱节。

### 5. Suggestion Threshold

新增独立配置，例如：

- `people.merge_suggestion_threshold`

规则：

- 该阈值必须低于现有 `attach_threshold`
- 它只用于“形成建议”
- 永远不能据此直接自动合并

按当前默认值推导：

- `attach_threshold` 默认约为 `0.70`
- `merge_suggestion_threshold` 建议默认取 `0.62` 左右

含义：

- `score >= attach_threshold`：属于自动并入更积极的区间
- `merge_suggestion_threshold <= score < attach_threshold`：生成建议，等待人工审核
- `score < merge_suggestion_threshold`：直接跳过

### 6. Suggestion Formation

后台生成时采用两阶段归并：

1. 先为每个候选人物选出“最佳目标人物”
2. 再按目标人物聚合，形成一条建议中的多个候选项

例如：

- 候选 `X / Y / Z` 都最接近 `target A`
- 则生成一条建议：`A <- [X, Y, Z]`

### 7. Human Decisions Override Recalculation

后台刷新建议时，必须服从人工已经确认的事实：

- 被剔除并写入 `cannot-link` 的候选不能再回到该目标下
- 已经完成合并的对象不应以原组合重新出现
- 被删除、被合并或不再满足条件的旧建议应转为 `obsolete`

后台只能补充新的待审核建议，不能推翻人工结论。

## API Design

### 1. Background Task APIs

新增独立接口组，风格与现有人物后台任务保持一致：

- `GET /people/merge-suggestions/task`
- `GET /people/merge-suggestions/stats`
- `GET /people/merge-suggestions/background/logs`
- `POST /people/merge-suggestions/background/pause`
- `POST /people/merge-suggestions/background/resume`
- `POST /people/merge-suggestions/background/rebuild`

任务状态建议值：

- `running`
- `idle`
- `paused`
- `stopping`
- `failed`

### 2. Suggestion Read APIs

人物列表下方区块只需要待审核建议，因此建议提供：

- `GET /people/merge-suggestions`
  - 只返回 `pending` 建议
  - 支持分页
- `GET /people/merge-suggestions/:id`
  - 返回单条建议的完整详情，供审核弹窗/抽屉使用

### 3. Review APIs

建议拆成两个审核动作：

- `POST /people/merge-suggestions/:id/exclude`
  - 请求体：`candidate_person_ids`
  - 效果：
    - item 标记为 `excluded`
    - 写入 `cannot-link(target, candidate)`
    - 若无剩余候选，则 suggestion 变 `dismissed`

- `POST /people/merge-suggestions/:id/apply`
  - 请求体：`candidate_person_ids`
  - 效果：
    - 复用现有 `MergePeople(target, sourceIDs)`
    - 被合并项标记 `merged`
    - 若 suggestion 已无剩余待处理项，则 suggestion 变 `applied`

## Frontend Design

### 1. Placement

在 `frontend/src/views/People/index.vue` 的“人物列表”卡片下面新增：

- `人物合并建议` 区块

显示规则：

- 有待审核建议时显示
- 全部处理完或当前没有建议时完全隐藏

### 2. Suggestion Card

每张建议卡片建议展示：

- 目标人物头像
- 目标人物姓名 / 编号
- 目标人物类别
- 候选人物缩略列表
- 候选数量
- 最高相似度
- `审核` 按钮

### 3. Review Interaction

点击 `审核` 后打开弹窗或抽屉，内容包括：

- 顶部：固定展示目标人物
- 列表：候选人物项
- 每个候选项显示：
  - 头像
  - 姓名 / 编号
  - 当前类别
  - 照片数 / 人脸数
  - 相似度
  - 选中状态

底部操作：

- `剔除所选`
- `确认合并所选`
- `取消`

这与“可以确认合并，可以剔除某几个人物后，确认合并”的需求一致。

### 4. Existing Manual Merge Stays

人物详情页当前已有的手工合并入口保留不动。

新功能只是新增一个“系统先提议，人工集中审核”的入口，底层仍复用已有：

- `MergePeople`
- `cannot-link`
- 人物状态与照片分类回写逻辑

## State Transitions

建议状态：

- `pending -> applied`
- `pending -> dismissed`
- `pending -> obsolete`

候选项状态：

- `pending -> excluded`
- `pending -> merged`
- `pending -> obsolete`

典型流程：

1. 后台生成建议：`suggestion.pending + items.pending`
2. 用户剔除若干项：对应 `items.excluded`
3. 用户确认合并剩余项：对应 `items.merged`
4. 若 suggestion 无剩余 `pending` items：
   - 全部 merged：`suggestion.applied`
   - 全部 excluded：`suggestion.dismissed`
5. 若人物已变化导致旧建议失效：`suggestion.obsolete`

## Non-Goals

本次明确不做：

- 不做自动确认合并
- 不做姓名识别或人物命名
- 不做独立“历史建议中心”页面
- 不回到人脸层做建议审核
- 不引入新的向量数据库或第二套 embedding 存储机制
- 不试图让建议结果实时秒级更新

## Testing Strategy

后续实现至少需要覆盖：

### Backend

- 模型与迁移：
  - 新表创建
  - 索引 / 约束 / 状态枚举
- Repository：
  - 建议与候选项的创建、更新、查询、分页
  - 候选唯一占用规则
- Service：
  - 只为 `family / friend` 生成建议
  - `cannot-link` 生效
  - 同一候选只归属于最高分目标
  - 降速续跑、游标续跑、暂停/继续/重跑
  - 审核时剔除与确认合并的状态变化
- Handler：
  - 任务状态接口
  - 建议列表与详情接口
  - `exclude` / `apply` 接口

### Frontend

- 类型定义与 API 封装
- `People` 页面建议区块渲染
- 审核弹窗状态处理
- 待审核为空时区块隐藏
- 构建验证：`cd frontend && npm run build`

## Acceptance Criteria

该功能完成后，应满足：

- 系统会在后台持续、低速地为 `家人 / 亲友` 生成人物合并建议
- 同一候选人物不会同时出现在多条待审核建议里
- 用户可剔除部分候选后再确认合并
- 剔除会被长期记住，后续不再重复建议
- 全部处理完或没有建议时，人物列表页中不显示“人物合并建议”区块
- 整个功能不会把现有人物聚类后台任务饿死，也不会因为对方忙而自己永远不启动
