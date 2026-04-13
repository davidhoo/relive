# Relive 文档索引

> 这份索引优先服务"当前版本怎么用、怎么查、怎么改"，并明确区分历史文档。

## 先看这些

### 当前使用
- `README.md`：项目总览
- `QUICKSTART.md`：当前版本快速启动
- `docs/BACKEND_API.md`：当前已实现 API
- `docs/ANALYZER_API_MODE.md`：当前 analyzer API 模式
- `docs/PROJECT_STATUS.md`：当前项目状态
- `docs/plans/2026-04-02-people-system-design.md`：人物系统的一期产品定义

### 当前开发
- `docs/QUICK_REFERENCE.md`：开发速查卡
- `docs/CONFIGURATION.md`：配置职责与优先级
- `docs/DEVICE_PROTOCOL.md`：设备协议设计
- `docs/GOTCHAS.md`：踩坑经验（GORM/SQLite/Vue/ESP32 常见陷阱）

### 设计方案
- `docs/plans/2026-04-02-people-system-design.md`：当前人物系统一期定义（已落地）
- `docs/plans/2026-04-02-people-system.md`：人物系统实施计划（已完成）
- `docs/plans/event-driven-curation.md`：事件驱动型智能策展方案（分阶段落地，剩余阶段已搁置）

## Plan Status

查询“还有哪些计划中的工作没做”时，默认只把 `Pending` 视为活动 backlog；应跳过 `Completed`、`Review Only`、`Superseded`，并把 `Candidate` 仅视为未来选项而非当前承诺路线。

### Pending
- 当前 `docs/plans/` 下没有已批准但尚未实现的 `Pending` 计划文件

### Partially Completed
- `docs/plans/event-driven-curation.md`：Phase 0 / 1 / 2a / 2b / 2c 已完成，其余阶段当前搁置

### Candidate / Future Option
- `docs/plans/face-recognition-vector-db.md`

### Review Only
- `docs/plans/2026-04-05-runtime-state-and-graceful-restart-review.md`

### Superseded
- `docs/plans/2026-04-01-startup-unification-design.md`
- `docs/plans/2026-04-01-startup-unification.md`
- `docs/plans/2026-04-01-photo-detail-face-list.md`
- `docs/plans/2026-04-03-people-cluster-threshold-design.md`
- `docs/plans/2026-04-03-people-cluster-threshold.md`
- `docs/plans/2026-04-06-people-clustering-optimization.md`

### Completed
- `docs/plans/2026-04-02-factory-reset-design.md`
- `docs/plans/2026-04-02-factory-reset.md`
- `docs/plans/2026-04-02-make-entrypoints-design.md`
- `docs/plans/2026-04-02-make-entrypoints.md`
- `docs/plans/2026-04-02-people-system-design.md`
- `docs/plans/2026-04-02-people-system.md`
- `docs/plans/2026-04-03-backend-page-card-spacing-design.md`
- `docs/plans/2026-04-03-backend-page-card-spacing.md`
- `docs/plans/2026-04-03-immich-lite-people-clustering-design.md`
- `docs/plans/2026-04-03-immich-lite-people-clustering.md`
- `docs/plans/2026-04-03-people-clustering-cross-photo-create-guard-design.md`
- `docs/plans/2026-04-03-people-clustering-cross-photo-create-guard.md`
- `docs/plans/2026-04-03-people-detail-layout-design.md`
- `docs/plans/2026-04-03-people-detail-layout.md`
- `docs/plans/2026-04-03-people-list-cards-design.md`
- `docs/plans/2026-04-03-people-list-cards.md`
- `docs/plans/2026-04-03-people-merge-avatar-candidates-design.md`
- `docs/plans/2026-04-03-people-merge-avatar-candidates.md`
- `docs/plans/2026-04-03-people-rescan-by-path-design.md`
- `docs/plans/2026-04-03-people-rescan-by-path.md`
- `docs/plans/2026-04-05-people-manual-feedback-recluster-design.md`
- `docs/plans/2026-04-05-people-manual-feedback-recluster.md`
- `docs/plans/2026-04-06-people-worker-offline-design.md`
- `docs/plans/2026-04-06-people-worker-offline.md`
- `docs/plans/2026-04-07-people-clustering-drain-and-visibility.md`
- `docs/plans/2026-04-07-people-clustering-optimization-v2.md`
- `docs/plans/2026-04-07-people-face-thumbnail-batch-design.md`
- `docs/plans/2026-04-07-people-face-thumbnail-batch.md`
- `docs/plans/2026-04-13-people-worker-runtime-lease.md`

## 当前实现文档

### 产品与架构
- `docs/REQUIREMENTS.md`
- `docs/IMAGE_PREPROCESSING.md`
- `docs/EXIF_HANDLING.md`
- `docs/GEOCODING.md`
- `docs/DEVICE_PROTOCOL.md`

### API 与运行
- `docs/BACKEND_API.md`
- `docs/ANALYZER_API_MODE.md`
- `QUICKSTART.md`
- `docs/PROJECT_STATUS.md`
- `docs/BACKEND_COMPLETE.md`（能力快照）
- `docs/FRONTEND_COMPLETE.md`（能力快照）

### 人物系统相关
- `docs/plans/2026-04-02-people-system-design.md`
- `docs/plans/2026-04-02-people-system.md`
- `docs/BACKEND_API.md`（`/people`、`/photos/:id/people`、`/faces/:id/thumbnail`）
- `docs/PROJECT_STATUS.md`（人物页、后台任务、展示策略权重）

### 开发与测试
- `docs/TESTING.md`
- `docs/QUICK_REFERENCE.md`

## 历史 / 阶段性文档

以下文档已移入 `docs/archive/`，保留用于理解项目演进，不应覆盖当前实现说明：

### 历史方案 / 评审
- `docs/ANALYZER.md`（历史）：解释早期文件模式 analyzer 与当前 API 模式的差异
- `docs/OFFLINE_WORKFLOW.md`（历史）：查看"导出 → 分析 → 导入"旧方案的完整设计背景
- `docs/OFFLINE_WORKFLOW_REVIEW.md`（历史）：查看旧离线方案当时识别出的技术风险与评审意见
- `docs/API_DESIGN.md`（设计稿）：了解接口命名和资源划分的原始设计意图
- `docs/FRONTEND_PLAN.md`（规划稿）：了解前端页面和模块最初的规划范围
- `docs/plans/face-recognition-vector-db.md`（旧候选方案）：人像识别 + 向量数据库 + 智能裁切混合方案，已被 `2026-04-02-people-system-design.md` 收敛替代
- `docs/plans/2026-04-01-photo-detail-face-list.md`（局部草案）：依赖尚不存在的 `/faces/*` API，已被 `2026-04-02-people-system.md` 吸收

### 归档文档
- `docs/archive/ARCHITECTURE.md`：系统架构（设计阶段）
- `docs/archive/DEVELOPMENT.md`：开发说明（设计阶段）
- `docs/archive/DEPLOYMENT.md`：部署指南（设计阶段）
- `docs/archive/AI_PROVIDERS.md`：AI Provider 架构（设计阶段）
- `docs/archive/DATABASE_SCHEMA.md`：数据库设计（早期设计稿）
- `docs/archive/development/`：历史调试笔记
- `docs/archive/ANALYZER_PHASE1_DONE.md`：Analyzer Phase 1 完成总结
- `docs/archive/ANALYZER_TEST_REPORT.md`：Analyzer 测试报告
- 各类 `*_COMPLETE.md`、`*_REVIEW_*.md`、`DAILY_SUMMARY_*.md`：阶段快照

## 真值优先级

文档与代码冲突时，优先级如下：
1. `VERSION`
2. `backend/internal/api/v1/router/router.go`
3. `backend/cmd/relive-analyzer/main.go`
4. `frontend/src/router/index.ts`
5. `docker-compose.yml`
6. `analyzer.yaml.example`
7. 本索引中"当前使用 / 当前实现文档"分组下的文档
