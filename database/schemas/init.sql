-- Relive Database Schema

-- 照片表
CREATE TABLE IF NOT EXISTS photos (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL UNIQUE,
    file_name TEXT NOT NULL,
    file_size INTEGER,
    width INTEGER,
    height INTEGER,
    taken_at DATETIME,  -- 照片拍摄时间（从 EXIF 提取）
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,

    -- AI 分析结果
    description TEXT,  -- 80-200 字描述
    caption TEXT,      -- 8-30 字文案
    category TEXT,     -- 分类

    -- 评分
    art_score INTEGER,      -- 美观艺术性评分 (0-100)
    memory_score INTEGER,   -- 值得回忆评分 (0-100)

    -- 分析状态
    analyzed BOOLEAN DEFAULT FALSE,
    analyzed_at DATETIME,

    -- 索引
    INDEX idx_taken_at (taken_at),
    INDEX idx_analyzed (analyzed),
    INDEX idx_art_score (art_score),
    INDEX idx_memory_score (memory_score)
);

-- 照片标签表（多对多关系）
CREATE TABLE IF NOT EXISTS tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS photo_tags (
    photo_id INTEGER NOT NULL,
    tag_id INTEGER NOT NULL,
    PRIMARY KEY (photo_id, tag_id),
    FOREIGN KEY (photo_id) REFERENCES photos(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
);

-- 展示历史表（记录相框显示过的照片）
CREATE TABLE IF NOT EXISTS display_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    photo_id INTEGER NOT NULL,
    displayed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    display_reason TEXT,  -- 展示原因：on_this_day, highest_score, random 等
    FOREIGN KEY (photo_id) REFERENCES photos(id) ON DELETE CASCADE,
    INDEX idx_displayed_at (displayed_at)
);

-- 配置表
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT,
    description TEXT,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 插入默认配置
INSERT OR IGNORE INTO settings (key, value, description) VALUES
    ('nas_photo_path', '/path/to/nas/photos', 'NAS 照片存储路径'),
    ('qwen_api_key', '', 'Qwen3-VL API Key'),
    ('qwen_api_url', 'https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation', 'Qwen API 地址'),
    ('scan_interval', '3600', '照片扫描间隔（秒）'),
    ('min_art_score', '60', '最低艺术性评分阈值'),
    ('min_memory_score', '70', '最低回忆价值评分阈值');
