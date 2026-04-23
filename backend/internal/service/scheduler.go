package service

import (
	"context"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
)

// TaskScheduler 定时任务调度器
type TaskScheduler struct {
	analysisService         AnalysisService
	displayService          DisplayService
	photoService            PhotoService
	mergeSuggestionService  PersonMergeSuggestionService
	thumbnailJobRepo        repository.ThumbnailJobRepository
	geocodeJobRepo          repository.GeocodeJobRepository
	stopCh                  chan struct{}
	wg                      sync.WaitGroup
	running                 bool
	mu                      sync.Mutex
}

// NewTaskScheduler 创建定时任务调度器
func NewTaskScheduler(
	analysisService AnalysisService,
	displayService DisplayService,
	photoService PhotoService,
	mergeSuggestionService PersonMergeSuggestionService,
	thumbnailJobRepo repository.ThumbnailJobRepository,
	geocodeJobRepo repository.GeocodeJobRepository,
) *TaskScheduler {
	return &TaskScheduler{
		analysisService:         analysisService,
		displayService:          displayService,
		photoService:            photoService,
		mergeSuggestionService:  mergeSuggestionService,
		thumbnailJobRepo:        thumbnailJobRepo,
		geocodeJobRepo:          geocodeJobRepo,
		stopCh:                  make(chan struct{}),
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

	// 启动每日展示批次确保任务
	s.wg.Add(1)
	go s.ensureDailyBatchTask()

	// 启动自动扫描检查任务
	s.wg.Add(1)
	go s.autoScanCheckTask()

	// 启动人物合并建议切片任务
	s.wg.Add(1)
	go s.mergeSuggestionSliceTask(1 * time.Minute)

	// 启动已完成任务清理（每6小时执行一次，清理7天前的终态记录）
	s.wg.Add(1)
	go s.cleanTerminalJobsTask()

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

	// 立即执行一次（仅清理锁，其他任务由各自的 goroutine 负责）
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
	s.ensureTodayDailyBatch()
	s.runAutoScanCheck()
	s.runMergeSuggestionSlice()
	s.cleanTerminalJobs()
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
	s.ensureTodayDailyBatch()

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

func (s *TaskScheduler) ensureDailyBatchTask() {
	defer s.wg.Done()

	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	s.ensureTodayDailyBatch()

	for {
		select {
		case <-ticker.C:
			s.ensureTodayDailyBatch()
		case <-s.stopCh:
			return
		}
	}
}

func (s *TaskScheduler) ensureTodayDailyBatch() {
	if s.displayService == nil {
		return
	}
	if _, err := s.displayService.GenerateDailyBatch(time.Now(), false); err != nil {
		logger.Warnf("Failed to ensure daily display batch: %v", err)
	}
}

func (s *TaskScheduler) autoScanCheckTask() {
	defer s.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// 启动后延迟 15 秒再首次扫描，避免与其他启动任务（迁移、锁清理等）争用数据库
	select {
	case <-time.After(15 * time.Second):
		s.runAutoScanCheck()
	case <-s.stopCh:
		return
	}

	for {
		select {
		case <-ticker.C:
			s.runAutoScanCheck()
		case <-s.stopCh:
			return
		}
	}
}

func (s *TaskScheduler) runAutoScanCheck() {
	if s.photoService == nil {
		return
	}
	if err := s.photoService.RunAutoScanCheck(); err != nil {
		logger.Warnf("Failed to run auto scan check: %v", err)
	}
}

func (s *TaskScheduler) mergeSuggestionSliceTask(interval time.Duration) {
	defer s.wg.Done()

	if interval <= 0 {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.runMergeSuggestionSlice()

	for {
		select {
		case <-ticker.C:
			s.runMergeSuggestionSlice()
		case <-s.stopCh:
			return
		}
	}
}

func (s *TaskScheduler) runMergeSuggestionSlice() {
	if s.mergeSuggestionService == nil {
		return
	}
	if err := s.mergeSuggestionService.RunBackgroundSlice(); err != nil {
		logger.Warnf("Failed to run merge suggestion slice: %v", err)
	}
}

// cleanTerminalJobsTask 定期清理已完成/失败/取消的任务记录
func (s *TaskScheduler) cleanTerminalJobsTask() {
	defer s.wg.Done()

	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	// 启动后延迟 10 分钟再首次清理，避免启动时并发压力
	select {
	case <-time.After(10 * time.Minute):
		s.cleanTerminalJobs()
	case <-s.stopCh:
		return
	}

	for {
		select {
		case <-ticker.C:
			s.cleanTerminalJobs()
		case <-s.stopCh:
			return
		}
	}
}

// cleanTerminalJobs 清理 7 天前的终态任务记录
func (s *TaskScheduler) cleanTerminalJobs() {
	cutoff := time.Now().AddDate(0, 0, -7)

	if s.thumbnailJobRepo != nil {
		if count, err := s.thumbnailJobRepo.DeleteTerminalBefore(cutoff); err != nil {
			logger.Errorf("Failed to clean terminal thumbnail jobs: %v", err)
		} else if count > 0 {
			logger.Infof("Cleaned %d terminal thumbnail jobs older than 7 days", count)
		}
	}

	if s.geocodeJobRepo != nil {
		if count, err := s.geocodeJobRepo.DeleteTerminalBefore(cutoff); err != nil {
			logger.Errorf("Failed to clean terminal geocode jobs: %v", err)
		} else if count > 0 {
			logger.Infof("Cleaned %d terminal geocode jobs older than 7 days", count)
		}
	}
}
