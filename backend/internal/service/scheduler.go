package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/davidhoo/relive/pkg/logger"
)

// TaskScheduler 定时任务调度器
type TaskScheduler struct {
	analysisService        AnalysisService
	displayService         DisplayService
	photoService           PhotoService
	mergeSuggestionService PersonMergeSuggestionService
	thumbnailJobRepo       repository.ThumbnailJobRepository
	geocodeJobRepo         repository.GeocodeJobRepository
	peopleJobRepo          repository.PeopleJobRepository
	identityProfileService PersonIdentityProfileService
	writeQueue             *database.WriteQueue
	stopCh                 chan struct{}
	wg                     sync.WaitGroup
	running                bool
	mu                     sync.Mutex
}

// NewTaskScheduler 创建定时任务调度器
func NewTaskScheduler(
	analysisService AnalysisService,
	displayService DisplayService,
	photoService PhotoService,
	mergeSuggestionService PersonMergeSuggestionService,
	thumbnailJobRepo repository.ThumbnailJobRepository,
	geocodeJobRepo repository.GeocodeJobRepository,
	peopleJobRepo repository.PeopleJobRepository,
	identityProfileService PersonIdentityProfileService,
) *TaskScheduler {
	return &TaskScheduler{
		analysisService:        analysisService,
		displayService:         displayService,
		photoService:           photoService,
		mergeSuggestionService: mergeSuggestionService,
		thumbnailJobRepo:       thumbnailJobRepo,
		geocodeJobRepo:         geocodeJobRepo,
		peopleJobRepo:          peopleJobRepo,
		identityProfileService: identityProfileService,
		writeQueue:             database.GetWriteQueue(),
		stopCh:                 make(chan struct{}),
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

	// 启动身份画像后台切片任务（仅非 legacy 模式）
	if s.identityProfileService != nil && s.identityProfileService.Mode() != "legacy" {
		s.wg.Add(1)
		go s.identityProfileSliceTask(1 * time.Minute)
	}

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
	var count int64
	var err error
	// Note: analysisService.CleanExpiredLocks() already uses writeQueue internally;
	// wrapping it here would cause a re-entrant mutex deadlock (sync.Mutex is not re-entrant).
	count, err = s.analysisService.CleanExpiredLocks()
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
	s.runIdentityProfileSlice()
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
	// Note: displayService.GenerateDailyBatch() already uses writeQueue internally;
	// wrapping it here would cause a re-entrant mutex deadlock (sync.Mutex is not re-entrant).
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

// identityProfileSliceTask 周期性执行身份画像后台切片。每个 tick 只调用一次
// RunBackgroundSlice（cooldown 由服务自身保证），不在调度器内循环 drain。
func (s *TaskScheduler) identityProfileSliceTask(interval time.Duration) {
	defer s.wg.Done()

	if interval <= 0 {
		interval = time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	s.runIdentityProfileSlice()

	for {
		select {
		case <-ticker.C:
			s.runIdentityProfileSlice()
		case <-s.stopCh:
			return
		}
	}
}

func (s *TaskScheduler) runIdentityProfileSlice() {
	if s.identityProfileService == nil {
		return
	}
	if err := s.identityProfileService.RunBackgroundSlice(); err != nil {
		logger.Warnf("Failed to run identity profile slice: %v", err)
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
		var count int64
		var err error
		if s.writeQueue != nil {
			err = s.writeQueue.Execute(func() error {
				count, err = s.thumbnailJobRepo.DeleteTerminalBefore(cutoff)
				return err
			})
		} else {
			count, err = s.thumbnailJobRepo.DeleteTerminalBefore(cutoff)
		}
		if err != nil {
			logger.Errorf("Failed to clean terminal thumbnail jobs: %v", err)
		} else if count > 0 {
			logger.Infof("Cleaned %d terminal thumbnail jobs older than 7 days", count)
		}
	}

	if s.geocodeJobRepo != nil {
		var count int64
		var err error
		if s.writeQueue != nil {
			err = s.writeQueue.Execute(func() error {
				count, err = s.geocodeJobRepo.DeleteTerminalBefore(cutoff)
				return err
			})
		} else {
			count, err = s.geocodeJobRepo.DeleteTerminalBefore(cutoff)
		}
		if err != nil {
			logger.Errorf("Failed to clean terminal geocode jobs: %v", err)
		} else if count > 0 {
			logger.Infof("Cleaned %d terminal geocode jobs older than 7 days", count)
		}
	}

	// 人物任务终态记录清理：分批物理删除，避免长事务/写锁
	s.cleanTerminalPeopleJobs(cutoff)
}

// peopleJobsCleanupConfig 人物任务清理参数
type peopleJobsCleanupConfig struct {
	batchSize int // 每批删除 ID 数
	maxPerRun int // 单次运行删除上限，历史积压多轮消化
}

// defaultPeopleJobsCleanupConfig 默认配置：每批 2000，单次上限 50000
func defaultPeopleJobsCleanupConfig() peopleJobsCleanupConfig {
	return peopleJobsCleanupConfig{
		batchSize: 2000,
		maxPerRun: 50000,
	}
}

// cleanTerminalPeopleJobs 分批物理删除早于 cutoff 的终态人物任务（completed/failed/cancelled）。
// 先分页查询待删除 ID，再按 ID 分批删除，每批独立短事务并通过写队列串行执行。
// 设置单次运行删除上限，历史积压通过多轮调度任务逐步消化。
func (s *TaskScheduler) cleanTerminalPeopleJobs(cutoff time.Time) {
	if s.peopleJobRepo == nil {
		return
	}

	cfg := defaultPeopleJobsCleanupConfig()
	result := s.runPeopleJobsCleanup(cutoff, cfg)

	// 可观测性日志：每次运行都记录截止时间、删除数量、批次数、总耗时、是否达上限、错误信息
	errMsg := ""
	if result.err != nil {
		errMsg = result.err.Error()
	}
	logger.Infof(
		"people jobs cleanup: cutoff=%s deleted=%d batches=%d elapsed=%s capped=%v err=%s",
		cutoff.Format(time.RFC3339),
		result.deleted,
		result.batches,
		result.elapsed.Round(time.Millisecond),
		result.capped,
		errMsg,
	)
	if result.err != nil {
		logger.Errorf("Failed to clean terminal people jobs: %v", result.err)
	}
}

// peopleJobsCleanupResult 一次清理运行的统计结果
type peopleJobsCleanupResult struct {
	deleted int64
	batches int
	capped  bool
	elapsed time.Duration
	err     error
}

// runPeopleJobsCleanup 执行实际的分批删除循环，返回统计结果。
// 每批：查询至多 batchSize 个待删除 ID，通过写队列执行 DeleteByIDs（独立短事务）。
// 达到 maxPerRun 或无更多待删除记录时停止。
func (s *TaskScheduler) runPeopleJobsCleanup(cutoff time.Time, cfg peopleJobsCleanupConfig) peopleJobsCleanupResult {
	start := time.Now()
	res := peopleJobsCleanupResult{}

	if cfg.batchSize <= 0 {
		cfg.batchSize = 2000
	}
	if cfg.maxPerRun <= 0 {
		cfg.maxPerRun = 50000
	}

	for res.deleted < int64(cfg.maxPerRun) {
		// 响应停止信号：长积压场景下允许调度器停止时中途退出
		select {
		case <-s.stopCh:
			res.elapsed = time.Since(start)
			return res
		default:
		}

		ids, err := s.peopleJobRepo.ListTerminalIDsBefore(cutoff, cfg.batchSize)
		if err != nil {
			res.err = fmt.Errorf("list terminal people job ids: %w", err)
			res.elapsed = time.Since(start)
			return res
		}
		if len(ids) == 0 {
			break
		}

		var batchDeleted int64
		deleteErr := s.executePeopleJobDelete(ids, &batchDeleted)
		if deleteErr != nil {
			res.err = fmt.Errorf("delete people jobs batch: %w", deleteErr)
			res.elapsed = time.Since(start)
			return res
		}

		res.deleted += batchDeleted
		res.batches++

		// 本批未填满 batch，说明已无更多待删除记录
		if len(ids) < cfg.batchSize {
			break
		}
	}

	if res.deleted >= int64(cfg.maxPerRun) {
		// 再确认是否仍有积压，标记 capped
		remaining, err := s.peopleJobRepo.ListTerminalIDsBefore(cutoff, 1)
		if err != nil {
			res.err = fmt.Errorf("check remaining people jobs: %w", err)
			res.elapsed = time.Since(start)
			return res
		}
		res.capped = len(remaining) > 0
	}

	res.elapsed = time.Since(start)
	return res
}

// executePeopleJobDelete 通过写队列串行执行单批删除（独立短事务）。
func (s *TaskScheduler) executePeopleJobDelete(ids []uint, deleted *int64) error {
	if s.writeQueue != nil {
		return s.writeQueue.Execute(func() error {
			n, err := s.peopleJobRepo.DeleteByIDs(ids)
			*deleted = n
			return err
		})
	}
	n, err := s.peopleJobRepo.DeleteByIDs(ids)
	*deleted = n
	return err
}
