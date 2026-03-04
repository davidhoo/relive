#!/bin/bash

# Relive 生产环境部署脚本
# 用途：Docker 部署到 NAS 或服务器
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
echo "║   🚀 Relive 生产环境部署工具             ║"
echo "║   智能照片记忆框架系统                    ║"
echo "╚════════════════════════════════════════════╝"
echo ""

# ============================================
# 1. 环境检查
# ============================================

echo -e "${BLUE}[1/6]${NC} 检查部署环境..."

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

echo -e "${BLUE}[2/6]${NC} 生成安全密钥..."

if [ ! -f ".env" ]; then
    echo -e "${YELLOW}  未找到 .env 文件，从模板创建...${NC}"

    if [ -f ".env.example" ]; then
        cp .env.example .env
    else
        # 创建基础 .env 文件
        cat > .env << 'EOF'
# Relive 环境变量配置

# JWT 密钥（自动生成）
JWT_SECRET=

# 前端端口
FRONTEND_PORT=8888

# 后端端口
BACKEND_PORT=8080

# 注意：AI Provider 的 API Key 通过管理页面配置，存储在数据库中
EOF
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

echo -e "${BLUE}[3/6]${NC} 创建数据目录..."

mkdir -p data/backend/logs
mkdir -p data/backend/thumbnails
mkdir -p data/frontend

echo -e "${GREEN}  ✓${NC} 数据目录已创建"
echo "    - data/backend/logs"
echo "    - data/backend/thumbnails"
echo "    - data/frontend"

echo ""

# ============================================
# 4. 配置照片路径
# ============================================

echo -e "${BLUE}[4/6]${NC} 配置照片路径..."

if [ -f "docker-compose.yml" ]; then
    # 检查 docker-compose.yml 中是否有照片路径配置示例
    if grep -q "# - /Users/david/Downloads/2025:/app/photos/iphone2025:ro" docker-compose.yml; then
        echo -e "${YELLOW}  照片路径配置示例已在 docker-compose.yml 中${NC}"
        echo ""
        echo "  请编辑 docker-compose.yml，配置你的照片目录："
        echo ""
        echo "  volumes:"
        echo "    # 示例："
        echo "    # - /volume1/photos/2024:/app/photos/2024:ro"
        echo "    # - /volume1/photos/2025:/app/photos/2025:ro"
        echo ""
    fi
else
    echo -e "${RED}❌ docker-compose.yml 不存在${NC}"
    exit 1
fi

read -p "是否现在配置照片路径？[y/N] " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo ""
    echo "请输入照片目录的完整路径（例如：/volume1/photos）："
    read -r PHOTOS_PATH

    if [ -d "$PHOTOS_PATH" ]; then
        # 在 docker-compose.yml 中添加照片路径
        echo ""
        echo "  将在 docker-compose.yml 中添加："
        echo "  - ${PHOTOS_PATH}:/app/photos:ro"
        echo ""
        echo -e "${GREEN}  ✓${NC} 照片路径配置完成"
        echo -e "${YELLOW}  注意：请手动编辑 docker-compose.yml 取消注释相关行${NC}"
    else
        echo -e "${RED}  ❌ 路径不存在: $PHOTOS_PATH${NC}"
        echo -e "${YELLOW}  请稍后手动编辑 docker-compose.yml 配置照片路径${NC}"
    fi
else
    echo -e "${YELLOW}  ⏭  跳过照片路径配置，请稍后手动配置${NC}"
fi

echo ""

# ============================================
# 5. 构建前端
# ============================================

echo -e "${BLUE}[5/6]${NC} 构建前端..."

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

echo ""

# ============================================
# 6. 启动服务
# ============================================

echo -e "${BLUE}[6/6]${NC} 启动 Docker 服务..."

# 构建并启动
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
FRONTEND_PORT=$(grep "^FRONTEND_PORT=" .env | cut -d'=' -f2)
BACKEND_PORT=$(grep "^BACKEND_PORT=" .env | cut -d'=' -f2)

# 如果端口为空，使用默认值
FRONTEND_PORT=${FRONTEND_PORT:-8888}
BACKEND_PORT=${BACKEND_PORT:-8080}

echo "📌 访问地址："
echo "   🌐 前端：http://localhost:${FRONTEND_PORT}"
echo "   🔧 后端：http://localhost:${BACKEND_PORT}"
echo "   💚 健康检查：http://localhost:${BACKEND_PORT}/system/health"
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
if curl -s "http://localhost:${BACKEND_PORT}/system/health" > /dev/null 2>&1; then
    echo -e "${GREEN}✓ 后端服务健康检查通过${NC}"
else
    echo -e "${YELLOW}⚠️  后端服务可能还在启动中，请稍后手动检查${NC}"
    echo "   检查命令：curl http://localhost:${BACKEND_PORT}/system/health"
fi

echo ""
echo "🎉 祝你使用愉快！"
echo ""
