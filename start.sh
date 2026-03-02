#!/bin/bash

# Relive 快速启动脚本

set -e

echo "🚀 Relive 快速启动脚本"
echo "======================="
echo ""

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# 检查 Docker
echo -n "检查 Docker... "
if ! command -v docker &> /dev/null; then
    echo -e "${RED}失败${NC}"
    echo "❌ Docker 未安装，请先安装 Docker"
    exit 1
fi
echo -e "${GREEN}OK${NC}"

# 检查 Docker Compose
echo -n "检查 Docker Compose... "
if ! command -v docker-compose &> /dev/null && ! docker compose version &> /dev/null; then
    echo -e "${RED}失败${NC}"
    echo "❌ Docker Compose 未安装，请先安装 Docker Compose"
    exit 1
fi
echo -e "${GREEN}OK${NC}"

# 检查 .env 文件
if [ ! -f ".env" ]; then
    echo ""
    echo -e "${YELLOW}⚠️  未找到 .env 文件${NC}"
    echo "正在从 .env.example 创建..."
    cp .env.example .env
    echo -e "${GREEN}✓${NC} .env 文件已创建"
    echo ""
    echo -e "${YELLOW}请编辑 .env 文件，配置以下内容：${NC}"
    echo "  1. PHOTOS_PATH - 你的照片目录路径"
    echo "  2. QWEN_API_KEY 或 OPENAI_API_KEY（如果使用在线 AI）"
    echo "  3. JWT_SECRET（生产环境必须修改）"
    echo ""
    read -p "按回车继续，或 Ctrl+C 退出先配置 .env... "
fi

# 创建必要的目录
echo ""
echo "创建数据目录..."
mkdir -p data/backend/logs
mkdir -p data/frontend
echo -e "${GREEN}✓${NC} 数据目录已创建"

# 检查前端是否已构建
if [ ! -d "frontend/dist" ]; then
    echo ""
    echo -e "${YELLOW}⚠️  前端未构建${NC}"
    echo "正在构建前端..."

    if [ ! -d "frontend/node_modules" ]; then
        echo "安装前端依赖..."
        cd frontend && npm install && cd ..
    fi

    echo "构建前端..."
    cd frontend && npm run build && cd ..
    echo -e "${GREEN}✓${NC} 前端构建完成"
fi

# 构建 Docker 镜像
echo ""
echo "构建 Docker 镜像..."
if docker compose version &> /dev/null; then
    docker compose build
else
    docker-compose build
fi
echo -e "${GREEN}✓${NC} Docker 镜像构建完成"

# 启动服务
echo ""
echo "启动服务..."
if docker compose version &> /dev/null; then
    docker compose up -d
else
    docker-compose up -d
fi

echo ""
echo -e "${GREEN}✓${NC} 服务启动成功！"
echo ""
echo "📌 访问地址："
echo "   前端：http://localhost:8888"
echo "   后端：http://localhost:8080"
echo "   健康检查：http://localhost:8080/system/health"
echo ""
echo "📝 查看日志："
echo "   docker-compose logs -f"
echo ""
echo "🛑 停止服务："
echo "   docker-compose down"
echo ""
echo "📊 查看状态："
echo "   docker-compose ps"
echo ""
