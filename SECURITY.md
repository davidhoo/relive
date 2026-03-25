# 🔒 Relive 安全指南

本文档提供 Relive 的安全配置建议和最佳实践。

---

## 📋 目录

- [快速部署安全检查清单](#快速部署安全检查清单)
- [认证与授权](#认证与授权)
- [网络安全](#网络安全)
- [数据安全](#数据安全)
- [Docker 安全](#docker-安全)
- [API 安全](#api-安全)
- [监控与审计](#监控与审计)
- [已知限制](#已知限制)

---

## 快速部署安全检查清单

在生产环境部署前，请确认以下事项：

### ✅ 必须完成（生产部署前）

- [ ] **JWT 密钥**：使用 `deploy.sh` 自动生成强随机密钥（或手动生成 32 字节）
- [ ] **修改默认密码**：首次登录后立即修改 admin 默认密码
- [ ] **限制端口暴露**：后端端口只监听 `127.0.0.1`（通过 Nginx 反向代理访问）
- [ ] **配置 HTTPS**：使用 Let's Encrypt 或自签名证书
- [ ] **照片访问认证**：已内置 JWT/API Key 认证（见下方说明）

### 🔶 强烈推荐

- [ ] **防火墙规则**：限制只允许必要的 IP 访问
- [ ] **反向代理**：使用 Nginx/Traefik 添加安全头
- [ ] **定期备份**：配置自动备份数据库
- [ ] **日志监控**：监控异常登录和 API 调用
- [ ] **更新及时性**：订阅 GitHub Release 通知

### 💡 可选优化

- [ ] API 请求频率限制
- [ ] 容器以非 root 用户运行
- [ ] 启用 Docker 日志限制
- [ ] 配置 Fail2Ban 防暴力破解

---

## 认证与授权

### 默认管理员账号

**首次部署**时，系统自动创建：
- 用户名：`admin`
- 密码：`admin`

⚠️ **首次登录强制修改密码**，系统会阻止未修改密码的用户访问其他功能。

### JWT Token 安全

**自动配置**（推荐）：
```bash
# 使用 deploy.sh 自动生成
./deploy.sh
```

**手动配置**：
```bash
# 生成强随机密钥
openssl rand -base64 32

# 写入 .env 文件
echo "JWT_SECRET=<生成的密钥>" >> .env
```

**安全建议**：
- ✅ JWT 密钥至少 32 字节
- ✅ 使用加密随机生成器
- ✅ 定期轮换密钥（建议 6 个月）
- ❌ 不要将 .env 文件提交到 Git

### API Key 管理

**用于设备和 relive-analyzer 访问**：
- 在 Web 界面「配置管理」→「API Keys」创建
- 支持自定义名称和过期时间
- 支持随时撤销

**安全建议**：
- ✅ 为不同设备/用途创建不同的 API Key
- ✅ 设置合理的过期时间
- ✅ 定期审查和清理未使用的 Key
- ⚠️ 通过 Header 传递（推荐），URL 参数仅用于 `<img>` 标签直链场景

---

## 网络安全

### 端口配置

**推荐配置**（只监听 localhost）：
```yaml
# docker-compose.yml
services:
  relive:
    ports:
      - "127.0.0.1:8080:8080"  # 只监听 localhost
```

**如需外部访问**，使用反向代理：
```nginx
# nginx.conf
server {
    listen 443 ssl http2;
    server_name photos.your-domain.com;

    ssl_certificate /path/to/cert.pem;
    ssl_certificate_key /path/to/key.pem;

    # 安全头
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header X-XSS-Protection "1; mode=block" always;
    add_header Referrer-Policy "no-referrer-when-downgrade" always;
    add_header Content-Security-Policy "default-src 'self'; img-src 'self' data:; script-src 'self'; style-src 'self' 'unsafe-inline';" always;

    # 隐藏版本信息
    server_tokens off;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

        # 请求频率限制
        limit_req zone=api burst=20 nodelay;
    }
}

# 频率限制配置
limit_req_zone $binary_remote_addr zone=api:10m rate=100r/m;
limit_req_zone $binary_remote_addr zone=login:10m rate=5r/m;
```

### CORS 配置

**生产环境配置**（需在代码中实现）：
```go
// 通过环境变量配置允许的域名
allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
if allowedOrigins == "" {
    allowedOrigins = "https://photos.your-domain.com"
}

corsConfig := cors.Config{
    AllowOrigins: strings.Split(allowedOrigins, ","),
    // ... 其他配置
}
```

**环境变量设置**：
```bash
# .env
ALLOWED_ORIGINS=https://photos.your-domain.com,https://photos-backup.your-domain.com
```

---

## 数据安全

### 照片访问控制

✅ **当前状态**（v1.0.0+）：

照片访问 API **需要认证**，支持以下方式：
```
GET /api/v1/photos/:id/image
GET /api/v1/photos/:id/thumbnail
```

**支持的认证方式**（满足任一即可）：

1. **JWT Token**（Web 前端推荐）
   ```http
   Authorization: Bearer <jwt_token>
   ```

2. **API Key**（设备访问）
   ```http
   X-API-Key: <api_key>
   # 或
   Authorization: Bearer <api_key>
   ```

3. **URL 参数**（图片直链，如 `<img>` 标签）
   ```
   GET /api/v1/photos/:id/image?token=<jwt_token>
   ```

**安全建议**：
- ✅ Web 端使用 JWT Token，有效期 24 小时
- ✅ 设备使用 API Key，可单独撤销
- ✅ 图片直链 Token 避免长期暴露
- ⚠️ API Key 建议通过 Header 传递（URL 参数可能被记录在日志中）

### 数据库安全

**SQLite 文件保护**：
```bash
# 设置合适的文件权限
chmod 600 data/backend/relive.db

# 定期备份
0 2 * * * /path/to/backup.sh
```

**备份脚本示例**：
```bash
#!/bin/bash
BACKUP_DIR="/path/to/backups"
DATE=$(date +%Y%m%d)

# 使用 SQLite 的在线备份
sqlite3 data/backend/relive.db ".backup '$BACKUP_DIR/relive-$DATE.db'"

# 压缩
gzip "$BACKUP_DIR/relive-$DATE.db"

# 删除 7 天前的备份
find $BACKUP_DIR -name "relive-*.db.gz" -mtime +7 -delete
```

### 敏感数据加密

**已加密**：
- ✅ 用户密码（bcrypt，cost=10）
- ✅ API Keys（数据库中加密存储）

**未加密**（明文存储）：
- ⚠️ AI Provider API Keys（在数据库 app_config 表）
- ⚠️ 照片 EXIF GPS 坐标

**建议**：
- 数据库文件本身使用文件系统加密（LUKS/VeraCrypt）
- 或部署在加密卷上（群晖 NAS 支持卷加密）

---

## Docker 安全

### 容器权限

**当前配置**（需改进）：
```dockerfile
# 容器以 root 运行
USER root
```

**推荐配置**（计划优化）：
```dockerfile
# 创建非特权用户
RUN addgroup -g 1000 relive && \
    adduser -D -u 1000 -G relive relive && \
    chown -R relive:relive /app

USER relive
```

**临时解决方案**（docker-compose.yml）：
```yaml
services:
  relive-backend:
    user: "1000:1000"  # 使用你的 UID:GID
    # 或使用群晖用户
    user: "${UID}:${GID}"
```

### 资源限制

**防止资源耗尽**：
```yaml
services:
  relive-backend:
    deploy:
      resources:
        limits:
          cpus: '2.0'
          memory: 2G
        reservations:
          memory: 512M
```

### 日志限制

**防止磁盘占满**：
```yaml
services:
  relive-backend:
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "3"
```

---

## API 安全

### 请求频率限制

**当前状态**：✅ 已实现（登录接口）

- 登录接口：内置速率限制（`middleware.LoginRateLimit()`）
- 通用 API / 扫描接口：建议通过 Nginx 配置

**Nginx 配置示例**（可选加强）：
```nginx
# 登录接口限制
location /api/v1/auth/login {
    limit_req zone=login burst=3 nodelay;
    limit_req_status 429;
    proxy_pass http://relive-backend:8080;
}

# 扫描接口限制
location ~ ^/api/v1/photos/(scan|rebuild) {
    limit_req zone=scan burst=1;
    proxy_pass http://relive-backend:8080;
}
```

### 输入验证

**已实现**：
- ✅ JSON 参数验证（使用 Gin binding）
- ✅ 路径遍历防护（路径清理）
- ✅ SQL 注入防护（GORM 参数化查询）

**需注意**：
- ⚠️ 文件上传未实现（计划功能）
- ⚠️ 路径验证可加强（防止 `../` 攻击）

---

## 监控与审计

### 日志记录

**记录的事件**：
- ✅ 用户登录/登出
- ✅ API 调用错误
- ✅ 数据库操作失败
- ✅ AI 分析任务

**日志位置**：
```
data/backend/logs/relive.log
```

**查看日志**：
```bash
# 实时查看
docker logs -f relive-backend

# 查询失败的登录
grep "login failed" data/backend/logs/relive.log

# 查询 API 错误
grep "ERROR" data/backend/logs/relive.log | grep "401\|403\|500"
```

### 异常监控

**建议配置告警**：
```bash
# 使用 logwatch 或自定义脚本
# 监控关键词：ERROR, WARN, "unauthorized", "failed"

# 示例：检测暴力破解
#!/bin/bash
THRESHOLD=10
FAILURES=$(grep -c "login failed" data/backend/logs/relive.log)

if [ $FAILURES -gt $THRESHOLD ]; then
    echo "警告：检测到 $FAILURES 次登录失败" | mail -s "Relive Security Alert" admin@example.com
fi
```

---

## 已知限制

### 当前版本

| 问题 | 严重性 | 状态 |
|------|--------|------|
| 容器以 root 运行 | 🟡 低 | 已知，计划优化 |
| AI Provider API Key 明文存储 | 🟠 中 | 已知，建议数据库文件权限保护 |

### 已修复

- ✅ 照片访问添加 JWT/API Key 认证（v1.0.0）
- ✅ HttpOnly Cookie 认证照片资源（v1.2.1）
- ✅ 登录速率限制（v1.1.0）
- ✅ SQLite 并发锁竞争优化（v1.0.0）
- ✅ 代码审查问题修复 — Data Race、调试日志等（v1.0.0）
- ✅ CORS 仅 debug 模式启用（v1.2.1）

---

## 安全报告

如发现安全漏洞，请通过以下方式报告：

1. **私下报告**（推荐）：
   - GitHub Private Security Advisories
   - 邮件：[安全邮箱]

2. **不要**：
   - 公开 Issue
   - 公开 Pull Request

我们会在 48 小时内响应，并在修复后公开致谢。

---

## 更新日志

### v1.3.1 (2026-03-25)
- ✅ sort_by 参数 SQL 注入白名单校验
- ✅ analyzer 类型断言 comma-ok 安全模式

### v1.3.0 (2026-03-17)
- ✅ 事件聚类 + 多维策展引擎（6 通道）
- ✅ 照片旋转重构（manual_rotation）
- ✅ SQLite 并发锁竞争优化
- ✅ SyncTags 嵌套事务自死锁修复
- ✅ MIT License

### v1.2.1 (2026-03-16)
- ✅ HttpOnly Cookie 认证照片资源（替代 URL Token 暴露 JWT）
- ✅ CORS 仅 debug 模式启用
- ✅ 弱 JWT 密钥启动警告
- ✅ 枚举字段 CHECK 约束

### v1.1.0 (2026-03-14)
- ✅ 登录速率限制（防暴力破解）

### v1.0.0 (2026-03-10)
- ✅ 照片访问添加 JWT/API Key 认证
- ✅ SQLite 并发优化
- ✅ 修复 AIHandler Data Race
- ✅ 统一版本管理

---

## 参考资源

- [OWASP Top 10](https://owasp.org/www-project-top-ten/)
- [Docker Security Best Practices](https://docs.docker.com/engine/security/)
- [Nginx Security](https://nginx.org/en/docs/http/ngx_http_ssl_module.html)

---

**最后更新**：2026-03-25
**下次审查**：2026-06-10
