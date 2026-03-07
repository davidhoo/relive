# Backend API 文档

本文档记录 Relive 后端已实现的 RESTful API 接口。

> **实现状态**：✅ 已实现 | ⏳ 开发中 | 📋 计划中

---

## 目录

- [1. 系统管理 API](#1-系统管理-api)
- [2. 照片管理 API](#2-照片管理-api)
  - [2.1 扫描照片](#21-扫描照片-)
  - [2.2 重建照片](#22-重建照片-)
  - [2.3 清理照片](#23-清理照片-)
  - [2.4 获取照片列表](#24-获取照片列表-)
  - [2.5 获取照片详情](#25-获取照片详情-)
  - [2.6 照片统计](#26-照片统计-)
- [3. 展示策略 API](#3-展示策略-api)
- [4. 设备管理 API](#4-设备管理-api)
- [5. AI 分析 API](#5-ai-分析-api)
- [6. 配置管理 API](#6-配置管理-api)
- [统一响应格式](#统一响应格式)
- [错误码说明](#错误码说明)

---

## 1. 系统管理 API

### 1.1 健康检查 ✅

检查系统运行状态。

**请求**
```http
GET /api/v1/system/health
```

**响应**
```json
{
  "success": true,
  "data": {
    "status": "healthy",
    "version": "0.2.0",
    "uptime": 3600,
    "time": "2026-02-28T18:00:00+08:00"
  },
  "message": "System is healthy"
}
```

**字段说明**
- `status`: 系统状态（`healthy` / `unhealthy`）
- `version`: 版本号
- `uptime`: 运行时间（秒）
- `time`: 当前服务器时间

---

### 1.2 系统统计 ✅

获取系统各模块统计信息。

**请求**
```http
GET /api/v1/system/stats
```

**响应**
```json
{
  "success": true,
  "data": {
    "total_photos": 11234,
    "analyzed_photos": 8456,
    "unanalyzed_photos": 2778,
    "total_devices": 3,
    "online_devices": 2,
    "total_displays": 456
  },
  "message": "Stats retrieved successfully"
}
```

**字段说明**
- `total_photos`: 照片总数
- `analyzed_photos`: 已分析照片数
- `unanalyzed_photos`: 未分析照片数
- `total_devices`: 设备总数
- `online_devices`: 在线设备数（5分钟内有心跳）
- `total_displays`: 展示记录总数

---

## 2. 照片管理 API

### 2.1 扫描照片 ✅

扫描指定目录的照片，提取 EXIF 信息，计算文件哈希。

**请求**
```http
POST /api/v1/photos/scan
Content-Type: application/json

{
  "path": "/volume1/Photos"
}
```

**响应**
```json
{
  "success": true,
  "data": {
    "scanned_count": 150,
    "new_count": 25,
    "updated_count": 3
  },
  "message": "Success"
}
```

**字段说明**
- `scanned_count`: 扫描到的照片总数
- `new_count`: 新增照片数量
- `updated_count`: 更新照片数量（文件哈希变化）

**特性**
- 递归扫描子目录
- 自动排除配置的目录（如 `.sync`、`@eaDir`）
- 支持格式：`.jpg`、`.jpeg`、`.png`
- EXIF 提取：拍摄时间、相机型号、GPS、尺寸、方向
- 增量更新：根据文件哈希判断是否需要更新

---

### 2.2 重建照片 ✅

重新扫描指定目录的照片，强制更新所有信息（EXIF、哈希、地理编码），保留 AI 分析结果，删除数据库中已不存在的文件记录。

**请求**
```http
POST /api/v1/photos/rebuild
Content-Type: application/json

{
  "path": "/volume1/Photos"
}
```

**请求参数**
- `path`: 要重建的目录路径（可选，默认使用配置中的扫描路径）

**响应**
```json
{
  "success": true,
  "data": {
    "scanned_count": 948,
    "new_count": 0,
    "updated_count": 948,
    "deleted_count": 0
  },
  "message": "Success"
}
```

**字段说明**
- `scanned_count`: 扫描到的照片总数
- `new_count`: 新增照片数量
- `updated_count`: 更新照片数量（强制更新所有字段）
- `deleted_count`: 删除的照片数量（文件已不存在的记录）

**与扫描的区别**
- 扫描：仅新增照片，跳过已存在照片
- 重建：强制更新所有照片，删除失效记录，保留 AI 分析结果

---

### 2.3 清理照片 ✅

遍历整个数据库，检查每个照片文件是否还存在，删除文件不存在的记录。

**请求**
```http
POST /api/v1/photos/cleanup
```

**响应**
```json
{
  "success": true,
  "data": {
    "total_count": 1454,
    "deleted_count": 145,
    "skipped_count": 0
  },
  "message": "Success"
}
```

**字段说明**
- `total_count`: 检查的照片总数
- `deleted_count`: 删除的记录数（文件不存在的照片）
- `skipped_count`: 跳过的记录数（无法访问的文件）

**使用场景**
- 照片文件被手动删除后，清理数据库中的无效记录
- 定期维护，保持数据库与文件系统同步

---

### 2.4 获取照片列表 ✅

分页查询照片列表，支持过滤和排序。

**请求**
```http
GET /api/v1/photos?page=1&page_size=20&analyzed=true&location=上海&sort_by=taken_at&sort_desc=true
```

**查询参数**
- `page`: 页码（默认：1）
- `page_size`: 每页数量（默认：20，最大：100）
- `analyzed`: 是否已分析（可选，`true` / `false`）
- `location`: 位置过滤（可选，模糊匹配）
- `sort_by`: 排序字段（默认：`taken_at`，可选：`overall_score`）
- `sort_desc`: 降序排序（默认：`true`）

**响应**
```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": 1,
        "file_path": "/volume1/Photos/2024/IMG_0001.jpg",
        "file_name": "IMG_0001.jpg",
        "file_size": 5242880,
        "taken_at": "2024-01-15T10:30:00Z",
        "camera_model": "iPhone 15 Pro",
        "width": 4032,
        "height": 3024,
        "location": "上海市",
        "ai_analyzed": true,
        "memory_score": 85,
        "beauty_score": 90,
        "overall_score": 87,
        "created_at": "2026-02-28T10:00:00Z"
      }
    ],
    "total": 11234,
    "page": 1,
    "page_size": 20
  },
  "message": "Success"
}
```

---

### 2.3 获取照片详情 ✅

根据 ID 获取照片的详细信息。

**请求**
```http
GET /api/v1/photos/{id}
```

**响应**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "file_path": "/volume1/Photos/2024/IMG_0001.jpg",
    "file_name": "IMG_0001.jpg",
    "file_size": 5242880,
    "file_hash": "a1b2c3d4e5f6...",
    "taken_at": "2024-01-15T10:30:00Z",
    "camera_model": "iPhone 15 Pro",
    "width": 4032,
    "height": 3024,
    "orientation": 1,
    "gps_latitude": 31.2304,
    "gps_longitude": 121.4737,
    "location": "上海市",
    "ai_analyzed": true,
    "ai_description": "一张在外滩拍摄的城市夜景照片，画面中可以看到东方明珠电视塔。",
    "ai_caption": "外滩夜景 - 东方明珠",
    "memory_score": 85,
    "beauty_score": 90,
    "overall_score": 87,
    "main_category": "cityscape",
    "tags": "夜景,城市,地标,上海",
    "analyzed_at": "2026-02-28T12:00:00Z",
    "created_at": "2026-02-28T10:00:00Z",
    "updated_at": "2026-02-28T12:00:00Z"
  },
  "message": "Success"
}
```

---

### 2.5 照片统计 ✅

获取照片的统计信息。

**请求**
```http
GET /api/v1/photos/stats
```

**响应**
```json
{
  "success": true,
  "data": {
    "total": 11234,
    "analyzed": 8456,
    "unanalyzed": 2778
  },
  "message": "Success"
}
```

---

## 3. 展示策略 API

### 3.1 获取展示照片 ✅

ESP32 设备获取要展示的照片（统一的“往年今日”展示策略）。

**请求**
```http
GET /api/v1/display/photo?device_id=ESP32-001
```

**查询参数**
- `device_id`: 设备 ID（必填）

**响应**
```json
{
  "success": true,
  "data": {
    "photo_id": 1234,
    "file_path": "/volume1/Photos/2023/IMG_5678.jpg",
    "width": 4032,
    "height": 3024,
    "taken_at": "2023-02-28T14:30:00Z",
    "location": "杭州市",
    "memory_score": 92,
    "beauty_score": 88,
    "overall_score": 90
  },
  "message": "Success"
}
```

**算法说明**
1. 优先查询往年今日附近的照片，按 `±3 → ±7 → ±30 → ±365` 逐级放宽
2. 所有阶段统一应用回忆分/美学分阈值，并过滤最近 7 天已展示的照片
3. 若严格往年今日未命中，则回溯最近 365 天内最接近目标日期的历史月日
4. 若仍无结果，则从满足阈值的已分析照片中按综合分兜底；必要时再放宽到全部已分析照片
5. 严格往年今日阶段按综合分优先；日期兜底阶段会在高分候选中保留少量随机性

---

### 3.2 记录展示 ✅

手动记录照片在设备上的展示。

**请求**
```http
POST /api/v1/display/record
Content-Type: application/json

{
  "device_id": "ESP32-001",
  "photo_id": 1234
}
```

**响应**
```json
{
  "success": true,
  "message": "Success"
}
```

**说明**
- 展示时间自动记录为当前时间
- 触发类型自动设置为 `manual`（手动）

---

## 4. 设备管理 API

### 4.1 设备接入模型 ✅

当前采用 **后台预创建设备 + 预分配 API Key** 的简单接入模型：

- 管理员通过后台创建一条设备记录
- 系统生成 `device_id` 和 `api_key`
- 将 `api_key` 写入离线分析程序或嵌入式设备配置
- 客户端直接访问业务接口，不再经过注册、激活、心跳流程
- 服务端通过认证中间件自动更新最近活跃时间和来源 IP

---

### 4.2 创建设备 ✅

管理员在后台创建设备，系统自动生成 `device_id` 和 `api_key`。

**请求**
```http
POST /api/v1/devices
Authorization: Bearer <admin-jwt>
Content-Type: application/json

{
  "name": "客厅相框",
  "device_type": "embedded",
  "description": "客厅 7.3 寸墨水屏相框",
  "render_profile": "waveshare_7in3e"
}
```

**请求字段**
- `name`: 设备名称（必填）
- `device_type`: 设备类型（可选，默认 `embedded`）
- `description`: 设备描述（可选）
- `render_profile`: 嵌入式设备渲染规格（可选）

**响应**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "created_at": "2026-02-28T18:00:00+08:00",
    "device_id": "ABCD1234",
    "name": "客厅相框",
    "api_key": "sk-relive-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "device_type": "embedded",
    "render_profile": "waveshare_7in3e"
  },
  "message": "Success"
}
```

**说明**
- `api_key` 仅在创建设备时返回一次
- 客户端后续直接使用该 `api_key` 访问业务接口
- 不再提供设备注册、激活、心跳接口

---

### 4.3 获取设备列表 ✅

分页获取设备列表。

**请求**
```http
GET /api/v1/devices?page=1&page_size=20
```

**查询参数**
- `page`: 页码（默认：1）
- `page_size`: 每页数量（默认：20，最大：100）

**响应**
```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": 1,
        "device_id": "ESP32-001",
        "name": "客厅相框",
        "device_type": "embedded",
        "description": "客厅7.3寸墨水屏相框",
        "ip_address": "192.168.1.100",
        "is_enabled": true,
        "online": true,
        "last_heartbeat": "2026-02-28T17:59:00+08:00",
        "battery_level": 85,
        "wifi_rssi": -45,
        "created_at": "2026-02-28T10:00:00Z"
      }
    ],
    "total": 3,
    "page": 1,
    "page_size": 20
  },
  "message": "Success"
}
```

---

### 4.4 获取设备详情 ✅

根据设备 ID 获取设备详细信息。

**请求**
```http
GET /api/v1/devices/{device_id}
```

**响应**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "device_id": "ESP32-001",
    "name": "客厅相框",
    "device_type": "embedded",
    "description": "客厅7.3寸墨水屏相框",
    "ip_address": "192.168.1.100",
    "is_enabled": true,
    "online": true,
    "last_heartbeat": "2026-02-28T17:59:00+08:00",
    "battery_level": 85,
    "wifi_rssi": -45,
    "created_at": "2026-02-28T10:00:00Z",
    "updated_at": "2026-02-28T17:59:00Z"
  },
  "message": "Success"
}
```

---

### 4.5 更新设备 ✅

更新设备信息。

**请求**
```http
PUT /api/v1/devices/{device_id}
Content-Type: application/json

{
  "name": "卧室相框",
  "description": "卧室新安装的相框",
  "is_enabled": true
}
```

**请求字段**
- `name`: 设备名称（可选）
- `description`: 设备描述（可选）
- `is_enabled`: 是否启用（可选）

**响应**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "device_id": "ESP32-001",
    "name": "卧室相框",
    "device_type": "embedded",
    "description": "卧室新安装的相框",
    "is_enabled": true,
    "updated_at": "2026-02-28T18:00:00Z"
  },
  "message": "Success"
}
```

---

### 4.6 删除设备 ✅

删除设备。

**请求**
```http
DELETE /api/v1/devices/{device_id}
```

**响应**
```json
{
  "success": true,
  "message": "Success"
}
```

---

### 4.7 设备统计 ✅

获取设备的统计信息。

**请求**
```http
GET /api/v1/devices/stats
```

**响应**
```json
{
  "success": true,
  "data": {
    "total": 3,
    "online": 2,
    "by_type": {
      "embedded": 2,
      "mobile": 1
    }
  },
  "message": "Success"
}
```

**字段说明**
- `total`: 设备总数
- `online`: 在线设备数（5分钟内有心跳）
- `by_type`: 按设备类型统计

---

## 5. AI 分析 API

### 5.1 分析照片 ✅

对单张照片进行 AI 分析。

**请求**
```http
POST /api/v1/ai/analyze
Content-Type: application/json

{
  "photo_id": 1234
}
```

**响应**
```json
{
  "success": true,
  "message": "Photo analyzed successfully"
}
```

**说明**
- 使用配置文件中指定的 AI Provider
- 分析完成后更新照片记录
- 如果照片已分析，会跳过

---

### 5.2 批量分析 ✅

批量分析未分析的照片。

**请求**
```http
POST /api/v1/ai/analyze/batch
Content-Type: application/json

{
  "limit": 100
}
```

**响应**
```json
{
  "success": true,
  "data": {
    "total_count": 100,
    "success_count": 98,
    "failed_count": 2,
    "total_cost": 0.392,
    "duration": 234.5
  },
  "message": "Batch analysis completed"
}
```

**字段说明**
- `total_count`: 总处理数量
- `success_count`: 成功数量
- `failed_count`: 失败数量
- `total_cost`: 总成本（人民币）
- `duration`: 耗时（秒）

---

### 5.3 获取分析进度 ✅

获取 AI 分析的实时进度。

**请求**
```http
GET /api/v1/ai/progress
```

**响应**
```json
{
  "success": true,
  "data": {
    "total": 10000,
    "analyzed": 7500,
    "unanalyzed": 2500,
    "progress": 75.0,
    "estimated_cost": 10.0,
    "provider": "ollama"
  },
  "message": "Progress retrieved successfully"
}
```

**字段说明**
- `total`: 照片总数
- `analyzed`: 已分析数量
- `unanalyzed`: 未分析数量
- `progress`: 进度百分比
- `estimated_cost`: 预估剩余成本（人民币）
- `provider`: 当前使用的 Provider

---

### 5.4 重新分析 ✅

重新分析已分析的照片（覆盖原有结果）。

**请求**
```http
POST /api/v1/ai/reanalyze/:id
```

**响应**
```json
{
  "success": true,
  "message": "Photo re-analyzed successfully"
}
```

**说明**
- 即使照片已分析也会重新分析
- 新结果会覆盖原有分析结果

---

### 5.5 获取 Provider 信息 ✅

获取当前使用的 AI Provider 信息。

**请求**
```http
GET /api/v1/ai/provider
```

**响应**
```json
{
  "success": true,
  "data": {
    "name": "ollama",
    "cost_per_photo": 0.0,
    "available": true,
    "max_concurrency": 1
  },
  "message": "Provider info retrieved successfully"
}
```

---

## 6. 配置管理 API

> **注意**：原导出/导入 API 已移除，请使用 relive-analyzer 工具进行离线分析。

### 7.1 获取配置 ✅

根据键获取配置值。

**请求**
```http
GET /api/v1/config/{key}
```

**示例**
```http
GET /api/v1/config/display.strategy
```

**响应**
```json
{
  "success": true,
  "data": {
    "id": 1,
    "key": "display.strategy",
    "value": "{\"algorithm\":\"on_this_day\",\"dailyCount\":3,\"minBeautyScore\":70,\"minMemoryScore\":60}",
    "description": "",
    "created_at": "2026-02-28T10:00:00Z",
    "updated_at": "2026-02-28T10:00:00Z"
  },
  "message": "Config retrieved successfully"
}
```

---

### 7.2 设置配置 ✅

设置或更新配置值。

**请求**
```http
PUT /api/v1/config/{key}
Content-Type: application/json

{
  "value": "{\"algorithm\":\"random\",\"dailyCount\":3,\"minBeautyScore\":70,\"minMemoryScore\":60}"
}
```

**示例**
```http
PUT /api/v1/config/display.strategy
Content-Type: application/json

{
  "value": "{\"algorithm\":\"random\",\"dailyCount\":3,\"minBeautyScore\":70,\"minMemoryScore\":60}"
}
```

**响应**
```json
{
  "success": true,
  "message": "Config updated successfully"
}
```

**常用配置键**
- `display.strategy`: 展示策略配置（JSON，包含 `algorithm` / `dailyCount` / `minBeautyScore` / `minMemoryScore`）
- `display.refresh_interval`: 刷新间隔（秒）
- `display.avoid_repeat_days`: 避免重复展示天数
- `ai.provider`: AI Provider（ollama / qwen / openai / vllm）
- `ai.temperature`: AI 温度参数
- `system.maintenance_mode`: 维护模式（true / false）

---

### 7.3 删除配置 ✅

删除配置项，系统将使用默认值。

**请求**
```http
DELETE /api/v1/config/{key}
```

**示例**
```http
DELETE /api/v1/config/display.strategy
```

**响应**
```json
{
  "success": true,
  "message": "Config deleted successfully"
}
```

**说明**
- 删除后系统将使用代码中定义的默认值
- 适用于重置配置到默认状态

---

### 7.4 获取所有配置 ✅

获取系统中的所有配置项。

**请求**
```http
GET /api/v1/config
```

**响应**
```json
{
  "success": true,
  "data": [
    {
      "id": 1,
      "key": "display.strategy",
      "value": "{\"algorithm\":\"on_this_day\",\"dailyCount\":3,\"minBeautyScore\":70,\"minMemoryScore\":60}",
      "description": "",
      "created_at": "2026-02-28T10:00:00Z",
      "updated_at": "2026-02-28T10:00:00Z"
    },
    {
      "id": 2,
      "key": "display.refresh_interval",
      "value": "3600",
      "description": "",
      "created_at": "2026-02-28T10:00:00Z",
      "updated_at": "2026-02-28T10:00:00Z"
    }
  ],
  "message": "Configs retrieved successfully"
}
```

---

### 7.5 批量设置配置 ✅

批量设置多个配置项。

**请求**
```http
POST /api/v1/config/batch
Content-Type: application/json

{
  "display.strategy": "{\"algorithm\":\"on_this_day\",\"dailyCount\":3,\"minBeautyScore\":70,\"minMemoryScore\":60}",
  "display.refresh_interval": "3600",
  "ai.provider": "ollama"
}
```

**响应**
```json
{
  "success": true,
  "message": "Configs updated successfully"
}
```

**说明**
- 使用事务确保所有配置要么全部成功要么全部失败
- 适用于批量导入配置或恢复配置

---

## 统一响应格式

所有 API 接口使用统一的响应格式。

### 成功响应

```json
{
  "success": true,
  "data": { ... },
  "message": "Success"
}
```

**字段说明**
- `success`: 请求是否成功（`true`）
- `data`: 响应数据（根据接口不同而不同）
- `message`: 提示消息

### 错误响应

```json
{
  "success": false,
  "error": {
    "code": "INVALID_REQUEST",
    "message": "device_id is required"
  }
}
```

**字段说明**
- `success`: 请求是否成功（`false`）
- `error.code`: 错误码（见下方错误码说明）
- `error.message`: 错误详细描述

---

## 错误码说明

| 错误码 | HTTP 状态码 | 说明 |
|--------|-------------|------|
| `INVALID_REQUEST` | 400 | 请求参数无效 |
| `NOT_FOUND` | 404 | 资源不存在 |
| `CREATE_FAILED` | 500 | 创建资源失败 |
| `UPDATE_FAILED` | 500 | 更新资源失败 |
| `QUERY_FAILED` | 500 | 查询资源失败 |
| `SCAN_FAILED` | 500 | 扫描失败 |
| `DATABASE_ERROR` | 500 | 数据库错误 |

---

## API 测试示例

### 使用 curl 测试

```bash
# 健康检查
curl http://localhost:8080/api/v1/system/health

# 系统统计
curl http://localhost:8080/api/v1/system/stats

# 照片统计
curl http://localhost:8080/api/v1/photos/stats

# 创建设备
curl -X POST http://localhost:8080/api/v1/devices \
  -H "Authorization: Bearer <admin-jwt>" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "客厅相框",
    "device_type": "embedded"
  }'

# 获取展示照片
curl "http://localhost:8080/api/v1/display/photo?device_id=ESP32-001"
```

---

## 更新日志

- **2026-03-05**: 重大更新
  - 🗑️ 移除导出/导入 API（功能迁移至 relive-analyzer）
  - 🔄 ESP32 设备 API 重构为通用设备管理 API
    - 简化设备接入（改为预分配 API Key，移除注册/激活/心跳流程）
    - 新增 device_type 字段（embedded/mobile/web/offline/service）
    - 新增 description、is_enabled 字段
    - 路径从 `/api/v1/esp32/*` 改为 `/api/v1/devices/*`
    - 新增更新、删除设备接口
  - 📊 设备统计新增按类型统计

- **2026-02-28**: 完成 Handler 层实现（15个接口）
  - ✅ 系统管理 API（2个）
  - ✅ 照片管理 API（4个）
  - ✅ 展示策略 API（2个）
  - ✅ ESP32 设备 API（5个）
  - ✅ 统一响应格式
  - ✅ 错误码规范

---

**文档版本**: v0.3.0
**最后更新**: 2026-03-05
**API 基准地址**: `http://localhost:8080/api/v1`
