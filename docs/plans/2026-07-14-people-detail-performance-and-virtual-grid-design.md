# 人物详情页整页虚拟化、轻量分页与后台让路设计

## 背景

人物详情页在 `d7da1c1` 引入 `VirtualMediaGrid` 后出现三类相关回归：

1. 照片和人脸网格使用固定估算行高，真实卡片高度超出虚拟行后被下一行覆盖。
2. 虚拟网格把自身当作滚动容器，但容器没有受限高度，真实滚动发生在整个页面；虚拟化、可见区判断和自动分页因此失真。
3. 大人物首次进入详情页时，照片分页接口先执行全量 `COUNT(DISTINCT photos.id)`，再执行 `DISTINCT + ORDER BY + OFFSET/LIMIT`。请求洪峰、缩略图 I/O 和后台事件聚类会进一步放大 SQLite 与 NAS 磁盘争用。

2026-07-14 NAS 只读诊断得到以下直接证据：

- `/api/v1/people/264862`：约 `0.87ms`。
- `/api/v1/people/264862/photos?page=1&page_size=30`：约 `22.85s`。
- 人物 `264862` 有 `4723` 张人脸、`4683` 张关联照片。
- 一个 `12769` 张关联照片的人物，照片第一页曾耗时约 `43.78s`，超过前端 `30s` 超时。
- 人物 `269417` 的一次访问自动请求了 `123` 个照片分页，页码一路到 `122`。
- 采样窗口内 NAS `iowait` 约 `27.9%`，数据卷达到 `100% util`；一分钟内 `125` 个 API 请求中有 `94` 个缩略图请求。
- 扫描完成后直接启动的事件增量聚类处理 `753` 张未聚类照片，未经过 `BackgroundTaskCoordinator` 准入与分批让路。

## 已确认的产品方向

- 保留整个页面自然向下滚动，不增加照片/人脸区域内部滚动条。
- 保留自动无限加载。
- 只有页面接近当前可见列表末尾时才加载下一页。
- 同一列表同一时间只允许一个分页请求。
- 隐藏 Tab 不加载；切换到人脸 Tab 后才加载人脸第一页。
- 不改变照片、人脸排序，不改变人物识别、聚类或事件策展结果。

## 目标

1. 修复所有密度和响应式宽度下的卡片覆盖，完整显示照片文字和人脸操作按钮。
2. 让虚拟化真正基于页面滚动工作，DOM 数量只与可见行和 overscan 有关。
3. 阻止首次进入、数据追加、Tab 隐藏状态或加载失败触发连续自动翻页。
4. 让照片/人脸详情分页不再为每一页执行全量计数。
5. 让扫描后自动事件聚类遵守统一后台任务准入、高 I/O 背压和前台让路规则。
6. 用真实浏览器布局测试和 NAS 规模验收覆盖本次回归。

## 总体架构

### 1. 前端：基于 window 的行级虚拟化

继续复用 `frontend/src/components/VirtualMediaGrid.vue`，但把滚动源从组件内部 `scrollRef` 改为浏览器窗口：

- 使用 `useWindowVirtualizer`，不再给网格设置 `overflow-y: auto`。
- 网格仅负责生成总高度和绝对定位虚拟行，页面仍由浏览器自然滚动。
- 为每个虚拟行绑定 `data-index` 和 `measureElement`，以真实 DOM 高度作为最终行高。
- `rowHeight` 只作为首屏估算值；照片与人脸、三种密度分别提供估算，不再共用一套 `110/260/420px`。
- 列数仍与当前响应式断点一致；密度或窗口宽度变化后重新测量。
- Tab 由隐藏变可见后重新测量，并恢复该 Tab 的页面滚动锚点。

分页触发必须来自“最后一个实际可见行接近当前数据末尾”，不能仅因 `items.length` 变化而无条件触发。以下状态必须同时满足：

- 当前 Tab 可见；
- `has_more = true`；
- `loading = false`；
- `error = false`；
- 最后可见行距离当前数据末尾不超过阈值。

切换人物或组件卸载时取消旧请求或丢弃旧人物响应，避免陈旧分页追加到新人物。

### 2. 后端：新增向后兼容的游标分页模式

保留现有 `page/page_size` 响应，兼容旧调用；人物详情页改用新的游标模式：

```text
GET /api/v1/people/:id/photos?pagination=cursor&page_size=30&cursor=<opaque>
GET /api/v1/people/:id/faces?pagination=cursor&page_size=50&cursor=<opaque>
```

响应统一为：

```json
{
  "items": [],
  "has_more": true,
  "next_cursor": "opaque-or-empty"
}
```

规则：

- 每次查询 `page_size + 1` 条；多出的 1 条只用于计算 `has_more`。
- 不执行 `COUNT`。
- Tab 总数继续使用 `GET /people/:id` 已返回的 `photo_count`、`face_count`。
- 照片排序保持 `taken_at DESC, id DESC`，游标必须显式处理 `taken_at IS NULL`。
- 人脸排序保持 `quality_score DESC, id ASC`。
- 游标是服务端编码的不透明字符串，非法或与当前资源类型不匹配时返回 `400 INVALID_CURSOR`。
- 旧分页模式的 response shape 和行为保持不变。

为 `faces` 增加 `(person_id, photo_id)` 复合索引，使同一人物关联照片去重能走覆盖索引；保留现有 `idx_face_person` 和 `idx_face_photo`，避免影响其他查询。

### 3. 后台：事件增量聚类纳入统一治理

当前 `photo_scan_service.go` 在扫描完成后直接执行：

```go
go s.eventClusteringService.RunIncremental()
```

该路径没有 `BackgroundTaskCoordinator` 准入，也没有可恢复的分批检查点。设计调整为：

- 新增 `BackgroundTaskEventClustering` class。
- 扫描完成只标记一次 automatic incremental 请求，不直接无限制启动重工作。
- 通过 coordinator `Begin` 获取 automatic slot，使用固定 `DedupeKey` 合并重复扫描触发。
- 因 `foreground_active`、`iowait_high`、cooldown 或已有任务而拒绝时，保留 pending/dirty 状态，由调度循环稍后重试，不能当作完成。
- 事件簇按小批次处理；每批提交后检查 context、foreground 和负载。需要让路时保存游标并退出本 slice。
- 用户显式点击的事件聚类/重建仍是 P1 user 任务，不被 automatic 背压直接拒绝，但仍沿用现有停止与状态机制。

本阶段只改变调度时机和执行粒度，不改变事件聚类算法、时间窗口、空间距离、最小照片数、事件画像或最终归属结果。

## 数据流

```text
进入人物详情
  -> GET /people/:id
  -> GET /people/:id/photos?pagination=cursor&page_size=30
  -> 渲染可见虚拟行并测量真实高度
  -> 页面接近照片列表末尾
  -> 使用 next_cursor 请求下一页（最多一个 in-flight）

切换到人脸 Tab
  -> 停止照片分页触发
  -> 首次 GET /people/:id/faces?pagination=cursor&page_size=50
  -> 按同一规则继续自然页面滚动
```

## 错误处理

- 首次加载失败：保留已加载人物信息，列表显示明确失败状态和手动重试。
- 后续页失败：保留已有列表与当前滚动位置；失败期间禁止自动重试，用户点击“重试”后才使用同一 cursor 重发。
- 重复响应：按 `id` 去重，但去重不能代替 in-flight 防重。
- cursor 非法：显示分页状态错误并停止继续加载，不回退为全量接口。
- 切换人物：旧请求响应必须被忽略，不能污染新人物列表。
- 后台事件聚类被背压：记录 class、decision reason、cursor/progress；不记录为 completed。

## 测试策略

### 前端逻辑测试

- 初次进入只请求人物信息和照片第一页。
- 不滚动时不会请求照片第二页。
- 接近末尾只新增一次请求；请求未完成时重复滚动不重复提交。
- 隐藏 Tab 不触发分页。
- 首次切换人脸 Tab 只加载人脸第一页。
- 分页失败后不自动重试，手动重试复用原 cursor。
- 切换人物后忽略旧请求响应。

### 真实浏览器布局测试

jsdom 不计算真实 CSS 布局，本任务必须增加浏览器模式测试。至少覆盖：

- 视口宽度 `1440`、`1024`、`768`、`390`。
- 照片和人脸的 `small/medium/large` 三种密度。
- 相邻虚拟行的 bounding box 不重叠。
- 照片图片、标题和时间完整位于卡片内。
- 人脸“头像”“照片”按钮可见且可点击。
- 加载上千条数据时，实际 DOM 卡片数量保持有界。

### 后端测试

- cursor 首次页、后续页、最后一页、空列表。
- 相同排序值时按 `id` 稳定翻页，无重复、无遗漏。
- `taken_at IS NULL` 的照片保持旧排序语义。
- 非法 cursor 返回 `400 INVALID_CURSOR`。
- cursor 模式不执行 COUNT；旧 page 模式保持兼容。
- `(person_id, photo_id)` 索引迁移存在且查询结果正确。
- event clustering automatic 请求被 foreground/iowait 拒绝时保持 pending。
- 多次扫描触发被 coalesce。
- 事件簇在批次边界暂停、恢复后最终结果与连续执行一致。

## 验收指标

1. 首次打开人物详情只出现人物信息请求和照片第一页请求。
2. 未接近列表末尾时，不出现第二页请求。
3. `264862`、`271594` 的照片第一页正常负载下 NAS API P95 小于 `1s`。
4. 自动后台任务运行期间，人物照片第一页 P95 小于 `3s`，不得超过前端 `30s` 超时。
5. 一次页面会话不会再出现页码自动增长到几十或上百页。
6. NAS 不再因单个人物详情访问持续产生大量分页和缩略图 I/O。
7. 所有断点和密度下相邻行不覆盖，人脸操作按钮完整可见。
8. 旧 `page/page_size` API 调用仍能工作。
9. 事件聚类背压、暂停和恢复有明确日志与状态，不丢任务、不重复并发。

## 不包含

- 不修改人物识别、人物聚类、identity profile、ANN、合并建议算法或阈值。
- 不修改照片、人脸的现有排序语义。
- 不修改 split、move、merge、排除人脸、头像设置等人物写操作契约。
- 不修改照片详情页的人脸归属编辑流程。
- 不引入 Redis、外部任务队列或新的数据库。
- 不把整个照片或人脸列表一次性返回前端。
- 不用简单增大固定行高作为最终修复。
- 不通过提高 Axios 超时时间掩盖慢查询或请求洪峰。
