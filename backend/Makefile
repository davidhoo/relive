.PHONY: all build run test clean deps lint fmt

# 变量
APP_NAME=relive
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}"

# 默认目标
all: build

# 构建
build:
	@echo "Building $(APP_NAME)..."
	@mkdir -p bin
	go build ${LDFLAGS} -o bin/$(APP_NAME) cmd/relive/main.go
	@echo "Build complete: bin/$(APP_NAME)"

# 运行
run:
	@echo "Running $(APP_NAME)..."
	@mkdir -p data/logs data/photos
	go run cmd/relive/main.go --config config.dev.yaml

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
	docker build -t $(APP_NAME):$(VERSION) .

# Docker 运行
docker-run:
	@echo "Running with Docker Compose..."
	docker-compose up -d

# 帮助
help:
	@echo "Relive Development Commands:"
	@echo "  make build          - Build the application"
	@echo "  make run            - Run the application"
	@echo "  make test           - Run tests"
	@echo "  make test-coverage  - Generate coverage report"
	@echo "  make lint           - Run linter"
	@echo "  make fmt            - Format code"
	@echo "  make clean          - Clean build artifacts"
	@echo "  make deps           - Install dependencies"
	@echo "  make docker-build   - Build Docker image"
	@echo "  make docker-run     - Run with Docker Compose"
