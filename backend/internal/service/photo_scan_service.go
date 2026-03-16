package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/analyzer"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/logger"
)

type activeScanJob struct {
	id              string
	taskType        string
	path            string
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	terminalStatus  string
	terminalMessage string
	mu              sync.RWMutex
}

func (j *activeScanJob) setTerminal(status, message string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.terminalStatus = status
	j.terminalMessage = message
}

func (j *activeScanJob) terminal() (string, string) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.terminalStatus, j.terminalMessage
}

type scanProgress struct {
	mu sync.Mutex

	phase           string
	totalFiles      int
	discoveredFiles int
	processedFiles  int
	newPhotos       int
	updatedPhotos   int
	deletedPhotos   int
	skippedFiles    int
	currentFile     string
	dirty           bool
}

type scanFileTask struct {
	path string
	info os.FileInfo
}

// ScanDirectory 扫描目录
func (s *photoService) ScanDirectory(dir string) ([]*model.Photo, error) {
	var photos []*model.Photo

	// 遍历目录
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录
		if info.IsDir() {
			// 检查是否是排除目录
			if s.shouldExcludeDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// 检查文件格式
		if !s.isSupportedFormat(path) {
			return nil
		}

		// 处理照片
		photo, err := s.processPhoto(path, info)
		if err != nil {
			logger.Warnf("Process photo failed: %s, error: %v", path, err)
			return nil // 继续处理其他文件
		}

		photos = append(photos, photo)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return photos, nil
}

// processPhoto 处理单张照片
func (s *photoService) processPhoto(filePath string, info os.FileInfo) (*model.Photo, error) {
	// 计算文件哈希
	fileHash, err := util.HashFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("hash file: %w", err)
	}

	// 提取 EXIF 信息
	exifData, err := util.ExtractEXIF(filePath)
	if err != nil {
		logger.Warnf("Extract EXIF failed: %s, error: %v", filePath, err)
		exifData = &util.EXIFData{} // 使用空数据
	}
	if exifData == nil {
		exifData = &util.EXIFData{}
	}

	// 获取图片尺寸（如果 EXIF 中没有）
	width := exifData.Width
	height := exifData.Height
	if width == 0 || height == 0 {
		width, height, err = util.GetImageSize(filePath)
		if err != nil {
			logger.Warnf("Get image size failed: %s, error: %v", filePath, err)
			// 使用默认值
			width = 0
			height = 0
		}
	}

	// 获取文件时间
	fileTimes := util.GetFileTimes(info)

	// 构建 Photo 对象
	now := time.Now()
	photo := &model.Photo{
		FilePath:       filePath,
		FileName:       filepath.Base(filePath),
		FileSize:       info.Size(),
		FileHash:       fileHash,
		FileModTime:    &fileTimes.ModTime,
		FileCreateTime: fileTimes.CreateTime,
		TakenAt:        exifData.TakenAt,
		CameraModel:    exifData.CameraModel,
		Width:          width,
		Height:         height,
		Orientation:    exifData.Orientation,
		GPSLatitude:    exifData.GPSLatitude,
		GPSLongitude:   exifData.GPSLongitude,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if photo.GPSLatitude != nil && photo.GPSLongitude != nil {
		photo.GeocodeStatus = model.GeocodeStatusPending
	} else {
		photo.GeocodeStatus = model.GeocodeStatusNone
	}
	photo.GeocodeProvider = ""
	photo.GeocodedAt = nil

	photo.ThumbnailPath = util.GenerateDerivedImagePath(filePath)
	photo.ThumbnailStatus = model.ThumbnailStatusPending
	photo.ThumbnailGeneratedAt = nil

	return photo, nil
}

// CleanupNonExistentPhotos 清理数据库中所有文件已不存在的照片
// 遍历整个数据库，检查每个照片文件是否还存在，不存在的则软删除
func (s *photoService) CleanupNonExistentPhotos() (*model.CleanupPhotosResponse, error) {
	logger.Info("Starting cleanup of non-existent photos")

	// 1. 获取数据库中的所有照片
	allPhotos, err := s.repo.ListAll()
	if err != nil {
		return nil, fmt.Errorf("list all photos: %w", err)
	}

	totalCount := len(allPhotos)
	deletedCount := 0
	skippedCount := 0

	logger.Infof("Found %d photos in database to check", totalCount)

	// 2. 检查每张照片的文件是否存在
	for _, photo := range allPhotos {
		// 检查文件是否存在
		if _, err := os.Stat(photo.FilePath); os.IsNotExist(err) {
			// 文件已不存在，软删除数据库记录
			if err := s.repo.Delete(photo.ID); err != nil {
				logger.Errorf("Soft delete photo failed: id=%d, path=%s, error=%v", photo.ID, photo.FilePath, err)
				continue
			}
			deletedCount++
			logger.Infof("Soft deleted photo (file not exists): id=%d, path=%s", photo.ID, photo.FilePath)
		} else if err != nil {
			// 其他错误（如权限问题），记录警告但不删除
			logger.Warnf("Cannot access photo file: id=%d, path=%s, error=%v", photo.ID, photo.FilePath, err)
			skippedCount++
		}
	}

	logger.Infof("Photo cleanup completed: total=%d, deleted=%d, skipped=%d", totalCount, deletedCount, skippedCount)

	return &model.CleanupPhotosResponse{
		TotalCount:   totalCount,
		DeletedCount: deletedCount,
		SkippedCount: skippedCount,
	}, nil
}

// ==================== Async Scan Methods ====================

func (s *photoService) StartScan(path string) (*model.ScanTask, error) {
	return s.startScanJob(path, model.ScanJobTypeScan, false)
}

func (s *photoService) StartRebuild(path string) (*model.ScanTask, error) {
	return s.startScanJob(path, model.ScanJobTypeRebuild, true)
}

func (s *photoService) StopScanTask(id string) (*model.ScanTask, error) {
	s.taskMutex.RLock()
	active := s.activeJob
	s.taskMutex.RUnlock()

	if active == nil {
		return nil, fmt.Errorf("no active scan task")
	}
	if id != "" && active.id != id {
		return nil, fmt.Errorf("scan task not found")
	}

	now := time.Now()
	active.setTerminal(model.ScanJobStatusStopped, "task stopped by user")
	if err := s.scanJobRepo.UpdateFields(active.id, map[string]interface{}{
		"status":            model.ScanJobStatusStopping,
		"phase":             model.ScanJobPhaseStopping,
		"stop_requested_at": &now,
		"last_heartbeat_at": &now,
	}); err != nil {
		return nil, err
	}
	active.cancel()

	job, err := s.scanJobRepo.GetByID(active.id)
	if err != nil {
		return nil, err
	}
	return scanJobToTask(job), nil
}

func (s *photoService) GetScanTask() *model.ScanTask {
	if s.scanJobRepo == nil {
		return nil
	}
	job, err := s.scanJobRepo.GetLatest()
	if err != nil {
		logger.Warnf("Get latest scan task failed: %v", err)
		return nil
	}
	return scanJobToTask(job)
}

func (s *photoService) HandleShutdown() error {
	s.taskMutex.RLock()
	active := s.activeJob
	s.taskMutex.RUnlock()
	if active == nil {
		if s.scanJobRepo == nil {
			return nil
		}
		return s.scanJobRepo.InterruptNonTerminal("task interrupted by service shutdown")
	}

	now := time.Now()
	active.setTerminal(model.ScanJobStatusInterrupted, "task interrupted by service shutdown")
	if err := s.scanJobRepo.UpdateFields(active.id, map[string]interface{}{
		"status":            model.ScanJobStatusInterrupted,
		"phase":             model.ScanJobPhaseStopping,
		"error_message":     "task interrupted by service shutdown",
		"completed_at":      &now,
		"last_heartbeat_at": &now,
	}); err != nil {
		return err
	}
	active.cancel()
	return nil
}

func (s *photoService) startScanJob(path string, taskType string, rebuild bool) (*model.ScanTask, error) {
	s.taskMutex.Lock()
	if s.activeJob != nil {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("scan task already running")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("path does not exist: %s", path)
	}

	now := time.Now()
	job := &model.ScanJob{
		ID:              fmt.Sprintf("%s_%d", taskType, now.UnixNano()),
		Type:            taskType,
		Status:          model.ScanJobStatusPending,
		Path:            path,
		Phase:           model.ScanJobPhasePending,
		StartedAt:       now,
		LastHeartbeatAt: &now,
	}
	if err := s.scanJobRepo.Create(job); err != nil {
		s.taskMutex.Unlock()
		return nil, fmt.Errorf("create scan job: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runtime := &activeScanJob{
		id:             job.ID,
		taskType:       taskType,
		path:           path,
		ctx:            ctx,
		cancel:         cancel,
		done:           make(chan struct{}),
		terminalStatus: model.ScanJobStatusCompleted,
	}
	s.activeJob = runtime
	s.taskMutex.Unlock()

	logger.Infof("Starting async %s: path=%s, task_id=%s", taskType, path, job.ID)
	go s.runScanTask(runtime, path, rebuild)

	return scanJobToTask(job), nil
}

func (s *photoService) runScanTask(runtime *activeScanJob, path string, rebuild bool) {
	defer func() {
		close(runtime.done)
		s.clearActiveJob(runtime.id)
	}()

	now := time.Now()
	if err := s.scanJobRepo.UpdateFields(runtime.id, map[string]interface{}{
		"status":            model.ScanJobStatusRunning,
		"phase":             model.ScanJobPhaseDiscovering,
		"last_heartbeat_at": &now,
	}); err != nil {
		logger.Errorf("[Task %s] Update start status failed: %v", runtime.id, err)
		return
	}

	workers := s.config.Performance.MaxScanWorkers
	if workers <= 0 {
		workers = 1
	}

	existingPhotos, err := s.repo.ListByPathPrefix(path)
	if err != nil {
		logger.Warnf("[Task %s] Load existing photos failed: %v", runtime.id, err)
		existingPhotos = nil
	}
	existingByPath := make(map[string]*model.Photo, len(existingPhotos))
	for _, photo := range existingPhotos {
		existingByPath[photo.FilePath] = photo
	}

	seenFiles := struct {
		sync.Mutex
		items map[string]struct{}
	}{items: make(map[string]struct{}, workers*2)}

	progress := &scanProgress{phase: model.ScanJobPhaseDiscovering, dirty: true}
	flushStop := make(chan struct{})
	flushDone := make(chan struct{})
	go s.flushScanProgressLoop(runtime.id, progress, flushStop, flushDone)

	var scanNodes []scanTreeNode
	pool := analyzer.NewWorkerPool(workers)
	pool.Start()

	walkErr := filepath.Walk(path, func(currentPath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			progress.incrementSkipped(1)
			return nil
		}

		select {
		case <-runtime.ctx.Done():
			return runtime.ctx.Err()
		default:
		}

		if info.IsDir() {
			if currentPath != path && s.shouldExcludeDir(info.Name()) {
				return filepath.SkipDir
			}
			scanNodes = append(scanNodes, scanTreeNode{Path: currentPath, ModTime: info.ModTime().UnixNano()})
			return nil
		}

		if !s.isSupportedFormat(currentPath) {
			return nil
		}

		progress.onDiscovered(filepath.Base(currentPath))
		task := scanFileTask{path: currentPath, info: info}
		if err := pool.Submit(func(ctx context.Context) error {
			return s.processScanFile(ctx, runtime.id, task, rebuild, existingByPath, &seenFiles, progress)
		}); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			progress.incrementSkipped(1)
			logger.Warnf("[Task %s] Submit scan task failed for %s: %v", runtime.id, currentPath, err)
		}
		return nil
	})

	if walkErr == nil {
		progress.setPhase(model.ScanJobPhaseProcessing)
	}

	pool.Wait()
	close(flushStop)
	<-flushDone

	if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		logger.Errorf("[Task %s] Walk scan path failed: %v", runtime.id, walkErr)
		s.finishScanTask(runtime, progress, model.ScanJobStatusFailed, walkErr.Error(), false, nil)
		return
	}

	if errors.Is(runtime.ctx.Err(), context.Canceled) {
		status, message := runtime.terminal()
		if status == "" {
			status = model.ScanJobStatusStopped
			message = "task cancelled"
		}
		s.finishScanTask(runtime, progress, status, message, false, nil)
		return
	}

	progress.setPhase(model.ScanJobPhaseFinalizing)
	if len(existingPhotos) > 0 {
		for _, existing := range existingPhotos {
			seenFiles.Lock()
			_, ok := seenFiles.items[existing.FilePath]
			seenFiles.Unlock()
			if ok {
				continue
			}
			if err := s.repo.Delete(existing.ID); err != nil {
				logger.Warnf("[Task %s] Delete missing photo failed: %v", runtime.id, err)
				continue
			}
			progress.incrementDeleted(1)
		}
	}

	if err := s.updateScanPathTimestamp(path); err != nil {
		logger.Warnf("[Task %s] Failed to update scan path timestamp: %v", runtime.id, err)
	}
	if err := s.updateScanTreeSnapshotWithSnapshot(path, &scanTreeSnapshot{RootPath: path, GeneratedAt: time.Now(), Nodes: scanNodes}); err != nil {
		logger.Warnf("[Task %s] Failed to update scan tree snapshot: %v", runtime.id, err)
	}

	logger.Infof("[Task %s] Completed: total=%d, new=%d, updated=%d, deleted=%d, skipped=%d",
		runtime.id, progress.totalFilesSnapshot(), progress.newPhotosSnapshot(), progress.updatedPhotosSnapshot(), progress.deletedPhotosSnapshot(), progress.skippedFilesSnapshot())

	if s.thumbnailService != nil {
		if task := s.thumbnailService.GetTaskStatus(); task == nil || (task.Status != model.ScanJobStatusRunning && task.Status != model.ScanJobStatusStopping) {
			if _, err := s.thumbnailService.StartBackground(); err != nil {
				logger.Warnf("[Task %s] Auto start thumbnail background failed: %v", runtime.id, err)
			} else {
				logger.Infof("[Task %s] Thumbnail background started automatically after scan completion", runtime.id)
			}
		}
	}
	if s.geocodeTaskService != nil {
		if task := s.geocodeTaskService.GetTaskStatus(); task == nil || (task.Status != model.ScanJobStatusRunning && task.Status != model.ScanJobStatusStopping) {
			if _, err := s.geocodeTaskService.StartBackground(); err != nil {
				logger.Warnf("[Task %s] Auto start geocode background failed: %v", runtime.id, err)
			} else {
				logger.Infof("[Task %s] Geocode background started automatically after scan completion", runtime.id)
			}
		}
	}

	s.finishScanTask(runtime, progress, model.ScanJobStatusCompleted, "", true, nil)
}

func (s *photoService) processScanFile(ctx context.Context, jobID string, task scanFileTask, rebuild bool, existingByPath map[string]*model.Photo, seenFiles *struct {
	sync.Mutex
	items map[string]struct{}
}, progress *scanProgress) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	progress.setCurrentFile(filepath.Base(task.path))
	existing := existingByPath[task.path]

	// 跳过 excluded 照片，不更新不恢复
	if existing != nil && existing.Status == model.PhotoStatusExcluded {
		seenFiles.Lock()
		seenFiles.items[existing.FilePath] = struct{}{}
		seenFiles.Unlock()
		progress.incrementProcessed(1)
		return nil
	}

	if s.canReuseExistingPhoto(existing, task.info, rebuild) {
		seenFiles.Lock()
		seenFiles.items[existing.FilePath] = struct{}{}
		seenFiles.Unlock()
		progress.incrementProcessed(1)
		return nil
	}

	photo, err := s.processPhotoFunc(task.path, task.info)
	if err != nil {
		logger.Warnf("[Task %s] Process photo failed: %s, error: %v", jobID, task.path, err)
		progress.incrementProcessed(1)
		progress.incrementSkipped(1)
		return nil
	}

	seenFiles.Lock()
	seenFiles.items[photo.FilePath] = struct{}{}
	seenFiles.Unlock()

	existing = existingByPath[photo.FilePath]
	if existing == nil {
		if err := s.repo.Create(photo); err != nil {
			logger.Errorf("[Task %s] Create photo failed: %v", jobID, err)
			progress.incrementSkipped(1)
		} else {
			progress.incrementNew(1)
			s.enqueueThumbnailForPhoto(photo, model.ThumbnailJobSourceScan, thumbnailPriorityScan)
			s.enqueueGeocodeForPhoto(photo, model.GeocodeJobSourceScan, geocodePriorityScan)
		}
		progress.incrementProcessed(1)
		return nil
	}

	if rebuild {
		photo.ID = existing.ID
		s.preserveAnalysisFields(existing, photo)
		if err := s.repo.Update(photo); err != nil {
			logger.Errorf("[Task %s] Update photo failed: %v", jobID, err)
			progress.incrementSkipped(1)
		} else {
			progress.incrementUpdated(1)
			s.enqueueThumbnailForPhoto(photo, model.ThumbnailJobSourceScan, thumbnailPriorityScan)
			s.enqueueGeocodeForPhoto(photo, model.GeocodeJobSourceScan, geocodePriorityScan)
		}
		progress.incrementProcessed(1)
		return nil
	}

	if existing.FileHash != photo.FileHash {
		photo.ID = existing.ID
		s.preserveAnalysisFields(existing, photo)
		if err := s.repo.Update(photo); err != nil {
			logger.Errorf("[Task %s] Update photo failed: %v", jobID, err)
			progress.incrementSkipped(1)
		} else {
			progress.incrementUpdated(1)
			s.enqueueThumbnailForPhoto(photo, model.ThumbnailJobSourceScan, thumbnailPriorityScan)
			s.enqueueGeocodeForPhoto(photo, model.GeocodeJobSourceScan, geocodePriorityScan)
		}
	}

	progress.incrementProcessed(1)
	return nil
}

func (s *photoService) canReuseExistingPhoto(existing *model.Photo, info os.FileInfo, rebuild bool) bool {
	if rebuild || existing == nil || existing.FileModTime == nil {
		return false
	}
	if existing.FileSize != info.Size() {
		return false
	}
	return existing.FileModTime.Equal(info.ModTime())
}

func (s *photoService) preserveAnalysisFields(existing, photo *model.Photo) {
	if existing == nil || photo == nil {
		return
	}
	if existing.Description != "" {
		photo.Description = existing.Description
		photo.MainCategory = existing.MainCategory
		photo.Tags = existing.Tags
	}
	photo.AIAnalyzed = existing.AIAnalyzed
	photo.AnalyzedAt = existing.AnalyzedAt
	photo.AIProvider = existing.AIProvider
	photo.Caption = existing.Caption
	photo.MemoryScore = existing.MemoryScore
	photo.BeautyScore = existing.BeautyScore
	photo.OverallScore = existing.OverallScore
	photo.ScoreReason = existing.ScoreReason
}

func (s *photoService) enqueueGeocodeForPhoto(photo *model.Photo, source string, priority int) {
	if s.geocodeTaskService == nil || photo == nil || photo.ID == 0 {
		return
	}
	if err := s.geocodeTaskService.EnqueuePhoto(photo.ID, source, priority, false); err != nil {
		logger.Warnf("enqueue geocode failed for photo %d: %v", photo.ID, err)
	}
}

func (s *photoService) enqueueThumbnailForPhoto(photo *model.Photo, source string, priority int) {
	if s.thumbnailService == nil || photo == nil || photo.ID == 0 {
		return
	}
	if err := s.thumbnailService.EnqueuePhoto(photo.ID, source, priority, false); err != nil {
		logger.Warnf("enqueue thumbnail failed for photo %d: %v", photo.ID, err)
	}
}

func (s *photoService) clearActiveJob(jobID string) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.activeJob != nil && s.activeJob.id == jobID {
		s.activeJob = nil
	}
}

func (s *photoService) flushScanProgressLoop(jobID string, progress *scanProgress, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.flushScanProgress(jobID, progress, false)
		case <-stop:
			s.flushScanProgress(jobID, progress, true)
			return
		}
	}
}

func (s *photoService) flushScanProgress(jobID string, progress *scanProgress, force bool) {
	fields, ok := progress.snapshotFields(force)
	if !ok {
		return
	}
	now := time.Now()
	fields["last_heartbeat_at"] = &now
	if err := s.scanJobRepo.UpdateFields(jobID, fields); err != nil {
		logger.Warnf("[Task %s] Flush scan progress failed: %v", jobID, err)
	}
}

func (s *photoService) finishScanTask(runtime *activeScanJob, progress *scanProgress, status string, message string, clearError bool, completedAt *time.Time) {
	now := time.Now()
	if completedAt == nil {
		completedAt = &now
	}
	fields, _ := progress.snapshotFields(true)
	fields["status"] = status
	fields["phase"] = progress.phaseSnapshot()
	fields["completed_at"] = completedAt
	fields["last_heartbeat_at"] = completedAt
	fields["current_file"] = ""
	if clearError {
		fields["error_message"] = ""
	} else if message != "" {
		fields["error_message"] = message
	}
	if status == model.ScanJobStatusStopped {
		fields["stop_requested_at"] = completedAt
	}
	if err := s.scanJobRepo.UpdateFields(runtime.id, fields); err != nil {
		logger.Warnf("[Task %s] Finalize scan task failed: %v", runtime.id, err)
	}
}

func scanJobToTask(job *model.ScanJob) *model.ScanTask {
	if job == nil {
		return nil
	}
	return &model.ScanTask{
		ID:              job.ID,
		Status:          job.Status,
		Type:            job.Type,
		Path:            job.Path,
		Phase:           job.Phase,
		TotalFiles:      job.TotalFiles,
		DiscoveredFiles: job.DiscoveredFiles,
		ProcessedFiles:  job.ProcessedFiles,
		NewPhotos:       job.NewPhotos,
		UpdatedPhotos:   job.UpdatedPhotos,
		DeletedPhotos:   job.DeletedPhotos,
		SkippedFiles:    job.SkippedFiles,
		CurrentFile:     job.CurrentFile,
		ErrorMessage:    job.ErrorMessage,
		StartedAt:       job.StartedAt,
		StopRequestedAt: job.StopRequestedAt,
		CompletedAt:     job.CompletedAt,
	}
}

func (p *scanProgress) onDiscovered(fileName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.discoveredFiles++
	p.totalFiles = p.discoveredFiles
	p.currentFile = fileName
	p.dirty = true
}

func (p *scanProgress) incrementProcessed(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.processedFiles += n
	p.dirty = true
}

func (p *scanProgress) incrementNew(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.newPhotos += n
	p.dirty = true
}

func (p *scanProgress) incrementUpdated(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.updatedPhotos += n
	p.dirty = true
}

func (p *scanProgress) incrementDeleted(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deletedPhotos += n
	p.dirty = true
}

func (p *scanProgress) incrementSkipped(n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.skippedFiles += n
	p.dirty = true
}

func (p *scanProgress) setCurrentFile(fileName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentFile = fileName
	p.dirty = true
}

func (p *scanProgress) setPhase(phase string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.phase = phase
	p.dirty = true
}

func (p *scanProgress) snapshotFields(force bool) (map[string]interface{}, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !force && !p.dirty {
		return nil, false
	}
	fields := map[string]interface{}{
		"phase":            p.phase,
		"total_files":      p.totalFiles,
		"discovered_files": p.discoveredFiles,
		"processed_files":  p.processedFiles,
		"new_photos":       p.newPhotos,
		"updated_photos":   p.updatedPhotos,
		"deleted_photos":   p.deletedPhotos,
		"skipped_files":    p.skippedFiles,
		"current_file":     p.currentFile,
	}
	p.dirty = false
	return fields, true
}

func (p *scanProgress) phaseSnapshot() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase
}

func (p *scanProgress) totalFilesSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.totalFiles
}

func (p *scanProgress) newPhotosSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.newPhotos
}

func (p *scanProgress) updatedPhotosSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.updatedPhotos
}

func (p *scanProgress) deletedPhotosSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deletedPhotos
}

func (p *scanProgress) skippedFilesSnapshot() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.skippedFiles
}
