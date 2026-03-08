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
	thumbnailSourceScan    = "scan"
	thumbnailSourcePassive = "passive"
	thumbnailSourceManual  = "manual"

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
	EnqueuePhoto(photoID uint, source string, priority int) error
	EnqueueByPath(path string, source string, priority int) (int, error)
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
	task := &model.ThumbnailTask{Status: "running", StartedAt: &now}
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
	if s.task != nil && s.task.Status == "running" {
		s.task.Status = "stopping"
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

func (s *thumbnailService) EnqueuePhoto(photoID uint, source string, priority int) error {
	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		return err
	}
	return s.enqueuePhotoModel(photo, source, priority)
}

func (s *thumbnailService) EnqueueByPath(path string, source string, priority int) (int, error) {
	photos, err := s.photoRepo.ListByPathPrefix(path)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, photo := range photos {
		if err := s.enqueuePhotoModel(photo, source, priority); err != nil {
			logger.Warnf("enqueue thumbnail by path failed for photo %d: %v", photo.ID, err)
			continue
		}
		count++
	}
	return count, nil
}

func (s *thumbnailService) enqueuePhotoModel(photo *model.Photo, source string, priority int) error {
	if photo == nil {
		return fmt.Errorf("photo is nil")
	}
	if source == "" {
		source = thumbnailSourceManual
	}
	if priority <= 0 {
		priority = thumbnailPriorityManual
	}
	thumbnailPath := photo.ThumbnailPath
	if thumbnailPath == "" {
		thumbnailPath = util.GenerateDerivedImagePath(photo.FilePath)
	}
	fullPath := filepath.Join(s.config.Photos.ThumbnailPath, thumbnailPath)
	if _, err := os.Stat(fullPath); err == nil {
		generatedAt := time.Now()
		return s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
			"thumbnail_path":         thumbnailPath,
			"thumbnail_status":       "ready",
			"thumbnail_generated_at": &generatedAt,
		}).Error
	}

	now := time.Now()
	if err := s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
		"thumbnail_path":         thumbnailPath,
		"thumbnail_status":       "pending",
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
		if activeJob.Status == "pending" {
			updates["status"] = "queued"
		}
		return s.jobRepo.UpdateFields(activeJob.ID, updates)
	}

	job := &model.ThumbnailJob{
		PhotoID:         photo.ID,
		FilePath:        photo.FilePath,
		Status:          "queued",
		Priority:        priority,
		Source:          source,
		QueuedAt:        now,
		LastRequestedAt: &now,
	}
	return s.jobRepo.Create(job)
}

func (s *thumbnailService) runBackground(active *activeThumbnailTask) {
	defer func() {
		now := time.Now()
		s.taskMutex.Lock()
		if s.task != nil && (s.task.Status == "running" || s.task.Status == "stopping") {
			s.task.Status = "stopped"
			s.task.StoppedAt = &now
		}
		s.appendBackgroundLog("缩略图后台生成已停止")
		s.active = nil
		s.taskMutex.Unlock()
		close(active.done)
	}()

	if err := s.seedPendingJobs(); err != nil {
		logger.Warnf("seed thumbnail jobs failed: %v", err)
		s.appendBackgroundLog(fmt.Sprintf("补齐历史待生成任务失败：%v", err))
	} else {
		s.appendBackgroundLog("已扫描历史照片并补齐缩略图待处理队列")
	}

	workers := s.config.Performance.MaxThumbnailWorkers
	if workers <= 0 {
		workers = 2
	}
	jobCh := make(chan *model.ThumbnailJob, workers*2)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				if err := s.processJob(job); err != nil {
					logger.Warnf("process thumbnail job %d failed: %v", job.ID, err)
				}
			}
		}()
	}

	for {
		active.mu.Lock()
		stopRequested := active.stop
		active.mu.Unlock()
		if stopRequested {
			break
		}

		job, err := s.jobRepo.ClaimNextJob()
		if err != nil {
			logger.Warnf("claim thumbnail job failed: %v", err)
			s.appendBackgroundLog(fmt.Sprintf("领取缩略图任务失败：%v", err))
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if job == nil {
			time.Sleep(800 * time.Millisecond)
			continue
		}

		s.updateTaskProgress(func(task *model.ThumbnailTask) {
			task.CurrentPhotoID = job.PhotoID
			task.CurrentFile = filepath.Base(job.FilePath)
		})
		s.appendBackgroundLog(fmt.Sprintf("开始生成照片 #%d 的缩略图 (%s)", job.PhotoID, filepath.Base(job.FilePath)))
		jobCh <- job
	}

	close(jobCh)
	wg.Wait()
}

func (s *thumbnailService) processJob(job *model.ThumbnailJob) error {
	photo, err := s.photoRepo.GetByID(job.PhotoID)
	if err != nil {
		now := time.Now()
		_ = s.jobRepo.UpdateFields(job.ID, map[string]interface{}{"status": "failed", "last_error": err.Error(), "completed_at": &now})
		return err
	}
	relPath, err := s.generator.GenerateThumbnail(photo.FilePath)
	now := time.Now()
	if err != nil {
		_ = s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
			"thumbnail_status": "failed",
		}).Error
		_ = s.jobRepo.UpdateFields(job.ID, map[string]interface{}{"status": "failed", "last_error": err.Error(), "completed_at": &now})
		s.updateTaskProgress(func(task *model.ThumbnailTask) {
			task.ProcessedJobs++
		})
		s.appendBackgroundLog(fmt.Sprintf("生成照片 #%d 缩略图失败：%v", photo.ID, err))
		return err
	}
	if err := s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
		"thumbnail_path":         relPath,
		"thumbnail_status":       "ready",
		"thumbnail_generated_at": &now,
	}).Error; err != nil {
		return err
	}
	if err := s.jobRepo.UpdateFields(job.ID, map[string]interface{}{"status": "completed", "completed_at": &now, "last_error": ""}); err != nil {
		return err
	}
	s.updateTaskProgress(func(task *model.ThumbnailTask) {
		task.ProcessedJobs++
	})
	s.appendBackgroundLog(fmt.Sprintf("生成照片 #%d 缩略图成功", photo.ID))
	return nil
}

func (s *thumbnailService) seedPendingJobs() error {
	var photos []model.Photo
	return s.db.Model(&model.Photo{}).
		Where("thumbnail_status != ? OR thumbnail_status IS NULL OR thumbnail_path = ''", "ready").
		FindInBatches(&photos, 200, func(tx *gorm.DB, batch int) error {
			for i := range photos {
				if err := s.enqueuePhotoModel(&photos[i], thumbnailSourceManual, thumbnailPriorityManual); err != nil {
					logger.Warnf("seed thumbnail job failed for photo %d: %v", photos[i].ID, err)
				}
			}
			return nil
		}).Error
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
