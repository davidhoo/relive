package service

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

const (
	thumbnailPriorityScan    = 50
	thumbnailPriorityManual  = 80
	thumbnailPriorityPassive = 100
)

type ThumbnailService interface {
	StartBackground() (*model.ThumbnailTask, error)
	StopBackground() error
	GetTaskStatus() *model.ThumbnailTask
	GetStats() (*model.ThumbnailStatsResponse, error)
	GetBackgroundLogs() []string
	EnqueuePhoto(photoID uint, source string, priority int, force bool) error
	EnqueueByPath(path string, source string, priority int) (int, error)
	GeneratePhoto(photoID uint, force bool) error
	HandleShutdown() error
}

type thumbnailService struct {
	db        *gorm.DB
	photoRepo repository.PhotoRepository
	jobRepo   repository.ThumbnailJobRepository
	config    *config.Config
	generator *util.ThumbnailGenerator

	taskMutex       sync.RWMutex
	task            *model.ThumbnailTask
	active          *activeThumbnailTask
	backgroundLogMu sync.RWMutex
	backgroundLogs  []string
}

type activeThumbnailTask struct {
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	stop   bool
}

func NewThumbnailService(db *gorm.DB, photoRepo repository.PhotoRepository, jobRepo repository.ThumbnailJobRepository, cfg *config.Config) ThumbnailService {
	return &thumbnailService{
		db:        db,
		photoRepo: photoRepo,
		jobRepo:   jobRepo,
		config:    cfg,
		generator: util.NewThumbnailGenerator(1024, 1024, 90, cfg.Photos.ThumbnailPath),
	}
}

func (s *thumbnailService) StartBackground() (*model.ThumbnailTask, error) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.active != nil {
		return nil, fmt.Errorf("thumbnail task already running")
	}
	now := time.Now()
	task := &model.ThumbnailTask{Status: model.TaskStatusRunning, StartedAt: &now}
	active := &activeThumbnailTask{stopCh: make(chan struct{}), done: make(chan struct{})}
	s.task = task
	s.active = active
	s.resetBackgroundLogs()
	s.appendBackgroundLog("缩略图后台生成已启动")
	go s.runBackground(active)
	return cloneThumbnailTask(task), nil
}

func (s *thumbnailService) StopBackground() error {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.active == nil {
		return fmt.Errorf("thumbnail task not running")
	}
	s.active.mu.Lock()
	if !s.active.stop {
		s.active.stop = true
		close(s.active.stopCh)
	}
	s.active.mu.Unlock()
	if s.task != nil && s.task.Status == model.TaskStatusRunning {
		s.task.Status = model.TaskStatusStopping
		s.appendBackgroundLog("收到停止请求，等待当前任务处理完成")
	}
	return nil
}

func (s *thumbnailService) GetTaskStatus() *model.ThumbnailTask {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()
	return cloneThumbnailTask(s.task)
}

func (s *thumbnailService) GetBackgroundLogs() []string {
	s.backgroundLogMu.RLock()
	defer s.backgroundLogMu.RUnlock()
	logs := make([]string, len(s.backgroundLogs))
	copy(logs, s.backgroundLogs)
	return logs
}

func (s *thumbnailService) GetStats() (*model.ThumbnailStatsResponse, error) {
	stats, err := s.jobRepo.GetStats()
	if err != nil {
		return nil, err
	}
	return &model.ThumbnailStatsResponse{
		Total:      stats.Total,
		Pending:    stats.Pending,
		Queued:     stats.Queued,
		Processing: stats.Processing,
		Completed:  stats.Completed,
		Failed:     stats.Failed,
		Cancelled:  stats.Cancelled,
	}, nil
}

func (s *thumbnailService) HandleShutdown() error {
	s.taskMutex.RLock()
	active := s.active
	s.taskMutex.RUnlock()
	if active == nil {
		return nil
	}
	return s.StopBackground()
}

func (s *thumbnailService) EnqueuePhoto(photoID uint, source string, priority int, force bool) error {
	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		return err
	}
	return s.enqueuePhotoModel(photo, source, priority, force)
}

func (s *thumbnailService) EnqueueByPath(path string, source string, priority int) (int, error) {
	photos, err := s.photoRepo.ListByPathPrefix(path)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, photo := range photos {
		if photo.Status == model.PhotoStatusExcluded {
			continue
		}
		if err := s.enqueuePhotoModel(photo, source, priority, false); err != nil {
			logger.Warnf("enqueue thumbnail by path failed for photo %d: %v", photo.ID, err)
			continue
		}
		count++
	}
	return count, nil
}

// GeneratePhoto 直接为单张照片生成缩略图（同步执行，不经过队列）
func (s *thumbnailService) GeneratePhoto(photoID uint, force bool) error {
	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		return err
	}
	if photo == nil {
		return fmt.Errorf("photo %d not found", photoID)
	}
	if photo.Status == model.PhotoStatusExcluded {
		return fmt.Errorf("photo %d is excluded", photoID)
	}

	thumbnailPath := photo.ThumbnailPath
	if thumbnailPath == "" {
		thumbnailPath = util.GenerateDerivedImagePath(photo.FilePath)
	}

	// 非强制模式下，如果文件已存在则直接标记 ready
	if !force {
		fullPath := filepath.Join(s.config.Photos.ThumbnailPath, thumbnailPath)
		if _, err := os.Stat(fullPath); err == nil {
			now := time.Now()
			return s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
				"thumbnail_path":         thumbnailPath,
				"thumbnail_status":       model.ThumbnailStatusReady,
				"thumbnail_generated_at": &now,
			}).Error
		}
	} else {
		// 强制模式：删除旧文件
		fullPath := filepath.Join(s.config.Photos.ThumbnailPath, thumbnailPath)
		if _, err := os.Stat(fullPath); err == nil {
			if err := os.Remove(fullPath); err != nil {
				logger.Warnf("Failed to remove old thumbnail for photo %d: %v", photo.ID, err)
			}
		}
	}

	relPath, err := s.generator.GenerateThumbnail(photo.FilePath)
	now := time.Now()
	if err != nil {
		_ = s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
			"thumbnail_status": model.ThumbnailStatusFailed,
		}).Error
		return fmt.Errorf("生成缩略图失败: %w", err)
	}

	return s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
		"thumbnail_path":         relPath,
		"thumbnail_status":       model.ThumbnailStatusReady,
		"thumbnail_generated_at": &now,
	}).Error
}

func (s *thumbnailService) enqueuePhotoModel(photo *model.Photo, source string, priority int, force bool) error {
	if photo == nil {
		return fmt.Errorf("photo is nil")
	}
	if photo.Status == model.PhotoStatusExcluded {
		return nil
	}
	if source == "" {
		source = model.ThumbnailJobSourceManual
	}
	if priority <= 0 {
		priority = thumbnailPriorityManual
	}
	thumbnailPath := photo.ThumbnailPath
	if thumbnailPath == "" {
		thumbnailPath = util.GenerateDerivedImagePath(photo.FilePath)
	}
	fullPath := filepath.Join(s.config.Photos.ThumbnailPath, thumbnailPath)

	if !force {
		if _, err := os.Stat(fullPath); err == nil {
			// 文件已存在，更新数据库状态为 ready
			generatedAt := time.Now()
			updateErr := s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
				"thumbnail_path":         thumbnailPath,
				"thumbnail_status":       model.ThumbnailStatusReady,
				"thumbnail_generated_at": &generatedAt,
			}).Error
			if updateErr != nil {
				logger.Warnf("Update photo %d thumbnail status to ready failed: %v", photo.ID, updateErr)
				return updateErr
			}
			logger.Debugf("Photo %d thumbnail already exists, updated status to ready", photo.ID)
			return nil
		}
	} else {
		// 强制重新生成：删除旧缩略图文件
		if _, err := os.Stat(fullPath); err == nil {
			if err := os.Remove(fullPath); err != nil {
				logger.Warnf("Failed to remove old thumbnail for photo %d: %v", photo.ID, err)
			}
		}
	}

	now := time.Now()
	if err := s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
		"thumbnail_path":         thumbnailPath,
		"thumbnail_status":       model.ThumbnailStatusPending,
		"thumbnail_generated_at": nil,
	}).Error; err != nil {
		return err
	}

	activeJob, err := s.jobRepo.GetActiveByPhotoID(photo.ID)
	if err != nil {
		return err
	}
	if activeJob != nil {
		updates := map[string]interface{}{
			"priority":          priority,
			"source":            source,
			"last_requested_at": &now,
		}
		if activeJob.Status == model.ThumbnailJobStatusPending {
			updates["status"] = model.ThumbnailJobStatusQueued
		}
		return s.jobRepo.UpdateFields(activeJob.ID, updates)
	}

	job := &model.ThumbnailJob{
		PhotoID:         photo.ID,
		FilePath:        photo.FilePath,
		Status:          model.ThumbnailJobStatusQueued,
		Priority:        priority,
		Source:          source,
		QueuedAt:        now,
		LastRequestedAt: &now,
	}
	return s.jobRepo.Create(job)
}

func (s *thumbnailService) runBackground(active *activeThumbnailTask) {
	logger.Info("Thumbnail background task starting...")
	s.appendBackgroundLog("缩略图后台生成任务启动中...")

	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("Thumbnail background task panic: %v", r)
			s.appendBackgroundLog(fmt.Sprintf("后台任务异常：%v", r))
		}
		now := time.Now()
		s.taskMutex.Lock()
		if s.task != nil && (s.task.Status == model.TaskStatusRunning || s.task.Status == model.TaskStatusStopping) {
			s.task.Status = model.TaskStatusStopped
			s.task.StoppedAt = &now
		}
		s.appendBackgroundLog("缩略图后台生成已停止")
		s.active = nil
		s.taskMutex.Unlock()
		close(active.done)
		logger.Info("Thumbnail background task stopped")
	}()

	s.appendBackgroundLog("正在检查待处理任务...")
	if err := s.seedPendingJobs(); err != nil {
		logger.Warnf("seed thumbnail jobs failed: %v", err)
		s.appendBackgroundLog(fmt.Sprintf("补齐历史待生成任务失败：%v", err))
	} else {
		s.appendBackgroundLog("已扫描历史照片并补齐缩略图待处理队列")
	}
	logger.Info("Thumbnail seedPendingJobs completed")

	workers := s.config.Performance.MaxThumbnailWorkers
	if workers <= 0 {
		workers = 2
	}
	// 限制并发数以减少 SQLite 锁竞争
	// SQLite 在 WAL 模式下支持并发读，但写仍是串行的
	maxWorkers := 2
	if workers < maxWorkers {
		maxWorkers = workers
	}
	jobCh := make(chan *model.ThumbnailJob, maxWorkers*2)
	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				if err := s.processJob(job); err != nil {
					logger.Warnf("process thumbnail job %d failed: %v", job.ID, err)
				}
				// 任务完成后短暂暂停，减少数据库写入冲突
				time.Sleep(50 * time.Millisecond)
			}
		}()
	}

	s.appendBackgroundLog("开始处理缩略图任务队列...")
	logger.Info("Starting thumbnail job processing loop")
	claimAttempt := 0
	for {
		active.mu.Lock()
		stopRequested := active.stop
		active.mu.Unlock()
		if stopRequested {
			logger.Info("Stop requested, exiting processing loop")
			break
		}

		claimAttempt++
		if claimAttempt%100 == 1 {
			logger.Debugf("Attempting to claim job (attempt %d)", claimAttempt)
		}

		job, err := s.jobRepo.ClaimNextJob()
		if err != nil {
			logger.Warnf("claim thumbnail job failed: %v", err)
			s.appendBackgroundLog(fmt.Sprintf("领取缩略图任务失败：%v", err))
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if job == nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		s.updateTaskProgress(func(task *model.ThumbnailTask) {
			task.CurrentPhotoID = job.PhotoID
			task.CurrentFile = filepath.Base(job.FilePath)
		})
		s.appendBackgroundLog(fmt.Sprintf("开始生成照片 #%d 的缩略图 (%s)", job.PhotoID, filepath.Base(job.FilePath)))
		jobCh <- job
		// 领取任务后短暂暂停，避免过快消耗任务导致数据库竞争
		time.Sleep(100 * time.Millisecond)
	}

	close(jobCh)
	wg.Wait()
}

func (s *thumbnailService) processJob(job *model.ThumbnailJob) error {
	photo, err := s.photoRepo.GetByID(job.PhotoID)
	if err != nil {
		now := time.Now()
		_ = s.jobRepo.UpdateFields(job.ID, map[string]interface{}{"status": model.ThumbnailJobStatusFailed, "last_error": err.Error(), "completed_at": &now})
		return err
	}
	if photo.Status == model.PhotoStatusExcluded {
		now := time.Now()
		_ = s.jobRepo.UpdateFields(job.ID, map[string]interface{}{"status": model.ThumbnailJobStatusCancelled, "completed_at": &now})
		s.updateTaskProgress(func(task *model.ThumbnailTask) { task.ProcessedJobs++ })
		return nil
	}
	relPath, err := s.generator.GenerateThumbnail(photo.FilePath)
	now := time.Now()
	if err != nil {
		// 使用带重试的更新
		_ = s.updatePhotoWithRetry(photo.ID, map[string]interface{}{
			"thumbnail_status": model.ThumbnailStatusFailed,
		})
		_ = s.updateJobWithRetry(job.ID, map[string]interface{}{"status": model.ThumbnailJobStatusFailed, "last_error": err.Error(), "completed_at": &now})
		s.updateTaskProgress(func(task *model.ThumbnailTask) {
			task.ProcessedJobs++
		})
		s.appendBackgroundLog(fmt.Sprintf("生成照片 #%d 缩略图失败：%v", photo.ID, err))
		return err
	}
	// 批量更新 photo 和 job，减少数据库操作次数
	if err := s.updatePhotoWithRetry(photo.ID, map[string]interface{}{
		"thumbnail_path":         relPath,
		"thumbnail_status":       model.ThumbnailStatusReady,
		"thumbnail_generated_at": &now,
	}); err != nil {
		logger.Warnf("update photo %d after thumbnail success failed: %v", photo.ID, err)
	}
	if err := s.updateJobWithRetry(job.ID, map[string]interface{}{"status": model.ThumbnailJobStatusCompleted, "completed_at": &now, "last_error": ""}); err != nil {
		logger.Warnf("update thumbnail job %d status failed: %v", job.ID, err)
	}
	s.updateTaskProgress(func(task *model.ThumbnailTask) {
		task.ProcessedJobs++
	})
	s.appendBackgroundLog(fmt.Sprintf("生成照片 #%d 缩略图成功", photo.ID))
	return nil
}

// updatePhotoWithRetry 带重试机制的 photo 更新
func (s *thumbnailService) updatePhotoWithRetry(photoID uint, updates map[string]interface{}) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		err := s.db.Model(&model.Photo{}).Where("id = ?", photoID).Updates(updates).Error
		if err == nil {
			return nil
		}
		// 检查是否是数据库锁定错误
		if isSQLiteLockError(err) {
			lastErr = err
			time.Sleep(time.Duration(i+1) * 50 * time.Millisecond)
			continue
		}
		return err
	}
	return lastErr
}

// updateJobWithRetry 带重试机制的 job 更新
func (s *thumbnailService) updateJobWithRetry(jobID uint, updates map[string]interface{}) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		err := s.jobRepo.UpdateFields(jobID, updates)
		if err == nil {
			return nil
		}
		lastErr = err
		if isSQLiteLockError(err) {
			time.Sleep(time.Duration(i+1) * 50 * time.Millisecond)
			continue
		}
		return err
	}
	return lastErr
}

// isSQLiteLockError 检查错误是否是 SQLite 锁定错误
func isSQLiteLockError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return containsStr(errStr, "database is locked") ||
		containsStr(errStr, "database table is locked") ||
		containsStr(errStr, "busy")
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func (s *thumbnailService) seedPendingJobs() error {
	logger.Info("Checking thumbnail job stats...")
	// 先检查是否已有足够的待处理任务
	stats, err := s.jobRepo.GetStats()
	if err != nil {
		logger.Errorf("Failed to get thumbnail job stats: %v", err)
		return fmt.Errorf("get thumbnail job stats: %w", err)
	}
	logger.Infof("Thumbnail job stats: pending=%d, queued=%d, processing=%d, completed=%d",
		stats.Pending, stats.Queued, stats.Processing, stats.Completed)

	// 如果已有待处理任务，跳过补齐
	if stats.Pending > 0 || stats.Queued > 0 {
		s.appendBackgroundLog(fmt.Sprintf("已有 %d 个待处理缩略图任务，跳过补齐", stats.Pending+stats.Queued))
		logger.Info("Skipping seedPendingJobs, tasks already exist")
		return nil
	}

	// 检查是否所有照片都已经处理完成（排除 excluded 照片）
	var totalPhotos, readyPhotos int64
	if err := s.db.Model(&model.Photo{}).Where("status = ?", model.PhotoStatusActive).Count(&totalPhotos).Error; err != nil {
		logger.Warnf("Failed to count total photos: %v", err)
	}
	if err := s.db.Model(&model.Photo{}).Where("status = ?", model.PhotoStatusActive).Where("thumbnail_status = ?", model.ThumbnailStatusReady).Count(&readyPhotos).Error; err != nil {
		logger.Warnf("Failed to count ready photos: %v", err)
	}
	if totalPhotos > 0 && totalPhotos == readyPhotos {
		s.appendBackgroundLog(fmt.Sprintf("所有 %d 张照片的缩略图已就绪", totalPhotos))
		logger.Info("All photos have thumbnails ready")
		return nil
	}

	s.appendBackgroundLog("正在扫描历史照片...")
	logger.Info("Scanning photos for thumbnail jobs...")

	// 先收集所有需要处理的照片ID，避免在FindInBatches回调中进行写入操作（排除 excluded 照片）
	var photoIDs []uint
	err = s.db.Model(&model.Photo{}).
		Select("id").
		Where("status = ?", model.PhotoStatusActive).
		Where("thumbnail_status != ? OR thumbnail_status IS NULL OR thumbnail_path = ''", model.ThumbnailStatusReady).
		FindInBatches(&photoIDs, 500, func(tx *gorm.DB, batch int) error {
			logger.Debugf("Collecting batch %d, IDs in batch: %d", batch, len(photoIDs))
			return nil
		}).Error
	if err != nil {
		logger.Errorf("FindInBatches failed: %v", err)
		return err
	}

	if len(photoIDs) == 0 {
		s.appendBackgroundLog("没有需要生成缩略图的照片")
		return nil
	}

	s.appendBackgroundLog(fmt.Sprintf("找到 %d 张需要生成缩略图的照片，开始入队...", len(photoIDs)))
	logger.Infof("Found %d photos needing thumbnails, enqueuing...", len(photoIDs))

	// 分批入队，每批处理完后短暂暂停以减少锁竞争
	batchSize := 50
	successCount := 0
	for i := 0; i < len(photoIDs); i += batchSize {
		end := i + batchSize
		if end > len(photoIDs) {
			end = len(photoIDs)
		}
		batch := photoIDs[i:end]

		for _, id := range batch {
			if err := s.enqueuePhotoWithRetry(id); err != nil {
				if !isSQLiteLockError(err) {
					logger.Warnf("seed thumbnail job failed for photo %d: %v", id, err)
				}
			} else {
				successCount++
			}
		}

		// 每批处理完后短暂暂停，让出时间片并减少锁竞争
		if end < len(photoIDs) {
			time.Sleep(10 * time.Millisecond)
		}
	}

	s.appendBackgroundLog(fmt.Sprintf("已扫描历史照片并补齐 %d 个缩略图待处理队列", successCount))
	logger.Infof("Finished seeding %d thumbnail jobs", successCount)
	return nil
}

// enqueuePhotoWithRetry 带重试的照片入队
func (s *thumbnailService) enqueuePhotoWithRetry(photoID uint) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		photo, err := s.photoRepo.GetByID(photoID)
		if err != nil {
			return err
		}
		if photo == nil {
			return fmt.Errorf("photo %d not found", photoID)
		}
		err = s.enqueuePhotoModel(photo, model.ThumbnailJobSourceManual, thumbnailPriorityManual, false)
		if err == nil {
			return nil
		}
		lastErr = err
		if isSQLiteLockError(err) {
			time.Sleep(time.Duration(i+1) * 50 * time.Millisecond)
			continue
		}
		return err
	}
	return lastErr
}

func (s *thumbnailService) updateTaskProgress(fn func(task *model.ThumbnailTask)) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.task == nil {
		return
	}
	fn(s.task)
}

func (s *thumbnailService) appendBackgroundLog(message string) {
	if message == "" {
		return
	}
	entry := fmt.Sprintf("[%s] %s", time.Now().Format("2006-01-02 15:04:05"), message)
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()
	s.backgroundLogs = append(s.backgroundLogs, entry)
	if len(s.backgroundLogs) > 100 {
		s.backgroundLogs = s.backgroundLogs[len(s.backgroundLogs)-100:]
	}
}

func (s *thumbnailService) resetBackgroundLogs() {
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()
	s.backgroundLogs = make([]string, 0, 100)
}

func cloneThumbnailTask(task *model.ThumbnailTask) *model.ThumbnailTask {
	if task == nil {
		return nil
	}
	copy := *task
	return &copy
}
