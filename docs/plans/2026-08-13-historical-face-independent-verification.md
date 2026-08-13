# 历史人脸独立复核与自动隔离任务说明

## 背景

现有“历史补证据”运行不能提供可信的人脸判断依据：`score-known-faces` 在已旋转的展示缩略图上再次运行同一套 InsightFace 检测，并复用检测结果的关键点、embedding 和 `det_score` 计算 `face_validity_score`。这不是独立的人脸真实性验证；当手、植物等被主检测器误检为脸时，误检自身产生的关键点和 embedding 仍会把“有效性”抬高。

此外，历史重评分在缩略图缺失时通过 `ImageProcessor.ProcessForAI(1024, 85)` 压缩原图。生产样本 face `538560` 的原图为 4032 × 3024，原始框约为 55 × 63 px；重评分证据却显示为 14 × 16 px、`too_small`、`face_validity_score=0.826811`。该组指标既不能说明它是脸，也不能说明原图中的真实可用性。

当前 v1 运行 #2 已以 `calibration + shadow` 结束，但状态为 `completed_with_errors`（4733 个目标中 4731 个已处理、907 个待审核、2 个技术失败），因此既没有自动隔离，也不满足当前 full/enforce 门禁。更重要的是，v1 只从 `historical_backfill + missing` 事件重新选目标；成功补证据后事件转为 `historical_rescore + available`，后续 full 运行无法复用同一批目标。

本任务以 v2 独立复核链路替代 v1 的同源启发式证据。v1 审计记录保留以供追溯，但不得再作为 v2 自动隔离或人工判断的依据。

## 目标

1. 对所有**没有当前人工最终结论**的历史 `Face` 创建固定快照，使用独立验证器和原图裁剪重新分流。
2. 高确定性 `non_face` 与高确定性 `low_quality` 自动隔离且可按运行恢复；灰区和技术失败进入不同的人工/重试队列。
3. 审核页展示可解释、可复核的原始证据，停止把 v1 “有效性”“质量分”当作是否为脸的结论。
4. 新照片检测同步采用更严格的准入，避免历史清理完成后继续写入低置信度误检。

## 已确定的产品规则

### 1. 分流语义

| 结果 | 必要证据 | 自动动作 | 人物与照片语义 |
| --- | --- | --- | --- |
| `non_face` | 独立验证器明确未检测到脸，且原主检测分低于准入线；不存在技术错误或极小框不确定性 | 自动隔离，可恢复 | 不计入照片 `face_count`；退出人物、画像和合并建议 |
| `low_quality` | 独立验证器确认是脸，但原图人脸框极小、极模糊、严重遮挡或严重曝光异常 | 自动隔离，可恢复 | 仍计入照片 `face_count`；不参与人物聚类、画像和合并建议 |
| `review_required` | 主/独立模型分歧、框太小而验证器无法可靠判断、已有自动结论冲突 | 不自动隔离 | 不改变当前人物归属，交人工审核 |
| `accepted` | 独立验证器确认是脸且原图质量达标 | 保留 | 保持当前人物归属和聚类状态 |
| `technical_error` / `unmatched` | 读图、EXIF/旋转、裁剪、模型或协议失败；或无法建立可靠匹配 | 不自动隔离 | 进入待重试队列；不得伪装为任何质检结论 |

人工结论优先于任意 v1/v2 自动结论。v2 不得自动恢复已被自动隔离的 Face；若 v2 与已有自动结论冲突，写 `review_required`，由人工决定是否恢复。

### 2. 原图与坐标规则

1. 存量 BBox 仍视为归一化坐标；其基准图像必须是“自动 EXIF 方向校正后，再叠加 `photos.manual_rotation`”的原图。
2. 后端在该方向一致的原图中计算人脸实际宽/高，原图质量指标只在未缩放的人脸框内计算。
3. 给独立验证器的输入为以人脸框为中心、四周各扩展 100% 的上下文裁剪，超出边界时裁切。验证器可将**副本**缩放到模型输入尺寸，但不得用缩放后的尺寸覆盖原图证据。
4. 每条 v2 证据必须保存：校正后原图宽/高、人脸框实际宽/高、上下文裁剪宽/高、扩展比例、主检测置信度及原图质量指标。缩略图尺寸不得替代这些字段。

### 3. 独立验证器与阈值

首期使用 OpenCV YuNet ONNX 作为独立验证器：它与当前 InsightFace `buffalo_sc` 主检测链路不同。模型文件作为镜像内受版本和 SHA-256 保护的资产交付，不允许容器在运行时下载模型。

新增 `FaceVerifier` 抽象，v2 只依赖其结构化结果，不依赖 YuNet 的内部实现。返回状态为 `face`、`no_face`、`uncertain`、`error`，并包含验证器名称、版本和置信度。

初始规则必须配置化、受 `rule_version=face_quality_v2` 版本管理，并在校准样本上定标后才可调整：

- `people.face_detection_min_confidence` 初始值为 `0.65`；低于此值的新检测不持久化为 `Face`。
- `people.face_quality_v2_min_original_short_edge` 初始值为 `48` px。低于此值的历史样本，独立验证器未确认是脸时只能进入 `review_required`，不能自动判 `non_face`。
- 仅当 `primary_detector_score < 0.65`、独立验证状态为 `no_face`、原图短边不少于 48 px，且无技术错误时，才自动 `non_face`。
- 仅当独立验证状态为 `face` 时，才可按原图短边、经统一尺寸归一化后的清晰度、遮挡和曝光规则自动判 `low_quality`。
- 初始模糊/遮挡/曝光阈值由带标签的 shadow 校准结果写入配置；不得沿用 v1 的 `quality_score`、`face_validity_score` 或压缩图 `too_small` 阈值。

上述条件优先保证 `non_face` 的精度。若任一证据不足，宁可进入 `review_required`，不得错误自动隔离真实脸。

## 实施范围

### A. v2 审计数据与历史运行快照

**涉及文件：**

- `backend/internal/model/face.go`
- `backend/internal/model/face_quality_rescore.go`
- `backend/internal/model/dto.go`
- `backend/internal/repository/face_quality_repo.go`
- `backend/internal/repository/face_quality_rescore_repo.go`
- `backend/pkg/database/database.go`
- `backend/pkg/database/database_test.go`

1. 在 `FaceQualityEvent` 增加 `evidence_pipeline`（`legacy_v1` / `independent_v2`）。现有行迁移为 `legacy_v1`；新实时检测和历史复核必须显式填写，不允许留空。
2. `EvidenceJSON` 使用新的 `FaceQualityEvidenceV2` 结构；保留旧 JSON 兼容读取，但 v2 审核接口不得把旧字段映射成 v2 结论。
3. 在 `FaceQualityRescoreRun` 增加 `pipeline_version` 和 `target_scope`。本任务创建的运行固定为 `pipeline_version=independent_v2`、`target_scope=all_non_manual_faces_without_independent_v2`。
4. 新增 v2 选目标查询：以 `faces.id` 为主体，选择没有当前 `source=manual` 事件、且没有当前 `evidence_pipeline=independent_v2` 事件的 Face。不得只扫描 `historical_backfill + missing`。
5. 已自动隔离而没有人工最终结论的 Face 也进入 v2 快照，但 v2 结果不得自动恢复其状态；若判定为 `accepted` 或与旧自动理由冲突，产出 `review_required` 和 `auto_decision_conflict` 原因码。
6. `face_quality_rescore_items` 保持 `(run_id, face_id)` 的精确快照语义。full/enforce 必须从指定 calibration run 的已处理 item 复制 Face ID、原始 BBox 和 v2 基线事件 ID，禁止重新按“当前事件状态”选目标。

### B. 原图裁剪与 v2 ML 协议

**涉及文件：**

- `backend/internal/util/image.go`
- `backend/internal/util/image_test.go`
- `backend/internal/service/face_quality_rescore.go`
- 新增 `backend/internal/service/face_quality_rescore_image.go`
- `backend/internal/mlclient/client.go`
- `backend/internal/mlclient/types.go`
- `ml-service/app/schemas.py`
- `ml-service/app/routers/faces.py`
- 新增 `ml-service/app/models/face_verifier.py`
- `ml-service/Dockerfile`
- 新增 `ml-service/assets/yunet/face_detection_yunet_2023mar.onnx`
- 新增 `ml-service/assets/yunet/SHA256SUMS`
- `ml-service/tests/test_face_router.py`
- 新增 `ml-service/tests/test_face_verifier.py`

1. 新增 Go 图像辅助函数：通过 `util.OpenImage`、现有 EXIF 方向校正和 `util.ApplyManualRotation` 生成方向一致的原图；按归一化 BBox 提取人脸框和带上下文的裁剪。它不得调用 `ProcessForAI(1024, 85)`。
2. 新增内部 ML 接口 `POST /api/v1/verify-known-face-crops`。请求按 Face 传输上下文裁剪 Base64、`face_id`、原图人脸框宽高和主检测分；浏览器不可访问该接口。
3. ML 端新增 `FaceVerifier` 协议与 YuNet 实现。YuNet 在裁剪副本中检测到足够置信且位于目标中心区域的脸时返回 `face`；成功推理但无可靠脸返回 `no_face`；输入短边不足模型最小可判尺寸或结果边界时返回 `uncertain`；加载/推理/解码异常返回 `error`。
4. ML 同时计算原图人脸框的质量特征：实际宽高、统一到固定短边后的 Laplacian 清晰度、亮度、对比度、遮挡/几何可用性。所有分数都必须在响应中标明计算域和版本。
5. 响应按 `face_id` 一一对应，包含 `verification_status`、`verifier_score`、`verifier_name`、`verifier_version`、原图尺寸、质量特征、原因码和 `evidence_schema_version=independent_v2`。任何单条错误只影响对应 item。
6. Docker 构建时复制 YuNet 资产并校验 `SHA256SUMS`；模型缺失或校验失败应使 ML health 降级并让历史 worker 写 `technical_error`，不可静默退回 v1 同源评分。

### C. v2 决策、队列、恢复与运行门禁

**涉及文件：**

- `backend/internal/service/face_quality_service.go`
- `backend/internal/service/face_quality_rescore.go`
- `backend/internal/service/face_quality_backfill.go`
- `backend/internal/api/v1/handler/people_handler.go`
- `backend/internal/api/v1/router/router.go`
- `backend/internal/repository/face_quality_rescore_repo.go`
- `backend/internal/service/face_quality_service_test.go`
- `backend/internal/service/face_quality_rescore_test.go`
- `backend/internal/api/v1/handler/people_rescore_handler_test.go`

1. 将现有 `evaluateFaceQuality` 明确限制为 `legacy_v1`。新增 `evaluateFaceQualityV2(evidence)`，只接受 `FaceQualityEvidenceV2`；不得读取或折算旧 `FaceValidityScore`、`QualityScore`。
2. v2 shadow 校准只写新证据和影子决策：对可能自动隔离的样本写 `review_required`，同时在 evidence 中保存建议决策；绝不改 `person_id`、`cluster_status`、`face_exclusions` 或照片统计。
3. v2 full/enforce 必须引用一个 `pipeline_version=independent_v2`、状态 `completed`、`retryable_count=0`、无 pending/processing、且 `processed_face_count + superseded_manual_count = target_face_count` 的校准 run。服务端逐项校验，前端不得自行推算资格。
4. full/enforce 仅对该 calibration 的精确 item 快照运行。`non_face` 与 `low_quality` 复用既有排除事务、`syncPersonState`、`RecomputeTopPersonCategory`、身份画像失效和 merge suggestion 脏标记；不得调用 `ApplyDetectionResult`、重建 embedding 或全库聚类。
5. `technical_error`、`unmatched` 和 `uncertain` 分别写明证据状态及原因，不失活最后一条可用人工结论。它们只能进入“待重试/待人工审核”，不能进入自动隔离。
6. 保留现有 run 级 `restore-auto`，但它只能恢复 `pipeline_version=independent_v2 AND rescore_run_id=:id` 的自动事件。恢复后 Face 进入 `pending`，不自动回到旧 `person_id`。

### D. 新照片严格准入

**涉及文件：**

- `backend/pkg/config/config.go`
- `backend/pkg/config/config_test.go`
- `backend/config.dev.yaml`
- `backend/internal/service/people_service.go`
- `backend/internal/service/people_service_test.go`
- `backend/cmd/relive-people-worker/internal/worker/api_worker.go`
- 对应 people-worker 测试文件
- `ml-service/app/models/face.py`
- `ml-service/tests/test_face_model.py`

1. 在 `PeopleConfig` 增加 v2 阈值字段及严格范围校验；生产配置须显式设定，不向浏览器暴露可写配置入口。
2. 将本地 `detectFacesLocally` 与远程 people-worker 的固定 `min_confidence: 0.5` 统一改为读取 `people.face_detection_min_confidence`，初始值 `0.65`。
3. 主检测过线的候选在持久化前执行独立验证；`no_face` 不创建 `Face`、不生成缩略图、不写 embedding、不进入聚类。主检测或独立验证不确定时，按 v2 规则写 `review_required`，而不是当作正常人脸进入聚类。
4. 不要求为了准入而重构现有 InsightFace 推理内部的 embedding 计算；约束是不得把未通过的候选持久化或用于人物识别。

### E. 审核页与可解释性

**涉及文件：**

- `backend/internal/model/dto.go`
- `backend/internal/repository/face_quality_repo.go`
- `backend/internal/service/face_quality_service.go`
- `backend/internal/api/v1/handler/people_handler.go`
- `frontend/src/types/people.ts`
- `frontend/src/api/people.ts`
- `frontend/src/views/People/FaceQualityReview.vue`
- `frontend/src/views/People/FaceQualityReview.spec.ts`

1. `GET /api/v1/people/face-quality/reviews` 的 item 增加 `evidence_pipeline` 和 v2 的结构化展示字段。对 `legacy_v1` 记录显示“旧版同源指标，仅供历史追溯”，不显示为可据以人工判断的有效性/质量分。
2. 对 `independent_v2` 记录展示四组证据：主检测分、独立复核结果/分数与模型版本、原图人脸框/上下文裁剪尺寸、清晰度/亮度/遮挡原因码。不得把它们再次压成单一“有效性”百分比。
3. 保留现有五个队列，但拆分理由必须明确：`pending_review` 只展示模型分歧或不确定证据；`rescore_retryable` 只展示技术失败；`auto_excluded` 标明 v2 run ID、自动理由和恢复入口。
4. 运行卡片显示 pipeline、目标快照范围、完整性、shadow 建议分流、enforce 自动隔离数、失败数和校准资格。v1 历史运行仍可查看，但明确标注“不可作为 v2 enforce 校准”。

## 测试与验收

### 自动化测试

1. Go 图像测试覆盖 EXIF + 手动旋转、横竖图、边缘框裁剪、原图尺寸记录，验证不会调用 1024px 的 `ProcessForAI`。
2. Python 测试覆盖 YuNet `face` / `no_face` / `uncertain` / `error` 四种返回，以及模型 SHA 校验失败时不得回退到 v1。
3. 策略测试覆盖：低分主检测 + `no_face` 自动 `non_face`；已确认脸 + 极模糊/极小原图框自动 `low_quality`；高分主检测 + `no_face`、极小未确认框、任一模型错误均为 `review_required` 或技术失败。
4. 运行测试覆盖全历史 v2 选集、跳过当前人工结论、包含旧自动结论、calibration → 同快照 full/enforce、v1 run 不可作为 v2 门禁、人工并发覆盖、run 级恢复和局部人物状态刷新。
5. 新照片入口测试覆盖本地与远程 worker 同时采用 0.65 配置阈值，且独立验证失败的候选不会落库或聚类。
6. 前端测试覆盖 legacy/v2 证据文案、四组 v2 证据显示、技术失败与人工审核分流、run 资格禁用和恢复入口。

### NAS 验收顺序

1. 在测试环境跑带标签样本的 v2 shadow，先校准 YuNet 与质量阈值；Git 中不得加入真实私人照片或人脸裁剪。
2. NAS 先以受限快照运行 v2 shadow，人工抽样至少 300 条 `non_face` 建议和 300 条 `low_quality` 建议；发现任一明显真实脸被建议为 `non_face` 时停止 enforce，回到阈值/模型诊断。
3. 受限 shadow 无技术失败、抽样通过后，运行全历史 v2 shadow；确认 run 计数闭合且无 retryable item。
4. 由操作者显式创建引用该 calibration run 的 full/enforce；系统不得自行发起。
5. 验证 face `538560` 的详情显示原图框尺寸而非 14 × 16 的压缩尺寸，并显示独立复核结果；它不得因遗留 v1 的 82.7% 有效性而被 `accepted`。
6. 对一条 v2 自动 `non_face` 和一条 v2 自动 `low_quality` 分别验证：人物样本数、照片 `face_count`、画像/合并建议局部刷新正确；按 run 恢复后样本回到 pending 且不恢复旧人物归属。

## 不包含

- 不删除照片、原始文件、Face 审计历史或 embedding。
- 不调用 `ApplyDetectionResult` 强制重检，不删除重建 Face，不触发全库重新聚类或重建所有人物画像。
- 不用 v1 `face_validity_score`、`quality_score`、`too_small` 直接自动隔离历史样本。
- 不自动恢复旧自动隔离样本，也不覆盖人工接受、人工排除或人工恢复。
- 不把 YuNet 模型或阈值做成浏览器可随意修改的运行时开关；阈值调整必须变更规则版本并重新完成 shadow 校准。
- 不改造 People 列表、人物详情、合并建议页面的布局；仅修改既有人脸质检审核页及其 API 数据展示。

## 交付边界

本任务书只定义实现契约，未修改产品代码、模型资产或 NAS 数据。v1 文档 `docs/plans/2026-08-12-face-quality-historical-evidence-and-review-experience.md` 保留为已实施方案的历史记录；本任务完成后，v2 的 `independent_v2` 运行是历史自动隔离唯一允许使用的校准来源。
