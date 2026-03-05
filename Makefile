# Makefile for Relive Project
# 禁用隐式规则，避免 deploy.sh 被自动转换为 deploy
MAKEFLAGS += --no-builtin-rules

.PHONY: help dev build deploy prod stop restart logs clean test deps sync-version build-analyzer analyzer

# 版本管理
VERSION_FILE := VERSION
VERSION_PKG_DIR := backend/pkg/version

# 同步版本文件到 go package
sync-version:
	@cp $(VERSION_FILE) $(VERSION_PKG_DIR)/VERSION
	@echo "Version synced: $$(cat $(VERSION_FILE))"

# 默认目标
help:
	@echo "Relive 项目管理命令"
	@echo ""
	@echo "首次使用 Docker 部署:"
	@echo "  cp docker-compose.yml.example docker-compose.yml"
	@echo "  cp docker-compose.prod.yml.example docker-compose.prod.yml"
	@echo "  # 编辑配置文件，设置你的照片路径等"
	@echo ""
	@echo "开发环境:"
	@echo "  make dev              - 启动开发环境（交互式菜单）"
	@echo "  make dev-backend      - 只启动后端开发服务"
	@echo "  make dev-frontend     - 只启动前端开发服务"
	@echo ""
	@echo "离线分析工具:"
	@echo "  make build-analyzer   - 构建离线分析工具"
	@echo "  make analyzer         - 构建并运行离线分析工具"
	@echo "  # 使用方式:"
	@echo "  # ./backend/bin/relive-analyzer check -config analyzer.yaml"
	@echo "  # ./backend/bin/relive-analyzer analyze -config analyzer.yaml"
	@echo ""
	@echo "生产部署:"
	@echo "  make build            - 构建 Docker 镜像"
	@echo "  make deploy           - 本地构建并部署"
	@echo "  make prod             - 使用 DockerHub 镜像部署"
	@echo "  make stop             - 停止所有服务"
	@echo "  make restart          - 重启服务"
	@echo "  make logs             - 查看日志"
	@echo ""
	@echo "测试和清理:"
	@echo "  make test             - 运行测试"
	@echo "  make clean            - 清理构建文件"
	@echo "  make deps             - 安装依赖"
	@echo ""

# 开发环境
dev:
	./dev.sh

dev-backend: sync-version
	cd backend && go run cmd/relive/main.go --config config.dev.yaml

dev-frontend:
	cd frontend && npm run dev

# Docker Compose 配置检查
check-compose:
	@test -f docker-compose.yml || (echo "错误: docker-compose.yml 不存在"; echo "请运行: cp docker-compose.yml.example docker-compose.yml"; exit 1)

# 生产部署
build: sync-version check-compose
	@echo "构建 Docker 镜像..."
	docker-compose build

deploy: check-compose
	@echo "本地构建并部署..."
	./deploy.sh

prod:
	@test -f docker-compose.prod.yml || (echo "错误: docker-compose.prod.yml 不存在"; echo "请运行: cp docker-compose.prod.yml.example docker-compose.prod.yml"; exit 1)
	@echo "使用 DockerHub 镜像部署..."
	docker-compose -f docker-compose.prod.yml up -d

stop: check-compose
	@echo "停止服务..."
	docker-compose down

restart: check-compose
	@echo "重启服务..."
	docker-compose restart

logs: check-compose
	docker-compose logs -f

# 测试
test:
	@echo "运行后端测试..."
	cd backend && go test -v ./...

# 清理
clean:
	@echo "清理构建文件..."
	rm -rf backend/bin
	rm -rf backend/data/logs/*
	rm -rf frontend/dist
	rm -rf frontend/node_modules/.vite
	@echo "清理完成"

# 安装依赖
deps:
	@echo "安装后端依赖..."
	cd backend && go mod download
	@echo "安装前端依赖..."
	cd frontend && npm install

# 构建离线分析工具
build-analyzer: sync-version
	@echo "构建离线分析工具..."
	cd backend && make build-analyzer
	@echo "构建完成: backend/bin/relive-analyzer"

# 运行离线分析工具
analyzer: build-analyzer
	@echo "运行离线分析工具..."
	cd backend && ./bin/relive-analyzer
