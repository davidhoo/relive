# relive-analyzer API 模式 - Phase 2 完成总结

> 日期：2026-03-03
> 状态：✅ 已完成

## 实现概述

Phase 2 分析器客户端改造已全部完成，实现了从 SQLite 文件模式到 HTTP API 模式的迁移。

## 新增文件列表

| 文件 | 说明 |
|------|------|
| `cmd/relive-analyzer/internal/config/config.go` | 分析器配置（YAML + 环境变量） |
| `cmd/relive-analyzer/internal/client/api_client.go` | HTTP API 客户端 |
| `cmd/relive-analyzer/internal/client/task_manager.go` | 任务获取与心跳管理 |
| `cmd/relive-analyzer/internal/download/downloader.go` | 照片下载与临时文件管理 |
| `cmd/relive-analyzer/internal/cache/buffer.go` | 结果缓冲区（批量提交） |
| `cmd/relive-analyzer/internal/cache/checkpoint.go` | 断点续传管理 |
| `cmd/relive-analyzer/internal/analyzer/api_analyzer.go` | API 模式分析器主程序 |
| `cmd/relive-analyzer/analyzer_factory.go` | 分析器工厂函数 |
| `cmd/relive-analyzer/main.go` | 新版 CLI 入口 |

## 核心功能

### 1. API Client 模块
- 支持 Bearer Token / X-API-Key / Query 参数三种认证方式
- 自动重试机制（指数退避，最多 3 次）
- 超时控制（可配置）
- 完整的 API 封装（tasks, heartbeat, release, results, stats）

### 2. 任务管理器
- 自动轮询获取任务
- 心跳续期（锁过期前 30 秒自动发送）
- 任务释放（失败时自动释放）
- 并发安全的任务队列

### 3. 照片下载器
- HTTP 流式下载
- 临时目录管理（`~/.relive-analyzer/temp/`）
- 自动清理（分析完成后立即删除）
- 磁盘限额保护（最大 10GB）
- 下载失败重试

### 4. 结果缓冲区
- 批量收集（默认 10 条）
- 定时刷新（默认 30 秒）
- 进程退出时持久化到文件
- 启动时自动恢复

### 5. 断点续传
- SQLite 数据库记录处理状态
- 防止重复处理
- 支持失败重试
- 清理卡住的 pending 记录

### 6. 多分析器并发安全
- 自动生成唯一的 Analyzer ID
- 支持 X-Analyzer-ID 头
- 任务锁机制

## 命令行接口

```bash
# 生成示例配置
./relive-analyzer gen-config > analyzer.yaml

# 检查服务端连接
./relive-analyzer check -config analyzer.yaml

# 启动分析
./relive-analyzer analyze -config analyzer.yaml

# 自定义并发数
./relive-analyzer analyze -config analyzer.yaml -workers 8

# 显示版本
./relive-analyzer version
```

## 配置文件示例

```yaml
server:
  endpoint: "http://nas:8080"
  api_key: "${RELIVE_API_KEY}"
  timeout: 30

analyzer:
  workers: 4
  fetch_limit: 10
  retry_count: 3
  retry_delay: 5
  checkpoint_file: "~/.relive-analyzer/checkpoint.db"

ai:
  provider: "ollama"
  ollama:
    endpoint: "http://localhost:11434"
    model: "llava:13b"
    temperature: 0.7
    timeout: 120

download:
  temp_dir: "~/.relive-analyzer/temp"
  timeout: 60
  max_concurrent: 5
  retry_count: 3
  keep_temp: false

batch:
  size: 10
  flush_interval: 30
  max_retry: 3
  retry_delay: 5

logging:
  level: "info"
  console: true
  file: "analyzer.log"
```

## 工作流程

```
1. 启动分析器
   ├── 加载配置
   ├── 验证 AI Provider
   ├── 恢复断点续传状态
   └── 恢复结果缓冲区

2. 任务获取循环
   └── 每 5 秒检查任务队列
       └── 队列不足时自动获取新任务

3. 任务处理循环
   └── 从队列获取任务
       ├── 检查是否已处理（断点续传）
       ├── 提交到 Worker Pool
       └── 并发分析

4. 单个任务处理
   ├── 标记为处理中
   ├── 下载照片
   ├── 图像预处理
   ├── AI 分析
   ├── 添加到结果缓冲区
   ├── 更新检查点
   └── 停止心跳

5. 结果提交
   └── 缓冲区批量提交
       ├── 数量达到阈值（10条）
       ├── 定时刷新（30秒）
       └── 进程退出时

6. 优雅退出
   ├── 停止任务获取
   ├── 等待 Worker Pool 完成
   ├── 刷新结果缓冲区
   ├── 保存检查点
   └── 清理临时文件
```

## 与 Phase 1 服务端配合

```
┌─────────────────┐     HTTP API      ┌─────────────────┐
│   Relive 服务    │ ◄──────────────► │  relive-analyzer│
│   (NAS/服务器)   │                   │   (AI 工作站)    │
└─────────────────┘                   └─────────────────┘
        │                                      │
        │  1. GET /tasks                       │  3. 本地 AI 分析
        │  2. POST /heartbeat                  │  4. POST /results
        │  5. GET /stats                       │
        │                                      │
        ▼                                      ▼
┌─────────────────┐                   ┌─────────────────┐
│   照片存储        │                   │   Ollama/vLLM   │
│  (本地/网络存储)  │                   │   (本地 GPU)    │
└─────────────────┘                   └─────────────────┘
```

## 测试验证

```bash
cd backend

# 编译
go build -o relive-analyzer ./cmd/relive-analyzer

# 生成配置
./relive-analyzer gen-config > analyzer.yaml

# 编辑配置，设置 endpoint 和 api_key

# 检查连接
./relive-analyzer check -config analyzer.yaml

# 启动分析
./relive-analyzer analyze -config analyzer.yaml -verbose
```

## 下一步工作

Phase 3: 功能优化

- [ ] 批量提交优化（触发策略、部分失败处理）
- [ ] 失败重试机制（指数退避、最大重试次数）
- [ ] 并发控制优化（动态 worker 数、背压处理）
- [ ] 性能监控（分析耗时、API 延迟、成功率）

Phase 4: 测试与文档

- [ ] 单元测试
- [ ] 集成测试
- [ ] 并发测试（多分析器同时运行）
- [ ] 压力测试
- [ ] 故障恢复测试
- [ ] 更新文档

## 注意事项

1. **API Key**: 确保环境变量 `RELIVE_API_KEY` 已设置或在配置文件中指定
2. **网络**: 分析器需要能够访问 Relive 服务端和本地 AI 服务
3. **磁盘空间**: 临时目录默认最大 10GB，可通过配置调整
4. **并发数**: 根据 GPU 显存调整 workers 数量（Ollama 建议 1-2，vLLM 可更高）
