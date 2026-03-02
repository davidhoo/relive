# 多阶段构建 - 后端
FROM golang:1.24-alpine AS backend-builder

WORKDIR /app

# 安装依赖
RUN apk add --no-cache gcc musl-dev sqlite-dev

# 复制 go mod 文件
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码
COPY . .

# 构建后端
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags "-X main.Version=${VERSION:-dev} -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o relive \
    ./cmd/relive/main.go

# 构建 relive-analyzer
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags "-X main.Version=${VERSION:-dev} -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o relive-analyzer \
    ./cmd/relive-analyzer/main.go

# 运行阶段
FROM alpine:latest

WORKDIR /app

# 安装运行时依赖
RUN apk add --no-cache \
    ca-certificates \
    sqlite-libs \
    tzdata

# 从构建阶段复制二进制文件
COPY --from=backend-builder /app/relive /app/relive
COPY --from=backend-builder /app/relive-analyzer /app/relive-analyzer

# 创建必要的目录
RUN mkdir -p /app/data/logs /app/data/photos

# 设置时区
ENV TZ=Asia/Shanghai

# 暴露端口
EXPOSE 8080

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/system/health || exit 1

# 运行应用
ENTRYPOINT ["/app/relive"]
CMD ["--config", "/app/config.yaml"]
