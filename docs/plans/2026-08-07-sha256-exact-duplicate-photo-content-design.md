# SHA-256 精确重复照片：内容级索引归一化技术方案

> 状态：Candidate / 未实施。本文只定义 SHA-256 完全相同文件的逻辑去重；不移动、删除或修改任何物理照片文件。

## 1. 目标与结论

Relive 当前把一个 `file_path` 当作一张照片。扫描虽会为文件计算 SHA-256 `file_hash`，但该哈希仅有普通索引而非内容身份，因此同一字节文件在不同目录、不同文件名下会产生多个 `photos` 行，以及多份缩略图、AI 分析、GPS、人脸、人物贡献、事件候选和展示候选。

本方案将身份边界改为：

```text
一个文件路径 = 一个来源索引（source）
一个 SHA-256 = 一个逻辑照片内容（content）
一个内容 = 多个来源索引，但所有业务派生数据只生效一次
```

它应达成以下结果：

- 同 SHA-256 的任意路径只在照片列表、搜索、人物、事件、展示和统计中出现一次；
- 新增副本不再重复触发缩略图、AI、GPS、人物检测或聚类；
- 存量副本与其历史数据均保留，不执行物理文件操作，也不删除 `photos`、`faces`、展示记录或人工操作证据；
- 同内容的人脸只对人物画像、合并建议和人物照片数贡献一次；人工归属冲突必须显式保留，不自动覆盖；
- 路径失效时能自动从同内容的其他可读副本读取图片，逻辑内容不失效。

## 2. 范围与非目标

### 本期范围

唯一的自动等价条件为：两个文件的完整 SHA-256 哈希值相同且非空。文件名、目录、文件时间、EXIF 及视觉相似度均不参与此判定。

### 明确不包含

- 重压缩、尺寸变化、格式转换、仅元数据不同的图片；
- 轻微裁剪、截图、屏摄、视频抽帧和视觉相似图片；
- 连拍序列、多机位照片和版本族；
- 移动、重命名、硬链接、删除、压缩或清理物理文件；
- 自动合并不同人物、自动改写人工锁定的人脸归属；
- 本次修改之外的算法阈值、AI 模型、人物聚类策略或发布配置。

后续若处理连拍或近似图片，必须建立独立的“序列/候选关系”，不能复用本方案的强等价关系。

## 3. 当前实现与问题边界

当前 `Photo` 以 `file_path` 为唯一索引，`file_hash` 是 SHA-256 普通索引。`PhotoScanService.processPhoto` 每次扫描都会计算哈希，但扫描写入按路径匹配；新路径即创建新的 `Photo`，随后派发缩略图、GPS 和人物任务。AI、缩略图、展示和人物服务也普遍以 `Photo.ID` 与 `Photo.FilePath` 作为内容身份或文件读取入口。

因此仅在前端隐藏同哈希照片，或仅在 SQL 中 `GROUP BY file_hash`，不能解决后台重复分析和人物画像污染。尤其是重复文件已经证明会留下完全相同的人脸 embedding，并在不同人物中形成错误的合并建议证据。

## 4. 架构设计

### 4.1 新的身份关系

```text
photos（保留：一行一个路径）
  ├─ content_id ───────────┐
  ├─ file_path             │
  ├─ source_state          │
  └─ 既有 Photo ID         │
                              ▼
photo_contents（新增：一行一个 SHA-256 内容）
  ├─ id                    逻辑内容身份
  ├─ sha256 UNIQUE         精确等价键
  ├─ canonical_photo_id    最早来源索引
  ├─ status                内容级 active/excluded
  └─ migration_state       归一化状态/审计信息
```

`photos` 不改名也不删除；在语义上它从“照片内容”成为“内容来源索引”。历史 Photo ID 继续有效，以避免破坏外部链接、历史展示记录和已有 API 调用。

新增字段/表的最低要求：

| 对象 | 字段/约束 | 目的 |
|---|---|---|
| `photo_contents` | `sha256` 唯一、非空 | 保证一个精确内容只建一个内容实体；并发扫描以该约束收敛 |
| `photo_contents` | `canonical_photo_id` | 标识默认元数据和派生结果的主来源 |
| `photo_contents` | `status` | 将显示排除语义提升到内容级 |
| `photos` | `content_id` 索引 | 将每个路径关联到内容实体 |
| `photos` | `source_state`（`available`/`missing`） | 路径不存在时保留索引与审计，而非删除内容 |
| `photo_content_migrations` 或 `app_config` 检查点 | 游标、版本、错误摘要 | 支持存量回填分批、可中断、可重跑 |

`content_id` 在部署迁移完成前允许为 `NULL`；回填完成后，所有哈希非空且可参与业务的来源必须有 `content_id`。哈希为空、读取失败或历史异常的行不自动猜测等价关系，仍按现有逻辑处理，并在审计报告中列出。

### 4.2 主来源与可读来源

“最早的一张”用于固定 `canonical_photo_id`，按以下稳定顺序选择：

1. `taken_at` 最早；
2. `file_create_time` 最早；
3. 数据库 `created_at` 最早；
4. `Photo.ID` 最小。

相同字节文件通常有相同 EXIF 拍摄时间，后续排序确保复制或扫描顺序不改变主来源。主来源失效时不得变更内容 ID 或抹掉主来源；`ContentResolver` 应从同内容 `source_state=available` 的来源中，按同一排序选择当前可读文件。这样既满足“最早”为默认，又能在原路径不可访问时继续展示和处理该内容。

### 4.3 统一后端边界

不得把 `file_hash` 的去重逻辑散落在各业务 SQL 中。新增两个共享边界：

#### ContentResolver

职责：

- 将任意 `Photo ID` 或 `Content ID` 解析为逻辑内容；
- 返回 canonical Photo、当前可读 Source Photo 和所有来源摘要；
- 支持副本 Photo ID 的旧链接解析到 canonical 内容；
- 对读取文件的服务提供 `resolved.FilePath`，而不是让调用方直接使用 `photo.FilePath`。

所有读取原文件的代码——AI 输入、缩略图生成、显示画布构建、人脸检测、EXIF/GPS 读取——都必须先调用该解析器。图像解码器、AI Provider、地理编码 Provider 和人脸模型本身无需理解副本。

#### ContentTaskDispatcher

职责：

- 将缩略图、AI、GPS、人脸的任务键、领取锁、处理状态和重试收敛为 `Content ID`；
- 同一内容有任务运行或已完成时，后续副本不得创建第二个任务；
- 某来源缺失时，为未完成/失败任务选择同组可读来源重试；
- 保留现有 `Photo ID` 任务字段以兼容现有任务表和日志，但它只是本次运行所选来源，不再是任务去重身份。

内容级唯一性必须在数据库写入处保证，不能依赖内存 map 或扫描顺序。两个并发扫描同时发现相同文件时，只允许一个事务创建内容并取得派发权；另一个事务登记来源并读取既有内容状态。

## 5. 新文件扫描与后台任务

扫描流程调整为：

```text
扫描路径
  → 计算 SHA-256
  → 原子创建或取得 PhotoContent
  → 创建/恢复该路径的 Photo source
  → 选定 canonical source 与可读 source
  → ContentTaskDispatcher 判断该内容的派生状态
      ├─ 新内容：各类任务各入队一次
      ├─ 已完成：直接复用结果，不入队
      ├─ 处理中：加入同一内容任务的观察/等待，不创建任务
      └─ 失败或缺失：仅重试这一份内容任务
```

以下派生结果在新架构中必须只存在一份有效结果：

| 能力 | 内容级语义 |
|---|---|
| 缩略图 | 生成一次，所有来源复用；读取原图时可切换可读来源 |
| AI 分析、caption、评分、分类、tags | 分析一次；内容级展示和统计只读取 canonical 结果 |
| GPS / geocode | 解析及地理编码一次；人工地点优先于自动结果 |
| 人脸检测 | 一次标准检测结果是人物业务的唯一有效来源 |
| 人物画像和合并建议 | 每个内容的人脸最多贡献一次 |
| 事件候选和展示候选 | 每个内容最多进入一次 |

现有后台逻辑中，扫描服务的缩略图/GPS/People 入队、缩略图任务的 `PhotoID + FilePath`、AI 服务直接读取 `Photo.FilePath`，都属于首批改造点。仅改扫描服务不足以阻止手动重试、后台重建或 API 触发的重复工作。

## 6. 存量数据归一化

### 6.1 前置安全条件

1. 在离线或低负载窗口，对 SQLite 数据库、`-wal`、`-shm` 和当前运行配置做完整备份；校验 `PRAGMA quick_check` 与备份校验和。
2. 使用现有只读重复人脸审计 CLI 生成基线：重复哈希组数、来源行数、跨人物冲突、人脸和人物 ID。
3. 部署只含新增表/字段与功能开关的版本；默认仍走旧读取路径。
4. 每个回填阶段均写检查点、处理量、跳过量和失败摘要；禁止长事务和全库 BLOB 自连接。

### 6.2 内容映射回填

按 `file_hash` 的稳定游标分批处理非空哈希：

1. 为每一个 distinct hash 幂等创建一个 `photo_contents` 行；
2. 将该 hash 的所有现有 `photos` 行连接到该 `content_id`；
3. 按第 4.2 节的稳定顺序写入 canonical；
4. 不删除、软删除、迁移或覆盖任何原始 Photo 行；
5. 记录元数据、状态和事件归属差异，供后续专门处理；
6. 重跑只补齐缺失映射，不改变已经确认的 canonical，除非来源记录的排序字段确实被修复并经过显式 reconcile。

内容级 `status` 首次取 canonical Photo 的状态；副本原有 `status` 原样保留。若同一内容的来源状态不一致，记录为状态差异而非批量修改副本。

### 6.3 AI、标签、GPS 与缩略图

已经存在的派生数据不物理合并。初期将 canonical Photo 的结果作为唯一有效结果，副本历史结果保持可审计但不参与查询。

如果 canonical 缺少某项成功结果、而副本有可确认的成功结果，可以在内容修复任务中将该结果复制到 canonical，并记录来源 Photo ID 和字段版本；有相互矛盾的人工地点、分类、标签或分析结果时，不做盲目合并，保留差异并以 canonical 值作为默认显示。需要人工优先级的字段必须在单独的内容编辑契约中处理。

### 6.4 人脸、人物与合并建议

人脸归一化必须独立于内容映射执行，且必须分批、可恢复：

1. 每个重复内容只选择 canonical 来源的一套标准人脸作为有效人脸；检测版本不一致或 canonical 检测不完整时，先只对 canonical 重新检测一次。
2. 建立 `face_content_members`（或等价映射）记录副本 Face 到 canonical Face 的关系、匹配证据和状态：`canonical`、`alias`、`unresolved`、`conflict`。
3. 仅 `canonical` Face 进入人物照片关系、人物 profile、头像候选、计数和合并建议。`alias` 和历史 Face 保留但不重复贡献。
4. 同内容同一张脸的非零 `person_id` 一致或只有一边已归属时，可以建立等价关系；若归属不同，尤其任一方 `manual_locked=true`，不得覆盖、不得自动合并人物，必须标为 `conflict`。
5. 对每批受影响人物，使用现有 identity-profile dirty/rebuild 机制局部重建；同时标记人物合并建议 dirty 并在后台受控重算。旧建议不作为新事实继续展示。

`person_photos` 目前以 `(person_id, photo_id)` 表达来源级关联。内容级语义应新增或派生 `(person_id, content_id)` 关系，供人物图片数、人物详情和画像输入使用；保留原表与原触发器作为历史兼容层，直到内容级读取长期稳定。

### 6.5 事件、展示与统计

| 范围 | 内容级修改 |
|---|---|
| 照片列表、搜索、相邻照片、计数 | 使用 canonical-content scope，不能用任意 `GROUP BY` 破坏排序和分页 |
| 标签/分类统计 | 仅聚合 canonical 内容，副本不提高标签热度和照片总数 |
| 展示策略 | 候选池、近期避重与展示记录按 `content_id` 判断；历史 `photo_id` 记录在读取时解析到内容 |
| 事件策展 | 同内容最多提名一次，避免副本提高事件权重 |
| 事件聚类与封面 | canonical 内容作为有效成员；副本分属不同事件时记录事件冲突并局部重聚类，不自动合并事件 |

所有直接按 `Photo.ID` 排除或计数的查询都要进行一次审计。推荐在 Repository 层新增 canonical-content 查询 scope，而不是在每个 Handler 或前端临时去重。

## 7. API 与产品语义

- `GET /photos` 默认返回每个内容一项，并带 `content_id`、`canonical_photo_id`、`source_count`、`available_source_count`。
- `GET /photos/:id` 接到副本 Photo ID 时，返回所属 canonical 内容及请求来源信息；不让旧链接失效。
- 内容级操作（排除、AI 重试、人脸重检、GPS 重算、展示预览）作用于 `Content ID`；API 可接受旧 Photo ID 并由服务端解析。
- 照片详情默认只显示主来源，提供“副本路径（N）”只读区，可查看各来源可用性和基础文件信息。
- 不提供任何物理文件删除、移动或重命名入口。

## 8. 分期上线、监控与回滚

### Phase 0：只读基线

完成备份、审计报告、数据量评估与性能基线；不修改业务读取。

### Phase 1：模式与内容映射

上线新增表/字段、ContentResolver、回填任务和功能开关。完成 `content_id` 回填及校验，但读取路径仍保留旧行为。

### Phase 2：新扫描与任务去重

新文件扫描使用 ContentTaskDispatcher；验证同一 SHA-256 的新路径只产生一份任务与派生结果。

### Phase 3：内容级读取

逐项启用照片列表/统计、缩略图与文件读取、AI/GPS、展示、事件候选的 canonical-content scope。每一项独立开关、独立观测。

### Phase 4：人物派生修复

分批建立人脸映射、内容级人物关联，局部重建受影响画像与合并建议；人工冲突只进入审计队列。

### Phase 5：稳定观察

持续观察一段运行周期，确认任务抑制、路径故障切换、人物与事件指标稳定。物理文件和副本索引仍全部保留；本方案没有“最终删除”阶段。

### 回滚

- 任一读取阶段异常：关闭对应内容级读取开关，回到旧 Photo ID 查询；
- 任务阶段异常：停止新 dispatcher 入队，保留已完成内容映射和来源索引；
- 人物阶段异常：停止后续批次和 profile rebuild，保留 face mapping/冲突记录；旧 Face 与原始归属均未删除；
- 不通过回滚删除新增内容映射或历史数据。数据结构始终为附加式，以便复核与再次尝试。

## 9. 验收与测试

### 核心验收

1. 两个不同路径、相同 SHA-256 的文件只产生一个 `PhotoContent`，物理文件均未被修改。
2. 新副本扫描不产生第二份缩略图、AI、GPS、People Job 或任务锁；并发扫描同一内容时也成立。
3. canonical 路径缺失后，缩略图、AI 重试、人脸读取和展示能从同组可用路径继续工作。
4. 默认照片列表、搜索、统计、标签热度、展示候选、事件候选和人物照片数均按内容去重。
5. 历史副本的 AI、GPS、Face、展示记录没有被删除；只有一份结果被标为业务有效。
6. 同一内容的人脸跨人物冲突不会自动改写人工锁定、不自动合并人物，并且不会继续污染 profile/merge suggestion。
7. 回填中断后可从检查点重跑；重复运行不产生第二个内容行、第二个 canonical 或重复人物贡献。
8. 关闭任意功能开关后，旧 Photo ID 查询和既有 API 仍可工作。

### 测试层次

- Model/Repository：哈希唯一性、canonical 排序、来源 fallback、内容级 scope、分页稳定性；
- Scan/Task：首次发现、重复发现、并发发现、任务进行中、任务失败重试、路径失效；
- Derived data：AI/GPS/thumbnail 复用、canonical 缺结果时的受控补齐；
- People：同人物 alias、不一致自动归属、人工锁定冲突、profile dirty/rebuild、合并建议刷新；
- Event/Display：候选去重、历史避重、事件冲突与封面解析；
- Migration：空哈希跳过、断点恢复、幂等、只添加不删除、备份恢复演练；
- 性能：大重复组、游标分页、SQLite 写入批次与锁等待监控。

## 10. 实施顺序与相关文件

实际实施需在单独的 implementation plan 中细化并按 TDD 执行。预期首批涉及：

- `backend/internal/model/photo.go`：内容/来源模型与 API DTO；
- `backend/pkg/database/database.go`：新增表、索引、迁移与内容级派生关系；
- `backend/internal/repository/photo_repo.go`：canonical-content scope、分页与候选查询；
- `backend/internal/service/photo_scan_service.go`：扫描归组、来源状态和内容级入队；
- `backend/internal/service/thumbnail_service.go`、`ai_service.go`、地理编码及 People 任务服务：统一使用 ContentResolver 与内容级任务身份；
- `backend/internal/service/people_service.go`、identity profile、merge suggestion 相关服务：有效人脸和内容级人物贡献；
- `backend/internal/service/display_service.go`、`display_daily_service.go`、事件仓库/服务：内容级候选和避重；
- `frontend/src/types/photo.ts`、照片列表及详情：展示内容与副本来源摘要。

在开始编码前，必须补充精确表名、迁移 SQL、接口契约、每个任务表的去重约束和逐文件测试清单；不得以本文直接修改生产数据库。
