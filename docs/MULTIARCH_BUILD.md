# 🏗️ 多架构镜像构建指南

本文档说明如何构建和测试支持多架构的 Docker 镜像。

---

## 🎯 为什么需要多架构支持？

### 常见的架构场景

| 设备 | 架构 | 示例 |
|------|------|------|
| Intel/AMD PC | `linux/amd64` | 大部分 PC、服务器、老款 Mac |
| Apple Silicon Mac | `linux/arm64` | M1/M2/M3 Mac |
| ARM NAS | `linux/arm64` | 部分群晖、威联通 NAS |
| 树莓派 | `linux/arm64` | Raspberry Pi 4/5 |

### 问题示例

**在 Apple Silicon Mac 上构建镜像**：
```bash
# 默认会构建 ARM64 镜像
docker build -t my-app .

# 推送到 DockerHub
docker push my-app

# 在 Intel NAS 上拉取会失败 ❌
# WARNING: The requested image's platform (linux/arm64)
# does not match the detected host platform (linux/amd64)
```

### 解决方案：多架构镜像

使用 Docker Buildx 构建多架构镜像，Docker 会自动根据目标平台选择正确的版本。

---

## 🚀 快速开始

### 一键构建（推荐）

```bash
# 构建并推送多架构镜像
./build-multiarch.sh v0.3.0
```

这会：
- ✅ 创建 buildx builder
- ✅ 构建 `linux/amd64` 和 `linux/arm64` 镜像
- ✅ 推送到 DockerHub
- ✅ 验证镜像

---

## 📖 详细步骤

### 1. 检查环境

**检查 Docker 版本**：
```bash
docker version
# 需要 Docker 19.03 或更高版本
```

**检查 Buildx**：
```bash
docker buildx version
# buildx v0.10.0+
```

**如果没有 Buildx**（Docker < 20.10）：
```bash
# macOS
brew install docker-buildx

# Linux
DOCKER_BUILDKIT=1 docker build --platform=local -o . "https://github.com/docker/buildx.git"
mkdir -p ~/.docker/cli-plugins
mv buildx ~/.docker/cli-plugins/docker-buildx
chmod +x ~/.docker/cli-plugins/docker-buildx
```

### 2. 创建 Builder

```bash
# 创建新的 builder 实例
docker buildx create --name relive-builder --use --bootstrap

# 检查 builder
docker buildx ls

# 输出示例：
# NAME/NODE           DRIVER/ENDPOINT STATUS  PLATFORMS
# relive-builder *    docker-container
#   relive-builder0   unix:///...     running linux/amd64, linux/arm64
```

### 3. 构建多架构镜像

**后端镜像**：
```bash
cd backend

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --tag davidhoo/relive-backend:v0.3.0 \
  --tag davidhoo/relive-backend:latest \
  --push \
  .
```

**前端镜像**：
```bash
cd frontend

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --tag davidhoo/relive-frontend:v0.3.0 \
  --tag davidhoo/relive-frontend:latest \
  --push \
  .
```

**关键参数说明**：
- `--platform` - 指定目标架构（逗号分隔）
- `--push` - 直接推送到 Registry（多架构镜像不能 load 到本地）
- `--tag` - 镜像标签

### 4. 验证镜像

**查看镜像 manifest**：
```bash
docker buildx imagetools inspect davidhoo/relive-backend:v0.3.0
```

**输出示例**：
```
Name:      docker.io/davidhoo/relive-backend:v0.3.0
MediaType: application/vnd.docker.distribution.manifest.list.v2+json
Digest:    sha256:abc123...

Manifests:
  Name:      docker.io/davidhoo/relive-backend:v0.3.0@sha256:def456...
  MediaType: application/vnd.docker.distribution.manifest.v2+json
  Platform:  linux/amd64

  Name:      docker.io/davidhoo/relive-backend:v0.3.0@sha256:ghi789...
  MediaType: application/vnd.docker.distribution.manifest.v2+json
  Platform:  linux/arm64
```

---

## 🧪 测试

### 在不同平台测试

**在 Intel Mac/Linux**：
```bash
# 强制拉取 amd64 版本
docker pull --platform linux/amd64 davidhoo/relive-backend:v0.3.0

# 运行测试
docker run --rm --platform linux/amd64 davidhoo/relive-backend:v0.3.0 /app/relive --version
```

**在 Apple Silicon Mac**：
```bash
# 强制拉取 arm64 版本
docker pull --platform linux/arm64 davidhoo/relive-backend:v0.3.0

# 运行测试
docker run --rm --platform linux/arm64 davidhoo/relive-backend:v0.3.0 /app/relive --version
```

**自动选择架构**：
```bash
# Docker 会自动选择匹配的架构
docker pull davidhoo/relive-backend:v0.3.0

# 查看实际架构
docker inspect davidhoo/relive-backend:v0.3.0 | grep Architecture
```

### 完整功能测试

```bash
# 使用 docker-compose 测试
docker-compose -f docker-compose.prod.yml up -d

# 检查容器运行
docker ps

# 测试 API
curl http://localhost:8080/system/health

# 查看日志
docker-compose logs -f
```

---

## 🔧 Dockerfile 优化

### CGO 编译（Go 项目）

**问题**：使用 CGO 时，需要针对不同架构安装编译工具。

**当前 Dockerfile** 已支持多架构：
```dockerfile
FROM golang:1.24-alpine AS backend-builder

WORKDIR /app

# 安装构建依赖（支持交叉编译）
RUN apk add --no-cache gcc g++ musl-dev sqlite-dev

# 构建时 Docker 会自动设置 TARGETPLATFORM
RUN CGO_ENABLED=1 GOOS=linux go build \
    -o relive \
    ./cmd/relive/main.go
```

### Node.js 项目（前端）

**前端 Dockerfile** 已支持多架构：
```dockerfile
FROM node:20-alpine AS frontend-builder

WORKDIR /app

COPY package*.json ./
RUN npm ci --legacy-peer-deps

COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=frontend-builder /app/dist /usr/share/nginx/html
```

**Node.js 镜像说明**：
- `node:20-alpine` 官方镜像已支持多架构
- `nginx:alpine` 官方镜像已支持多架构
- 无需额外配置

---

## 📊 镜像大小对比

### 单架构 vs 多架构

| 类型 | amd64 | arm64 | 多架构 |
|------|-------|-------|--------|
| 后端 | ~50MB | ~48MB | ~98MB（manifest list）|
| 前端 | ~25MB | ~25MB | ~50MB（manifest list）|

**说明**：
- 多架构镜像使用 manifest list，包含多个平台的镜像
- 拉取时只下载目标平台的版本，不会下载全部
- 实际磁盘占用 = 单架构大小

---

## 🚨 常见问题

### 1. Buildx 构建失败

**错误**：
```
ERROR: failed to solve: process "/bin/sh -c ..." did not complete successfully
```

**解决**：
```bash
# 清理 builder cache
docker buildx prune -af

# 重新创建 builder
docker buildx rm relive-builder
docker buildx create --name relive-builder --use --bootstrap
```

### 2. 无法推送镜像

**错误**：
```
ERROR: failed to push: unauthorized
```

**解决**：
```bash
# 登录 DockerHub
docker login

# 确认登录状态
docker info | grep Username
```

### 3. 平台不匹配警告

**警告**：
```
WARNING: The requested image's platform (linux/arm64) does not match
the detected host platform (linux/amd64)
```

**这是正常的**：
- Docker 会尝试运行镜像（通过 QEMU 模拟）
- 性能较差，但可用于测试
- 生产环境会自动选择匹配的架构

### 4. CGO 编译失败

**错误**：
```
# runtime/cgo
gcc: error: unrecognized command line option '-marm'
```

**解决**：确保 Dockerfile 中包含正确的构建工具：
```dockerfile
RUN apk add --no-cache gcc g++ musl-dev
```

---

## 🔄 CI/CD 集成

### GitHub Actions 示例

```yaml
name: Build Multi-arch Images

on:
  push:
    tags:
      - 'v*'

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Set up QEMU
        uses: docker/setup-qemu-action@v2

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v2

      - name: Login to DockerHub
        uses: docker/login-action@v2
        with:
          username: ${{ secrets.DOCKERHUB_USERNAME }}
          password: ${{ secrets.DOCKERHUB_TOKEN }}

      - name: Build and push backend
        uses: docker/build-push-action@v4
        with:
          context: ./backend
          platforms: linux/amd64,linux/arm64
          push: true
          tags: |
            davidhoo/relive-backend:${{ github.ref_name }}
            davidhoo/relive-backend:latest

      - name: Build and push frontend
        uses: docker/build-push-action@v4
        with:
          context: ./frontend
          platforms: linux/amd64,linux/arm64
          push: true
          tags: |
            davidhoo/relive-frontend:${{ github.ref_name }}
            davidhoo/relive-frontend:latest
```

---

## 📈 性能对比

### 构建时间

| 架构 | 后端 | 前端 | 总计 |
|------|------|------|------|
| 单架构 (amd64) | ~3分钟 | ~2分钟 | ~5分钟 |
| 多架构 (amd64+arm64) | ~6分钟 | ~4分钟 | ~10分钟 |

**说明**：多架构构建时间约为单架构的 2 倍。

### 运行性能

| 场景 | 性能 | 说明 |
|------|------|------|
| 原生架构运行 | 100% | 最佳性能 |
| QEMU 模拟运行 | ~30% | 仅用于测试 |

---

## 🎯 最佳实践

### 1. 使用 manifest list

✅ **推荐**：构建多架构镜像
```bash
docker buildx build --platform linux/amd64,linux/arm64 --push .
```

❌ **不推荐**：分别构建单架构镜像
```bash
docker build --platform linux/amd64 -t app:amd64 .
docker build --platform linux/arm64 -t app:arm64 .
```

### 2. 测试所有架构

```bash
# 在 CI/CD 中测试多个架构
docker run --rm --platform linux/amd64 app:latest /app/test
docker run --rm --platform linux/arm64 app:latest /app/test
```

### 3. 文档说明

在 README 中明确支持的架构：

```markdown
## 支持的平台

- ✅ linux/amd64 (Intel/AMD x86_64)
- ✅ linux/arm64 (Apple Silicon, ARM NAS, Raspberry Pi)
```

### 4. 标签管理

```bash
# 为每个版本推送多架构镜像
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --tag app:v0.3.0 \
  --tag app:latest \
  --push \
  .
```

---

## 📚 参考资源

- [Docker Buildx 文档](https://docs.docker.com/buildx/working-with-buildx/)
- [Multi-platform images](https://docs.docker.com/build/building/multi-platform/)
- [Go 交叉编译](https://golang.org/doc/install/source#environment)

---

## ✅ 构建检查清单

发布前确认：

- [ ] 支持 linux/amd64 和 linux/arm64
- [ ] 在两个架构上测试通过
- [ ] 镜像大小合理（< 100MB）
- [ ] 推送到 DockerHub 成功
- [ ] Manifest list 正确
- [ ] 文档已更新
- [ ] CI/CD 配置完成

---

**最后更新**：2026-03-04
