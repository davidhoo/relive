# 本地测试指南

## 🚀 快速开始

### 方式 1：使用测试脚本（推荐）

```bash
./test-local.sh
```

脚本会自动：
- 启动容器（端口 18080）
- 测试 API 和前端
- 显示访问信息和常用命令

### 方式 2：手动运行

```bash
# 启动容器
docker run -d \
  --name relive-test \
  -p 18080:8080 \
  -e JWT_SECRET=test-jwt-secret-for-local-testing \
  -v $(pwd)/test-data:/app/data \
  -v $(pwd)/backend/config.prod.yaml:/app/config.yaml:ro \
  davidhu/relive:v0.3.0

# 查看日志
docker logs -f relive-test

# 测试访问
open http://localhost:18080
```

## 📍 访问地址

| 服务 | 地址 | 说明 |
|------|------|------|
| **前端界面** | http://localhost:18080 | 主界面 |
| **API 文档** | http://localhost:18080/api/v1/system/health | 健康检查 |
| **登录** | http://localhost:18080 | admin / admin |

## 🔍 常用命令

```bash
# 查看日志
docker logs -f relive-test-local

# 进入容器
docker exec -it relive-test-local sh

# 停止容器
docker stop relive-test-local

# 删除容器
docker rm -f relive-test-local

# 查看容器状态
docker ps | grep relive

# 测试 API
curl http://localhost:18080/api/v1/system/health | jq

# 重启容器
docker restart relive-test-local
```

## 📊 功能测试

### 1. 测试前端界面
```bash
open http://localhost:18080
```

**测试项**：
- ✅ 页面能正常加载
- ✅ 登录功能正常（admin/admin）
- ✅ 导航菜单显示正常
- ✅ 静态资源（CSS/JS）加载正常

### 2. 测试后端 API
```bash
# 健康检查
curl http://localhost:18080/api/v1/system/health

# 系统信息
curl http://localhost:18080/api/v1/system/environment

# 照片列表（需要登录）
curl http://localhost:18080/api/v1/photos
```

### 3. 测试照片扫描（可选）

如果想测试照片功能，需要挂载照片目录：

```bash
# 停止现有容器
docker stop relive-test-local
docker rm relive-test-local

# 重新启动并挂载照片目录
docker run -d \
  --name relive-test-local \
  -p 18080:8080 \
  -e JWT_SECRET=test-jwt-secret \
  -v $(pwd)/test-data:/app/data \
  -v $(pwd)/backend/config.prod.yaml:/app/config.yaml:ro \
  -v /你的照片目录:/app/photos:ro \
  davidhu/relive:v0.3.0
```

然后在 Web 界面中添加扫描路径：`/app/photos`

## 🎯 验证单镜像架构

### 验证前端静态文件由 Go 提供

```bash
# 查看前端请求日志
docker logs relive-test-local 2>&1 | grep "GET.*/"

# 应该看到类似：
# [GIN] GET "/"
# [GIN] GET "/assets/index-*.js"
# [GIN] GET "/assets/index-*.css"
```

### 验证单容器运行

```bash
# 应该只有一个 relive 容器
docker ps | grep relive
# relive-test-local   davidhu/relive:v0.3.0   18080->8080
```

### 验证镜像大小

```bash
docker images davidhu/relive:v0.3.0
# 应该显示约 49.2MB
```

## 🧪 性能测试

### 响应时间测试
```bash
# API 响应时间
time curl -s http://localhost:18080/api/v1/system/health > /dev/null

# 前端加载时间
time curl -s http://localhost:18080/ > /dev/null
```

### 并发测试（可选）
```bash
# 安装 ab (Apache Bench)
# macOS: brew install httpd (包含 ab)

# 测试 API
ab -n 100 -c 10 http://localhost:18080/api/v1/system/health

# 测试前端
ab -n 100 -c 10 http://localhost:18080/
```

## 🛠️ 故障排查

### 容器无法启动
```bash
# 查看详细错误
docker logs relive-test-local

# 检查端口占用
lsof -i :18080

# 检查配置文件
cat backend/config.prod.yaml
```

### 前端无法访问
```bash
# 检查静态文件是否存在
docker exec relive-test-local ls -la /app/frontend/dist/

# 检查配置
docker exec relive-test-local cat /app/config.yaml | grep static_path
```

### API 返回 404
```bash
# 检查路由配置
docker logs relive-test-local 2>&1 | grep "Server listening"

# 测试不同的 API 路径
curl http://localhost:18080/api/v1/system/health
curl http://localhost:18080/system/health
```

## 🎉 测试完成后

```bash
# 清理测试环境
docker stop relive-test-local
docker rm relive-test-local

# 删除测试数据（可选）
rm -rf test-data/
```

## 📝 测试检查清单

- [ ] 容器能正常启动
- [ ] 前端页面能访问（http://localhost:18080）
- [ ] 能成功登录（admin/admin）
- [ ] API 健康检查返回 200
- [ ] 静态资源正常加载（CSS/JS）
- [ ] 日志没有错误信息
- [ ] 单个容器包含前端+后端
- [ ] 镜像大小约 49MB
- [ ] 响应时间 < 100ms

## 💡 提示

- 默认端口 18080 可以避免与其他服务冲突
- 测试数据保存在 `./test-data/` 目录
- 首次启动会自动创建默认管理员账号
- 如需测试 AI 功能，需配置 AI Provider
