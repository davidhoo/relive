# People System Design

**Date:** 2026-04-02
> **Status:** Completed
> **Note:** The approved phase-one people system described here has landed on `main`.

**Supersedes:** `docs/plans/face-recognition-vector-db.md`, `docs/plans/2026-04-01-photo-detail-face-list.md` (partial)

## Problem

Relive 当前主分支还没有真正的人物系统：

- 没有 `face / person` 数据模型
- 没有 `people` 相关路由、Handler、Service、Repository
- 没有人物列表、人物详情、人物任务状态页面
- `people_spotlight` 仍主要依赖 VLM 标签和 `Event.PrimaryTag` 猜测“人物专题”

同时，旧方案把几个不同层级的问题揉在了一起：

- 核心产品定义
- 人脸检测与聚类实现
- 向量数据库
- 智能裁切
- 远程 GPU / 云端部署

这会让“人物系统到底是不是一期核心能力”失焦。

## Current Codebase Truth

当前仓库中的真值是：

- `backend/internal/api/v1/router/router.go` 没有 `people` 或 `faces` 路由
- `backend/internal/model/photo.go` 没有人脸/人物相关字段
- `frontend/src/router/index.ts` 没有人物页面路由
- `frontend/src/views/Photos/Detail.vue` 还没有“这张照片中的人物”展示
- `ml-service/` 目录存在，但当前没有可作为正式能力的已提交服务源码
- `backend/internal/service/event_curation_service.go` 的 `people_spotlight` 仍是标签启发式，而不是基于真实人物聚类

因此，本文档描述的是新的产品真值，不是当前已实现能力说明。

## Approved Product Definition

### 1. Product Role

- 人物识别是 Relive 的默认核心能力，不是高级插件，也不是实验性能力。
- 默认目标部署环境是家用 NAS / CPU-only 主机。
- 可以慢，但必须能在后台纯异步运行，不要求用户手动盯着任务跑完。

### 2. Core Objects

- `Face`：单张照片中的一张检测到的人脸，是底层记录。
- `Person`：系统把多张照片中的多张 `Face` 聚成“同一个人”后的对象，是用户真正管理的对象。

产品主对象是 `Person`，不是 `Face`。

规则固定为：

- 只要检测到人脸，就应形成某个人物归属
- 即使只出现一次，也先生成一个人物
- 人物类别固定为 `家人 / 亲友 / 熟人 / 路人`
- 默认类别是 `路人`
- 类别由用户手动指定，不做自动关系猜测
- 以后新照片只要继续归到该人物，就自动继承该类别
- 人物代表头像默认自动挑选，但用户可以手动改

### 3. User-Facing Surfaces

最终产品面对普通用户只保留 3 个主要入口：

- `人物列表`
- `人物详情`
- `后台任务状态`

其中：

- `人物列表` 是主入口
- `人物详情` 是主要管理面
- `后台任务状态` 用于查看进度、状态、失败数、最近日志

不做独立的“全局人脸管理页”作为主功能。

`Face` 只在需要理解或纠错时出现：

- `照片详情`：显示这张照片中的人物和相关人脸信息
- `人物详情`：显示构成该人物的人脸样本，供拆分、移动、改头像

### 4. Required Correction Operations

最低必须支持：

- 合并人物
- 将选中的人脸拆分为新人物
- 将选中的人脸移动到已有其他人物
- 修改人物类别
- 修改人物姓名
- 修改代表头像

并且：

- 人工修正优先级高于自动聚类
- 后续后台增量处理不能推翻用户手动做过的合并、拆分、移动、分类、头像指定

### 5. Background Behavior Rules

后台行为固定为：

- 扫描/重建后的正常照片自动进入人物处理链路
- `excluded` 照片不参与人物系统
- 未检测到人脸的照片记录为“无人脸”并结束
- 自动聚类风格以“尽量平衡，但边界样本略偏保守”为准
- 新照片优先尝试并入已有人物，不确定时新建人物，后续允许人工合并
- 整条链路纯异步运行，用户默认不需要手动点“开始人物聚类”

### 6. Display Strategy Integration

人物系统首先作用在 `照片层`，不是先作用在 `事件层`。

规则固定为：

- 一张照片里如果出现多人，只取其中出现人物的最高类别
- 默认加权关系是：
  - `家人`：高加分
  - `亲友`：中加分
  - `熟人`：少量加分
  - `路人`：不加不减
- 人物优先级只是选图输入之一，不替代时间、事件、地点、画质等现有因素
- 用户把人物类别改掉后，该人物所有历史照片后续都应立即按新类别参与展示

现有 `people_spotlight` 可以保留，但应该逐步从“VLM 标签猜人物”迁移为“真实人物数据驱动”。

## Data And Architecture Direction

为满足“照片层立即生效”与“主分支现有查询结构简单”这两个条件，一期实现建议采用：

- `faces`：存单张人脸记录、bbox、置信度、缩略图、embedding、人工锁定信息
- `people`：存人物对象、类别、名称、代表头像、计数信息
- `people_jobs`：存后台人物处理队列，沿用当前 `thumbnail_jobs / geocode_jobs` 的异步模式
- `photos.face_process_status`：记录该照片的人脸处理状态
- `photos.face_count`：记录检测到的人脸数量
- `photos.top_person_category`：记录该照片当前最高人物类别，作为展示策略的派生缓存

关键原则：

- 权威事实仍是 `faces` 与 `people` 的关系
- `photos.top_person_category` 是派生字段，不是新的事实源
- 当人物类别或人脸归属变化时，必须立即回写受影响照片，确保历史照片马上生效

### Why Not sqlite-vec In V1

旧方案把 `sqlite-vec` 放进了一期核心链路，但当前产品定义不需要这么重：

- 一期目标是人物系统闭环，不是向量平台
- NAS / CPU-only 场景优先，先把稳定性和可维护性做出来
- 人脸 embedding 可以先直接存 SQLite 普通列/BLOB
- 真正需要 ANN 检索、跨模态向量或语义搜索时，再评估 `sqlite-vec`

## Explicit Non-Goals

当前明确不做：

- 不做“自动识别具体姓名”
- 不做用户自定义人物类别
- 不做独立的人脸统一管理页
- 不把 `sqlite-vec`、`CLIP`、远程 GPU、云端部署当作一期硬要求
- 不把“智能裁切增强”绑定为人物系统一期验收条件
- 不允许后台自动全量重聚类覆盖用户已做过的人工修正

## Delta From Older Docs

相对旧文档，变化点已经很明确：

- 旧 `face-recognition-vector-db.md` 是“技术模块打包方案”，新设计是“人物驱动产品方案”
- 旧方案把 `IsFamily` / `HasFamily` 这种单一家庭布尔值放得太核心，新设计要求固定四级类别
- 旧方案把向量数据库、智能裁切、未来 CLIP 一起列为主路径，新设计把它们全部降为后续增强
- 旧 `2026-04-01-photo-detail-face-list.md` 默认假设 `/faces/*` API 已存在，但当前主分支并不存在这些接口，因此它只能作为后续子任务，不能继续单独代表人物需求

## Delivery Phases

- Phase 1：结构化数据与后台检测/聚类链路
- Phase 2：人物列表、人物详情、任务状态、照片详情人物展示
- Phase 3：照片层展示优先级接入与 `people_spotlight` 迁移
- Phase 4：可选增强，例如智能裁切、远程 GPU、语义向量检索
