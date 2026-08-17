# 人脸质检 v4：YuNet 检测尺度归一化 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 修复高分辨率清晰人脸因以原始像素尺度直接送入 YuNet 而漏检的问题；保留原图质量计算、目标框 IoU 安全匹配和 shadow 优先发布边界。

**Architecture:** 现有 Go 端输出未缩放的原图上下文裁剪，ML 的 `FaceVerifier._run_yunet` 直接以该尺寸推理。新增一个“仅供检测”的等比缩小副本：目标脸最长边超过 256 px 时缩小；YuNet 的候选框再映射回未缩放上下文坐标系，复用既有 IoU 匹配。清晰度、亮度、对比度和遮挡始终从未缩放原图目标框计算。

**Tech Stack:** Python / FastAPI / OpenCV YuNet、Go / Gin / GORM / SQLite、Vue 3 / TypeScript / Vitest。

---

## 背景与根因

`Face #538580` 与 `Face #538582` 是 4032 × 3024 的 HEIC 照片；两者都有两张脸。v3 已正确拒绝裁剪内另一张脸，不能降低 IoU 来“修复”：

| Face | 目标框原图像素 | 上下文裁剪 | 当前 v3 结果 |
| --- | ---: | ---: | --- |
| #538580 | 745 × 1083 | 2235 × 2666 | `no_face`；另一张脸最高 77.609% |
| #538582 | 749 × 1119 | 2247 × 2659 | `no_face`；另一张脸最高 76.363% |

当前 ML 代码把上述大裁剪直接送入 YuNet。YuNet 的适用检测脸尺度约为 10 × 10 至 300 × 300 px；两个目标脸最长边均超过 1000 px。原图分辨率对质量计算有价值，但对人脸检测输入没有必要，且超出模型主要工作尺度。

`#538665` 的目标框仅 40 × 51 px，属于“不应放大”的控制样本。它的现有 92.4% 是旧 v2 证据，未记录目标框 IoU，不能作为已验证目标脸的依据。

## 目标行为

1. 目标脸最长边大于 256 px 时，等比缩小上下文副本后再调用 YuNet；小脸不得放大。
2. 检测框必须映射回未缩放上下文坐标系，再运行既有 IoU ≥ 0.3 匹配。
3. 质量特征仍只使用未缩放原图目标框；原图尺寸和质量逻辑不得改变。
4. 新证据记录检测缩放比例和实际检测输入尺寸；候选框与 IoU 仍表示未缩放坐标。
5. 规则版本新增 `face_quality_v4`，只有完成且闭合的 v4 calibration 才能成为未来 v4 enforce 的依据。

## 不包含

- 不改 YuNet 置信度阈值 0.5、IoU 阈值 0.3、上下文扩展比例或质量阈值。
- 不引入 MediaPipe/第二模型，不替换 YuNet 资产。
- 不删除或改写现有 Face、照片、人物归属、聚类、人工结论、历史事件。
- 不启动 v4 full/enforce，也不自动处理审核队列。
- 不修复“item 已完成但 run 仍 running”的独立任务收尾问题；若它阻塞 v4 shadow，先停止并单独处理。

## 任务 1：以失败测试固定尺度归一化契约

**Files:**

- Modify: `ml-service/tests/test_face_verifier.py`
- Modify: `ml-service/tests/test_face_router.py`

**Step 1: 写缩放计划的失败测试。**

为内部函数（建议 `_plan_detection_input`）添加测试：

```python
def test_detection_plan_downscales_large_target_without_upscaling_small_target():
    large = _plan_detection_input(2235, 2666, 745, 1083)
    assert large.scale == pytest.approx(256 / 1083)
    assert (large.input_width, large.input_height) == (
        round(2235 * 256 / 1083), round(2666 * 256 / 1083),
    )

    small = _plan_detection_input(120, 153, 40, 51)
    assert small.scale == 1.0
    assert (small.input_width, small.input_height) == (120, 153)
```

缩放依据必须是目标框最长边，不能用整张上下文最长边；否则边缘裁剪与常规裁剪会得到不一致的目标人脸尺度。

**Step 2: 写 #538580 等价的大脸失败回归。**

构造未缩放上下文 `2235 × 2666`、目标框 `(744, 500, 745, 1083)`。假 YuNet 检测器断言收到的是缩放后尺寸，并在缩放坐标中返回目标框等比候选。断言：

```python
assert result.verification_status == "face"
assert result.target_match_iou is not None
assert result.target_match_iou >= 0.3
assert result.verifier_input_scale == pytest.approx(256 / 1083)
assert result.best_target_candidate_box is not None
```

候选框审计坐标必须回到原上下文坐标，接近 `(744, 500, 745, 1083)`。

**Step 3: 写群像反例。**

在同一缩放比例下只返回右下方邻脸候选。断言映射回原坐标后仍是：

```python
assert result.verification_status == "no_face"
assert result.verifier_score == 0.0
assert result.max_context_score > 0
assert result.target_match_iou is None
```

**Step 4: 运行并确认失败。**

```bash
cd ml-service
./.venv/bin/python -m pytest -q tests/test_face_verifier.py -k 'detection_plan or scale_normalized'
```

预期：失败，因为当前实现没有检测缩放计划和审计字段。

**Step 5: 提交测试。**

```bash
git add ml-service/tests/test_face_verifier.py ml-service/tests/test_face_router.py
git commit -m "test: cover normalized YuNet detection scale"
```

## 任务 2：实现“缩放检测、原坐标匹配”

**Files:**

- Modify: `ml-service/app/models/face_verifier.py`
- Modify: `ml-service/app/schemas.py`
- Test: `ml-service/tests/test_face_verifier.py`
- Test: `ml-service/tests/test_face_router.py`

**Step 1: 定义单一检测输入计划。**

```python
_YUNET_MAX_TARGET_LONG_EDGE = 256

@dataclass(frozen=True)
class _DetectionInputPlan:
    scale: float
    input_width: int
    input_height: int

def _plan_detection_input(frame_width, frame_height, target_width, target_height):
    target_long_edge = max(target_width, target_height)
    scale = min(1.0, _YUNET_MAX_TARGET_LONG_EDGE / max(1, target_long_edge))
    return _DetectionInputPlan(
        scale=scale,
        input_width=max(1, round(frame_width * scale)),
        input_height=max(1, round(frame_height * scale)),
    )
```

短边小于 24 px 的 `uncertain` 判断仍以未缩放上下文为准，不能因放大而伪造可靠证据。

**Step 2: 实现最小数据流。**

在 `FaceVerifier._verify_one`：

1. 解码 `frame`，保留原 `context_crop_width_px` / `context_crop_height_px`。
2. 在原尺寸进行短边判断。
3. 仅在 `scale < 1` 时使用 `cv2.INTER_AREA` 生成 `detection_frame`。
4. 对 `detection_frame` 调用 `_run_yunet`。
5. 将 YuNet 检测框按 `round(value / scale)` 映射回原上下文，裁剪到原图边界；`scale == 1` 时不得额外取整。
6. 仅将映射后的候选框传给 `_match_target_face`。
7. 对原 `frame` 调用 `_compute_quality`，参数不变。

不得改变 YuNet 阈值、IoU 阈值或上下文裁剪策略。

**Step 3: 扩展 ML schema。**

在 `ml-service/app/schemas.py`：

- 新增 `EVIDENCE_SCHEMA_VERSION_V2_TARGET_MATCH_SCALE_NORMALIZED = "independent_v2_target_match_v3"`；
- 为 `VerifyKnownFaceCropResult` 新增：

```python
verifier_input_scale: float = 1.0
verifier_input_width_px: int = 0
verifier_input_height_px: int = 0
```

只有本任务新输出使用新 schema；旧 `independent_v2` 与 `independent_v2_target_match_v2` 必须继续可读。

**Step 4: 验证并提交。**

```bash
cd ml-service
./.venv/bin/python -m pytest -q tests/test_face_verifier.py tests/test_face_router.py
git add app/models/face_verifier.py app/schemas.py tests/test_face_verifier.py tests/test_face_router.py
git commit -m "fix: normalize oversized YuNet verification inputs"
```

## 任务 3：透传审计字段并建立 v4 校准门禁

**Files:**

- Modify: `backend/internal/mlclient/client.go`
- Modify: `backend/internal/model/face.go`
- Modify: `backend/internal/model/face_quality_rescore.go`
- Modify: `backend/internal/model/dto.go`
- Modify: `backend/internal/service/face_quality_rescore.go`
- Modify: `backend/internal/service/face_quality_rescore_test.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/people_rescore_handler_test.go`

**Step 1: 写 Go 失败测试。**

构造 ML 返回：

```go
VerifyKnownFaceCropResult{
    VerificationStatus: "face",
    VerifierInputScale: 256.0 / 1083.0,
    VerifierInputWidthPx: 527,
    VerifierInputHeightPx: 629,
    EvidenceSchemaVersion: "independent_v2_target_match_v3",
}
```

断言 `buildV2Evidence` 原样持久化三个输入字段和 schema。另建完成的 v3 校准，尝试创建 v4 full/enforce，断言拒绝；建立完成、计数闭合且 `retryable_count=0` 的 v4 calibration 后才允许其取得 enforce 资格。

**Step 2: 运行并确认失败。**

```bash
cd backend
go test ./internal/service ./internal/api/v1/handler -run 'Test.*(V4|VerifierInputScale|CalibrationRule)' -count=1
```

**Step 3: 实现最小 Go 变更。**

1. 在 `mlclient.VerifyKnownFaceCropResult` 与 `model.FaceQualityEvidenceV2` 增加相同 JSON 字段；字段缺失必须兼容旧证据。
2. `buildV2Evidence` 原样写入这些字段；原图尺寸、质量字段、候选框坐标语义不变。
3. 在 rescore 模型常量中新增 `FaceQualityRescoreRuleVersionV4 = "face_quality_v4"`。
4. 创建 run 时显式接受 v2/v3/v4；未知的非空规则版本必须返回参数错误，不能静默降级 v2。
5. v4 快照选择“无人工结论、无当前 v4 自动事件”的 Face，因此可重跑已有 v2/v3 自动证据；人工事件始终优先。
6. 将当前仅检查 v3 的 full/enforce 门禁泛化为“请求 rule_version 必须等于 calibration run 的 rule_version”；错误码和文案改为通用规则版本不匹配。
7. 同步 DTO、handler 测试和 API 注释；不新增任何 enforce 页面入口。

**Step 4: 验证并提交。**

```bash
cd backend
go test ./internal/service ./internal/api/v1/handler -run 'Test.*(Rescore|V4|VerifierInputScale|Calibration)' -count=1
go test ./internal/repository -run 'Test.*FaceQualityRescore' -count=1
git add internal/mlclient/client.go internal/model/face.go internal/model/face_quality_rescore.go internal/model/dto.go internal/service/face_quality_rescore.go internal/service/face_quality_rescore_test.go internal/api/v1/handler/people_handler.go internal/api/v1/handler/people_rescore_handler_test.go
git commit -m "feat: version normalized YuNet evidence as face quality v4"
```

## 任务 4：审核页显示检测尺度，且不误读旧证据

**Files:**

- Modify: `frontend/src/types/people.ts`
- Modify: `frontend/src/views/People/FaceQualityReview.vue`
- Modify: `frontend/src/views/People/FaceQualityReview.spec.ts`

**Step 1: 写失败测试。**

构造 v4 evidence：`evidence_schema_version="independent_v2_target_match_v3"`、`verifier_input_scale=0.23638`、`verifier_input_width_px=527`、`verifier_input_height_px=629`。断言技术诊断显示：

```text
YuNet 检测输入：527 × 629 px（缩放 23.6%）
```

再用旧 v2 fixture 断言仍显示“旧证据，未记录目标匹配诊断”，不得显示缩放字段或伪造“已匹配目标脸”。

**Step 2: 实现最小 UI 变更。**

- 扩展 `FaceQualityEvidenceV2` 可选字段。
- 仅当三个新字段完整存在时，在既有技术诊断区域显示检测输入。
- 三分支主文案不变；`max_context_score` 永远不是确认分。

**Step 3: 验证并提交。**

```bash
cd frontend
npm run test -- src/views/People/FaceQualityReview.spec.ts
npm run typecheck
npm run build
git add src/types/people.ts src/views/People/FaceQualityReview.vue src/views/People/FaceQualityReview.spec.ts
git commit -m "feat: show normalized YuNet input diagnostics"
```

## 任务 5：全量回归与 NAS v4 shadow 验收

**Files:**

- No product code changes.

**Step 1: 本地完整回归。**

```bash
cd backend && go test ./...
cd ../ml-service && ./.venv/bin/python -m pytest -q
cd ../frontend && npm run build
cd .. && git diff --check
```

**Step 2: 部署前检查。**

在 NAS 只读确认：

1. 没有“所有 item 已处理、但 run 仍 running”的旧任务；若有，停止本任务，转交独立收尾修复。
2. 新镜像部署后 `relive` 和 `relive-ml` 均为 healthy。
3. `/api/v1/health` 返回 `verifier_available=true`、`yunet`、`opencv-yunet-2023mar`。

**Step 3: 创建 v4 定点 shadow。**

```sh
TOKEN='JWT'
curl -sS -X POST \
  'http://127.0.0.1:8080/api/v1/people/face-quality/rescore-runs' \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "mode": "calibration",
    "pipeline_version": "independent_v2",
    "rule_version": "face_quality_v4",
    "face_ids": [538580, 538582, 538665]
  }'
```

`calibration` 必须是 shadow。不得打印、提交或传递 JWT。

**Step 4: 定点验收。**

对 #538580、#538582，要求：

- run 为 `completed`、`retryable_count=0`、计数闭合；
- 新事件为 `face_quality_v4` / `independent_v2_target_match_v3`；
- `verifier_input_scale < 1`；
- `verification_status=face`、`target_match_iou >= 0.3`；
- 无 `context_face_not_target`；
- shadow 未改写人物归属、聚类、人工锁定或排除状态。

对 #538665，只要求 `verifier_input_scale=1`。它必须按目标框真实匹配，而不是因相邻 #538664 的分数通过；未匹配时保持 `review_required`，不得自动排除。

**Step 5: 扩大但保持 shadow。**

定点通过后，以 50–100 个高分辨率 Face 建立 v4 calibration+shadow，覆盖清晰大脸、HEIC 方向元数据、多人同框、边缘脸和小脸控制组。人工抽查所有 v4 新通过样本：零个明显邻脸误匹配；未确认样本只能进入 `review_required`。记录通过、待审、重试和抽查误匹配数。

**Step 6: 停止条件与回滚。**

出现任一情况立即停止，不启动 full/enforce：#538580/#538582 仍未匹配；群像误匹配；scale/schema/rule 缺失或不一致；技术重试非零；shadow 改写人物、聚类、人工结论或排除状态。

回滚只回退 backend 与 `relive-ml` 镜像；保留新增 run 与 `face_quality_events` 审计记录，不手改数据库、不删除历史证据。
