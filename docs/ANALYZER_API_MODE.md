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

---

## 失败退避与 Provider 熔断（v2 运维语义）

为防止 AI Provider 短暂 5xx / 429 / 异常响应被放大成“领取 → 失败 → 立即重领”的热循环，analyzer 与服务端协同实现了失败退避与 Provider 级熔断。服务端是任务状态的唯一真值。

### 服务端重试与退避

- 统一最大尝试次数 `analysisMaxAttempts = 10`，替代历史上本地 3 次 / 服务端 10 次 / 统计 3 次三套口径。
- 退避表（第 N 次失败后距下次可重试）：`30s → 2m → 10m → 30m → 2h → 2h → 2h → 2h → 2h`；第 10 次进入最终失败，不再参与自动领取。
- `input_permanent`（JPEG 损坏、不支持格式）直接进入最终失败。
- 成功提交结果后清空 attempts、next_retry_at 与最近失败字段。
- 客户端的 `Retry-After` 只能延长退避，不能缩短服务端退避。

### Provider 熔断（analyzer 侧）

- 默认连续 **3 个不同照片**的 Provider 故障后熔断 open；同一照片重复失败不伪造阈值。
- open 退避阶梯：`30s → 1m → 2m → 5m → 10m`，上限 10 分钟。
- open 时停止领取；已在 worker 中的任务允许结束并分别释放。
- 到期只允许一个 half-open 探测任务；探测成功 close，失败重新 open 并升级退避。
- HTTP 429 直接 open，并优先遵守 `Retry-After`。
- runtime lease 丢失（连续 3 次心跳失败）进入 `lease-paused`，停止普通 fetch，重获成功才恢复。

### 统计字段（`GET /api/v1/analyzer/stats`）

| 字段 | 含义 |
|------|------|
| `pending` | 可被领取（未被锁、未进入 retry_wait / failed） |
| `retry_waiting` | 退避等待中（next_retry_at 未到） |
| `locked` | 当前被锁定（且 ai_analyzed=false） |
| `failed` | 最终失败（达到 max attempts） |

### 安全停止与恢复

- **circuit open 不等于任务丢失**：照片仍在服务端，退避到期后会自动重新领取。
- **自动恢复**：Provider 恢复后 half-open 探测成功即 close，无需清库或重启 NAS。
- **最终失败**：达到 max attempts 的照片不再自动领取；如需重试，请通过 Web UI 排除/恢复或后续手动重置接口，**禁止直接修改 SQLite 的 `analysis_retry_count` / `analysis_next_retry_at` / `analysis_lock_*` 字段**——这些字段由服务端原子维护，手改会破坏锁版本与退避一致性。
- **安全停止**：`Ctrl+C` / `SIGTERM` 触发优雅退出；在途任务会以 `client_cancelled` 释放（不计业务失败次数），不会触发熔断。

### 配置项（`analyzer.yaml`）

```yaml
analyzer:
  max_attempts: 10                # 诊断用，权威值在服务端
  circuit_failure_threshold: 3    # 连续不同照片失败阈值
  circuit_initial_backoff: 30     # 秒
  circuit_max_backoff: 600        # 秒
```

非法值（负数、initial > max）会在启动校验时返回明确错误。

