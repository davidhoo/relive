#!/bin/bash

# Relive 本地测试脚本
# 用途：快速启动容器测试单镜像架构

set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║   🧪 Relive 本地测试                     ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# 容器名称
CONTAINER_NAME="relive-test-local"
PORT=18080

# 检查容器是否已存在
if docker ps -a --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    echo -e "${YELLOW}容器已存在，正在删除...${NC}"
    docker stop $CONTAINER_NAME >/dev/null 2>&1 || true
    docker rm $CONTAINER_NAME >/dev/null 2>&1 || true
fi

# 检查端口是否被占用
if lsof -Pi :$PORT -sTCP:LISTEN -t >/dev/null 2>&1 ; then
    echo -e "${YELLOW}警告: 端口 $PORT 已被占用${NC}"
    read -p "是否使用其他端口？输入端口号（或回车使用 19080）: " NEW_PORT
    PORT=${NEW_PORT:-19080}
fi

# 创建测试数据目录
TEST_DATA_DIR="./test-data"
mkdir -p "$TEST_DATA_DIR"

echo -e "${BLUE}[1/4]${NC} 启动容器..."
echo "  镜像: davidhu/relive:latest"
echo "  端口: http://localhost:$PORT"
echo "  数据目录: $TEST_DATA_DIR"
echo ""

# 启动容器
docker run -d \
  --name $CONTAINER_NAME \
  -p $PORT:8080 \
  -e TZ=Asia/Shanghai \
  -e JWT_SECRET=test-jwt-secret-for-local-testing-only \
  -v "$(pwd)/$TEST_DATA_DIR:/app/data" \
  -v "$(pwd)/backend/config.prod.yaml:/app/config.yaml:ro" \
  davidhu/relive:latest

echo -e "${GREEN}  ✓${NC} 容器已启动"
echo ""

echo -e "${BLUE}[2/4]${NC} 等待服务启动..."
sleep 5

# 检查容器状态
if docker ps --format '{{.Names}}' | grep -q "^${CONTAINER_NAME}$"; then
    echo -e "${GREEN}  ✓${NC} 容器运行正常"
else
    echo -e "${RED}  ❌ 容器启动失败${NC}"
    echo ""
    echo "查看日志："
    docker logs $CONTAINER_NAME
    exit 1
fi

echo ""
echo -e "${BLUE}[3/4]${NC} 测试 API..."

# 测试健康检查
if curl -s http://localhost:$PORT/api/v1/system/health | grep -q "healthy"; then
    echo -e "${GREEN}  ✓${NC} 后端 API 正常"
else
    echo -e "${YELLOW}  ⚠️  API 可能尚未就绪，请稍后测试${NC}"
fi

# 测试前端
if curl -s -I http://localhost:$PORT/ | grep -q "200 OK"; then
    echo -e "${GREEN}  ✓${NC} 前端访问正常"
else
    echo -e "${YELLOW}  ⚠️  前端可能尚未就绪，请稍后测试${NC}"
fi

echo ""
echo -e "${BLUE}[4/4]${NC} 测试完成！"
echo ""

# 显示访问信息
echo "╔════════════════════════════════════════════╗"
echo "║   ✅ 容器已启动                           ║"
echo "╚════════════════════════════════════════════╝"
echo ""
echo "📍 访问地址："
echo "   前端: ${GREEN}http://localhost:$PORT${NC}"
echo "   API:  http://localhost:$PORT/api/v1/system/health"
echo ""
echo "📊 默认账号："
echo "   用户名: admin"
echo "   密码: admin（首次登录需修改）"
echo ""
echo "🔍 实用命令："
echo "   查看日志: ${BLUE}docker logs -f $CONTAINER_NAME${NC}"
echo "   进入容器: ${BLUE}docker exec -it $CONTAINER_NAME sh${NC}"
echo "   停止容器: ${BLUE}docker stop $CONTAINER_NAME${NC}"
echo "   删除容器: ${BLUE}docker rm -f $CONTAINER_NAME${NC}"
echo ""
echo "💡 提示："
echo "   - 数据保存在: $TEST_DATA_DIR/"
echo "   - 配置文件: backend/config.prod.yaml"
echo "   - 按 Ctrl+C 可以停止查看日志"
echo ""

# 询问是否查看日志
read -p "是否查看容器日志？[y/N] " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo ""
    echo "📋 容器日志（Ctrl+C 退出）："
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    docker logs -f $CONTAINER_NAME
fi
