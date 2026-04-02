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
- `/people`
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

人物系统补充：
- `/people`：人物列表 + 后台任务标签页
- `/people/:id`：人物详情（改名、改类别、改头像、拆分、移动、合并）
- `/photos/:id`：照片详情页内含人物分组和人脸样本区

## 当前人物相关 API

- `GET /api/v1/people`
- `GET /api/v1/people/:id`
- `GET /api/v1/people/:id/photos`
- `GET /api/v1/people/:id/faces`
- `PATCH /api/v1/people/:id/category`
- `PATCH /api/v1/people/:id/name`
- `PATCH /api/v1/people/:id/avatar`
- `POST /api/v1/people/merge`
- `POST /api/v1/people/split`
- `POST /api/v1/people/move-faces`
- `GET /api/v1/people/task`
- `GET /api/v1/people/stats`
- `GET /api/v1/people/background/logs`
- `GET /api/v1/photos/:id/people`
- `GET /api/v1/faces/:id/thumbnail`

## 展示策略补充

- `photos.top_person_category` 会作为照片层人物信号参与展示排序
- 人物优先级为：`family > friend > acquaintance > stranger`
- `people_spotlight` 会优先使用真实人物数据支持的事件，其次才退回到 `PrimaryTag` 猜测

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
