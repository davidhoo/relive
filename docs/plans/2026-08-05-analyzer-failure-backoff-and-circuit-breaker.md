# Analyzer Failure Backoff And Circuit Breaker Implementation Plan

> **Status:** Pending
>
> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 防止 AI Provider 的短暂 5xx、限流或异常响应被放大成 Analyzer 对同一批照片的“领取 → 失败 → 释放 → 立即重领”热循环，并确保失败状态、退避时间和最终终态在服务端可靠可见。

**Architecture:** 服务端继续作为分析任务状态的唯一真值，为照片增加持久化失败信息和 `next_retry_at`，并通过全局 `WriteQueue` 原子完成领取、续租和释放。Analyzer 增加错误分类与 Provider 级熔断器：Provider 整体不可用时暂停领取，半开探测成功后再恢复。现有 `retry_later` 请求字段保留兼容，但新的重试次数、退避时间和终态一律由服务端决定。

**Tech Stack:** Go、Gin、GORM、SQLite、现有 `database.WriteQueue`、relive-analyzer API 模式、SQLite checkpoint、Go 单元测试与 `httptest`。

---

## 背景与已确认事实

2026-08-04 线上曾出现以下完整故障链：

1. 本机 `relive-analyzer` 使用 VLLM，Provider 连续返回 `502 Bad Gateway`。
2. `handleTaskError` 立即调用 `ReleaseTask(..., retryLater=false)`。
3. 服务端清除锁后，`GetPendingTasks` 按 `id ASC` 立即重新领取同一批照片。
4. 高频 release 与扫描、心跳、结果提交等 SQLite 写操作竞争，NAS 出现 `Failed to release task: database is locked` 和 HTTP 500。
5. 本地 checkpoint 使用 3 次阈值，服务端领取使用 10 次阈值，统计又把 `>=3` 视为失败，失败语义不一致。

本次事故后来随 VLLM 恢复而自行收敛；当前 active 照片已全部完成分析，照片 `#130793` 已成功。本文任务处理的是复发防护，不是补跑历史照片。

## 实施范围

包含：

- 服务端持久化分析失败次数、失败分类、脱敏错误摘要、最近失败时间和下次重试时间。
- 统一任务最大尝试次数与退避计算，服务端作为唯一真值。
- 领取、续租、失败释放通过全局 `WriteQueue` 串行写 SQLite。
- 失败释放改成条件原子更新，不再先读后写。
- 为任务锁增加单调递增的 lock version，避免同一 Analyzer 的迟到 heartbeat/release 操作新任务。
- Analyzer 对 Provider 错误进行统一分类。
- Analyzer 增加 Provider 级 closed/open/half-open 熔断状态机。
- runtime lease 丢失时暂停任务领取，并按有界退避重新获取。
- API、日志、统计、配置样例和回归测试。

不包含：

- 不更换 VLLM 模型、Endpoint 或推理服务实现。
- 不修改照片分析 Prompt、评分、标签和 caption 业务语义。
- 不重构在线批量分析、结果缓冲区或整个后台任务框架。
- 不新增前端管理页面、失败照片列表或手动重试按钮。
- 不调整 identity profile、People、thumbnail、geocode 或 event curation 调度逻辑。
- 不自动切换到 Qwen/Ollama；熔断只暂停和恢复当前 Provider。

## 固定业务规则

### 错误分类

| failure class | 典型错误 | 单任务处理 | Provider 处理 |
|---|---|---|---|
| `provider_transient` | HTTP 502/503/504、连接失败、请求超时 | 增加 attempts 并进入退避 | 连续命中阈值后熔断 |
| `rate_limited` | HTTP 429 | 增加 attempts，优先采用 `Retry-After` | 立即暂停领取 |
| `response_invalid` | JSON 解析失败、缺少必填字段 | 增加 attempts 并进入退避 | 不因单张失败熔断；不同照片连续 3 次时熔断 |
| `input_permanent` | JPEG 损坏、格式不支持 | 直接进入最终失败 | 不熔断 |
| `client_cancelled` | 用户取消、进程退出 | 不增加业务失败次数 | 不熔断 |

错误分类必须基于结构化错误或 HTTP 状态码；禁止只靠任意字符串包含关系决定所有分支。尚未暴露状态码的 Provider 应先用受控 error wrapper 补足类型信息。

### 服务端尝试与退避

- `analysisMaxAttempts = 10`，替代当前本地 3 次、服务端 10 次、统计 3 次三套口径。
- 第 1 次失败：30 秒后可重试。
- 第 2 次失败：2 分钟后可重试。
- 第 3 次失败：10 分钟后可重试。
- 第 4 次失败：30 分钟后可重试。
- 第 5～9 次失败：2 小时后可重试。
- 第 10 次失败：最终失败，不再参与自动领取。
- `input_permanent` 直接推进到最终失败。
- 成功提交结果后清空 attempts、next retry 和最近失败字段。
- `Retry-After` 晚于默认退避时采用更晚时间；客户端不能缩短服务端退避。

### Analyzer 熔断

- 默认连续 3 个不同照片的 Provider 故障后 open。
- open 退避：`30s → 1m → 2m → 5m → 10m`，最大 10 分钟。
- open 时停止领取；已在 worker 中的任务允许结束并分别释放。
- 到期只允许一个 half-open 探测任务。
- 探测成功 close；失败重新 open 并升级退避。
- HTTP 429 直接 open，并优先遵守 `Retry-After`。
- runtime lease 丢失进入独立 `lease-paused`，不能继续普通 fetch。

## API 兼容契约

扩展 `ReleaseTaskRequest`，保留旧字段：

```go
type ReleaseTaskRequest struct {
    Reason            string `json:"reason" binding:"required"`
    ErrorMsg          string `json:"error_msg,omitempty"`
    RetryLater        bool   `json:"retry_later"` // legacy compatibility only
    FailureClass      string `json:"failure_class,omitempty"`
    Provider          string `json:"provider,omitempty"`
    RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
    LockVersion       int64  `json:"lock_version,omitempty"`
}
```

新 Analyzer 必须发送 `failure_class`、`provider` 和 `lock_version`。旧 Analyzer 未发送时按现有 `reason` 保守映射；`retry_later` 不再拥有跳过计数或自行决定调度时间的权力。

成功 release 返回：

```json
{
  "task_id": "task_130793_...",
  "new_status": "retry_wait",
  "attempts": 1,
  "next_retry_at": "2026-08-05T10:00:30+08:00",
  "final": false
}
```

永久失败返回 `new_status=failed`、`final=true`、`next_retry_at=null`；`client_cancelled` 返回 pending 且不增加 attempts。

## 数据模型

在 `Photo` 增加：

```go
AnalysisLockVersion   int64      `gorm:"not null;default:0" json:"-"`
AnalysisNextRetryAt   *time.Time `gorm:"index:idx_analysis_retry_ready" json:"-"`
AnalysisLastErrorCode string     `gorm:"type:varchar(50)" json:"-"`
AnalysisLastError     string     `gorm:"type:varchar(500)" json:"-"`
AnalysisLastFailedAt  *time.Time `json:"-"`
```

错误摘要入库前必须去除 Authorization/API key/URL query secret、压缩空白并截断到 500 字符。

---

### Task 1：用服务测试复现失败立即重领和 release 写锁问题

**Files:**

- Create: `backend/internal/service/analysis_service_test.go`
- Modify: `backend/internal/service/analysis_service.go`

**Step 1：建立最小测试数据库**

使用 SQLite 临时文件，迁移 `model.Photo`，初始化独立 `database.WriteQueue`，插入 thumbnail ready、`ai_analyzed=false` 的照片。

**Step 2：写失败测试**

覆盖：

- `ReleaseTask` 失败写必须经过 WriteQueue。
- 第一次 transient release 后不能被下一次 `GetPendingTasks` 立即重领。
- release 只增加一次 retry count。
- 最终失败不再被领取。
- 成功结果清空历史失败字段。

**Step 3：运行并确认失败**

```bash
cd backend
go test ./internal/service -run 'TestAnalysisService_(Release|Retry|Success)' -count=1 -v
```

Expected：因无 next-retry 字段、release 未走 WriteQueue、任务可立即重领而失败。

**Step 4：Commit**

```bash
git add backend/internal/service/analysis_service_test.go
git commit -m "test(analyzer): reproduce release retry hot loop"
```

### Task 2：增加持久化失败字段与统一重试策略

**Files:**

- Modify: `backend/internal/model/photo.go`
- Modify: `backend/pkg/database/database.go`
- Modify: `backend/pkg/database/database_test.go`
- Create: `backend/internal/service/analysis_retry_policy.go`
- Create: `backend/internal/service/analysis_retry_policy_test.go`

**Step 1：写失败测试**

覆盖数据库迁移字段，以及纯函数：

```go
func nextAnalysisRetry(attempt int, class string, retryAfter time.Duration, now time.Time) RetryDecision
```

断言固定退避表、Retry-After 下限、permanent 终态、client-cancelled 不计数和第 10 次终态。

**Step 2：运行并确认失败**

```bash
cd backend
go test ./pkg/database ./internal/service -run 'Test.*(AnalysisRetry|PhotoAnalysisFields)' -count=1 -v
```

**Step 3：实现最小模型和纯策略**

重试常量只能在 `analysis_retry_policy.go` 定义一次，service、stats 和测试复用。

**Step 4：运行并确认通过**

重复 Step 2，Expected：PASS。

**Step 5：Commit**

```bash
git add backend/internal/model/photo.go backend/pkg/database/database.go backend/pkg/database/database_test.go backend/internal/service/analysis_retry_policy.go backend/internal/service/analysis_retry_policy_test.go
git commit -m "feat(analyzer): persist retry scheduling state"
```

### Task 3：收紧领取、heartbeat 与 release 的锁版本和原子写语义

**Files:**

- Modify: `backend/internal/model/analyzer.go`
- Modify: `backend/internal/service/analysis_service.go`
- Modify: `backend/internal/service/analysis_service_test.go`
- Modify: `backend/internal/api/v1/handler/analyzer_handler.go`
- Create: `backend/internal/api/v1/handler/analyzer_handler_test.go`

**Step 1：写锁版本回归测试**

覆盖：

- 领取时 `analysis_lock_version + 1`，响应带 `lock_version`。
- heartbeat/release 同时匹配 photo/analyzer/lock-version/未完成状态。
- 旧版本迟到 release 不得清除新锁或增加 attempts。
- 同版本重复 release 幂等，不重复增加 attempts。
- release 与模拟并发写都经 WriteQueue，不出现 `database is locked`。

**Step 2：运行并确认失败**

```bash
cd backend
go test ./internal/service ./internal/api/v1/handler -run 'Test.*Analyzer.*(LockVersion|Release|Heartbeat)' -count=1 -v
```

**Step 3：重写服务写路径**

- `GetPendingTasks` 增加 next-retry 和统一 max-attempt 条件。
- 领取时递增 lock version。
- `ReleaseTask` 接收完整 request，通过 `executeWrite` 执行条件 UPDATE。
- `ExtendTaskLock` 同样走 `executeWrite` 并校验版本。
- 返回结构化 `ReleaseTaskResult`。
- RowsAffected=0 时区分 stale lock 与已释放的幂等请求，不统一返回 500。

**Step 4：更新 handler**

stale lock 返回 HTTP 409 `TASK_LOCK_STALE`；幂等重复返回 HTTP 200。内部错误仍为 500，但不返回原始 Provider body。

**Step 5：运行并确认通过**

重复 Step 2，Expected：PASS。

**Step 6：Commit**

```bash
git add backend/internal/model/analyzer.go backend/internal/service/analysis_service.go backend/internal/service/analysis_service_test.go backend/internal/api/v1/handler/analyzer_handler.go backend/internal/api/v1/handler/analyzer_handler_test.go
git commit -m "fix(analyzer): make task release durable and token-aware"
```

### Task 4：统一统计与成功清理语义

**Files:**

- Modify: `backend/internal/service/analysis_service.go`
- Modify: `backend/internal/service/analysis_service_test.go`
- Modify: `backend/internal/model/analyzer.go`

**Step 1：写统计测试**

断言：

- `Pending` 不包含最终失败。
- 新增 `RetryWaiting`。
- `Failed` 只统计达到统一 max attempts 的任务。
- `Locked` 同时要求 `ai_analyzed=false`。
- SubmitResults 成功清空 lock、attempts、next-retry 和 last-error。

**Step 2：实现并运行**

```bash
cd backend
go test ./internal/service -run 'TestAnalysisService_(Stats|SubmitResults)' -count=1 -v
```

Expected：PASS。

**Step 3：Commit**

```bash
git add backend/internal/service/analysis_service.go backend/internal/service/analysis_service_test.go backend/internal/model/analyzer.go
git commit -m "fix(analyzer): align retry and failure statistics"
```

### Task 5：实现错误分类与 Provider 熔断器

**Files:**

- Create: `backend/cmd/relive-analyzer/internal/analyzer/failure_policy.go`
- Create: `backend/cmd/relive-analyzer/internal/analyzer/failure_policy_test.go`
- Create: `backend/cmd/relive-analyzer/internal/analyzer/circuit_breaker.go`
- Create: `backend/cmd/relive-analyzer/internal/analyzer/circuit_breaker_test.go`
- Modify: `backend/internal/provider/vllm.go`
- Modify: `backend/internal/provider/ollama.go`

**Step 1：为 Provider 暴露结构化错误**

错误类型至少保留 provider、HTTP status、retry-after、timeout/transport 标志；body 只保留脱敏截断摘要。

**Step 2：写分类器表驱动测试**

覆盖 429、502/503/504、连接失败、deadline、响应缺字段、JPEG 损坏、unsupported format 和未知错误。

**Step 3：用 fake clock 写熔断状态机测试**

覆盖：

- closed 连续 3 个不同 photo ID 失败后 open。
- 同一 photo ID 重复失败不能伪造阈值。
- open 禁止普通领取。
- 到期只允许一个 half-open probe。
- probe 成功 close；失败升级退避且不超过 10 分钟。
- 429 采用更晚的 Retry-After。

**Step 4：运行并实现**

```bash
cd backend
go test ./cmd/relive-analyzer/internal/analyzer -run 'Test.*(FailurePolicy|CircuitBreaker)' -count=1 -v
```

Expected：先 FAIL，最小实现后 PASS。

**Step 5：Commit**

```bash
git add backend/cmd/relive-analyzer/internal/analyzer/failure_policy.go backend/cmd/relive-analyzer/internal/analyzer/failure_policy_test.go backend/cmd/relive-analyzer/internal/analyzer/circuit_breaker.go backend/cmd/relive-analyzer/internal/analyzer/circuit_breaker_test.go backend/internal/provider/vllm.go backend/internal/provider/ollama.go
git commit -m "feat(analyzer): classify provider failures and open circuit"
```

### Task 6：把熔断、release 与 lease-paused 接入主循环

**Files:**

- Modify: `backend/cmd/relive-analyzer/internal/analyzer/api_analyzer.go`
- Create: `backend/cmd/relive-analyzer/internal/analyzer/api_analyzer_test.go`
- Modify: `backend/cmd/relive-analyzer/internal/client/api_client.go`
- Modify: `backend/cmd/relive-analyzer/internal/client/api_client_test.go`
- Modify: `backend/cmd/relive-analyzer/internal/client/task_manager.go`
- Modify: `backend/cmd/relive-analyzer/internal/cache/checkpoint.go`
- Modify: `backend/cmd/relive-analyzer/internal/cache/checkpoint_test.go`

**Step 1：写集成级失败测试**

使用 fake provider 和 `httptest.Server` 覆盖：

- 4 workers 同时遇到 502，只允许已在途任务 release；熔断后 fetch 次数停止增长。
- release payload 包含 failure class/provider/lock version。
- half-open 成功后恢复 fetch。
- checkpoint 只保留诊断，不再用本地 3 次阈值覆盖服务端决定。
- runtime lease 丢失后停止普通 fetch，重获成功才恢复。
- cancel 退出不记业务失败。

**Step 2：实现主循环状态协调**

`fetchLoop` 在 circuit open、probe 已在途、lease 未持有或 context 取消时不得领取。使用 timer/select，不使用不可取消的长 sleep。

**Step 3：修正结束摘要**

区分全部成功、部分最终失败、Provider 熔断暂停和用户取消；存在最终失败时不得输出笼统的 `Analysis completed successfully`。

**Step 4：运行测试**

```bash
cd backend
go test ./cmd/relive-analyzer/internal/analyzer ./cmd/relive-analyzer/internal/client ./cmd/relive-analyzer/internal/cache -count=1
```

Expected：PASS，且 fake-clock 测试无真实长等待。

**Step 5：Commit**

```bash
git add backend/cmd/relive-analyzer/internal/analyzer backend/cmd/relive-analyzer/internal/client backend/cmd/relive-analyzer/internal/cache
git commit -m "fix(analyzer): pause fetching during provider and lease outages"
```

### Task 7：增加配置样例和运行文档

**Files:**

- Modify: `backend/cmd/relive-analyzer/internal/config/config.go`
- Modify: `backend/cmd/relive-analyzer/internal/config/config_test.go`
- Modify: `analyzer.yaml.example`
- Modify: `docs/ANALYZER_API_MODE.md`

**Step 1：增加配置并验证边界**

```yaml
analyzer:
  max_attempts: 10
  circuit_failure_threshold: 3
  circuit_initial_backoff: 30
  circuit_max_backoff: 600
```

缺省值必须符合固定业务规则；非法值返回明确错误。

**Step 2：文档化运维语义**

说明 circuit open 不等于任务丢失、自动恢复条件、最终失败规则、stats 字段和安全停止方式；禁止指导用户直接改 SQLite retry/lock 字段。

**Step 3：运行配置测试**

```bash
cd backend
go test ./cmd/relive-analyzer/internal/config -count=1
```

**Step 4：Commit**

```bash
git add backend/cmd/relive-analyzer/internal/config/config.go backend/cmd/relive-analyzer/internal/config/config_test.go analyzer.yaml.example docs/ANALYZER_API_MODE.md
git commit -m "docs(analyzer): document retry and circuit behavior"
```

### Task 8：全量回归和 NAS 故障注入验收

**Files:**

- Modify if needed: only files already listed above

**Step 1：本地回归**

```bash
cd backend
go test ./internal/service ./internal/api/v1/handler ./pkg/database -count=1
go test ./cmd/relive-analyzer/... -count=1
go test ./... -count=1
```

Expected：全部 PASS。

**Step 2：构建与静态检查**

```bash
cd backend
go build -o bin/relive ./cmd/relive
go build -o bin/relive-analyzer ./cmd/relive-analyzer
cd ..
git diff --check
```

Expected：两个二进制构建成功，`git diff --check` 无输出。

**Step 3：隔离注入 502**

使用测试 VLLM stub，不直接破坏生产 Provider。连续返回 502 至少 2 分钟，确认：

- 达到阈值后停止领取，请求频率迅速下降。
- 已在途任务进入 retry_wait，attempts 只增加一次。
- 无 `database is locked`。
- half-open probe 只有一个。

**Step 4：恢复 Provider**

让 probe 成功，确认自动 close 并继续处理，无需清库或重启 NAS。

**Step 5：NAS 部署后只读验收**

记录：

- stats 的 pending/retry_waiting/failed/locked。
- circuit 状态变更日志。
- 近 1 小时 release 锁错误为 0。
- 无同一 photo ID 短时间反复领取。
- 正常吞吐无明显下降。

验收记录不得包含 API key、完整敏感 URL 或原始敏感响应。

## 完成标准

- Provider 短暂 502/503/504、429 或异常响应不会产生立即重领热循环。
- Analyzer 熔断和 runtime lease 暂停是明确状态，不是只打日志后继续 fetch。
- 服务端持久化 attempts、next retry、失败分类和脱敏摘要，并作为唯一调度真值。
- release/heartbeat 与任务锁版本绑定，迟到请求不能影响新任务。
- SQLite 写路径串行化，故障注入期间 release 锁错误为 0。
- retry-wait、最终失败和成功清理在统计中互不混淆。
- Provider 恢复后自动继续，无需手工修改数据库。
- 全量 Go 测试、构建与 NAS 验收通过。

