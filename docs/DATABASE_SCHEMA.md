# Relive 数据库设计文档

> 详细的数据库表结构设计
> 数据库：SQLite（可平滑迁移到 PostgreSQL）
> ORM：GORM
> 参考：InkTime 项目数据库设计

---

## 一、设计原则

### 1.1 核心原则
- ✅ **标准 SQL**：兼容 SQLite 和 PostgreSQL
- ✅ **适度冗余**：性能优先，允许合理冗余
- ✅ **索引优化**：为高频查询建立索引
- ✅ **扩展性**：预留未来功能字段

### 1.2 命名规范
- **表名**：小写+下划线，复数形式（photos, tags）
- **字段名**：小写+下划线（snake_case）
- **主键**：统一使用 `id`
- **时间戳**：`created_at`, `updated_at`
- **软删除**：`deleted_at`（可选）

### 1.3 数据类型映射

| 逻辑类型 | SQLite | PostgreSQL | GORM 类型 |
|---------|--------|------------|----------|
| 整数 | INTEGER | INTEGER | int |
| 长整数 | INTEGER | BIGINT | int64 |
| 浮点数 | REAL | DOUBLE PRECISION | float64 |
| 字符串 | TEXT | VARCHAR(n) | string |
| 长文本 | TEXT | TEXT | string |
| 布尔值 | INTEGER | BOOLEAN | bool |
| 时间 | TEXT | TIMESTAMP | time.Time |
| JSON | TEXT | JSONB | string |

---

## 二、核心表设计

### 2.1 photos（照片主表）⭐

**用途**：存储照片的所有信息，包括文件信息、EXIF、AI 分析结果、评分等

```sql
CREATE TABLE photos (
    -- 主键
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- 文件基础信息
    file_path TEXT NOT NULL UNIQUE,          -- 文件完整路径（唯一）
    file_name TEXT NOT NULL,                 -- 文件名
    file_size INTEGER,                       -- 文件大小（字节）
    file_hash TEXT,                          -- 文件 MD5 哈希（去重）

    -- 图片尺寸信息
    width INTEGER NOT NULL,                  -- 图片宽度
    height INTEGER NOT NULL,                 -- 图片高度
    orientation INTEGER DEFAULT 1,           -- 方向（1-8）

    -- EXIF 基础信息
    exif_datetime DATETIME,                  -- 拍摄时间（最重要）⭐
    exif_make TEXT,                          -- 相机品牌
    exif_model TEXT,                         -- 相机型号

    -- EXIF 拍摄参数
    exif_iso INTEGER,                        -- ISO 感光度
    exif_exposure_time TEXT,                 -- 快门速度（如 "1/125"）
    exif_f_number REAL,                      -- 光圈值（如 2.8）
    exif_focal_length REAL,                  -- 焦距（如 50.0）
    exif_flash INTEGER DEFAULT 0,            -- 闪光灯（0/1）

    -- EXIF GPS 信息
    exif_gps_lat REAL,                       -- GPS 纬度
    exif_gps_lon REAL,                       -- GPS 经度
    exif_gps_alt REAL,                       -- GPS 高度
    exif_city TEXT,                          -- 反查的城市名称

    -- EXIF 软件信息
    exif_software TEXT,                      -- 软件信息（识别截图）
    exif_json TEXT,                          -- 完整 EXIF 的 JSON
    exif_available BOOLEAN DEFAULT TRUE,     -- EXIF 是否可用

    -- AI 分析结果
    caption TEXT,                            -- 详细描述（80-200字）
    side_caption TEXT,                       -- 短文案（8-30字）
    category TEXT,                           -- 主分类
    type TEXT,                               -- 类型标签（可多个，逗号分隔）

    -- 评分
    memory_score REAL DEFAULT 0.0,           -- 回忆价值评分（0-100）
    beauty_score REAL DEFAULT 0.0,           -- 美观度评分（0-100）
    display_score REAL DEFAULT 0.0,          -- 综合展示评分（计算值）
    reason TEXT,                             -- 评分理由（≤40字）

    -- 分析状态
    analyzed BOOLEAN DEFAULT FALSE,          -- 是否已分析
    analyzed_at DATETIME,                    -- 分析时间
    analysis_error TEXT,                     -- 分析错误信息
    raw_json TEXT,                           -- AI 原始返回 JSON

    -- 文件状态
    file_missing BOOLEAN DEFAULT FALSE,      -- 文件是否丢失
    file_missing_at DATETIME,                -- 丢失检测时间

    -- 时间戳
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,

    -- 索引
    INDEX idx_file_path (file_path),
    INDEX idx_file_hash (file_hash),
    INDEX idx_exif_datetime (exif_datetime),
    INDEX idx_exif_city (exif_city),
    INDEX idx_category (category),
    INDEX idx_memory_score (memory_score),
    INDEX idx_display_score (display_score),
    INDEX idx_analyzed (analyzed),
    INDEX idx_file_missing (file_missing),
    INDEX idx_gps (exif_gps_lat, exif_gps_lon)
);
```

**GORM 模型**：
```go
type Photo struct {
    ID       uint      `gorm:"primaryKey"`

    // 文件信息
    FilePath string    `gorm:"uniqueIndex;not null"`
    FileName string    `gorm:"not null"`
    FileSize int64
    FileHash string    `gorm:"index"`

    // 尺寸
    Width       int  `gorm:"not null"`
    Height      int  `gorm:"not null"`
    Orientation int  `gorm:"default:1"`

    // EXIF 基础
    ExifDatetime *time.Time `gorm:"index"`
    ExifMake     string
    ExifModel    string

    // EXIF 拍摄参数
    ExifISO          int
    ExifExposureTime string
    ExifFNumber      float64
    ExifFocalLength  float64
    ExifFlash        int `gorm:"default:0"`

    // EXIF GPS
    ExifGPSLat  *float64
    ExifGPSLon  *float64
    ExifGPSAlt  *float64
    ExifCity    string   `gorm:"index"`

    // EXIF 其他
    ExifSoftware   string
    ExifJSON       string `gorm:"type:text"`
    ExifAvailable  bool   `gorm:"default:true"`

    // AI 分析
    Caption     string `gorm:"type:text"`
    SideCaption string
    Category    string `gorm:"index"`
    Type        string

    // 评分
    MemoryScore  float64 `gorm:"index;default:0"`
    BeautyScore  float64 `gorm:"default:0"`
    DisplayScore float64 `gorm:"index;default:0"`
    Reason       string

    // 分析状态
    Analyzed      bool       `gorm:"index;default:false"`
    AnalyzedAt    *time.Time
    AnalysisError string     `gorm:"type:text"`
    RawJSON       string     `gorm:"type:text"`

    // 文件状态
    FileMissing   bool       `gorm:"index;default:false"`
    FileMissingAt *time.Time

    // 时间戳
    CreatedAt time.Time
    UpdatedAt time.Time

    // 关联
    Tags            []Tag            `gorm:"many2many:photo_tags;"`
    DisplayHistory  []DisplayHistory `gorm:"foreignKey:PhotoID"`
}
```

---

### 2.2 tags（标签表）

**用途**：存储所有标签（事件、情绪、季节等）

```sql
CREATE TABLE tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,               -- 标签名称
    category TEXT,                           -- 标签分类（event/emotion/season/time）
    description TEXT,                        -- 标签描述
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,

    INDEX idx_name (name),
    INDEX idx_category (category)
);
```

**GORM 模型**：
```go
type Tag struct {
    ID          uint   `gorm:"primaryKey"`
    Name        string `gorm:"uniqueIndex;not null"`
    Category    string `gorm:"index"` // event/emotion/season/time
    Description string
    CreatedAt   time.Time

    // 关联
    Photos []Photo `gorm:"many2many:photo_tags;"`
}
```

---

### 2.3 photo_tags（照片标签关联表）

**用途**：多对多关系，一张照片可以有多个标签

```sql
CREATE TABLE photo_tags (
    photo_id INTEGER NOT NULL,
    tag_id INTEGER NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,

    PRIMARY KEY (photo_id, tag_id),
    FOREIGN KEY (photo_id) REFERENCES photos(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE,

    INDEX idx_photo_id (photo_id),
    INDEX idx_tag_id (tag_id)
);
```

**GORM 模型**：
```go
// GORM 自动处理，无需单独定义模型
```

---

### 2.4 display_history（展示历史表）

**用途**：记录照片在墨水屏上的展示历史

```sql
CREATE TABLE display_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    photo_id INTEGER NOT NULL,               -- 照片 ID
    device_id TEXT,                          -- 设备标识（支持多设备）
    displayed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    display_reason TEXT,                     -- 展示原因
    display_score REAL,                      -- 展示时的评分

    FOREIGN KEY (photo_id) REFERENCES photos(id) ON DELETE CASCADE,

    INDEX idx_photo_id (photo_id),
    INDEX idx_displayed_at (displayed_at),
    INDEX idx_device_id (device_id),
    INDEX idx_display_reason (display_reason)
);
```

**display_reason 取值**：
- `on_this_day` - 往年今日（±3天）
- `on_this_week` - 往年本周（±7天）
- `on_this_month` - 往年本月
- `high_score` - 年度高分照片
- `random` - 随机选择

**GORM 模型**：
```go
type DisplayHistory struct {
    ID            uint      `gorm:"primaryKey"`
    PhotoID       uint      `gorm:"index;not null"`
    DeviceID      string    `gorm:"index"`
    DisplayedAt   time.Time `gorm:"index;not null"`
    DisplayReason string    `gorm:"index"`
    DisplayScore  float64

    // 关联
    Photo Photo `gorm:"foreignKey:PhotoID"`
}
```

---

### 2.5 devices（设备表）⭐

**用途**：存储设备信息（电子相框、移动端、Web浏览器等）

```sql
CREATE TABLE devices (
    -- 主键
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    -- 设备信息
    device_id TEXT NOT NULL UNIQUE,          -- 设备唯一ID（如MAC地址）
    name TEXT NOT NULL,                       -- 设备名称
    api_key TEXT NOT NULL UNIQUE,             -- API Key
    ip_address TEXT,                          -- IP地址

    -- 设备类型
    device_type TEXT DEFAULT 'embedded',      -- 设备类型（embedded/mobile/web/offline/service）

    -- 描述/备注
    description TEXT,                         -- 设备描述

    -- 状态信息
    is_enabled BOOLEAN DEFAULT TRUE,          -- 是否可用（服务端控制）
    online BOOLEAN DEFAULT FALSE,             -- 是否在线（自动检测）
    last_heartbeat DATETIME,                  -- 最近活跃时间（历史字段名保留）
    battery_level INTEGER DEFAULT 0,          -- 电池电量（0-100）
    wifi_rssi INTEGER DEFAULT 0,              -- WiFi信号强度（dBm）

    -- 配置信息
    config TEXT,                              -- 设备配置（JSON）

    -- 时间戳
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,

    -- 索引
    INDEX idx_device_id (device_id),
    INDEX idx_api_key (api_key),
    INDEX idx_device_type (device_type),
    INDEX idx_last_heartbeat (last_heartbeat)
);
```

**设备类型说明**：
- `embedded` - 嵌入式设备（电子相框、ESP32等）
- `mobile` - 移动端（手机、平板）
- `web` - Web浏览器
- `offline` - 离线分析程序
- `service` - 后台服务

**GORM 模型**：
```go
type Device struct {
    ID        uint           `gorm:"primarykey" json:"id"`
    CreatedAt time.Time      `json:"created_at"`
    UpdatedAt time.Time      `json:"updated_at"`
    DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

    // 设备信息
    DeviceID  string `gorm:"type:varchar(50);not null;uniqueIndex:idx_device_id" json:"device_id"`
    Name      string `gorm:"type:varchar(100);not null" json:"name"`
    APIKey    string `gorm:"type:varchar(100);not null;uniqueIndex:idx_api_key" json:"-"`
    IPAddress string `gorm:"type:varchar(50)" json:"ip_address"`

    // 设备类型
    DeviceType string `gorm:"type:varchar(20);default:'embedded';index:idx_device_type" json:"device_type"`

    // 描述/备注
    Description string `gorm:"type:varchar(500)" json:"description"`

    // 状态信息
    IsEnabled     bool       `gorm:"default:true" json:"is_enabled"`
    Online        bool       `gorm:"default:false" json:"online"`
    LastSeen *time.Time `gorm:"column:last_heartbeat;index:idx_last_heartbeat" json:"last_heartbeat"`
    BatteryLevel  int        `gorm:"default:0" json:"battery_level"`
    WiFiRSSI      int        `gorm:"column:wifi_rssi;default:0" json:"wifi_rssi"`

    // 配置信息
    Config string `gorm:"type:text" json:"config"`

    // 关联
    DisplayRecords []DisplayRecord `gorm:"foreignKey:DeviceID" json:"-"`
}
```

---

### 2.6 settings（配置表）

**用途**：存储系统配置

```sql
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT,
    description TEXT,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

**预置配置**：
```sql
INSERT INTO settings (key, value, description) VALUES
    ('nas_photo_paths', '[]', 'NAS 照片目录列表（JSON 数组）'),
    ('excluded_paths', '[]', '排除目录列表（JSON 数组）'),
    ('qwen_api_key', '', 'Qwen API Key'),
    ('qwen_api_url', 'https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation', 'Qwen API 地址'),
    ('qwen_model', 'qwen-vl-max', 'Qwen 模型名称'),
    ('scan_daily_limit', '1000', '每日扫描上限'),
    ('scan_time_window', '02:00-06:00', '扫描时间窗口'),
    ('memory_threshold', '70.0', '最低回忆价值阈值'),
    ('display_dedup_days', '7', '展示去重天数'),
    ('home_lat', '', '常驻地纬度'),
    ('home_lon', '', '常驻地经度'),
    ('home_radius_km', '10', '常驻地半径（km）'),
    ('full_scan_completed', 'false', '全量扫描是否完成'),
    ('last_scan_at', '', '最后扫描时间');
```

**GORM 模型**：
```go
type Setting struct {
    Key         string    `gorm:"primaryKey"`
    Value       string
    Description string
    UpdatedAt   time.Time
}
```

---

### 2.6 scan_jobs（扫描任务表）（可选）

**用途**：记录扫描任务和进度

```sql
CREATE TABLE scan_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_type TEXT NOT NULL,                  -- full/incremental/manual
    status TEXT NOT NULL,                    -- pending/running/completed/failed
    directory TEXT,                          -- 扫描目录
    total_files INTEGER DEFAULT 0,
    processed_files INTEGER DEFAULT 0,
    failed_files INTEGER DEFAULT 0,
    started_at DATETIME,
    completed_at DATETIME,
    error_message TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,

    INDEX idx_status (status),
    INDEX idx_job_type (job_type),
    INDEX idx_created_at (created_at)
);
```

**GORM 模型**：
```go
type ScanJob struct {
    ID             uint       `gorm:"primaryKey"`
    JobType        string     `gorm:"index;not null"` // full/incremental/manual
    Status         string     `gorm:"index;not null"` // pending/running/completed/failed
    Directory      string
    TotalFiles     int        `gorm:"default:0"`
    ProcessedFiles int        `gorm:"default:0"`
    FailedFiles    int        `gorm:"default:0"`
    StartedAt      *time.Time
    CompletedAt    *time.Time
    ErrorMessage   string     `gorm:"type:text"`
    CreatedAt      time.Time  `gorm:"index"`
}
```

---

## 三、索引策略

### 3.1 主要索引

**photos 表**（11个索引）：
```sql
-- 唯一索引
CREATE UNIQUE INDEX idx_photos_file_path ON photos(file_path);

-- 高频查询索引
CREATE INDEX idx_photos_exif_datetime ON photos(exif_datetime);
CREATE INDEX idx_photos_memory_score ON photos(memory_score);
CREATE INDEX idx_photos_display_score ON photos(display_score);
CREATE INDEX idx_photos_analyzed ON photos(analyzed);

-- 筛选查询索引
CREATE INDEX idx_photos_category ON photos(category);
CREATE INDEX idx_photos_exif_city ON photos(exif_city);
CREATE INDEX idx_photos_file_missing ON photos(file_missing);

-- 复合索引（往年今日查询）
CREATE INDEX idx_photos_datetime_score ON photos(exif_datetime, display_score);

-- GPS 查询索引
CREATE INDEX idx_photos_gps ON photos(exif_gps_lat, exif_gps_lon);

-- 去重索引
CREATE INDEX idx_photos_file_hash ON photos(file_hash);
```

### 3.2 索引使用场景

| 查询场景 | 使用索引 | 预估性能 |
|---------|---------|---------|
| 往年今日查询 | idx_photos_datetime_score | < 10ms |
| 按评分排序 | idx_photos_display_score | < 20ms |
| 按城市筛选 | idx_photos_exif_city | < 15ms |
| 按分类筛选 | idx_photos_category | < 15ms |
| 查找未分析照片 | idx_photos_analyzed | < 10ms |
| 查找丢失照片 | idx_photos_file_missing | < 10ms |
| 去重检查 | idx_photos_file_hash | < 5ms |

---

## 四、数据库初始化脚本

### 4.1 完整 SQL 脚本

```sql
-- Relive 数据库初始化脚本
-- SQLite 3.38+

-- 启用外键约束
PRAGMA foreign_keys = ON;

-- WAL 模式
PRAGMA journal_mode = WAL;

-- 设置缓存
PRAGMA cache_size = -40000;  -- 40MB

-- 正常同步模式
PRAGMA synchronous = NORMAL;

-- 创建 photos 表
CREATE TABLE IF NOT EXISTS photos (
    -- （完整SQL见上文）
    ...
);

-- 创建 tags 表
CREATE TABLE IF NOT EXISTS tags (
    ...
);

-- 创建 photo_tags 表
CREATE TABLE IF NOT EXISTS photo_tags (
    ...
);

-- 创建 display_history 表
CREATE TABLE IF NOT EXISTS display_history (
    ...
);

-- 创建 settings 表
CREATE TABLE IF NOT EXISTS settings (
    ...
);

-- 创建 scan_jobs 表
CREATE TABLE IF NOT EXISTS scan_jobs (
    ...
);

-- 插入默认配置
INSERT OR IGNORE INTO settings (key, value, description) VALUES
    ('nas_photo_paths', '[]', 'NAS 照片目录列表'),
    ('qwen_api_key', '', 'Qwen API Key'),
    ...;

-- 创建全文搜索索引（可选）
-- CREATE VIRTUAL TABLE photos_fts USING fts5(
--     caption, side_caption, category, type,
--     content=photos, content_rowid=id
-- );
```

### 4.2 GORM Auto Migrate

```go
package database

import (
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
)

func InitDB(dbPath string) (*gorm.DB, error) {
    // 打开数据库
    db, err := gorm.Open(sqlite.Open(dbPath+"?cache=shared&mode=rwc&_journal_mode=WAL"), &gorm.Config{})
    if err != nil {
        return nil, err
    }

    // 自动迁移
    err = db.AutoMigrate(
        &Photo{},
        &Tag{},
        &DisplayHistory{},
        &Device{},
        &Setting{},
        &ScanJob{},
    )
    if err != nil {
        return nil, err
    }

    // 设置 SQLite PRAGMA
    db.Exec("PRAGMA foreign_keys = ON")
    db.Exec("PRAGMA journal_mode = WAL")
    db.Exec("PRAGMA cache_size = -40000")
    db.Exec("PRAGMA synchronous = NORMAL")

    // 初始化默认配置
    initDefaultSettings(db)

    return db, nil
}

func initDefaultSettings(db *gorm.DB) {
    defaults := []Setting{
        {Key: "nas_photo_paths", Value: "[]", Description: "NAS 照片目录列表"},
        {Key: "qwen_api_key", Value: "", Description: "Qwen API Key"},
        // ... 更多默认配置
    }

    for _, s := range defaults {
        db.FirstOrCreate(&s, Setting{Key: s.Key})
    }
}
```

---

## 五、查询示例

### 5.1 往年今日查询

```go
// 查询往年今日的照片（±3天）
func GetOnThisDayPhotos(db *gorm.DB, today time.Time, limit int) ([]Photo, error) {
    var photos []Photo

    // 计算日期范围
    startDay := today.AddDate(0, 0, -3).Format("01-02")
    endDay := today.AddDate(0, 0, 3).Format("01-02")
    thisYear := today.Year()

    err := db.Where("strftime('%m-%d', exif_datetime) BETWEEN ? AND ?", startDay, endDay).
        Where("strftime('%Y', exif_datetime) < ?", fmt.Sprintf("%d", thisYear)).
        Where("memory_score >= ?", 70.0).
        Where("file_missing = ?", false).
        Order("display_score DESC").
        Limit(limit).
        Find(&photos).Error

    return photos, err
}
```

### 5.2 避免重复展示

```go
// 获取最近N天已展示的照片ID
func GetRecentlyDisplayedPhotoIDs(db *gorm.DB, days int) ([]uint, error) {
    var ids []uint

    cutoff := time.Now().AddDate(0, 0, -days)

    err := db.Model(&DisplayHistory{}).
        Where("displayed_at >= ?", cutoff).
        Pluck("photo_id", &ids).Error

    return ids, err
}

// 查询时排除
query := db.Where("id NOT IN ?", recentlyDisplayedIDs)
```

### 5.3 按城市统计

```go
// 统计每个城市的照片数量
type CityStats struct {
    City  string
    Count int64
}

func GetCityStats(db *gorm.DB) ([]CityStats, error) {
    var stats []CityStats

    err := db.Model(&Photo{}).
        Select("exif_city as city, COUNT(*) as count").
        Where("exif_city != ''").
        Group("exif_city").
        Order("count DESC").
        Find(&stats).Error

    return stats, err
}
```

---

## 六、数据迁移方案

### 6.1 版本管理

**使用 golang-migrate**：
```bash
# 安装
go install -tags 'sqlite3' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# 创建迁移
migrate create -ext sql -dir migrations -seq init_schema

# 执行迁移
migrate -path migrations -database "sqlite3://relive.db" up

# 回滚
migrate -path migrations -database "sqlite3://relive.db" down 1
```

### 6.2 迁移文件示例

**001_init_schema.up.sql**：
```sql
-- 创建 photos 表
CREATE TABLE IF NOT EXISTS photos (
    ...
);

-- 其他表
...
```

**001_init_schema.down.sql**：
```sql
-- 删除表
DROP TABLE IF EXISTS photos;
DROP TABLE IF EXISTS tags;
...
```

---

## 七、备份和恢复

### 7.1 备份策略

**每日自动备份**：
```bash
#!/bin/bash
# backup.sh

DATE=$(date +%Y%m%d)
BACKUP_DIR="/backup/relive"

# 创建备份目录
mkdir -p $BACKUP_DIR

# 备份数据库
cp /data/relive.db $BACKUP_DIR/relive.db.$DATE

# 清理30天前的备份
find $BACKUP_DIR -name "relive.db.*" -mtime +30 -delete

# 可选：压缩
gzip $BACKUP_DIR/relive.db.$DATE
```

**Cron 配置**：
```
0 3 * * * /path/to/backup.sh
```

### 7.2 恢复

```bash
# 停止服务
docker stop relive

# 恢复数据库
cp /backup/relive/relive.db.20260228 /data/relive.db

# 启动服务
docker start relive
```

---

## 八、性能优化建议

### 8.1 查询优化

- ✅ 使用 `EXPLAIN QUERY PLAN` 分析查询
- ✅ 避免 SELECT *，只查询需要的字段
- ✅ 使用 LIMIT 限制结果集
- ✅ 合理使用索引
- ✅ 批量操作使用事务

### 8.2 写入优化

```go
// 批量插入
db.CreateInBatches(photos, 100)

// 使用事务
db.Transaction(func(tx *gorm.DB) error {
    for _, photo := range photos {
        if err := tx.Create(&photo).Error; err != nil {
            return err
        }
    }
    return nil
})
```

### 8.3 定期维护

```go
// 定期 VACUUM（每月）
db.Exec("VACUUM")

// 更新统计信息（每周）
db.Exec("ANALYZE")

// 检查完整性（每周）
db.Raw("PRAGMA integrity_check").Scan(&result)
```

---

## 九、总结

### 9.1 表结构总结

| 表名 | 用途 | 预估行数 |
|------|------|---------|
| photos | 照片主表 | 11万+ |
| tags | 标签 | 100-500 |
| photo_tags | 照片标签关联 | 50万+ |
| display_history | 展示历史 | 1万+ |
| devices | 设备管理 | < 100 |
| settings | 配置 | < 50 |
| scan_jobs | 扫描任务 | < 1000 |

### 9.2 存储空间估算

```
photos 表：11万 × 5KB = ~550MB
tags 表：500 × 1KB = ~500KB
photo_tags 表：50万 × 0.1KB = ~50MB
display_history 表：1万 × 0.5KB = ~5MB
devices 表：100 × 1KB = ~100KB
索引：~100MB

总计：~700MB
```

### 9.3 设计优势

- ✅ **标准 SQL**：兼容多种数据库
- ✅ **合理索引**：查询性能优秀
- ✅ **扩展性强**：预留未来功能字段
- ✅ **维护简单**：SQLite 单文件
- ✅ **备份方便**：复制文件即可

---

**数据库设计完成** ✅
**准备实现数据访问层** 🚀
