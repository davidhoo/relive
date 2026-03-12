#!/bin/bash

# Relive 本地部署脚本
# 用途：本地构建并启动 Docker 服务
# 使用：./deploy.sh

set -e

# 颜色定义
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo ""
echo "╔════════════════════════════════════════════╗"
echo "║   🚀 Relive 本地部署工具                 ║"
echo "║   智能照片记忆框架系统                    ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# ============================================
# 1. 环境检查
# ============================================

echo -e "${BLUE}[1/5]${NC} 检查部署环境..."

# 检查 Docker
if ! command -v docker &> /dev/null; then
    echo -e "${RED}❌ Docker 未安装${NC}"
    echo "请先安装 Docker: https://docs.docker.com/get-docker/"
    exit 1
fi
echo -e "${GREEN}  ✓${NC} Docker 已安装"

# 检查 Docker Compose
if ! command -v docker-compose &> /dev/null && ! docker compose version &> /dev/null; then
    echo -e "${RED}❌ Docker Compose 未安装${NC}"
    echo "请先安装 Docker Compose: https://docs.docker.com/compose/install/"
    exit 1
fi
echo -e "${GREEN}  ✓${NC} Docker Compose 已安装"

# 检查 openssl（用于生成密钥）
if ! command -v openssl &> /dev/null; then
    echo -e "${YELLOW}⚠️  openssl 未安装，将使用 /dev/urandom 生成密钥${NC}"
fi

echo ""

# ============================================
# 2. 生成 JWT 密钥
# ============================================

echo -e "${BLUE}[2/5]${NC} 生成安全密钥..."

if [ ! -f ".env" ]; then
    echo -e "${YELLOW}  未找到 .env 文件，从模板创建...${NC}"

    if [ -f ".env.example" ]; then
        cp .env.example .env
    else
        # 创建基础 .env 文件
        cat > .env << 'EOF'
# Relive 环境变量配置

# JWT 密钥（生产环境必须修改）
JWT_SECRET=relive-production-secret-please-change-me

# 服务端口（单镜像架构，统一端口）
RELIVE_PORT=8080

# 外部访问地址（可选，但推荐在需要 analyzer / 反向代理 / 域名访问时设置）
# RELIVE_EXTERNAL_URL=https://photos.example.com

# =============================================================================
# 关于照片路径配置
# =============================================================================
#
# 直接运行 Go 程序时：
#   不需要配置照片路径，系统启动后在 Web 界面添加扫描路径即可
#
# Docker 部署时：
#   照片路径配置在 docker-compose.yml 的 volumes 部分
#   修改方法：
#   1. 编辑 docker-compose.yml
#   2. 找到 volumes 下的照片目录挂载配置
#   3. 修改冒号左边的宿主机路径为你实际的照片目录
#   4. 例如：- /Users/david/Pictures:/app/photos:ro
#
# =============================================================================
EOF
    fi
fi

# 确保生产配置文件存在
if [ ! -f "backend/config.prod.yaml" ]; then
    echo -e "${YELLOW}  未找到 backend/config.prod.yaml，从示例创建...${NC}"
    if [ -f "backend/config.prod.yaml.example" ]; then
        cp backend/config.prod.yaml.example backend/config.prod.yaml
        echo -e "${GREEN}  ✓${NC} 已创建 backend/config.prod.yaml"
    else
        echo -e "${RED}❌ 未找到 backend/config.prod.yaml.example${NC}"
        exit 1
    fi
fi

# 生成 JWT 密钥
if command -v openssl &> /dev/null; then
    JWT_SECRET=$(openssl rand -base64 32)
else
    JWT_SECRET=$(head -c 32 /dev/urandom | base64)
fi

# 更新 .env 文件中的 JWT_SECRET
if grep -q "^JWT_SECRET=" .env; then
    # 如果已经有密钥，询问是否替换
    CURRENT_SECRET=$(grep "^JWT_SECRET=" .env | cut -d'=' -f2)
    if [ -z "$CURRENT_SECRET" ] || [ "$CURRENT_SECRET" = "relive-production-secret-please-change-me" ]; then
        # 如果是空的或默认值，直接替换
        sed -i.bak "s|^JWT_SECRET=.*|JWT_SECRET=$JWT_SECRET|" .env
        echo -e "${GREEN}  ✓${NC} JWT 密钥已生成并写入 .env"
    else
        echo -e "${GREEN}  ✓${NC} JWT 密钥已存在，跳过生成"
    fi
else
    # 如果没有 JWT_SECRET 行，添加它
    echo "JWT_SECRET=$JWT_SECRET" >> .env
    echo -e "${GREEN}  ✓${NC} JWT 密钥已生成并写入 .env"
fi

# 清理备份文件
rm -f .env.bak

echo ""

# ============================================
# 3. 创建数据目录
# ============================================

echo -e "${BLUE}[3/5]${NC} 创建数据目录..."

mkdir -p data/backend/logs
mkdir -p data/backend/thumbnails

echo -e "${GREEN}  ✓${NC} 数据目录已创建"
echo "    - data/backend/logs"
echo "    - data/backend/thumbnails"

# ============================================
# 3.1 下载离线地理编码数据（可选）
# ============================================

if [ ! -f "data/backend/cities500.txt" ]; then
    echo ""
    echo -e "${YELLOW}  离线地理编码数据未找到${NC}"
    echo "  用于照片位置信息显示，大小约 180MB"
    echo ""
    read -p "是否现在下载？[y/N] " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo -e "${BLUE}  下载 cities500.txt...${NC}"
        if command -v wget &> /dev/null; then
            wget -q -O data/backend/cities500.zip "https://download.geonames.org/export/dump/cities500.zip" && \
            unzip -q data/backend/cities500.zip -d data/backend/ && \
            rm -f data/backend/cities500.zip && \
            echo -e "${GREEN}  ✓${NC} 城市数据下载完成"
        elif command -v curl &> /dev/null; then
            curl -sL -o data/backend/cities500.zip "https://download.geonames.org/export/dump/cities500.zip" && \
            unzip -q data/backend/cities500.zip -d data/backend/ && \
            rm -f data/backend/cities500.zip && \
            echo -e "${GREEN}  ✓${NC} 城市数据下载完成"
        else
            echo -e "${RED}  ❌ 需要 wget 或 curl 来下载${NC}"
            echo "  手动下载: https://download.geonames.org/export/dump/cities500.zip"
            echo "  解压到: data/backend/cities500.txt"
        fi
    else
        echo -e "${YELLOW}  ⏭  跳过，稍后可手动下载${NC}"
        echo "  下载地址: https://download.geonames.org/export/dump/cities500.zip"
        echo "  解压到: data/backend/cities500.txt"
    fi
else
    echo -e "${GREEN}  ✓${NC} 城市数据已存在: cities500.txt"
fi

echo ""

# ============================================
# 4. 提示照片路径配置
# ============================================

echo -e "${BLUE}[4/5]${NC} 照片路径配置..."

if [ ! -f "docker-compose.yml" ]; then
    echo -e "${RED}❌ docker-compose.yml 不存在${NC}"
    exit 1
fi

echo -e "${YELLOW}  请编辑 docker-compose.yml 配置照片目录挂载${NC}"
echo ""
echo "  示例："
echo "    volumes:"
echo "      - /your/photos/path:/app/photos:ro"
echo ""
echo -e "${GREEN}  ✓${NC} 部署后可在 Web 界面添加扫描路径"

echo ""

# ============================================
# 5. 构建并启动服务
# ============================================

echo -e "${BLUE}[5/5]${NC} 构建并启动服务..."

# 构建前端（如果需要）
if [ ! -d "frontend/dist" ]; then
    echo "  前端未构建，开始构建..."

    if [ ! -d "frontend/node_modules" ]; then
        echo "  安装前端依赖..."
        cd frontend
        if command -v npm &> /dev/null; then
            npm install
        else
            echo -e "${RED}  ❌ npm 未安装，无法构建前端${NC}"
            exit 1
        fi
        cd ..
    fi

    echo "  构建前端..."
    cd frontend
    npm run build
    cd ..

    echo -e "${GREEN}  ✓${NC} 前端构建完成"
else
    echo -e "${GREEN}  ✓${NC} 前端已构建，跳过"
fi

# 构建并启动 Docker
if docker compose version &> /dev/null 2>&1; then
    docker compose build
    docker compose up -d
else
    docker-compose build
    docker-compose up -d
fi

echo -e "${GREEN}  ✓${NC} Docker 服务已启动"

echo ""

# ============================================
# 部署完成
# ============================================

echo "╔════════════════════════════════════════════╗"
echo "║   ✅ 部署成功！                           ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# 获取实际端口
RELIVE_PORT=$(grep "^RELIVE_PORT=" .env | cut -d'=' -f2)
RELIVE_PORT=${RELIVE_PORT:-8080}

echo "📌 访问地址："
echo "   🌐 http://localhost:${RELIVE_PORT}"
echo "   💚 健康检查：http://localhost:${RELIVE_PORT}/api/v1/system/health"
echo ""

echo "🔐 默认管理员账号："
echo "   用户名：admin"
echo "   密码：admin"
echo "   ⚠️  首次登录会强制修改密码"
echo ""

echo "📝 常用命令："
echo "   查看日志：docker-compose logs -f"
echo "   停止服务：docker-compose down"
echo "   重启服务：docker-compose restart"
echo "   查看状态：docker-compose ps"
echo ""

echo "📚 下一步："
echo "   1. 访问前端地址，使用 admin/admin 登录"
echo "   2. 首次登录后修改密码"
echo "   3. 在「配置管理」中添加扫描路径"
echo "   4. 开始扫描照片"
echo "   5. （可选）配置 AI 提供者进行智能分析"
echo ""

echo "💡 提示："
echo "   - 如需配置 AI 分析，请在 Web 界面的「配置管理」中设置"
echo "   - 照片路径需在 docker-compose.yml 中配置 volumes"
echo "   - 建议配置 HTTPS 和反向代理"
echo ""

# ============================================
# 安全提醒
# ============================================

echo -e "${YELLOW}⚠️  安全提醒：${NC}"
echo "   1. JWT 密钥已自动生成，请勿泄露 .env 文件"
echo "   2. 首次登录后请立即修改管理员密码"
echo "   3. 生产环境建议配置 HTTPS"
echo "   4. 建议限制后端端口只监听 127.0.0.1"
echo "   5. 定期备份数据库文件：data/backend/relive.db"
echo ""

# ============================================
# 健康检查
# ============================================

echo "正在等待服务启动..."
sleep 5

# 健康检查
if curl -s "http://localhost:${RELIVE_PORT}/api/v1/system/health" > /dev/null 2>&1; then
    echo -e "${GREEN}✓ 服务健康检查通过${NC}"
else
    echo -e "${YELLOW}⚠️  服务可能还在启动中，请稍后手动检查${NC}"
    echo "   检查命令：curl http://localhost:${RELIVE_PORT}/api/v1/system/health"
fi

echo ""
echo "🎉 祝你使用愉快！"
echo ""
