package service

import (
	"context"
	"sync"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// TaskScheduler 定时任务调度器
type TaskScheduler struct {
	analysisService AnalysisService
	stopCh          chan struct{}
	wg              sync.WaitGroup
	running         bool
	mu              sync.Mutex
}

// NewTaskScheduler 创建定时任务调度器
func NewTaskScheduler(analysisService AnalysisService) *TaskScheduler {
	return &TaskScheduler{
		analysisService: analysisService,
		stopCh:          make(chan struct{}),
	}
}

// Start 启动定时任务
func (s *TaskScheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		logger.Warn("Task scheduler is already running")
		return
	}

	s.running = true
	s.stopCh = make(chan struct{})

	// 启动清理过期锁任务（每5分钟执行一次）
	s.wg.Add(1)
	go s.cleanExpiredLocksTask()

	logger.Info("Task scheduler started")
}

// Stop 停止定时任务
func (s *TaskScheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return
	}

	close(s.stopCh)
	s.wg.Wait()
	s.running = false

	logger.Info("Task scheduler stopped")
}

// IsRunning 检查调度器是否正在运行
func (s *TaskScheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// cleanExpiredLocksTask 清理过期锁任务
func (s *TaskScheduler) cleanExpiredLocksTask() {
	defer s.wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// 立即执行一次
	s.cleanExpiredLocks()

	for {
		select {
		case <-ticker.C:
			s.cleanExpiredLocks()
		case <-s.stopCh:
			return
		}
	}
}

// cleanExpiredLocks 执行清理过期锁
func (s *TaskScheduler) cleanExpiredLocks() {
	count, err := s.analysisService.CleanExpiredLocks()
	if err != nil {
		logger.Errorf("Failed to clean expired locks: %v", err)
		return
	}
	if count > 0 {
		logger.Infof("Scheduler cleaned %d expired locks", count)
	}
}

// RunOnce 立即执行所有任务（用于测试或手动触发）
func (s *TaskScheduler) RunOnce() {
	s.cleanExpiredLocks()
}

// RunWithContext 使用上下文运行调度器（支持外部取消）
func (s *TaskScheduler) RunWithContext(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		logger.Warn("Task scheduler is already running")
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	// 立即执行一次
	s.cleanExpiredLocks()

	for {
		select {
		case <-ticker.C:
			s.cleanExpiredLocks()
		case <-ctx.Done():
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			logger.Info("Task scheduler stopped due to context cancellation")
			return
		case <-s.stopCh:
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return
		}
	}
}
