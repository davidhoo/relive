# 人脸质检 v2：验证器交付、暂停竞态与运行 #3 恢复 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task.

**Goal:** 让 `independent_v2` 历史补证据运行仅在已校验的 YuNet 验证器可用时启动或恢复；修复暂停/取消被 worker 进度刷新覆盖的竞态，并安全恢复 NAS 运行 #3 的剩余快照与技术失败项。

**Architecture:** 将运行状态转换和运行进度更新拆为不同的、条件化的数据库写入：worker 只能写统计字段，不能用过期的内存对象覆盖 `paused` 或 `cancelled`；领取下一照片批次时也必须在同一事务内确认 run 仍为 `running`。YuNet 作为构建前由受版本/摘要约束的脚本取得的部署资产，Docker 构建再次校验；ML 健康接口把验证器可用性纳入健康状态，后端据此拒绝 v2 的创建、恢复与按运行重试。

**Tech Stack:** Go / GORM / SQLite、Python / FastAPI / OpenCV、Docker Compose、pytest、Go testing。

---

## 背景与已证实事实

2026-08-13 的 NAS 运行 #3 为 `calibration + shadow + independent_v2`，快照范围是 1,000 张照片、4,692 个 Face。启动时 `relive-ml` 镜像内只有 `ml-service/assets/yunet/SHA256SUMS` 的占位内容，不含 `face_detection_yunet_2023mar.onnx`；ML 启动日志写明 `YuNet model missing ... verifier unavailable`。

`FaceVerifier` 因而按设计返回 `error + verifier_unavailable`，后端正确把 item 写为 `retryable_error`，没有产生真实 v2 证据或自动隔离。后端容器停止时的最终只读快照为：`retryable_error=720`、`pending=3,969`、`processing=3`、`processed_face_count=0`、`auto_excluded_count=0`。这 720 条不是 `non_face`、`low_quality` 或人工灰区。

另已复现暂停竞态：`POST /rescore-runs/3/pause` 两次返回 HTTP 200，但 worker 手持启动时读取的 `run.Status=running`，随后 `refreshRunCounts` 调用 `db.Save(run)`，把整行（包括旧 `status`）写回。因此暂停没有真正阻止下一批领取。当前 NAS `relive` 已停止，物理上冻结了 worker；数据库中的 #3 状态可能仍是 `running`，不得在未修复前直接重启后端。

## 设计约束

1. `shadow` 和 retry 永不创建 `face_exclusions`、不改变 `person_id`、`cluster_status`、embedding 或聚类；技术错误绝不改写成质检结论。
2. 模型不得在容器运行时下载。模型文件不提交 Git；其固定来源、Git revision、文件名和 SHA-256 必须提交并由构建验证。
3. 对 v2 来说，`/api/v1/health` 只有在验证器可用时才是 2xx；任何 `degraded`/网络/协议错误均禁止创建、恢复或 retry v2 run。legacy v1 行为不变。
4. 暂停是持久状态：在暂停请求与一个已领取批次并发时，该批可完成并写入进度，但 worker 不得再领取新照片；进度写入不得把 `paused` 或 `cancelled` 改回 `running`。
5. 本次仅恢复 shadow 证据。即使 #3 和其 retry 成功，也不得据此自动发起或放行 full/enforce；enforce 仍须另行做单一、闭合、人工批准的 v2 校准。

## 任务 1：让运行状态转换与进度刷新互不覆盖

**Files:**

- Modify: `backend/internal/repository/face_quality_rescore_repo.go`
- Modify: `backend/internal/service/face_quality_rescore.go`
- Modify: `backend/internal/service/face_quality_rescore_test.go`
- Modify: `backend/internal/repository/face_quality_rescore_repo_test.go`

### Step 1: 先写 worker 与暂停并发的失败测试

在 `backend/internal/service/face_quality_rescore_test.go` 添加受 channel 控制的 fake ML client。测试创建两个不同照片的 pending item：

1. 启动 `processOneBatch`，让 client 在首批 item 已被领取为 `processing` 后阻塞。
2. 并发调用 `Pause(run.ID)`，断言数据库 run 为 `paused`。
3. 解除 client 阻塞，让本批正常到终态并触发 `refreshRunCounts`。
4. 重新读取 run，断言状态仍为 `paused`；调用第二次 `processOneBatch`，断言它没有把第二照片的 pending item 领取为 `processing`。

再为 `Cancel(run.ID)` 重复同一测试；批次完成后的进度刷新不得把 `cancelled` 覆盖回 `running`。

Run:

```bash
cd backend && go test ./internal/service -run 'TestRescore_(Pause|Cancel)DuringInFlightBatch' -count=1
```

Expected: 在实现前失败，现状会观察到 run 回到 `running` 或第二照片被领取。

### Step 2: 添加精确的 repository 写入原语

在 repository interface 和实现中新增以下语义明确的方法，而非继续让 worker 使用 `UpdateRun(*Run)`：

```go
UpdateRunProgress(runID uint, progress FaceQualityRescoreRunProgress) error
TransitionRunStatus(runID uint, from []string, to string, completedAt *time.Time) (bool, error)
ClaimNextPhotoItemsWhenRunning(runID uint) ([]*model.FaceQualityRescoreItem, error)
```

`FaceQualityRescoreRunProgress` 只含 `processed_*`、`accepted_count`、`review_required_count`、`auto_excluded_count`、`retryable_count`、`superseded_manual_count`、`last_error` 和 `updated_at`。`UpdateRunProgress` 必须使用 `Model(...).Where("id = ?", runID).Updates(map[string]any{...})`；不得包含 `status`、`started_at`、`completed_at` 或 run 的其他字段。

`TransitionRunStatus` 必须以 `WHERE id=? AND status IN ?` 条件更新，只允许预期来源状态转换：

- pause: `queued|running -> paused`
- resume: `paused -> running`
- cancel: 除终态外的活动状态 -> `cancelled`
- completion: `queued|running -> completed|completed_with_errors`

返回 `false` 表示并发方已改变状态，调用方不应重试或覆盖。

`ClaimNextPhotoItemsWhenRunning` 在现有 `ClaimNextPhotoItems` 的同一 SQLite transaction 中先检查 `face_quality_rescore_runs.status='running'`，不满足时返回空集且不更新 item。保留原 `ClaimNextPhotoItems` 仅供不需要 run 状态门禁的现有调用；v1/v2 worker 一律切换到新方法。

### Step 3: 最小化替换 service 的写入路径

修改 `face_quality_rescore.go`：

1. `Pause`、`Resume`、`Cancel` 改用 `TransitionRunStatus`。`Resume` 先执行 v2 readiness 检查（任务 3），成功后才将遗留 `processing` item 重置为 pending，最后条件转换到 running。
2. `refreshRunCounts` 仍可计算当前统计，但只能调用 `UpdateRunProgress`；可以更新传入 run 对象的内存统计供日志/完成判定使用，绝不能把它整行保存。
3. `processOneBatchV1` 与 `processOneBatchV2` 改用 `ClaimNextPhotoItemsWhenRunning`。这样一个暂停中的 run 不会开始下一照片批次。
4. `maybeCompleteRun` 在计数刷新后，重新读取最新 run 并通过 `TransitionRunStatus` 写终态；若已经暂停或取消，直接返回。禁止 `db.Save(latest)` 覆盖状态。

保留 `UpdateRun` 给创建 run、初始化 run 和经条件检查后的非 worker 管理路径；为其补注释，说明它不可用于持有过期 run 的后台进度刷新。

### Step 4: 验证暂停、取消与既有恢复行为

Run:

```bash
cd backend && go test ./internal/service ./internal/repository -run 'TestRescore_(Pause|Cancel|AllFailure|RetryRun|Full)' -count=1
```

Expected: PASS。尤其验证暂停中的 `processing` item 只会在显式 resume 时回到 pending；cancelled 的 run 不再领取 pending item，亦不被完成判定改为其他状态。

### Step 5: Commit

```bash
git add backend/internal/repository/face_quality_rescore_repo.go \
  backend/internal/repository/face_quality_rescore_repo_test.go \
  backend/internal/service/face_quality_rescore.go \
  backend/internal/service/face_quality_rescore_test.go
git commit -m "fix(face-quality): preserve paused rescore runs during progress refresh"
```

## 任务 2：把 YuNet 变为可重现、可验证的构建资产

**Files:**

- Modify: `.gitignore`
- Modify: `ml-service/assets/yunet/SHA256SUMS`
- Create: `scripts/fetch-yunet-model.sh`
- Modify: `ml-service/Dockerfile`
- Modify: `ml-service/tests/test_face_router.py`
- Modify: `ml-service/tests/test_face_verifier.py`

### Step 1: 写健康降级的失败测试

更新 `test_health_endpoint` 并新增验证器不可用测试。通过替换 `app.routers.faces.verifier` 为 `available=False` 的 stub，断言：

```python
response = client.get("/api/v1/health")
assert response.status_code == 503
assert response.json() == {
    "status": "degraded",
    "verifier_available": False,
    "verifier_name": "yunet",
    "verifier_version": "opencv-yunet-2023mar",
}
```

可用 stub 则必须返回 HTTP 200、`status="ok"` 和相同的 verifier identity。保留现有缺模型/SHA 不匹配时 `verify_crops` 返回 `verifier_unavailable` 的单元测试，确保没有回退 v1。

Run:

```bash
cd ml-service && pytest -q tests/test_face_router.py tests/test_face_verifier.py
```

Expected: 在实现前 health 仍为 HTTP 200/`{"status":"ok"}`，测试失败。

### Step 2: 固定模型清单与取得脚本

将 `ml-service/assets/yunet/SHA256SUMS` 的占位注释替换为唯一有效条目：

```text
8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4  face_detection_yunet_2023mar.onnx
```

注释中记录来源、revision 和取得日期：[OpenCV Zoo `47534e27c9851bb1128ccc0102f1145e27f23f98` 的 YuNet 2023mar 文件](https://github.com/opencv/opencv_zoo/blob/47534e27c9851bb1128ccc0102f1145e27f23f98/models/face_detection_yunet/face_detection_yunet_2023mar.onnx)。该 revision 的文件大小应为 `232589` bytes；不要在脚本中使用浮动的 `main` 分支 URL。模型属于 OpenCV Zoo 的 YuNet 模型目录，遵循其目录声明的 MIT license。

新建可执行 `scripts/fetch-yunet-model.sh`，仅供构建前人工/CI 调用，必须：

1. 以脚本常量保存上述 commit、文件名、SHA-256 和 HTTPS URL；拒绝环境变量替换来源或摘要。
2. 下载到同目录的 `*.partial`，用 `sha256sum -c`（或 macOS 可用的 `shasum -a 256` 等价校验）验证后原子 `mv` 到 `ml-service/assets/yunet/face_detection_yunet_2023mar.onnx`。
3. 对已存在文件也做校验；摘要错误时失败退出，不能覆盖有效文件。
4. 不打印 credentials、不下载任何照片或人脸裁剪。

在 `.gitignore` 增加只忽略 `ml-service/assets/yunet/*.onnx` 的规则，保留跟踪 `SHA256SUMS` 与脚本。模型二进制不进入 Git，也不允许使用运行时卷替换镜像中的该资产。

### Step 3: 让构建和健康检查拒绝坏资产

修改 `ml-service/Dockerfile`：`COPY assets ./assets` 后执行：

```dockerfile
RUN test -s assets/yunet/face_detection_yunet_2023mar.onnx \
    && sha256sum -c assets/yunet/SHA256SUMS
```

模型缺失或摘要不匹配必须使镜像构建失败，不能生成“容器健康但验证器不可用”的发布物。保留 `FaceVerifier` 的运行时摘要校验，防御镜像层损坏。

扩展 `HealthResponse`，令 health router 从 `app.routers.faces.verifier` 读取可用性；不可用时返回上述结构化降级响应及 503。Docker Compose 现有 urllib healthcheck 会把 503 标记为 unhealthy，无需下载模型或改动照片卷。

### Step 4: 运行模型/健康测试和本地构建验证

Run:

```bash
scripts/fetch-yunet-model.sh
cd ml-service && pytest -q tests/test_face_router.py tests/test_face_verifier.py
cd .. && docker compose build relive-ml
```

Expected: 单元测试通过；Docker build 显示 `face_detection_yunet_2023mar.onnx: OK`。实际 HTTP health 验证只在任务 4 通过 `docker compose up -d relive-ml` 启动服务后执行，构建期不把未启动的 HTTP server 误判为模型错误。

### Step 5: Commit

```bash
git add .gitignore scripts/fetch-yunet-model.sh ml-service/Dockerfile \
  ml-service/assets/yunet/SHA256SUMS ml-service/app \
  ml-service/tests/test_face_router.py ml-service/tests/test_face_verifier.py
git commit -m "fix(ml): gate YuNet verifier on verified deployment asset"
```

## 任务 3：在后端阻断不可用验证器的 v2 运行

**Files:**

- Modify: `backend/internal/mlclient/client.go`
- Modify: `backend/internal/mlclient/client_test.go`
- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/face_quality_rescore.go`
- Modify: `backend/internal/service/face_quality_rescore_test.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/people_rescore_handler_test.go`

### Step 1: 写 v2 readiness 门禁的失败测试

给 `PeopleMLClient` 增加 `Health(ctx)`；fake client 可分别模拟 `verifier_available=false`、网络错误和 ready。新增三组测试：

1. `CreateRun(... independent_v2)` 在验证器不可用时返回确定的 `errV2VerifierUnavailable`，数据库中不创建 run/item。
2. paused 的 v2 run 调用 `Resume` 时验证器不可用，保持 `paused`，且既有 `processing` item 不得被提前重置为 pending。
3. v2 来源 run 的 `RetryRun` 在验证器不可用时不创建 retry run；legacy v1 create/resume/retry 不查询该门禁。

handler 测试断言前端收到稳定的 409（建议错误码 `FACE_QUALITY_VERIFIER_UNAVAILABLE`），而不是伪成功或 500。

Run:

```bash
cd backend && go test ./internal/service ./internal/api/v1/handler -run 'TestRescore_.*Verifier' -count=1
```

Expected: 实现前 v2 run 可以被创建/恢复，测试失败。

### Step 2: 添加只读 ML health client 与服务门禁

在 `mlclient.Client` 增加 `Health(ctx)`，请求 `GET /api/v1/health`，仅当 HTTP 200、`status="ok"`、`verifier_available=true`、名称 `yunet` 且版本 `opencv-yunet-2023mar` 时返回 ready。任何 503、非预期 identity、解析错误和 timeout 都映射为“未就绪”，不暴露底层路径或原始异常给浏览器。

`peopleService` 的 client interface 和所有 fake/stub 同步实现该方法。`faceQualityRescoreService.ensureV2VerifierReady` 仅为 `pipeline_version=independent_v2` 调用健康检查，并供 `CreateRun`、`Resume` 与 `RetryRun` 使用。检查必须发生在快照、item 状态重置和任意数据库写入之前。

后端应保留运行时 batch 的 `verifier_unavailable -> retryable_error` 防线：健康检查只是防止已知坏环境开始/恢复，不是假定部署之后永远不会损坏。

### Step 3: 完整后端回归

Run:

```bash
cd backend && go test ./internal/mlclient ./internal/service ./internal/api/v1/handler -count=1
cd backend && go test ./... -count=1
```

Expected: PASS。existing v1 rescore、v2 evidence、校准门禁和 pause/resume 测试全部保持通过。

### Step 4: Commit

```bash
git add backend/internal/mlclient/client.go backend/internal/mlclient/client_test.go \
  backend/internal/service/people_service.go backend/internal/service/face_quality_rescore.go \
  backend/internal/service/face_quality_rescore_test.go \
  backend/internal/api/v1/handler/people_handler.go \
  backend/internal/api/v1/handler/people_rescore_handler_test.go
git commit -m "fix(face-quality): gate v2 runs on verifier readiness"
```

## 任务 4：NAS 上线与 #3 的受控恢复

**Files:**

- Modify: `docs/NAS_BACKUP.md`（只补充本任务所需的 preflight/restore 引用；不要复制全部运行手册）
- Modify: `docs/plans/2026-08-13-face-quality-v2-recovery-and-pause-race-repair.md`（记录实际 deployed commit、镜像 ID、run ID 和验收结果）

### Step 1: 在停机窗口前保存可恢复证据

前置条件：`relive` 容器已停止，`relive-ml` 可继续运行；不得重启 `relive` 后端。先使用既有 NAS 备份工具生成数据库备份并对备份运行 `PRAGMA quick_check` 和 `sha256sum -c`。记录备份目录、当前 git SHA、现有镜像 ID 和 run #3 当前计数。

将 #3 安全落为暂停状态必须在后端停机时完成。使用有明确 run ID 和来源状态约束的单事务维护操作：

```sql
BEGIN IMMEDIATE;
UPDATE face_quality_rescore_runs
SET status = 'paused', updated_at = CURRENT_TIMESTAMP
WHERE id = 3 AND status = 'running';
SELECT changes();
COMMIT;
```

期望 `changes()=1`；否则中止并重新读取状态，不可用无条件 UPDATE。此操作只改变 run 控制状态，不重置 item、不删事件、不改 Face。

### Step 2: 构建前取得并校验模型

在 NAS 部署 checkout 的已审核 git SHA 上运行：

```bash
scripts/fetch-yunet-model.sh
sha256sum -c ml-service/assets/yunet/SHA256SUMS
docker compose config --quiet
docker compose build relive-ml
```

构建失败、摘要错误、模型缺失或 Compose 配置错误均停止上线。不得用 `docker cp`、手工容器内下载或未校验卷文件替代该步骤。

### Step 3: 启动 ML 并验证 ready，再启动后端

先更新/启动 `relive-ml`，等待其健康状态为 `healthy`，再从容器内或同一 Docker network 验证：

```bash
docker compose up -d relive-ml
docker inspect --format '{{.State.Health.Status}}' relive-ml
docker exec relive-ml python -c \
  "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:5050/api/v1/health').read().decode())"
```

健康 JSON 必须包含 `status="ok"` 和 `verifier_available=true`。之后才可启动 `relive`：

```bash
docker compose up -d relive
```

启动后立即读取 run #3：必须为 `paused`，且 `pending + processing + retryable_error = target_face_count`。此时 worker 不得领取任何新的 pending item；若计数变化，立刻停止 `relive`，保留现场日志和数据库备份，不做第二次尝试。

### Step 4: 恢复 #3 的剩余 pending 快照

在 UI 或 API 明确恢复 run #3。服务应先 readiness 检查，再把因停机留下的 3 条 `processing` 重置为 pending，并条件转为 running。观察首批 1–3 张照片：

- 新写入事件必须是 `evidence_pipeline=independent_v2`；
- `verifier_name=yunet`、`verifier_version=opencv-yunet-2023mar`；
- `evidence_state=available` 才计入已获证据/真实灰区；
- `auto_excluded_count` 必须仍为 0（shadow）；
- 不得出现新的 `verifier_unavailable`。

若 health 变为 degraded、出现 `verifier_unavailable`、数据库锁持续出现或任何 shadow 自动隔离，立刻暂停已修复 run；不要改为 enforce，也不要继续累计错误。

### Step 5: 精确 shadow 重试运行 #3 的 720 条技术失败项

当 #3 的 pending 清空并变为 `completed_with_errors` 后，调用现有 `POST /api/v1/people/face-quality/rescore-runs/3/retry`。该操作必须创建 `retry_of_run_id=3` 的新的 calibration + shadow + independent_v2 run，其 item 集合仅包含 #3 当前 `historical_rescore + retryable_error|unmatched` 事件；不得重新抓取“全部缺证据”或新建普通校准。

再次核对 retry run：其总 item 数等于来源 run 的当前失败事件数；无 `verifier_unavailable`；所有成功结果拥有 v2 evidence。此 retry 是历史补证据修复，不是 full/enforce 许可。

### Step 6: 记录验收与回滚

记录每一步的时间、git SHA、ML image ID、YuNet SHA-256、run #3/retry run ID、目标/已获证据/真实灰区/自动隔离/失败数。合格条件：

1. #3 暂停后没有新 item 被领取；修复后暂停/恢复按预期工作。
2. ML health 不再把 `verifier_available=false` 伪报为 healthy。
3. #3 的 remaining pending 和 retry run 只生成 v2 evidence 或明确技术状态；无自动隔离、无人工结论被覆盖。
4. 运行 #3 和 retry run 的 item 计数闭合，且 retry 目标精确等于来源失败事件集。

回滚：停止 `relive`；保留数据库和日志；使用备份文档的已验证备份恢复，仅在明确授权下执行恢复。模型缺失/摘要失败时，不回退到 v1 作为“独立复核”替代，不恢复或 enforce #3。

### Step 7: Commit 文档与上线记录

```bash
git add docs/NAS_BACKUP.md docs/plans/2026-08-13-face-quality-v2-recovery-and-pause-race-repair.md
git commit -m "docs(face-quality): document v2 verifier recovery runbook"
```

## 实施与上线记录（2026-08-13）

> 本段记录实际交付物与 NAS 现场执行 runbook。代码侧（任务 1–3）已完成并提交；NAS 侧（任务 4 Step 1–6）需现场执行，下方验收清单的「实际值」列在执行时填写。

### 代码交付物（已完成）

| 任务 | Commit | 说明 |
|------|--------|------|
| 任务 1 | `cd0de51` | 拆分状态转换与进度刷新：`UpdateRunProgress`/`TransitionRunStatus`/`ClaimNextPhotoItemsWhenRunning`；`Pause/Resume/Cancel/refreshRunCounts/maybeCompleteRun` 改条件化写入；并发暂停/取消失败测试。 |
| 任务 2 | `f302509` | YuNet 构建资产：`SHA256SUMS` 真实条目、`scripts/fetch-yunet-model.sh`、Dockerfile 构建期 `sha256sum -c`、`HealthResponse` 扩展 + 503 降级。 |
| 任务 2 修 | `d7e3651` | `test_verify_known_face_crops_endpoint_shape` 用 monkeypatch 注入 `available=False`，不再依赖磁盘模型。 |
| 任务 3 | `a785620` | 后端 v2 readiness 门禁：`mlclient.Health` + `ensureV2VerifierReady`（CreateRun/Resume/RetryRun 三处）+ `ErrRescoreV2VerifierUnavailable` → 409；RetryRun 继承 src 管线。 |

**YuNet 模型契约**（构建期与运行时双重校验）：

- 文件：`face_detection_yunet_2023mar.onnx`
- SHA-256：`8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4`
- 大小：232589 bytes
- 来源：OpenCV Zoo revision `47534e27c9851bb1128ccc0102f1145e27f23f98`（MIT）
- 取得脚本：`scripts/fetch-yunet-model.sh`（常量硬编码，拒绝环境变量替换来源/摘要）

**本地验证状态**：`go test ./...` 全绿；`ml-service` pytest 28 passed；`go vet`/`gofmt` clean。`docker compose build relive-ml` 的构建期 `sha256sum -c` 门禁逻辑已就绪，实际镜像构建验证在 NAS Step 2 执行。

### NAS 现场 runbook（待执行）

前置条件：`relive` 后端已停止（当前已冻结），`relive-ml` 可继续运行；不得在未完成 Step 1–3 前重启 `relive`。

1. **备份与落 paused**（Step 1）：`make backup-nas` 生成已校验 SQLite 备份（`PRAGMA quick_check` + `sha256sum -c`），记录备份目录/git SHA/现有镜像 ID/run #3 当前计数。随后对生产库执行条件 SQL（仅当 `id=3 AND status='running'` 时置 `paused`，`SELECT changes()` 须为 1，否则中止重读）：
   ```sql
   BEGIN IMMEDIATE;
   UPDATE face_quality_rescore_runs SET status='paused', updated_at=CURRENT_TIMESTAMP WHERE id=3 AND status='running';
   SELECT changes();
   COMMIT;
   ```
2. **构建前取得并校验模型**（Step 2）：在已审核 git SHA 上 `scripts/fetch-yunet-model.sh` → `sha256sum -c ml-service/assets/yunet/SHA256SUMS` → `docker compose config --quiet` → `docker compose build relive-ml`（须显示 `face_detection_yunet_2023mar.onnx: OK`）。禁用 `docker cp`/运行时下载/未校验卷。
3. **ML ready 后启后端**（Step 3）：`docker compose up -d relive-ml` → 等 `healthy` → `docker exec relive-ml python -c "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:5050/api/v1/health').read().decode())"` 必须含 `status="ok"` + `verifier_available=true`。随后 `docker compose up -d relive`。启动后立即读 run #3：必须 `paused`，且 `pending+processing+retryable_error = target_face_count(=4692)`；worker 不得领取新 pending，否则立刻停 `relive` 保留现场。
4. **恢复 #3 剩余 pending**（Step 4）：UI/API 显式 Resume run #3（服务先 readiness 检查 → 重置 3 条 processing 为 pending → 条件转 running）。观察首批 1–3 张：事件 `evidence_pipeline=independent_v2`、`verifier_name=yunet`、`verifier_version=opencv-yunet-2023mar`、`evidence_state=available` 才计入证据/灰区；`auto_excluded_count` 必须仍为 0（shadow）；不得出现新 `verifier_unavailable`。health 降级/出现 unavailable/锁持续/自动隔离 → 立即暂停，不改 enforce。
5. **retry 720 条技术失败**（Step 5）：#3 pending 清空且变为 `completed_with_errors` 后 `POST /api/v1/people/face-quality/rescore-runs/3/retry`。须创建 `retry_of_run_id=3` 的 calibration+shadow+independent_v2 run，item 集合仅含 #3 当前 `historical_rescore + retryable_error|unmatched` 事件；总 item 数 = 来源当前失败事件数；无 `verifier_unavailable`；成功结果均 v2 evidence。
6. **验收与回滚**（Step 6）：记录时间/git SHA/ML image ID/YuNet SHA-256/run #3 与 retry run ID/目标/已获证据/真实灰区/自动隔离/失败数。回滚：停 `relive`、保留 DB+日志、仅授权下用已校验备份恢复；模型缺失/摘要失败时**不**回退 v1、**不**恢复或 enforce #3。

### 验收清单

合格条件（计划 §6）：

- [ ] #3 落 paused 后无新 item 被领取；修复后 pause/resume 按预期工作。
- [ ] ML health 不再把 `verifier_available=false` 伪报为 healthy（503 = unhealthy）。
- [ ] #3 remaining pending 与 retry run 只生成 v2 evidence 或明确技术状态；无自动隔离、无人工结论被覆盖。
- [ ] run #3 与 retry run item 计数闭合，retry 目标精确等于来源失败事件集。

实际值（执行时填写）：

| 项 | 实际值 |
|----|--------|
| 部署 git SHA | _待填_ |
| relive-ml image ID | _待填_ |
| YuNet SHA-256 | `8f2383e4...2552fa4`（契约，构建期校验） |
| run #3 ID | 3 |
| run #3 恢复后计数（pending/processing/retryable_error/processed/auto_excluded） | _待填_ |
| retry run ID | _待填_ |
| retry run item 数 / 来源失败事件数 | _待填_ |
| 备份目录 | _待填_ |

## 最终验证清单

```bash
cd ml-service && pytest -q
cd ../backend && go test ./... -count=1
cd ../frontend && npm test && npm run build
cd .. && git diff --check
```
在独立 checkout 执行上述自动化验证；不要以 NAS 生产数据库替代测试库。上线前后均运行目标化 SQLite 查询，禁止在运行中的生产数据库上做全表导出或无范围 UPDATE。

## 不包含

- 不变更 v2 阈值、YuNet 模型版本或自动隔离策略，不把 `2026may` 模型替换进当前 `opencv-yunet-2023mar` 契约。
- 不删除照片、原始文件、Face、审计事件、embedding 或人物关系。
- 不运行 full/enforce，不自动恢复旧自动隔离项，不覆盖任何人工结论。
- 不新增浏览器可写的模型/摘要/阈值配置，不允许模型在容器运行时下载。
- 不修复与本事故无关的前端、People 聚类、展示策略或外部 people-worker 行为。

## 交付边界

本任务书基于 NAS 的只读证据和已停止的 `relive` 后端编写；它本身不修改产品代码、模型资产、镜像或数据库。实施完成前，#3 必须保持物理冻结；只有任务 1–3 的测试与任务 4 的 build/health preflight 都通过，才允许重新启动后端并进行 shadow 恢复。
