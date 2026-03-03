# relive-analyzer API 模式 - Phase 1 完成总结

> 日期：2026-03-03
> 状态：✅ 已完成

## 实现概述

Phase 1 服务端 API 开发已全部完成，包括所有需求文档中规划的接口和功能。

## 已完成的接口

### 1. 获取待分析任务
- **路径**: `GET /api/v1/analyzer/tasks`
- **认证**: API Key (Header: `Authorization: Bearer {key}` 或 `X-API-Key: {key}` 或 Query: `api_key={key}`)
- **功能**:
  - 自动锁定任务（5分钟锁定期）
  - 支持 `X-Analyzer-ID` 头追踪分析器实例
  - 返回下载 URL 和锁过期时间
  - 支持 limit 参数（默认10，最大50）

### 2. 任务心跳续期
- **路径**: `POST /api/v1/analyzer/tasks/{task_id}/heartbeat`
- **功能**:
  - 续期任务锁（延长5分钟）
  - 支持进度上报（可选）
  - 409 响应处理（任务被其他分析器锁定）

### 3. 释放任务
- **路径**: `POST /api/v1/analyzer/tasks/{task_id}/release`
- **功能**:
  - 主动释放无法处理的任务
  - 支持指定释放原因
  - 支持标记是否允许稍后重试

### 4. 提交分析结果
- **路径**: `POST /api/v1/analyzer/results`
- **功能**:
  - 批量提交（1-50条）
  - 幂等性处理（重复提交不报错）
  - 部分失败处理（每条独立处理）
  - 自动计算综合评分（70%回忆 + 30%美观）

### 5. 获取统计信息
- **路径**: `GET /api/v1/analyzer/stats`
- **功能**:
  - 总照片数、已分析数、待分析数
  - 当前锁定数、失败数
  - 队列压力评估（low/normal/high）

### 6. 清理过期锁（手动）
- **路径**: `POST /api/v1/analyzer/clean-locks`
- **功能**: 手动触发清理过期任务锁

## 认证方式

支持三种方式传递 API Key：

1. **Bearer Token** (推荐):
   ```
   Authorization: Bearer {api_key}
   ```

2. **X-API-Key Header**:
   ```
   X-API-Key: {api_key}
   ```

3. **Query 参数** (调试使用):
   ```
   GET /api/v1/analyzer/tasks?api_key={key}
   ```

## 定时任务

- **任务**: 自动清理过期锁
- **频率**: 每5分钟执行一次
- **实现**: `internal/service/scheduler.go`
- **启动**: 服务启动时自动开始
- **停止**: 服务关闭时优雅退出

## 数据库模型

Photo 表新增字段（model/photo.go）：

```go
AnalysisLockID        *string    // 分析器实例ID
AnalysisLockExpiredAt *time.Time // 锁过期时间
AnalysisRetryCount    int        // 分析重试次数
```

## 核心文件列表

| 文件 | 说明 |
|------|------|
| `internal/model/analyzer.go` | 分析器相关模型定义 |
| `internal/service/analysis_service.go` | 分析服务业务逻辑 |
| `internal/service/scheduler.go` | 定时任务调度器 |
| `internal/api/v1/handler/analyzer_handler.go` | HTTP 接口处理 |
| `internal/api/v1/router/router.go` | 路由配置 |
| `internal/middleware/auth.go` | API Key 认证中间件 |

## 多分析器并发安全

- 使用数据库 UPDATE 语句模拟行级锁
- 任务锁定时长：5分钟
- 支持心跳续期
- 锁过期后任务自动重新分配

## 错误码

| 状态码 | 说明 |
|--------|------|
| 200 | 成功 |
| 400 | 请求格式错误 |
| 401 | API Key 无效 |
| 404 | 任务不存在或已过期 |
| 409 | 任务被其他分析器锁定 |
| 413 | 批量提交数量超限 |
| 503 | 无可分析任务 |

## 下一步工作

Phase 2: 分析器改造（客户端实现）

- [ ] API Client 模块
- [ ] 任务获取与心跳续期
- [ ] 照片下载与临时文件管理
- [ ] 结果批量提交
- [ ] 本地缓存机制
- [ ] 断点续传

## 测试建议

```bash
# 1. 检查服务健康
curl http://localhost:8080/api/v1/system/health

# 2. 获取任务（使用 API Key）
curl -H "Authorization: Bearer your-api-key" \
  http://localhost:8080/api/v1/analyzer/tasks?limit=5

# 3. 查看统计
curl -H "Authorization: Bearer your-api-key" \
  http://localhost:8080/api/v1/analyzer/stats

# 4. 提交结果
curl -X POST -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"results":[{"photo_id":1,"description":"test","memory_score":80,"beauty_score":70}]}' \
  http://localhost:8080/api/v1/analyzer/results
```
