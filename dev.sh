#!/bin/bash

# Relive 开发环境启动脚本

set -e

echo "🔧 Relive 开发环境启动"
echo "====================="
echo ""

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 检查配置文件
if [ ! -f "backend/config.dev.yaml" ]; then
    echo "❌ 未找到 backend/config.dev.yaml"
    exit 1
fi

# 创建数据目录
echo "创建数据目录..."
mkdir -p backend/data/logs
mkdir -p backend/data/photos
echo -e "${GREEN}✓${NC} 数据目录已创建"
echo ""

# 函数：启动后端
start_backend() {
    echo -e "${BLUE}启动后端服务...${NC}"
    cd backend

    # 检查依赖
    if [ ! -f "go.mod" ]; then
        echo "❌ 未找到 go.mod"
        exit 1
    fi

    # 下载依赖
    echo "检查 Go 依赖..."
    go mod download

    # 运行后端
    echo -e "${GREEN}✓${NC} 后端服务启动在 http://localhost:8080"
    go run cmd/relive/main.go --config config.dev.yaml
}

# 函数：启动前端
start_frontend() {
    echo -e "${BLUE}启动前端服务...${NC}"
    cd frontend

    # 检查 node_modules
    if [ ! -d "node_modules" ]; then
        echo "安装前端依赖..."
        npm install
    fi

    echo -e "${GREEN}✓${NC} 前端服务启动在 http://localhost:5173"
    npm run dev
}

# 显示菜单
echo "请选择启动模式："
echo "  1. 启动后端（端口 8080）"
echo "  2. 启动前端（端口 5173）"
echo "  3. 同时启动前后端（推荐）"
echo ""
read -p "请输入选项 [1-3]: " choice

case $choice in
    1)
        start_backend
        ;;
    2)
        start_frontend
        ;;
    3)
        echo ""
        echo "同时启动前后端..."
        echo "后端: http://localhost:8080"
        echo "前端: http://localhost:5173"
        echo ""
        echo "按 Ctrl+C 停止所有服务"
        echo ""

        # 使用 trap 捕获 Ctrl+C
        trap 'echo ""; echo "停止所有服务..."; kill $(jobs -p) 2>/dev/null; exit' INT

        # 在后台启动后端
        (cd backend && go run cmd/relive/main.go --config config.dev.yaml) &
        BACKEND_PID=$!

        # 等待后端启动
        sleep 3

        # 在后台启动前端
        (cd frontend && npm run dev) &
        FRONTEND_PID=$!

        # 等待进程
        wait
        ;;
    *)
        echo "无效选项"
        exit 1
        ;;
esac
