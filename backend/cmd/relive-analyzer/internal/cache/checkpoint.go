package cache

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
	_ "github.com/mattn/go-sqlite3"
)

const (
	defaultCheckpointFile = "checkpoint.db"
	checkpointSchema      = `
CREATE TABLE IF NOT EXISTS checkpoint (
    photo_id INTEGER PRIMARY KEY,
    status TEXT NOT NULL,           -- 'success', 'failed', 'pending'
    attempts INTEGER DEFAULT 0,     -- 尝试次数
    error_msg TEXT,                 -- 失败原因
    processed_at TIMESTAMP          -- 处理时间
);

CREATE INDEX IF NOT EXISTS idx_status ON checkpoint(status);
CREATE INDEX IF NOT EXISTS idx_processed_at ON checkpoint(processed_at);
`
)

// Checkpoint 断点续传管理器
type Checkpoint struct {
	db       *sql.DB
	dbPath   string
	mu       sync.RWMutex
}

// CheckpointStatus 检查点状态
type CheckpointStatus string

const (
	StatusSuccess CheckpointStatus = "success"
	StatusFailed  CheckpointStatus = "failed"
	StatusPending CheckpointStatus = "pending"
)

// NewCheckpoint 创建断点续传管理器
func NewCheckpoint(dbPath string) (*Checkpoint, error) {
	// 处理 ~ 展开
	if strings.HasPrefix(dbPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		dbPath = filepath.Join(home, dbPath[2:])
	}

	// 确保目录存在
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create checkpoint dir: %w", err)
	}

	// 打开数据库
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open checkpoint db: %w", err)
	}

	// 创建表结构
	if _, err := db.Exec(checkpointSchema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create checkpoint schema: %w", err)
	}

	return &Checkpoint{
		db:     db,
		dbPath: dbPath,
	}, nil
}

// Close 关闭数据库
func (c *Checkpoint) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

// MarkPending 标记照片为处理中
func (c *Checkpoint) MarkPending(photoID uint) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.db.Exec(
		`INSERT OR REPLACE INTO checkpoint (photo_id, status, attempts, processed_at)
		 VALUES (?, ?, COALESCE((SELECT attempts FROM checkpoint WHERE photo_id = ?), 0) + 1, ?)`,
		photoID, StatusPending, photoID, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("mark pending: %w", err)
	}
	return nil
}

// MarkSuccess 标记照片为成功
func (c *Checkpoint) MarkSuccess(photoID uint) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.db.Exec(
		`INSERT OR REPLACE INTO checkpoint (photo_id, status, attempts, error_msg, processed_at)
		 VALUES (?, ?, COALESCE((SELECT attempts FROM checkpoint WHERE photo_id = ?), 1), NULL, ?)`,
		photoID, StatusSuccess, photoID, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("mark success: %w", err)
	}
	return nil
}

// MarkFailed 标记照片为失败
func (c *Checkpoint) MarkFailed(photoID uint, errorMsg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.db.Exec(
		`INSERT OR REPLACE INTO checkpoint (photo_id, status, attempts, error_msg, processed_at)
		 VALUES (?, ?, COALESCE((SELECT attempts FROM checkpoint WHERE photo_id = ?), 1), ?, ?)`,
		photoID, StatusFailed, photoID, errorMsg, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	return nil
}

// IsProcessed 检查照片是否已处理（成功或失败）
func (c *Checkpoint) IsProcessed(photoID uint) (bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var status string
	err := c.db.QueryRow(
		"SELECT status FROM checkpoint WHERE photo_id = ? AND status != ?",
		photoID, StatusPending,
	).Scan(&status)

	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check processed: %w", err)
	}

	return status == string(StatusSuccess) || status == string(StatusFailed), nil
}

// GetStats 获取统计信息
func (c *Checkpoint) GetStats() (*CheckpointStats, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := &CheckpointStats{}

	// 总记录数
	if err := c.db.QueryRow("SELECT COUNT(*) FROM checkpoint").Scan(&stats.Total); err != nil {
		return nil, fmt.Errorf("count total: %w", err)
	}

	// 成功数
	if err := c.db.QueryRow("SELECT COUNT(*) FROM checkpoint WHERE status = ?", StatusSuccess).Scan(&stats.Success); err != nil {
		return nil, fmt.Errorf("count success: %w", err)
	}

	// 失败数
	if err := c.db.QueryRow("SELECT COUNT(*) FROM checkpoint WHERE status = ?", StatusFailed).Scan(&stats.Failed); err != nil {
		return nil, fmt.Errorf("count failed: %w", err)
	}

	// 处理中数
	if err := c.db.QueryRow("SELECT COUNT(*) FROM checkpoint WHERE status = ?", StatusPending).Scan(&stats.Pending); err != nil {
		return nil, fmt.Errorf("count pending: %w", err)
	}

	return stats, nil
}

// GetFailedPhotos 获取失败的照片列表
func (c *Checkpoint) GetFailedPhotos(limit int) ([]FailedPhoto, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rows, err := c.db.Query(
		`SELECT photo_id, attempts, error_msg, processed_at
		 FROM checkpoint
		 WHERE status = ?
		 ORDER BY processed_at DESC
		 LIMIT ?`,
		StatusFailed, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query failed photos: %w", err)
	}
	defer rows.Close()

	var photos []FailedPhoto
	for rows.Next() {
		var p FailedPhoto
		var processedAt sql.NullTime
		if err := rows.Scan(&p.PhotoID, &p.Attempts, &p.ErrorMsg, &processedAt); err != nil {
			return nil, fmt.Errorf("scan failed photo: %w", err)
		}
		if processedAt.Valid {
			p.ProcessedAt = processedAt.Time
		}
		photos = append(photos, p)
	}

	return photos, rows.Err()
}

// ResetFailed 重置失败状态为未处理（用于重试）
func (c *Checkpoint) ResetFailed(photoID uint) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := c.db.Exec("DELETE FROM checkpoint WHERE photo_id = ?", photoID)
	if err != nil {
		return fmt.Errorf("reset failed: %w", err)
	}
	return nil
}

// CleanupOldRecords 清理旧记录（保留最近 N 天的记录）
func (c *Checkpoint) CleanupOldRecords(days int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cutoff := time.Now().AddDate(0, 0, -days)
	result, err := c.db.Exec(
		"DELETE FROM checkpoint WHERE processed_at < ? AND status != ?",
		cutoff, StatusPending,
	)
	if err != nil {
		return 0, fmt.Errorf("cleanup old records: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("get affected rows: %w", err)
	}

	if affected > 0 {
		logger.Infof("Cleaned up %d old checkpoint records", affected)
	}

	return affected, nil
}

// CheckpointStats 统计信息
type CheckpointStats struct {
	Total   int64
	Success int64
	Failed  int64
	Pending int64
}

// FailedPhoto 失败的照片记录
type FailedPhoto struct {
	PhotoID     uint
	Attempts    int
	ErrorMsg    string
	ProcessedAt time.Time
}

// GetDBPath 获取数据库路径
func (c *Checkpoint) GetDBPath() string {
	return c.dbPath
}

// FilterProcessed 从照片ID列表中过滤掉已处理的照片
func (c *Checkpoint) FilterProcessed(photoIDs []uint) ([]uint, error) {
	if len(photoIDs) == 0 {
		return photoIDs, nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	// 构建 IN 查询
	placeholders := make([]string, len(photoIDs))
	args := make([]interface{}, len(photoIDs))
	for i, id := range photoIDs {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		"SELECT photo_id FROM checkpoint WHERE photo_id IN (%s) AND status != ?",
		strings.Join(placeholders, ","),
	)
	args = append(args, StatusPending)

	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query processed: %w", err)
	}
	defer rows.Close()

	processed := make(map[uint]bool)
	for rows.Next() {
		var id uint
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan processed: %w", err)
		}
		processed[id] = true
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	// 过滤
	var result []uint
	for _, id := range photoIDs {
		if !processed[id] {
			result = append(result, id)
		}
	}

	return result, nil
}

// ResetStuckPending 重置卡住的处理中状态（用于启动时清理）
func (c *Checkpoint) ResetStuckPending(maxAge time.Duration) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	result, err := c.db.Exec(
		"DELETE FROM checkpoint WHERE status = ? AND processed_at < ?",
		StatusPending, cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("reset stuck pending: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("get affected rows: %w", err)
	}

	if affected > 0 {
		logger.Infof("Reset %d stuck pending records", affected)
	}

	return affected, nil
}
