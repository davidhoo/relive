# Router Bridge For Request Redirects Design

**Date:** 2026-04-13

> **Status:** Completed
> **Note:** The router bridge described here has landed on `main`. This change intentionally fixes only the router dynamic-import warning and does not attempt Element Plus on-demand refactoring.

## Problem

当前前端构建里有一条明确但低价值的警告：

> `src/router/index.ts` is dynamically imported by `src/utils/request.ts` but also statically imported by `src/main.ts`

根因是：

- `frontend/src/main.ts` 静态导入了 router 并 `app.use(router)`
- `frontend/src/utils/request.ts` 为了处理 `401/403` 跳转，又通过 `import('@/router')` 动态导入 router

这带来两个问题：

1. 这条动态导入并不会真正拆出新 chunk，只会制造构建警告
2. HTTP 层直接依赖路由模块，边界不清晰

## Goal

移除 `request.ts` 对 router 模块的直接动态导入，同时保持现有行为不变：

- `401` 继续跳转 `/login`
- `403 FIRST_LOGIN_REQUIRED` 继续跳转 `/change-Password`
- 不引入新的用户可见行为变化

## Non-Goals

- 不解决 `element-plus` 大 chunk 警告
- 不做全局导航系统重构
- 不改动路由守卫逻辑
- 不改动 store / auth API 结构

## Options

### Option A: 在 `request.ts` 里静态导入 router

优点：
- 改动最少

缺点：
- 会让 `request -> router -> store -> authApi -> request` 依赖关系更紧
- 循环依赖更显性，长期更脏

### Option B: 新增轻量 router bridge

做法：

- 新增一个极小的 `navigation` / `routerBridge` 模块
- bridge 只暴露：
  - 注册 router 实例
  - 按路径跳转
- `main.ts` 启动时注册 router
- `request.ts` 通过 bridge 发起跳转，不再 import router

优点：
- 去掉构建警告
- 让 HTTP 层不再直接依赖 router 模块
- 改动面很小

缺点：
- 多一个非常小的中间层

### Option C: 完全把登录跳转职责移到调用方

优点：
- `request.ts` 更纯

缺点：
- 需要改很多 API 调用点
- 不符合这次“只清 warning、不改行为”的目标

## Decision

采用 **Option B: router bridge**。

## Design

### 1. Add a tiny bridge module

新增模块，例如：

- `frontend/src/router/bridge.ts`

职责只有两个：

- `registerRouter(router)`
- `navigateTo(path)`

bridge 内部持有 router 引用，但不反向 import `request.ts` 或 store。

### 2. Register once in app bootstrap

在 `frontend/src/main.ts` 中：

- 保留现有 `import router from './router'`
- 在 `app.use(router)` 前后注册 router 到 bridge

### 3. Update request interceptor

在 `frontend/src/utils/request.ts` 中：

- 删除对 `@/router` 的动态 import
- 改用 bridge 的 `navigateTo('/login')` / `navigateTo('/change-Password')`
- 保留现有的 401 去重跳转逻辑

### 4. Validation

至少验证：

- `request.ts` 不再包含 `import('@/router')`
- bridge 文件存在并被 `main.ts` 注册
- `npm run build` 不再出现 router mixed import warning

## Files

- Create: `frontend/src/router/bridge.ts`
- Modify: `frontend/src/utils/request.ts`
- Modify: `frontend/src/main.ts`
- Create: `frontend/scripts/check-router-bridge.mjs`
