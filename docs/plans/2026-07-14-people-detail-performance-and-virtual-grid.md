# 人物详情页性能与虚拟网格修复实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 修复人物详情页照片/人脸卡片覆盖、虚拟化失效、自动连续翻页和大人物首屏慢，并让扫描后自动事件聚类在 NAS 高负载或前台读请求期间主动让路。

**Architecture:** 前端把 `VirtualMediaGrid` 改为基于 window 的行级虚拟化，用真实 DOM 测量修正行高，并用可见区状态驱动单请求无限加载。后端为人物照片/人脸增加向后兼容的 cursor 分页，取消新模式下的每页 COUNT；事件增量聚类接入现有 `BackgroundTaskCoordinator`，按小批次执行并保留 pending 重试语义。

**Tech Stack:** Vue 3、TypeScript、`@tanstack/vue-virtual`、Vitest、真实浏览器测试、Go、Gin、GORM、SQLite、现有 `BackgroundTaskCoordinator`。

---

## 实施边界

本计划只处理以下范围：

- `frontend/src/components/VirtualMediaGrid.vue`
- `frontend/src/views/People/Detail.vue`
- `frontend/src/api/people.ts`
- 人物照片/人脸只读分页 handler、repository、DTO、索引
- 扫描后自动事件增量聚类的准入、分批、暂停和恢复
- 相关单元测试、浏览器布局测试、日志和 NAS 验收

保持不变：

- 页面继续使用整个浏览器自然滚动。
- 照片排序仍为 `taken_at DESC, id DESC`。
- 人脸排序仍为 `quality_score DESC, id ASC`。
- 旧 `page/page_size` API 保持兼容。
- 人物识别、人物聚类、事件聚类算法与最终业务结果不变。
- 当前工作区正在进行的 split `source_person_id` / `409 SPLIT_ASSIGNMENT_CONFLICT` 改动不属于本计划，不得覆盖、回退或混入本任务提交。

## 阶段一：前端止血与布局正确性

### Task 1：补齐虚拟网格和分页回归测试基础

**Files:**

- Modify: `frontend/package.json`
- Modify: `frontend/package-lock.json`
- Create: `frontend/vitest.browser.config.ts`
- Create: `frontend/src/components/VirtualMediaGrid.browser.spec.ts`
- Modify: `frontend/src/components/VirtualMediaGrid.spec.ts`
- Modify: `frontend/src/views/People/Detail.spec.ts`

**Step 1: 写会失败的请求次数测试**

在 `Detail.spec.ts` 增加以下场景：

- 首次进入只调用一次 `getPhotos`，等待多个 tick 后仍不请求第二页。
- `VirtualMediaGrid` 连续两次发出接近末尾事件时，in-flight 请求只调用一次。
- 照片请求失败后，数据变化和滚动事件都不能自动重试。
- 人脸 Tab 隐藏时不能请求人脸；首次切换后只请求第一页。
- route person ID 改变后，旧请求晚到不能追加到新人物。

不要继续把 `VirtualMediaGrid` 简单 stub 为 `true`。改用可显式触发 `visible-range-change` 的测试 stub，确保 Detail 的分页守卫被真实覆盖。

**Step 2: 写会失败的真实布局测试**

为 Vitest Browser 配置 Chromium provider。测试渲染真实 `VirtualMediaGrid` 和照片/人脸卡片，在 `1440/1024/768/390` 宽度下覆盖三种密度，并断言：

```ts
expect(nextRow.top).toBeGreaterThanOrEqual(previousRow.bottom)
expect(avatarButtonRect.bottom).toBeLessThanOrEqual(faceCardRect.bottom)
expect(photoMetaRect.bottom).toBeLessThanOrEqual(photoCardRect.bottom)
```

同时加载 2000 个测试项，断言 DOM 中 `.photo-card` / `.face-card` 数量显著小于总项数。

**Step 3: 运行测试并确认失败**

```bash
cd frontend
npm test -- --run src/components/VirtualMediaGrid.spec.ts src/views/People/Detail.spec.ts
npm run test:browser -- --run src/components/VirtualMediaGrid.browser.spec.ts
```

Expected:

- 请求次数测试因当前 `items.length -> maybeLoadMore()` 链路失败。
- 浏览器测试因固定行高和内部滚动容器模型失败。

**Step 4: Commit**

```bash
git add frontend/package.json frontend/package-lock.json frontend/vitest.browser.config.ts frontend/src/components/VirtualMediaGrid.browser.spec.ts frontend/src/components/VirtualMediaGrid.spec.ts frontend/src/views/People/Detail.spec.ts
git commit -m "test(people): reproduce detail virtual grid regressions"
```

### Task 2：把 VirtualMediaGrid 改为 window virtualizer 和真实行高测量

**Files:**

- Modify: `frontend/src/components/VirtualMediaGrid.vue`
- Modify: `frontend/src/components/VirtualMediaGrid.spec.ts`
- Test: `frontend/src/components/VirtualMediaGrid.browser.spec.ts`

**Step 1: 替换滚动模型**

- 使用 `useWindowVirtualizer`，删除组件内部 `overflow-y: auto` 和内部滚动事件依赖。
- 外层容器只负责占据正常文档流位置。
- 为每个虚拟行设置稳定的 `data-index`，并把元素传给 `virtualizer.measureElement`。
- 使用网格在页面中的实际 `offsetTop` 作为 window virtualizer 的 `scrollMargin`。
- 行 transform 必须扣除 `scrollMargin`，避免页面头部高度被重复计算。

目标结构：

```vue
<div ref="gridRef" class="virtual-media-grid">
  <div :style="{ height: `${totalHeight}px`, position: 'relative' }">
    <div
      v-for="row in virtualRows"
      :key="row.key"
      :data-index="row.index"
      :ref="measureRow"
      :style="rowStyle(row)"
    >
      ...
    </div>
  </div>
</div>
```

**Step 2: 改为真实测量优先**

- `estimateSize` 只提供首次估算。
- 不再把 `rowHeight + gap` 和 virtualizer `gap` 重复计算。
- `ResizeObserver`、密度变化和列数变化后调用 `measure()`。
- 照片与人脸分别传入估算值，但最终定位必须来自真实行测量。

**Step 3: 改造组件输出事件**

组件不再直接决定是否加载。统一输出：

```ts
emit('visible-range-change', {
  firstRowIndex,
  lastRowIndex,
  rowCount,
})
```

父组件负责结合 active Tab、loading、error、hasMore 判断是否加载。删除 `watch(items.length) -> maybeLoadMore()` 的隐式翻页责任。

**Step 4: 运行测试**

```bash
cd frontend
npm test -- --run src/components/VirtualMediaGrid.spec.ts
npm run test:browser -- --run src/components/VirtualMediaGrid.browser.spec.ts
```

Expected: PASS。

**Step 5: Commit**

```bash
git add frontend/src/components/VirtualMediaGrid.vue frontend/src/components/VirtualMediaGrid.spec.ts frontend/src/components/VirtualMediaGrid.browser.spec.ts
git commit -m "fix(people): virtualize detail grids against window scroll"
```

### Task 3：收紧人物详情页的无限加载状态机

**Files:**

- Modify: `frontend/src/views/People/Detail.vue`
- Modify: `frontend/src/views/People/Detail.spec.ts`
- Modify: `frontend/src/views/People/peopleGridUtils.ts`
- Modify: `frontend/src/views/People/peopleGridUtils.spec.ts`

**Step 1: 写纯逻辑判定**

把加载判定收敛为一个纯函数，输入至少包含：

```ts
{
  active: boolean
  loading: boolean
  error: boolean
  hasMore: boolean
  rowCount: number
  lastVisibleRowIndex: number
  thresholdRows: number
}
```

只有 active、非 loading、非 error、hasMore 且接近末尾时返回 true。

**Step 2: 为照片和人脸分别维护单一 in-flight**

- 保留 `photosLoading` / `facesLoading` 作为唯一请求锁。
- 记录请求发起时的 `personId` 和 cursor；响应回来后再次核对当前 route。
- 后续页失败保留原 cursor，并设置 error；只有手动“重试”清除 error 并重发。
- `items.length` 变化不能直接触发加载。
- active Tab 变化后只恢复测量和滚动锚点，不主动把隐藏 Tab 拉到底。

**Step 3: 修正 Tab 页面滚动锚点**

保存的是 window scroll 与首个可见 item 的组合锚点，而不是内部 `scrollRef.scrollTop`。密度变化和 Tab 切换后，以 item index 恢复到对应虚拟行。

**Step 4: 运行测试**

```bash
cd frontend
npm test -- --run src/views/People/peopleGridUtils.spec.ts src/views/People/Detail.spec.ts
npm run typecheck
npm run build
```

Expected: PASS。

**Step 5: Commit**

```bash
git add frontend/src/views/People/Detail.vue frontend/src/views/People/Detail.spec.ts frontend/src/views/People/peopleGridUtils.ts frontend/src/views/People/peopleGridUtils.spec.ts
git commit -m "fix(people): guard detail infinite loading by visible range"
```

## 阶段二：人物媒体轻量游标分页

### Task 4：定义 cursor 分页协议和兼容性测试

**Files:**

- Modify: `backend/internal/model/dto.go`
- Create: `backend/internal/api/v1/handler/people_media_cursor.go`
- Create: `backend/internal/api/v1/handler/people_media_cursor_test.go`
- Modify: `backend/internal/api/v1/handler/people_handler_test.go`
- Modify: `frontend/src/types/api.ts`

**Step 1: 定义响应类型**

Go 和 TypeScript 都新增 cursor 响应：

```ts
export interface CursorPagedResponse<T> {
  items: T[]
  has_more: boolean
  next_cursor?: string
}
```

旧 `PagedResponse<T>` 不修改。

**Step 2: 定义不透明 cursor**

cursor payload 必须包含 `version`、`kind`、排序值和 `id`，使用 URL-safe base64 编码。照片 cursor 需要区分 `taken_at` 非空/空值阶段；人脸 cursor 保存 `quality_score + id`。

测试要求：

- encode/decode 往返一致。
- photos cursor 不能用于 faces。
- 非法 base64、未知 version、缺少字段返回 `INVALID_CURSOR`。
- cursor 中不得包含文件路径、姓名或其他敏感字段。

**Step 3: Handler 兼容性测试**

- `pagination=cursor` 走新模式。
- 未提供 `pagination=cursor` 时继续走现有 page 模式。
- 新模式不返回 `total/page/total_pages`。
- 非法 cursor 返回 HTTP 400 和稳定错误码 `INVALID_CURSOR`。

**Step 4: 运行测试并确认先失败**

```bash
cd backend
go test ./internal/api/v1/handler -run 'TestPeopleMediaCursor|TestPeopleHandler.*Cursor' -count=1
```

**Step 5: Commit**

```bash
git add backend/internal/model/dto.go backend/internal/api/v1/handler/people_media_cursor.go backend/internal/api/v1/handler/people_media_cursor_test.go backend/internal/api/v1/handler/people_handler_test.go frontend/src/types/api.ts
git commit -m "feat(people): define media cursor pagination contract"
```

### Task 5：实现人物照片 cursor 查询并增加复合索引

**Files:**

- Modify: `backend/internal/model/face.go`
- Modify: `backend/internal/repository/photo_repo.go`
- Modify: `backend/internal/repository/photo_repo_test.go`
- Modify: `backend/internal/repository/repository.go` or the existing photo repository interface file

**Step 1: 写失败测试**

覆盖：

- 同一人物在同一照片有多张脸，照片只返回一次。
- 相同 `taken_at` 使用 `id DESC` 稳定翻页。
- `taken_at IS NULL` 排在非空时间之后，跨入 NULL 区间后无重复、无遗漏。
- 第一页和后续页都只取 `limit + 1`，不执行 COUNT。
- 旧 page 查询结果保持不变。

**Step 2: 增加索引**

在 `Face` 模型保留现有单列索引，并新增 `(person_id, photo_id)` 复合索引。测试通过 `PRAGMA index_list/index_info` 验证列顺序。

**Step 3: 实现查询**

新增 repository 方法，不改旧方法签名：

```go
ListPhotoSummariesByPersonIDCursor(personID uint, cursor *PersonPhotoCursor, limit int) ([]*model.Photo, bool, *PersonPhotoCursor, error)
```

查询约束：

- 使用 `faces(person_id, photo_id)` 先限定人物关联照片。
- 按现有 `photos.taken_at DESC, photos.id DESC` 排序。
- 非 NULL cursor 条件必须包含后续 NULL 记录。
- NULL cursor 只继续扫描 NULL taken_at 且更小的 ID。
- 取 `limit + 1`，裁掉额外项后计算 `hasMore` 和 `nextCursor`。
- 不执行 `COUNT(DISTINCT ...)`。

**Step 4: 查询计划回归**

测试或诊断输出必须证明关联子查询使用新的 `(person_id, photo_id)` 索引；不能退化为 faces 全表扫描。

**Step 5: 运行测试**

```bash
cd backend
go test ./internal/repository -run 'TestPhotoRepository_.*Person.*Cursor|TestFacePersonPhotoIndex' -count=1
```

Expected: PASS。

**Step 6: Commit**

```bash
git add backend/internal/model/face.go backend/internal/repository/photo_repo.go backend/internal/repository/photo_repo_test.go backend/internal/repository/*.go
git commit -m "perf(people): add count-free photo cursor pagination"
```

### Task 6：实现人物人脸 cursor 查询

**Files:**

- Modify: `backend/internal/repository/face_repo.go`
- Modify: `backend/internal/repository/face_exclusion_repo_test.go`
- Modify: the existing face repository interface file

**Step 1: 写失败测试**

覆盖：

- `quality_score DESC, id ASC` 与旧排序一致。
- 相同 quality score 跨页无重复、无遗漏。
- excluded 人脸仍被过滤。
- 最后一页正确返回 `has_more=false`。
- cursor 模式不执行 COUNT，旧 page 模式保持兼容。

**Step 2: 实现查询**

新增：

```go
ListByPersonIDCursor(personID uint, cursor *PersonFaceCursor, limit int) ([]*model.Face, bool, *PersonFaceCursor, error)
```

使用 `limit + 1` 和 keyset 条件：

```sql
quality_score < :score
OR (quality_score = :score AND id > :id)
```

继续使用精简 SELECT，不能读取 embedding。

**Step 3: 运行测试**

```bash
cd backend
go test ./internal/repository -run 'TestFaceRepository_.*Cursor' -count=1
```

Expected: PASS。

**Step 4: Commit**

```bash
git add backend/internal/repository/face_repo.go backend/internal/repository/face_exclusion_repo_test.go backend/internal/repository/*.go
git commit -m "perf(people): add count-free face cursor pagination"
```

### Task 7：接通 handler、前端 API 与人物详情页

**Files:**

- Modify: `backend/internal/api/v1/handler/people_handler.go`
- Modify: `backend/internal/api/v1/handler/people_handler_test.go`
- Modify: `frontend/src/api/people.ts`
- Modify: `frontend/src/views/People/Detail.vue`
- Modify: `frontend/src/views/People/Detail.spec.ts`

**Step 1: 后端接通新模式**

`GetPersonPhotos` / `GetPersonFaces` 在 `pagination=cursor` 时：

- 校验 `page_size`，继续沿用当前上限。
- decode cursor。
- 调用对应 cursor repository 方法。
- 返回 `CursorPagedResponse`。
- 不调用旧 paginated 方法，不执行 total 计算。

旧分支保持原样。

**Step 2: 前端切换 cursor 状态**

`peopleApi.getPhotos/getFaces` 增加 cursor 模式参数和返回类型。`Detail.vue` 删除 `photosPage/facesPage` 作为新模式状态，改为：

- `photosNextCursor/facesNextCursor`
- `photosHasMore/facesHasMore`
- `photosLoading/facesLoading`
- `photosError/facesError`

Tab 标题继续读取 `person.photo_count/face_count`。判断“已移动全部人脸”继续以人物详情返回的 `face_count` 为权威，不依赖 cursor response total。

**Step 3: 运行前后端测试**

```bash
cd backend
go test ./internal/api/v1/handler ./internal/repository -count=1

cd ../frontend
npm test -- --run src/views/People/Detail.spec.ts src/views/People/peopleGridUtils.spec.ts
npm run typecheck
npm run build
```

Expected: PASS。

**Step 4: Commit**

```bash
git add backend/internal/api/v1/handler/people_handler.go backend/internal/api/v1/handler/people_handler_test.go frontend/src/api/people.ts frontend/src/views/People/Detail.vue frontend/src/views/People/Detail.spec.ts
git commit -m "perf(people): use cursor pagination in detail view"
```

## 阶段三：事件增量聚类后台让路

### Task 8：为 event clustering 增加 coordinator 准入与合并

**Files:**

- Modify: `backend/internal/service/background_task_coordinator.go`
- Modify: `backend/internal/service/background_task_coordinator_test.go`
- Modify: `backend/internal/service/event_clustering_service.go`
- Modify: `backend/internal/service/event_clustering_service_test.go`
- Modify: `backend/internal/service/service.go`
- Modify: `backend/internal/service/photo_scan_service.go`

**Step 1: 写失败测试**

覆盖：

- automatic event clustering 在 `iowait_high` 时不启动重工作，保持 pending。
- foreground active 时不启动。
- 多次扫描完成只保留一个 running + 一个 pending，不并发运行多个 incremental。
- 被拒绝后，负载恢复时能够重试。
- 用户显式 `StartClustering/StartRebuild` 仍按 P1 user 运行。

**Step 2: 增加 task class**

新增：

```go
BackgroundTaskEventClustering BackgroundTaskClass = "event_clustering"
```

给 `eventClusteringService` 注入现有 coordinator。扫描完成不再直接 `go RunIncremental()`，改为只提交 automatic request。

**Step 3: 实现单槽 pending worker**

- 使用内存 dirty/pending flag 和 wake channel；不新增 SQLite 高频任务表。
- coordinator 拒绝时保留 pending，并按短退避重新检查。
- `Begin` 成功后必须 defer release。
- 日志必须包含 `class=event_clustering priority=automatic decision=<reason>`。

**Step 4: 运行测试**

```bash
cd backend
go test ./internal/service -run 'Test.*EventClustering.*(Coordinator|Coalesce|Pending|UserPriority)' -count=1
```

Expected: PASS。

**Step 5: Commit**

```bash
git add backend/internal/service/background_task_coordinator.go backend/internal/service/background_task_coordinator_test.go backend/internal/service/event_clustering_service.go backend/internal/service/event_clustering_service_test.go backend/internal/service/service.go backend/internal/service/photo_scan_service.go
git commit -m "feat(event): govern automatic clustering startup"
```

### Task 9：把事件增量聚类改为可暂停的小批次

**Files:**

- Modify: `backend/internal/service/event_clustering_service.go`
- Modify: `backend/internal/service/event_clustering_service_test.go`
- Modify: `backend/internal/model/event.go` only if an optional progress field is required

**Step 1: 写结果等价测试**

用同一组照片运行：

- 一次连续 incremental。
- 每批暂停并恢复的 incremental。

断言最终 event 数量、photo.event_id 归属、event 时间范围、photo_count 和画像结果一致。

**Step 2: 增加批次安全点**

- 聚类发现阶段完成后，把 clusters 作为本轮内存工作集。
- 每个 slice 只处理受配置限制的 cluster 数或时间预算。
- 每个 cluster 写入保持当前 WriteQueue 语义。
- 批次边界检查 context、coordinator foreground 与 load snapshot。
- 需要让路时保存下一个 cluster index，释放 automatic slot，并保持 pending。
- 进程重启后可重新从 `event_id IS NULL` 发现未完成照片，不要求新增持久化 cursor。

**Step 3: 补充日志和状态**

至少记录：

- discovered photos / clusters
- processed clusters / photos
- paused reason
- slice elapsed
- resumed count
- completed

**Step 4: 运行测试**

```bash
cd backend
go test ./internal/service -run 'TestEventClustering.*(Batch|Pause|Resume|Equivalent)' -count=1
go test ./... -count=1
```

Expected: PASS。

**Step 5: Commit**

```bash
git add backend/internal/service/event_clustering_service.go backend/internal/service/event_clustering_service_test.go backend/internal/model/event.go
git commit -m "perf(event): make incremental clustering yieldable"
```

## 阶段四：全量验证与 NAS 验收

### Task 10：本地全量回归

**Files:**

- Test only; do not make unrelated cleanup changes.

**Step 1: Backend**

```bash
cd backend
go test ./... -count=1
go build -o /tmp/relive ./cmd/relive
```

Expected: PASS。

**Step 2: Frontend**

```bash
cd frontend
npm test -- --run
npm run test:browser -- --run
npm run typecheck
npm run build
```

Expected: PASS。允许记录现有 chunk size warning，但不得出现新增错误。

**Step 3: Diff hygiene**

```bash
git diff --check
git status --short
```

确认没有覆盖当前工作区的 split conflict 改动，也没有混入无关格式化。

### Task 11：NAS 只读验收

部署动作不属于本计划自动执行范围；部署后再进行以下只读验证。

**Step 1: 请求行为**

分别打开：

- `/people/264862`
- `/people/271594`

确认：

- 首次只请求人物信息和照片 cursor 第一页。
- 不滚动时 30 秒内没有第二页请求。
- 接近列表末尾时一次只增加一个 cursor 请求。
- 切换人脸 Tab 前没有 faces 列表请求。

**Step 2: 接口时延**

从 NAS Gin 日志统计：

- 正常负载照片第一页 P95 `< 1s`。
- 自动后台任务运行时 P95 `< 3s`。
- 不再出现超过 Axios `30s` 的人物照片/人脸列表请求。

**Step 3: 资源与后台治理**

检查：

```bash
/usr/local/bin/docker stats --no-stream relive relive-ml
iostat -x 1 2
/usr/local/bin/docker logs --since 30m --timestamps relive
```

确认：

- 单次人物详情访问不会产生几十或上百分页。
- 缩略图请求随可见内容增长，不在首屏形成全量洪峰。
- event clustering 在 `iowait_high` 或 foreground active 时出现 pause/defer 日志。
- 负载恢复后任务继续并最终完成。

**Step 4: 最终提交**

仅在所有验证通过后提交遗漏的测试或文档调整：

```bash
git add <only-files-from-this-plan>
git commit -m "test(people): verify detail performance on NAS scale"
```

## 最终验收标准

1. 整页自然滚动保留，没有列表内部滚动条。
2. 照片和人脸仍自动无限加载，但只在接近可见末尾时加载。
3. 首次进入不会自动连续请求后续分页。
4. 三种密度、四种目标视口宽度下没有行覆盖。
5. 人脸“头像”“照片”按钮完整可见、可点击。
6. DOM 卡片数量与可见行和 overscan 有关，不随已加载总数线性增长。
7. cursor 分页无 COUNT、无重复、无遗漏，旧 page API 兼容。
8. `264862`、`271594` 在 NAS 规模下满足时延指标。
9. 自动事件聚类可合并、可暂停、可恢复，最终结果不变。
10. 不修改任何人物写操作、聚类算法或策展语义。
