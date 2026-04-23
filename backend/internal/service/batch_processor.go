package service

import (
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
)

// BatchProcessor 批量处理器
type BatchProcessor struct {
	batchSize     int
	flushInterval time.Duration
	queue         *ResultQueue

	// 批处理缓冲
	buffer []*QueuedResult
	mu     sync.Mutex

	// 重试队列（失败的项）
	retryQueue []*QueuedResult
	retryMu    sync.Mutex

	// 统计
	processedCount uint64
	failedCount    uint64
	retriedCount   uint64

	// 去重窗口（最近处理过的照片ID）
	dedupWindow map[uint]time.Time
	dedupMu     sync.Mutex
}

// NewBatchProcessor 创建批量处理器
func NewBatchProcessor(batchSize int, flushInterval time.Duration, queue *ResultQueue) *BatchProcessor {
	return &BatchProcessor{
		batchSize:     batchSize,
		flushInterval: flushInterval,
		queue:         queue,
		buffer:        make([]*QueuedResult, 0, batchSize),
		retryQueue:    make([]*QueuedResult, 0),
		dedupWindow:   make(map[uint]time.Time),
	}
}

// Run 运行处理器（单 goroutine）
func (p *BatchProcessor) Run(stopCh <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()

	// 空转退避：连续空轮次越多，间隔越长
	idleTicks := 0
	maxIdleInterval := 30 * time.Second

	logger.Info("Batch processor started")

	for {
		select {
		case <-ticker.C:
			hasWork := p.flush(false)
			if hasWork {
				idleTicks = 0
				ticker.Reset(p.flushInterval)
			} else {
				idleTicks++
				if idleTicks >= 3 {
					// 退避：3 次空转后拉长间隔，上限 30s
					backoff := p.flushInterval * time.Duration(idleTicks-1)
					if backoff > maxIdleInterval {
						backoff = maxIdleInterval
					}
					ticker.Reset(backoff)
				}
			}

		case <-stopCh:
			// 停止前刷新所有数据
			logger.Info("Batch processor stopping, flushing remaining data...")
			p.flush(true)
			return
		}
	}
}

// flush 批量写入数据库，返回是否有实际数据处理
func (p *BatchProcessor) flush(isFinal bool) bool {
	// 1. 从队列取数据
	items := p.queue.takeFromQueue(p.batchSize, 500*time.Millisecond)

	// 2. 加入重试队列中的项
	p.retryMu.Lock()
	retryCount := len(p.retryQueue)
	if retryCount > 0 {
		items = append(items, p.retryQueue...)
		p.retryQueue = p.retryQueue[:0]
	}
	p.retryMu.Unlock()

	// 3. 加入缓冲区
	p.mu.Lock()
	p.buffer = append(p.buffer, items...)

	// 如果没有数据在缓冲区，直接返回
	if len(p.buffer) == 0 {
		p.mu.Unlock()
		return false
	}

	logger.Infof("Batch processor flush: %d new items, %d in buffer, isFinal=%v",
		len(items), len(p.buffer), isFinal)

	// 如果缓冲区还不够大且不是最终刷新，等待更多数据
	// 但如果是定时刷新（从队列取不到新数据）且缓冲区有数据，则强制刷新
	if len(p.buffer) < p.batchSize && !isFinal && len(items) > 0 {
		p.mu.Unlock()
		return true
	}

	// 取出缓冲区所有数据
	toProcess := make([]*QueuedResult, len(p.buffer))
	copy(toProcess, p.buffer)
	p.buffer = p.buffer[:0]
	p.mu.Unlock()

	// 4. 去重
	uniqueItems := p.deduplicate(toProcess)

	// 5. 批量写入
	logger.Infof("Writing %d unique items to database", len(uniqueItems))
	if err := p.batchWrite(uniqueItems); err != nil {
		logger.Errorf("Batch write failed: %v", err)
		// 失败时加入重试队列
		p.scheduleRetry(toProcess)
		p.queue.onProcessed(len(toProcess), false)
	} else {
		logger.Infof("Batch write success: %d results", len(uniqueItems))
		p.queue.onProcessed(len(uniqueItems), true)
	}

	return true
}

// deduplicate 去重（同一照片保留最新，并检查最近处理过的）
func (p *BatchProcessor) deduplicate(items []*QueuedResult) []*QueuedResult {
	p.dedupMu.Lock()
	defer p.dedupMu.Unlock()

	// 清理过期的去重窗口（5分钟前的）
	now := time.Now()
	for id, t := range p.dedupWindow {
		if now.Sub(t) > 5*time.Minute {
			delete(p.dedupWindow, id)
		}
	}

	// 去重：同一照片保留最新的
	latest := make(map[uint]*QueuedResult)
	for _, item := range items {
		photoID := item.Result.PhotoID

		// 检查是否在去重窗口中（最近已处理）
		if _, processed := p.dedupWindow[photoID]; processed {
			continue
		}

		if existing, ok := latest[photoID]; ok {
			// 保留更新的
			if item.EnqueuedAt.After(existing.EnqueuedAt) {
				latest[photoID] = item
			}
		} else {
			latest[photoID] = item
		}
	}

	result := make([]*QueuedResult, 0, len(latest))
	for _, v := range latest {
		result = append(result, v)
		// 加入去重窗口
		p.dedupWindow[v.Result.PhotoID] = now
	}

	return result
}

// batchWrite 批量写入数据库（串行，无并发问题）
func (p *BatchProcessor) batchWrite(items []*QueuedResult) error {
	if len(items) == 0 {
		return nil
	}

	// 构建批量更新数据
	validResults := make([]struct {
		result       model.AnalysisResult
		overallScore int
		aiProvider   string
		deviceID     uint
	}, 0, len(items))

	for _, item := range items {
		result := item.Result
		overallScore := model.CalcOverallScore(result.MemoryScore, result.BeautyScore)
		aiProvider := result.AIProvider
		if aiProvider == "" {
			aiProvider = "analyzer"
		}

		validResults = append(validResults, struct {
			result       model.AnalysisResult
			overallScore int
			aiProvider   string
			deviceID     uint
		}{
			result:       result,
			overallScore: overallScore,
			aiProvider:   aiProvider,
			deviceID:     item.DeviceID,
		})
	}

	// 执行批量更新
	return p.executeBatchUpdate(validResults)
}

// executeBatchUpdate 执行批量更新
func (p *BatchProcessor) executeBatchUpdate(results []struct {
	result       model.AnalysisResult
	overallScore int
	aiProvider   string
	deviceID     uint
}) error {
	if len(results) == 0 {
		return nil
	}

	logger.Infof("Executing batch update for %d results", len(results))

	// 转换为 model.AnalysisResult 和 deviceID
	submitResults := make([]model.AnalysisResult, len(results))
	deviceID := results[0].deviceID // 假设同一批是同一设备

	for i, r := range results {
		submitResults[i] = r.result
		submitResults[i].OverallScore = r.overallScore
		submitResults[i].AIProvider = r.aiProvider
		submitResults[i].AnalyzedAt = time.Now()
	}

	logger.Infof("Calling SubmitResultsDirectly with %d results, deviceID=%d", len(submitResults), deviceID)

	// 直接调用 AnalysisService 的 SubmitResultsDirectly 方法
	resp, err := p.queue.analysisService.SubmitResultsDirectly(submitResults, deviceID)
	if err != nil {
		logger.Errorf("SubmitResultsDirectly failed: %v", err)
		return err
	}

	logger.Infof("SubmitResultsDirectly succeeded: accepted=%d, rejected=%d", resp.Accepted, resp.Rejected)
	return nil
}

// scheduleRetry 安排重试
func (p *BatchProcessor) scheduleRetry(items []*QueuedResult) {
	p.retryMu.Lock()
	defer p.retryMu.Unlock()

	now := time.Now()
	for _, item := range items {
		// 最多重试 3 次
		if item.RetryCount >= 3 {
			logger.Errorf("Max retries exceeded for photo %d, dropping result", item.Result.PhotoID)
			continue
		}

		item.RetryCount++
		item.EnqueuedAt = now // 更新入队时间，用于重试延迟
		p.retryQueue = append(p.retryQueue, item)
	}

	logger.Warnf("Scheduled %d items for retry", len(p.retryQueue))
}

// Stats 获取处理器统计
func (p *BatchProcessor) Stats() map[string]interface{} {
	p.mu.Lock()
	bufferSize := len(p.buffer)
	p.mu.Unlock()

	p.retryMu.Lock()
	retrySize := len(p.retryQueue)
	p.retryMu.Unlock()

	return map[string]interface{}{
		"buffer_size":  bufferSize,
		"retry_queue":  retrySize,
		"processed":    p.processedCount,
		"failed":       p.failedCount,
	}
}
