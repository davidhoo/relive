# 多阶段构建 - 前端
FROM node:20-alpine AS frontend-builder

WORKDIR /app

# 复制 package 文件
COPY package*.json ./

# 安装依赖
RUN npm ci --legacy-peer-deps

# 复制源代码
COPY . .

# 构建前端
RUN npm run build

# 生产阶段 - 使用 nginx
FROM nginx:alpine

# 复制构建产物
COPY --from=frontend-builder /app/dist /usr/share/nginx/html

# 复制 nginx 配置（如果有自定义配置）
# COPY nginx.conf /etc/nginx/nginx.conf

# 暴露端口
EXPOSE 80

# 健康检查
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost/ || exit 1

CMD ["nginx", "-g", "daemon off;"]
