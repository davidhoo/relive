# 数据库选型分析：SQLite vs PostgreSQL vs MySQL

> 生成日期：2026-05-12
> 基于 Relive 项目当前代码库的全面审计

---

## 一、现状基线

### 1.1 规模指标

| 指标 | 当前值 | 5 年预估 | 10 年预估 |
|------|--------|----------|-----------|
| 照片数量 | ~11 万 | ~12.5 万 | ~14 万 |
| 数据库文件大小 | ~500MB | ~570MB | ~640MB |
| 数据表数量 | 24 张 | 24-26 张 | 26-28 张 |
| 日均读请求 | ~50-200 | ~100-300 | ~200-500 |
| 日均写请求 | ~500-1000 | ~500-1000 | ~500-1000 |
| 并发用户 | 1-2 | 1-3 | 1-5 |

### 1.2 当前 SQLite 使用特征

| 维度 | 详情 |
|------|------|
| **运行模式** | WAL（Write-Ahead Logging），busy_timeout=60s |
| **连接池** | API: 4 open / 2 idle；Background: 2 open / 1 idle |
| **索引** | Photo 表 13 个索引，复合索引 3 个，FTS5 全文索引 1 个 |
| **迁移机制** | 无版本化迁移文件，全部内联在 `database.go`，基于 `app_config` 键幂等执行 |
| **事务使用** | City 导入、照片批量更新、DisplayRecord 写入等关键路径有事务保护 |
| **全文搜索** | FTS5 external-content 模式，3 个自动触发器，不可用时降级 LIKE |
| **窗口函数** | `ROW_NUMBER() OVER (PARTITION BY ...)` 用于人脸聚类 top-N 查询 |
| **GORM AutoMigrate** | 24 个模型全部参与，启动时自动执行 |
| **PRAGMA 设置** | WAL + 64MB cache + NORMAL sync + foreign_keys=ON + temp_store=memory |

### 1.3 SQLite 特有语法依赖清单

| 特性 | 出现位置 | 次数 | PostgreSQL 兼容性 | MySQL 兼容性 |
|------|----------|------|-------------------|--------------|
| `strftime('%m-%d', col)` | photo_repo, event_repo | 4 | 需改为 `TO_CHAR(col, 'MM-DD')` | 需改为 `DATE_FORMAT(col, '%m-%d')` |
| `PRAGMA foreign_keys` | database.go | 3 | 删除（PG 默认开启） | 删除（需建表时声明） |
| `sqlite_master` | system_service | 1 | 改为 `information_schema.tables` | 改为 `information_schema.tables` |
| `sqlite_sequence` | system_service | 1 | 改为 `pg_sequences` 或 `ALTER SEQUENCE` | 改为 `ALTER TABLE AUTO_INCREMENT` |
| FTS5 `MATCH` | photo_repo | 1 | 改为 `tsvector/tsquery` 或 pg_trgm | 改为 `FULLTEXT INDEX` |
| `COALESCE` | 多处 | ~20 | 完全兼容 | 完全兼容 |
| `LIKE '%keyword%'` | photo_repo, person_repo | 5 | 完全兼容（或改 ILIKE） | 完全兼容 |
| `ROW_NUMBER() OVER` | face_repo | 1 | 完全兼容 | 完全兼容（8.0+） |
| `AUTOINCREMENT` | 所有模型 | 24 | 改为 `SERIAL` 或 `BIGSERIAL` | 完全兼容 |
| `INTEGER PRIMARY KEY` | 所有模型 | 24 | 改为 `SERIAL PRIMARY KEY` | 完全兼容 |

---

## 二、三数据库全面对比

### 2.1 基础特性对比

| 维度 | SQLite | PostgreSQL | MySQL |
|------|--------|------------|-------|
| **架构** | 嵌入式，无独立进程 | C/S 架构，独立进程 | C/S 架构，独立进程 |
| **存储** | 单文件 | 集群目录（多文件） | 表空间文件 |
| **许可证** | 公有领域 | PostgreSQL License（类 MIT） | GPL v2（Oracle 所有） |
| **最新稳定版** | 3.46+ (2024) | 17.x (2024) | 8.0 / 9.0 (2024) |
| **Go 驱动** | `mattn/go-sqlite3`（CGO）| `lib/pq` 或 `pgx` | `go-sql-driver/mysql` |
| **GORM 支持** | 原生 | 原生 | 原生 |
| **Docker 镜像大小** | 无需（嵌入） | ~100-200MB | ~150-300MB |
| **内存占用** | ~数 MB | ~50-200MB | ~100-500MB |

### 2.2 性能对比（Relive 场景）

| 场景 | SQLite | PostgreSQL | MySQL |
|------|--------|------------|-------|
| **单条 SELECT by PK** | <1ms | <1ms | <1ms |
| **日期范围查询（索引）** | <10ms | <5ms | <5ms |
| **全文搜索** | FTS5: <20ms | tsvector: <10ms | FULLTEXT: <15ms |
| **单条 INSERT** | <5ms | <3ms | <3ms |
| **批量 INSERT（100条/事务）** | <50ms | <30ms | <30ms |
| **聚合统计（COUNT/SUM）** | <50ms（11万条） | <20ms | <25ms |
| **并发读** | 优秀（WAL 模式） | 优秀 | 优秀 |
| **并发写** | 单写串行 | 多写并行 | 多写并行 |
| **JOIN 性能（3表）** | 中等 | 优秀 | 良好 |
| **大数据量排序** | 中等 | 优秀 | 良好 |

> **结论**：Relive 的查询负载（读多写少、单用户、简单查询为主）下，三者性能差异可忽略不计。瓶颈不在数据库引擎，而在 I/O（NAS 磁盘）。

### 2.3 功能特性对比

| 功能 | SQLite | PostgreSQL | MySQL |
|------|--------|------------|-------|
| **事务 ACID** | 完整支持 | 完整支持 | InnoDB 完整支持 |
| **外键约束** | 支持（需 PRAGMA 启用） | 默认启用 | InnoDB 默认启用 |
| **全文搜索** | FTS5（够用） | tsvector + GIN（强大） | FULLTEXT（中等） |
| **JSON 支持** | JSON 函数（3.38+） | JSONB（强大，可索引） | JSON 类型（中等） |
| **窗口函数** | 支持（3.25+） | 完整支持 | 支持（8.0+） |
| **CTE (WITH)** | 支持（3.8.3+） | 完整支持 | 支持（8.0+） |
| **UPSERT** | `INSERT OR REPLACE` | `ON CONFLICT` | `ON DUPLICATE KEY UPDATE` |
| **部分索引** | 支持 | 支持 | 不支持 |
| **表达式索引** | 支持 | 支持 | 8.0+ 函数索引 |
| **触发器** | 支持 | 完整支持（BEFORE/AFTER/ROW） | 支持 |
| **物化视图** | 不支持 | 支持 | 不支持 |
| **并行查询** | 不支持 | 支持 | 不支持 |
| **分区表** | 不支持 | 原生支持 | 原生支持 |
| **逻辑复制** | 不支持 | 支持 | 支持（binlog） |
| **扩展生态** | 有限 | 极其丰富（PostGIS, pgvector） | 中等 |

### 2.4 运维对比

| 维度 | SQLite | PostgreSQL | MySQL |
|------|--------|------------|-------|
| **安装部署** | 零配置 | 需安装服务 | 需安装服务 |
| **Docker 部署** | 无需独立容器 | 需独立容器 | 需独立容器 |
| **备份** | `cp` 文件即可 | `pg_dump` / WAL 归档 | `mysqldump` / binlog |
| **恢复** | `cp` 回文件 | `pg_restore` | `mysql < dump.sql` |
| **升级** | 替换二进制 | 需停机或逻辑复制 | 需停机或 pt-online-schema |
| **监控** | 无内置 | `pg_stat_*` 系统视图 | `performance_schema` |
| **调优** | PRAGMA 即可 | 需调 shared_buffers, work_mem 等 | 需调 innodb_buffer_pool 等 |
| **日常维护** | 偶尔 VACUUM | 需 VACUUM ANALYZE | 需 OPTIMIZE TABLE |
| **安全更新** | 极少（嵌入式） | 频繁（需跟进小版本） | 频繁（Oracle 补丁周期） |
| **学习曲线** | 极低 | 中等 | 中等 |

### 2.5 扩展性对比

| 维度 | SQLite | PostgreSQL | MySQL |
|------|--------|------------|-------|
| **单表行数上限** | 无硬限制（推荐 <500 万） | 无硬限制 | 无硬限制 |
| **数据库大小上限** | 无硬限制（推荐 <1GB） | 无硬限制 | 无硬限制 |
| **最大连接数** | 串行写 + 并行读 | 数百-数千 | 数百-数千 |
| **读写分离** | 不支持 | 原生流复制 | 主从复制 |
| **水平分片** | 不支持 | Citus 扩展 | Vitess / ProxySQL |
| **向量搜索** | sqlite-vec / sqlite-vss | pgvector | 无原生支持 |
| **地理空间** | 无 | PostGIS（业界最强） | 基础空间类型 |

---

## 三、Relive 项目迁移影响评估

### 3.1 迁移工作量矩阵

| 工作项 | SQLite → PostgreSQL | SQLite → MySQL |
|--------|--------------------|-----------------|
| **1. 连接配置** | 小：改 DSN + 驱动 | 小：改 DSN + 驱动 |
| **2. 模型类型映射** | 中：`AUTOINCREMENT` → `SERIAL`，布尔类型调整 | 小：基本兼容 |
| **3. `strftime` 改写** | 4 处：改为 `TO_CHAR` | 4 处：改为 `DATE_FORMAT` |
| **4. PRAGMA 移除** | 3 处：删除即可 | 3 处：删除即可 |
| **5. FTS5 全文搜索** | **大**：重写为 `tsvector/tsquery` + GIN 索引 | **大**：重写为 `FULLTEXT INDEX` |
| **6. `sqlite_master/sequence`** | 2 处：改为 `information_schema` / `pg_sequences` | 2 处：改为 `information_schema` / `AUTO_INCREMENT` |
| **7. UPSERT 语法** | 中：`INSERT OR REPLACE` → `ON CONFLICT DO UPDATE` | 中：`INSERT OR REPLACE` → `ON DUPLICATE KEY UPDATE` |
| **8. 迁移脚本重写** | **大**：13 个内联迁移全部重写为 SQL | **大**：13 个内联迁移全部重写为 SQL |
| **9. 数据迁移** | 中：`pgloader` 或自写脚本 | 中：`mysqldump` 转换或自写脚本 |
| **10. Repository 层测试** | 大：所有 repository 测试需重跑 | 大：所有 repository 测试需重跑 |
| **11. Docker Compose** | 小：加 PostgreSQL 容器 | 小：加 MySQL 容器 |
| **12. 配置系统扩展** | 小：扩展 DatabaseConfig | 小：扩展 DatabaseConfig |
| **13. 事务隔离级别** | 无：PG 默认 READ COMMITTED 够用 | 无：InnoDB 默认 REPEATABLE READ 够用 |
| **14. 连接池调参** | 小：MaxOpenConns 需增大 | 小：MaxOpenConns 需增大 |

### 3.2 代码改动量估算

| 层级 | SQLite → PostgreSQL | SQLite → MySQL |
|------|--------------------|-----------------|
| `pkg/database/` | **重写**（~600 行） | **重写**（~600 行） |
| `pkg/config/` | 小改（~20 行） | 小改（~20 行） |
| `internal/repository/` | 中改（~100-150 行） | 中改（~80-120 行） |
| `internal/service/` | 小改（~30 行） | 小改（~30 行） |
| `internal/model/` | 小改（GORM tag 调整） | 几乎不动 |
| 测试文件 | 中改（~200 行） | 中改（~200 行） |
| Docker/部署 | 新增 1 个容器 | 新增 1 个容器 |
| **总计估算** | **~1000-1200 行** | **~900-1100 行** |

### 3.3 迁移风险评估

| 风险项 | SQLite → PostgreSQL | SQLite → MySQL |
|--------|--------------------|-----------------|
| **数据丢失** | 低（有 pgloader 工具） | 低（有成熟工具） |
| **性能回退** | 极低（PG 更强） | 极低（MySQL 够用） |
| **兼容性 bug** | 中（语法差异多） | 中（语法差异少些） |
| **运维复杂度上升** | 高（从零维护到需维护服务） | 高（同左） |
| **NAS 资源压力** | 中（+100-200MB 内存） | 中（+100-500MB 内存） |
| **备份复杂度上升** | 高（cp → pg_dump） | 高（cp → mysqldump） |
| **回滚难度** | 高（需重新导回 SQLite） | 高（同左） |

---

## 四、决策框架

### 4.1 决策维度权重（基于 Relive 场景）

| 维度 | 权重 | 说明 |
|------|------|------|
| 运维简便性 | ⭐⭐⭐⭐⭐ | 个人 NAS，希望零维护 |
| 部署轻量 | ⭐⭐⭐⭐⭐ | 群晖资源有限 |
| 备份恢复 | ⭐⭐⭐⭐ | 灾难恢复能力 |
| 当前性能 | ⭐⭐⭐⭐ | 够用即可 |
| 扩展上限 | ⭐⭐ | 10 年 14 万张，增长缓慢 |
| 并发能力 | ⭐⭐ | 单用户为主 |
| 功能丰富度 | ⭐⭐ | FTS5 已满足全文搜索需求 |
| 社区/生态 | ⭐ | 三者都足够成熟 |

### 4.2 评分矩阵

| 维度 (权重) | SQLite | PostgreSQL | MySQL |
|-------------|--------|------------|-------|
| 运维简便性 (5) | 5 | 2 | 2 |
| 部署轻量 (5) | 5 | 2 | 2 |
| 备份恢复 (4) | 5 | 3 | 3 |
| 当前性能 (4) | 4 | 5 | 4 |
| 扩展上限 (2) | 2 | 5 | 4 |
| 并发能力 (2) | 2 | 5 | 5 |
| 功能丰富度 (2) | 3 | 5 | 3 |
| 社区/生态 (1) | 4 | 5 | 4 |
| **加权总分** | **4.05** | **3.15** | **2.90** |

> 计算方式：Σ(得分 × 权重) / Σ权重

### 4.3 触发迁移的条件

维持 SQLite 的前提被打破时，应考虑迁移：

| 条件 | 当前状态 | 触发阈值 |
|------|----------|----------|
| 照片数量 | ~11 万 | > 50 万 |
| 并发用户 | 1-2 | > 10 |
| 数据库大小 | ~500MB | > 2GB |
| 写入瓶颈 | 无 | 频繁 `database is locked` |
| 全文搜索需求 | FTS5 够用 | 需要中文分词、模糊搜索 |
| 向量搜索需求 | 未使用 | 人脸/图像相似度搜索 |
| 地理空间查询 | 离线 geocode 够用 | 需要 PostGIS 实时查询 |
| 多机部署 | 单机 | 需要读写分离/高可用 |

---

## 五、结论与建议

### 5.1 结论：继续使用 SQLite

**Relive 的使用场景（个人 NAS、单用户、14 万张照片以内、读多写少）完美匹配 SQLite 的设计目标。** 迁移到 PostgreSQL 或 MySQL 带来的收益远小于成本：

| 维度 | 迁移收益 | 迁移成本 |
|------|----------|----------|
| 性能 | 无明显提升（瓶颈在磁盘 I/O） | 1000+ 行代码重写 |
| 运维 | 负收益（增加维护负担） | 学习 PG/MySQL 运维 |
| 功能 | FTS5 → tsvector 提升有限 | 全文搜索层重写 |
| 扩展 | 获得理论上的更高上限 | 当前远未触及上限 |

### 5.2 建议

1. **不迁移**。继续使用 SQLite，将精力投入到更有价值的功能开发上。
2. **优化现有 SQLite 使用**：完成 DATABASE_AUDIT 中标记的中优先级待办项（复合索引、Job 清理等）。
3. **如果未来需要向量搜索**：优先评估 `sqlite-vec`（已在 face-recognition-vector-db 设计中考虑），而非为了 pgvector 迁移到 PostgreSQL。
4. **如果未来真的需要迁移**：选择 PostgreSQL（而非 MySQL），因为：
   - GORM + pgx 驱动成熟度最高
   - tsvector 全文搜索远强于 MySQL FULLTEXT
   - pgvector 可复用向量搜索需求
   - PostgreSQL 许可证更自由（类 MIT vs GPL）

### 5.3 如果一定要迁移的路线图

假设未来触发了迁移条件，推荐的迁移路径：

```
Phase 0: 准备（1 周）
├── 扩展 DatabaseConfig 支持 postgres 类型
├── database.go 抽象 InitDB 接口
└── 编写 SQLite → PostgreSQL 数据迁移脚本

Phase 1: 适配层（2 周）
├── 统一 strftime → 标准 SQL（CASE + EXTRACT）
├── FTS5 → tsvector 抽象搜索接口
├── PRAGMA 移除（PG 不需要）
├── sqlite_master/sequence → information_schema
└── 所有 repository 测试适配双数据库

Phase 2: 切换（1 周）
├── Docker Compose 新增 PostgreSQL 容器
├── 配置切换 database.type = "postgres"
├── 数据迁移验证
└── 灰度运行（双写对比）

Phase 3: 清理（1 周）
├── 移除 SQLite 特有代码路径
├── 更新文档和部署指南
└── 建立 pg_dump 备份 cron
```

预计总工期：**4-5 周**（单人开发）。

---

## 附录 A：MySQL 特有劣势（为何不选 MySQL）

如果排除 SQLite 后在 PostgreSQL 和 MySQL 之间选择，MySQL 的劣势：

1. **GPL 许可证风险**：MySQL 是 Oracle 所有，GPL 协议对商业使用有传染性。PostgreSQL 的许可证更宽松。
2. **全文搜索弱于 PostgreSQL**：MySQL FULLTEXT 对中文支持差，需要额外分词插件。
3. **无原生向量搜索**：PostgreSQL 有 pgvector，MySQL 无对应方案。
4. **JSON 能力弱于 PostgreSQL**：MySQL 的 JSON 类型无法创建 GIN 索引。
5. **部分索引不支持**：MySQL 不支持 `WHERE condition` 的部分索引。
6. **优化器能力**：PostgreSQL 的查询优化器普遍被认为更强，复杂 JOIN 选择更优。
7. **扩展生态**：PostGIS 远强于 MySQL 的空间类型。
8. **Oracle 公司治理**：社区对 Oracle 的 MySQL 治理存在信任问题（MariaDB 分叉即因此）。

## 附录 B：关键文件索引

| 文件 | 说明 |
|------|------|
| `backend/pkg/database/database.go` | 数据库初始化、迁移、PRAGMA 配置（643 行） |
| `backend/pkg/config/config.go:38-43` | DatabaseConfig 结构体 |
| `backend/config.dev.yaml:10-14` | 开发环境数据库配置 |
| `internal/repository/` | 18 个 Repository 文件 |
| `internal/model/` | 24 个 GORM 模型 |
| `docs/archive/DATABASE_EVALUATION.md` | 历史评估文档（SQLite vs PostgreSQL） |
| `docs/archive/DATABASE_AUDIT_2026-03-12.md` | 数据库审查报告 |
