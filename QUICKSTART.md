# Relive 快速启动指南

> 目标：用当前仓库的实际部署方式，把 Relive 跑起来并完成首次扫描。

## 前置要求

- Docker 20.10+
- Docker Compose v2（可使用 `docker compose` 命令）
- 一个可挂载到容器内的照片目录
- 可选：Ollama / Qwen / OpenAI 等 AI 服务

## 1. 克隆仓库

```bash
git clone https://github.com/davidhoo/relive.git
cd relive
```

## 2. 准备已发布镜像部署文件

```bash
cp docker-compose.prod.yml.example docker-compose.prod.yml
cp .env.example .env
cp backend/config.prod.yaml.example backend/config.prod.yaml
```

至少建议修改：

```env
JWT_SECRET=replace-with-a-random-secret
```

> 当前 Docker 部署 **不通过** `.env` 中的 `PHOTOS_PATH` 指定照片目录；照片目录需要在 `docker-compose.prod.yml` 的 `volumes` 里挂载。
>
> 如果你不确定某项配置该改 `.env`、`docker-compose.prod.yml`、`analyzer.yaml` 还是后台配置页，请先阅读 `docs/CONFIGURATION.md`。

## 3. 修改照片目录挂载

编辑 `docker-compose.prod.yml`，把示例路径改成你自己的宿主机目录：

```yaml
services:
  relive:
    volumes:
      - /your/photo/library:/app/photos:ro
```

说明：
- 冒号左边是宿主机真实路径
- 冒号右边建议保持 `/app/photos`
- 后续在 Web 界面里配置扫描路径时，填的是容器内路径，例如 `/app/photos`

## 4. 启动服务

```bash
make deploy-image
```

`make deploy-image` 是普通用户默认推荐路径，会拉取已发布镜像并启动服务。

如果你正在本地改代码，需要从源码构建镜像，请先执行 `cp docker-compose.yml.example docker-compose.yml`，再使用 `make deploy` 做源码部署。

常用访问地址：
- Web：`http://localhost:8080`
- 健康检查：`http://localhost:8080/api/v1/system/health`

## 5. 首次登录与初始化

默认账号：
- 用户名：`admin`
- 密码：`admin`

首次登录后会被强制要求修改密码。

推荐初始化顺序：
1. 打开“配置管理”页面，添加扫描路径，例如 `/app/photos`
2. 打开“照片管理”页面，执行扫描或重建
3. 如需 AI 分析，在“配置管理”中配置 AI Provider
4. 打开“AI 分析”页面，启动在线分析；或使用下方的 analyzer API 模式

## 6. 使用 analyzer API 模式（可选，推荐大批量照片）

当前版本的 `relive-analyzer` 使用 **API 模式**，不再使用 `export.db` 导出/导入流程。

### 6.1 在 Web 中创建设备

进入“设备管理”页面：
- 新建设备
- 设备类型选择 `offline` 或 `service`
- 复制创建成功后显示的 `api_key`

### 6.2 生成 analyzer 配置

```bash
cp analyzer.yaml.example analyzer.yaml
```

编辑 `analyzer.yaml`，至少填写：

```yaml
server:
  endpoint: "http://your-relive-host:8080"
  api_key: "your-device-api-key"
```

### 6.3 构建并运行 analyzer

```bash
make build-analyzer
./backend/bin/relive-analyzer check -config analyzer.yaml
./backend/bin/relive-analyzer analyze -config analyzer.yaml
```

自定义并发：

```bash
./backend/bin/relive-analyzer analyze -config analyzer.yaml -workers 8
```

## 常用命令

```bash
# 查看服务状态
docker compose -f docker-compose.prod.yml ps

# 查看日志
docker compose -f docker-compose.prod.yml logs -f
docker compose -f docker-compose.prod.yml logs -f relive

# 停止服务
docker compose -f docker-compose.prod.yml down

# 重启服务
docker compose -f docker-compose.prod.yml restart

# 更新后重新部署
git pull
make deploy-image
```

## 故障排除

### 健康检查

```bash
curl http://localhost:8080/api/v1/system/health
```

当前响应结构示例：

```json
{
  "success": true,
  "data": {
    "status": "healthy",
    "version": "1.3.0",
    "uptime": 123,
    "timestamp": "2026-03-09T12:00:00+08:00"
  },
  "message": "System is healthy"
}
```

### 照片目录不可见

```bash
docker compose -f docker-compose.prod.yml exec relive ls -la /app/photos
```

如果目录不存在或为空，优先检查 `docker-compose.prod.yml` 的挂载路径。

如果同一台机器同时保留了 `docker-compose.yml` 和 `docker-compose.prod.yml`，排查镜像部署问题时优先使用显式带 `-f docker-compose.prod.yml` 的命令，避免误操作到源码部署栈。

更多配置分层说明见：`docs/CONFIGURATION.md`。

### analyzer 无法连接服务

先检查配置：
- `server.endpoint` 是否能从 analyzer 所在机器访问
- `server.api_key` 是否来自“设备管理”中新创建的设备
- 设备是否仍处于启用状态
