# Relive 部署指南

> 详细的部署步骤和配置说明
> 最后更新：2026-03-05
> 版本：v1.1

---

## 目录

- [一、部署架构](#一部署架构)
- [二、环境要求](#二环境要求)
- [三、NAS 部署](#三nas-部署)
- [四、relive-analyzer 部署](#四relive-analyzer-部署)
- [五、数据库初始化](#五数据库初始化)
- [六、配置说明](#六配置说明)
- [七、版本管理](#七版本管理)
- [八、AI 提供者配置](#八ai-提供者配置)
- [九、反向代理配置](#九反向代理配置)
- [十、监控和日志](#十监控和日志)
- [十一、故障排查](#十一故障排查)

---

## 一、部署架构

### 1.1 总体架构

```
┌─────────────────────────────────────────────────────┐
│  群晖 NAS                                            │
│                                                     │
│  ┌──────────────────────────────────────────────┐  │
│  │  Docker 容器                                  │  │
│  │                                               │  │
│  │  ┌──────────────┐       ┌─────────────────┐ │  │
│  │  │  Relive      │       │  Redis (可选)    │ │  │
│  │  │  Backend     │◄──────┤  缓存           │ │  │
│  │  │  :8080       │       └─────────────────┘ │  │
│  │  └──────┬───────┘                           │  │
│  │         │                                    │  │
│  │         ▼                                    │  │
│  │  ┌─────────────────┐                        │  │
│  │  │  SQLite         │                        │  │
│  │  │  relive.db      │                        │  │
│  │  └─────────────────┘                        │  │
│  │                                               │  │
│  └───────────────────────────────────────────────┘  │
│                                                     │
│  /volume1/photos/  ◄─ 照片目录                     │
│  /volume1/docker/relive/  ◄─ 数据目录              │
│                                                     │
└─────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────┐
│  任何电脑（分析工具）                                │
│                                                     │
│  ┌──────────────────────────────────────────────┐  │
│  │  relive-analyzer                             │  │
│  │                                               │  │
│  │  1. 通过 API 获取待分析照片列表              │  │
│  │  2. 调用 AI 服务（任意提供者）                │  │
│  │  3. 通过 API 提交分析结果                    │  │
│  └──────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────┐
│  AI 服务（多种选择）                                 │
│                                                     │
│  - Ollama（本地/远程）                              │
│  - Qwen API（阿里云）                               │
│  - OpenAI GPT-4V（OpenAI）                         │
│  - vLLM（自部署）                                   │
│  - 其他提供者                                       │
└─────────────────────────────────────────────────────┘
```

### 1.2 部署场景

**场景 1：NAS + 本地 Ollama**
```
NAS (Relive Backend) ◄───► 同一局域网 ◄───► PC (Ollama)
          │
          └─► 照片目录
```

**场景 2：NAS + 云端 GPU**
```
NAS (Relive Backend) ─┐
          │           │
          └─► 照片目录 │
                      │
笔记本 (relive-analyzer) ─► 互联网 ─► 云端 GPU (Ollama)
```

**场景 3：NAS + 在线 API**
```
NAS (Relive Backend) ─┐
          │           │
          └─► 照片目录 │
                      │
笔记本 (relive-analyzer) ─► 互联网 ─► Qwen/OpenAI API
```

---

## 二、环境要求

### 2.1 NAS 环境

**硬件要求**：
- CPU：双核及以上（推荐 Intel x86）
- 内存：4GB 以上
- 存储：根据照片数量，建议 500GB+
- 网络：千兆以太网

**软件要求**：
- 操作系统：群晖 DSM 7.0+（或其他 NAS 系统）
- Docker：Docker 20.10+
- Docker Compose：1.29+（可选）

**推荐配置**：
- 群晖 DS920+、DS1621+ 或更高型号
- 16GB 内存
- SSD 缓存（可选，提升性能）

### 2.2 分析工具环境

**硬件要求**：
- CPU：任何现代 CPU（分析工具本身不需要 GPU）
- 内存：4GB 以上
- 存储：50GB 以上（用于导出数据）

**软件要求**：
- 操作系统：Windows 10+、macOS 10.15+、Linux
- 网络：能访问 AI 服务（本地/局域网/互联网）

### 2.3 AI 服务环境

**本地 Ollama**：
- GPU：NVIDIA RTX 3060 及以上（12GB+ VRAM）
- 内存：16GB+ 系统内存
- 存储：50GB+（模型文件）

**云端 GPU**：
- RunPod、Vast.ai、AutoDL 等云 GPU 平台
- 按需付费，约 $0.3-0.5/小时

**在线 API**：
- 阿里云百炼平台（Qwen）
- OpenAI API
- 无硬件要求

---

## 三、NAS 部署

### 3.1 Docker 镜像构建

#### 方法 1：从源码构建

**克隆仓库**：
```bash
cd /volume1/docker/
git clone https://github.com/davidhoo/relive.git
cd relive
```

**构建镜像**：
```bash
docker build -t relive:latest -f Dockerfile .
```

**Dockerfile 示例**：
```dockerfile
# 多阶段构建
FROM golang:1.21-alpine AS builder

WORKDIR /app

# 复制依赖文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源码
COPY . .

# 构建
RUN CGO_ENABLED=1 GOOS=linux go build -o relive ./cmd/relive

# 运行时镜像
FROM alpine:latest

# 安装依赖
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

# 复制二进制文件
COPY --from=builder /app/relive .
COPY --from=builder /app/database/migrations ./database/migrations
COPY --from=builder /app/configs ./configs

# 设置时区
ENV TZ=Asia/Shanghai

# 暴露端口
EXPOSE 8080

# 启动命令
CMD ["./relive"]
```

#### 方法 2：使用预构建镜像（推荐）

```bash
docker pull davidhoo/relive:latest
```

### 3.2 Docker Compose 部署

> **注意**：从 v1.1 版本开始，使用 `docker-compose.yml.example` 模板文件。你需要先复制并修改配置。

**创建部署目录**：
```bash
mkdir -p /volume1/docker/relive
cd /volume1/docker/relive
```

**复制并编辑配置文件**：
```bash
# 从仓库复制模板
cp docker-compose.yml.example docker-compose.yml
cp docker-compose.prod.yml.example docker-compose.prod.yml  # 生产环境

# 编辑配置文件，修改照片路径等
nano docker-compose.yml
```

**创建 docker-compose.yml**（基于模板）：
```yaml
version: '3.8'

services:
  relive:
    image: davidhoo/relive:latest
    container_name: relive
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      # 数据目录
      - ./data:/app/data
      # 照片目录（只读）
      - /volume1/photos:/photos:ro
      # 配置文件
      - ./config.yaml:/app/config.yaml
      # 日志目录
      - ./logs:/app/logs
    environment:
      - TZ=Asia/Shanghai
      - GIN_MODE=release
    networks:
      - relive-network

  # Redis 缓存（可选）
  redis:
    image: redis:7-alpine
    container_name: relive-redis
    restart: unless-stopped
    ports:
      - "6379:6379"
    volumes:
      - ./redis-data:/data
    command: redis-server --appendonly yes
    networks:
      - relive-network

networks:
  relive-network:
    driver: bridge
```

**创建配置文件** `config.yaml`：
```yaml
# 服务配置
server:
  host: "0.0.0.0"
  port: 8080
  mode: "release"  # debug / release

# 数据库配置
database:
  type: "sqlite"
  path: "/app/data/relive.db"
  auto_migrate: true

# 照片目录
photos:
  root_path: "/photos"
  exclude_dirs:
    - ".sync"
    - "@eaDir"
    - "#recycle"
  supported_formats:
    - ".jpg"
    - ".jpeg"
    - ".png"
    - ".heic"

# AI 配置（初始为空，通过 Web 界面配置）
ai:
  provider: ""  # ollama / qwen / openai / vllm / hybrid
  timeout: 60

# 展示策略
display:
  algorithm: "on_this_day"
  fallback_days: [3, 7, 30, 365]
  avoid_repeat_days: 7

# 日志配置
logging:
  level: "info"  # debug / info / warn / error
  file: "/app/logs/relive.log"
  max_size: 100  # MB
  max_backups: 10
  max_age: 30  # days

# 安全配置
security:
  jwt_secret: "your-secret-key-change-this"  # 修改为随机字符串
  api_key_prefix: "sk-relive-"
```

**启动服务**：
```bash
docker-compose up -d
```

**查看日志**：
```bash
docker-compose logs -f relive
```

### 3.3 群晖 DSM 界面部署

#### 方法 1：使用 Docker 应用

1. 打开 DSM 控制面板
2. 进入 **套件中心** → 安装 **Docker**
3. 打开 **Docker** 应用
4. **注册表** → 搜索 `davidhoo/relive` → 下载
5. **映像** → 选择 `davidhoo/relive:latest` → 启动

#### 方法 2：使用 Container Manager（DSM 7.2+）

1. 打开 **Container Manager**
2. **项目** → **新增**
3. 上传 `docker-compose.yml`
4. 配置环境变量和卷映射
5. **应用** → 启动

### 3.4 验证部署

**检查服务状态**：
```bash
# 检查容器运行状态
docker ps | grep relive

# 检查日志
docker logs relive

# 测试 API
curl http://localhost:8080/api/v1/system/health
```

**预期响应**：
```json
{
  "success": true,
  "data": {
    "status": "healthy",
    "version": "0.1.0",
    "uptime": 3600
  }
}
```

---

## 四、relive-analyzer 部署

### 4.1 下载二进制文件

**Linux / macOS**：
```bash
# 下载
wget https://github.com/davidhoo/relive/releases/download/v0.1.0/relive-analyzer-linux-amd64

# 重命名
mv relive-analyzer-linux-amd64 relive-analyzer

# 添加执行权限
chmod +x relive-analyzer
```

**Windows**：
```powershell
# 下载
Invoke-WebRequest -Uri "https://github.com/davidhoo/relive/releases/download/v0.1.0/relive-analyzer-windows-amd64.exe" -OutFile "relive-analyzer.exe"
```

### 4.2 从源码编译

```bash
# 克隆仓库
git clone https://github.com/davidhoo/relive.git
cd relive/relive-analyzer

# 编译
go build -o relive-analyzer ./cmd/analyzer

# 查看版本
./relive-analyzer --version
```

### 4.3 配置文件

**创建配置文件** `analyzer-config.yaml`：
```yaml
# AI 提供者配置
provider: "ollama"  # ollama / qwen / openai / vllm / hybrid

# Ollama 配置
ollama:
  endpoint: "http://localhost:11434"  # 本地
  # endpoint: "http://192.168.1.100:11434"  # 局域网
  # endpoint: "https://xxx.runpod.io:11434"  # 云端
  model: "llava:13b"
  timeout: 120

# Qwen 配置
qwen:
  api_key: "sk-xxxxx"
  endpoint: "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
  model: "qwen-vl-max"
  timeout: 60

# OpenAI 配置
openai:
  api_key: "sk-xxxxx"
  endpoint: "https://api.openai.com/v1/chat/completions"
  model: "gpt-4-vision-preview"
  timeout: 60

# 混合模式配置
hybrid:
  primary: "ollama"      # 主提供者
  fallback: "qwen"       # 备用提供者
  retry_on_error: true

# 批量处理配置
batch:
  size: 10              # 每批处理数量
  workers: 4            # 并发 worker 数量
  retry_times: 3        # 失败重试次数

# 日志配置
logging:
  level: "info"
  file: "analyzer.log"
```

### 4.4 使用示例

**基本用法**：
```bash
# 分析导出的数据库
./relive-analyzer --input export.db --output import.db --config analyzer-config.yaml
```

**指定提供者**：
```bash
# 使用 Ollama
./relive-analyzer --input export.db --output import.db --provider ollama

# 使用 Qwen API
./relive-analyzer --input export.db --output import.db --provider qwen

# 使用混合模式
./relive-analyzer --input export.db --output import.db --provider hybrid
```

**查看进度**：
```bash
# 实时显示进度
./relive-analyzer --input export.db --output import.db --verbose

# 输出示例：
# [1/1000] 分析中... IMG_1234.jpg (10.2s)
# [2/1000] 分析中... IMG_1235.jpg (9.8s)
# ...
# 完成！成功：998，失败：2，耗时：2h 15m
```

**断点续传**：
```bash
# 自动跳过已分析的照片
./relive-analyzer --input export.db --output import.db --resume
```

---

## 五、数据库初始化

### 5.1 自动迁移（推荐）

**配置**：
```yaml
database:
  auto_migrate: true
```

**首次启动时自动创建表结构**。

### 5.2 手动迁移

**使用迁移脚本**：
```bash
# 进入容器
docker exec -it relive sh

# 执行迁移
./relive migrate up

# 查看迁移状态
./relive migrate status
```

### 5.3 导入城市数据

**城市数据库**（GeoNames）：
```bash
# 下载城市数据（~40MB）
wget https://download.geonames.org/export/dump/cities15000.zip
unzip cities15000.zip

# 导入数据库
docker exec -it relive ./relive import-cities --file cities15000.txt

# 验证
docker exec -it relive sh -c "sqlite3 /app/data/relive.db 'SELECT COUNT(*) FROM cities;'"
```

---

## 六、配置说明

### 6.1 配置文件位置

| 配置文件 | 位置 | 说明 |
|---------|------|------|
| **主配置** | `/volume1/docker/relive/config.yaml` | 后端服务配置 |
| **分析器配置** | `analyzer-config.yaml` | relive-analyzer 配置 |
| **环境变量** | `.env` | 敏感配置（API Key 等）|

### 6.2 环境变量

**创建 `.env` 文件**：
```bash
# JWT 密钥
JWT_SECRET=your-random-secret-key-change-this

# AI 提供者 API Keys
QWEN_API_KEY=sk-xxxxx
OPENAI_API_KEY=sk-xxxxx

# 数据库密码（如果使用 PostgreSQL）
DB_password=your-db-password

# Redis 密码（如果启用认证）
REDIS_password=your-redis-password
```

**在 docker-compose.yml 中引用**：
```yaml
services:
  relive:
    env_file:
      - .env
```

### 6.3 照片目录配置

**推荐目录结构**：
```
/volume1/photos/
├── 2020/
│   ├── 01/
│   │   ├── IMG_0001.jpg
│   │   └── IMG_0002.jpg
│   └── 02/
├── 2021/
├── 2022/
├── 2023/
├── 2024/
├── 2025/
└── 2026/
```

**排除目录**：
```yaml
photos:
  exclude_dirs:
    - ".sync"          # Synology 同步目录
    - "@eaDir"         # 群晖缩略图
    - "#recycle"       # 回收站
    - ".DS_Store"      # macOS 系统文件
    - "Thumbs.db"      # Windows 系统文件
```

---

## 七、版本管理

### 7.1 统一版本号

Relive 使用单一版本号管理所有组件：

**版本文件**：`VERSION`（位于项目根目录）
- 格式：`MAJOR.MINOR.PATCH`（如 `1.0.0`）
- 这是唯一的版本来源

**各组件版本获取**：
- **Go 后端**：通过 `//go:embed` 嵌入 VERSION 文件
- **前端**：Vite 构建时读取 VERSION 并注入 `__APP_VERSION__`
- **relive-analyzer**：与主程序共享相同版本

### 7.2 查看版本

**通过 API**：
```bash
curl http://localhost:8080/api/v1/system/health
# 返回中包含 "version": "1.0.0"
```

**通过前端**：
- 在 Web 界面底部查看版本信息

**通过命令行**：
```bash
./relive-analyzer version
```

### 7.3 版本升级

**升级前准备**：
```bash
# 1. 查看当前版本
curl http://localhost:8080/api/v1/system/health | jq .data.version

# 2. 备份数据
./backup.sh
```

**升级步骤**：
```bash
# 1. 拉取新镜像
docker-compose pull

# 2. 停止服务
docker-compose down

# 3. 启动新版本
docker-compose up -d

# 4. 验证版本
curl http://localhost:8080/api/v1/system/health | jq .data.version
```

---

## 八、AI 提供者配置

### 8.1 Ollama 配置

**本地部署 Ollama**：
```bash
# 安装 Ollama
curl -fsSL https://ollama.ai/install.sh | sh

# 拉取模型
ollama pull llava:13b

# 启动服务
ollama serve
```

**配置**：
```yaml
ai:
  provider: "ollama"
  ollama:
    endpoint: "http://localhost:11434"
    model: "llava:13b"
```

### 8.2 Qwen API 配置

**获取 API Key**：
1. 访问 [阿里云百炼平台](https://bailian.console.aliyun.com/)
2. 创建 API Key
3. 充值余额

**配置**：
```yaml
ai:
  provider: "qwen"
  qwen:
    api_key: "${QWEN_API_KEY}"  # 从环境变量读取
    model: "qwen-vl-max"
```

### 8.3 OpenAI 配置

**获取 API Key**：
1. 访问 [OpenAI Platform](https://platform.openai.com/)
2. 创建 API Key
3. 充值余额

**配置**：
```yaml
ai:
  provider: "openai"
  openai:
    api_key: "${OPENAI_API_KEY}"
    model: "gpt-4-vision-preview"
```

### 8.4 混合模式配置

**配置多个提供者**：
```yaml
ai:
  provider: "hybrid"
  hybrid:
    primary: "ollama"       # 主提供者（优先）
    fallback: "qwen"        # 备用提供者
    retry_on_error: true    # 失败时切换

  # 各提供者配置
  ollama:
    endpoint: "http://localhost:11434"
    model: "llava:13b"

  qwen:
    api_key: "${QWEN_API_KEY}"
    model: "qwen-vl-max"
```

---

## 九、反向代理配置

### 9.1 Nginx 配置

**场景**：通过 HTTPS 访问 Relive

**配置文件** `/etc/nginx/sites-available/relive`：
```nginx
server {
    listen 443 ssl http2;
    server_name relive.example.com;

    # SSL 证书
    ssl_certificate /etc/nginx/ssl/relive.crt;
    ssl_certificate_key /etc/nginx/ssl/relive.key;

    # SSL 配置
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    # 反向代理
    location / {
        proxy_pass http://192.168.1.100:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket 支持（如果需要）
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }

    # 图片下载优化
    location /api/v1/esp32/image/ {
        proxy_pass http://192.168.1.100:8080;
        proxy_buffering on;
        proxy_buffer_size 4k;
        proxy_buffers 8 4k;
        proxy_busy_buffers_size 8k;
    }

    # 访问日志
    access_log /var/log/nginx/relive-access.log;
    error_log /var/log/nginx/relive-error.log;
}

# HTTP 重定向到 HTTPS
server {
    listen 80;
    server_name relive.example.com;
    return 301 https://$server_name$request_uri;
}
```

### 9.2 群晖 反向代理

**DSM 控制面板配置**：

1. 打开 **控制面板** → **登录门户** → **高级** → **反向代理服务器**
2. 点击 **新增**
3. 配置：
   - 来源：
     - 协议：HTTPS
     - 主机名：relive.example.com
     - 端口：443
   - 目的地：
     - 协议：HTTP
     - 主机名：localhost
     - 端口：8080
4. 启用 **WebSocket**
5. 保存

---

## 十、监控和日志

### 10.1 日志查看

**Docker 日志**：
```bash
# 实时查看日志
docker logs -f relive

# 查看最近 100 行
docker logs --tail 100 relive

# 查看特定时间范围
docker logs --since "2026-02-28T10:00:00" relive
```

**日志文件**：
```bash
# 查看应用日志
tail -f /volume1/docker/relive/logs/relive.log

# 搜索错误
grep "ERROR" /volume1/docker/relive/logs/relive.log
```

### 10.2 性能监控

**系统资源**：
```bash
# 查看容器资源使用
docker stats relive

# 输出示例：
# NAME    CPU %   MEM USAGE / LIMIT     MEM %   NET I/O
# relive  5.0%    256MiB / 4GiB         6.4%    1.2MB / 3.4MB
```

**数据库大小**：
```bash
# 查看数据库文件大小
du -h /volume1/docker/relive/data/relive.db

# 查看表大小
sqlite3 /volume1/docker/relive/data/relive.db "
SELECT
  name,
  COUNT(*) as count,
  pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) AS size
FROM pg_tables
WHERE schemaname = 'public'
GROUP BY name;
"
```

### 10.3 健康检查

**API 端点**：
```bash
# 系统健康检查
curl http://localhost:8080/api/v1/system/health

# 系统统计
curl http://localhost:8080/api/v1/system/stats
```

**Docker 健康检查**（在 docker-compose.yml 中添加）：
```yaml
services:
  relive:
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/api/v1/system/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s
```

---

## 十一、故障排查

### 11.1 常见问题

#### 问题 1：容器无法启动

**症状**：
```bash
docker ps  # 看不到 relive 容器
```

**排查步骤**：
```bash
# 查看容器日志
docker logs relive

# 检查端口占用
netstat -tlnp | grep 8080

# 检查配置文件
docker exec relive cat /app/config.yaml
```

**可能原因**：
- 端口 8080 被占用 → 修改端口
- 配置文件错误 → 检查 YAML 语法
- 权限问题 → 检查卷映射权限

#### 问题 2：无法访问照片

**症状**：
```
扫描照片时报错：permission denied
```

**解决方案**：
```bash
# 检查权限
ls -la /volume1/photos

# 修改容器用户（docker-compose.yml）
services:
  relive:
    user: "1000:1000"  # 使用 NAS 用户 UID/GID
```

#### 问题 3：AI 分析失败

**症状**：
```
AI 分析队列显示失败
```

**排查步骤**：
```bash
# 检查 AI 提供者配置
curl http://localhost:8080/api/v1/config

# 测试 Ollama 连接
curl http://localhost:11434/api/tags

# 测试 Qwen API
curl -H "Authorization: Bearer $QWEN_API_KEY" \
     https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation
```

**可能原因**：
- API Key 错误 → 检查环境变量
- 网络不通 → 测试连接
- 模型不存在 → 拉取模型

#### 问题 4：数据库损坏

**症状**：
```
database disk image is malformed
```

**解决方案**：
```bash
# 备份数据库
cp /volume1/docker/relive/data/relive.db /volume1/docker/relive/data/relive.db.backup

# 尝试修复
sqlite3 /volume1/docker/relive/data/relive.db "PRAGMA integrity_check;"

# 导出并重建
sqlite3 /volume1/docker/relive/data/relive.db .dump > backup.sql
sqlite3 /volume1/docker/relive/data/relive_new.db < backup.sql
```

### 11.2 日志级别

**调整日志级别**（config.yaml）：
```yaml
logging:
  level: "debug"  # 临时启用 debug 日志
```

**重启服务**：
```bash
docker-compose restart relive
```

### 11.3 性能问题

**症状**：
- 扫描照片很慢
- API 响应慢
- 内存占用高

**优化方案**：
```yaml
# 增加 worker 数量
scanner:
  workers: 8  # 根据 CPU 核心数调整

# 启用缓存
cache:
  enabled: true
  type: "redis"
  redis:
    addr: "redis:6379"

# 调整数据库连接池
database:
  max_open_conns: 25
  max_idle_conns: 5
```

---

## 十二、备份和恢复

### 12.1 数据备份

**备份脚本** `backup.sh`：
```bash
#!/bin/bash

BACKUP_DIR="/volume1/docker/relive/backups"
DATE=$(date +%Y%m%d_%H%M%S)

# 创建备份目录
mkdir -p $BACKUP_DIR

# 备份数据库
cp /volume1/docker/relive/data/relive.db \
   $BACKUP_DIR/relive_$DATE.db

# 备份配置
cp /volume1/docker/relive/config.yaml \
   $BACKUP_DIR/config_$DATE.yaml

# 压缩
tar -czf $BACKUP_DIR/relive_backup_$DATE.tar.gz \
    $BACKUP_DIR/relive_$DATE.db \
    $BACKUP_DIR/config_$DATE.yaml

# 删除 7 天前的备份
find $BACKUP_DIR -name "relive_backup_*.tar.gz" -mtime +7 -delete

echo "备份完成：$BACKUP_DIR/relive_backup_$DATE.tar.gz"
```

**设置定时任务**（群晖 DSM）：
1. **控制面板** → **任务计划** → **新增** → **计划的任务** → **用户定义的脚本**
2. 设置每天凌晨 2:00 执行
3. 用户定义的脚本：`/volume1/docker/relive/backup.sh`

### 12.2 数据恢复

```bash
# 解压备份
tar -xzf relive_backup_20260228_020000.tar.gz

# 停止服务
docker-compose down

# 恢复数据库
cp backups/relive_20260228_020000.db data/relive.db

# 恢复配置
cp backups/config_20260228_020000.yaml config.yaml

# 启动服务
docker-compose up -d
```

---

## 十三、升级指南

### 13.1 升级准备

**备份数据**：
```bash
./backup.sh
```

**查看更新日志**：
```bash
# 查看最新版本
curl -s https://api.github.com/repos/davidhoo/relive/releases/latest | grep tag_name

# 阅读 CHANGELOG
curl -s https://raw.githubusercontent.com/davidhoo/relive/main/CHANGELOG.md
```

### 13.2 升级步骤

**拉取新镜像**：
```bash
docker pull davidhoo/relive:latest
```

**停止服务**：
```bash
docker-compose down
```

**启动新版本**：
```bash
docker-compose up -d
```

**验证**：
```bash
# 检查版本
curl http://localhost:8080/api/v1/system/health | jq .data.version

# 检查日志
docker logs -f relive
```

### 13.3 回滚

**如果升级失败**：
```bash
# 停止服务
docker-compose down

# 使用旧版本镜像
docker-compose up -d davidhoo/relive:v0.0.9

# 恢复备份数据
cp backups/relive_backup_latest.db data/relive.db
```

---

## 十四、总结

### 14.1 部署检查清单

- [ ] NAS 环境满足要求
- [ ] Docker 和 Docker Compose 已安装
- [ ] 照片目录已准备
- [ ] 配置文件已创建
- [ ] 容器成功启动
- [ ] API 健康检查通过
- [ ] 数据库初始化完成
- [ ] 城市数据已导入
- [ ] relive-analyzer 可正常运行
- [ ] AI 提供者配置完成
- [ ] ESP32 设备可连接
- [ ] 备份脚本已设置

### 14.2 常用命令

```bash
# 启动服务
docker-compose up -d

# 停止服务
docker-compose down

# 重启服务
docker-compose restart

# 查看日志
docker-compose logs -f

# 进入容器
docker exec -it relive sh

# 备份数据
./backup.sh

# 升级服务
docker-compose pull && docker-compose up -d
```

---

**部署指南完成** ✅
**准备生产部署** 🚀
