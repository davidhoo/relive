# relive-analyzer API 模式说明

> 版本：v1.0 对齐版
> 更新日期：2026-03-09
> 状态：已实现并在当前仓库中使用

## 概述

当前版本的 `relive-analyzer` 通过 HTTP API 与 Relive 主服务通信：
- 主服务负责分配待分析照片任务
- analyzer 负责下载图片、调用 AI、提交结果
- 不再依赖旧版 `export.db` 导出 / 导入工作流

源码真值：
- 服务端路由：`backend/internal/api/v1/router/router.go`
- analyzer CLI：`backend/cmd/relive-analyzer/main.go`
- 配置模板：`analyzer.yaml.example`（仓库根目录唯一 analyzer 模板）

---

## 接入流程

### 1. 在 Web 后台创建设备

进入“设备管理”页面，新建设备：
- 设备类型建议选择 `offline` 或 `service`
- 创建成功后复制 `api_key`

### 2. 准备配置文件

```bash
cp analyzer.yaml.example analyzer.yaml
```

> 当前不再使用 `backend/configs/analyzer.yaml`；根目录 `analyzer.yaml.example` 是唯一推荐模板。

最小配置示例：

```yaml
server:
  endpoint: "http://your-relive-host:8080"
  api_key: "your-device-api-key"
  timeout: 30

analyzer:
  workers: 4
```

也可以通过环境变量提供 API Key：

```bash
export RELIVE_API_KEY=your-device-api-key
```

### 3. 构建 analyzer

```bash
make build-analyzer
```

### 4. 验证连通性

```bash
./backend/bin/relive-analyzer check -config analyzer.yaml
```

### 5. 开始分析

```bash
./backend/bin/relive-analyzer analyze -config analyzer.yaml
```

自定义并发：

```bash
./backend/bin/relive-analyzer analyze -config analyzer.yaml -workers 8
```

查看版本：

```bash
./backend/bin/relive-analyzer version
```

生成配置模板：

```bash
./backend/bin/relive-analyzer gen-config > analyzer.yaml
```

---

## CLI 命令

当前 CLI 支持以下子命令：

| 命令 | 说明 |
|------|------|
| `check` | 检查服务连通性与任务统计 |
| `analyze` | 启动分析循环 |
| `version` | 输出版本信息 |
| `gen-config` | 生成示例配置 |

> 当前 CLI **不支持** 旧文档中的 `estimate`、`-db export.db`、`--input/--output` 等文件模式参数。

---

## 服务端 API

analyzer 主要使用以下接口：

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/analyzer/tasks` | 获取待分析任务 |
| POST | `/api/v1/analyzer/tasks/:task_id/heartbeat` | 任务续租 |
| POST | `/api/v1/analyzer/tasks/:task_id/release` | 释放任务 |
| POST | `/api/v1/analyzer/results` | 提交分析结果 |
| GET | `/api/v1/analyzer/stats` | 获取统计信息 |
| POST | `/api/v1/analyzer/runtime/acquire` | 获取运行时占用 |
| POST | `/api/v1/analyzer/runtime/heartbeat` | 续租运行时占用 |
| POST | `/api/v1/analyzer/runtime/release` | 释放运行时占用 |

认证方式：
- `Authorization: Bearer <api_key>`
- 或 `X-API-Key: <api_key>`

任务中的图片下载链接由服务端生成，analyzer 会按返回的 URL 拉取图片并提交分析结果。

---

## 适用场景

- NAS 与 AI 主机分离，但 analyzer 能访问 Relive 服务
- 一台或多台分析主机并发处理照片
- 本地 GPU / 远程 GPU / 云端 API 混合使用

## 不适用场景

- analyzer 所在机器完全无法访问 Relive 服务
- 仍希望沿用 `export.db` 文件交换流程

如需查看旧的文件模式背景，请参考 `docs/ANALYZER.md`；该文档仅保留为历史说明，不代表当前实现。
