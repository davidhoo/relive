package service

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
)

// QueuedResult 队列中的结果项（从 repository 包导入）
type QueuedResult = repository.QueuedResult

// ResultQueue 结果写入队列
type ResultQueue struct {
	// 内存队列（带缓冲）
	queue chan *QueuedResult

	// 持久化存储
	storage repository.ResultStorage

	// 批量处理器
	processor *BatchProcessor

	// 数据库连接（用于批量写入）
	db interface{} // 实际类型为 *gorm.DB，避免循环依赖

	// 分析服务（用于实际写入）
	analysisService AnalysisService

	// 控制
	mu      sync.RWMutex
	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup

	// 统计
	enqueuedCount   uint64
	processedCount  uint64
	failedCount     uint64

	// 配置
	batchSize     int
	flushInterval time.Duration
}

// ResultQueueConfig 队列配置
type ResultQueueConfig struct {
	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
}

// DefaultResultQueueConfig 默认配置
func DefaultResultQueueConfig() ResultQueueConfig {
	return ResultQueueConfig{
		QueueSize:     10000,              // 内存队列缓冲 10000 条
		BatchSize:     50,                 // 每批写入 50 条
		FlushInterval: 5 * time.Second,    // 每 5 秒刷新一次
	}
}

// NewResultQueue 创建结果队列
func NewResultQueue(storage repository.ResultStorage, analysisService AnalysisService, config ResultQueueConfig) *ResultQueue {
	if config.QueueSize <= 0 {
		config.QueueSize = 10000
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 50
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = 5 * time.Second
	}

	q := &ResultQueue{
		queue:           make(chan *QueuedResult, config.QueueSize),
		storage:         storage,
		analysisService: analysisService,
		stopCh:          make(chan struct{}),
		batchSize:       config.BatchSize,
		flushInterval:   config.FlushInterval,
	}

	// 创建批量处理器
	q.processor = NewBatchProcessor(config.BatchSize, config.FlushInterval, q)

	return q
}

// Start 启动队列
func (q *ResultQueue) Start() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.running {
		return nil
	}

	// 恢复未处理的数据
	if err := q.restore(); err != nil {
		logger.Errorf("Failed to restore queue: %v", err)
	}

	q.running = true
	q.stopCh = make(chan struct{})

	// 启动批量处理器
	q.wg.Add(1)
	go q.processor.Run(q.stopCh, &q.wg)

	logger.Infof("Result queue started (batchSize=%d, flushInterval=%v)",
		q.batchSize, q.flushInterval)
	return nil
}

// Stop 停止队列
func (q *ResultQueue) Stop() {
	q.mu.Lock()
	if !q.running {
		q.mu.Unlock()
		return
	}
	q.running = false
	close(q.stopCh)
	q.mu.Unlock()

	// 等待处理器完成
	q.wg.Wait()

	// 持久化剩余数据
	if err := q.persist(); err != nil {
		logger.Errorf("Failed to persist queue on stop: %v", err)
	}

	logger.Info("Result queue stopped")
}

// IsRunning 检查队列是否运行中
func (q *ResultQueue) IsRunning() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.running
}

// Enqueue 提交结果到队列（立即返回，不等待写入）
func (q *ResultQueue) Enqueue(results []model.AnalysisResult, deviceID uint) (*model.SubmitResultsResponse, error) {
	resp := &model.SubmitResultsResponse{
		Accepted:      0,
		Rejected:      0,
		RejectedItems: make([]model.RejectedItem, 0),
		FailedPhotos:  make([]uint, 0),
	}

	q.mu.RLock()
	running := q.running
	q.mu.RUnlock()

	if !running {
		return nil, fmt.Errorf("result queue is not running")
	}

	logger.Infof("Enqueue called with %d results", len(results))

	for _, r := range results {
		// 基础验证
		if err := validateQueuedResult(r); err != nil {
			resp.Rejected++
			resp.RejectedItems = append(resp.RejectedItems, model.RejectedItem{
				PhotoID: r.PhotoID,
				Reason:  "validation_failed",
				Message: err.Error(),
			})
			continue
		}

		// 入队（带超时，避免阻塞）
		select {
		case q.queue <- &QueuedResult{
			Result:     r,
			DeviceID:   deviceID,
			EnqueuedAt: time.Now(),
		}:
			resp.Accepted++
			atomic.AddUint64(&q.enqueuedCount, 1)
			logger.Debugf("Enqueued result for photo %d", r.PhotoID)
		case <-time.After(100 * time.Millisecond):
			// 队列满或阻塞，拒绝请求（客户端会重试）
			resp.Rejected++
			resp.RejectedItems = append(resp.RejectedItems, model.RejectedItem{
				PhotoID: r.PhotoID,
				Reason:  "server_busy",
				Message: "Result queue is full, please retry later",
			})
			logger.Warnf("Failed to enqueue result for photo %d: queue full or timeout", r.PhotoID)
		}
	}

	logger.Infof("Enqueue completed: accepted=%d, rejected=%d", resp.Accepted, resp.Rejected)
	return resp, nil
}

// QueueSize 获取队列当前大小
func (q *ResultQueue) QueueSize() int {
	return len(q.queue)
}

// Stats 获取队列统计
func (q *ResultQueue) Stats() map[string]interface{} {
	return map[string]interface{}{
		"enqueued":   atomic.LoadUint64(&q.enqueuedCount),
		"processed":  atomic.LoadUint64(&q.processedCount),
		"failed":     atomic.LoadUint64(&q.failedCount),
		"queue_size": q.QueueSize(),
		"running":    q.IsRunning(),
	}
}

// restore 恢复未处理的数据
func (q *ResultQueue) restore() error {
	items, err := q.storage.Load()
	if err != nil {
		return fmt.Errorf("load from storage: %w", err)
	}

	if len(items) == 0 {
		return nil
	}

	logger.Infof("Restoring %d results from storage", len(items))

	// 恢复到内存队列
	restored := 0
	for _, item := range items {
		select {
		case q.queue <- item:
			restored++
		default:
			// 内存队列满，保留在存储中
			logger.Warnf("Queue full, keeping %d items in storage", len(items)-restored)
			break
		}
	}

	return nil
}

// persist 持久化剩余数据
func (q *ResultQueue) persist() error {
	// 收集队列中剩余的数据
	var items []*QueuedResult
	for {
		select {
		case item := <-q.queue:
			items = append(items, item)
		default:
			goto done
		}
	}
done:

	if len(items) == 0 {
		return nil
	}

	logger.Infof("Persisting %d remaining results", len(items))
	return q.storage.Save(items)
}

// validateQueuedResult 验证结果
func validateQueuedResult(r model.AnalysisResult) error {
	if r.PhotoID == 0 {
		return fmt.Errorf("photo_id is required")
	}
	if r.Description == "" {
		return fmt.Errorf("description is required")
	}
	if r.MemoryScore < 0 || r.MemoryScore > 100 {
		return fmt.Errorf("memory_score must be between 0 and 100")
	}
	if r.BeautyScore < 0 || r.BeautyScore > 100 {
		return fmt.Errorf("beauty_score must be between 0 and 100")
	}
	return nil
}

// takeFromQueue 从队列取一批数据（供 BatchProcessor 使用）
func (q *ResultQueue) takeFromQueue(batchSize int, timeout time.Duration) []*QueuedResult {
	items := make([]*QueuedResult, 0, batchSize)

	// 至少取一条（阻塞）
	select {
	case item := <-q.queue:
		if item != nil {
			items = append(items, item)
		}
	case <-time.After(timeout):
		return items
	}

	// 继续取，直到达到 batchSize 或队列为空
	for len(items) < batchSize {
		select {
		case item := <-q.queue:
			if item != nil {
				items = append(items, item)
			}
		default:
			// 队列空，返回已收集的
			return items
		}
	}

	return items
}

// onProcessed 处理完成回调
func (q *ResultQueue) onProcessed(count int, success bool) {
	if success {
		atomic.AddUint64(&q.processedCount, uint64(count))
	} else {
		atomic.AddUint64(&q.failedCount, uint64(count))
	}
}
