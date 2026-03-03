#!/bin/bash

# Docker Desktop 安装脚本
# 用于 macOS 系统通过 Homebrew 安装 Docker Desktop

set -e

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║         Docker Desktop 安装脚本 (macOS)                     ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

# 检查 Homebrew
if ! command -v brew &> /dev/null; then
    print_error "Homebrew 未安装"
    echo "请先安装 Homebrew:"
    echo '  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"'
    exit 1
fi

print_success "Homebrew 已安装"

# 检查 Docker 是否已安装
if command -v docker &> /dev/null; then
    print_success "Docker 已安装: $(docker --version)"

    # 检查 Docker 是否运行
    if docker info &> /dev/null; then
        print_success "Docker 正在运行"
        echo ""
        print_success "Docker 安装完成且运行正常！"
        exit 0
    else
        print_warning "Docker 已安装但未运行"
        echo ""
        echo "请启动 Docker Desktop:"
        echo "  open /Applications/Docker.app"
        echo ""
        echo "等待 Docker 完全启动后，重新运行测试脚本："
        echo "  ./test-docker.sh"
        exit 0
    fi
fi

# 安装 Docker Desktop
echo ""
print_info "开始安装 Docker Desktop..."
echo ""

# 提示用户
print_warning "安装 Docker Desktop 需要管理员密码"
echo "请在提示时输入你的系统密码"
echo ""

# 执行安装
if brew install --cask docker-desktop; then
    print_success "Docker Desktop 安装成功"
    echo ""
    print_info "正在启动 Docker Desktop..."

    # 启动 Docker
    open /Applications/Docker.app

    echo ""
    echo "═══════════════════════════════════════════════════════"
    print_success "Docker Desktop 安装完成！"
    echo "═══════════════════════════════════════════════════════"
    echo ""
    echo "Docker Desktop 正在启动，请等待："
    echo "  1. 顶部菜单栏的 Docker 图标停止动画"
    echo "  2. 出现 'Docker Desktop is running' 提示"
    echo ""
    echo "然后运行测试脚本："
    echo "  ./test-docker.sh"
    echo ""

else
    print_error "安装失败"
    echo ""
    echo "可能的解决方案："
    echo "1. 检查网络连接"
    echo "2. 运行: brew update"
    echo "3. 手动下载安装: https://www.docker.com/products/docker-desktop"
    exit 1
fi
