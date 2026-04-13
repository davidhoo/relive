# People Rescan By Path Design

> **Status:** Completed
> **Note:** The path-level people rescan entry described here has landed on `main`; keep this document for historical traceability.

**Goal:** 在“照片管理 > 扫描路径”中新增“人物重扫”入口，使用户无需先去做照片重建，也能对某个路径重新触发人物扫描/聚类后台任务。

**Scope:** `backend/internal/api/v1/handler/people_handler.go`、`backend/internal/api/v1/router/router.go`、`backend/internal/model/dto.go`、`backend/internal/api/v1/handler/people_handler_test.go`、`frontend/src/api/people.ts`、`frontend/src/views/Photos/index.vue`

## Problem

当前人物后台处理虽然会在扫描/重建后自动启动，但没有单独的路径级手动入口。

结果是：

- 用户想“重新跑一轮人物扫描/聚类”时，需要先去照片页做重建
- 人物管理页是监控视角，不适合承载路径级处理入口

## Decision

把入口只放在“照片管理 > 扫描路径”列表中。

新增一个单一后端接口，职责是：

1. 若人物后台未运行，则先启动人物后台
2. 将指定路径下的照片重新加入人物任务队列
3. 返回入队数量和是否本次新启动了后台

## Why This Approach

- 符合路径级操作的心智模型
- 前端只需调用一个接口，失败状态集中
- 不污染人物管理页，让它继续作为监控面板
- 不复用“重建”，避免语义混乱

## UX Rules

- 按钮位置：扫描路径列表操作列
- 按钮名称：`人物重扫`
- 禁用规则：路径被禁用时禁用
- 后台任务若已在运行，则直接入队，不重复启动
- 若后台任务处于 `stopping`，返回冲突错误，提示稍后重试

## Verification

- 后端 handler 测试：
  - 未运行时会启动后台并按路径入队
- 前端结构检查：
  - `Photos/index.vue` 出现“人物重扫”按钮和点击处理函数
- 构建：
  - `cd frontend && npm run build`
