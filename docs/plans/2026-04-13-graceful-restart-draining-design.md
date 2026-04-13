# Graceful Restart Draining Design

**Date:** 2026-04-13

> **Status:** Completed
> **Note:** The low-cost graceful-restart path described here has landed on `main`. This design intentionally avoids turning every runtime into a durable DB task.

## Problem

当前后端已经具备基础的优雅退出能力，但还没有“可治理的优雅重启”：

- `backend/cmd/relive/main.go` 会捕获 `SIGINT/SIGTERM` 并调用部分 `HandleShutdown()`，但关机编排不完整。
- `Photo/Thumbnail/Geocode/People` 背景服务有停机通知入口，但 `AI` 后台分析和 `EventClustering` 没有在统一停机路径中显式处理。
- 人物手工纠错后的反馈重聚类是纯进程内调度，当前 `HandleShutdown()` 只停人物后台任务，不处理反馈调度。
- 系统只有 `/api/v1/system/health`，没有 readiness/draining 语义；停机期间仍可能继续接新流量。
- HTTP shutdown timeout 只有 5 秒，对 NAS 上的后台任务和 SQLite 收尾偏短。

这类问题不会立刻破坏数据库，但会造成：

- 重启时后台任务被硬切断
- 前端或调用方在停机窗口继续发请求
- 结果队列与后台状态缺少足够的排水时间
- 人物反馈式重聚类在重启窗口内直接丢失

## Goal

在不重构为“全持久化任务系统”的前提下，补齐低成本优雅重启能力：

- 进程进入停机流程时先切换到 `draining`
- 对外提供 readiness 信号，明确“活着但不再接新活”
- 补齐统一 shutdown 编排，覆盖 AI / EventClustering / People feedback
- 给后台服务更长的收尾时间

## Non-Goals

- 不把 AI 后台分析改造成数据库任务
- 不把事件聚类改造成数据库任务
- 不为人物反馈重聚类新增任务表
- 不改变现有业务接口的核心语义

## Approaches Considered

### Option A: Keep current behavior

优点：
- 0 改动

缺点：
- 仍然没有 readiness/draining
- 重启时后台运行态继续无序丢失

### Option B: Low-cost graceful restart

优点：
- 改动面可控
- 不引入新的任务表和恢复语义
- 能明显改善停机期间的用户体验和后台收尾质量

缺点：
- 仍然不能跨重启恢复 AI / 事件聚类 / 人物反馈的精确进度

### Option C: Full durable task orchestration

优点：
- 理论上最完整

缺点：
- 需要新任务表、恢复语义、幂等和一致性设计
- 远超当前问题需要解决的范围

## Decision

采用 **Option B: Low-cost graceful restart**。

## Design

### 1. Introduce process lifecycle state

新增一个轻量 `lifecycle` 状态对象，至少维护：

- `draining bool`

`main.go` 在收到 `SIGINT/SIGTERM` 后立即切换为 `draining=true`。

### 2. Split liveness from readiness

保留现有 `/api/v1/system/health` 作为 liveness：

- 只要进程活着且数据库可 ping，就返回健康

新增 `/api/v1/system/readiness`：

- 正常运行时返回 `200`
- 进入 `draining` 后返回 `503`
- 数据库不可用时返回 `500`

这样可以明确区分：

- “服务还活着”
- “服务是否还应该继续接流量”

### 3. Orderly shutdown sequence

`main.go` 的停机顺序调整为：

1. `draining=true`
2. 停止 scheduler，避免继续触发新后台工作
3. 显式通知后台服务进入 stop：
   - `Photo.HandleShutdown()`
   - `Thumbnail.HandleShutdown()`
   - `GeocodeTask.HandleShutdown()`
   - `People.HandleShutdown()`
   - `AI.StopBackgroundAnalyze()`（若正在跑）
   - `EventClustering.StopTask()`（若正在跑）
4. 等待这些后台服务进入终态或超时
5. 再执行 `http.Server.Shutdown(...)`
6. 退出进程

### 4. People feedback shutdown

人物服务当前的反馈重聚类调度是一个进程内循环。此次不做持久化，只做“停机时不要再继续调度”：

- `HandleShutdown()` 除了停止人物后台任务，还要让 feedback loop 感知 shutdown
- 若 feedback loop 还未实际开始执行，应直接退出
- 若当前已进入一次 `triggerRecluster()`，允许其跑完当前轮，但不再继续下一轮

### 5. Timeout policy

应用内 shutdown timeout 从当前 5 秒提升到一个更合理的值，推荐：

- 服务排水等待：30 秒
- HTTP shutdown：10 秒

部署层同步建议：

- `docker-compose*.yml` 中为 `relive` 配置更长的 `stop_grace_period`

## Files

主要改动预计集中在：

- `backend/cmd/relive/main.go`
- `backend/internal/api/v1/handler/system.go`
- `backend/internal/api/v1/router/router.go`
- `backend/internal/api/v1/handler/system_test.go`
- `backend/internal/service/people_service.go`
- `backend/internal/service/people_service_test.go`
- 新增一个轻量 lifecycle 状态文件（路径待实现时确定）
- `docker-compose.yml`
- `docker-compose.prod.yml.example`

## Validation

至少验证：

- readiness 在正常态返回 200，在 draining 时返回 503
- `main` 的 shutdown helper 会对 AI / EventClustering / task services 发出 stop 通知
- people feedback loop 在 shutdown 后不会继续新一轮调度
- `git diff --check` 与相关 Go 测试通过
