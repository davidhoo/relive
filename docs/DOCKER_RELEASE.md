# 📦 Docker 镜像发布指南

本文档说明如何构建和发布 Relive 的 Docker 镜像到 DockerHub。

---

## 🎯 镜像架构

### 两个独立镜像

1. **relive-backend** - 后端服务（Go）
   - 基础镜像：`golang:1.24-alpine` → `alpine:latest`
   - 包含：后端二进制、迁移脚本、配置文件
   - 大小：~50MB

2. **relive-frontend** - 前端服务（Vue + Nginx）
   - 基础镜像：`node:18-alpine` → `nginx:alpine`
   - 包含：前端静态文件、Nginx 配置
   - 大小：~25MB

---

## 🏗️ 构建镜像

### 方法 1：使用脚本（推荐）

```bash
# 创建构建脚本
cat > build-images.sh << 'EOF'
#!/bin/bash
set -e

VERSION=${1:-latest}

echo "构建版本: $VERSION"

# 构建后端
echo "构建后端镜像..."
cd backend
docker build -t davidhoo/relive-backend:$VERSION .
docker tag davidhoo/relive-backend:$VERSION davidhoo/relive-backend:latest
cd ..

# 构建前端
echo "构建前端镜像..."
cd frontend
docker build -t davidhoo/relive-frontend:$VERSION .
docker tag davidhoo/relive-frontend:$VERSION davidhoo/relive-frontend:latest
cd ..

echo "✓ 构建完成"
EOF

chmod +x build-images.sh

# 构建
./build-images.sh v0.3.0
```

### 方法 2：手动构建

**后端镜像**：
```bash
cd backend
docker build -t davidhoo/relive-backend:v0.3.0 .
docker tag davidhoo/relive-backend:v0.3.0 davidhoo/relive-backend:latest
```

**前端镜像**：
```bash
cd frontend
docker build -t davidhoo/relive-frontend:v0.3.0 .
docker tag davidhoo/relive-frontend:v0.3.0 davidhoo/relive-frontend:latest
```

---

## 📤 发布到 DockerHub

### 1. 登录 DockerHub

```bash
docker login
# 输入用户名和密码
```

### 2. 推送镜像

**推送脚本**：
```bash
cat > push-images.sh << 'EOF'
#!/bin/bash
set -e

VERSION=${1:-latest}

echo "推送版本: $VERSION"

# 推送后端
echo "推送后端镜像..."
docker push davidhoo/relive-backend:$VERSION
docker push davidhoo/relive-backend:latest

# 推送前端
echo "推送前端镜像..."
docker push davidhoo/relive-frontend:$VERSION
docker push davidhoo/relive-frontend:latest

echo "✓ 推送完成"
EOF

chmod +x push-images.sh

# 推送
./push-images.sh v0.3.0
```

**或手动推送**：
```bash
# 推送后端
docker push davidhoo/relive-backend:v0.3.0
docker push davidhoo/relive-backend:latest

# 推送前端
docker push davidhoo/relive-frontend:v0.3.0
docker push davidhoo/relive-frontend:latest
```

---

## 🔖 版本管理

### 语义化版本

遵循 [Semantic Versioning](https://semver.org/)：

- **Major (v1.0.0)**：破坏性更新
- **Minor (v0.3.0)**：新功能
- **Patch (v0.2.1)**：Bug 修复

### 标签策略

每个版本推送**两个标签**：

1. **版本号标签**：`v0.3.0`（固定，不变）
2. **latest 标签**：`latest`（指向最新稳定版）

**示例**：
```bash
# v0.3.0 发布
docker tag davidhoo/relive-backend:v0.3.0 davidhoo/relive-backend:latest
docker push davidhoo/relive-backend:v0.3.0
docker push davidhoo/relive-backend:latest
```

---

## 🚀 完整发布流程

### 发布新版本（例如 v0.3.0）

```bash
#!/bin/bash
# release.sh

VERSION="v0.3.0"

echo "==> 发布 Relive $VERSION"

# 1. 更新版本号
echo "1. 更新版本信息..."
# 更新 backend/cmd/relive/main.go 中的版本号
# 更新 frontend/package.json 中的版本号
# 更新 CHANGELOG.md

# 2. Git 提交和标签
echo "2. 创建 Git 标签..."
git add .
git commit -m "release: $VERSION"
git tag $VERSION
git push origin main
git push origin $VERSION

# 3. 构建镜像
echo "3. 构建 Docker 镜像..."
./build-images.sh $VERSION

# 4. 推送镜像
echo "4. 推送到 DockerHub..."
./push-images.sh $VERSION

# 5. 创建 GitHub Release
echo "5. 创建 GitHub Release..."
gh release create $VERSION \
  --title "Release $VERSION" \
  --notes-file CHANGELOG.md \
  --target main

echo "✓ 发布完成！"
echo "DockerHub: https://hub.docker.com/r/davidhoo/relive-backend"
echo "GitHub: https://github.com/davidhoo/relive/releases/tag/$VERSION"
```

---

## 🧪 测试镜像

### 本地测试

```bash
# 使用 docker-compose.prod.yml 测试
docker-compose -f docker-compose.prod.yml up -d

# 查看日志
docker-compose -f docker-compose.prod.yml logs -f

# 健康检查
curl http://localhost:8080/system/health

# 访问前端
open http://localhost:8888
```

### 多架构测试（可选）

如需支持 ARM（树莓派、Apple Silicon）：

```bash
# 安装 buildx
docker buildx create --name multiarch --use

# 构建多架构镜像
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t davidhoo/relive-backend:v0.3.0 \
  --push \
  ./backend
```

---

## 📋 DockerHub 页面配置

### 1. 仓库描述

**relive-backend**：
```
Relive - Intelligent Photo Memory Frame Backend

Smart photo management system with AI analysis, supporting multiple AI providers (Ollama/Qwen/OpenAI/VLLM).

Features:
- 📸 Photo scanning and EXIF extraction
- 🤖 AI-powered analysis (5 providers)
- 📅 "On This Day" display algorithm
- 🔌 Offline workflow support
- 🌐 RESTful API

Documentation: https://github.com/davidhoo/relive
```

**relive-frontend**：
```
Relive - Intelligent Photo Memory Frame Frontend

Modern web interface for managing photos and viewing AI analysis results.

Built with Vue 3 + TypeScript + Element Plus.

Documentation: https://github.com/davidhoo/relive
```

### 2. 添加 README

在 DockerHub 仓库设置中添加完整的 README.md。

### 3. 自动构建（可选）

在 DockerHub 中配置 GitHub 集成，实现自动构建：

1. 连接 GitHub 仓库
2. 配置构建规则：
   - `main` 分支 → `latest` 标签
   - `v*.*.*` 标签 → 对应版本标签

---

## 📊 镜像大小优化

### 当前大小

```bash
docker images | grep relive
# relive-backend   latest   50MB
# relive-frontend  latest   25MB
```

### 优化建议

1. **使用 Alpine 基础镜像** ✅（已使用）
2. **多阶段构建** ✅（已使用）
3. **删除不必要的文件**：
   ```dockerfile
   # 在构建阶段
   RUN go build ... && \
       rm -rf /go/pkg /go/src
   ```

4. **使用 .dockerignore**：
   ```
   .git
   .github
   node_modules
   *.md
   docs/
   ```

---

## 🔐 安全扫描

### 使用 Trivy 扫描漏洞

```bash
# 安装 Trivy
brew install aquasecurity/trivy/trivy

# 扫描镜像
trivy image davidhoo/relive-backend:latest
trivy image davidhoo/relive-frontend:latest
```

### CI/CD 集成

在 GitHub Actions 中添加安全扫描：

```yaml
# .github/workflows/docker-scan.yml
name: Docker Security Scan

on:
  push:
    tags:
      - 'v*'

jobs:
  scan:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3

      - name: Build image
        run: docker build -t relive-backend:test ./backend

      - name: Run Trivy scan
        uses: aquasecurity/trivy-action@master
        with:
          image-ref: relive-backend:test
          format: 'sarif'
          output: 'trivy-results.sarif'
```

---

## 📚 用户文档

### 安装说明（在 README.md 中）

```markdown
## 🐳 Docker 安装（推荐）

### 方式 1：一键安装（最简单）

```bash
curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/install.sh | bash
```

### 方式 2：使用 Docker Compose

1. 下载配置文件：
```bash
mkdir ~/relive && cd ~/relive
curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/docker-compose.prod.yml -o docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/backend/config.prod.yaml -o config.prod.yaml
```

2. 生成密钥并启动：
```bash
echo "JWT_SECRET=$(openssl rand -base64 32)" > .env
docker-compose up -d
```

### 方式 3：从源码构建

```bash
git clone https://github.com/davidhoo/relive.git
cd relive
./deploy.sh
```
```

---

## ✅ 发布检查清单

发布前确认：

- [ ] 版本号已更新（backend/main.go, frontend/package.json）
- [ ] CHANGELOG.md 已更新
- [ ] 本地测试通过
- [ ] Docker 镜像构建成功
- [ ] 镜像已推送到 DockerHub
- [ ] Git 标签已创建
- [ ] GitHub Release 已发布
- [ ] DockerHub 页面已更新
- [ ] 安全扫描无高危漏洞
- [ ] 文档已更新

---

## 🔄 回滚流程

如发现问题需要回滚：

```bash
# 1. 恢复 latest 标签到上一个稳定版本
docker pull davidhoo/relive-backend:v0.2.1
docker tag davidhoo/relive-backend:v0.2.1 davidhoo/relive-backend:latest
docker push davidhoo/relive-backend:latest

# 2. 通知用户
# 在 GitHub Release 中标记问题版本
# 发布补丁版本说明

# 3. 修复问题后发布新版本
```

---

## 📞 支持

- **DockerHub**: https://hub.docker.com/r/davidhoo/relive-backend
- **GitHub**: https://github.com/davidhoo/relive
- **Issues**: https://github.com/davidhoo/relive/issues

---

**最后更新**：2026-03-04
