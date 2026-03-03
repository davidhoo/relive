package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
)

const (
	defaultBufferFile = "batch_buffer.json"
	defaultBatchSize  = 10
	defaultFlushInterval = 30 * time.Second
	maxBatchSize      = 50
)

// ResultBuffer 结果缓冲区
type ResultBuffer struct {
	results      []model.AnalysisResult
	mutex        sync.RWMutex
	batchSize    int
	flushInterval time.Duration
	bufferFile   string

	// 提交回调
	submitter    func(ctx context.Context, results []model.AnalysisResult) error

	// 刷新控制
	flushTimer   *time.Timer
	stopCh       chan struct{}
	wg           sync.WaitGroup
	mu           sync.Mutex
}

// BufferOption 缓冲区配置选项
type BufferOption func(*ResultBuffer)

// WithBatchSize 设置批量大小
func WithBatchSize(size int) BufferOption {
	return func(b *ResultBuffer) {
		if size > 0 && size <= maxBatchSize {
			b.batchSize = size
		}
	}
}

// WithFlushInterval 设置刷新间隔
func WithFlushInterval(interval time.Duration) BufferOption {
	return func(b *ResultBuffer) {
		if interval > 0 {
			b.flushInterval = interval
		}
	}
}

// WithBufferFile 设置缓冲区文件路径
func WithBufferFile(path string) BufferOption {
	return func(b *ResultBuffer) {
		b.bufferFile = path
	}
}

// NewResultBuffer 创建结果缓冲区
func NewResultBuffer(submitter func(ctx context.Context, results []model.AnalysisResult) error, opts ...BufferOption) *ResultBuffer {
	buffer := &ResultBuffer{
		results:       make([]model.AnalysisResult, 0),
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
		bufferFile:    defaultBufferFile,
		submitter:     submitter,
		stopCh:        make(chan struct{}),
	}

	for _, opt := range opts {
		opt(buffer)
	}

	return buffer
}

// Start 启动刷新定时器
func (b *ResultBuffer) Start() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.flushTimer != nil {
		return
	}

	b.flushTimer = time.NewTimer(b.flushInterval)
	b.wg.Add(1)

	go b.flushLoop()
	logger.Info("Result buffer started")
}

// Stop 停止刷新定时器并刷新剩余数据
func (b *ResultBuffer) Stop() {
	b.mu.Lock()
	if b.flushTimer != nil {
		b.flushTimer.Stop()
	}
	close(b.stopCh)
	b.mu.Unlock()

	b.wg.Wait()

	// 刷新剩余数据
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.Flush(ctx); err != nil {
		logger.Errorf("Final flush failed: %v", err)
		// 保存到文件以便恢复
		if err := b.Persist(); err != nil {
			logger.Errorf("Failed to persist buffer: %v", err)
		}
	}

	logger.Info("Result buffer stopped")
}

// Add 添加结果到缓冲区
func (b *ResultBuffer) Add(result model.AnalysisResult) int {
	b.mutex.Lock()
	b.results = append(b.results, result)
	count := len(b.results)
	shouldFlush := count >= b.batchSize
	b.mutex.Unlock()

	if shouldFlush {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := b.Flush(ctx); err != nil {
			logger.Errorf("Auto flush failed: %v", err)
		}
	}

	return count
}

// Flush 立即刷新缓冲区
func (b *ResultBuffer) Flush(ctx context.Context) error {
	b.mutex.Lock()
	if len(b.results) == 0 {
		b.mutex.Unlock()
		return nil
	}

	results := make([]model.AnalysisResult, len(b.results))
	copy(results, b.results)
	b.results = b.results[:0]
	b.mutex.Unlock()

	// 重置定时器
	b.mu.Lock()
	if b.flushTimer != nil {
		b.flushTimer.Reset(b.flushInterval)
	}
	b.mu.Unlock()

	logger.Infof("Flushing %d results", len(results))

	if err := b.submitter(ctx, results); err != nil {
		// 提交失败，恢复数据
		b.mutex.Lock()
		b.results = append(results, b.results...)
		b.mutex.Unlock()
		return fmt.Errorf("submit results: %w", err)
	}

	// 删除持久化文件（如果存在）
	if _, err := os.Stat(b.bufferFile); err == nil {
		if err := os.Remove(b.bufferFile); err != nil {
			logger.Warnf("Failed to remove buffer file: %v", err)
		}
	}

	return nil
}

// Persist 将缓冲区持久化到文件
func (b *ResultBuffer) Persist() error {
	b.mutex.RLock()
	if len(b.results) == 0 {
		b.mutex.RUnlock()
		return nil
	}

	data := bufferData{
		Version:  1,
		SavedAt:  time.Now(),
		Results:  make([]model.AnalysisResult, len(b.results)),
	}
	copy(data.Results, b.results)
	b.mutex.RUnlock()

	jsonData, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal buffer: %w", err)
	}

	if err := os.WriteFile(b.bufferFile, jsonData, 0644); err != nil {
		return fmt.Errorf("write buffer file: %w", err)
	}

	logger.Infof("Buffer persisted to %s (%d results)", b.bufferFile, len(data.Results))
	return nil
}

// Restore 从文件恢复缓冲区
func (b *ResultBuffer) Restore() error {
	data, err := os.ReadFile(b.bufferFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在，正常
		}
		return fmt.Errorf("read buffer file: %w", err)
	}

	var bufferData bufferData
	if err := json.Unmarshal(data, &bufferData); err != nil {
		return fmt.Errorf("unmarshal buffer: %w", err)
	}

	b.mutex.Lock()
	b.results = append(b.results, bufferData.Results...)
	count := len(b.results)
	b.mutex.Unlock()

	logger.Infof("Restored %d results from buffer file", count)

	// 删除已恢复的文件
	if err := os.Remove(b.bufferFile); err != nil {
		logger.Warnf("Failed to remove buffer file after restore: %v", err)
	}

	return nil
}

// Count 获取当前缓冲区中的结果数量
func (b *ResultBuffer) Count() int {
	b.mutex.RLock()
	defer b.mutex.RUnlock()
	return len(b.results)
}

// flushLoop 刷新循环
func (b *ResultBuffer) flushLoop() {
	defer b.wg.Done()

	for {
		select {
		case <-b.flushTimer.C:
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := b.Flush(ctx)
			cancel()

			if err != nil {
				logger.Errorf("Periodic flush failed: %v", err)
			}

			// 重置定时器
			b.mu.Lock()
			if b.flushTimer != nil {
				b.flushTimer.Reset(b.flushInterval)
			}
			b.mu.Unlock()

		case <-b.stopCh:
			return
		}
	}
}

// bufferData 缓冲区数据格式
type bufferData struct {
	Version int                   `json:"version"`
	SavedAt time.Time             `json:"saved_at"`
	Results []model.AnalysisResult `json:"results"`
}

// SetWorkDir 设置工作目录（用于缓冲区文件）
func (b *ResultBuffer) SetWorkDir(dir string) {
	if !filepath.IsAbs(b.bufferFile) {
		b.bufferFile = filepath.Join(dir, b.bufferFile)
	}
}
