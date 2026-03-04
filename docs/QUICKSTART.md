# 🚀 快速部署指南

适用于 Docker 部署到 NAS 或服务器。

---

## 📋 部署前准备

### 环境要求

- Docker 20.10+
- Docker Compose 1.29+
- 4GB+ 内存
- 50GB+ 存储空间

### 群晖 NAS 用户

1. 安装 **Docker** 套件（或 **Container Manager** for DSM 7.2+）
2. 确保有 SSH 访问权限

---

## ⚡ 一键部署

### 1. 克隆仓库

```bash
git clone https://github.com/davidhoo/relive.git
cd relive
```

### 2. 运行部署脚本

```bash
chmod +x deploy.sh
./deploy.sh
```

脚本会自动：
- ✅ 检查 Docker 环境
- ✅ 生成安全的 JWT 密钥
- ✅ 创建数据目录
- ✅ 构建前端
- ✅ 启动 Docker 服务

### 3. 访问系统

部署完成后，访问：
- **Web 界面**: http://your-nas-ip:8080

**默认账号**：
- 用户名：`admin`
- 密码：`admin`（首次登录强制修改）

---

## 📁 配置照片路径

编辑 `docker-compose.yml`，取消注释并修改照片路径：

```yaml
services:
  relive:
    volumes:
      # 修改为你的照片目录
      - /volume1/photos/2024:/app/photos/2024:ro
      - /volume1/photos/2025:/app/photos/2025:ro
```

重启服务：
```bash
docker-compose restart
```

---

## 🎯 使用流程

### 1. 登录并修改密码
访问前端 → 登录 → 强制修改密码

### 2. 添加扫描路径
**配置管理** → **扫描路径** → 添加 `/app/photos/2024`

### 3. 扫描照片
**照片管理** → **扫描照片** → 选择路径

### 4. 配置 AI（可选）
**配置管理** → **AI 配置** → 选择提供者：
- **Ollama**（本地免费）
- **Qwen API**（阿里云，¥0.004/张）
- **OpenAI**（最高质量，¥0.07/张）

### 5. AI 分析（可选）
**照片管理** → **AI 分析** → 批量分析

---

## 🔧 常用命令

```bash
# 查看服务状态
docker-compose ps

# 查看日志
docker-compose logs -f

# 重启服务
docker-compose restart

# 停止服务
docker-compose down

# 更新到最新版本
git pull
./deploy.sh
```

---

## 🔒 安全建议

### 必须完成

1. ✅ **JWT 密钥**：`deploy.sh` 自动生成
2. ✅ **修改密码**：首次登录强制修改
3. ⚠️ **限制访问**：配置防火墙只允许内网访问

### 推荐完成

4. 配置 HTTPS（使用 Nginx 反向代理）
5. 限制后端端口只监听 `127.0.0.1`
6. 定期备份数据库

详见 [SECURITY.md](../SECURITY.md)

---

## 🌐 配置 HTTPS（可选）

### 使用 Nginx 反向代理

```nginx
server {
    listen 443 ssl http2;
    server_name photos.your-domain.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### 使用群晖反向代理

**控制面板** → **登录门户** → **高级** → **反向代理服务器** → **新增**

---

## 🐛 故障排查

### 容器无法启动

```bash
# 查看日志
docker logs relive

# 检查端口占用
netstat -tlnp | grep 8080
```

### 无法访问照片

检查 `docker-compose.yml` 中的照片路径是否正确：
```bash
docker exec relive ls /app/photos
```

### AI 分析失败

查看 AI 配置是否正确：
```bash
docker logs relive | grep "AI"
```

---

## 📊 性能优化

### 群晖 NAS

```yaml
# docker-compose.yml
services:
  relive:
    deploy:
      resources:
        limits:
          cpus: '2.0'
          memory: 2G
```

### 大量照片（10万+）

- 使用 SSD 缓存
- 增加 SQLite cache_size
- 分批扫描照片

---

## 📚 更多文档

- [完整部署指南](DEPLOYMENT.md) - 详细配置说明
- [安全指南](../SECURITY.md) - 安全配置和最佳实践
- [API 文档](API_DESIGN.md) - REST API 接口文档
- [开发指南](DEVELOPMENT.md) - 开发环境搭建

---

## ❓ 获取帮助

- [GitHub Issues](https://github.com/davidhoo/relive/issues)
- [Discussions](https://github.com/davidhoo/relive/discussions)
- [README](../README.md) - 项目介绍

---

**部署时间**：约 5-10 分钟
**下一步**：开始扫描照片并享受智能展示！ 🎉
