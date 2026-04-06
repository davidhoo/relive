# People Worker API 模式使用指南

用于 Mac M4 等高性能设备离线执行人脸检测，将检测结果提交到 NAS 后端进行存储和聚类。

## 概述

People Worker 是一个独立的 CLI 工具，它：
- 从 NAS 后端获取待处理的照片任务
- 下载照片到本地
- 调用本地 `relive-ml` 服务进行人脸检测和特征提取
- 将检测结果提交回 NAS 后端
- 由 NAS 后端负责存储、缩略图生成和人物聚类

## 架构

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│   NAS Backend   │◄────┤  People Worker  │◄────┤   relive-ml     │
│   (SQLite)      │     │   (Mac M4)      │     │   (Local)       │
└─────────────────┘     └─────────────────┘     └─────────────────┘
        ▲                                              │
        │                                              │
        └────────────── 人脸检测结果 ◄──────────────────┘
```

## 前置条件

1. **NAS 后端已部署并运行**
   - 确保 NAS 上运行着 Relive 后端服务
   - 需要一个 API Key 用于认证

2. **本地运行 relive-ml 服务**
   ```bash
   # 在 Mac M4 上启动 ML 服务
   cd relive-ml
   python app.py --port 5050
   ```

3. **网络连通性**
   - Mac 可以访问 NAS 的 8080 端口
   - People Worker 可以访问本地的 5050 端口（relive-ml）

## 安装

### 从源码构建

```bash
# 克隆仓库
git clone https://github.com/davidhoo/relive.git
cd relive

# 构建 people-worker
make build-people-worker

# 二进制位于 backend/bin/relive-people-worker
```

### 直接运行（开发模式）

```bash
cd backend
go run ./cmd/relive-people-worker --help
```

## 配置

### 1. 生成示例配置

```bash
./backend/bin/relive-people-worker gen-config > people-worker.yaml
```

### 2. 编辑配置

```yaml
server:
  endpoint: "http://your-nas-ip:8080"  # NAS 地址
  api_key: "your-api-key"               # API Key

people_worker:
  worker_id: "mac-m4"                   # Worker 标识
  workers: 4                            # 并发数（M4 推荐 4-8）

ml:
  endpoint: "http://localhost:5050"     # 本地 ML 服务
```

### 3. 获取 API Key

在 NAS 的 Web UI 中：
1. 进入"设备管理"
2. 创建设备类型为 `offline` 的新设备
3. 复制生成的 API Key

## 使用

### 检查连接

在启动 Worker 前，先检查连接是否正常：

```bash
./backend/bin/relive-people-worker check -config people-worker.yaml
```

预期输出：
```
[INFO] Checking server connection...
[INFO] Server connection OK (lease expires at: ...)
[INFO] Checking ML service connection...
[INFO] ML service connection OK
[INFO] All checks passed!
```

### 启动 Worker

```bash
./backend/bin/relive-people-worker run -config people-worker.yaml
```

选项：
- `-workers N` - 覆盖配置中的并发数
- `-verbose` - 启用调试日志

示例：
```bash
# 使用 8 个并发 Worker
./backend/bin/relive-people-worker run -config people-worker.yaml -workers 8

# 调试模式
./backend/bin/relive-people-worker run -config people-worker.yaml -verbose
```

### 停止 Worker

按 `Ctrl+C` 进行优雅停止：
- 停止获取新任务
- 完成当前处理中的任务
- 释放运行时租约
- 清理临时文件

## 监控进度

### 命令行输出

Worker 会输出处理进度：
```
[INFO] Processing task 123 (photo 456)
[INFO] Task 123: detected 3 faces in 45ms
[INFO] Worker stopped. Processed: 100, Failed: 2, Faces detected: 285
```

### NAS Web UI

在 NAS 的 Web UI 中查看：
- **人物管理页面** - 查看处理队列和统计
- **照片管理页面** - 查看人脸检测状态

## 性能调优

### M4 Mac 推荐配置

```yaml
people_worker:
  workers: 4        # 4-8 之间测试最佳值
  fetch_limit: 10   # 每批获取任务数

ml:
  timeout: 15       # 根据 ML 服务性能调整
```

### 并发数建议

| 设备 | Workers | 预期吞吐量 |
|------|---------|-----------|
| M4 Mac (10-core) | 4-6 | ~100-150 张/分钟 |
| M4 Mac (14-core) | 6-8 | ~150-200 张/分钟 |
| M4 Max | 8-12 | ~200-300 张/分钟 |

## 故障排除

### 无法连接服务器

```
Error: server connection failed
```

检查：
1. NAS 后端是否运行
2. 配置中的 `server.endpoint` 是否正确
3. 防火墙是否允许访问

### 无法连接 ML 服务

```
Error: ML service connection failed
```

检查：
1. relive-ml 是否运行在配置的端口
2. `ml.endpoint` 是否正确

### API Key 无效

```
Error: unauthorized
```

检查：
1. API Key 是否正确复制
2. 设备是否被禁用

### 任务处理失败

查看日志：
```bash
./backend/bin/relive-people-worker run -config people-worker.yaml -verbose
```

常见原因：
- 下载失败：检查网络连接
- 检测失败：检查 relive-ml 服务状态
- 提交失败：检查 NAS 后端状态

## 与 Analyzer 的区别

| 特性 | Analyzer | People Worker |
|------|----------|---------------|
| 功能 | AI 照片分析 | 人脸检测 |
| 处理内容 | 描述、标签、评分 | 人脸位置、特征向量 |
| 后端依赖 | AI 服务 | ML 服务 (relive-ml) |
| 可以并行 | ✅ | ✅ |

可以同时运行 Analyzer 和 People Worker。

## 注意事项

1. **不要多个 People Worker 同时运行**
   - 运行时租约会阻止多个 Worker 同时执行
   - 如果需要更多并发，增加 `workers` 配置

2. **临时文件清理**
   - Worker 会自动清理下载的临时文件
   - 异常退出时可能需要手动清理 `~/.relive-people-worker/temp`

3. **网络中断恢复**
   - 网络中断时正在处理的任务会失败并重试
   - 恢复后 Worker 会自动继续

## 版本信息

```bash
./backend/bin/relive-people-worker version
```

兼容的 NAS 后端版本：v1.3.0+
