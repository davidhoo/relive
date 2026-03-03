#!/bin/bash

# Relive Docker 部署测试脚本
# 用于验证 Docker 部署功能是否正常

set -e

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 测试计数
TESTS_PASSED=0
TESTS_FAILED=0

# 打印带颜色的信息
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[PASS]${NC} $1"
    ((TESTS_PASSED++))
}

print_error() {
    echo -e "${RED}[FAIL]${NC} $1"
    ((TESTS_FAILED++))
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

# 测试标题
print_header() {
    echo ""
    echo "═══════════════════════════════════════════════════"
    echo "  $1"
    echo "══════════════════════════════════════════════════="
}

# 检查命令是否存在
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# 等待服务就绪
wait_for_service() {
    local url=$1
    local max_attempts=$2
    local attempt=1

    print_info "等待服务就绪: $url"
    while [ $attempt -le $max_attempts ]; do
        if curl -s "$url" > /dev/null 2>&1; then
            return 0
        fi
        echo -n "."
        sleep 2
        ((attempt++))
    done
    echo ""
    return 1
}

# ═══════════════════════════════════════════════════
# 测试开始
# ═══════════════════════════════════════════════════

echo ""
echo "╔═══════════════════════════════════════════════════╗"
echo "║     Relive Docker 部署测试脚本                    ║"
echo "╚═══════════════════════════════════════════════════╝"
echo ""

# ═══════════════════════════════════════════════════
# 1. 环境检查
# ═══════════════════════════════════════════════════
print_header "1. 环境检查"

# 检查 Docker
if command_exists docker; then
    DOCKER_VERSION=$(docker --version)
    print_success "Docker 已安装: $DOCKER_VERSION"
else
    print_error "Docker 未安装"
    echo ""
    echo "请安装 Docker Desktop:"
    echo "  brew install --cask docker"
    echo ""
    exit 1
fi

# 检查 Docker 守护进程
if docker info > /dev/null 2>&1; then
    print_success "Docker 守护进程运行正常"
else
    print_error "Docker 守护进程未运行"
    echo ""
    echo "请启动 Docker Desktop 应用"
    echo ""
    exit 1
fi

# 检查 Docker Compose
if docker compose version > /dev/null 2>&1 || docker-compose version > /dev/null 2>&1; then
    COMPOSE_VERSION=$(docker compose version 2>/dev/null || docker-compose version --short 2>/dev/null)
    print_success "Docker Compose 可用: $COMPOSE_VERSION"
else
    print_error "Docker Compose 未安装"
    exit 1
fi

# 检查 .env 文件
if [ -f ".env" ]; then
    print_success ".env 文件存在"
else
    print_warning ".env 文件不存在，将使用默认配置"
    cp .env.example .env
fi

# 检查前端构建
if [ -d "frontend/dist" ]; then
    print_success "前端构建产物存在 (frontend/dist)"
else
    print_warning "前端未构建，将在部署时自动构建"
fi

# ═══════════════════════════════════════════════════
# 2. 构建测试
# ═══════════════════════════════════════════════════
print_header "2. Docker 构建测试"

print_info "开始构建 Docker 镜像..."
if docker compose build > /tmp/docker-build.log 2>&1; then
    print_success "Docker 镜像构建成功"
else
    print_error "Docker 镜像构建失败"
    echo ""
    echo "错误日志:"
    tail -50 /tmp/docker-build.log
    exit 1
fi

# ═══════════════════════════════════════════════════
# 3. 部署测试
# ═══════════════════════════════════════════════════
print_header "3. 服务部署测试"

print_info "启动 Docker 服务..."
if docker compose up -d > /tmp/docker-up.log 2>&1; then
    print_success "Docker 服务启动成功"
else
    print_error "Docker 服务启动失败"
    echo ""
    echo "错误日志:"
    cat /tmp/docker-up.log
    exit 1
fi

# 等待服务就绪
print_info "等待服务初始化..."
sleep 5

# ═══════════════════════════════════════════════════
# 4. 健康检查
# ═══════════════════════════════════════════════════
print_header "4. 健康检查"

# 检查后端健康状态
if wait_for_service "http://localhost:8080/system/health" 15; then
    HEALTH_STATUS=$(curl -s http://localhost:8080/system/health)
    print_success "后端健康检查通过"
    print_info "响应: $HEALTH_STATUS"
else
    print_error "后端健康检查失败"
    echo ""
    echo "后端日志:"
    docker compose logs --tail=30 relive-backend
fi

# 检查前端可访问性
if wait_for_service "http://localhost:8888" 10; then
    print_success "前端服务可访问 (http://localhost:8888)"
else
    print_error "前端服务无法访问"
fi

# 检查 API 端点
if curl -s "http://localhost:8080/api/v1/photos?page_size=1" > /dev/null 2>&1; then
    print_success "API 端点正常工作"
else
    print_warning "API 端点可能需要认证，跳过测试"
fi

# ═══════════════════════════════════════════════════
# 5. 功能测试
# ═══════════════════════════════════════════════════
print_header "5. 功能测试"

# 测试系统统计接口
STATS_RESPONSE=$(curl -s http://localhost:8080/api/v1/system/stats 2>/dev/null || echo "")
if [ -n "$STATS_RESPONSE" ] && echo "$STATS_RESPONSE" | grep -q "success"; then
    print_success "系统统计接口正常"
    print_info "响应: $STATS_RESPONSE"
else
    print_warning "系统统计接口可能需要认证"
fi

# ═══════════════════════════════════════════════════
# 6. 容器状态检查
# ═══════════════════════════════════════════════════
print_header "6. 容器状态检查"

# 检查后端容器
BACKEND_STATUS=$(docker inspect --format='{{.State.Status}}' relive-backend 2>/dev/null || echo "not found")
if [ "$BACKEND_STATUS" = "running" ]; then
    print_success "后端容器运行正常"
else
    print_error "后端容器状态异常: $BACKEND_STATUS"
fi

# 检查前端容器
FRONTEND_STATUS=$(docker inspect --format='{{.State.Status}}' relive-frontend 2>/dev/null || echo "not found")
if [ "$FRONTEND_STATUS" = "running" ]; then
    print_success "前端容器运行正常"
else
    print_error "前端容器状态异常: $FRONTEND_STATUS"
fi

# 检查资源使用
print_info "容器资源使用:"
docker stats --no-stream --format "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.Status}}" relive-backend relive-frontend 2>/dev/null || true

# ═══════════════════════════════════════════════════
# 测试报告
# ═══════════════════════════════════════════════════
print_header "测试报告"

echo ""
echo "┌─────────────────────────────────────────────────┐"
echo "│                 测试结果汇总                     │"
echo "├─────────────────────────────────────────────────┤"
printf "│  通过: %-4d                                    │\n" $TESTS_PASSED
printf "│  失败: %-4d                                    │\n" $TESTS_FAILED
echo "└─────────────────────────────────────────────────┘"
echo ""

if [ $TESTS_FAILED -eq 0 ]; then
    echo -e "${GREEN}✓ 所有测试通过！Docker 部署功能正常。${NC}"
    echo ""
    echo "访问地址:"
    echo "  • 前端界面: http://localhost:8888"
    echo "  • 后端 API: http://localhost:8080/api/v1/"
    echo "  • 健康检查: http://localhost:8080/system/health"
    echo ""
    echo "常用命令:"
    echo "  docker-compose logs -f     # 查看日志"
    echo "  docker-compose ps          # 查看状态"
    echo "  docker-compose down        # 停止服务"
    exit 0
else
    echo -e "${RED}✗ 部分测试失败，请检查上述错误信息。${NC}"
    echo ""
    echo "调试命令:"
    echo "  docker-compose logs -f relive-backend   # 查看后端日志"
    echo "  docker-compose logs -f relive-frontend  # 查看前端日志"
    exit 1
fi
