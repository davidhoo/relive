# Backend API 文档

本文档描述当前仓库中已经实现并对外暴露的主要 HTTP API。

> 源码真值：`backend/internal/api/v1/router/router.go`
>
> 说明：当前版本已经移除旧的导出/导入 API；离线批量分析统一使用 `relive-analyzer` 的 API 模式。

---

## 认证模型

| 方式 | 用途 | 说明 |
|------|------|------|
| 无认证 | 健康检查、环境信息、登录 | 公开接口 |
| JWT | Web 管理后台 | `Authorization: Bearer <jwt>` |
| API Key | 设备 / analyzer | `Authorization: Bearer <api_key>` 或 `X-API-Key: <api_key>` |
| 混合认证 | 照片/预览资源 | 支持 JWT、API Key，部分图片接口支持 `?token=` |

## 统一响应格式

成功响应：

```json
{
  "success": true,
  "data": {},
  "message": "Success"
}
```

失败响应：

```json
{
  "success": false,
  "error": {
    "code": "SOME_ERROR",
    "message": "Human readable message"
  }
}
```

---

## 1. 公开接口

### 1.1 认证

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/auth/login` | 登录 |
| POST | `/api/v1/auth/logout` | 登出 |
| POST | `/api/v1/auth/change-Password` | 修改密码（需要 JWT，但跳过首次登录检查） |
| GET | `/api/v1/auth/user` | 当前用户信息（需要 JWT） |

### 1.2 系统

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/system/health` | 健康检查 |
| GET | `/api/v1/system/environment` | 运行环境信息 |

健康检查示例：

```http
GET /api/v1/system/health
```

```json
{
  "success": true,
  "data": {
    "status": "healthy",
    "version": "1.0.0",
    "uptime": 123,
    "timestamp": "2026-03-09T12:00:00+08:00"
  },
  "message": "System is healthy"
}
```

---

## 2. 设备 / analyzer / 展示接口（API Key）

### 2.1 设备展示接口

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/display/photo` | 兼容旧设备的展示接口 |
| POST | `/api/v1/display/record` | 提交展示记录 |
| GET | `/api/v1/device/display` | 当前设备展示内容 |
| HEAD | `/api/v1/device/display.bin` | 检查二进制展示文件 |
| GET | `/api/v1/device/display.bin` | 获取二进制展示文件 |

### 2.2 analyzer API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/analyzer/tasks` | 获取待分析任务 |
| POST | `/api/v1/analyzer/tasks/:task_id/heartbeat` | 续租任务锁 |
| POST | `/api/v1/analyzer/tasks/:task_id/release` | 释放任务 |
| POST | `/api/v1/analyzer/results` | 提交分析结果 |
| GET | `/api/v1/analyzer/stats` | 获取 analyzer 统计 |
| POST | `/api/v1/analyzer/clean-locks` | 清理过期锁 |
| POST | `/api/v1/analyzer/runtime/acquire` | 获取运行时占用 |
| POST | `/api/v1/analyzer/runtime/heartbeat` | 续租运行时占用 |
| POST | `/api/v1/analyzer/runtime/release` | 释放运行时占用 |

获取任务示例：

```http
GET /api/v1/analyzer/tasks?limit=10
Authorization: Bearer <api_key>
```

---

## 3. 资源访问接口（JWT 或 API Key）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/photos/:id/image` | 原图访问 |
| GET | `/api/v1/photos/:id/thumbnail` | 缩略图 |
| GET | `/api/v1/photos/:id/frame-preview` | 相框预览图 |
| GET | `/api/v1/photos/:id/device-preview` | 设备预览图 |
| GET | `/api/v1/display/items/:id/preview` | 每日展示项预览 |
| GET | `/api/v1/display/assets/:id/preview` | 展示资源预览 |
| GET | `/api/v1/display/assets/:id/bin` | 展示资源二进制 |
| GET | `/api/v1/display/assets/:id/header` | 展示资源头信息 |

---

## 4. 管理后台接口（JWT + 首次登录检查）

### 4.1 系统

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/system/stats` | 系统统计 |
| POST | `/api/v1/system/reset` | 系统还原 |

### 4.2 展示

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/display/preview` | 预览选图结果 |
| GET | `/api/v1/display/batch` | 获取每日展示批次 |
| GET | `/api/v1/display/history` | 展示批次历史 |
| POST | `/api/v1/display/batch/generate` | 生成每日展示批次 |
| POST | `/api/v1/display/batch/generate/async` | 异步生成每日展示批次 |
| GET | `/api/v1/display/render-profiles` | 渲染规格列表 |

### 4.3 照片

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/photos/scan/async` | 启动异步扫描任务 |
| POST | `/api/v1/photos/rebuild/async` | 启动异步重建任务 |
| POST | `/api/v1/photos/tasks/:id/stop` | 停止当前任务 |
| GET | `/api/v1/photos/scan/task` | 查询当前扫描/重建任务 |
| POST | `/api/v1/photos/cleanup` | 清理数据库中已失效照片 |
| POST | `/api/v1/photos/validate-path` | 校验扫描路径 |
| POST | `/api/v1/photos/list-directories` | 浏览目录 |
| POST | `/api/v1/photos/count-by-paths` | 统计各路径照片数 |
| POST | `/api/v1/photos/derived-status-by-paths` | 统计分析/缩略图/GPS 派生状态 |
| GET | `/api/v1/photos/stats` | 照片统计 |
| GET | `/api/v1/photos/categories` | 分类列表 |
| GET | `/api/v1/photos/tags` | 标签列表 |
| GET | `/api/v1/photos` | 分页照片列表 |
| GET | `/api/v1/photos/:id` | 照片详情 |

异步扫描示例：

```http
POST /api/v1/photos/scan/async
Content-Type: application/json
Authorization: Bearer <jwt>

{
  "path": "/app/photos"
}
```

```json
{
  "success": true,
  "data": {
    "task_id": "scan-task-uuid"
  },
  "message": "扫描任务已启动"
}
```

照片列表常用查询参数：
- `page`
- `page_size`
- `analyzed`
- `location`
- `search`
- `sort_by`
- `sort_desc`

### 4.4 缩略图

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/thumbnails/background/start` | 启动后台缩略图任务 |
| POST | `/api/v1/thumbnails/background/stop` | 停止后台缩略图任务 |
| GET | `/api/v1/thumbnails/background/logs` | 后台日志 |
| GET | `/api/v1/thumbnails/task` | 当前任务状态 |
| GET | `/api/v1/thumbnails/stats` | 缩略图统计 |
| POST | `/api/v1/thumbnails/enqueue` | 入队指定照片 |
| POST | `/api/v1/thumbnails/enqueue-by-path` | 按路径入队 |

### 4.5 GPS 地理编码

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/geocode/background/start` | 启动后台地理编码 |
| POST | `/api/v1/geocode/background/stop` | 停止后台地理编码 |
| GET | `/api/v1/geocode/background/logs` | 后台日志 |
| GET | `/api/v1/geocode/task` | 当前任务状态 |
| GET | `/api/v1/geocode/stats` | 地理编码统计 |
| POST | `/api/v1/geocode/repair-legacy-status` | 修复旧状态字段 |
| POST | `/api/v1/geocode/enqueue` | 入队指定照片 |
| POST | `/api/v1/geocode/enqueue-by-path` | 按路径入队 |

### 4.6 AI 分析

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/ai/analyze` | 单次分析 |
| POST | `/api/v1/ai/analyze/batch` | 批量分析 |
| POST | `/api/v1/ai/background/start` | 启动后台分析 |
| POST | `/api/v1/ai/background/stop` | 停止后台分析 |
| GET | `/api/v1/ai/background/logs` | 后台日志 |
| GET | `/api/v1/ai/progress` | 分析进度 |
| GET | `/api/v1/ai/task` | 当前任务状态 |
| GET | `/api/v1/ai/runtime` | 运行时状态 |
| POST | `/api/v1/ai/reanalyze/:id` | 重分析单张照片 |
| GET | `/api/v1/ai/provider` | 当前 Provider 信息 |

### 4.7 设备管理

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/devices` | 创建设备 |
| DELETE | `/api/v1/devices/:id` | 删除设备 |
| PUT | `/api/v1/devices/:id/enabled` | 启用/禁用 |
| PUT | `/api/v1/devices/:id/render-profile` | 更新渲染规格 |
| GET | `/api/v1/devices/stats` | 设备统计 |
| GET | `/api/v1/devices` | 设备列表 |
| GET | `/api/v1/devices/:device_id` | 设备详情 |

### 4.8 配置管理

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/config` | 配置列表 |
| POST | `/api/v1/config/batch` | 批量设置配置 |
| GET | `/api/v1/config/:key` | 获取配置 |
| PUT | `/api/v1/config/:key` | 设置配置 |
| DELETE | `/api/v1/config/:key` | 删除配置 |
| DELETE | `/api/v1/config/scan-paths/:id` | 删除扫描路径 |
| GET | `/api/v1/config/prompts` | 获取提示词配置 |
| PUT | `/api/v1/config/prompts` | 更新提示词配置 |
| POST | `/api/v1/config/prompts/reset` | 重置提示词 |
| GET | `/api/v1/config/cities-data/status` | 离线城市数据状态 |
| POST | `/api/v1/config/cities-data/download` | 下载并导入城市数据 |

---

## 5. 与旧文档的差异

当前版本与早期设计/总结文档的主要区别：
- 已移除旧的导出/导入 API
- `relive-analyzer` 默认使用 API 模式，而不是 `export.db` 文件交换
- 扫描和重建接口为异步任务接口，返回 `task_id`
- 前端已包含缩略图、地理编码、设备管理、系统管理等独立页面
