#!/bin/bash

# Relive 多架构镜像构建脚本
# 支持：linux/amd64 (Intel x86), linux/arm64 (Apple Silicon, ARM NAS)

set -e

VERSION=${1:-latest}

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║   🏗️  Relive 多架构镜像构建工具           ║"
echo "╚════════════════════════════════════════════╝"
echo ""

echo -e "${BLUE}版本：${NC}$VERSION"
echo -e "${BLUE}架构：${NC}linux/amd64, linux/arm64"
echo ""

# ============================================
# 1. 检查环境
# ============================================

echo -e "${BLUE}[1/5]${NC} 检查构建环境..."

# 检查 Docker
if ! command -v docker &> /dev/null; then
    echo -e "${RED}❌ Docker 未安装${NC}"
    exit 1
fi
echo -e "${GREEN}  ✓${NC} Docker 已安装"

# 检查 buildx
if ! docker buildx version &> /dev/null; then
    echo -e "${RED}❌ Docker Buildx 未安装${NC}"
    echo "请升级 Docker 到 19.03 或更高版本"
    exit 1
fi
echo -e "${GREEN}  ✓${NC} Docker Buildx 已安装"

echo ""

# ============================================
# 2. 创建 buildx builder
# ============================================

echo -e "${BLUE}[2/5]${NC} 配置构建器..."

# 检查是否已存在 builder
if docker buildx ls | grep -q "relive-builder"; then
    echo -e "${YELLOW}  构建器已存在，使用现有构建器${NC}"
    docker buildx use relive-builder
else
    echo "  创建多架构构建器..."
    docker buildx create --name relive-builder --use --bootstrap
    echo -e "${GREEN}  ✓${NC} 构建器创建成功"
fi

# 检查 builder 状态
echo "  检查构建器状态..."
docker buildx inspect --bootstrap

echo ""

# ============================================
# 3. 构建后端镜像
# ============================================

echo -e "${BLUE}[3/5]${NC} 构建后端镜像（多架构）..."

cd backend

echo "  构建 davidhoo/relive-backend:$VERSION"
echo "  架构：linux/amd64, linux/arm64"

# 构建并推送（多架构镜像必须推送，不能 load 到本地）
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --tag davidhoo/relive-backend:$VERSION \
  --tag davidhoo/relive-backend:latest \
  --push \
  .

if [ $? -eq 0 ]; then
    echo -e "${GREEN}  ✓${NC} 后端镜像构建成功"
else
    echo -e "${RED}  ❌ 后端镜像构建失败${NC}"
    exit 1
fi

cd ..

echo ""

# ============================================
# 4. 构建前端镜像
# ============================================

echo -e "${BLUE}[4/5]${NC} 构建前端镜像（多架构）..."

cd frontend

echo "  构建 davidhoo/relive-frontend:$VERSION"
echo "  架构：linux/amd64, linux/arm64"

docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --tag davidhoo/relive-frontend:$VERSION \
  --tag davidhoo/relive-frontend:latest \
  --push \
  .

if [ $? -eq 0 ]; then
    echo -e "${GREEN}  ✓${NC} 前端镜像构建成功"
else
    echo -e "${RED}  ❌ 前端镜像构建失败${NC}"
    exit 1
fi

cd ..

echo ""

# ============================================
# 5. 验证镜像
# ============================================

echo -e "${BLUE}[5/5]${NC} 验证镜像..."

echo ""
echo "  后端镜像信息："
docker buildx imagetools inspect davidhoo/relive-backend:$VERSION

echo ""
echo "  前端镜像信息："
docker buildx imagetools inspect davidhoo/relive-frontend:$VERSION

echo ""

# ============================================
# 构建完成
# ============================================

echo "╔════════════════════════════════════════════╗"
echo "║   ✅ 构建完成！                           ║"
echo "╚════════════════════════════════════════════╝"
echo ""

echo "📦 已推送镜像："
echo "   davidhoo/relive-backend:$VERSION"
echo "   davidhoo/relive-backend:latest"
echo "   davidhoo/relive-frontend:$VERSION"
echo "   davidhoo/relive-frontend:latest"
echo ""

echo "🏗️  支持架构："
echo "   ✓ linux/amd64 (Intel/AMD x86_64)"
echo "   ✓ linux/arm64 (Apple Silicon, ARM NAS)"
echo ""

echo "🧪 测试命令："
echo "   # 在 Intel Mac/Linux 上测试"
echo "   docker pull --platform linux/amd64 davidhoo/relive-backend:$VERSION"
echo ""
echo "   # 在 Apple Silicon Mac 上测试"
echo "   docker pull --platform linux/arm64 davidhoo/relive-backend:$VERSION"
echo ""
echo "   # 在 NAS 上自动选择架构"
echo "   docker pull davidhoo/relive-backend:$VERSION"
echo ""

echo "📝 下一步："
echo "   1. 在不同架构的设备上测试镜像"
echo "   2. 更新 GitHub Release"
echo "   3. 更新文档说明多架构支持"
echo ""

echo "💡 提示："
echo "   - 多架构镜像会自动根据目标平台选择正确的版本"
echo "   - 镜像大小会略大（包含两个架构）"
echo "   - 如需支持更多架构，修改 --platform 参数"
echo ""

# ============================================
# 清理（可选）
# ============================================

read -p "是否删除本地构建器？[y/N] " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    docker buildx rm relive-builder
    echo -e "${GREEN}✓ 构建器已删除${NC}"
else
    echo -e "${YELLOW}保留构建器供下次使用${NC}"
fi

echo ""
echo "🎉 构建和发布完成！"
echo ""
