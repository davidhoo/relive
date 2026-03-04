# 🏗️ 多架构支持说明

Relive 现已支持多架构 Docker 镜像，可在不同平台上无缝运行。

---

## ✅ 支持的架构

| 架构 | 平台 | 状态 |
|------|------|------|
| **linux/amd64** | Intel/AMD x86_64 | ✅ 完全支持 |
| **linux/arm64** | Apple Silicon, ARM NAS | ✅ 完全支持 |

### 适用设备

#### linux/amd64 (x86_64)
- 大部分 PC 和服务器
- Intel Mac (2020 年前)
- 群晖 DS920+, DS1621+ 等
- 威联通 TS-453D 等
- 大部分 NAS 设备

#### linux/arm64 (ARM)
- Apple Silicon Mac (M1/M2/M3)
- 群晖 DS220j, DS420j 等 ARM 型号
- 威联通 TS-230 等 ARM 型号
- Raspberry Pi 4/5
- 其他 ARM 服务器

---

## 🚀 使用方法

### 自动选择架构（推荐）

Docker 会自动根据你的平台选择正确的镜像：

```bash
# 一键安装（自动选择架构）
curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/install.sh | bash
```

### 手动指定架构

如需手动指定架构：

```bash
# 拉取 amd64 版本（Intel NAS）
docker pull --platform linux/amd64 davidhoo/relive-backend:latest

# 拉取 arm64 版本（Apple Silicon 或 ARM NAS）
docker pull --platform linux/arm64 davidhoo/relive-backend:latest
```

---

## 🧪 验证镜像

### 检查支持的架构

```bash
docker buildx imagetools inspect davidhoo/relive-backend:latest
```

**输出示例**：
```
Name:      docker.io/davidhoo/relive-backend:latest
MediaType: application/vnd.docker.distribution.manifest.list.v2+json

Manifests:
  Platform:  linux/amd64
  Digest:    sha256:abc123...

  Platform:  linux/arm64
  Digest:    sha256:def456...
```

### 测试运行

```bash
# 测试 amd64
docker run --rm --platform linux/amd64 davidhoo/relive-backend:latest /app/relive --version

# 测试 arm64
docker run --rm --platform linux/arm64 davidhoo/relive-backend:latest /app/relive --version
```

---

## 📊 性能对比

### 构建时间

| 类型 | 时间 | 说明 |
|------|------|------|
| 单架构 | ~5分钟 | 只构建一个平台 |
| 多架构 | ~10分钟 | 并行构建两个平台 |

### 运行性能

| 场景 | 性能 | 说明 |
|------|------|------|
| 原生架构 | 100% | 最佳性能 |
| QEMU 模拟 | ~30% | 仅用于测试 |

**结论**：
- ✅ 原生架构运行性能 100%，无损耗
- ⚠️ 跨架构模拟运行性能差，不推荐生产使用

---

## 📦 镜像大小

### 单平台镜像大小

| 组件 | amd64 | arm64 |
|------|-------|-------|
| 后端 | ~50MB | ~48MB |
| 前端 | ~25MB | ~25MB |

### Manifest List

多架构镜像使用 Manifest List：
- 包含两个平台的索引
- 拉取时只下载目标平台版本
- 实际磁盘占用 = 单平台大小

---

## 🔧 开发者指南

### 本地构建多架构镜像

```bash
# 使用自动化脚本
./build-multiarch.sh v0.3.0

# 或手动构建
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --tag davidhoo/relive-backend:v0.3.0 \
  --push \
  ./backend
```

### CI/CD 自动构建

GitHub Actions 会在创建 tag 时自动构建多架构镜像：

```bash
# 创建版本 tag
git tag v0.3.0
git push origin v0.3.0

# GitHub Actions 自动：
# 1. 构建 linux/amd64 和 linux/arm64
# 2. 推送到 DockerHub
# 3. 验证镜像
# 4. 创建 Release
```

详见 [多架构构建指南](MULTIARCH_BUILD.md)

---

## 🐛 常见问题

### Q1: 如何知道我的 NAS 是什么架构？

**方法 1：查看型号**
- DS920+, DS1621+, DS1823xs+ → amd64
- DS220j, DS420j, DS223 → arm64

**方法 2：SSH 到 NAS**
```bash
uname -m
# x86_64 → amd64
# aarch64 或 armv8 → arm64
```

### Q2: 在 Apple Silicon Mac 上构建的镜像能在 Intel NAS 上运行吗？

**能！** 使用多架构构建：
```bash
# 在 M1 Mac 上构建多架构镜像
./build-multiarch.sh

# 推送到 DockerHub 后，Intel NAS 自动拉取 amd64 版本
```

### Q3: 单架构镜像和多架构镜像如何选择？

| 场景 | 推荐 |
|------|------|
| 个人使用，固定设备 | 单架构 |
| 开源项目，公开发布 | 多架构 ✅ |
| 多设备部署 | 多架构 ✅ |

### Q4: 出现 "platform mismatch" 警告怎么办？

**警告示例**：
```
WARNING: The requested image's platform (linux/arm64)
does not match the detected host platform (linux/amd64)
```

**原因**：拉取的镜像架构与系统架构不匹配

**解决**：
1. 确认使用多架构镜像（检查 manifest）
2. 让 Docker 自动选择（不指定 --platform）
3. 如仍有问题，手动指定正确的平台

---

## 📚 相关文档

- [多架构构建详细指南](MULTIARCH_BUILD.md) - 开发者必读
- [DockerHub 部署指南](DEPLOY_FROM_DOCKERHUB.md) - 用户部署
- [Docker 发布指南](DOCKER_RELEASE.md) - 维护者发布

---

## 🎯 总结

### 对用户
- ✅ **无需关心架构** - 自动选择正确版本
- ✅ **一键安装** - `install.sh` 支持所有架构
- ✅ **性能最佳** - 原生架构运行

### 对开发者
- ✅ **一次构建，到处运行** - 多架构支持
- ✅ **自动化 CI/CD** - GitHub Actions 自动构建
- ✅ **开发友好** - 在任何平台开发和测试

---

**支持架构**：linux/amd64, linux/arm64
**最后更新**：2026-03-04
**版本**：v0.3.0+
