# Immich-lite People Clustering Design

> **Status:** Completed
> **Note:** The clustering redesign described here has landed on `main`; keep this document for historical traceability.

**Goal:** 将当前“单脸对单脸 + 单阈值”的人物归属逻辑升级为更接近 Immich 的增量聚类流程，让自动聚类成为主流程、人工修正成为辅助流程。

**Scope:** `backend/internal/service/people_service.go` 及相关 model/repository/test 文件；本阶段不引入向量数据库，继续基于现有 SQLite + GORM 实现。

## Current Problem

当前人物系统的核心流程是：

1. 对单张照片做人脸检测，得到若干 `DetectedFace`
2. 每张新脸与所有已归属人脸逐个计算余弦相似度
3. 取最高分，若 `>= peopleClusterThreshold` 就并入对应人物
4. 否则立即新建一个 `Person`

这个模型有几个结构性问题：

- **单坏样本问题**：一张低质量、遮挡、侧脸、光照异常的人脸就会触发错误拆分
- **单阈值折中问题**：阈值调高会漏并，调低会误并，没有缓冲区
- **单步决策问题**：新脸一到就立即新建人物，没有“等待更多证据”的机制
- **单代表问题**：人物实际上被隐式表示为“所有历史人脸”，但匹配仍是“单脸对单脸”，没有稳定的人物原型

这导致系统在大量单样本人物场景下精度明显低于 Immich 一类的批量聚类系统。

## Alternatives Considered

### Option 1: 继续调余弦阈值

优点：
- 改动最小

缺点：
- 只能在误并/漏并之间做硬折中
- 无法解决 `#169/#203/#206/#189` 这类同一人但相似度分散的问题
- 会不断陷入“再降一点阈值试试”的循环

### Option 2: 仅做人物原型匹配

优点：
- 比单脸匹配更稳
- 改动相对可控

缺点：
- 仍然是“来一张判一张”
- 没有密度/核心点机制
- 低证据样本仍会被过早新建人物

### Option 3: Immich-lite 增量聚类

优点：
- 先聚类、后决策，符合自动聚类主流程
- 支持“挂起不确定样本”，避免过早新建人物
- 可以同时利用“人与人原型”以及“待归属样本之间的互相支持”

缺点：
- 需要重构人物后台处理链路
- 需要新增少量聚类状态和 repository 查询能力

**Decision:** 采用 Option 3。

## Target Architecture

### High-level Flow

新的流程分为两段：

1. **Face Extraction**
   - 人物后台从照片中提取人脸、embedding、缩略图
   - 将人脸结果写入 `faces`
   - 新人脸先进入“待聚类”状态，而不是立刻决定人物归属

2. **Incremental Clustering**
   - 对一批待聚类人脸进行相似图构建
   - 同时参考已有 `Person` 的代表脸原型
   - 根据连接阈值、附着阈值、最小核心点数决定：
     - 并入某个已有人物
     - 形成新人物
     - 继续挂起等待更多样本

### Key Design Principle

**不确定样本不立即新建人物。**

这是整个方案和当前逻辑最大的差异，也是更接近 Immich 的核心点。

## Data Model Changes

### `faces`

为 `Face` 增加聚类状态字段，建议：

- `cluster_status`
  - `pending`
  - `assigned`
  - `outlier`
  - `manual`
- `cluster_score`
  - 最近一次归属时的最高匹配分数
- `clustered_at`
  - 最近一次进入聚类流程的时间

理由：
- 当前仅凭 `person_id IS NULL` 无法区分“待聚类”和“已判定孤立样本”
- 需要能对 `outlier` 做后续重试或人工观察

### `people`

不强制新增字段，但要明确一个逻辑概念：

- 每个 `Person` 的**代表脸集合**不再只是一张隐式代表脸
- 实际匹配时使用 `top-k` 高质量人脸原型

如后续需要缓存，可考虑新增 `person_face_prototypes` 表；第一阶段可先运行时动态选择 `top-k`

## Matching and Clustering Strategy

### 1. Person Prototypes

每个已有人物取 `top-k` 张高质量人脸作为原型，建议：

- `k = 3`
- 排序依据：
  - `manual_locked DESC`
  - `quality_score DESC`
  - `confidence DESC`
  - `id ASC`

### 2. Similarity Graph

对于当前批次的 `pending` 人脸：

- 在批次内部，计算人脸之间的余弦相似度
- 若 `similarity >= link_threshold`，建立一条无向边

建议初始值：

- `link_threshold = 0.72`

这不是最终人物归属阈值，只是“这些脸是否彼此足够相近，能形成局部小团”的阈值。

### 3. Core Point / Density Rule

批次内部只有在组件满足最小支持数时，才允许自动形成新人物。

建议初始值：

- `min_cluster_faces = 2`

含义：
- 单张看起来像陌生人的人脸，不自动新建人物
- 至少两张彼此接近的新脸同时出现，才可以创建新人物

### 4. Attach to Existing Person

对于一个待聚类组件，计算它与每个已有 `Person` 原型集合的匹配分数。

推荐策略：

- 组件内每张脸分别对该人物的 `top-k` 原型取最大相似度
- 对组件整体取平均或中位数作为 `attach_score`

建议初始值：

- `attach_threshold = 0.86`

规则：
- `attach_score >= attach_threshold`：组件并入该人物
- 否则继续判断是否可新建人物

### 5. Deferred / Pending

若组件：

- 不能稳定附着到已有 `Person`
- 且内部样本数不足 `min_cluster_faces`

则保持 `pending`

后续触发时机：

- 同一路径人物重扫
- 新照片检测后自动触发增量聚类
- 计划中的周期性人物重聚类任务

## Processing Semantics

### Photo-level Job Semantics

`PeopleJob` 仍然保持照片级。

完成标准调整为：

- 当前照片的人脸检测完成，并已进入聚类流程
- 不要求当前照片里的每张脸都已经成功归属到某个人物

因此：

- `photo.face_process_status = ready` 表示“人脸提取已完成”
- 不再隐含“所有脸都已稳定归属”

### Top Person Category

`photo.top_person_category` 只根据 `assigned/manual` 状态的人脸重新计算。

若当前照片的所有脸都还是 `pending/outlier`：

- `top_person_category` 为空字符串

## Repository Changes

### `FaceRepository`

需要新增查询与批量更新能力：

- `ListPending(limit int)`
- `ListPendingByPhotoIDs(photoIDs []uint)`
- `ListTopByPersonIDs(personIDs []uint, perPerson int)`
- `UpdateClusterFields(ids []uint, fields map[string]interface{})`

### `PersonRepository`

保留现有接口，但要支持：

- 根据 `face_count > 0` 过滤活跃人物
- 在聚类后批量刷新统计和头像

## Manual Operations Compatibility

手动操作仍然优先：

- `merge/split/move/avatar`

规则：

- `manual_locked` 的人脸不参与自动重分配
- 手动合并后，新人物原型应立即重算
- 手动拆分/移动后，相关 `Person` 的 `face_count/photo_count/representative_face_id` 立即同步

## Failure Handling

### ML Detection Failure

保持现有语义：

- 照片标记 `face_process_status = failed`
- `PeopleJob` 失败

### Clustering Failure

检测已成功，但聚类阶段失败时：

- 保留已提取的人脸数据
- 新脸保持 `cluster_status = pending`
- `PeopleJob` 记录失败原因
- 后续可通过路径级人物重扫重新推进

## Why This Is Better Than Threshold Tuning

以你最近分析的样本为例：

- 某些“肉眼明显同一人”的 pair 余弦只有 `0.58 ~ 0.65`
- 某些同组中的离群样本甚至只有 `0.17`

这说明问题不是“阈值还不够低”，而是：

- 单样本 embedding 不稳定
- 坏样本需要依赖更多样本与局部密度来纠偏

Immich-lite 方案可以用“多样本支持 + 延后决策”来避免因为单脸失败而把整个人物拆碎。

## Verification Strategy

### Unit Tests

- `top-k` 原型选择
- 组件构建/连边规则
- 附着到已有 `Person`
- 不足核心点数时保持 `pending`
- 两张相近新脸形成新人物

### Integration Tests

- 两张单独看不够稳的人脸，在第二张到来后自动并入同一人物
- 单个坏样本不会立即新建人物
- 手动锁定人脸不会被自动聚类改写

### Migration Safety

第一阶段不要求立即重聚全库。

上线策略：

1. 新检测的人脸走新聚类链路
2. 旧数据通过“人物重扫”逐步重新进入新链路
3. 验证效果后，再评估是否需要全量重聚类任务
