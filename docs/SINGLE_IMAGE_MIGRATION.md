# 单镜像架构迁移指南

## 背景

从 v0.3.0 开始，Relive 从**双镜像架构**（relive-backend + relive-frontend）迁移到**单镜像架构**（relive），简化部署和维护。

## 变更说明

### 之前（双镜像）

```yaml
services:
  relive-backend:
    image: davidhoo/relive-backend:latest
    ports:
      - "8080:8080"  # 后端 API

  relive-frontend:
    image: davidhoo/relive-frontend:latest
    ports:
      - "8888:80"    # 前端页面
```

**问题**：
- ❌ 两个镜像需要独立管理
- ❌ 两个容器占用更多资源
- ❌ 需要维护 CORS 配置
- ❌ 端口配置复杂（8080 + 8888）

### 现在（单镜像）

```yaml
services:
  relive:
    image: davidhoo/relive:latest
    ports:
      - "8080:8080"  # 前端 + 后端 API
```

**优势**：
- ✅ 一个镜像搞定所有（`davidhoo/relive`）
- ✅ 一个容器运行（减少资源占用）
- ✅ 一个端口访问（简化配置）
- ✅ 无需 CORS 配置（同域访问）
- ✅ 部署超简单

## 技术实现

### 镜像构建

新的 `Dockerfile`（在项目根目录）：

```dockerfile
# Stage 1: 构建前端
FROM node:20-alpine AS frontend-builder
WORKDIR /frontend
COPY frontend/ ./
RUN npm ci && npm run build

# Stage 2: 构建后端
FROM golang:1.24-alpine AS backend-builder
WORKDIR /app
COPY backend/ ./
RUN CGO_ENABLED=1 GOOS=linux go build -o relive ./cmd/relive/main.go

# Stage 3: 运行阶段（包含前端 + 后端）
FROM alpine:latest
COPY --from=backend-builder /app/relive /app/relive
COPY --from=frontend-builder /frontend/dist /app/frontend/dist
CMD ["/app/relive"]
```

### 静态文件服务

Go 后端添加静态文件路由（`router/router.go`）：

```go
// 提供前端静态文件
if cfg.Server.StaticPath != "" {
    r.Static("/assets", cfg.Server.StaticPath+"/assets")
    r.StaticFile("/", cfg.Server.StaticPath+"/index.html")
    r.NoRoute(func(c *gin.Context) {
        c.File(cfg.Server.StaticPath + "/index.html")
    })
}
```

### 配置文件

`config.prod.yaml` 新增配置：

```yaml
server:
  static_path: "/app/frontend/dist"  # 前端静态文件路径
```

## 迁移步骤

### 方式 1：全新部署（推荐）

直接使用新的单镜像版本：

```bash
# 一键安装
curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/install.sh | bash

# 或手动部署
docker run -d \
  -p 8080:8080 \
  -v ./data:/app/data \
  -v ./config.prod.yaml:/app/config.yaml:ro \
  davidhoo/relive:latest
```

访问：http://your-nas-ip:8080

### 方式 2：从双镜像迁移

如果你已经部署了旧版本（双镜像），迁移步骤：

```bash
# 1. 停止并删除旧容器
docker-compose down

# 2. 备份数据（可选）
cp -r data data.backup

# 3. 下载新的 docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/docker-compose.prod.yml \
  -o docker-compose.yml

# 4. 启动新版本
docker-compose up -d

# 5. 验证
curl http://localhost:8080/api/v1/system/health
```

**数据兼容性**：
- ✅ 数据库格式不变（无需迁移）
- ✅ 配置文件兼容（只需添加 `static_path`）
- ✅ 照片数据完全兼容

### 方式 3：从源码构建

```bash
git clone https://github.com/davidhoo/relive.git
cd relive

# 使用新的根目录 Dockerfile
docker build -t relive:local .

# 运行
docker run -d -p 8080:8080 relive:local
```

## 多架构支持

单镜像同样支持多架构：

- ✅ linux/amd64（Intel/AMD x86_64）
- ✅ linux/arm64（Apple Silicon、ARM NAS）

Docker 会自动选择正确的架构版本。

## 访问地址变化

### 旧版（双镜像）

- 前端：http://your-nas-ip:8888
- 后端 API：http://your-nas-ip:8080/api/v1/...

### 新版（单镜像）

- 前端：http://your-nas-ip:8080
- 后端 API：http://your-nas-ip:8080/api/v1/...

**所有内容通过同一个端口访问**！

## 开发环境

开发环境不受影响，仍然可以独立运行：

```bash
# 后端（8080）
cd backend && go run cmd/relive/main.go --config config.dev.yaml

# 前端（5173）
cd frontend && npm run dev
```

`config.dev.yaml` 中设置 `static_path: ""`，后端不提供静态文件服务。

## FAQ

### Q: 旧版双镜像还能用吗？

A: 可以，但**不再维护**。建议迁移到单镜像版本。

### Q: 性能有影响吗？

A: **性能更好**！
- 减少了容器间网络开销
- 无需 CORS 预检请求
- 资源占用更少

### Q: 镜像大小如何？

A: **更小**！

- 双镜像：~50MB (backend) + ~25MB (frontend) = **75MB**
- 单镜像：~**55MB** total

（去除了 Nginx，复用了 Alpine 基础层）

### Q: ESP32 设备访问有变化吗？

A: **没有变化**！设备仍然访问 `/api/v1/display/photo` 等 API 端点。

### Q: 可以自定义前端吗？

A: 可以！两种方式：
1. 挂载自定义前端目录：`-v ./custom-frontend:/app/frontend/dist`
2. 重新构建镜像

## 相关文档

- [快速开始](QUICKSTART.md)
- [DockerHub 部署](DEPLOY_FROM_DOCKERHUB.md)
- [多架构支持](MULTIARCH.md)
- [Dockerfile](../Dockerfile)

---

**单镜像架构**：更简单、更高效、更易维护 ✨

**生效版本**：v0.3.0+
**最后更新**：2026-03-04
