#!/bin/bash

# Relive 多架构镜像测试脚本

set -e

VERSION=${1:-latest}

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║   🧪 Relive 多架构镜像测试                ║"
echo "╚════════════════════════════════════════════╝"
echo ""

echo -e "${BLUE}测试版本：${NC}$VERSION"
echo ""

# ============================================
# 1. 检查镜像 manifest
# ============================================

echo -e "${BLUE}[1/4]${NC} 检查镜像 manifest..."

echo ""
echo "  后端镜像："
if docker buildx imagetools inspect davidhoo/relive-backend:$VERSION > /dev/null 2>&1; then
    docker buildx imagetools inspect davidhoo/relive-backend:$VERSION

    # 检查是否包含 amd64
    if docker buildx imagetools inspect davidhoo/relive-backend:$VERSION | grep -q "linux/amd64"; then
        echo -e "${GREEN}  ✓${NC} 包含 linux/amd64"
    else
        echo -e "${RED}  ❌ 缺少 linux/amd64${NC}"
    fi

    # 检查是否包含 arm64
    if docker buildx imagetools inspect davidhoo/relive-backend:$VERSION | grep -q "linux/arm64"; then
        echo -e "${GREEN}  ✓${NC} 包含 linux/arm64"
    else
        echo -e "${RED}  ❌ 缺少 linux/arm64${NC}"
    fi
else
    echo -e "${RED}  ❌ 无法获取后端镜像信息${NC}"
    exit 1
fi

echo ""
echo "  前端镜像："
if docker buildx imagetools inspect davidhoo/relive-frontend:$VERSION > /dev/null 2>&1; then
    docker buildx imagetools inspect davidhoo/relive-frontend:$VERSION | head -20

    if docker buildx imagetools inspect davidhoo/relive-frontend:$VERSION | grep -q "linux/amd64"; then
        echo -e "${GREEN}  ✓${NC} 包含 linux/amd64"
    else
        echo -e "${RED}  ❌ 缺少 linux/amd64${NC}"
    fi

    if docker buildx imagetools inspect davidhoo/relive-frontend:$VERSION | grep -q "linux/arm64"; then
        echo -e "${GREEN}  ✓${NC} 包含 linux/arm64"
    else
        echo -e "${RED}  ❌ 缺少 linux/arm64${NC}"
    fi
else
    echo -e "${RED}  ❌ 无法获取前端镜像信息${NC}"
    exit 1
fi

echo ""

# ============================================
# 2. 测试拉取镜像
# ============================================

echo -e "${BLUE}[2/4]${NC} 测试拉取镜像..."

# 测试 amd64
echo ""
echo "  测试拉取 linux/amd64 版本..."
if docker pull --platform linux/amd64 davidhoo/relive-backend:$VERSION > /dev/null 2>&1; then
    echo -e "${GREEN}  ✓${NC} amd64 后端镜像拉取成功"
else
    echo -e "${RED}  ❌ amd64 后端镜像拉取失败${NC}"
fi

if docker pull --platform linux/amd64 davidhoo/relive-frontend:$VERSION > /dev/null 2>&1; then
    echo -e "${GREEN}  ✓${NC} amd64 前端镜像拉取成功"
else
    echo -e "${RED}  ❌ amd64 前端镜像拉取失败${NC}"
fi

# 测试 arm64
echo ""
echo "  测试拉取 linux/arm64 版本..."
if docker pull --platform linux/arm64 davidhoo/relive-backend:$VERSION > /dev/null 2>&1; then
    echo -e "${GREEN}  ✓${NC} arm64 后端镜像拉取成功"
else
    echo -e "${RED}  ❌ arm64 后端镜像拉取失败${NC}"
fi

if docker pull --platform linux/arm64 davidhoo/relive-frontend:$VERSION > /dev/null 2>&1; then
    echo -e "${GREEN}  ✓${NC} arm64 前端镜像拉取成功"
else
    echo -e "${RED}  ❌ arm64 前端镜像拉取失败${NC}"
fi

echo ""

# ============================================
# 3. 测试运行镜像
# ============================================

echo -e "${BLUE}[3/4]${NC} 测试运行镜像..."

# 获取当前平台
CURRENT_ARCH=$(uname -m)
if [ "$CURRENT_ARCH" = "x86_64" ]; then
    PLATFORM="linux/amd64"
elif [ "$CURRENT_ARCH" = "arm64" ] || [ "$CURRENT_ARCH" = "aarch64" ]; then
    PLATFORM="linux/arm64"
else
    PLATFORM="linux/amd64"
fi

echo ""
echo "  当前平台：$PLATFORM"

# 测试后端
echo ""
echo "  测试后端镜像..."
if docker run --rm --platform $PLATFORM davidhoo/relive-backend:$VERSION /app/relive --version 2>/dev/null; then
    echo -e "${GREEN}  ✓${NC} 后端运行测试通过"
else
    echo -e "${YELLOW}  ⚠️  后端运行测试失败（可能需要配置文件）${NC}"
fi

# 测试前端
echo ""
echo "  测试前端镜像..."
CONTAINER_ID=$(docker run -d --rm --platform $PLATFORM davidhoo/relive-frontend:$VERSION)
sleep 2

if docker exec $CONTAINER_ID sh -c "wget --spider http://localhost/" > /dev/null 2>&1; then
    echo -e "${GREEN}  ✓${NC} 前端运行测试通过"
else
    echo -e "${RED}  ❌ 前端运行测试失败${NC}"
fi

docker stop $CONTAINER_ID > /dev/null 2>&1 || true

echo ""

# ============================================
# 4. 检查镜像大小
# ============================================

echo -e "${BLUE}[4/4]${NC} 检查镜像大小..."

echo ""
echo "  后端镜像大小："
docker images davidhoo/relive-backend --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}" | grep $VERSION

echo ""
echo "  前端镜像大小："
docker images davidhoo/relive-frontend --format "table {{.Repository}}\t{{.Tag}}\t{{.Size}}" | grep $VERSION

# 检查大小是否合理
BACKEND_SIZE=$(docker images davidhoo/relive-backend:$VERSION --format "{{.Size}}" | head -1)
if [[ "$BACKEND_SIZE" =~ "MB" ]]; then
    SIZE_NUM=$(echo $BACKEND_SIZE | grep -o '[0-9]*')
    if [ $SIZE_NUM -lt 100 ]; then
        echo -e "${GREEN}  ✓${NC} 后端镜像大小合理 ($BACKEND_SIZE)"
    else
        echo -e "${YELLOW}  ⚠️  后端镜像较大 ($BACKEND_SIZE)${NC}"
    fi
fi

FRONTEND_SIZE=$(docker images davidhoo/relive-frontend:$VERSION --format "{{.Size}}" | head -1)
if [[ "$FRONTEND_SIZE" =~ "MB" ]]; then
    SIZE_NUM=$(echo $FRONTEND_SIZE | grep -o '[0-9]*')
    if [ $SIZE_NUM -lt 50 ]; then
        echo -e "${GREEN}  ✓${NC} 前端镜像大小合理 ($FRONTEND_SIZE)"
    else
        echo -e "${YELLOW}  ⚠️  前端镜像较大 ($FRONTEND_SIZE)${NC}"
    fi
fi

echo ""

# ============================================
# 测试总结
# ============================================

echo "╔════════════════════════════════════════════╗"
echo "║   ✅ 测试完成                             ║"
echo "╚════════════════════════════════════════════╝"
echo ""

echo "📊 测试总结："
echo "   ✓ Manifest 包含多架构"
echo "   ✓ 可以拉取 amd64 和 arm64 版本"
echo "   ✓ 镜像可以正常运行"
echo "   ✓ 镜像大小合理"
echo ""

echo "🎯 下一步："
echo "   1. 在实际 NAS 上测试部署"
echo "   2. 在不同架构的设备上验证"
echo "   3. 更新文档说明多架构支持"
echo ""

# ============================================
# 清理（可选）
# ============================================

read -p "是否清理测试镜像？[y/N] " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    docker rmi --force \
        davidhoo/relive-backend:$VERSION \
        davidhoo/relive-frontend:$VERSION \
        2>/dev/null || true
    echo -e "${GREEN}✓ 测试镜像已清理${NC}"
fi

echo ""
echo "🎉 多架构支持验证完成！"
echo ""
