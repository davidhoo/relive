# 人脸质检 v2 目标框匹配修复 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 修复照片边缘的人脸在上下文裁剪被截断后被 YuNet 误判为 `no_face`，让历史和实时质检都以“是否匹配目标脸框”而不是“是否位于裁剪图中心”作出判断。

**Architecture:** Go 端已经将目标脸框在上下文裁剪中的偏移和尺寸传给 ML 服务；ML 端应使用这些参数把 YuNet 检测框与目标框做匹配。新的匹配策略作为 `face_quality_v3` 规则版本上线，保留 `independent_v2` 证据管线；历史样本按新规则重新 shadow 校准，旧 `face_quality_v2` 校准不得放行 v3 的 full/enforce。

**Tech Stack:** Go / Gin / GORM / SQLite、Python / FastAPI / OpenCV YuNet、Vue 3 / TypeScript / Vitest。

---

## 背景与已确认根因

`Face #538580` 是清楚的真实人脸：主检测分为 `0.746871`，原图人脸框为 `745 × 1083 px`，但 v2 记录为 `verification_status=no_face`、`verifier_score=0.77609`。

这不是“YuNet 完全没有检测到人脸”。当前 `ml-service/app/models/face_verifier.py` 的 `_has_centered_face`：

- 只判断 YuNet 检测框的中心是否位于**整张上下文裁剪图的中心 40%**；
- 将裁剪图内任意检测框的最高置信度写入 `verifier_score`，即使该框不在目标位置；
- 没有使用请求已携带的 `face_box_offset_x/y` 与 `face_box_width/height` 来判断检测到的是不是目标脸。

本例正常上下文裁剪应为 `3 × 745 = 2235`、`3 × 1083 = 3249 px`，实际为 `2235 × 2666 px`，说明纵向扩展被原图边界截断。被截断后目标脸不再位于裁剪图中心，固定中心 40% 门槛因此产生假阴性。

当前 `face_quality_v2` 的历史 shadow 因主检测分高于 0.65，正确地将该样本放入 `review_required`，没有自动隔离；但实时路径会直接丢弃任意独立验证器 `no_face` 的候选，存在漏掉新照片真实人脸的风险。

## 目标行为

1. YuNet 的 `face/no_face` 语义改为“是否匹配到**目标脸框**”，而不是“是否位于上下文图几何中心”。
2. 匹配仅接受与目标框重叠足够的检测框，不能让群像中附近其他人脸替代目标脸。
3. 对 `no_face`，证据同时说明“目标匹配分”和“裁剪中其他检测最高分”，避免出现“未检测到脸 77.6%”的矛盾文案。
4. 新照片中，主检测已经过线但独立验证没有匹配到目标的样本必须进入 `review_required`，不能在持久化前静默丢弃。
5. 规则版本升级为 `face_quality_v3`；旧 `face_quality_v2` 证据样本可以在 v3 shadow 中重新复核，人工结论仍绝对优先。
6. `Face #538580` 是上线前的必测定点样本：新规则必须输出 `verification_status=face`。

## 不包含

- 不降低 YuNet 的 0.5 检测置信度阈值。
- 不通过扩大或取消“裁剪图中心区域”来规避问题。
- 不改动人物聚类、身份画像、合并建议或照片级截图排除。
- 不删除 Face、照片、审计记录或既有人工结论。
- 本任务不直接启动 full/enforce，也不把历史 `review_required` 自动改成人工接受。

## 任务 1：先以失败测试固定边缘裁剪回归

**Files:**

- Modify: `ml-service/tests/test_face_verifier.py`
- Modify: `backend/internal/service/face_quality_rescore_image_test.go`

**Step 1: 新增 Python 失败测试。**

为 `FaceVerifier` 注入假的 YuNet 检测器，构造 `100 × 100` 上下文图：目标框在边界裁剪后的 `(0, 0, 25, 25)`，检测框为 `(2, 2, 23, 23)`，即目标脸处于裁剪左上而不在整图中心。断言结果为 `face`，并断言目标匹配分为检测框分数。

再构造目标框 `(0, 0, 25, 25)`、检测框 `(45, 45, 25, 25)` 的群像反例。断言为 `no_face`，目标匹配分为 `0`，但“裁剪内最高检测分”保留该检测框分数。

**Step 2: 验证旧实现失败。**

```bash
cd ml-service
./.venv/bin/python -m pytest -q tests/test_face_verifier.py -k target_box
```

预期：边缘目标脸案例在旧的裁剪中心规则下失败。

**Step 3: 增强 Go 裁剪测试。**

在现有 `TestPrepareV2FaceCrops_EdgeBoxContextClamped` 中保留对 offset 的断言，并增加以下断言：目标框 offset 为 `(0,0)` 时，调用方必须将该 offset 原样传到 v2 请求；不得将目标位置重置为裁剪中心。

**Step 4: 运行图像裁剪测试。**

```bash
cd backend
go test ./internal/service -run '^TestPrepareV2FaceCrops' -count=1
```

预期：通过。

## 任务 2：以目标框 IoU 匹配取代固定中心判定

**Files:**

- Modify: `ml-service/app/models/face_verifier.py`
- Modify: `ml-service/app/schemas.py`
- Modify: `backend/internal/mlclient/client.go`
- Modify: `backend/internal/model/face.go`
- Modify: `ml-service/tests/test_face_verifier.py`

**Step 1: 定义单一、可审计的匹配策略。**

新增内部函数，例如：

```python
def _match_target_face(
    detected: list[tuple[float, tuple[int, int, int, int]]],
    target_x: int, target_y: int, target_w: int, target_h: int,
) -> tuple[float, float | None, float]:
    """返回 (target_match_score, target_match_iou, max_context_score)。"""
```

目标框为请求中的 `face_box_offset_x/y/width/height`。对 YuNet 每个检测框计算 IoU；仅 `IoU >= 0.3` 的候选可匹配目标。多个候选命中时，选择 IoU 最大者；IoU 相同时选择分数更高者。0.3 与既有已知框重评分的 IoU 线保持一致。

不要使用裁剪图的固定中心比例；不要因为上下文内另一张脸分数高而判目标脸存在。

**Step 2: 变更 `FaceVerifier._verify_one`。**

- 使用 `_match_target_face` 决定 `face/no_face`。
- `verifier_score` 改为目标匹配分；无匹配时为 `0`。
- 新增可选诊断字段：`max_context_score`、`target_match_iou`。
- 记录 `reason_codes=["target_face_not_matched"]`；仅当裁剪内存在其他检测但无目标匹配时可附加 `"context_face_not_target"`。
- 将证据 schema 版本升级为新的字符串，例如 `independent_v2_target_match_v2`；不要伪造或改写旧证据。

**Step 3: 扩展 Python 与 Go DTO。**

在 `ml-service/app/schemas.py`、`backend/internal/mlclient/client.go`、`backend/internal/model/face.go` 的 v2 证据结构中增加相同 JSON 字段：

```text
max_context_score: float
target_match_iou: optional float
```

旧 JSON 缺少字段必须能正常解析；不得数据库迁移，因为证据保存在 `evidence_json`。

**Step 4: 运行 ML 回归测试。**

```bash
cd ml-service
./.venv/bin/python -m pytest -q tests/test_face_verifier.py tests/test_face_router.py
```

预期：边缘目标脸、群像干扰、无检测、输入过小、解码失败均通过。

## 任务 3：保护实时新照片，不能因单次 no_face 静默丢脸

**Files:**

- Modify: `backend/internal/service/people_service.go`
- Modify: `backend/internal/service/people_service_test.go`

**Step 1: 写失败测试。**

构造主检测分 `>= 0.65`、独立验证 `no_face` 的候选。断言 `filterDetectionsByIndependentVerification` 保留该候选，并带上 `VerifierStatus="no_face"`；随后 `ApplyDetectionResult` 应写 `review_required`，不得删除该 Face 或让其进入人物聚类。

**Step 2: 修改实时分流。**

将当前 `case "no_face": continue` 改为保留候选。 `no_face`、`uncertain`、`error` 都走保守的 `review_required` 分支，并写明 `verifier_no_target_match`、`verifier_uncertain` 或 `verifier_error` 原因码。

不要把实时 `no_face` 当作自动 `non_face`；历史 full/enforce 的高确定性双信号门禁保持不变。

**Step 3: 运行定向 Go 测试。**

```bash
cd backend
go test ./internal/service -run 'Test.*(IndependentVerification|FaceQuality)' -count=1
```

预期：主检测过线的真实脸不会因单个 v2 `no_face` 被丢弃；技术错误与灰区不触发自动隔离。

## 任务 4：以新规则版本重新选择旧 v2 样本，并提供定点校准

**Files:**

- Modify: `backend/internal/model/face_quality_rescore.go`
- Modify: `backend/internal/model/dto.go`
- Modify: `backend/internal/repository/face_quality_rescore_repo.go`
- Modify: `backend/internal/repository/face_quality_rescore_repo_test.go`
- Modify: `backend/internal/service/face_quality_rescore.go`
- Modify: `backend/internal/service/face_quality_rescore_test.go`
- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/people_rescore_handler_test.go`
- Modify: `frontend/src/types/people.ts`

**Step 1: 引入规则版本而非复用 #5 校准。**

增加 `FaceQualityRescoreRuleVersionV3 = "face_quality_v3"`。证据管线仍为 `independent_v2`；`face_quality_v3` 表示“同一独立验证管线的目标框匹配规则版本”。

创建 v3 full/enforce 时，`getEligibleCalibration` 除现有状态、计数和管线检查外，必须要求校准运行的 `rule_version=face_quality_v3`。因此目前 #5 的 v2 校准不能在新代码下放行 v3 enforce。

**Step 2: 规则版本化快照查询。**

把 `ListV2SnapshotTargets(photoLimit int)` 改为接收 `ruleVersion` 与可选 `faceIDs`：

```go
ListIndependentSnapshotTargets(ruleVersion string, photoLimit int, faceIDs []uint) ([]model.FaceQualityRescoreRetryTarget, error)
```

查询继续排除任何当前人工结论；但自动事件仅排除“当前 `rule_version` 已等于请求规则版本”的 Face。这样 v3 可以重新复核已有 v2 证据，人工接受/排除仍不被系统覆盖。

`face_ids` 只允许 `mode=calibration + shadow`，最多 50 个，去重、非零且必须属于当前数据库；`mode=full` 拒绝该参数。它是发布前定点验证入口，不做前端批量重跑功能。

**Step 3: 先写服务与 handler 的失败测试。**

至少覆盖：

- 有当前 `face_quality_v2` 自动事件、无人工事件的 Face 被 v3 快照选中；
- 当前人工 `accept` 的 Face 不被 v3 选中；
- `face_ids=[538580]` 只创建这个目标的 shadow 校准；
- v2 校准 #5 不能作为 v3 full/enforce 的校准来源；
- v3 校准状态 `completed`、无 retryable、计数闭合后才 `eligible_for_enforce=true`。

**Step 4: 实现最小服务和 API 改动。**

扩展 `FaceQualityRescoreRunCreateRequest` 并把 `FaceIDs` 传到 service/repository；前端类型同步，但不新增页面按钮。创建 targeted 校准可通过受保护的既有 rescore API 调用，审计日志必须保留 `selection_policy=explicit_face_ids`。

**Step 5: 运行重评分测试。**

```bash
cd backend
go test ./internal/repository -run 'Test.*FaceQualityRescore' -count=1
go test ./internal/service -run 'TestRescore_' -count=1
go test ./internal/api/v1/handler -run 'Test.*Rescore' -count=1
```

预期：旧 v2 样本可被 v3 shadow 选中，人工结论不被选中，v2 校准不能误放行 v3 enforce。

## 任务 5：审核页展示目标匹配，不再把上下文分数当成确认分

**Files:**

- Modify: `frontend/src/types/people.ts`
- Modify: `frontend/src/views/People/FaceQualityReview.vue`
- Modify: `frontend/src/views/People/FaceQualityReview.spec.ts`

**Step 1: 更新类型与详情展示。**

在 `FaceQualityEvidenceV2` 增加 `max_context_score?`、`target_match_iou?`。详情抽屉按状态显示：

| 状态 | 展示 |
| --- | --- |
| `face` | “已匹配目标脸”，显示目标匹配分与 IoU。 |
| `no_face` 且 `max_context_score > 0` | “未匹配到目标脸；裁剪中其他检测最高 NN%”。 |
| `no_face` 且无上下文检测 | “裁剪中未检测到可匹配的目标脸”。 |

不得把 `max_context_score` 作为“未检测到脸的置信度”。旧 v2 记录缺少新字段时，保留原始 JSON 查看入口并标注“旧证据，未记录目标匹配诊断”。

**Step 2: 写前端失败测试并实现。**

新增 #538580 等价 fixture：`verification_status=no_face`、`verifier_score=0`、`max_context_score=0.77609`。断言页面出现“未匹配到目标脸”和“裁剪中其他检测最高 77.6%”，不得出现“未检测到脸置信度 77.6%”。

**Step 3: 运行前端验证。**

```bash
cd frontend
npm run test -- src/views/People/FaceQualityReview.spec.ts
npm run typecheck
npm run build
```

预期：审核页可兼容旧证据并正确解释新证据。

## 任务 6：发布、定点验收与回滚

### 发布前

1. 完成所有上述测试，以及：

   ```bash
   cd backend && go test ./...
   cd ../ml-service && ./.venv/bin/python -m pytest -q
   cd ../frontend && npm run build
   git diff --check
   ```

2. 构建 ML 镜像后，确认 `/api/v1/health` 返回 `verifier_available=true`、`verifier_name=yunet`、`verifier_version=opencv-yunet-2023mar`。
3. 不启动 full/enforce。

### NAS 验收顺序

1. 部署修复后的 backend 与 `relive-ml`。
2. 创建 v3 的定点 `calibration + shadow`，只传 `face_ids=[538580]`。
3. 在审核页确认：该样本为“已匹配目标脸 / 确认为脸”，不再出现 `no_face`；Face 的 `person_id`、聚类状态、排除记录均未被定点 shadow 改动。
4. 再选择包含边缘脸、群像、正常脸的 50～100 个样本做 v3 shadow；抽查边缘脸与群像反例。
5. 仅当新 v3 校准为 `completed`、`retryable_count=0`、目标计数闭合且页面显示“可作为 v2 enforce 校准”时，才由操作者另行决定是否启动 v3 full/enforce。

### 回滚

- 若定点或小样本 shadow 仍有误判：不启动 enforce；保留审计证据，回退应用镜像即可，不能删除 v3 事件。
- 若 v3 full/enforce 已误自动隔离：使用既有“按运行恢复自动隔离”能力，只恢复该 v3 run 的自动动作；恢复后 Face 回 `pending`，不恢复旧人物归属。
- 不通过修改数据库分数、批量删除 `face_quality_events` 或直接恢复旧 `person_id` 回滚。

## 验收标准

- `Face #538580` 在新规则的定点 shadow 中为 `face`，并有非零目标匹配分与 `target_match_iou >= 0.3`。
- 边缘裁剪样本不会因裁剪被截断而假阴性。
- 群像中非目标脸不会因位于裁剪中心或分数更高而被误匹配。
- 新照片的主检测过线样本在独立验证未匹配时进入 `review_required`，不被静默丢弃。
- 当前 #5（`face_quality_v2`）不能作为新 `face_quality_v3` full/enforce 的校准依据。
- 人工结论、历史审计、照片与人物聚类行为均未被本修复重写。

