# 人脸质检审核页三项修复任务说明

## 背景与已确认事实

人脸质检审核页已上线，但当前存在三项独立且已确认的问题：

1. 详情抽屉的 `detail-photo` 使用 `photo_thumbnail` 的存储相对路径拼接为 `${apiBaseUrl}/../${path}`。该地址不经过 `GET /api/v1/photos/:id/thumbnail`，浏览器归一化后会访问错误路径，导致原照片预览无法正确显示。
2. 人脸卡片只支持逐张勾选。虽然现有 `PATCH /api/v1/people/faces/quality-decision` 已支持提交多个 `event_ids`，界面没有本页全选、反选或明确的清空选择操作。
3. NAS 已确认：历史人脸没有质检证据时，`faces.face_validity_score` 的数据库默认值为 `0`；这不是模型实际判为 0 分。审核页无条件渲染该数值，因而把“未采集”错误显示为“0% 有效”。

本任务只修复审核页的图片访问、批量选择和证据缺失展示，不改变质检策略、审核队列状态或人物聚合行为。

## 目标

- 详情抽屉可靠显示该审核项所属照片的受权限保护缩略图；缩略图不可用时给出可恢复的界面反馈。
- 在不新增批量写接口的前提下，让用户可高效选择当前页样本并复用既有批量决策。
- 精确区分“模型实际评分为 0”与“历史样本没有评分证据”，杜绝把后者展示为 `0%`。

## 已确定范围

### 1. 详情照片使用既有照片缩略图接口

修改 `frontend/src/views/People/FaceQualityReview.vue`：

- 删除 `photoThumbUrl(path: string) => ${apiBaseUrl}/../${path}` 的路径拼接。
- 新增按审核项 `photo_id` 构造的 helper：

  ```ts
  const photoThumbnail = (photoId: number, version: number) =>
    `${apiBaseUrl}/photos/${photoId}/thumbnail?v=${version}`
  ```

  `version` 使用 `event_id`。它只用于浏览器缓存隔离；后端继续忽略该查询参数，不新增路由或静态文件暴露规则。

- 抽屉照片预览只以 `current.photo_id` 调用该 helper，不能再读取 `current.photo_thumbnail` 或 `current.photo_file_path` 作为浏览器 URL。
- 将当前裸 `<img>` 替换为可处理加载失败的 Element Plus 图片容器，保持 `contain` 显示。加载失败时显示“照片缩略图不可用”，并提供到 `/photos/:photo_id` 的“查看照片详情”链接；不得静默留白。
- 后端继续使用已有的 `GET /api/v1/photos/:id/thumbnail`。该接口负责照片授权、HEIC 回退、被动缩略图生成和缓存；本任务不绕过它读取 NAS 文件路径。
- `FaceQualityReviewItem` 现有 `photo_thumbnail`、`photo_file_path` 可暂时保留在响应中以保证接口兼容，但审核页不得继续依赖它们；本任务不删除字段。

### 2. 当前页全选、反选与清空选择

修改 `frontend/src/views/People/FaceQualityReview.vue`，保留现有 `selectedIds: Set<number>` 与 `peopleApi.applyFaceQualityDecision(eventIds, action)`：

- “当前页”严格等于当前 `items` 中服务端返回的审核事件（当前固定 `page_size = 24`），不包含未加载或其他筛选条件下的记录。
- 在批量工具栏增加：
  - 全选本页（含本页数量）；
  - 反选本页；
  - 清空选择；
  - 已选数量，文案明确为“跨页累计”。
- 全选本页控件必须支持三态：
  - 本页全未选：未选；
  - 本页部分选中：`indeterminate`；
  - 本页全部选中：选中。
- “全选本页”只把当前 `items` 的 `event_id` 加入集合；再次取消时只删除当前页 ID，不能清除其他页已选项目。
- “反选本页”只反转当前 `items` 的选择状态，同样保留其他页选择。
- 翻页时保留 `selectedIds`，使用户可以跨多个已浏览页面累积选择；批量提交仍将 `Array.from(selectedIds)` 原样传给既有 API。
- 切换 Tab、任一筛选条件、时间范围或点击刷新时，清空选择。批量操作成功后也清空选择并刷新统计与当前列表。
- 所有集合修改统一复制为新 `Set` 后再赋回 `selectedIds.value`，不要直接在模板事件中调用 `.clear()`，以保证 Vue 响应式状态和三态复选框同步。

不做“全选全部筛选结果”。这需要后端以筛选快照定义批量目标，并处理结果在选择期间变化的问题；现有 `event_ids` 契约不具备该语义。

### 3. 质检证据可用性作为显式响应语义

修改 `backend/internal/model/dto.go`、`backend/internal/service/face_quality_service.go` 和 `frontend/src/types/people.ts`：

- 在 `model.FaceQualityReviewItem` 和前端同名接口新增：

  ```text
  quality_evidence_available: boolean
  ```

- 该字段由 `buildReviewItem` 根据当前 `FaceQualityEvent.EvidenceJSON` 明确计算：仅当该字段非空且能反序列化为 `model.FaceQualityEvidence` 时为 `true`。不得依据 `face_validity_score`、`quality_score`、`rule_version`、事件来源或判定状态推断。
- 这样，模型真实输出 `face_validity_score = 0` 时，其完整证据 JSON 仍令字段为 `true`；历史回填时没有证据 JSON 的记录则为 `false`。不需要新增数据库列、修改既有审计记录或伪造历史分数。
- `buildReviewItem` 仍按当前逻辑填充数值字段，供兼容旧客户端；新前端必须只在 `quality_evidence_available === true` 时显示有效性和质量分数。
- 为避免前端先于后端部署时把 `undefined` 误当真，新前端必须将缺失字段视为 `false`。

修改 `frontend/src/views/People/FaceQualityReview.vue`：

- 卡片底部：
  - `quality_evidence_available === true`：保留 `NN% 有效`；
  - 否则：显示 `有效性未采集`，不得出现百分比。
- 详情抽屉：
  - 证据可用：显示“有效性”和“质量分”百分比，保留原因码和证据 JSON；
  - 证据不可用：显示“有效性：未采集（历史人脸）”“质量分：未采集”，隐藏空的原因码与证据 JSON。
- 不提供“将 0 修正为其他数值”的编辑入口，不触发重新检测，不改变 `review_required`、`excluded`、`pending`、`person_id` 或照片人脸计数。

## 接口契约

仅扩展既有读取接口：

```text
GET /api/v1/people/face-quality/reviews
```

每个 `items[]` 新增：

```json
{
  "quality_evidence_available": true
}
```

该字段对旧客户端是可忽略的向后兼容扩展；不修改请求参数、分页、筛选和以下写接口的契约：

```text
PATCH /api/v1/people/faces/quality-decision
GET   /api/v1/photos/:id/thumbnail
```

## 实施文件与职责

| 文件 | 修改内容 |
| --- | --- |
| `backend/internal/model/dto.go` | 为 `FaceQualityReviewItem` 增加 `QualityEvidenceAvailable bool` 及 JSON 字段。 |
| `backend/internal/service/face_quality_service.go` | 在 `buildReviewItem` 中安全解析 `FaceQualityEvent.EvidenceJSON`，填充可用性标记；解析失败视为不可用且不影响列表请求。 |
| `backend/internal/service/face_quality_service_test.go` | 覆盖无证据、有效证据但真实 0 分、非法 JSON 三种读取语义。 |
| `frontend/src/types/people.ts` | 为 `FaceQualityReviewItem` 增加 `quality_evidence_available: boolean`。 |
| `frontend/src/views/People/FaceQualityReview.vue` | 修复详情照片 URL 与失败态；增加本页选择工具；按证据可用性显示分数。 |
| `frontend/src/views/People/FaceQualityReview.spec.ts`（新增） | 使用 mock API 和 Element Plus 桩覆盖本任务的前端交互与展示。 |

不修改 `backend/internal/api/v1/handler/people_handler.go`、`backend/internal/api/v1/router/router.go`、数据库迁移、`face_quality_events` 表结构或 ML 服务。

## 验证

### 后端

在 `backend/internal/service/face_quality_service_test.go` 增加表驱动或等效覆盖：

1. 历史 `Face` 的两个分数字段均为默认 `0`，且当前事件 `evidence_json` 为空：读取响应中 `quality_evidence_available=false`；数值为零不改变该结论。
2. 有可解析 `FaceQualityEvidence`，其中 `face_validity_score=0`：读取响应中 `quality_evidence_available=true`，证明真实零分不会被隐藏。
3. `evidence_json` 非空但 JSON 无法解析：接口成功返回，`quality_evidence_available=false`；不能因为单条历史坏数据使整页 500。

运行：

```bash
cd backend
go test ./internal/service -run 'TestFaceQuality' -count=1
go test ./internal/service -count=1
```

### 前端

在新增 `FaceQualityReview.spec.ts` 覆盖：

1. 抽屉详情图片 URL 为 `/api/v1/photos/<photo_id>/thumbnail?v=<event_id>`，不包含 `../`、`photo_thumbnail` 或本地文件路径。
2. 图片加载失败时出现“照片缩略图不可用”和照片详情链接。
3. 无证据样本显示“有效性未采集”和“未采集（历史人脸）”，不显示 `0%`；可用证据且真实 0 分的样本显示 `0%`。
4. 全选本页选择全部 `items`；部分取消后全选框为半选；反选只反转本页；清空选择清除全部已选。
5. 翻页后保留上一页选择；Tab/筛选/刷新及成功批量动作清空选择；提交给 `applyFaceQualityDecision` 的 ID 包含跨页累计结果且无重复。

运行：

```bash
cd frontend
npm run test -- src/views/People/FaceQualityReview.spec.ts
npm run typecheck
npm run build
```

## 验收标准

1. 任意审核详情都通过受保护的 `/photos/:id/thumbnail` 展示照片；原有相对路径不再出现在浏览器图片请求中。
2. 照片缩略图请求失败时用户能看到失败原因和“查看照片详情”入口，不留空白区域。
3. 用户可在当前页全选、反选、清空，并可跨已浏览页面累计后执行现有批量决策；Tab、筛选、刷新和成功决策后不会残留失效选择。
4. NAS 中已确认的历史无证据样本不再显示为 `0% 有效`；真实模型返回 0 分的有证据样本仍准确显示 `0%`。
5. 不新增批量写接口、不改质检判定、不改变任何人脸的审核状态或人物归属，也不触发重新检测或全库重聚类。

## 上线与回滚

- 先部署后端读取字段扩展，再部署前端；即使前端先部署，缺失字段也按“未采集”显示，不会误显示 0 分。
- 没有数据库迁移和数据回填，不需要停服务或备份恢复。
- 如需回滚，回退前端即可恢复原界面；回退后端不会影响已有审计记录或批量决策接口。

## 不包含

- 不处理“开始时间 / 结束时间”筛选文案或时间语义；
- 不改变历史无证据人脸进入 `review_required` 的 fail-closed 策略；
- 不对历史照片重新跑模型以补齐真实质检证据；
- 不实现跨所有筛选结果的全选、服务端选择快照或异步批量任务；
- 不修改 People 列表、人物详情、合并建议、截图排除、聚类阈值或质检规则；
- 不删除 `photo_thumbnail`、`photo_file_path` 等现有响应字段，也不开放任何 NAS 文件系统路径。
