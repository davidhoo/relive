# Relive 开发速查卡

> 当前维护版，优先对应仓库现状而不是历史工具链。

## 真值文件

- 版本：`VERSION`
- 后端路由：`backend/internal/api/v1/router/router.go`
- analyzer CLI：`backend/cmd/relive-analyzer/main.go`
- 前端路由：`frontend/src/router/index.ts`
- 源码部署模板：`docker-compose.yml.example`
- 镜像部署模板：`docker-compose.prod.yml.example`
- analyzer 模板：`analyzer.yaml.example`
- 配置职责：`docs/CONFIGURATION.md`
- 生成配置：`backend/config.dev.yaml.example` / `backend/config.prod.yaml.example`

## 常用命令

```bash
# 本地开发
make dev

# 构建与部署
make build
make deploy-image
make deploy

# 查看日志
make logs

# 服务控制
make stop
make restart

# 后端测试
make test

# 清理
make clean

# analyzer
make build-analyzer
./backend/bin/relive-analyzer check -config analyzer.yaml
./backend/bin/relive-analyzer analyze -config analyzer.yaml
```

注：若同一台机器同时存在 `docker-compose.yml` 和 `docker-compose.prod.yml`，`make logs` / `make stop` / `make restart` 默认优先作用于 `docker-compose.yml`。排查镜像部署时，优先显式使用 `docker compose -f docker-compose.prod.yml ...`。

## 当前前端页面

- `/dashboard`
- `/photos`
- `/analysis`
- `/thumbnails`
- `/geocode`
- `/devices`
- `/events`
- `/display`
- `/config`
- `/system`
- `/login`
- `/change-Password`

## 当前 analyzer 说明

- 使用 API 模式
- 不再以 `export.db` 作为默认工作流
- 认证依赖“设备管理”中创建出来的 `api_key`

## 阅读顺序

1. `README.md`
2. `QUICKSTART.md`
3. `docs/CONFIGURATION.md`
4. `docs/BACKEND_API.md`
5. `docs/ANALYZER_API_MODE.md`
6. `docs/ARCHITECTURE.md`
