# relive-analyzer 离线分析工具

> **⚠️ 版本说明**
> - 本文档描述的是 **v1.x 文件模式**（使用 SQLite 数据库文件）
> - **v2.0+ API 模式** 请查看 [ANALYZER_API_MODE.md](./ANALYZER_API_MODE.md)
> - 推荐使用 API 模式，更灵活、无需传输数据库文件

## 概述

`relive-analyzer` 是 relive 项目的离线照片分析工具，专门设计用于解决 NAS 与 AI 服务物理分离的场景。

### 两种工作模式

| 模式 | 版本 | 数据交换方式 | 适用场景 |
|------|------|-------------|---------|
| **文件模式** | v1.x | SQLite 数据库文件 | NAS 与分析机物理隔离，需离线传输 |
| **API 模式** | v2.0+ | HTTP API | 分析机可访问 NAS 网络，实时通信 |

### 使用场景

1. **NAS 端**：运行 relive 服务，扫描照片并导出数据库
2. **分析端**：将 `export.db` 复制到有 AI 服务的电脑上
3. **批量分析**：使用 `relive-analyzer` 批量分析照片
4. **导入回 NAS**：将分析结果导入回 NAS 的 relive 服务

### 核心特性

- ✅ **支持 5 种 AI Provider**：Ollama、Qwen、OpenAI、vLLM、Hybrid
- ✅ **高性能并发**：自动根据 Provider 能力设置并发数
- ✅ **断点续传**：中断后重新运行自动跳过已分析照片
- ✅ **实时进度**：进度条显示，ETA 计算
- ✅ **失败重试**：可配置的重试次数和延迟
- ✅ **成本统计**：准确跟踪 API 调用成本
- ✅ **优雅退出**：Ctrl+C 安全停止，不丢失已分析数据

---

## 安装

### 编译

```bash
cd /path/to/relive/backend

# 开发版本
go build -o relive-analyzer ./cmd/relive-analyzer

# 生产版本（带版本信息）
go build -ldflags "\
  -X main.Version=1.0.0 \
  -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  -o relive-analyzer ./cmd/relive-analyzer

# 交叉编译 Linux
GOOS=linux GOARCH=amd64 go build -o relive-analyzer-linux ./cmd/relive-analyzer
```

编译后的二进制文件位于 `cmd/relive-analyzer/relive-analyzer`。

---

## 配置

### 配置文件 (`analyzer.yaml`)

配置文件位于 `configs/analyzer.yaml`：

```yaml
analyzer:
  workers: 0              # 并发数（0=自动）
  retry_count: 3          # 重试次数
  retry_delay: 5          # 重试延迟（秒）

ai:
  provider: "ollama"      # 使用的 Provider

  ollama:
    endpoint: "http://localhost:11434"
    model: "llava:13b"
    temperature: 0.7
    timeout: 120

  qwen:
    api_key: "${QWEN_API_KEY}"
    model: "qwen-vl-max"
    temperature: 0.7
    timeout: 60

  openai:
    api_key: "${OPENAI_API_KEY}"
    model: "gpt-4-vision-preview"
    temperature: 0.7
    max_tokens: 500
    timeout: 60

  vllm:
    endpoint: "http://localhost:8000"
    model: "llava-v1.6-vicuna-13b"
    temperature: 0.7
    max_tokens: 500
    timeout: 120

  hybrid:
    primary: "ollama"
    fallback: "qwen"
    retry_on_error: true

logging:
  level: "info"
  console: true
  file: "analyzer.log"
  max_size: 100
  max_backups: 3
  max_age: 30
```

### 环境变量

API 密钥应通过环境变量设置：

```bash
export QWEN_API_KEY="your-qwen-api-key"
export OPENAI_API_KEY="your-openai-api-key"
```

---

## 使用指南

### 1. 检查数据库状态

查看数据库中照片的分析状态：

```bash
./relive-analyzer check -db /path/to/export.db
```

输出示例：
```
==================================================
Database Status
==================================================
Total photos:      1000
Analyzed:          200 (20.0%)
Unanalyzed:        800 (80.0%)
==================================================
```

### 2. 估算成本和时间

在开始分析前，估算所需的成本和时间：

```bash
./relive-analyzer estimate -config analyzer.yaml -db /path/to/export.db
```

输出示例：
```
==================================================
Cost Estimation
==================================================
Provider:          qwen
Unanalyzed photos: 800
Workers:           5
--------------------------------------------------
Cost per photo:    ¥0.0050
Estimated total:   ¥4.00
--------------------------------------------------
Est. time:         16m0s
==================================================

Note: This is a rough estimate. Actual cost and time may vary.
```

### 3. 开始分析

运行批量分析：

```bash
# 基本用法
./relive-analyzer analyze -config analyzer.yaml -db /path/to/export.db

# 指定并发数
./relive-analyzer analyze -config analyzer.yaml -db /path/to/export.db -workers 10

# 启用详细日志
./relive-analyzer analyze -config analyzer.yaml -db /path/to/export.db -verbose

# 自定义重试设置
./relive-analyzer analyze -config analyzer.yaml -db /path/to/export.db \
  -retry 5 -retry-delay 10
```

进度显示示例：
```
[==============>               ] 150/800 (18.8%) | Elapsed: 5m12s | ETA: 22m48s
```

完成后统计信息：
```
==================================================
Analysis Statistics
==================================================
Total:     800
Success:   795 (99.4%)
Failed:    5
Skipped:   0
──────────────────────────────────────────────────
Elapsed:   28m15s
Avg Time:  2.1s per photo
Total Cost: ¥3.98
Avg Cost:   ¥0.0050 per photo
──────────────────────────────────────────────────
Failure Reasons:
  - file not found: 3
  - timeout: 2
==================================================
```

### 4. 查看版本

```bash
./relive-analyzer version
```

---

## 命令参考

### check

检查数据库状态。

**语法**：
```bash
relive-analyzer check -db <database-path>
```

**参数**：
- `-db string` - 数据库文件路径（必需）

### estimate

估算分析成本和时间。

**语法**：
```bash
relive-analyzer estimate -config <config-file> -db <database-path>
```

**参数**：
- `-config string` - 配置文件路径（默认：`analyzer.yaml`）
- `-db string` - 数据库文件路径（必需）

### analyze

执行批量分析。

**语法**：
```bash
relive-analyzer analyze [options]
```

**参数**：
- `-config string` - 配置文件路径（默认：`analyzer.yaml`）
- `-db string` - 数据库文件路径（必需）
- `-workers int` - 并发数（0=自动，默认：0）
- `-retry int` - 失败重试次数（默认：3）
- `-retry-delay int` - 重试延迟（秒，默认：5）
- `-verbose` - 启用详细日志

### version

显示版本信息。

**语法**：
```bash
relive-analyzer version
```

---

## 高级用法

### 并发控制

不同 Provider 的推荐并发数：

- **Ollama**：1（GPU 密集型，避免过载）
- **Qwen/OpenAI**：5-10（API 限流）
- **vLLM**：取决于服务器容量，可更高
- **Hybrid**：根据 primary provider 决定

手动设置：
```bash
# Ollama - 单线程
./relive-analyzer analyze -config analyzer.yaml -db export.db -workers 1

# Qwen - 10 并发
./relive-analyzer analyze -config analyzer.yaml -db export.db -workers 10
```

### 断点续传

分析过程中按 `Ctrl+C` 可安全中断。重新运行相同命令会自动续传：

```bash
# 首次运行
./relive-analyzer analyze -config analyzer.yaml -db export.db

# （按 Ctrl+C 中断）

# 续传（自动跳过已分析）
./relive-analyzer analyze -config analyzer.yaml -db export.db
```

工具会自动检测 `ai_analyzed = 0` 的照片，跳过已分析的记录。

### Hybrid 模式

混合模式可在主 Provider 失败时自动切换到备用 Provider：

```yaml
ai:
  provider: "hybrid"
  hybrid:
    primary: "ollama"     # 优先使用本地 Ollama
    fallback: "qwen"      # 失败时切换到 Qwen
    retry_on_error: true
```

适用场景：
- 本地 GPU 不稳定，需要云端备份
- 希望降低成本，优先使用免费本地模型
- 多个 API 账户轮换使用

### 日志分析

日志文件位于配置的路径（默认：`analyzer.log`），使用 JSON 格式：

```bash
# 查看实时日志
tail -f analyzer.log

# 筛选错误
cat analyzer.log | jq 'select(.level=="ERROR")'

# 统计失败原因
cat analyzer.log | jq -r 'select(.msg | contains("Failed")) | .msg'
```

---

## 故障排查

### 常见问题

#### 1. Provider 不可用

**错误信息**：
```
Error: Provider ollama is not available
```

**解决方法**：
- 检查 Ollama 服务是否运行：`curl http://localhost:11434/api/tags`
- 确认模型已下载：`ollama pull llava:13b`
- 检查配置文件中的 endpoint

#### 2. API 密钥未配置

**错误信息**：
```
Error creating provider: Qwen API key not configured
```

**解决方法**：
```bash
export QWEN_API_KEY="your-key"
./relive-analyzer analyze -config analyzer.yaml -db export.db
```

#### 3. 数据库文件不存在

**错误信息**：
```
Error: Database file not found: export.db
```

**解决方法**：
- 确认文件路径正确
- 使用绝对路径：`./relive-analyzer check -db /absolute/path/to/export.db`

#### 4. 分析速度慢

**可能原因**：
- 并发数设置过低
- 网络延迟（云端 API）
- GPU 性能不足（本地模型）

**优化方法**：
```bash
# 增加并发数（适用于云端 API）
./relive-analyzer analyze -config analyzer.yaml -db export.db -workers 10

# 切换到更快的 Provider
# 修改 analyzer.yaml 中的 provider 设置
```

#### 5. 内存不足

**症状**：进程被系统 kill

**解决方法**：
- 减少并发数：`-workers 1`
- 使用更小的模型
- 增加系统内存

---

## 性能基准

基于实际测试的性能参考（单张照片平均时间）：

| Provider | 模型 | 并发数 | 平均时间 | 吞吐量 |
|----------|------|--------|----------|--------|
| Ollama | llava:13b | 1 | 8-12s | 5-7 张/分钟 |
| Qwen | qwen-vl-max | 5 | 2-3s | 100-150 张/分钟 |
| OpenAI | gpt-4-vision | 3 | 3-5s | 36-60 张/分钟 |
| vLLM | llava-v1.6 | 4 | 5-8s | 30-48 张/分钟 |

**注意**：实际性能取决于硬件配置、网络状况和照片内容。

---

## 工作流程示例

### 完整离线分析流程

**1. NAS 端（导出数据库）**

```bash
# 假设 NAS 上运行 relive 服务
# 使用 Web UI 或 API 导出数据库
curl http://nas-ip:8080/api/export -o export.db

# 或使用 scp 从 NAS 复制
scp nas:/path/to/relive/data.db ./export.db
```

**2. 分析端（批量分析）**

```bash
# 复制数据库到本地
cp /nas/export.db ./export.db

# 检查状态
./relive-analyzer check -db export.db

# 估算成本
./relive-analyzer estimate -config analyzer.yaml -db export.db

# 开始分析
./relive-analyzer analyze -config analyzer.yaml -db export.db -verbose

# 分析完成后，检查结果
./relive-analyzer check -db export.db
```

**3. NAS 端（导入结果）**

```bash
# 将分析后的数据库复制回 NAS
scp export.db nas:/path/to/relive/

# 使用 Web UI 或 API 导入
curl -X POST http://nas-ip:8080/api/import -F "file=@export.db"
```

---

## 与主服务的区别

| 特性 | relive 主服务 | relive-analyzer |
|------|--------------|----------------|
| 部署方式 | Web 服务器 | 命令行工具 |
| 依赖 | Gin、GORM、完整数据库 | 最小依赖、直接 SQL |
| 并发能力 | 单张实时分析 | 批量高并发 |
| 进度跟踪 | WebSocket 实时推送 | 终端进度条 |
| 断点续传 | 数据库状态 | 数据库状态 |
| 使用场景 | 在线实时服务 | 离线批量处理 |

---

## 开发与贡献

### 目录结构

```
backend/
├── cmd/
│   └── relive-analyzer/
│       └── main.go              # CLI 入口
├── internal/
│   └── analyzer/
│       ├── analyzer.go          # 核心分析逻辑
│       ├── worker_pool.go       # 并发控制
│       ├── progress.go          # 进度跟踪
│       └── stats.go             # 统计信息
└── configs/
    └── analyzer.yaml            # 配置文件
```

### 复用的模块

- `internal/provider/*` - 5 种 AI Provider 实现
- `internal/util/image.go` - 图片预处理
- `internal/util/exif.go` - EXIF 提取
- `pkg/config/` - 配置加载
- `pkg/logger/` - 日志系统

### 贡献指南

欢迎贡献代码！请遵循以下步骤：

1. Fork 项目
2. 创建功能分支
3. 提交代码
4. 创建 Pull Request

---

## 许可证

遵循 relive 项目主许可证。

---

## 支持

- **Issues**：[GitHub Issues](https://github.com/yourusername/relive/issues)
- **文档**：[项目 Wiki](https://github.com/yourusername/relive/wiki)
- **讨论**：[Discussions](https://github.com/yourusername/relive/discussions)
