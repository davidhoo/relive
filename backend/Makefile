.PHONY: all build build-analyzer run run-analyzer test clean deps lint fmt

# 变量
APP_NAME=relive
ANALYZER_NAME=relive-analyzer
PEOPLE_WORKER_NAME=relive-people-worker
VERSION=$(shell cat ../VERSION 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# 使用 pkg/version 包的 LDFLAGS
LDFLAGS=-ldflags "-X github.com/davidhoo/relive/pkg/version.BuildTime=${BUILD_TIME} -X github.com/davidhoo/relive/pkg/version.GitCommit=${GIT_COMMIT}"

# 默认目标：构建所有
all: build build-analyzer build-people-worker

# 构建主程序
build:
	@echo "Building $(APP_NAME)..."
	@mkdir -p bin
	go build ${LDFLAGS} -o bin/$(APP_NAME) cmd/relive/main.go
	@echo "Build complete: bin/$(APP_NAME)"

# 构建离线分析工具
build-analyzer:
	@echo "Building $(ANALYZER_NAME)..."
	@mkdir -p bin
	go build ${LDFLAGS} -o bin/$(ANALYZER_NAME) ./cmd/relive-analyzer
	@echo "Build complete: bin/$(ANALYZER_NAME)"

# 构建人物检测 Worker
build-people-worker:
	@echo "Building $(PEOPLE_WORKER_NAME)..."
	@mkdir -p bin
	go build ${LDFLAGS} -o bin/$(PEOPLE_WORKER_NAME) ./cmd/relive-people-worker
	@echo "Build complete: bin/$(PEOPLE_WORKER_NAME)"

# 运行主程序
run:
	@echo "Running $(APP_NAME)..."
	@mkdir -p data/logs data/photos
	test -f config.dev.yaml || cp config.dev.yaml.example config.dev.yaml
	go run cmd/relive/main.go --config config.dev.yaml

# 运行离线分析工具（显示帮助）
run-analyzer:
	@echo "Running $(ANALYZER_NAME)..."
	@mkdir -p bin
	go run ./cmd/relive-analyzer

# 测试
test:
	@echo "Running tests..."
	go test -v ./...

# 测试覆盖率
test-coverage:
	@echo "Running tests with coverage..."
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# 代码检查
lint:
	@echo "Running linter..."
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run

# 格式化
fmt:
	@echo "Formatting code..."
	gofmt -w .
	@which goimports > /dev/null || (echo "Installing goimports..." && go install golang.org/x/tools/cmd/goimports@latest)
	goimports -w .

# 清理
clean:
	@echo "Cleaning..."
	rm -rf bin/
	rm -f coverage.out coverage.html
	@echo "Clean complete"

# 安装依赖
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy
	@echo "Dependencies installed"

# Docker 构建
docker-build:
	@echo "Building Docker image..."
	docker build -t $(APP_NAME):$(VERSION) ..

# Docker 运行
docker-run:
	@echo "Running with Docker Compose..."
	docker-compose -f ../docker-compose.yml up -d

# 帮助
help:
	@echo "Relive Development Commands:"
	@echo "  make build              - Build the main application (bin/relive)"
	@echo "  make build-analyzer     - Build the analyzer tool (bin/relive-analyzer)"
	@echo "  make build-people-worker - Build the people worker (bin/relive-people-worker)"
	@echo "  make all                - Build all binaries"
	@echo "  make run            - Run the main application"
	@echo "  make run-analyzer   - Run the analyzer (help)"
	@echo "  make test           - Run tests"
	@echo "  make test-coverage  - Generate coverage report"
	@echo "  make lint           - Run linter"
	@echo "  make fmt            - Format code"
	@echo "  make clean          - Clean build artifacts"
	@echo "  make deps           - Install dependencies"
	@echo "  make docker-build   - Build Docker image"
	@echo "  make docker-run     - Run with Docker Compose"
