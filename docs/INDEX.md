# Relive 文档索引

> 这份索引优先服务“当前版本怎么用、怎么查、怎么改”，并明确区分历史文档。

## 先看这些

### 当前使用
- `README.md`：项目总览
- `QUICKSTART.md`：当前版本快速启动
- `docs/QUICKSTART.md`：NAS / 服务器部署入口
- `docs/BACKEND_API.md`：当前已实现 API
- `docs/ANALYZER_API_MODE.md`：当前 analyzer API 模式
- `docs/PROJECT_STATUS.md`：当前项目状态

### 当前开发
- `docs/QUICK_REFERENCE.md`：开发速查卡
- `docs/DEVELOPMENT.md`：开发说明（含阶段性内容，遇冲突请以源码为准）
- `docs/ARCHITECTURE.md`：系统架构
- `docs/DEVICE_PROTOCOL.md`：设备协议设计
- `CHANGELOG.md`：最近变更记录

## 当前实现文档

### 产品与架构
- `docs/ARCHITECTURE.md`
- `docs/REQUIREMENTS.md`
- `docs/DATABASE_SCHEMA.md`
- `docs/AI_PROVIDERS.md`
- `docs/IMAGE_PREPROCESSING.md`
- `docs/EXIF_HANDLING.md`
- `docs/GEOCODING.md`
- `docs/DEVICE_PROTOCOL.md`

### API 与运行
- `docs/BACKEND_API.md`
- `docs/ANALYZER_API_MODE.md`
- `docs/QUICKSTART.md`
- `docs/DEPLOYMENT.md`（混合文档，已在文首标明历史内容）
- `docs/PROJECT_STATUS.md`
- `docs/BACKEND_COMPLETE.md`（能力快照）
- `docs/FRONTEND_COMPLETE.md`（能力快照）

### 开发与测试
- `docs/DEVELOPMENT.md`
- `docs/TESTING.md`
- `docs/QUICK_REFERENCE.md`
- `docs/INTEGRATION_TEST_REPORT.md`（历史测试快照）

## 历史 / 阶段性文档

以下文档保留用于理解项目演进，不应覆盖当前实现说明：

### 历史方案 / 评审
- `docs/ANALYZER.md`（历史）：解释早期文件模式 analyzer 与当前 API 模式的差异
- `docs/OFFLINE_WORKFLOW.md`（历史）：查看“导出 → 分析 → 导入”旧方案的完整设计背景
- `docs/OFFLINE_WORKFLOW_REVIEW.md`（历史）：查看旧离线方案当时识别出的技术风险与评审意见
- `docs/API_DESIGN.md`（设计稿）：了解接口命名和资源划分的原始设计意图
- `docs/FRONTEND_PLAN.md`（规划稿）：了解前端页面和模块最初的规划范围

### 阶段快照 / 完成总结
- `docs/ANALYZER_PHASE1_DONE.md`（阶段快照）：回顾 analyzer API 服务端阶段交付内容
- `docs/ANALYZER_PHASE2_DONE.md`（阶段快照）：回顾 analyzer 客户端从文件模式迁移到 API 模式的过程
- `docs/INTEGRATION_TEST_REPORT.md`（阶段快照）：查看 2026-02-28 当时的集成测试范围与问题记录
- `docs/BACKEND_COMPLETE.md` / `docs/FRONTEND_COMPLETE.md`：查看当前能力快照，但不要把其中历史描述当成源码真值
- 各类 `*_COMPLETE.md`、`*_REVIEW_*.md`、`DAILY_SUMMARY_*.md`：查看某个阶段“当时做了什么、怎么判断完成”的记录

## 真值优先级

文档与代码冲突时，优先级如下：
1. `VERSION`
2. `backend/internal/api/v1/router/router.go`
3. `backend/cmd/relive-analyzer/main.go`
4. `frontend/src/router/index.ts`
5. `docker-compose.yml`
6. `analyzer.yaml.example`
7. 本索引中“当前使用 / 当前实现文档”分组下的文档
