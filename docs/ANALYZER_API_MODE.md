# relive-analyzer API 模式需求文档

> 版本：v2.0
> 更新日期：2026-03-03
> 状态：需求设计阶段

## 1. 概述

### 1.1 背景
原离线分析模式使用 SQLite 数据库文件进行数据交换，需要导出/导入数据库。新版本改为 **API 模式**，分析器通过 HTTP API 与主服务通信，实时获取任务和回写结果。

### 1.2 核心变化

| 特性 | 旧模式（文件） | 新模式（API） |
|------|--------------|--------------|
| 数据获取 | 读取本地 SQLite 文件 | 调用 API 获取待分析照片列表 |
| 照片读取 | 本地文件系统 | 通过 API 获取照片下载链接或本地路径 |
| 结果回写 | 更新本地 SQLite | 调用 API 提交分析结果 |
| 断点续传 | 基于 ai_analyzed 字段 | 基于服务端任务状态 + 本地缓存 |
| 部署方式 | 复制数据库文件 | 仅需配置 API Key 和端点 |

### 1.3 适用场景

1. **本地 GPU 分析**：NAS/服务器上存有照片，使用本地高性能 GPU 机器进行分析
2. **局域网部署**：分析器运行在局域网内的专用 AI 工作站上
3. **多机协作**：多台分析器同时工作，通过 API 协调任务分配

---

## 2. 架构设计

### 2.1 系统架构

```
┌─────────────────┐     HTTP API      ┌─────────────────┐
│   Relive 服务    │ ◄──────────────► │  relive-analyzer │
│   (NAS/服务器)   │                   │   (AI 工作站)    │
└─────────────────┘                   └─────────────────┘
         │                                      │
         │  1. 获取待分析任务                      │  3. 调用本地 AI
         │  2. 获取照片数据                        │  4. 回写分析结果
         │                                      │
         ▼                                      ▼
┌─────────────────┐                   ┌─────────────────┐
│   照片存储        │                   │   Ollama/vLLM   │
│  (本地/网络存储)  │                   │   (本地 GPU)    │
└─────────────────┘                   └─────────────────┘
```

### 2.2 工作流程

```
1. 启动分析器
   └── 验证 API Key 和连接

2. 循环获取任务
   └── GET /api/v1/analyzer/tasks?limit=10
   └── 返回待分析照片列表（含下载 URL 或本地路径）

3. 并发分析
   └── Worker Pool 并行处理
   └── 每张照片：下载 → 预处理 → AI 分析 → 回写结果

4. 提交结果
   └── POST /api/v1/analyzer/results
   └── 批量提交分析结果

5. 断点续传（异常中断后）
   └── 本地缓存记录已处理 photo_id
   └── 重新启动时跳过已处理
```

---

## 3. 服务端 API 接口需求

### 接口汇总

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/analyzer/tasks` | 获取待分析任务列表 |
| POST | `/api/v1/analyzer/tasks/{task_id}/heartbeat` | 任务心跳续期 |
| POST | `/api/v1/analyzer/tasks/{task_id}/release` | 释放任务 |
| POST | `/api/v1/analyzer/results` | 提交分析结果 |
| GET | `/api/v1/analyzer/stats` | 获取统计信息 |

---

### 3.1 获取待分析任务

**请求：**
```http
GET /api/v1/analyzer/tasks?limit=10
Authorization: Bearer {api_key}
```

或 Query 参数方式（调试使用）：
```http
GET /api/v1/analyzer/tasks?limit=10&api_key=xxx
```

**查询参数：**
| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| limit | int | 否 | 获取任务数量（默认 10，最大 50） |
| api_key | string | 条件 | API 密钥（Header 不传时必填） |

**请求头：**
| 头信息 | 必需 | 说明 |
|--------|------|------|
| Authorization | 推荐 | `Bearer {api_key}` |
| X-Analyzer-ID | 否 | 分析器实例标识（用于任务归属追踪） |

**任务分配机制：**

1. **任务锁定（Lease）**
   - 服务端返回任务时，自动将任务标记为 `locked`
   - 任务被锁定后，其他分析器在锁定期内无法获取该任务
   - 锁定期默认 **5 分钟**（可通过响应头 `X-Lock-Timeout` 获取）

2. **任务状态流转**
   ```
   pending ──► locked ──► analyzing ──► completed/failed
                 │
                 └── 锁过期 ──► pending (重新可分配)
   ```

3. **多分析器并发安全**
   - 使用数据库行级锁（`SELECT FOR UPDATE SKIP LOCKED`）
   - 确保同一照片不会被分配给多个分析器
   - 支持多台分析器同时工作，自动负载均衡

4. **心跳续期**
   - 长时间分析需要续期锁，否则任务可能被重新分配
   - 客户端可通过 `POST /api/v1/analyzer/tasks/{task_id}/heartbeat` 续期

**响应：**
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "tasks": [
      {
        "id": "task_xxx",
        "photo_id": 12345,
        "file_path": "/photos/IMG_001.jpg",
        "download_url": "http://nas:8080/api/v1/photos/12345/download?token=xxx",
        "download_token_expires_at": "2026-03-03T15:00:00Z",
        "width": 4000,
        "height": 3000,
        "taken_at": "2024-01-15T10:30:00Z",
        "location": "北京",
        "camera_model": "iPhone 14 Pro",
        "lock_expires_at": "2026-03-03T14:35:00Z"
      }
    ],
    "total_remaining": 856,
    "lock_duration": 300
  }
}
```

**响应字段说明：**
| 字段 | 类型 | 说明 |
|------|------|------|
| tasks | array | 任务列表（可能为空数组） |
| tasks[].id | string | 任务唯一标识（用于心跳续期） |
| tasks[].photo_id | int | 照片ID |
| tasks[].download_url | string | 照片下载 URL（带临时 token） |
| tasks[].download_token_expires_at | string | 下载 token 过期时间（ISO8601） |
| tasks[].lock_expires_at | string | 任务锁过期时间 |
| total_remaining | int | 剩余待分析照片总数（包含已被锁定的） |
| lock_duration | int | 锁定期时长（秒） |

**错误响应：**

```json
// 401 - API Key 无效
{
  "code": 401,
  "message": "invalid api key",
  "data": null
}

// 429 - 请求过于频繁
{
  "code": 429,
  "message": "rate limit exceeded",
  "data": {
    "retry_after": 60
  }
}

// 503 - 无可分析任务（服务端维护或全部完成）
{
  "code": 503,
  "message": "no tasks available",
  "data": {
    "total_remaining": 0
  }
}
```

**并发场景处理：**

| 场景 | 服务端处理 | 客户端处理 |
|------|-----------|-----------|
| 多分析器同时获取任务 | 数据库行级锁确保不重复分配 | 正常处理分配到的任务 |
| 任务锁即将过期 | 服务端在响应头返回 `X-Lock-Expires-At` | 在过期前 30 秒发送心跳续期 |
| 任务已被其他分析器锁定 | 该任务不会出现在返回列表中 | 无需处理 |
| 下载 URL 过期 | 返回的任务包含有效的下载 URL | 如需重新下载，重新获取任务 |

---

### 3.2 任务心跳续期

长时间分析（如大图片或慢速模型）需要续期任务锁，防止任务被重新分配。

**请求：**
```http
POST /api/v1/analyzer/tasks/{task_id}/heartbeat
Authorization: Bearer {api_key}
```

**路径参数：**
| 参数 | 类型 | 说明 |
|------|------|------|
| task_id | string | 任务ID（从获取任务接口返回） |

**请求体（可选）：**
```json
{
  "progress": 50,           // 分析进度百分比（0-100）
  "status": "analyzing"     // 当前状态：analyzing, downloading
}
```

**响应：**
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "lock_expires_at": "2026-03-03T14:40:00Z",
    "lock_duration": 300
  }
}
```

**错误响应：**
```json
// 404 - 任务不存在或已过期
{
  "code": 404,
  "message": "task not found or expired",
  "data": null
}

// 409 - 任务已被其他分析器认领
{
  "code": 409,
  "message": "task locked by another analyzer",
  "data": {
    "analyzer_id": "analyzer_xxx",
    "locked_at": "2026-03-03T14:30:00Z"
  }
}
```

**续期策略建议：**
- 分析器应在锁过期前 **30 秒** 发送心跳
- 下载大文件时，每 60 秒发送一次心跳
- AI 分析过程中，每 120 秒发送一次心跳

**并发冲突处理：**

| 场景 | 服务端行为 | 客户端处理 |
|------|-----------|-----------|
| 心跳时锁已过期，且任务被其他分析器获取 | 返回 409 Conflict | 停止分析，丢弃结果（其他分析器已接管） |
| 心跳时任务已完成 | 返回 404 | 停止分析，丢弃结果 |
| 网络延迟导致心跳到达服务端时锁已过期 | 返回 409 | 视为任务丢失，丢弃结果 |

---

### 3.3 释放任务（可选）

当分析器无法处理某张照片时（如下载失败、文件损坏），可主动释放任务，让其他分析器或稍后重试。

**请求：**
```http
POST /api/v1/analyzer/tasks/{task_id}/release
Authorization: Bearer {api_key}
```

**请求体：**
```json
{
  "reason": "download_failed",    // 释放原因
  "error_msg": "连接超时",         // 详细错误信息
  "retry_later": true             // 是否允许稍后重试
}
```

**释放原因枚举：**
| 原因 | 说明 | 服务端处理 |
|------|------|-----------|
| download_failed | 下载失败 | 标记为 pending，允许重试 |
| file_corrupted | 文件损坏 | 标记为 failed，记录错误 |
| ai_unavailable | AI 服务不可用 | 标记为 pending，稍后重试 |
| unsupported_format | 不支持的格式 | 标记为 failed，跳过 |
| timeout | 分析超时 | 标记为 pending，增加超时计数 |

**响应：**
```json
{
  "code": 200,
  "message": "task released",
  "data": {
    "photo_id": 12345,
    "new_status": "pending",
    "retry_count": 1
  }
}
```

---

### 3.4 提交分析结果

**请求：**
```http
POST /api/v1/analyzer/results
Authorization: Bearer {api_key}
Content-Type: application/json
```

或 Query 参数方式：
```http
POST /api/v1/analyzer/results?api_key=xxx
```

**请求体：**
```json
{
  "results": [
    {
      "photo_id": 12345,
      "task_id": "task_xxx",          // 可选，用于确认任务归属
      "description": "夕阳下的海滩，金色的阳光洒在沙滩上...",
      "caption": "海滩日落",
      "memory_score": 85,
      "beauty_score": 78,
      "overall_score": 83,
      "main_category": "风景",
      "tags": "海滩,日落,自然,旅行",
      "analyzed_at": "2026-03-03T14:30:00Z"
    }
  ]
}
```

**请求字段说明：**
| 字段 | 类型 | 必需 | 说明 |
|------|------|------|------|
| results | array | 是 | 分析结果列表（1-50条） |
| results[].photo_id | int | 是 | 照片ID |
| results[].task_id | string | 否 | 任务ID，用于验证任务归属 |
| results[].description | string | 是 | AI 生成的描述 |
| results[].caption | string | 否 | 简短标题 |
| results[].memory_score | int | 是 | 记忆分数（0-100） |
| results[].beauty_score | int | 是 | 美观分数（0-100） |
| results[].overall_score | int | 是 | 综合分数（0-100） |
| results[].main_category | string | 否 | 主分类 |
| results[].tags | string | 否 | 标签（逗号分隔） |
| results[].analyzed_at | string | 否 | 分析完成时间（ISO8601） |

**响应：**
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "accepted": 9,
    "rejected": 1,
    "rejected_items": [
      {
        "photo_id": 12345,
        "reason": "task_expired",
        "message": "任务锁已过期，照片已被分配给其他分析器"
      }
    ],
    "failed_photos": [12346]
  }
}
```

**拒绝原因说明：**
| 原因 | 说明 | 客户端处理 |
|------|------|-----------|
| task_expired | 任务锁已过期 | 丢弃结果，该照片已被其他分析器处理 |
| invalid_photo_id | 照片ID不存在 | 丢弃结果，记录日志 |
| validation_failed | 字段验证失败 | 修正后重试 |
| duplicate_result | 该照片已有分析结果 | 丢弃结果，视为成功 |

**错误响应：**
```json
// 400 - 请求格式错误
{
  "code": 400,
  "message": "invalid request",
  "data": {
    "errors": [
      "results[0].memory_score must be between 0 and 100"
    ]
  }
}

// 401 - API Key 无效
{
  "code": 401,
  "message": "invalid api key"
}

// 413 - 批量提交数量超限
{
  "code": 413,
  "message": "batch size too large",
  "data": {
    "max_allowed": 50,
    "current": 100
  }
}
```

**幂等性与并发处理：**

1. **重复提交（幂等性）**
   - 服务端使用 `photo_id` 作为幂等键
   - 同一 `photo_id` 的重复提交视为成功（不报错）
   - 客户端可以放心重试，无需去重

2. **任务锁过期后的结果提交**
   - 如果任务锁已过期，但照片尚未被其他分析器完成
   - 服务端会接受结果，但需要验证 `task_id` 历史记录
   - 如果照片已被其他分析器完成，返回 `duplicate_result`

3. **批量提交的部分失败**
   - 每个结果是独立处理的，一个失败不影响其他
   - 客户端根据 `rejected_items` 决定是否需要重试
   - 建议重试策略：指数退避，最多 3 次

4. **并发写入冲突**
   | 场景 | 服务端处理 | 客户端处理 |
   |------|-----------|-----------|
   | 两个分析器同时完成同一任务 | 先到达的接受，后到达的返回 `duplicate_result` | 视为成功，继续处理下一任务 |
   | 任务锁过期后结果到达 | 验证 `task_id` 归属，合法则接受 | 正常处理 |
   | 批量提交时网络中断 | 服务端可能部分处理，客户端重试时幂等去重 | 重试提交，幂等保证不重复 |

---

### 3.5 获取统计信息

**请求：**
```http
GET /api/v1/analyzer/stats
Authorization: Bearer {api_key}
```

或 Query 参数方式：
```http
GET /api/v1/analyzer/stats?api_key=xxx
```

**查询参数：**
| 参数 | 类型 | 必需 | 说明 |
|------|------|------|------|
| api_key | string | 条件 | API 密钥（Header 不传时必填） |

**响应：**
```json
{
  "code": 200,
  "message": "success",
  "data": {
    "total_photos": 10000,
    "analyzed": 1500,
    "pending": 8500,
    "locked": 20,               // 当前被锁定的任务数
    "failed": 80,               // 分析失败的照片数
    "my_tasks": {               // 当前 API Key 的任务统计
      "locked": 10,             // 当前持有的锁
      "completed": 500,         // 已完成分析数
      "failed": 5               // 分析失败数
    },
    "avg_analysis_time": 3.5,   // 平均分析时间（秒）
    "queue_pressure": "normal"  // 队列压力：low, normal, high
  }
}
```

**字段说明：**
| 字段 | 类型 | 说明 |
|------|------|------|
| total_photos | int | 照片总数 |
| analyzed | int | 已完成分析的照片数 |
| pending | int | 待分析的照片数（不含被锁定的） |
| locked | int | 当前被分析器锁定的任务数 |
| failed | int | 分析失败/重试超限的照片数 |
| my_tasks | object | 当前 API Key 的任务统计 |
| queue_pressure | string | 队列压力评估 |

**队列压力说明：**
| 压力级别 | 条件 | 建议 |
|----------|------|------|
| low | pending < 100 | 可减少并发或暂停 |
| normal | 100 <= pending < 1000 | 正常工作 |
| high | pending >= 1000 | 增加并发或启动更多分析器 |

---

## 4. 多分析器并发协调机制

当多个分析器实例同时运行时（无论是同一台机器的多进程，还是多台机器分布式部署），需要确保任务分配的正确性和系统稳定性。

### 4.1 任务分配策略

**服务端责任：**
1. **原子性分配**：使用数据库事务 + 行级锁（`SELECT FOR UPDATE SKIP LOCKED`）
2. **锁超时机制**：防止分析器崩溃导致任务永久锁定
3. **任务幂等**：同一 photo_id 可以被多次分析，但只保存第一个结果

**客户端责任：**
1. **唯一标识**：每个分析器实例应生成唯一的 `X-Analyzer-ID`（UUID 或 hostname + pid）
2. **心跳续期**：长任务需定期发送心跳，防止锁过期
3. **优雅退出**：收到终止信号时，释放持有的任务或等待完成当前批次

### 4.2 并发场景处理矩阵

| 场景 | 发生条件 | 服务端处理 | 客户端处理 |
|------|---------|-----------|-----------|
| **正常并行** | 多个分析器获取不同任务 | 各自锁定不同任务 | 正常分析并提交 |
| **锁竞争** | 多个分析器同时获取同一任务 | 数据库锁确保只有一个成功 | 失败的获取其他任务 |
| **锁过期** | 分析器A的锁过期，分析器B获取了该任务 | 将任务分配给B，记录A的锁失效 | A检测到心跳失败，丢弃结果 |
| **结果竞争** | A和B同时完成同一任务 | 先到达的接受，后到达的返回 `duplicate_result` | 视为成功，继续下一任务 |
| **网络分区** | A无法连接服务端，任务锁过期 | 锁过期后任务重新分配 | A恢复后检测到锁失效，丢弃结果 |
| **批量提交冲突** | A提交的一批结果中包含B已完成的任务 | 幂等处理，重复项返回 `duplicate_result` | 正常处理，继续下一批次 |

### 4.3 任务丢失防护

**可能导致任务丢失的场景：**
1. 分析器崩溃，持有的任务锁过期 → **自动重新分配** ✅
2. 分析器网络中断，无法续期 → **锁过期后重新分配** ✅
3. 分析器故意跳过某张照片 → **通过 release 接口释放** ✅

**防护措施：**
- 服务端定期扫描超时锁，自动重置为 `pending`
- 客户端启动时检查本地缓存，确认任务是否仍被锁定
- 任务在 `locked` 状态下也有最大重试次数限制

### 4.4 性能与公平性

**负载均衡：**
- 每个分析器独立获取任务，自然实现负载均衡
- 分析速度快的机器会自动获取更多任务

**公平性保障：**
- 任务分配采用 FIFO（按 photo_id 或创建时间排序）
- 防止某个分析器因网络快而"饿死"其他分析器

**限流与背压：**
- 服务端可返回 429 限制过于频繁的请求
- 客户端应根据 `queue_pressure` 调整并发数

### 4.5 断点续传与多分析器

**场景：同一 API Key 被多个分析器使用**

当多个分析器使用相同的 API Key 时：

1. **服务端视角**：无法区分不同分析器实例（除非使用 `X-Analyzer-ID`）
2. **本地缓存**：每个分析器应使用独立的本地缓存文件（如 `.analyzer_cache.{hostname}`）
3. **断点续传**：
   - 分析器重启后，通过本地缓存了解已处理的照片
   - 再次获取任务时，服务端会自动跳过已分析的照片
   - 无需关心其他分析器处理了多少

**最佳实践：**
- 生产环境建议每个分析器使用独立的 API Key（便于追踪和限流）
- 或者使用 `X-Analyzer-ID` 头区分实例

---

## 5. 分析器功能需求

### 5.1 命令列表

| 命令 | 说明 |
|------|------|
| `check` | 检查服务端连接和任务统计 |
| `analyze` | 启动分析进程 |
| `version` | 显示版本信息 |

### 4.2 配置项

```yaml
# analyzer.yaml
server:
  endpoint: "http://nas:8080"     # 服务端地址
  api_key: "${RELIVE_API_KEY}"    # API Key（优先环境变量）
  timeout: 30                     # API 请求超时（秒）

analyzer:
  workers: 4                      # 并发数（默认 4）
  batch_size: 10                  # 每批获取任务数
  retry_count: 3                  # 失败重试次数
  retry_delay: 5                  # 重试延迟（秒）
  cache_file: ".analyzer_cache"   # 本地断点续传缓存

ai:
  provider: "ollama"              # 仅支持本地 Provider
  ollama:
    endpoint: "http://localhost:11434"
    model: "llava:13b"
    temperature: 0.7
    timeout: 120

  vllm:
    endpoint: "http://localhost:8000"
    model: "llava-v1.6-vicuna-13b"
    temperature: 0.7
    timeout: 120

logging:
  level: "info"
  console: true
  file: "analyzer.log"
```

### 4.3 核心功能

#### 4.3.1 并发控制
- Worker Pool 管理并发任务
- 默认并发数：4（本地 AI 通常受限于 GPU 显存）
- 支持动态调整

#### 4.3.2 断点续传
- 本地缓存文件 `.analyzer_cache` 记录已处理 photo_id
- 支持持久化恢复，不依赖服务端状态
- 缓存格式：每行一个 `photo_id`

#### 4.3.3 进度跟踪
- 实时显示：已处理/总数、成功率、ETA
- 显示当前批次进度
- 统计：平均耗时、总耗时

#### 4.3.4 照片下载与暂存

**下载流程：**
```
1. 从任务中获取 download_url
2. HTTP GET 下载照片（支持流式下载）
3. 校验 Content-Type（必须是 image/*）
4. 写入临时目录（默认：~/.relive-analyzer/temp/）
5. 分析完成后自动清理
```

**临时文件管理：**
- **临时目录**: `~/.relive-analyzer/temp/`（可配置）
- **命名规则**: `{photo_id}_{timestamp}_{random}.jpg`
- **自动清理**: 分析完成后立即删除，异常退出时启动时清理
- **磁盘限额**: 临时目录最大 10GB，超过时拒绝新任务

**下载配置：**
```yaml
download:
  temp_dir: "~/.relive-analyzer/temp"   # 临时目录
  timeout: 60                            # 下载超时（秒）
  max_concurrent_downloads: 5            # 最大并发下载数
  retry_count: 3                         # 下载失败重试次数
  keep_temp: false                       # 是否保留临时文件（调试用）
```

**失败处理：**
| 场景 | 处理策略 |
|------|---------|
| 下载超时 | 重试 3 次，然后标记失败 |
| 文件损坏 | 校验图片格式，失败则跳过 |
| 磁盘不足 | 暂停新任务，等待清理 |
| URL 过期 | 重新获取任务（服务端需支持刷新 URL）|

#### 4.3.5 批量提交策略

**触发条件（满足任一）：**
1. **数量阈值**: 累积达到 `batch_size` 条结果（默认 10）
2. **时间阈值**: 距离上次提交超过 `flush_interval`（默认 30 秒）
3. **进程退出**: 收到终止信号时立即刷新

**提交流程：**
```
1. 收集结果到缓冲区
2. 达到触发条件 → 构建批量请求
3. POST /api/v1/analyzer/results
4. 服务端返回 accepted/rejected 列表
5. 已接受的从缓冲区移除
6. 被拒绝/失败的保留，下次重试
```

**配置项：**
```yaml
batch:
  size: 10                # 批量提交数量
  flush_interval: 30      # 自动刷新间隔（秒）
  max_retry: 3            # 提交失败重试次数
  retry_delay: 5          # 重试延迟（秒）
```

**失败处理：**
| 场景 | 处理策略 |
|------|---------|
| 网络中断 | 保留在缓冲区，指数退避重试 |
| 部分失败 | 服务端返回失败列表，仅重试失败项 |
| 全部失败 | 保留在缓冲区，延迟后整体重试 |
| 进程退出 | 序列化缓冲区到磁盘，下次启动恢复 |

### 4.4 异常处理

| 场景 | 处理策略 |
|------|---------|
| API 连接失败 | 指数退避重试，最多 5 次 |
| 获取任务为空 | 等待 30 秒后重试 |
| AI 分析失败 | 记录失败原因，继续下一批 |
| 结果提交失败 | 本地缓存，定时重试 |
| 照片下载失败 | 跳过，记录 photo_id |
| Ctrl+C 中断 | 保存当前进度，优雅退出 |

---

## 6. 详细设计

### 5.1 数据流详细设计

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           relive-analyzer                               │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────┐            │
│  │  TaskFetcher │────▶│ Worker Pool  │────▶│ ResultBuffer │            │
│  │              │     │              │     │              │            │
│  │ - 轮询获取    │     │ - 并发分析    │     │ - 批量收集    │            │
│  │ - 任务队列    │     │ - 下载分析    │     │ - 定时刷新    │            │
│  └──────────────┘     └──────────────┘     └──────────────┘            │
│         │                   │                       │                  │
│         ▼                   ▼                       ▼                  │
│  ┌──────────────────────────────────────────────────────────┐        │
│  │                      Local Cache                          │        │
│  │  - checkpoint.db (SQLite) 断点续传状态                     │        │
│  │  - batch_buffer.json 未提交结果                            │        │
│  │  - temp/ 临时下载的照片文件                                │        │
│  └──────────────────────────────────────────────────────────┘        │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
                                    │
           ┌────────────────────────┼────────────────────────┐
           ▼                        ▼                        ▼
    ┌──────────────┐         ┌──────────────┐         ┌──────────────┐
    │  Relive API  │         │    Local AI  │         │   Temp File  │
    │              │         │              │         │              │
    │ /tasks       │         │ Ollama/vLLM  │         │ ~/.relive-   │
    │ /results     │         │ GPU分析      │         │ analyzer/    │
    └──────────────┘         └──────────────┘         └──────────────┘
```

### 5.2 分析器状态机

```
                    ┌─────────────┐
                    │    Init     │
                    └──────┬──────┘
                           │ 验证配置
                           ▼
                    ┌─────────────┐
         ┌─────────│   CheckAPI  │◄────────┐
         │         └──────┬──────┘         │
         │                │ API正常        │
         │ 失败重试       ▼                │
         │         ┌─────────────┐         │
         └─────────│   Fetching  │         │
                   │   Tasks     │─────────┘
                   └──────┬──────┘
                          │ 获取到任务
                          ▼
                   ┌─────────────┐
                   │  Processing │◄────────┐
                   │   (Worker   │         │
                   │    Pool)    │─────────┘
                   └──────┬──────┘  循环处理
                          │ 所有任务完成
                          ▼
                   ┌─────────────┐
                   │   Flushing  │
                   │   Results   │
                   └──────┬──────┘
                          │
                          ▼
                   ┌─────────────┐
                   │  Completed  │
                   └─────────────┘
```

### 5.3 模块结构

```
backend/cmd/relive-analyzer/
├── main.go                    # CLI 入口
└── internal/
    ├── analyzer/
    │   ├── analyzer.go        # 主控制器（状态机）
    │   ├── config.go          # 配置解析
    │   ├── worker_pool.go     # 并发控制（复用现有）
    │   ├── progress.go        # 进度跟踪（复用现有）
    │   └── stats.go           # 统计信息（复用现有）
    ├── client/
    │   ├── api_client.go      # HTTP API 客户端
    │   ├── task_fetcher.go    # 任务获取器
    │   └── result_sender.go   # 结果发送器
    ├── cache/
    │   ├── checkpoint.go      # 断点续传管理
    │   └── buffer.go          # 结果缓冲区
    ├── download/
    │   ├── downloader.go      # 照片下载器
    │   └── temp_manager.go    # 临时文件管理
    └── ai/
        └── local_provider.go  # 本地 AI Provider（复用现有）
```

---

## 7. 安全设计

### 6.1 API Key 管理
- 支持环境变量 `RELIVE_API_KEY`
- 配置文件中可使用占位符 `${RELIVE_API_KEY}`
- 命令行参数支持 `-api-key`（仅用于测试）

### 6.2 传输安全
- 支持 HTTPS
- 照片下载 URL 带临时 token
- API Key 仅在 Header 或 Query 中传输

---

## 8. 错误码定义

### 7.1 服务端 API 错误码

| 错误码 | 说明 | 处理建议 |
|--------|------|---------|
| 200 | 成功 | - |
| 401 | API Key 无效 | 检查配置 |
| 403 | 权限不足 | 联系管理员 |
| 404 | 照片不存在 | 跳过，记录日志 |
| 429 | 请求过于频繁 | 降低并发数 |
| 500 | 服务端错误 | 稍后重试 |
| 503 | 服务维护中 | 暂停，稍后重试 |

### 7.2 分析器内部错误码

| 错误码 | 说明 | 处理策略 |
|--------|------|---------|
| A001 | 配置无效 | 启动时检查，立即退出 |
| A002 | API 连接失败 | 指数退避重试 5 次 |
| A003 | 任务获取为空 | 等待 30 秒后继续 |
| A004 | 照片下载失败 | 重试 3 次，然后跳过 |
| A005 | AI 分析失败 | 记录失败原因，继续 |
| A006 | 结果提交失败 | 保留缓冲区，重试 |
| A007 | 磁盘空间不足 | 暂停，等待清理 |
| A008 | 临时文件损坏 | 清理后重新下载 |

---

## 9. 使用示例

### 6.1 基本使用

```bash
# 设置环境变量
export RELIVE_API_KEY="your-api-key"

# 检查连接和任务统计
./relive-analyzer check -config analyzer.yaml

# 启动分析
./relive-analyzer analyze -config analyzer.yaml
```

### 6.2 命令行参数

```bash
./relive-analyzer analyze \
  -config analyzer.yaml \
  -server http://nas:8080 \
  -api-key $RELIVE_API_KEY \
  -workers 4 \
  -batch-size 10 \
  -verbose
```

### 6.3 Docker 运行

```bash
docker run -d \
  --name relive-analyzer \
  --gpus all \
  -e RELIVE_API_KEY=$RELIVE_API_KEY \
  -e RELIVE_SERVER=http://nas:8080 \
  -v $(pwd)/analyzer.yaml:/app/analyzer.yaml \
  relive-analyzer:latest
```

---

## 10. 实现计划

### Phase 1：服务端 API 开发
- [ ] 实现 `/api/v1/analyzer/tasks` 接口（含任务锁定机制）
- [ ] 实现 `/api/v1/analyzer/tasks/{id}/heartbeat` 心跳续期接口
- [ ] 实现 `/api/v1/analyzer/tasks/{id}/release` 任务释放接口
- [ ] 实现 `/api/v1/analyzer/results` 结果提交接口（含幂等性处理）
- [ ] 实现 `/api/v1/analyzer/stats` 统计接口
- [ ] API Key 认证中间件
- [ ] 数据库行级锁实现（`SELECT FOR UPDATE SKIP LOCKED`）
- [ ] 定时任务：清理过期锁（锁超时自动重置）

### Phase 2：分析器改造
- [ ] 新增 API Client 模块（含认证、重试、超时）
- [ ] 实现任务获取与心跳续期机制
- [ ] 实现照片下载与临时文件管理
- [ ] 重构结果回写逻辑（批量提交）
- [ ] 本地缓存机制（断点续传 + 结果缓冲区）
- [ ] 多分析器并发安全（X-Analyzer-ID 支持）

### Phase 3：功能优化
- [ ] 批量提交优化（触发策略、部分失败处理）
- [ ] 失败重试机制（指数退避、最大重试次数）
- [ ] 并发控制优化（动态 worker 数、背压处理）
- [ ] 性能监控（分析耗时、API 延迟、成功率）

### Phase 4：测试与文档
- [ ] 单元测试
- [ ] 集成测试（单分析器全链路）
- [ ] 并发测试（多分析器同时运行）
- [ ] 压力测试（高并发、大文件、弱网）
- [ ] 故障恢复测试（断网、重启、崩溃）
- [ ] 更新文档

---

## 11. 附录

### 11.1 本地缓存文件格式

**断点续传缓存 (checkpoint.db)**
SQLite 数据库，表结构：
```sql
CREATE TABLE checkpoint (
    photo_id INTEGER PRIMARY KEY,
    status TEXT NOT NULL,           -- 'success', 'failed', 'pending'
    attempts INTEGER DEFAULT 0,     -- 尝试次数
    error_msg TEXT,                 -- 失败原因
    processed_at TIMESTAMP          -- 处理时间
);

CREATE INDEX idx_status ON checkpoint(status);
```

**结果缓冲区 (batch_buffer.json)**
进程异常退出时序列化未提交结果：
```json
{
  "version": 1,
  "saved_at": "2026-03-03T14:30:00Z",
  "results": [
    {
      "photo_id": 12345,
      "description": "...",
      "memory_score": 85,
      "...": "..."
    }
  ]
}
```

### 11.2 与服务端版本兼容性

| 分析器版本 | 服务端最低版本 | 说明 |
|-----------|--------------|------|
| 2.0.x | 1.5.0 | API 模式首次发布 |

### 11.3 完整配置示例

```yaml
# analyzer.yaml - 完整配置示例
server:
  endpoint: "http://nas:8080"           # 服务端地址
  api_key: "${RELIVE_API_KEY}"          # API Key（从环境变量读取）
  timeout: 30                           # API 请求超时（秒）
  retry_max: 5                          # 最大重试次数

analyzer:
  workers: 4                            # 并发分析数
  fetch_limit: 10                       # 每批获取任务数
  retry_count: 3                        # AI 分析重试次数
  retry_delay: 5                        # 重试延迟（秒）
  checkpoint_file: "checkpoint.db"      # 断点续传文件

download:
  temp_dir: "~/.relive-analyzer/temp"   # 临时文件目录
  timeout: 60                           # 下载超时（秒）
  max_concurrent: 5                     # 最大并发下载
  retry_count: 3                        # 下载重试次数
  max_temp_size: "10GB"                 # 临时目录上限

batch:
  size: 10                              # 批量提交数量
  flush_interval: 30                    # 自动刷新间隔（秒）
  max_retry: 3                          # 提交失败重试次数
  retry_delay: 5                        # 重试延迟（秒）
  buffer_file: "batch_buffer.json"      # 缓冲区持久化文件

ai:
  provider: "ollama"                    # AI Provider
  ollama:
    endpoint: "http://localhost:11434"
    model: "llava:13b"
    temperature: 0.7
    timeout: 120

logging:
  level: "info"
  console: true
  file: "analyzer.log"
  max_size: 100
  max_backups: 3
  max_age: 30
```

---

## 12. 决策记录

| 日期 | 事项 | 决策 |
|------|------|------|
| 2026-03-03 | 照片传输方式 | **下载 URL 模式**：服务端提供下载 URL，分析器自动下载暂存 |
| 2026-03-03 | 结果提交策略 | **批量提交**：可配置 batch_size 和 flush_interval，支持异常退出时持久化 |
| 2026-03-03 | 断点续传粒度 | **精确到 photo_id**：使用 SQLite 记录每张照片的处理状态 |
| 2026-03-03 | 临时文件管理 | **自动清理**：分析完成后立即删除，支持磁盘限额保护 |
