# Relive API 接口设计文档

> RESTful API 接口规范
> 版本：v1.0
> 最后更新：2026-02-28

---

## 一、API 设计原则

### 1.1 基本原则
- ✅ **RESTful 风格**：资源导向，使用标准 HTTP 方法
- ✅ **版本控制**：URL 中包含版本号 `/api/v1/`
- ✅ **统一响应**：标准化的响应格式
- ✅ **错误处理**：清晰的错误码和错误信息
- ✅ **认证安全**：API Key 或 Token 认证
- ✅ **分页支持**：大量数据使用分页
- ✅ **筛选排序**：支持灵活的查询参数

### 1.2 URL 结构

```
https://your-nas-domain.com/api/v1/{resource}
```

**示例**：
```
https://relive.local/api/v1/photos
https://relive.local/api/v1/photos/12345
https://relive.local/api/v1/scan/start
```

### 1.3 HTTP 方法

| 方法 | 用途 | 示例 |
|------|------|------|
| GET | 获取资源 | `GET /api/v1/photos` |
| POST | 创建资源 | `POST /api/v1/scan/start` |
| PUT | 更新资源（完整） | `PUT /api/v1/photos/123` |
| PATCH | 更新资源（部分） | `PATCH /api/v1/photos/123` |
| DELETE | 删除资源 | `DELETE /api/v1/photos/123` |

---

## 二、认证机制

### 2.1 API Key 认证（ESP32 使用）

**请求头**：
```http
X-API-Key: your-api-key-here
```

**配置**：
```yaml
# 在 settings 表中配置
esp32_api_key: "random-generated-key-32-chars"
```

### 2.2 Token 认证（Web 管理界面）

**登录获取 Token**：
```http
POST /api/v1/auth/login
Content-Type: application/json

{
  "username": "admin",
  "password": "password"
}
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
    "expires_at": "2026-03-07T10:00:00Z"
  }
}
```

**使用 Token**：
```http
Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

---

## 三、统一响应格式

### 3.1 成功响应

```json
{
  "code": 0,
  "message": "success",
  "data": {
    // 业务数据
  }
}
```

### 3.2 错误响应

```json
{
  "code": 1001,
  "message": "Photo not found",
  "error": "The requested photo ID does not exist"
}
```

### 3.3 分页响应

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "items": [
      // 数据列表
    ],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 11000,
      "total_pages": 550
    }
  }
}
```

---

## 四、核心 API 接口

### 4.1 照片管理接口

#### 4.1.1 获取照片列表

**请求**：
```http
GET /api/v1/photos?page=1&page_size=20&sort=exif_datetime&order=desc
```

**查询参数**：
| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| page | int | 否 | 页码（默认 1） |
| page_size | int | 否 | 每页数量（默认 20，最大 100） |
| sort | string | 否 | 排序字段（exif_datetime/memory_score/display_score） |
| order | string | 否 | 排序方向（asc/desc，默认 desc） |
| category | string | 否 | 筛选分类 |
| city | string | 否 | 筛选城市 |
| date_from | string | 否 | 日期范围开始（YYYY-MM-DD） |
| date_to | string | 否 | 日期范围结束（YYYY-MM-DD） |
| analyzed | bool | 否 | 是否已分析 |
| min_score | float | 否 | 最低 memory_score |

**响应示例**：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "items": [
      {
        "id": 12345,
        "file_path": "/volume1/photos/2024/IMG_1234.jpg",
        "file_name": "IMG_1234.jpg",
        "file_size": 2048576,
        "width": 4032,
        "height": 3024,
        "exif_datetime": "2024-02-28T14:30:00Z",
        "exif_make": "Apple",
        "exif_model": "iPhone 13 Pro",
        "exif_city": "杭州",
        "caption": "阳光明媚的下午，西湖边游人如织...",
        "side_caption": "西湖春日",
        "category": "风景自然",
        "memory_score": 85.5,
        "beauty_score": 78.2,
        "display_score": 82.3,
        "analyzed": true,
        "analyzed_at": "2026-02-28T10:00:00Z",
        "thumbnail_url": "/api/v1/photos/12345/thumbnail",
        "created_at": "2026-02-28T09:00:00Z",
        "updated_at": "2026-02-28T10:00:00Z"
      }
    ],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 11000,
      "total_pages": 550
    }
  }
}
```

#### 4.1.2 获取照片详情

**请求**：
```http
GET /api/v1/photos/{id}
```

**响应示例**：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": 12345,
    "file_path": "/volume1/photos/2024/IMG_1234.jpg",
    "file_name": "IMG_1234.jpg",
    "file_size": 2048576,
    "file_hash": "d8e8fca2dc0f896fd7cb4cb0031ba249",
    "width": 4032,
    "height": 3024,
    "orientation": 1,
    "exif_datetime": "2024-02-28T14:30:00Z",
    "exif_make": "Apple",
    "exif_model": "iPhone 13 Pro",
    "exif_iso": 100,
    "exif_exposure_time": "1/125",
    "exif_f_number": 1.8,
    "exif_focal_length": 5.7,
    "exif_flash": 0,
    "exif_gps_lat": 30.2489,
    "exif_gps_lon": 120.2052,
    "exif_gps_alt": 10.5,
    "exif_city": "杭州",
    "exif_software": "",
    "exif_json": "{...}",
    "exif_available": true,
    "caption": "阳光明媚的下午，西湖边游人如织，湖面波光粼粼。远处的雷峰塔在蓝天白云的映衬下格外醒目。",
    "side_caption": "西湖春日",
    "category": "风景自然",
    "type": "旅行,城市景观",
    "memory_score": 85.5,
    "beauty_score": 78.2,
    "display_score": 82.3,
    "reason": "旅行风光，构图良好，光线充足，具有较高的回忆价值",
    "analyzed": true,
    "analyzed_at": "2026-02-28T10:00:00Z",
    "analysis_error": "",
    "raw_json": "{...}",
    "file_missing": false,
    "file_missing_at": null,
    "tags": [
      {"id": 1, "name": "旅行", "category": "event"},
      {"id": 5, "name": "宁静", "category": "emotion"}
    ],
    "thumbnail_url": "/api/v1/photos/12345/thumbnail",
    "image_url": "/api/v1/photos/12345/image",
    "created_at": "2026-02-28T09:00:00Z",
    "updated_at": "2026-02-28T10:00:00Z"
  }
}
```

#### 4.1.3 更新照片信息

**请求**：
```http
PATCH /api/v1/photos/{id}
Content-Type: application/json

{
  "category": "人物肖像",
  "memory_score": 90.0,
  "side_caption": "家人团聚"
}
```

**响应**：
```json
{
  "code": 0,
  "message": "Photo updated successfully",
  "data": {
    "id": 12345,
    "updated_at": "2026-02-28T15:00:00Z"
  }
}
```

#### 4.1.4 删除照片记录

**请求**：
```http
DELETE /api/v1/photos/{id}
```

**响应**：
```json
{
  "code": 0,
  "message": "Photo deleted successfully"
}
```

**说明**：仅删除数据库记录，不删除原文件

#### 4.1.5 获取照片缩略图

**请求**：
```http
GET /api/v1/photos/{id}/thumbnail?size=400
```

**查询参数**：
| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| size | int | 否 | 缩略图宽度（默认 400，可选 200/400/800） |

**响应**：
- Content-Type: `image/jpeg`
- 返回 JPEG 图片二进制数据

#### 4.1.6 获取原始照片

**请求**：
```http
GET /api/v1/photos/{id}/image
```

**响应**：
- Content-Type: `image/jpeg` 或 `image/png`
- 返回原始图片二进制数据

---

### 4.2 照片扫描接口

#### 4.2.1 开始扫描

**请求**：
```http
POST /api/v1/scan/start
Content-Type: application/json

{
  "mode": "incremental",
  "directories": ["/volume1/photos"],
  "speed_limit": 1000
}
```

**参数**：
| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| mode | string | 否 | 扫描模式（full/incremental，默认 incremental） |
| directories | array | 否 | 指定扫描目录（空则使用配置） |
| speed_limit | int | 否 | 每日上限（默认使用配置） |

**响应**：
```json
{
  "code": 0,
  "message": "Scan started",
  "data": {
    "job_id": 1,
    "status": "running",
    "started_at": "2026-02-28T10:00:00Z"
  }
}
```

#### 4.2.2 暂停扫描

**请求**：
```http
POST /api/v1/scan/pause
```

**响应**：
```json
{
  "code": 0,
  "message": "Scan paused",
  "data": {
    "job_id": 1,
    "status": "paused"
  }
}
```

#### 4.2.3 恢复扫描

**请求**：
```http
POST /api/v1/scan/resume
```

**响应**：
```json
{
  "code": 0,
  "message": "Scan resumed",
  "data": {
    "job_id": 1,
    "status": "running"
  }
}
```

#### 4.2.4 停止扫描

**请求**：
```http
POST /api/v1/scan/stop
```

**响应**：
```json
{
  "code": 0,
  "message": "Scan stopped",
  "data": {
    "job_id": 1,
    "status": "stopped"
  }
}
```

#### 4.2.5 获取扫描状态

**请求**：
```http
GET /api/v1/scan/status
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "job_id": 1,
    "job_type": "incremental",
    "status": "running",
    "total_files": 11000,
    "processed_files": 5000,
    "failed_files": 10,
    "progress": 45.45,
    "current_file": "/volume1/photos/2024/IMG_5001.jpg",
    "started_at": "2026-02-28T10:00:00Z",
    "estimated_completion": "2026-02-28T16:00:00Z",
    "speed": "120 files/hour"
  }
}
```

#### 4.2.6 获取扫描历史

**请求**：
```http
GET /api/v1/scan/history?page=1&page_size=20
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "items": [
      {
        "id": 1,
        "job_type": "full",
        "status": "completed",
        "total_files": 11000,
        "processed_files": 11000,
        "failed_files": 50,
        "started_at": "2026-02-28T02:00:00Z",
        "completed_at": "2026-02-28T08:00:00Z",
        "duration": "6h 0m 0s"
      }
    ],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 5,
      "total_pages": 1
    }
  }
}
```

---

### 4.3 ESP32 展示接口 ⭐

#### 4.3.1 获取今日照片（简化版）

**请求**：
```http
GET /api/v1/display/today
X-API-Key: your-esp32-api-key
```

**查询参数**：
| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| device_id | string | 否 | 设备标识（用于多设备去重） |
| width | int | 否 | 墨水屏宽度（默认 800） |
| height | int | 否 | 墨水屏高度（默认 480） |

**响应示例**：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "photo_id": 12345,
    "image_url": "/api/v1/display/photo/12345/render?width=800&height=480",
    "side_caption": "西湖春日",
    "date": "2024-02-28",
    "city": "杭州",
    "reason": "on_this_day",
    "years_ago": 2
  }
}
```

**说明**：
- 自动应用"往年今日"算法
- 自动去重（7天内不重复）
- 支持多次请求返回不同照片
- 轻量级响应，适合 ESP32

#### 4.3.2 下载渲染后的照片（ESP32 专用）

**请求**：
```http
GET /api/v1/display/photo/{id}/render?width=800&height=480&format=bin
X-API-Key: your-esp32-api-key
```

**查询参数**：
| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| width | int | 是 | 目标宽度 |
| height | int | 是 | 目标高度 |
| format | string | 否 | 输出格式（bin/jpg，默认 bin） |
| orientation | string | 否 | 方向（landscape/portrait） |

**响应**：
- Content-Type: `application/octet-stream` (format=bin)
- Content-Type: `image/jpeg` (format=jpg)
- 返回已渲染的图片数据（含文案、日期、城市等）

**处理流程**：
```
1. 读取原始照片
2. 根据 orientation 旋转
3. 裁剪/缩放到目标尺寸
4. 叠加文案、日期、城市（使用模板）
5. 转换为墨水屏格式（如需要）
6. 返回二进制数据
```

#### 4.3.3 记录展示历史

**请求**：
```http
POST /api/v1/display/history
X-API-Key: your-esp32-api-key
Content-Type: application/json

{
  "photo_id": 12345,
  "device_id": "esp32-001",
  "display_reason": "on_this_day"
}
```

**响应**：
```json
{
  "code": 0,
  "message": "Display history recorded"
}
```

**说明**：ESP32 成功显示照片后调用此接口记录

#### 4.3.4 设备心跳

**请求**：
```http
POST /api/v1/display/heartbeat
X-API-Key: your-esp32-api-key
Content-Type: application/json

{
  "device_id": "esp32-001",
  "battery_level": 85,
  "wifi_rssi": -45,
  "free_heap": 120000
}
```

**响应**：
```json
{
  "code": 0,
  "message": "Heartbeat received",
  "data": {
    "server_time": "2026-02-28T10:00:00Z",
    "next_update": "2026-03-01T08:00:00Z"
  }
}
```

---

### 4.4 统计分析接口

#### 4.4.1 获取照片统计概览

**请求**：
```http
GET /api/v1/stats/overview
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "total_photos": 11000,
    "analyzed_photos": 10500,
    "analyzing_photos": 100,
    "failed_photos": 50,
    "pending_photos": 350,
    "total_size": 52428800000,
    "avg_memory_score": 72.5,
    "avg_beauty_score": 68.3,
    "cities_count": 45,
    "tags_count": 120,
    "oldest_photo": "2010-01-01T00:00:00Z",
    "latest_photo": "2026-02-28T14:00:00Z"
  }
}
```

#### 4.4.2 按分类统计

**请求**：
```http
GET /api/v1/stats/categories
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": [
    {"category": "人物肖像", "count": 3500, "percentage": 31.8},
    {"category": "风景自然", "count": 2800, "percentage": 25.5},
    {"category": "美食餐饮", "count": 1200, "percentage": 10.9},
    {"category": "旅行记录", "count": 1000, "percentage": 9.1}
  ]
}
```

#### 4.4.3 按城市统计

**请求**：
```http
GET /api/v1/stats/cities?limit=20
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": [
    {"city": "杭州", "count": 5000, "percentage": 45.5},
    {"city": "上海", "count": 1200, "percentage": 10.9},
    {"city": "厦门", "count": 800, "percentage": 7.3}
  ]
}
```

#### 4.4.4 按年份统计

**请求**：
```http
GET /api/v1/stats/years
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": [
    {"year": 2024, "count": 3200, "avg_score": 75.5},
    {"year": 2023, "count": 3000, "avg_score": 73.2},
    {"year": 2022, "count": 2800, "avg_score": 71.8}
  ]
}
```

#### 4.4.5 展示历史统计

**请求**：
```http
GET /api/v1/stats/display-history?days=30
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "total_displays": 90,
    "unique_photos": 85,
    "by_reason": {
      "on_this_day": 65,
      "on_this_week": 15,
      "high_score": 10
    },
    "daily_stats": [
      {"date": "2026-02-28", "count": 3},
      {"date": "2026-02-27", "count": 3}
    ]
  }
}
```

---

### 4.5 标签管理接口

#### 4.5.1 获取标签列表

**请求**：
```http
GET /api/v1/tags?category=event
```

**查询参数**：
| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| category | string | 否 | 筛选分类（event/emotion/season/time） |

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": [
    {
      "id": 1,
      "name": "旅行",
      "category": "event",
      "description": "旅行相关照片",
      "photo_count": 1500,
      "created_at": "2026-02-28T10:00:00Z"
    }
  ]
}
```

#### 4.5.2 创建标签

**请求**：
```http
POST /api/v1/tags
Content-Type: application/json

{
  "name": "春游",
  "category": "event",
  "description": "春季出游活动"
}
```

**响应**：
```json
{
  "code": 0,
  "message": "Tag created successfully",
  "data": {
    "id": 50,
    "name": "春游"
  }
}
```

#### 4.5.3 为照片添加标签

**请求**：
```http
POST /api/v1/photos/{id}/tags
Content-Type: application/json

{
  "tag_ids": [1, 5, 10]
}
```

**响应**：
```json
{
  "code": 0,
  "message": "Tags added successfully"
}
```

#### 4.5.4 删除照片标签

**请求**：
```http
DELETE /api/v1/photos/{id}/tags/{tag_id}
```

**响应**：
```json
{
  "code": 0,
  "message": "Tag removed successfully"
}
```

---

### 4.6 系统配置接口

#### 4.6.1 获取配置列表

**请求**：
```http
GET /api/v1/settings
```

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": [
    {
      "key": "nas_photo_paths",
      "value": "[\"/volume1/photos\", \"/volume2/backup\"]",
      "description": "NAS 照片目录列表",
      "updated_at": "2026-02-28T10:00:00Z"
    },
    {
      "key": "memory_threshold",
      "value": "70.0",
      "description": "最低回忆价值阈值"
    }
  ]
}
```

#### 4.6.2 更新配置

**请求**：
```http
PUT /api/v1/settings/{key}
Content-Type: application/json

{
  "value": "75.0"
}
```

**响应**：
```json
{
  "code": 0,
  "message": "Setting updated successfully",
  "data": {
    "key": "memory_threshold",
    "value": "75.0",
    "updated_at": "2026-02-28T15:00:00Z"
  }
}
```

#### 4.6.3 批量更新配置

**请求**：
```http
POST /api/v1/settings/batch
Content-Type: application/json

{
  "settings": {
    "memory_threshold": "75.0",
    "scan_daily_limit": "2000",
    "display_dedup_days": "14"
  }
}
```

**响应**：
```json
{
  "code": 0,
  "message": "Settings updated successfully",
  "data": {
    "updated_count": 3
  }
}
```

---

### 4.7 搜索接口

#### 4.7.1 全文搜索

**请求**：
```http
GET /api/v1/search?q=西湖&page=1&page_size=20
```

**查询参数**：
| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| q | string | 是 | 搜索关键词 |
| fields | string | 否 | 搜索字段（caption/side_caption/category，逗号分隔） |
| page | int | 否 | 页码 |
| page_size | int | 否 | 每页数量 |

**响应**：
```json
{
  "code": 0,
  "message": "success",
  "data": {
    "items": [
      {
        "id": 12345,
        "file_name": "IMG_1234.jpg",
        "caption": "阳光明媚的下午，西湖边游人如织...",
        "side_caption": "西湖春日",
        "memory_score": 85.5,
        "thumbnail_url": "/api/v1/photos/12345/thumbnail",
        "highlight": "阳光明媚的下午，<em>西湖</em>边游人如织..."
      }
    ],
    "pagination": {
      "page": 1,
      "page_size": 20,
      "total": 45,
      "total_pages": 3
    }
  }
}
```

---

## 五、错误码定义

### 5.1 错误码范围

| 范围 | 说明 |
|------|------|
| 0 | 成功 |
| 1000-1999 | 客户端错误（4xx） |
| 2000-2999 | 服务端错误（5xx） |
| 3000-3999 | 业务逻辑错误 |

### 5.2 常用错误码

| 错误码 | HTTP 状态 | 说明 |
|--------|-----------|------|
| 0 | 200 | 成功 |
| 1000 | 400 | 请求参数错误 |
| 1001 | 404 | 资源不存在 |
| 1002 | 401 | 未授权（缺少或无效的认证信息） |
| 1003 | 403 | 禁止访问 |
| 1004 | 429 | 请求过于频繁 |
| 2000 | 500 | 服务器内部错误 |
| 2001 | 503 | 服务不可用 |
| 3001 | 400 | 照片不存在 |
| 3002 | 400 | 照片文件丢失 |
| 3003 | 409 | 扫描任务已在运行 |
| 3004 | 400 | 今日无合适照片 |
| 3005 | 400 | API 调用限额已用尽 |

### 5.3 错误响应示例

```json
{
  "code": 1001,
  "message": "Photo not found",
  "error": "The requested photo with ID 99999 does not exist in the database"
}
```

---

## 六、分页和排序

### 6.1 分页参数

| 参数 | 类型 | 默认值 | 说明 |
|------|------|--------|------|
| page | int | 1 | 页码（从 1 开始） |
| page_size | int | 20 | 每页数量（最大 100） |

### 6.2 排序参数

| 参数 | 类型 | 说明 |
|------|------|------|
| sort | string | 排序字段（如 exif_datetime） |
| order | string | 排序方向（asc/desc） |

### 6.3 分页响应格式

```json
{
  "pagination": {
    "page": 1,
    "page_size": 20,
    "total": 11000,
    "total_pages": 550,
    "has_next": true,
    "has_prev": false
  }
}
```

---

## 七、速率限制

### 7.1 限流策略

| 接口类型 | 限制 | 说明 |
|---------|------|------|
| ESP32 接口 | 100 次/小时 | 每个设备 |
| Web 管理接口 | 1000 次/小时 | 每个用户 |
| 搜索接口 | 100 次/小时 | 每个用户 |
| 扫描接口 | 10 次/小时 | 全局 |

### 7.2 限流响应头

```http
X-RateLimit-Limit: 1000
X-RateLimit-Remaining: 950
X-RateLimit-Reset: 1709107200
```

### 7.3 超限响应

```json
{
  "code": 1004,
  "message": "Rate limit exceeded",
  "error": "Too many requests. Please try again in 3600 seconds."
}
```

---

## 八、版本控制

### 8.1 API 版本

当前版本：`v1`

URL 格式：`/api/v1/{resource}`

### 8.2 版本兼容性

- ✅ **向后兼容**：新版本保持旧版本接口可用
- ✅ **废弃通知**：提前在响应头中标记废弃接口
- ✅ **迁移期**：废弃接口至少保留 6 个月

### 8.3 版本废弃响应头

```http
X-API-Deprecated: true
X-API-Deprecated-Date: 2026-08-28
X-API-Replacement: /api/v2/photos
```

---

## 九、Webhook（未来扩展）

### 9.1 支持的事件

| 事件 | 说明 |
|------|------|
| scan.started | 扫描开始 |
| scan.completed | 扫描完成 |
| scan.failed | 扫描失败 |
| photo.analyzed | 照片分析完成 |
| display.updated | 墨水屏更新 |

### 9.2 Webhook 配置

```json
{
  "url": "https://your-webhook-endpoint.com/relive",
  "events": ["scan.completed", "photo.analyzed"],
  "secret": "your-webhook-secret"
}
```

### 9.3 Webhook 请求示例

```http
POST https://your-webhook-endpoint.com/relive
Content-Type: application/json
X-Relive-Signature: sha256=...

{
  "event": "scan.completed",
  "timestamp": "2026-02-28T10:00:00Z",
  "data": {
    "job_id": 1,
    "total_files": 11000,
    "processed_files": 11000
  }
}
```

---

## 十、实现建议

### 10.1 推荐的 Golang Web 框架

| 框架 | 特点 | 推荐度 |
|------|------|--------|
| **Gin** | 性能高，文档全，生态好 | ⭐⭐⭐⭐⭐ 推荐 |
| Fiber | 超高性能，类 Express | ⭐⭐⭐⭐ |
| Echo | 简洁高效 | ⭐⭐⭐⭐ |
| Chi | 轻量级，标准库风格 | ⭐⭐⭐ |

### 10.2 推荐使用 Gin

**理由**：
- ✅ 性能优秀（每秒处理数万请求）
- ✅ 文档完善，社区活跃
- ✅ 中间件丰富（认证、限流、日志等）
- ✅ 学习曲线平缓
- ✅ 广泛应用，最佳实践丰富

**代码示例**：
```go
package main

import (
    "github.com/gin-gonic/gin"
)

func main() {
    r := gin.Default()

    // API v1 路由组
    v1 := r.Group("/api/v1")
    {
        // 照片相关
        v1.GET("/photos", GetPhotos)
        v1.GET("/photos/:id", GetPhotoDetail)
        v1.PATCH("/photos/:id", UpdatePhoto)

        // ESP32 展示
        v1.GET("/display/today", GetTodayPhoto)
        v1.GET("/display/photo/:id/render", RenderPhoto)

        // 扫描相关
        v1.POST("/scan/start", StartScan)
        v1.GET("/scan/status", GetScanStatus)
    }

    r.Run(":8080")
}

// GetPhotos 获取照片列表
func GetPhotos(c *gin.Context) {
    page := c.DefaultQuery("page", "1")
    pageSize := c.DefaultQuery("page_size", "20")

    // 查询数据库...

    c.JSON(200, gin.H{
        "code": 0,
        "message": "success",
        "data": gin.H{
            "items": []interface{}{},
            "pagination": gin.H{
                "page": page,
                "page_size": pageSize,
            },
        },
    })
}
```

### 10.3 中间件建议

**推荐中间件**：
```go
import (
    "github.com/gin-gonic/gin"
    "github.com/gin-contrib/cors"
    "github.com/gin-contrib/gzip"
    "github.com/ulule/limiter/v3"
)

func setupMiddleware(r *gin.Engine) {
    // CORS 跨域
    r.Use(cors.Default())

    // Gzip 压缩
    r.Use(gzip.Gzip(gzip.DefaultCompression))

    // 日志
    r.Use(gin.Logger())

    // 恢复 panic
    r.Use(gin.Recovery())

    // API Key 认证
    r.Use(AuthMiddleware())

    // 速率限制
    r.Use(RateLimitMiddleware())
}
```

### 10.4 项目结构建议

```
backend/
├── main.go                 # 入口文件
├── config/
│   └── config.go          # 配置管理
├── api/
│   ├── v1/
│   │   ├── photos.go      # 照片接口
│   │   ├── scan.go        # 扫描接口
│   │   ├── display.go     # ESP32 接口
│   │   └── stats.go       # 统计接口
│   └── middleware/
│       ├── auth.go        # 认证中间件
│       └── ratelimit.go   # 限流中间件
├── service/
│   ├── photo.go           # 照片业务逻辑
│   ├── scan.go            # 扫描业务逻辑
│   └── ai.go              # AI 分析
├── model/
│   ├── photo.go           # 照片模型
│   ├── tag.go             # 标签模型
│   └── display.go         # 展示历史模型
├── database/
│   └── db.go              # 数据库连接
└── utils/
    ├── response.go        # 统一响应
    └── errors.go          # 错误处理
```

---

## 十一、测试接口（开发环境）

### 11.1 健康检查

**请求**：
```http
GET /health
```

**响应**：
```json
{
  "status": "ok",
  "version": "v1.0.0",
  "database": "connected",
  "uptime": "72h15m30s"
}
```

### 11.2 API 文档（Swagger）

**请求**：
```http
GET /api/docs
```

**说明**：返回 Swagger UI 页面，展示所有 API 接口文档

---

## 十二、总结

### 12.1 核心接口概览

| 功能模块 | 接口数量 | 关键接口 |
|---------|---------|---------|
| 照片管理 | 6 个 | 列表、详情、更新、缩略图 |
| 照片扫描 | 6 个 | 开始、暂停、状态、历史 |
| ESP32 展示 | 4 个 | 获取照片、下载渲染图、心跳 ⭐ |
| 统计分析 | 5 个 | 概览、分类、城市、年份 |
| 标签管理 | 4 个 | 列表、创建、添加、删除 |
| 系统配置 | 3 个 | 获取、更新、批量更新 |
| 搜索 | 1 个 | 全文搜索 |

**总计**：29 个接口

### 12.2 设计优势

- ✅ **RESTful 规范**：清晰的资源导向设计
- ✅ **统一响应格式**：便于前端处理
- ✅ **完善的错误处理**：明确的错误码和错误信息
- ✅ **ESP32 优化**：专门的轻量级接口
- ✅ **可扩展性**：版本控制、Webhook 预留
- ✅ **安全性**：API Key + Token 双重认证
- ✅ **性能优化**：分页、限流、缓存支持

### 12.3 待实现功能

- [ ] API 接口实现（Gin 框架）
- [ ] 认证中间件
- [ ] 速率限制中间件
- [ ] 响应缓存
- [ ] API 文档生成（Swagger）
- [ ] 单元测试
- [ ] 集成测试

---

**API 设计完成** ✅
**准备进入后端开发阶段** 🚀
