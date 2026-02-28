---
name: warn-docker-volume-delete
enabled: true
event: bash
pattern: docker-compose\s+down.*-v|docker\s+volume\s+(rm|prune)
action: warn
---

⚠️ **Docker 数据卷删除警告**

你正在执行可能删除 Docker 数据卷的命令。

**Relive 项目数据卷包含**：
- 📸 照片分析结果数据库
- ⚙️ 系统配置
- 📊 展示历史记录

**提醒**：
- `docker-compose down -v` 会删除所有数据卷
- 删除后数据无法恢复
- 如果只是重启服务，使用 `docker-compose restart` 或 `docker-compose down`（不带 -v）

**安全操作**：
```bash
# 仅重启服务
docker-compose restart

# 停止服务（保留数据）
docker-compose down

# 重新构建（保留数据）
docker-compose up --build
```
