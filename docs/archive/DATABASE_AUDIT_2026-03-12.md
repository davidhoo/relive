# 数据库审查报告 (2026-03-12)

> 对 Relive 项目数据库层（模型定义、Repository、Service 数据访问、迁移机制）的全面审查

---

## 审查范围

- 14 个 GORM 模型（含 `ResultQueueItem`）
- `database.go` 初始化与 AutoMigrate
- 全部 Repository 实现
- Service 层中的直接 DB 操作
- 原始 SQL 使用情况
- 索引、外键、事务、错误处理

---

## 已完成的修复

### 1. OverallScore 计算公式统一

**问题**: 公式 `int(memory*0.7 + beauty*0.3)` 分散在 6 处（photo.go Hook、photo_repo.go、ai_service.go x2、analysis_service.go、batch_processor.go、analyzer.go），修改时极易遗漏。

**修复**: 新增 `model.CalcOverallScore(memoryScore, beautyScore int) int` 包级函数，所有调用处统一引用。

**涉及文件**:
- `internal/model/photo.go` — 新增函数，`CalculateOverallScore()` 方法改为调用它
- `internal/repository/photo_repo.go` — `MarkAsAnalyzed`
- `internal/service/ai_service.go` — 2 处
- `internal/service/analysis_service.go` — `SubmitResultsDirectly`
- `internal/service/batch_processor.go` — `processBatch`
- `internal/analyzer/analyzer.go` — `saveResult`
- `internal/repository/photo_repo_test.go` — 测试断言

### 2. City 导入加事务保护

**问题**: `config_handler.go` 和 `cmd/import-cities/main.go` 中先 `DELETE FROM cities` 再分批 INSERT，无事务包裹。中途失败会导致 cities 表数据残缺或清空。

**修复**: 改为先解析全部数据到内存，再在 `db.Transaction()` 中执行 DELETE + 分批 INSERT。任何环节失败自动回滚。

**涉及文件**:
- `internal/api/v1/handler/config_handler.go` — `importCitiesFromFile`
- `cmd/import-cities/main.go` — main 函数

### 3. ListDailyBatches N+1 查询优化

**问题**: 先查 N 个 batch，再循环调用 `loadDailyBatchByID`（每次 3 个 Preload），产生 `1 + N*4` 次查询。

**修复**: 改为单次 `Find` 带 3 个 Preload（Items、Items.Photo、Items.Assets），GORM 内部发 4 次 `WHERE ... IN (...)` 查询，与 N 无关。

**额外修复**: `loadDailyBatchByID` 中 `normalizeBatchDateString` 被重复调用了两次，删除多余的一行。

**涉及文件**:
- `internal/service/display_daily_service.go`

### 4. batchUpdatePhotos 手工 SQL 改为参数化查询

**问题**: 使用字符串拼接构建 `UPDATE ... CASE WHEN` SQL，仅用 `escapeSQL`（替换单引号）做转义，存在 SQL 注入风险。

**修复**: 改为事务内逐条 `tx.Model().Where().Updates(map)` — GORM 自动参数化。批量大小受 API 限制（最多 50 条），逐条更新性能无影响。同时删除了不再需要的 `escapeSQL` 函数。

**涉及文件**:
- `internal/service/analysis_service.go`

### 5. ResultQueueItem 纳入主 AutoMigrate

**问题**: `ResultQueueItem` 模型定义在 `repository` 包中，未注册到 `database.go` 的主 AutoMigrate 列表，依赖 `service.go` 中单独调用 `MigrateResultQueue(db)`。如果调用链有遗漏，会导致表不存在。

**修复**:
- 将 `ResultQueueItem` 结构定义从 `repository` 包移至 `model` 包（与所有其他模型一致）
- 在 `database.go` 的 AutoMigrate 列表中添加 `&model.ResultQueueItem{}`
- 删除 `service.go` 中单独的 `MigrateResultQueue(db)` 调用
- 更新测试文件引用

**涉及文件**:
- `internal/model/models.go` — 新增模型定义
- `pkg/database/database.go` — AutoMigrate 列表
- `internal/repository/result_queue_repo.go` — 删除旧定义，引用 model 包
- `internal/service/service.go` — 删除单独迁移调用
- `internal/service/photo_service_test.go` — 更新引用
- `internal/api/v1/handler/system_test.go` — 更新引用

---

## 待优化项（中优先级）

### 6. 缺少复合索引

**影响**: 频繁查询使用多列条件，但只有单列索引，数据量大时性能下降。

| 查询场景 | 涉及字段 | 建议索引 |
|---|---|---|
| `WasDisplayedRecently` | `photo_id, device_id, displayed_at` | 复合索引 |
| `ClaimNextJob` (Thumbnail/Geocode) | `status, priority, queued_at` | 复合索引 |
| City 地理查询 | `latitude, longitude` | 考虑 R-Tree 虚拟表 |

**改动**: 在模型 GORM tag 中添加复合索引，AutoMigrate 自动执行。涉及 DB schema 变更。

### 7. Photo 搜索全表扫描

**影响**: `photo_repo.go` 的 `search` 过滤在 7 个字段上做 `LIKE`（file_path, file_name, main_category, tags, description, caption, location），大数据量时性能堪忧。

**建议**: 引入 SQLite FTS5 全文检索扩展。改动较大，需评估是否值得。

### 8. ThumbnailJob / GeocodeJob 无清理机制

**影响**: 已完成/取消的任务永久保留，表无限增长。

**建议**: 增加定期清理逻辑（如保留最近 7 天的已完成记录），或在任务完成时直接删除。

### 9. 枚举字段无 CHECK 约束

**影响**: `ThumbnailStatus`、`GeocodeStatus`、`DeviceType`、各 Job `Status` 等均为 varchar，无数据库层面约束，旁路写入可能产生非法值。

**建议**: 通过 GORM 的 `AfterMigrate` 或手动 SQL 添加 CHECK 约束。仅靠应用层校验目前也可接受。

### 10. SystemHandler.Stats 静默忽略错误

**影响**: `system.go` 中所有 `Count`/`Select` 调用均未检查 error 返回值，统计数据可能为零而不报错。

**建议**: 添加错误检查并记录日志，小改动。

---

## 待优化项（低优先级）

### 11. Tags 逗号分隔文本存储

`Photo.Tags` 为 `type:text`，`GetTags()` 需加载所有照片的 tags 字段到内存解析。规范做法是 photo_tags 关联表，但当前数据规模下可接受。

### 12. Photo 硬/软删除混用

`Delete` 用 `Unscoped().Delete`（硬删除），`SoftDeleteByPathPrefix` 用软删除。硬删除时如果存在 DisplayRecord 引用，SQLite FK 约束会报错。应统一策略或先清理关联记录。

### 13. GORM Hook 在 map Updates 时不触发

`MarkAsAnalyzed` 使用 `Updates(map[string]interface{}{...})`，不触发 `BeforeUpdate` Hook。代码中已手动补偿（现在通过 `CalcOverallScore` 统一），但如果 Hook 中新增其他逻辑需注意此问题。

### 14. ScanJob 字符串主键

ScanJob 用 `varchar(64)` 字符串主键（UUID），其余所有模型用 `uint` 自增。功能上无问题，风格不统一。

### 15. 多个 Service 绕过 Repository 层

`analysis_service`、`display_daily_service`、`thumbnail_service`、`geocode_task_service`、`analysis_runtime_service`、`SystemHandler` 均直接使用 `*gorm.DB`。导致 Photo 的更新逻辑分散，Repository 接口不完整。属于架构层面重构，工作量大。

---

## 数据库配置评价（良好，无需改动）

- SQLite WAL 模式 + 64MB cache + NORMAL sync — 适合单机场景
- 连接池 `MaxOpenConns=4` — 适合 SQLite 串行写入
- 外键已启用（`PRAGMA foreign_keys=ON`），迁移时不禁用
- 时间统一 UTC 存储（`NowFunc: time.Now().UTC()`）
- Photo 表索引覆盖全面（13 个索引）
- 事务在关键操作中使用恰当
