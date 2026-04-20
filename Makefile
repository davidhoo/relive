# Makefile for Relive Project
# 禁用隐式规则，避免 deploy.sh 被自动转换为 deploy
MAKEFLAGS += --no-builtin-rules

# 自动检测 docker compose v2 或 v1
DOCKER_COMPOSE := $(shell docker compose version >/dev/null 2>&1 && echo "docker compose" || echo "docker-compose")
SOURCE_COMPOSE_FILE := docker-compose.yml
IMAGE_COMPOSE_FILE := docker-compose.prod.yml
RUNTIME_COMPOSE_FILE := $(firstword $(wildcard $(SOURCE_COMPOSE_FILE) $(IMAGE_COMPOSE_FILE)))
RUNTIME_COMPOSE_ARGS := $(if $(RUNTIME_COMPOSE_FILE),-f $(RUNTIME_COMPOSE_FILE),)

.PHONY: help dev build deploy deploy-image prod stop restart logs clean test deps sync-version build-analyzer analyzer dev-backend dev-frontend check-compose check-runtime-compose

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
	@echo "开发环境:"
	@echo "  make dev              - 启动本地开发环境"
	@echo ""
	@echo "部署:"
	@echo "  make deploy-image     - 使用已发布镜像部署（推荐）"
	@echo "  make deploy           - 从源码本地构建并部署"
	@echo "  make build            - 构建 Docker 镜像"
	@echo "  make stop             - 停止所有服务"
	@echo "  make restart          - 重启服务"
	@echo "  make logs             - 查看日志"
	@echo ""
	@echo "测试和清理:"
	@echo "  make test             - 运行测试"
	@echo "  make clean            - 清理构建文件"
	@echo ""
	@echo "工具:"
	@echo "  make build-analyzer       - 构建离线分析工具"
	@echo "  make build-people-worker  - 构建人物检测 Worker（Mac M4）"
	@echo ""

# 开发环境
dev:
	./dev.sh

dev-backend: sync-version
	test -f backend/config.dev.yaml || cp backend/config.dev.yaml.example backend/config.dev.yaml
	cd backend && go run ./cmd/relive --config config.dev.yaml

dev-frontend:
	cd frontend && npm run dev

# Docker Compose 配置检查
check-compose:
	@test -f $(SOURCE_COMPOSE_FILE) || (echo "错误: $(SOURCE_COMPOSE_FILE) 不存在"; echo "请运行: cp docker-compose.yml.example $(SOURCE_COMPOSE_FILE)"; exit 1)

check-runtime-compose:
	@test -n "$(RUNTIME_COMPOSE_FILE)" || (echo "错误: 未找到 docker-compose.yml 或 docker-compose.prod.yml"; echo "请运行: cp docker-compose.yml.example docker-compose.yml 或 cp docker-compose.prod.yml.example docker-compose.prod.yml"; exit 1)

# 生产部署
build: sync-version check-compose
	@echo "构建 Docker 镜像..."
	$(DOCKER_COMPOSE) build

deploy: check-compose
	@echo "本地构建并部署..."
	./deploy.sh

deploy-image:
	@echo "使用已发布镜像部署..."
	./deploy-image.sh

prod: deploy-image

stop: check-runtime-compose
	@echo "停止服务..."
	$(DOCKER_COMPOSE) $(RUNTIME_COMPOSE_ARGS) down

restart: check-runtime-compose
	@echo "重启服务..."
	$(DOCKER_COMPOSE) $(RUNTIME_COMPOSE_ARGS) restart

logs: check-runtime-compose
	$(DOCKER_COMPOSE) $(RUNTIME_COMPOSE_ARGS) logs -f

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

# 构建人物检测 Worker
build-people-worker: sync-version
	@echo "构建人物检测 Worker..."
	cd backend && make build-people-worker
	@echo "构建完成: backend/bin/relive-people-worker"
