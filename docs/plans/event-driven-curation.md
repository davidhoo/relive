# 事件驱动型智能策展方案

> 日期：2026-03-16
> 状态：Phase 0 + Phase 1 + Phase 2a/2b/2c 已完成，其余搁置

## 目标

将 Relive 的照片展示从"单图随机/往年今日"升级为"事件驱动型智能策展"，让相框每天展示的照片具备内容丰富度、视觉美感、新鲜感和叙事性。

## 整体架构

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│  第一阶段    │     │  第二阶段     │     │  第三阶段     │     │  第四阶段     │
│  事件聚类    │────▶│  事件画像     │────▶│  策展召回     │────▶│  相框交付     │
│             │     │              │     │              │     │              │
│ 输入:       │     │ 输入:        │     │ 输入:         │     │ 输入:         │
│  taken_at   │     │  tags        │     │  事件特征     │     │  今日N张      │
│  GPS坐标    │     │  category    │     │  event_score  │     │              │
│             │     │  scores      │     │  cover_photo  │     │ 输出:         │
│ 输出:       │     │  caption     │     │              │     │  渲染资产     │
│  event_id   │     │              │     │ 输出:         │     │  (bin/jpg)    │
│             │     │ 输出:        │     │  今日N张照片   │     │              │
│ AI参与: 无   │     │  事件特征字段 │     │              │     │              │
│ (纯时空)    │     │  AI参与: 高   │     │  AI参与: 高   │     │  AI参与: 无   │
└─────────────┘     └──────────────┘     └──────────────┘     └──────────────┘
```

保留现有展示策略（on_this_day / random）不动，新方案作为新的 algorithm 选项（如 `"event_curated"`）并行存在。

---

## 第一阶段：事件聚类 (Event Clustering)

### 1.1 聚类算法：时空切分

基于 `taken_at` 和 GPS 坐标，将照片按连续性聚合为事件：

- **时间切分**：相邻照片间隔 < 6 小时 → 同一事件；间隔 > 24 小时 → 必为新事件
- **空间校验**：6 小时内地理位置偏移 > 50 公里 → 强制切分（赶飞机、跨城旅行）
- **AI 不参与聚类**：聚类只依赖时空信息，保证算法确定性，不受 AI 分析质量波动影响

参数设计（可配置）：

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `time_gap_same_event` | 6h | 小于此间隔视为同一事件 |
| `time_gap_new_event` | 24h | 大于此间隔强制新事件 |
| `distance_force_split` | 50km | 超过此距离强制切分 |

6h~24h 之间的"灰色地带"处理：结合 GPS 距离判断。有 GPS 且距离 < 50km → 同一事件；无 GPS → 按时间间隔是否超过中位值判断。

### 1.2 照片分类

聚类后照片分为两类：

- **事件照片**：有 `taken_at`，成功归入某个事件，`event_id` 非空
- **散片**：无 `taken_at` 或不满足聚类条件，`event_id` 为 NULL
  - 散片不是废片，仍然参与策展（进入"角落遗珠"等召回通道）
  - 截图/文档类照片（可通过 `main_category` 识别）建议用 `status = excluded` 排除

### 1.3 触发时机

**增量聚类**：
- 触发点：扫描任务（scan/rebuild）完成后自动触发
- 范围：只处理 `event_id IS NULL` 且 `taken_at IS NOT NULL` 的新照片
- 策略：只追加 — 新照片归入已有事件或创建新事件，不修改已有事件边界
- 不是每张照片触发一次，而是一批扫描完成后统一聚类

**全量重建**：
- 触发点：手动触发（管理页面入口 / API）
- 范围：清空所有 event_id，全库重新排序+切分
- 策略：全量覆盖 — 聚类算法是确定性的，同样输入产生同样输出
- 作为后台慢任务运行，复用现有后台任务基础设施（类似 thumbnail/geocode background task）

**依赖关系**：
- 聚类依赖 `taken_at` + 原始 GPS 坐标（`gps_latitude`/`gps_longitude`）
- 不依赖 geocode 结果（地名），可与 geocode Job 并行

### 1.4 数据模型

#### events 表

```sql
CREATE TABLE events (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    start_time        DATETIME NOT NULL,      -- 事件起始时间（簇内最早 taken_at）
    end_time          DATETIME NOT NULL,      -- 事件结束时间（簇内最晚 taken_at）
    duration_hours    REAL NOT NULL DEFAULT 0, -- 持续时长（小时）
    photo_count       INTEGER NOT NULL DEFAULT 0, -- 照片数量
    gps_latitude      REAL,                   -- 事件中心点纬度（簇内 GPS 均值）
    gps_longitude     REAL,                   -- 事件中心点经度
    location          TEXT DEFAULT '',        -- 代表地名（最频繁的 photo.location）
    cover_photo_id    INTEGER,               -- 代表图 ID（beauty_score 最高）
    primary_category  TEXT DEFAULT '',        -- 主分类（簇内最频繁 main_category）
    primary_tag       TEXT DEFAULT '',        -- 主标签（簇内最频繁 tag）
    event_score       REAL NOT NULL DEFAULT 0, -- 事件分值
    display_count     INTEGER NOT NULL DEFAULT 0, -- 累计展示次数
    last_displayed_at DATETIME,              -- 最后展示时间
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL,

    FOREIGN KEY (cover_photo_id) REFERENCES photos(id) ON DELETE SET NULL
);

CREATE INDEX idx_events_start_time ON events(start_time);
CREATE INDEX idx_events_event_score ON events(event_score DESC);
CREATE INDEX idx_events_display_count ON events(display_count);
CREATE INDEX idx_events_last_displayed ON events(last_displayed_at);
CREATE INDEX idx_events_primary_category ON events(primary_category);
```

#### photos 表新增字段

```sql
ALTER TABLE photos ADD COLUMN event_id INTEGER REFERENCES events(id) ON DELETE SET NULL;
CREATE INDEX idx_photos_event_id ON photos(event_id);
```

#### event_clustering_jobs 表

复用现有 Job 模式，或直接用 `scan_jobs` 表增加 `type = "event_cluster"` / `"event_rebuild"`。

### 1.5 聚类算法伪代码

```
func clusterPhotos(photos []Photo) []Event:
    // 1. 按 taken_at 排序
    sort(photos, by=taken_at ASC)

    // 2. 顺序扫描，切分事件
    events = []
    currentEvent = [photos[0]]

    for i in 1..len(photos):
        prev = currentEvent.last()
        curr = photos[i]

        timeDelta = curr.taken_at - prev.taken_at
        gpsDist = haversine(prev.gps, curr.gps)  // 无 GPS 时返回 0

        shouldSplit = false
        if timeDelta > 24h:
            shouldSplit = true
        elif timeDelta > 6h:
            // 灰色地带：有 GPS 且距离远则切分
            if gpsDist > 0 && gpsDist > 50km:
                shouldSplit = true
            else:
                shouldSplit = true  // 无 GPS 时保守切分
        elif gpsDist > 50km:
            shouldSplit = true  // 6h 内但跨城

        if shouldSplit:
            events.append(buildEvent(currentEvent))
            currentEvent = [curr]
        else:
            currentEvent.append(curr)

    events.append(buildEvent(currentEvent))
    return events
```

---

## 第二阶段：事件画像 (Event Profiling)

聚类完成后，对每个事件计算特征字段。画像在聚类 Job 中同步完成，不需要独立 Job。

### 2.1 事件分值

```
event_score = avg(overall_score) * log2(photo_count + 1)
```

- `overall_score` = 现有综合分（beauty * 0.4 + memory * 0.6）
- 对数压制照片数量：单张高分照片有中等分值；10 张旅行照偏高；100 张婚礼照很高但不爆炸
- 示例：avg_score=80, count=1 → 80；avg_score=70, count=10 → 242；avg_score=60, count=50 → 340

### 2.2 特征提取

遍历事件内所有照片：

| 字段 | 计算方式 |
|------|----------|
| `cover_photo_id` | `beauty_score` 最高的照片 ID |
| `primary_category` | 簇内出现频次最高的 `main_category` |
| `primary_tag` | 簇内出现频次最高的 tag（从 photo_tags 表聚合） |
| `location` | 最频繁的 `photo.location`；若无则用 GPS 中心点反查 |
| `gps_latitude/longitude` | 簇内所有有 GPS 照片的坐标均值 |
| `duration_hours` | `(end_time - start_time)` 转小时 |

### 2.3 画像更新时机

- 聚类 Job 完成时同步计算
- AI 分析完成后（新照片被分析），可触发关联事件的画像刷新（更新 primary_tag、cover_photo 等）
- 不需要独立 Job，作为轻量操作附加在现有流程中

---

## 第三阶段：策展召回引擎 (Curation Engine)

服务器每天为相框生成包含 N 张照片的"今日播放列表"。

### 3.1 多维提名（替代固定轨道配额）

不预设轨道配额，而是让每个维度各自提名候选事件，最后统一评分。某个维度没有候选时自然不产生结果，无需特殊处理。

**提名维度**：

| 维度 | 召回逻辑 | 候选内容 |
|------|----------|----------|
| 时光隧道 | 往年今日 ±N 天的事件 | 事件的 cover_photo |
| 巅峰回忆 | 全库 event_score 最高的事件（不限年份） | 事件的 cover_photo |
| 地理漂移 | GPS 距离用户常驻地最远的事件 | 事件的 cover_photo（需要有 GPS） |
| 角落遗珠 | `display_count = 0` 且 `beauty_score > 阈值`的散片或事件 | 照片本身 |
| （可扩展） | 未来可加：人物专题、季节匹配、特定 tag 专题等 | ... |

**常驻地计算**：
- 从 photos 表聚合最高频的 city（已有 country/province/city 字段）
- 或取 GPS 坐标的聚类中心（K-means with K=1）
- 缓存在 app_config 中，定期更新

### 3.2 动态评分修正

对候选池中的照片/事件做二次加权：

| 修正因子 | 规则 | 权重 |
|----------|------|------|
| 季节对齐 | 拍摄月份与当前月份一致 | × 1.2 |
| 新鲜度压制 | 过去 30 天展示过的事件 | × 0.1 |
| 人物偏好 | primary_tag 含人物相关标签 | + 20 |
| 标签季节匹配 | tag 含"雪"/"滑雪" + 当前冬天 | × 1.15 |
| 展示衰减 | display_count 越高，分值越低 | × 1/(1 + display_count * 0.1) |

评分修正参数应可配置，方便后续调优。

### 3.3 多样性选择（隔离机制）

从评分后的候选池中，按分值降序逐张选入最终列表，每选一张后更新禁止规则：

| 隔离规则 | 说明 |
|----------|------|
| 事件隔离 | 同 `event_id` 的其他照片/事件今日不再入选（一天不全是某次旅行） |
| 时间隔离 | `taken_at` 前后 24 小时内的照片不再入选 |
| 内容隔离 | 已选中 `primary_category` = X，下一张优先选不同 category |

这与现有 `display_algorithm.go` 的多样性选择（时间间隔、事件桶、位置桶）是同一思路的升级版，基于持久化的 event_id 而非每次动态计算。

### 3.4 序列编排

将选出的 N 张照片按视觉节奏排序：

- **第 1 张**：beauty_score 最高（"清晨惊喜"）
- **中间部分**：按 `primary_category` 交叉排列（travel → family → food → landscape），避免连续同类
- **末尾**：memory_score 最高（"睡前怀旧"）

---

## 第四阶段：相框交付

复用现有 DailyDisplayBatch 机制，策展引擎只替换"选照片"这一步，渲染流水线（canvas → dither → bin/header）保持不动。

### 4.1 与现有系统的对接

```
现有流程：
  getOnThisDayPhotos() → 选出 N 张 → GenerateDailyBatch() → 渲染资产

新增流程：
  curateEventPhotos()  → 选出 N 张 → GenerateDailyBatch() → 渲染资产
                                      ↑ 这里开始完全复用
```

策展引擎作为 `DisplayStrategyConfig.Algorithm = "event_curated"` 的新选项，与现有 `on_this_day` / `random` 并行。

### 4.2 展示记录更新

每张照片展示后，除了写 `display_records` 表（现有），还需更新：
- `events.display_count += 1`
- `events.last_displayed_at = now`

用于后续的"新鲜度压制"和"展示衰减"。

---

## 实施路线

```
Phase 0: 事件聚类 ✅ 已完成 (2026-03-16, commit eeaac37)
  - events 表 + photos.event_id 字段
  - 聚类算法实现（时空切分）
  - 增量聚类 Job（扫描完成后触发）
  - 全量重建 Job（手动触发，后台慢任务）
  - 事件画像计算（聚类同步完成）
  - 管理页面：事件列表、手动触发重建

Phase 1: 策展引擎 ✅ 已完成 (2026-03-16, commit 640437c)
  - 多维提名（时光隧道 / 巅峰回忆 / 地理漂移 / 角落遗珠）
  - 常驻地计算（app_config 缓存，7 天有效期）
  - 动态评分修正（季节/新鲜度/人物/展示衰减）
  - 多样性选择（事件隔离 / 时间隔离 / 内容隔离，两轮贪心）
  - 序列编排（首张最美 / 末张最忆 / 中间交叉）
  - 作为新 algorithm="event_curated" 对接 DailyBatch
  - 展示计数反馈（display_count / last_displayed_at）

Phase 2: 精细化调优（部分完成）
  - ✅ 2a: 评分修正参数可配置化（前端管理页面） (2026-03-16, commit 689ea91)
  - ✅ 2b: 策展效果可视化（批次详情标注来源通道） (2026-03-16, commit 33b37a4)
  - ✅ 2c: 人物专题 + 季节专题提名通道 (2026-03-16, commit 911d7cd)
  - 搁置: 纪念日提名、常驻地管理(2d)、A/B 效果评估(2e)
```

---

## 与现有代码的关系

| 现有模块 | 变更 |
|----------|------|
| `display_service.go` | GetDisplayPhoto 新增 `"event_curated"` case |
| `display_algorithm.go` | 保留不动，新策展逻辑写在独立文件 |
| `display_config.go` | DisplayStrategyConfig 新增策展相关参数 |
| `display_daily_service.go` | GenerateDailyBatch 内部调用策展引擎选照片，渲染流水线不变 |
| `photo_scan_service.go` | runScanTask 完成后触发聚类 Job |
| `model/` | 新增 Event model、EventClusterJob 相关 |
| `repository/` | 新增 EventRepository |
| `service/` | 新增 event_cluster_service.go、event_curation_service.go |
