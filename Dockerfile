# 多阶段构建 - Relive 统一镜像
# Stage 1: 构建前端
FROM node:20-alpine AS frontend-builder

WORKDIR /frontend

# 复制前端依赖文件
COPY frontend/package*.json ./
RUN npm ci

# 复制前端源代码
COPY frontend/ ./

# 复制 VERSION 文件（vite.config.ts 需要读取）
COPY VERSION ../VERSION

# 构建前端（生产环境）
RUN npm run build

# Stage 2: 构建后端
FROM golang:1.26-alpine AS backend-builder

WORKDIR /app

# 安装依赖（包括 g++ 用于编译 goheif/libde265）
RUN apk add --no-cache gcc g++ musl-dev sqlite-dev

# 配置 Go Proxy（支持国内网络环境）
ARG GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

# 复制 go mod 文件
COPY backend/go.mod backend/go.sum ./
RUN go mod download

# 复制后端源代码
COPY backend/ ./

# 复制 VERSION 文件到 version package（用于 //go:embed）
COPY VERSION ./pkg/version/VERSION

# 构建参数
ARG VERSION=dev
ARG BUILD_TIME

# 构建后端
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags "-X github.com/davidhoo/relive/pkg/version.BuildTime=${BUILD_TIME} -X github.com/davidhoo/relive/pkg/version.GitCommit=${VERSION}" \
    -o relive \
    ./cmd/relive/main.go

# 构建 relive-analyzer
WORKDIR /app/cmd/relive-analyzer
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags "-X github.com/davidhoo/relive/pkg/version.BuildTime=${BUILD_TIME} -X github.com/davidhoo/relive/pkg/version.GitCommit=${VERSION}" \
    -o /app/relive-analyzer \
    .
WORKDIR /app

# Stage 3: 运行阶段
FROM alpine:latest

WORKDIR /app

# 安装运行时依赖
RUN apk add --no-cache \
    ca-certificates \
    sqlite-libs \
    sqlite \
    tzdata \
    libstdc++ \
    perl \
    exiftool \
    vips-tools

# 从构建阶段复制后端二进制文件
COPY --from=backend-builder /app/relive /app/relive
COPY --from=backend-builder /app/relive-analyzer /app/relive-analyzer
COPY --from=backend-builder /app/assets/fonts /app/fonts

# 从构建阶段复制前端静态文件
COPY --from=frontend-builder /frontend/dist /app/frontend/dist

# 复制脚本和默认配置
COPY backend/scripts/docker-entrypoint.sh /app/docker-entrypoint.sh
COPY backend/config.base.yaml /app/config.base.yaml
RUN chmod +x /app/docker-entrypoint.sh

# 创建必要的目录
RUN mkdir -p /app/data/logs /app/data/photos

# 设置时区
ENV TZ=Asia/Shanghai

# 暴露端口（只需要一个端口）
EXPOSE 8080

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/v1/system/health || exit 1

# 设置入口点（配置文件由入口脚本自动检测）
ENTRYPOINT ["/app/docker-entrypoint.sh"]
