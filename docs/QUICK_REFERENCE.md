# Relive 开发速查卡

> 当前维护版，优先对应仓库现状而不是历史工具链。

## 真值文件

- 版本：`VERSION`
- 后端路由：`backend/internal/api/v1/router/router.go`
- analyzer CLI：`backend/cmd/relive-analyzer/main.go`
- people-worker CLI：`backend/cmd/relive-people-worker/main.go`
- 前端路由：`frontend/src/router/index.ts`
- 源码部署模板：`docker-compose.yml.example`
- 镜像部署模板：`docker-compose.prod.yml.example`
- analyzer 模板：`analyzer.yaml.example`
- people-worker 模板：`people-worker.yaml.example`
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

# people-worker (Mac M4 人脸检测加速)
make build-people-worker
./backend/bin/relive-people-worker gen-config > people-worker.yaml
./backend/bin/relive-people-worker check -config people-worker.yaml
./backend/bin/relive-people-worker run -config people-worker.yaml
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

## 人物身份画像运行状态 API（只读，需认证）

- `GET /api/v1/people/identity-profiles/stats` — 返回 mode、profile/center/member/backfill/ANN/decision 汇总
- `GET /api/v1/people/identity-profiles/decisions?limit=50` — 返回最近决策遥测（limit 1–200，默认 50）

安全说明：

- 两个接口都需要 JWT 认证，仅支持 GET。
- 只读：不返回 embedding、图片路径、缩略图路径、人物名称或 API key。
- 不提供运行时修改模式、rebuild 或 rescue 的接口；模式仍通过 YAML 配置 + 服务重启管理。
- 生产默认 `legacy`：stats 返回 `mode=legacy` 与零值运行状态，不查询画像表/ANN/AppConfig。
- 显式调用 decisions 接口时允许读取历史 decision 记录（用户触发的只读查询，不属于后台画像负载）。

示例：

```bash
curl -H "Authorization: Bearer <token>" \
  http://localhost:8080/api/v1/people/identity-profiles/stats
```

> 不要在文档或外发材料中写真实 token、NAS 地址或用户数据。

## People Worker API (API Key 认证)

- `GET /api/v1/people/worker/tasks` - 获取待处理任务
- `POST /api/v1/people/worker/tasks/:task_id/heartbeat` - 任务心跳
- `POST /api/v1/people/worker/tasks/:task_id/release` - 释放任务
- `POST /api/v1/people/worker/results` - 提交检测结果
- `POST /api/v1/people/runtime/acquire` - 获取运行时租约
- `POST /api/v1/people/runtime/heartbeat` - 运行时心跳
- `POST /api/v1/people/runtime/release` - 释放运行时租约

## 展示策略补充

- `photos.top_person_category` 会作为照片层人物信号参与展示排序
- 人物优先级为：`family > friend > acquaintance > stranger`
- `people_spotlight` 会优先使用真实人物数据支持的事件，其次才退回到 `PrimaryTag` 猜测

## 当前 analyzer 说明

- 使用 API 模式
- 不再以 `export.db` 作为默认工作流
- 认证依赖”设备管理”中创建出来的 `api_key`

## 当前 people-worker 说明

- 用于 Mac M4 等高性能设备加速人脸检测
- 需要本地运行 `relive-ml` 服务
- 与 `relive-analyzer` 可以同时运行
- 详见 `docs/PEOPLE_WORKER_API_MODE.md`

## 阅读顺序

1. `README.md`
2. `QUICKSTART.md`
3. `docs/CONFIGURATION.md`
4. `docs/BACKEND_API.md`
5. `docs/ANALYZER_API_MODE.md`
6. `docs/ARCHITECTURE.md`
