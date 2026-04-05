# Runtime State And Graceful Restart Review Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 保存本轮关于“进程内运行态”和“优雅重启”风险的审计结论，供后续评审时决定是否值得投入优化。

**Architecture:** 当前系统混合使用“数据库任务记录”和“进程内运行态”两种模式。这个计划不要求立刻重构任务系统，而是先把问题、影响、现有恢复能力和三档优化路径整理出来，后续按收益和成本做决策。若未来批准优化，优先考虑低成本的排水式优雅重启，而不是直接把所有运行态改造成持久化任务。

**Tech Stack:** Go、Gin、GORM、SQLite、Docker Compose、systemd/Docker 信号退出模型

---

### Task 1: 复核进程内状态清单

**Files:**
- Review: `backend/internal/service/people_service.go`
- Review: `backend/internal/service/ai_service.go`
- Review: `backend/internal/service/event_clustering_service.go`
- Review: `backend/internal/service/scheduler.go`
- Review: `backend/internal/service/result_queue.go`
- Review: `backend/internal/service/photo_service.go`
- Review: `backend/internal/service/photo_scan_service.go`
- Review: `backend/internal/service/thumbnail_service.go`
- Review: `backend/internal/service/geocode_task_service.go`
- Review: `backend/internal/service/display_service.go`
- Review: `backend/internal/service/display_daily_service.go`

**Step 1: 确认纯进程内状态**

重点确认这些状态是否只存在于内存中，且重启后不会恢复：

- `peopleService.feedbackPending / feedbackRunning`
- `aiService.currentTask / backgroundStopCh`
- `eventClusteringService.activeTask`
- `TaskScheduler.running / stopCh`
- `photoService.lastAutoScanCheck`
- `displayService.batchGenRunning`

**Step 2: 确认半持久化状态**

重点确认哪些模块“运行态在内存、任务记录在数据库”：

- `scan_jobs`
- `thumbnail_jobs`
- `geocode_jobs`
- `people_jobs`
- `daily_display_batches`

**Step 3: 记录审计结论**

本轮已发现的结论如下：

- 人物反馈重聚类是纯进程内调度，重启后尚未执行的反馈会丢
- AI 后台分析与事件聚类任务的当前运行上下文主要在内存里
- 结果写入队列只在优雅停机时落盘，进程崩溃时存在内存窗口
- 扫描、人脸、缩略图、地理编码任务虽然有内存运行态，但底层任务记录已在数据库中

### Task 2: 复核当前关机与恢复能力

**Files:**
- Review: `backend/cmd/relive/main.go`
- Review: `backend/internal/service/service.go`
- Review: `backend/internal/service/photo_service.go`
- Review: `backend/internal/service/result_queue.go`

**Step 1: 检查当前优雅退出路径**

重点确认：

- `SIGINT/SIGTERM` 是否被捕获
- HTTP 是否通过 `http.Server.Shutdown(...)` 关闭
- timeout 是否足够
- 哪些后台服务在关机时被显式通知

**Step 2: 记录当前已存在的恢复能力**

本轮已确认：

- `main.go` 已处理 `SIGINT/SIGTERM`
- `Photo/Thumbnail/GeocodeTask` 已有 `HandleShutdown()`
- `ResultQueue.Stop()` 会在优雅停机时持久化剩余队列
- `scanJobRepo.InterruptNonTerminal(...)` 会在服务启动时处理中断旧扫描任务

**Step 3: 记录当前缺口**

本轮已确认：

- `main.go` 当前没有统一处理 `People` 反馈调度的退出
- `main.go` 没有显式处理 `AI` 后台分析停止
- `main.go` 没有显式处理 `EventClustering` 停止
- HTTP shutdown timeout 当前偏短
- 当前没有 `draining/readiness=false` 机制阻止重启期间继续接新流量

### Task 3: 在评审会上选择优化档位

**Files:**
- Review only

**Step 1: 准备三档选项**

**Option A: 不优化**

- 接受纯进程内运行态在重启时丢失
- 仅保留当前行为
- 适合“重启极少、任务可接受中断”的场景

**Option B: 低成本优雅重启**

- 目标是“尽量排水后再退出”，不是持久化全部运行态
- 做法：
  - 补齐所有后台服务的 shutdown 通知
  - 增加 `draining` 状态
  - 延长 shutdown 超时到 30-90 秒
  - 部署层配置较长 `stop_grace_period` / `TimeoutStopSec`
- 这是推荐路径

**Option C: 全持久化任务化**

- 将 AI 后台分析、事件聚类、人物反馈重聚类等运行态改造成数据库任务
- 重启后支持恢复
- 成本最高，仅在业务规模和稳定性要求明显上升后考虑

**Step 2: 使用下面的决策门槛**

满足任一条件时，优先考虑 Option B：

- 部署环境会频繁重启或自动滚动发布
- 用户已能感知“重启打断后台任务/按钮卡顿”
- 计划扩展到长期后台运行的 NAS/家庭服务器场景

满足任一条件时，再考虑 Option C：

- 后台 AI/聚类任务执行时间长到无法接受中断
- 需要跨重启恢复精确进度
- 需要多实例或更严格的任务一致性

### Task 4: 如果未来批准优化，按以下顺序实施

**Files:**
- Modify later: `backend/cmd/relive/main.go`
- Modify later: `backend/internal/service/people_service.go`
- Modify later: `backend/internal/service/ai_service.go`
- Modify later: `backend/internal/service/event_clustering_service.go`
- Modify later: `backend/internal/api/v1/handler/system.go` or readiness endpoint owner
- Modify later: deployment config (`docker-compose*.yml`, systemd unit if any)

**Step 1: 先做低成本优雅重启**

- 在 `main.go` 增加完整 shutdown 顺序
- 引入全局 `draining` 状态
- readiness 在 draining 时返回不可接流量
- 延长 shutdown timeout

**Step 2: 观察一段时间**

观察指标：

- 重启时是否仍出现明显前台报错
- SQLite 是否仍因后台任务造成长时间阻塞
- 是否仍频繁丢失需要补偿的后台运行态

**Step 3: 再决定是否进入持久化重构**

- 如果 Option B 已足够，停止投入
- 如果仍存在明显业务损失，再为 AI/事件聚类/人物反馈重聚类设计 DB 任务表

### Task 5: 未来复审时的验证命令

**Files:**
- Review only

**Step 1: 服务重启前准备**

Run: `make dev-backend`

**Step 2: 手动制造运行态**

- 启动扫描任务
- 启动缩略图/地理编码/人物后台任务
- 启动 AI 后台分析或事件聚类

**Step 3: 触发重启并观察**

Run: `pkill -TERM relive` or deployment-equivalent graceful restart command

**Step 4: 记录结果**

至少记录：

- HTTP 请求是否被平滑收尾
- 哪些后台任务被中断
- 哪些任务在重启后可恢复
- 哪些纯内存状态直接丢失

### Current Findings Snapshot

本轮已经确认的重点问题：

1. 人物反馈重聚类是纯进程内调度，重启会丢掉待执行反馈。
2. AI 后台分析和事件聚类属于“业务性强但运行态主要在内存”的模块。
3. 结果写入队列依赖优雅停机落盘，崩溃时仍有内存窗口。
4. 扫描/缩略图/地理编码/人脸后台任务虽然有数据库任务表，但运行中的控制面状态仍在内存。
5. 当前系统已有基础优雅退出能力，但 shutdown 编排和 readiness/draining 还不完整。

### Recommendation

当前建议：

- 先不做大规模任务系统重构
- 将此文档作为复审入口
- 若未来要优化，优先选择 **Option B: 低成本优雅重启**
- 只有在重启打断造成明确业务损失时，再进入 **Option C: 全持久化任务化**
