# 人脸质检 v4：YuNet 边界候选框修复与全量任务恢复 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 修复 YuNet 在未缩放输入中返回负坐标候选框时导致整条独立复核失败的问题，使 #13 可安全继续处理，并在结束后补跑失败项。

**Architecture:** YuNet 的检测框属于图像边界坐标；框的左上角略微在裁剪图外（如 `x=-1`）是模型允许的边界输出。统一在 ML 服务的“检测框映射到未缩放上下文坐标”边界做裁切，保证后续 IoU、审计候选框和 Pydantic DTO 只接收图内有效框。不得放宽 DTO 的非负约束、IoU 阈值或自动隔离规则。

**Tech Stack:** Python 3.12、FastAPI、Pydantic、OpenCV YuNet、pytest；Go/Gin 历史重评分 API；Docker Compose。

---

## 背景与已确认事实

NAS 的 v4 full/enforce 运行 #13 已创建后暂停，快照范围为 79,323 张照片、270,864 个 Face。暂停前累计结果：

| 项目 | 数量 |
| --- | ---: |
| 已处理 | 10,010 |
| 已接受 | 8,738 |
| 待人工审核 | 367 |
| 已自动隔离 | 905 |
| 待重试 | 538 |
| 仍 pending | 260,316 |

ML 日志的完整故障点是：

```text
app/models/face_verifier.py:285
CandidateBox(x=bx, y=by, width=bw, height=bh)
ValidationError: x
Input should be greater than or equal to 0; input_value=-1
```

本地最小复现已确认：

```python
_map_boxes_to_original([(0.91, (-1, 10, 30, 40))], 1.0, 100, 100)
# 当前返回 (-1, 10, 30, 40)
```

根因位于 `ml-service/app/models/face_verifier.py`：`_map_boxes_to_original()` 在 `scale >= 1` 时直接返回 YuNet 原始框；只有 `scale < 1` 的分支裁切边界。`CandidateBox` 在 `ml-service/app/schemas.py` 正确地要求 `x/y/width/height >= 0`，因此诊断候选框构造时抛出未捕获异常。该异常会使该 Face 成为 `retryable_error`，而不是产生 `face`、`no_face` 或 `review_required` 证据。

## 目标行为

1. 无论 `scale` 是否为 1，进入 `_match_target_face()` 和 `CandidateBox` 的候选框都必须完全位于未缩放上下文图内。
2. 对于仅左/上越界的框，左/上裁为 `0`，右/下边界保持原位置，因此宽高相应缩短。
3. 对于完全在图外、或裁切后宽高不大于 0 的框，直接丢弃；它不能参与 `max_context_score`、IoU 或诊断。
4. 图内合法框、`face_quality_v4` 规则版本、YuNet 阈值 0.5、目标框 IoU 阈值 0.3、质量计算域均保持不变。
5. 修复后，原有 #13 必须先恢复处理 pending 项；其既有 538 个 retryable 项在当前产品契约下不能通过 `/retry` 重跑（该接口只接受 `calibration` 来源）。#13 终态后，再以同一 #11 校准创建一个新的 v4 full/enforce；由于已成功处理的 Face 已有当前 v4 自动事件，新 run 只会快照未获 v4 结论的失败项（以创建响应中的实际目标数为准）。

## 不包含

- 不允许把 `CandidateBox` 的 `x/y` schema 改为允许负数。
- 不改 YuNet 模型、模型版本、置信度阈值、IoU 阈值、上下文扩展比例或质量阈值。
- 不改变 #13 暂停前的审计事件、人工结论、人物归属、聚类或已自动隔离 Face。
- 不通过 SQLite 直接改 run、item、事件或排除记录。
- 不新增“full run 的 retry API”；该接口限制是独立的产品改进，不作为本次故障修复的前置条件。
- 不恢复 #13，直到 ML 回归测试、镜像部署和 NAS health 验收完成。

## 开发与交付约定

- 直接在当前 `main` 工作区开发；不创建分支或 worktree。
- 本文档不包含 `git commit`、`git push` 或发布标签操作。
- 实现完成后保留已验证的未提交改动和测试结果，由操作者自行决定何时 commit、push。

## 任务 1：用失败回归测试固定 `scale=1` 的边界框契约

**Files:**

- Modify: `ml-service/tests/test_face_verifier.py`
- Test: `ml-service/tests/test_face_verifier.py`

**Step 1: 编写最小失败测试。**

在现有 `test_verifier_scale_not_applied_to_small_target` 附近新增：

```python
def test_verifier_clamps_edge_candidate_box_when_scale_is_one(tmp_path: Path):
    def factory():
        class D:
            def setInputSize(self, _): ...
            def detect(self, _frame):
                # YuNet 候选略越过左、上边界；右下仍在 100x100 图内。
                return None, np.array([[-1, -2, 31, 42, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.91]])
        return D()

    verifier = _make_verifier_with_factory(tmp_path, factory)
    frame = np.full((100, 100, 3), 200, dtype=np.uint8)
    result = verifier.verify_crops([VerifyKnownFaceCropTarget(
        face_id=9001,
        context_crop_base64=_encode_image(frame),
        face_box_offset_x=60,
        face_box_offset_y=60,
        face_box_width_px=20,
        face_box_height_px=20,
        primary_detector_score=0.8,
    )]).results[0]

    assert result.verifier_input_scale == 1.0
    assert result.verification_status == "no_face"
    assert result.best_target_candidate_box is not None
    assert (result.best_target_candidate_box.x, result.best_target_candidate_box.y) == (0, 0)
    assert (result.best_target_candidate_box.width, result.best_target_candidate_box.height) == (30, 40)
```

目标框刻意放在右下，确保本测试仅验证边界归一化，不会因 IoU 命中而混入“是否是目标脸”的语义。

**Step 2: 运行测试并确认当前代码失败。**

```bash
cd ml-service
./.venv/bin/python -m pytest -q \
  tests/test_face_verifier.py::test_verifier_clamps_edge_candidate_box_when_scale_is_one
```

预期：失败，错误包含 `ValidationError` 和 `input_value=-1`。

**Step 3: 补充缩放分支保护测试。**

新增一个直接测试 `_map_boxes_to_original()`：同一越界框在 `scale=0.5` 与 `scale=1.0` 时均返回非负、图内、正宽高的框。断言正常图内框（例如 `(12, 12, 38, 49)`）在 `scale=1` 下保持完全不变。

## 任务 2：在坐标边界统一裁切候选框

**Files:**

- Modify: `ml-service/app/models/face_verifier.py`
- Test: `ml-service/tests/test_face_verifier.py`

**Step 1: 新增单一图内裁切辅助函数。**

在 `_map_boxes_to_original()` 附近新增私有函数。它接收已处于未缩放上下文坐标系的 `(x, y, width, height)` 和 `frame_width/frame_height`，按边界交集返回 `tuple[int, int, int, int] | None`：

```python
def _clip_box_to_frame(x: int, y: int, width: int, height: int,
                       frame_width: int, frame_height: int) -> tuple[int, int, int, int] | None:
    x0, y0 = max(0, x), max(0, y)
    x1, y1 = min(frame_width, x + width), min(frame_height, y + height)
    if x1 <= x0 or y1 <= y0:
        return None
    return x0, y0, x1 - x0, y1 - y0
```

不要用 `abs(x)`、不要把负坐标简单置零但保持原宽度；那会把原本图外的像素凭空扩展到图内。

**Step 2: 统一 `_map_boxes_to_original()` 的输出路径。**

保留当前比例映射规则：

- `scale < 1`：先按 `round(value / scale)` 映射到原坐标；
- `scale >= 1`：保留整数坐标，不引入额外缩放取整。

随后两条路径都必须调用 `_clip_box_to_frame()`。仅把非 `None` 的裁切结果加入输出。这样 `_match_target_face()`、`max_context_score` 和 `CandidateBox` 永远不会接收负坐标或越界宽高。

**Step 3: 不改 schema 和判定策略。**

保留 `CandidateBox` 的 `Field(ge=0)`；它是正确的 API 边界保护。不要修改 `_YUNET_CONFIDENCE_THRESHOLD`、`_YUNET_TARGET_IOU_THRESHOLD`、`_YUNET_MAX_TARGET_LONG_EDGE`、`_compute_quality()` 或 `face_quality_v4` 版本号。

**Step 4: 运行针对性测试。**

```bash
cd ml-service
./.venv/bin/python -m pytest -q tests/test_face_verifier.py
```

预期：新测试通过，既有 #538580/#538582 大脸缩放、#538665 小脸控制样本和群像反例全部继续通过。

## 任务 3：完整回归与镜像构建验收

**Files:**

- Verify only: `ml-service/app/models/face_verifier.py`
- Verify only: `ml-service/tests/test_face_verifier.py`
- Verify only: `ml-service/tests/test_face_router.py`
- Verify only: `ml-service/Dockerfile`

**Step 1: 运行 ML 全量测试。**

```bash
cd ml-service
./.venv/bin/python -m pytest -q
```

预期：全绿；不得只以新用例通过作为完成依据。

**Step 2: 构建镜像。**

在仓库根目录执行：

```bash
/usr/local/bin/docker compose build relive-ml
```

预期：YuNet 资产 SHA-256 校验通过，镜像构建成功。

**Step 3: 检查改动范围。**

```bash
git diff --check
git status --short
```

预期：没有空白错误；改动只包含 ML 代码与测试（以及本计划文档）。

## 任务 4：NAS 部署、#13 恢复与失败项收尾

**前置条件：** 任务 1–3 全绿；#13 仍为 `paused`；不允许有其它 `queued/running/paused` 的 rescore run。

**Step 1: 部署 ML 镜像，不重建 backend。**

在 NAS 的 `/volume1/docker/relive`：

```bash
/usr/local/bin/docker compose build relive-ml
/usr/local/bin/docker compose up -d --no-deps relive-ml
```

**Step 2: 验证 verifier 身份。**

```bash
/usr/local/bin/docker exec relive-ml python -c \
  "import urllib.request; print(urllib.request.urlopen('http://127.0.0.1:5050/api/v1/health').read().decode())" | jq
```

必须同时满足：

```json
{
  "status": "ok",
  "verifier_available": true,
  "verifier_name": "yunet",
  "verifier_version": "opencv-yunet-2023mar"
}
```

**Step 3: 先看近期 ML 日志。**

```bash
/usr/local/bin/docker logs --since 2m relive-ml 2>&1 | tail -100
```

预期：没有新的 `CandidateBox` / `input_value=-1` / `ValidationError`。

**Step 4: 恢复 #13。**

仅在步骤 2–3 通过后，以管理员 JWT 执行：

```bash
curl -sS -X POST \
  'http://127.0.0.1:8080/api/v1/people/face-quality/rescore-runs/13/resume' \
  -H "Authorization: Bearer $TOKEN" | jq
```

不要打印、提交或传递 `$TOKEN`。恢复后仅处理 `pending` 的 260,316 项；此前已经标为 `retryable_error` 的 538 项不会被此 run 自动再领走。

**Step 5: 运行中抽查。**

在处理至少 100 个新 Face 后，确认：

- `retryable_count` 未因 `CandidateBox` 负坐标继续增加；
- ML 日志无同类 `ValidationError`；
- `processed_face_count` 持续上升；
- 人脸审核页的新增 `review_required` 是真实灰区，不是技术错误。

发现同类错误、`retryable_count` 明显继续增长或 verifier 不可用时，立即再次 pause #13，保留日志证据，不要继续运行。

**Step 6: #13 队列耗尽后创建失败项收尾 full run。**

#13 预计进入 `completed_with_errors`，原因是其 538 个已失败 item 仍存在。不得调用 `/13/retry`：当前 API 明确只接受 `calibration` 来源。

确认 #13 已终态且无其它活跃 run 后，使用已验证的 #11 v4 calibration 创建新的 full/enforce：

```bash
curl -sS -X POST \
  'http://127.0.0.1:8080/api/v1/people/face-quality/rescore-runs' \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "mode": "full",
    "calibration_run_id": 11,
    "pipeline_version": "independent_v2",
    "rule_version": "face_quality_v4"
  }' | jq
```

验收：新 run 的 `target_face_count` 应接近 538（允许有人工作出结论或已有 v4 事件导致减少），绝不能重新快照数十万已成功写入 v4 的 Face。若数量异常大，立即取消新 run 并排查快照条件，禁止继续。

**Step 7: 最终验收。**

- 两个 run 均为终态；
- 后续失败项 run 的 `retryable_count=0`；
- 没有新的负坐标 `CandidateBox` 错误；
- 人工结论未被改写；
- `auto_excluded` 仅来自 full/enforce 的高确定性结果；
- 抽查 #538580、#538582、#538665 和 20 个新结果，确认清晰大脸仍以目标框匹配，不把群像邻脸当作目标。
