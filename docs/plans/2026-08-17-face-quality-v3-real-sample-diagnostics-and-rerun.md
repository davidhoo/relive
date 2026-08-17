# Face Quality V3 Real-Sample Diagnostics and Rerun Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让 Face #538580 的 v3 shadow 能记录“YuNet 实际检测到了什么框、与目标框相差多少”，可安全重复验证，并只在真实证据证明可行时调整目标匹配策略。

**Architecture:** 运行 #6 已证明部署、YuNet 与 v3 数据链路可用，但真实照片中 `max_context_score=0.77609`、`target_match_iou=null`，因此当前仅知道“有候选脸但未匹配目标”，不能判断候选是目标还是旁人。先将候选框与重叠诊断从 ML 透传到审计证据，再允许 `face_ids` 定点 shadow 覆盖同规则的旧自动证据；重跑 #538580 后，依据真实几何数据选择匹配策略，不能预先降低 IoU 阈值。

**Tech Stack:** Python / FastAPI / OpenCV YuNet、Go / Gin / GORM / SQLite、Vue 3 / TypeScript、pytest / Go test / Vitest。

---

## 已确认的线上事实

- NAS 上运行 #6：`calibration + shadow`、`rule_version=face_quality_v3`、`selection_policy=explicit_face_ids`，只含 Face #538580，已完成，未自动隔离。
- 运行 #6 新事件 `#285613`：`verification_status=no_face`、`verifier_score=0`、`max_context_score=0.77609`、无 `target_match_iou`，原因码为 `target_face_not_matched,context_face_not_target`。
- Face #538580 的 `person_id=264862`、`cluster_status=manual`、排除状态均未变。
- 事件行的 `rule_version=face_quality_v3`，但其 `evidence_json.rule_version=face_quality_v2`；这是后端硬编码造成的审计不一致。
- 目前的单元测试只使用人工构造的“目标框与 YuNet 框高度重叠”fixture，不能代表这张 HEIC 原图的真实检测框几何。

## 不包含

- 不降低 YuNet 的检测置信度阈值 `0.5`。
- 不直接把 `target_match_iou` 阈值从 `0.3` 改小。
- 不因为 #538580 是清晰人脸就手工把它写成 `face` 或 `accepted`。
- 不删除 #6、#285613 或任何历史审计事件。
- 不改动人物归属、聚类、Embedding、照片排除或人工结论。
- 不启动 full/enforce；本计划所有运行都是 `calibration + shadow`。

## 任务 1：记录低于阈值的目标匹配诊断

**Files:**

- Modify: `ml-service/app/schemas.py`
- Modify: `ml-service/app/models/face_verifier.py`
- Test: `ml-service/tests/test_face_verifier.py`
- Modify: `backend/internal/mlclient/client.go`
- Modify: `backend/internal/model/face.go`
- Modify: `backend/internal/service/face_quality_rescore.go`
- Test: `backend/internal/service/face_quality_rescore_test.go`

**Step 1: 先写 ML 失败测试。**

为 `_match_target_face` 构造一个真实形状的反例：目标框为 `(744,500,745,1083)`，候选框置信度为 `0.77609`，但 IoU 小于 `0.3`。断言结果仍为 `no_face`，同时返回以下诊断，不能丢失：

```python
assert result.verification_status == "no_face"
assert result.max_context_score == 0.77609
assert result.best_target_iou is not None
assert result.best_target_candidate_score == 0.77609
assert result.target_match_iou is None
```

候选框的具体坐标以 NAS 重跑得到的真实数据填入测试；不得继续使用任意的“等价 fixture”替代真实几何关系。

**Step 2: 运行失败测试。**

```bash
cd ml-service
./.venv/bin/python -m pytest -q tests/test_face_verifier.py -k 'diagnostic or target'
```

预期：因 `best_target_iou` / `best_target_candidate_score` 尚不存在而失败。

**Step 3: 只增加诊断字段，不改变判定。**

在 `VerifyKnownFaceCropResult` 和 Go 对应 DTO 中增加：

```text
best_target_iou: optional float
best_target_candidate_score: float
best_target_candidate_box: optional {x, y, width, height}
```

修改 `_match_target_face`：始终计算所有候选与目标框的最大 IoU；只有 `IoU >= 0.3` 时才填 `target_match_iou` 并返回 `face`。低于阈值时填写 `best_target_*`，仍返回 `no_face`。

在 `FaceQualityEvidenceV2` 以 `target_match_diagnostics` 嵌套 JSON 保存这些字段。该字段仅供审计/排障；不作为自动隔离、质量分或 UI 的确认分。

**Step 4: 修正证据中的规则版本。**

把：

```go
RuleVersion: model.FaceQualityRescoreRuleVersionV2,
```

改为由调用方传入当前 `run.RuleVersion`：

```go
ev := buildV2Evidence(crops, result, outcome)
ev.RuleVersion = run.RuleVersion
```

或将 `runRuleVersion string` 加为 `buildV2Evidence` 参数。不得用 ML 响应或前端请求推导规则版本。

**Step 5: 写 Go 失败测试并实现。**

在 `face_quality_rescore_test.go` 断言：

```go
assert.Equal(t, model.FaceQualityRescoreRuleVersionV3, evidence.RuleVersion)
assert.InDelta(t, 0.77609, evidence.BestTargetCandidateScore, 0.000001)
assert.NotNil(t, evidence.BestTargetIoU)
assert.Nil(t, evidence.TargetMatchIoU)
```

**Step 6: 运行验证并提交。**

```bash
cd ml-service && ./.venv/bin/python -m pytest -q tests/test_face_verifier.py
cd ../backend && go test ./internal/mlclient ./internal/service -run 'TestRescore_|TestBuildV2' -count=1
git add ml-service/app/schemas.py ml-service/app/models/face_verifier.py ml-service/tests/test_face_verifier.py backend/internal/mlclient/client.go backend/internal/model/face.go backend/internal/service/face_quality_rescore.go backend/internal/service/face_quality_rescore_test.go
git commit -m "feat: persist v3 target match diagnostics"
```

## 任务 2：允许定点 shadow 追加复核同规则 Face

**Files:**

- Modify: `backend/internal/repository/face_quality_rescore_repo.go`
- Test: `backend/internal/repository/face_quality_rescore_repo_test.go`
- Modify: `backend/internal/service/face_quality_rescore.go`
- Test: `backend/internal/service/face_quality_rescore_test.go`

**Step 1: 写失败测试。**

建立一个 Face，它已有当前 `source=auto + rule_version=face_quality_v3` 的事件。断言：

```go
targets, err := repo.ListIndependentSnapshotTargets(
    model.FaceQualityRescoreRuleVersionV3, 0, []uint{face.ID},
)
require.NoError(t, err)
require.Len(t, targets, 1)
assert.Equal(t, face.ID, targets[0].FaceID)
```

再建立一条当前 `source=manual` 事件，断言定点请求仍返回零目标。人工结论优先级不得因定点重跑被绕过。

**Step 2: 运行失败测试。**

```bash
cd backend
go test ./internal/repository ./internal/service -run 'Test.*Targeted.*(Existing|Manual|V3)' -count=1
```

预期：已有 v3 自动事件的 Face 被当前同规则排除，测试失败。

**Step 3: 修改快照选择的最小边界。**

在 `ListIndependentSnapshotTargets` 中：

- `faceIDs` 非空时，保留“目标必须存在”和“排除当前 manual”的条件；
- 不应用“已有当前相同 rule_version 自动事件则排除”的条件；
- 非定点运行仍保留原有同规则去重，避免全量重复复核；
- 不新增 `full + face_ids`，仍只允许 `calibration + shadow`、最多 50 个目标。

新运行会追加新事件并以 `is_current=true` 取代旧自动事件的当前性；旧事件保留审计记录。不得 UPDATE 历史 `evidence_json`，更不得删除 #6。

**Step 4: 运行验证并提交。**

```bash
cd backend
go test ./internal/repository ./internal/service -run 'Test.*Targeted|TestRescore_V3' -count=1
git add internal/repository/face_quality_rescore_repo.go internal/repository/face_quality_rescore_repo_test.go internal/service/face_quality_rescore.go internal/service/face_quality_rescore_test.go
git commit -m "fix: allow explicit v3 shadow re-verification"
```

## 任务 3：基于真实框决定匹配策略，不预设放宽规则

**Files:**

- Modify (only after diagnostics are known): `ml-service/app/models/face_verifier.py`
- Test: `ml-service/tests/test_face_verifier.py`
- Test: `ml-service/tests/test_face_router.py`
- Modify (only if evidence fields change): `ml-service/app/schemas.py`
- Modify (only if Go DTO changes): `backend/internal/mlclient/client.go`

**Step 1: 在 NAS 进行第二次定点 shadow。**

部署任务 1 和任务 2 后，以管理员 JWT 创建：

```sh
TOKEN='JWT'
curl -sS -X POST \
  'http://127.0.0.1:8080/api/v1/people/face-quality/rescore-runs' \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "mode":"calibration",
    "pipeline_version":"independent_v2",
    "rule_version":"face_quality_v3",
    "face_ids":[538580]
  }'
```

记录新事件的 `best_target_candidate_box`、`best_target_iou`、`best_target_candidate_score` 和目标框。该运行必须是 shadow。

**Step 2: 先判定候选身份，再决定代码。**

| 真实诊断结果 | 结论 | 后续改动 |
| --- | --- | --- |
| 最高重叠候选在目标框内/紧邻目标，且视觉确认是 #538580，但 IoU < 0.3 | 两个检测器框尺度或锚点不同 | 新增受限的补充关联规则：候选被目标框高覆盖、候选中心位于目标可接受区域、且没有更接近的竞争候选；保留原始 IoU，并新增 `match_method=coverage_center`。 |
| 0.77609 候选是旁人 | 当前 v3 `review_required` 正确 | 不放宽匹配。评估更小的目标上下文、独立身份特征或人工审核，不把旁人自动判为目标。 |
| 无候选靠近目标且 YuNet 没检出目标 | YuNet 对该真实样本漏检 | 不放宽关联。评估第二独立检测器/人工审核；保持 `review_required`。 |

**Step 3: 若且仅若第一种结果成立，先写失败测试。**

将 NAS 得到的匿名化框坐标写入单元测试。测试必须同时证明：

- 该真实几何关系能以 `coverage_center` 匹配到目标；
- 原始 IoU 如实保留，不伪造成 `>=0.3`；
- 群像中的高分非目标脸仍不能满足补充规则；
- 任一检测框未达到严格条件时仍为 `review_required`。

不要把这组坐标、原图 Base64 或私人照片加入仓库。

**Step 4: 实现最小补充规则并运行 ML 测试。**

仅在测试证明其必要时实现。`verification_status=face` 的证据必须明确记录 `match_method` 为 `iou` 或 `coverage_center`，UI 不得把 `max_context_score` 当作确认分。

```bash
cd ml-service
./.venv/bin/python -m pytest -q tests/test_face_verifier.py tests/test_face_router.py
git add app/models/face_verifier.py app/schemas.py tests/test_face_verifier.py tests/test_face_router.py
git commit -m "fix: associate verified target faces by evidence"
```

## 任务 4：前端与全量回归验证

**Files:**

- Modify (only if diagnostic display is added): `frontend/src/types/people.ts`
- Modify (only if diagnostic display is added): `frontend/src/views/People/FaceQualityReview.vue`
- Test: `frontend/src/views/People/FaceQualityReview.spec.ts`

**Step 1: 保持现有三分支主文案。**

普通审核页继续只显示：

- “已匹配目标脸”；
- “未匹配到目标脸；裁剪中其他检测最高 NN%”；
- “裁剪中未检测到可匹配的目标脸”。

候选坐标仅在受控的“诊断详情”折叠区域显示，不能成为普通用户的误导性评分。

**Step 2: 全量验证。**

```bash
cd backend && go test ./...
cd ../ml-service && ./.venv/bin/python -m pytest -q
cd ../frontend && npm run test -- src/views/People/FaceQualityReview.spec.ts
cd ../frontend && npm run typecheck && npm run build
cd .. && git diff --check
```

预期：全部通过；`git diff --check` 无输出。

## NAS 验收与停止条件

1. 部署 backend 与 `relive-ml`，确认 ML health 返回 `verifier_available=true`、`yunet`、`opencv-yunet-2023mar`。
2. 使用任务 2 的定点 shadow 重跑 Face #538580，确认新增运行而非修改 #6。
3. 读取新证据的目标框诊断；按任务 3 的决策表判断，禁止凭主观印象放宽规则。
4. 仅在“候选确为目标”的证据成立且新增测试通过后，再部署匹配规则修复并第三次 shadow 验收。
5. 最终验收：#538580 为 `face` 或有明确、可解释的 `review_required` 根因；无自动隔离；`person_id=264862`、`cluster_status=manual`、排除状态不变；事件行和 `evidence_json` 的规则版本一致。
6. 完成定点验证后，才可讨论 50～100 个样本的 v3 shadow。full/enforce 仍须单独授权。

## 回滚

- 任一 shadow 误判、诊断缺失或 ML 服务异常：停止后续运行，不启动 enforce。
- 应用回滚仅回退 backend 与 `relive-ml` 镜像；保留新增事件与运行用于审计。
- 不删除 `face_quality_events`，不直接修改数据库分数，不恢复或改写 `person_id`。
