# 🚀 使用 DockerHub 镜像部署

本指南适用于直接从 DockerHub 拉取镜像部署，**无需克隆源码**。

---

## 📋 三种部署方式

### 🌟 方式 1：一键安装（推荐，最简单）

适合新用户，自动完成所有配置。

```bash
curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/install.sh | bash
```

**脚本会自动：**
- ✅ 检查 Docker 环境
- ✅ 创建安装目录（默认 `~/relive`）
- ✅ 下载配置文件
- ✅ 生成 JWT 密钥
- ✅ 拉取 DockerHub 镜像
- ✅ 启动服务

**完成后访问：** `http://your-ip:8080`

---

### 🔧 方式 2：手动配置（推荐，可控）

适合需要自定义配置的用户。

#### 步骤 1：创建目录

```bash
mkdir -p ~/relive
cd ~/relive
```

#### 步骤 2：下载配置文件

```bash
# docker-compose 配置
curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/docker-compose.prod.yml -o docker-compose.yml

# 后端配置
curl -fsSL https://raw.githubusercontent.com/davidhoo/relive/main/backend/config.prod.yaml.example -o config.prod.yaml.example
cp config.prod.yaml.example config.prod.yaml
```

#### 步骤 3：创建 .env 文件

```bash
# 生成 JWT 密钥并写入 .env
cat > .env << EOF
# Relive 环境变量配置
JWT_SECRET=$(openssl rand -base64 32)
RELIVE_PORT=8080
AUTO_IMPORT_CITIES=true
EOF
```

#### 步骤 4：配置照片路径

编辑 `docker-compose.yml`，在 `volumes` 部分添加你的照片目录：

```yaml
services:
  relive:
    volumes:
      # 取消注释并修改为你的路径
      - /volume1/photos/2024:/app/photos/2024:ro
      - /volume1/photos/2025:/app/photos/2025:ro
```

#### 步骤 5：启动服务

```bash
# 创建数据目录
mkdir -p data/backend/logs data/backend/thumbnails

# 启动
docker-compose up -d

# 查看日志
docker-compose logs -f
```

---

### 💻 方式 3：群晖 DSM 界面部署

适合喜欢图形界面的用户。

#### 使用 Container Manager (DSM 7.2+)

1. **打开 Container Manager**

2. **项目 → 新增**
   - 项目名称：`relive`
   - 路径：`/docker/relive`

3. **设置来源**
   - 选择「创建 docker-compose.yml」
   - 粘贴以下内容：

```yaml
version: '3.8'
services:
  relive:
    image: davidhu/relive:latest
    container_name: relive
    restart: unless-stopped
    ports:
      - "8080:8080"
    volumes:
      - ./config.prod.yaml:/app/config.yaml:ro
      - ./data/backend:/app/data
      # 修改为你的照片路径
      - /volume1/photos:/app/photos:ro
    environment:
      - TZ=Asia/Shanghai
      - JWT_SECRET=${JWT_SECRET}
```

4. **设置环境变量**
   - 添加变量：`JWT_SECRET`
   - 值：使用生成的随机密钥（32字节 base64）

5. **上传配置文件**
   - 将 `config.prod.yaml.example` 上传到 `/docker/relive/`
   - 然后复制为 `config.prod.yaml` 并按需修改

6. **构建并启动**

#### 使用传统 Docker 应用 (DSM 7.0-7.1)

1. **下载镜像**
   - 打开 Docker 应用
   - 注册表 → 搜索 `davidhu/relive`
   - 下载 latest 标签

2. **创建容器**
   - 映像 → `davidhu/relive:latest` → 启动
   - 容器名称：`relive`
   - 端口设置：
     - 本地端口：`8080` → 容器端口：`8080`
   - 卷：
     - `/docker/relive/config.prod.yaml` → `/app/config.yaml` (只读)
     - `/docker/relive/data/backend` → `/app/data`
     - `/volume1/photos` → `/app/photos` (只读)
   - 环境：
     - `TZ` = `Asia/Shanghai`
     - `JWT_SECRET` = `<生成的密钥>`

---

## 🔑 生成 JWT 密钥

### 方法 1：使用 OpenSSL（推荐）

```bash
openssl rand -base64 32
```

### 方法 2：使用在线工具

访问：https://generate-random.org/api-key-generator?count=1&length=32&type=base64

### 方法 3：在群晖上使用

```bash
# SSH 到群晖
ssh admin@your-nas-ip

# 生成密钥
head -c 32 /dev/urandom | base64
```

---

## 📁 目录结构

部署后的目录结构：

```
~/relive/                          # 安装目录
├── docker-compose.yml             # Docker Compose 配置
├── config.prod.yaml.example       # 后端配置模板
├── config.prod.yaml               # 本地生成的后端配置
├── .env                           # 环境变量（包含密钥）
└── data/                          # 数据目录
    └── backend/
        ├── relive.db              # SQLite 数据库
        ├── logs/                  # 日志文件
        └── thumbnails/            # 缩略图缓存
```

---

## 🎯 首次使用

### 1. 访问系统

打开浏览器访问：`http://your-nas-ip:8080`

### 2. 登录

- 用户名：`admin`
- 密码：`admin`

⚠️ **首次登录会强制修改密码**

### 3. 配置扫描路径

**配置管理** → **扫描路径** → 添加路径

例如：`/app/photos/2024`

### 4. 扫描照片

**照片管理** → **扫描照片** → 选择路径 → 开始扫描

### 5. 配置 AI（可选）

**配置管理** → **AI 配置** → 选择提供者

---

## 🔄 常用操作

### 查看日志

```bash
cd ~/relive
docker-compose logs -f
```

### 重启服务

```bash
cd ~/relive
docker-compose restart
```

### 停止服务

```bash
cd ~/relive
docker-compose down
```

### 更新到最新版本

```bash
cd ~/relive

# 拉取最新镜像
docker-compose pull

# 重启服务
docker-compose up -d
```

### 备份数据

```bash
# 备份数据库
cp ~/relive/data/backend/relive.db ~/relive-backup-$(date +%Y%m%d).db

# 或使用 SQLite 备份命令
sqlite3 ~/relive/data/backend/relive.db ".backup '~/relive-backup.db'"
```

---

## 🔒 安全配置

### 1. 限制端口访问

编辑 `docker-compose.yml`，只监听 localhost：

```yaml
services:
  relive:
    ports:
      - "127.0.0.1:8080:8080"  # 只允许本地访问
```

然后配置 Nginx 反向代理。

### 2. 配置 HTTPS

使用 Nginx 反向代理：

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

### 3. 配置防火墙

只允许内网访问：

```bash
# iptables 示例
iptables -A INPUT -p tcp --dport 8080 -s 192.168.1.0/24 -j ACCEPT
iptables -A INPUT -p tcp --dport 8080 -j DROP
```

详见 [SECURITY.md](../SECURITY.md)

---

## 🐛 故障排查

### 容器无法启动

```bash
# 查看容器状态
docker ps -a

# 查看日志
docker logs relive

# 检查端口占用
netstat -tlnp | grep 8080
```

### 无法访问服务

1. 检查防火墙规则
2. 检查群晖防火墙
3. 确认端口映射正确
4. 查看容器日志

### 照片无法访问

```bash
# 进入容器检查
docker exec -it relive sh
ls -la /app/photos

# 检查权限
ls -la /volume1/photos
```

### 数据库错误

```bash
# 检查数据库文件
sqlite3 ~/relive/data/backend/relive.db "PRAGMA integrity_check;"

# 查看表
sqlite3 ~/relive/data/backend/relive.db ".tables"
```

---

## 📊 性能优化

### 资源限制

编辑 `docker-compose.yml`：

```yaml
services:
  relive:
    deploy:
      resources:
        limits:
          cpus: '2.0'
          memory: 2G
        reservations:
          memory: 512M
```

### 日志限制

```yaml
services:
  relive:
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
```

---

## 📚 更多文档

- [快速开始](QUICKSTART.md) - 5分钟上手指南
- [安全指南](../SECURITY.md) - 安全配置和最佳实践
- [完整部署指南](DEPLOYMENT.md) - 详细配置说明

---

## ❓ 获取帮助

- [GitHub Issues](https://github.com/davidhoo/relive/issues)
- [Discussions](https://github.com/davidhoo/relive/discussions)
- [README](../README.md)

---

**部署时间**：5-10 分钟
**镜像大小**：~75MB（包含前端）
**推荐配置**：2核4GB内存
