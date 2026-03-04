#!/bin/bash

# Relive 一键安装脚本（使用 DockerHub 镜像）
# 用途：无需克隆仓库，直接从 DockerHub 部署
# 使用：curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/install.sh | bash

set -e

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║   🚀 Relive 一键安装工具                 ║"
echo "║   使用 DockerHub 镜像快速部署            ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# ============================================
# 1. 环境检查
# ============================================

echo -e "${BLUE}[1/5]${NC} 检查环境..."

if ! command -v docker &> /dev/null; then
    echo -e "${RED}❌ Docker 未安装${NC}"
    echo "请先安装 Docker: https://docs.docker.com/get-docker/"
    exit 1
fi
echo -e "${GREEN}  ✓${NC} Docker 已安装"

if ! command -v docker-compose &> /dev/null && ! docker compose version &> /dev/null; then
    echo -e "${RED}❌ Docker Compose 未安装${NC}"
    exit 1
fi
echo -e "${GREEN}  ✓${NC} Docker Compose 已安装"

echo ""

# ============================================
# 2. 创建安装目录
# ============================================

echo -e "${BLUE}[2/5]${NC} 创建安装目录..."

INSTALL_DIR="${INSTALL_DIR:-$HOME/relive}"

echo "  安装目录: $INSTALL_DIR"
read -p "  是否使用此目录？[Y/n] " -n 1 -r
echo
if [[ $REPLY =~ ^[Nn]$ ]]; then
    read -p "  请输入安装目录: " INSTALL_DIR
fi

mkdir -p "$INSTALL_DIR"
cd "$INSTALL_DIR"

echo -e "${GREEN}  ✓${NC} 目录已创建: $INSTALL_DIR"

# 创建数据目录
mkdir -p data/backend/logs
mkdir -p data/backend/thumbnails

echo ""

# ============================================
# 3. 下载配置文件
# ============================================

echo -e "${BLUE}[3/5]${NC} 下载配置文件..."

GITHUB_RAW="https://raw.githubusercontent.com/davidhoo/relive/main"

# 下载 docker-compose 配置
echo "  下载 docker-compose.yml..."
curl -fsSL "$GITHUB_RAW/docker-compose.prod.yml" -o docker-compose.yml

# 下载后端配置
echo "  下载 config.prod.yaml..."
curl -fsSL "$GITHUB_RAW/backend/config.prod.yaml" -o config.prod.yaml

# 下载 Nginx 配置（可选）
echo "  下载 nginx.conf..."
curl -fsSL "$GITHUB_RAW/nginx.conf" -o nginx.conf

echo -e "${GREEN}  ✓${NC} 配置文件下载完成"

echo ""

# ============================================
# 4. 生成 .env 配置
# ============================================

echo -e "${BLUE}[4/5]${NC} 生成配置..."

if [ ! -f ".env" ]; then
    echo "  生成 JWT 密钥..."

    if command -v openssl &> /dev/null; then
        JWT_SECRET=$(openssl rand -base64 32)
    else
        JWT_SECRET=$(head -c 32 /dev/urandom | base64)
    fi

    cat > .env << EOF
# Relive 环境变量配置（自动生成）

# JWT 密钥（请勿泄露）
JWT_SECRET=$JWT_SECRET

# 前端端口
FRONTEND_PORT=8888

# 后端端口
BACKEND_PORT=8080

# 自动导入城市数据
AUTO_IMPORT_CITIES=true

# AI Provider API Keys（可选，可在 Web 界面配置）
# QWEN_API_KEY=
# OPENAI_API_KEY=
EOF

    echo -e "${GREEN}  ✓${NC} JWT 密钥已生成"
else
    echo -e "${YELLOW}  .env 文件已存在，跳过${NC}"
fi

echo ""

# ============================================
# 5. 配置照片路径（可选）
# ============================================

echo -e "${BLUE}[5/5]${NC} 配置照片路径（可选）..."

echo ""
echo "  照片路径配置方式："
echo "  1. 现在手动输入路径（自动添加到 docker-compose.yml）"
echo "  2. 稍后手动编辑 docker-compose.yml"
echo ""
read -p "  请选择 [1/2]，直接回车跳过: " -n 1 -r
echo

if [[ $REPLY =~ ^[1]$ ]]; then
    echo ""
    echo "  请输入照片目录完整路径（例如：/volume1/photos）："
    read -r PHOTOS_PATH

    if [ -d "$PHOTOS_PATH" ]; then
        # 在 docker-compose.yml 中添加 volume
        echo "      - $PHOTOS_PATH:/app/photos:ro" >> docker-compose.yml
        echo -e "${GREEN}  ✓${NC} 照片路径已添加: $PHOTOS_PATH"
    else
        echo -e "${YELLOW}  ⚠️  路径不存在，请稍后手动编辑 docker-compose.yml${NC}"
    fi
else
    echo -e "${YELLOW}  跳过照片路径配置，请稍后编辑：${NC}"
    echo "    $INSTALL_DIR/docker-compose.yml"
fi

echo ""

# ============================================
# 6. 拉取镜像并启动
# ============================================

echo -e "${BLUE}正在拉取镜像并启动服务...${NC}"
echo ""

# 拉取镜像
echo "  拉取后端镜像..."
docker pull davidhoo/relive-backend:latest

echo "  拉取前端镜像..."
docker pull davidhoo/relive-frontend:latest

# 启动服务
echo ""
echo "  启动服务..."
if docker compose version &> /dev/null 2>&1; then
    docker compose up -d
else
    docker-compose up -d
fi

echo ""

# ============================================
# 部署完成
# ============================================

echo "╔════════════════════════════════════════════╗"
echo "║   ✅ 安装成功！                           ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# 获取 IP 地址
if command -v hostname &> /dev/null; then
    HOST_IP=$(hostname -I | awk '{print $1}')
    if [ -z "$HOST_IP" ]; then
        HOST_IP="localhost"
    fi
else
    HOST_IP="localhost"
fi

FRONTEND_PORT=$(grep "^FRONTEND_PORT=" .env | cut -d'=' -f2)
FRONTEND_PORT=${FRONTEND_PORT:-8888}

echo "📌 访问地址："
echo "   🌐 前端：http://${HOST_IP}:${FRONTEND_PORT}"
echo "   🔧 后端：http://${HOST_IP}:8080"
echo "   💚 健康检查：http://${HOST_IP}:8080/system/health"
echo ""

echo "📁 安装位置："
echo "   $INSTALL_DIR"
echo ""

echo "🔐 默认账号："
echo "   用户名：admin"
echo "   密码：admin"
echo "   ⚠️  首次登录会强制修改密码"
echo ""

echo "📝 常用命令："
echo "   cd $INSTALL_DIR"
echo "   docker-compose logs -f       # 查看日志"
echo "   docker-compose restart       # 重启服务"
echo "   docker-compose down          # 停止服务"
echo "   docker-compose pull && docker-compose up -d  # 更新"
echo ""

echo "📚 下一步："
echo "   1. 访问前端地址，使用 admin/admin 登录"
echo "   2. 首次登录后修改密码"
echo "   3. 如未配置照片路径，编辑："
echo "      $INSTALL_DIR/docker-compose.yml"
echo "   4. 在 Web 界面「配置管理」中添加扫描路径"
echo "   5. 开始扫描照片"
echo ""

echo -e "${YELLOW}⚠️  安全提醒：${NC}"
echo "   - JWT 密钥已自动生成，请勿泄露 .env 文件"
echo "   - 首次登录后请立即修改密码"
echo "   - 生产环境建议配置 HTTPS"
echo "   - 定期备份数据库：data/backend/relive.db"
echo ""

# 健康检查
echo "正在等待服务启动..."
sleep 8

if curl -s "http://localhost:8080/system/health" > /dev/null 2>&1; then
    echo -e "${GREEN}✓ 后端服务健康检查通过${NC}"
else
    echo -e "${YELLOW}⚠️  后端服务可能还在启动中${NC}"
    echo "   请稍后访问：http://${HOST_IP}:8080/system/health"
fi

echo ""
echo "🎉 享受你的智能照片记忆框架！"
echo ""
