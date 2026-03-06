package repository

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// QueuedResult 队列中的结果项
type QueuedResult struct {
	Result     model.AnalysisResult `json:"result"`
	DeviceID   uint                 `json:"device_id"`
	EnqueuedAt time.Time            `json:"enqueued_at"`
	RetryCount int                  `json:"retry_count"`
}

// ResultStorage 持久化存储接口
type ResultStorage interface {
	// 保存未处理的结果
	Save(results []*QueuedResult) error
	// 加载未处理的结果
	Load() ([]*QueuedResult, error)
	// 删除已处理的结果
	Delete(count int) error
	// 获取待处理数量
	PendingCount() (int, error)
}

// ResultQueueItem 队列项数据库模型
type ResultQueueItem struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`

	// 数据
	Data       string `gorm:"type:text;not null" json:"data"`       // JSON 序列化的 QueuedResult
	Priority   int    `gorm:"default:0" json:"priority"`            // 优先级
	RetryCount int    `gorm:"default:0" json:"retry_count"`         // 重试次数
	Processed  bool   `gorm:"default:false;index" json:"processed"` // 是否已处理
}

// TableName 指定表名
func (ResultQueueItem) TableName() string {
	return "result_queue"
}

// DBResultStorage 数据库存储实现
type DBResultStorage struct {
	db *gorm.DB
	mu sync.Mutex
}

// NewDBResultStorage 创建数据库存储
func NewDBResultStorage(db *gorm.DB) ResultStorage {
	return &DBResultStorage{db: db}
}

// Save 保存结果到数据库
func (s *DBResultStorage) Save(results []*QueuedResult) error {
	if len(results) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]ResultQueueItem, 0, len(results))
	for _, r := range results {
		data, err := json.Marshal(r)
		if err != nil {
			logger.Errorf("Failed to marshal result: %v", err)
			continue
		}

		items = append(items, ResultQueueItem{
			Data:       string(data),
			Priority:   r.RetryCount, // 重试次数越多优先级越高
			RetryCount: r.RetryCount,
			Processed:  false,
		})
	}

	if len(items) == 0 {
		return nil
	}

	return s.db.CreateInBatches(items, 100).Error
}

// Load 加载未处理的结果
func (s *DBResultStorage) Load() ([]*QueuedResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var items []ResultQueueItem
	if err := s.db.Where("processed = ?", false).
		Order("priority DESC, created_at ASC").
		Limit(10000).
		Find(&items).Error; err != nil {
		return nil, err
	}

	results := make([]*QueuedResult, 0, len(items))
	idsToDelete := make([]uint, 0, len(items))

	for _, item := range items {
		var result QueuedResult
		if err := json.Unmarshal([]byte(item.Data), &result); err != nil {
			logger.Errorf("Failed to unmarshal result: %v", err)
			idsToDelete = append(idsToDelete, item.ID)
			continue
		}
		result.RetryCount = item.RetryCount
		results = append(results, &result)
		idsToDelete = append(idsToDelete, item.ID)
	}

	// 删除已加载的记录
	if len(idsToDelete) > 0 {
		if err := s.db.Delete(&ResultQueueItem{}, idsToDelete).Error; err != nil {
			logger.Errorf("Failed to delete loaded items: %v", err)
		}
	}

	return results, nil
}

// Delete 删除已处理的结果
func (s *DBResultStorage) Delete(count int) error {
	// 实际上我们在 Load 时就已经删除了
	return nil
}

// PendingCount 获取待处理数量
func (s *DBResultStorage) PendingCount() (int, error) {
	var count int64
	err := s.db.Model(&ResultQueueItem{}).Where("processed = ?", false).Count(&count).Error
	return int(count), err
}

// MigrateResultQueue 迁移队列表
func MigrateResultQueue(db *gorm.DB) error {
	return db.AutoMigrate(&ResultQueueItem{})
}

// FileResultStorage 文件存储实现（备用方案）
type FileResultStorage struct {
	filePath string
	mu       sync.Mutex
}

// NewFileResultStorage 创建文件存储
func NewFileResultStorage(dataDir string) ResultStorage {
	return &FileResultStorage{
		filePath: filepath.Join(dataDir, "result_queue.json"),
	}
}

// Save 保存到文件
func (s *FileResultStorage) Save(results []*QueuedResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 读取现有数据
	existing, _ := s.loadFromFile()

	// 合并数据
	allResults := append(existing, results...)

	// 保存到文件
	return s.saveToFile(allResults)
}

// Load 从文件加载
func (s *FileResultStorage) Load() ([]*QueuedResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.loadFromFile()
}

// Delete 清空文件
func (s *FileResultStorage) Delete(count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 读取现有数据
	existing, err := s.loadFromFile()
	if err != nil {
		return err
	}

	// 删除前 count 条
	if count >= len(existing) {
		return s.saveToFile([]*QueuedResult{})
	}

	return s.saveToFile(existing[count:])
}

// PendingCount 获取待处理数量
func (s *FileResultStorage) PendingCount() (int, error) {
	results, err := s.Load()
	return len(results), err
}

func (s *FileResultStorage) loadFromFile() ([]*QueuedResult, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []*QueuedResult{}, nil
		}
		return nil, err
	}

	var results []*QueuedResult
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	return results, nil
}

func (s *FileResultStorage) saveToFile(results []*QueuedResult) error {
	// 确保目录存在
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	// 原子写入：先写入临时文件，再重命名
	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("write tmp file: %w", err)
	}

	return os.Rename(tmpFile, s.filePath)
}
