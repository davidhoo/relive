# 前端能力快照

> 更新日期：2026-03-09
> 状态：与当前实现对齐的简化快照
> 源码真值：`frontend/src/router/index.ts`

## 概览

当前前端是基于 Vue 3 + TypeScript + Element Plus 的管理后台，包含：
- 登录与首次修改密码流程
- 仪表盘
- 照片管理与详情
- AI 分析
- 缩略图任务管理
- GPS 地理编码任务管理
- 设备管理
- 展示策略与每日展示批次
- 配置管理
- 系统管理

## 当前主要路由

### 公开 / 辅助页面
- `/login`
- `/change-Password`
- `/photos/:id`

### 主后台页面
- `/dashboard`
- `/photos`
- `/analysis`
- `/thumbnails`
- `/geocode`
- `/devices`
- `/display`
- `/config`
- `/system`

## 当前 API 模块

前端当前直接对接的 API 模块包括：
- `frontend/src/api/auth.ts`
- `frontend/src/api/system.ts`
- `frontend/src/api/photo.ts`
- `frontend/src/api/ai.ts`
- `frontend/src/api/thumbnail.ts`
- `frontend/src/api/geocode.ts`
- `frontend/src/api/device.ts`
- `frontend/src/api/display.ts`
- `frontend/src/api/config.ts`

## 与旧总结文档的差异

如果你在旧文档中看到以下内容，请以当前实现为准：
- “Export 导出/导入页面” → 当前前端没有该页面
- “只有 4 个 API 模块 / 5 个类型文件” → 当前模块与类型已扩展
- “8 个核心页面” → 当前主后台页面为 9 个，另有登录、改密、详情页

## 备注

本文件用于快速了解当前前端能力，不再维护旧版逐页长篇说明。需要精确行为时，请直接查看：
- `frontend/src/router/index.ts`
- `frontend/src/views/`
- `frontend/src/api/`
