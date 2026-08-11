# 人脸级质检与审核队列任务说明

## 目标

在“AI 分析完成、非截屏照片才进入人物扫描”的基础上，为每个检测到的人脸框增加独立质检：

- 自动隔离高确定性的非人脸与极低质量脸；
- 灰区样本不进入聚合，进入审核队列；
- 人工可确认、改判和恢复；
- 人脸重新检测后，人工结论仍能正确生效；
- 不重构“人物列表”与“合并建议”页面，仅让它们使用更干净的人脸输入。

## 背景与现状

当前系统已支持在照片详情中把单张人脸标记为 `non_face` 或 `low_quality`，使其退出人物归属、身份画像和合并建议。排除记录以“照片 + 人脸框 IoU”在重新检测后匹配。

当前缺口：

1. 检测结果会直接进入聚类；`quality_score` 是清晰度启发分，不是“是否真实人脸”的判别器。
2. 现有排除记录创建时依赖 `source_face_id`；重新检测会删除并新建 `Face`，恢复流程必须改为依据“照片 + 当前匹配的人脸框/排除记录”处理，不能依赖过期的 Face ID。

## 范围

### 包含

- 自动人脸有效性与质量评估；
- 高确定性自动排除、灰区审核；
- 可追溯、可恢复的人工覆盖；
- 全局“人脸质检”审核页面；
- 新增照片的实时质检；
- 存量人脸的低并发后台审计；
- 对受影响人物的局部统计、头像、identity profile、合并建议刷新；
- 上线开关、审计指标与按规则版本恢复。

### 不包含

- 不重构人物列表、人物详情、合并建议的页面布局或交互；
- 不把侧边栏改造成多级“人物”菜单；仅在现有扁平侧边栏中，紧邻“人物管理”新增同级入口“人脸质检”；
- 不修改截图照片级排除逻辑；
- 不调整通用人物聚类阈值，不在本任务中打散全部路人并重聚类；
- 不处理“重复人脸合并”问题；
- 不删除原始照片、原始人脸裁剪或历史审计；
- 不把现有 `quality_score` 单独作为硬排除阈值。

## 设计

### 1. 判定模型与状态

每个检测框须独立得到两类结论：

| 维度 | 状态 | 含义 |
| --- | --- | --- |
| 人脸有效性 | `valid` / `non_face` / `uncertain` | 是否真实人脸 |
| 识别可用性 | `usable` / `low_quality` / `uncertain` | 是否适合参与身份聚合 |
| 最终动作 | `accept` / `exclude` / `review_required` | 是否可进入聚类 |

规则：

- 高确定性 `non_face`：自动 `exclude`，原因 `non_face`；
- 高确定性真实脸但极低质量：自动 `exclude`，原因 `low_quality`；
- 任一维度为灰区：`review_required`，不进入聚类；
- 合格样本：正常进入聚类；
- 图片读取、embedding 缺失/非法等技术问题：标记为可重试失败，不得伪装成 `non_face`。

统计语义：

- `non_face` 不计入 `photos.face_count`；
- `low_quality` 计入照片检测到的人脸数，但不计入人物 `face_count`，也不参与聚类、身份画像或合并建议；
- `review_required` 不参与聚类；照片详情须明确提示“待质检”，不能显示为普通“待识别”。

### 2. 质检证据

在现有 `ml-service` 检测结果中补充结构化质检证据。现有接口只返回框、检测置信度、质量分和 embedding；需扩展为至少包含：

- `face_validity_score`：二次人脸验证置信度；
- 人脸实际像素宽高；
- 清晰度、亮度/曝光、对比度；
- 关键点完整性与几何合理性；
- 姿态、遮挡或无法估计标记；
- `quality_reasons`：如 `too_small`、`blurred`、`overexposed`、`invalid_landmarks`；
- 质检模型版本与规则版本。

执行分层：

1. 基础硬校验：框合法性、最小像素尺寸、embedding 合法性；
2. 图像质量计算：清晰度、尺寸、曝光、关键点、姿态；
3. 二次人脸验证：只对候选框运行，避免把第一阶段检测置信度当作真实人脸证明；
4. 策略引擎：按版本化规则映射为自动排除、待审核或接受。

自动排除规则必须要求“高确定性”，且具备可解释原因；灰区不得自动归入任何人物。

### 3. 数据模型与恢复

保留现有 `faces` 与 `face_exclusions` 的运行态职责，并新增追加式审计表，例如 `face_quality_events`：

- `photo_id`、归一化人脸框、最近匹配 Face ID；
- 判定：`accepted`、`non_face`、`low_quality`、`review_required`；
- 来源：`auto`、`manual`；
- 规则版本、模型版本、证据 JSON、原因码；
- 审核动作、审核时间、恢复时间。

恢复或人工确认必须写入新的人工事件，不能简单删除历史。优先级：

1. 人工排除优先于自动接受；
2. 人工确认“真实且可参与识别”优先于自动排除；
3. 后续重新检测按照片和人脸框 IoU 回填当前结论；
4. 新模型/规则不得无声覆盖人工结论；
5. 恢复后只回到 `pending`，重新参与聚类，不恢复旧 `person_id`。

重新检测匹配到旧记录后，必须更新当前匹配 Face 引用；恢复时按当前匹配记录处理，保证“重扫后仍可恢复”。

### 4. 后端接口

保留兼容现有接口：

- `PATCH /api/v1/people/faces/exclusion`

新增接口：

- `GET /api/v1/people/face-quality/stats`
- `GET /api/v1/people/face-quality/reviews`
  - 支持 `state`、`reason`、`source`、`rule_version`、时间范围、分页；
- `PATCH /api/v1/people/faces/quality-decision`
  - 支持批量确认排除、改判原因、人工接受/恢复；
- `POST /api/v1/people/face-quality/restore-auto`
  - 按规则版本恢复自动排除的样本，仅用于回滚或阈值修正。

接口返回审核卡片所需的完整上下文：人脸裁剪、原图与框、原因、分数、来源、规则版本、当前状态和可用操作。

所有静态质检路由必须定义在 `GET /people/:id` 等动态路由之前，避免被人物 ID 路由吞掉。

### 5. 人脸处理链路

改造 `ApplyDetectionResult` 的共享入口，保证本地扫描与远程 worker 结果走同一质检逻辑：

```text
检测结果
→ 基础校验
→ 生成/读取人脸裁剪
→ Face 级质检
→ 匹配既有人工结论
→ 写入 Face、排除状态与审计事件
→ 仅 accept 样本进入聚类
→ 局部刷新人物画像与合并建议
```

要求：

- 自动 `non_face` / `low_quality` 写入现有排除态，且不会分配 `person_id`；
- `review_required` 同样不得进入聚类；
- 批量结果必须在单事务中写入 Face、质检状态与照片统计；
- 事务提交后才刷新人物状态、identity profile、原型缓存与合并建议；
- 任何质检失败应 fail-closed：不把未知样本交给聚类。

### 6. 审核界面

新增路由和页面：

- 路由：`/face-quality-review`；
- 页面：`frontend/src/views/People/FaceQualityReview.vue`；
- 侧边栏：作为“人物管理”后的同级菜单项“人脸质检”。

页面包含三个 Tab：

1. 待人工审核；
2. 自动隔离；
3. 已人工确认。

页面采用“虚拟化人脸卡片网格 + 详情抽屉”：

- 卡片展示人脸裁剪、原因、来源、时间、置信等级；
- 详情展示原图预览及框位置、各项证据、规则版本、操作历史；
- 操作包括确认排除、改为非人脸、改为低质量、恢复参与识别；
- 支持批量操作，但“恢复参与识别”必须二次确认；
- 支持按原因、来源、状态、规则版本、时间、扫描路径筛选。

照片详情页保留现有就地排除/恢复能力，并补充“在质检页查看”跳转；不改变人物列表和合并建议页面。

### 7. 存量审计与上线

新照片在质检功能启用后立即按规则处理。存量数据不得直接全库重聚类：

1. 先对存量 Face 低并发生成质检候选与审计，不改人物归属；
2. 抽样复核各原因的准确性；
3. 对高确定性规则开启自动排除；
4. 灰区只进入审核队列；
5. 按批次清理存量，并只重算受影响人物；
6. 人脸样本质量稳定后，再单独评估“路人打散重聚合”。

部署前备份 NAS 实际运行数据卷中的 SQLite 数据库。上线开关至少支持：

- `disabled`：停止新自动判定；
- `shadow`：仅产出候选；
- `enforce`：新样本执行自动排除。

已确认首期采用 `enforce` 处理高确定性新样本；存量建议先 `shadow`，再分批启用。

回滚顺序：

1. 切换为 `disabled`，立即停止新的自动排除；
2. 通过“按规则版本恢复自动排除”操作撤销该版本结论；
3. 如迁移或数据异常，使用部署前 SQLite 备份回滚；
4. 回滚不自动重建全库人物聚合。

## 预计修改文件

- `ml-service/app/schemas.py`
- `ml-service/app/models/face.py`
- `ml-service/tests/test_face_model.py`
- `ml-service/tests/test_face_router.py`
- `backend/internal/mlclient/client.go`
- `backend/internal/model/face.go`
- `backend/internal/model/dto.go`
- `backend/pkg/database/database.go`
- `backend/internal/repository/face_exclusion_repo.go`
- `backend/internal/repository/face_quality_repo.go`（新增）
- `backend/internal/service/face_quality_service.go`（新增）
- `backend/internal/service/face_exclusion.go`
- `backend/internal/service/people_service.go`
- `backend/internal/api/v1/handler/people_handler.go`
- `backend/internal/api/v1/router/router.go`
- `frontend/src/types/people.ts`
- `frontend/src/api/people.ts`
- `frontend/src/router/index.ts`
- `frontend/src/layouts/MainLayout.vue`
- `frontend/src/views/People/FaceQualityReview.vue`（新增）
- `frontend/src/views/Photos/Detail.vue`

## 验证与验收

后端和模型：

```bash
cd ml-service && pytest -q
cd backend && go test ./internal/model ./internal/repository ./internal/service ./internal/api/v1/handler ./pkg/database -count=1
cd backend && go test ./... -count=1
```

前端：

```bash
cd frontend && npm run test
cd frontend && npm run typecheck
cd frontend && npm run build
```

必须覆盖：

- 高确定性非人脸自动排除，且不计入照片人脸数；
- 极低质量真实脸自动排除，计入照片人脸数但不进入人物聚合；
- 灰区进入审核且不聚类；
- 人工排除、人工接受、改判和恢复；
- 重新检测后人工结论仍然生效，且可恢复；
- 自动结论不能覆盖人工接受；
- 排除后只局部更新相关人物、头像、画像和合并建议；
- 人物列表与合并建议页面无结构或交互回归；
- 截屏照片级排除链路保持不变；
- NAS 存量批处理可暂停、可继续、可按规则版本恢复。
