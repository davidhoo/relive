package service

import (
	"fmt"
	"strings"
	"sync"
	"time"

	internalgeocode "github.com/davidhoo/relive/internal/geocode"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

const (
	geocodeSourceScan    = "scan"
	geocodeSourcePassive = "passive"
	geocodeSourceManual  = "manual"

	geocodePriorityScan    = 50
	geocodePriorityManual  = 80
	geocodePriorityPassive = 100
)

type GeocodeTaskService interface {
	StartBackground() (*model.GeocodeTask, error)
	StopBackground() error
	GetTaskStatus() *model.GeocodeTask
	GetStats() (*model.GeocodeStatsResponse, error)
	GetBackgroundLogs() []string
	RepairLegacyStatus() (int64, error)
	EnqueuePhoto(photoID uint, source string, priority int, force bool) error
	EnqueueByPath(path string, source string, priority int) (int, error)
	GeocodePhoto(photoID uint) error
	HandleShutdown() error
}

type geocodeTaskService struct {
	db             *gorm.DB
	photoRepo      repository.PhotoRepository
	jobRepo        repository.GeocodeJobRepository
	geocodeService GeocodeService

	taskMutex       sync.RWMutex
	task            *model.GeocodeTask
	active          *activeGeocodeTask
	backgroundLogMu sync.RWMutex
	backgroundLogs  []string
}

type activeGeocodeTask struct {
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	stop   bool
}

func NewGeocodeTaskService(db *gorm.DB, photoRepo repository.PhotoRepository, jobRepo repository.GeocodeJobRepository, geocodeService GeocodeService) GeocodeTaskService {
	return &geocodeTaskService{db: db, photoRepo: photoRepo, jobRepo: jobRepo, geocodeService: geocodeService}
}

func (s *geocodeTaskService) StartBackground() (*model.GeocodeTask, error) {
	if s.geocodeService == nil {
		return nil, fmt.Errorf("geocode service not configured")
	}
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.active != nil {
		return nil, fmt.Errorf("geocode task already running")
	}
	now := time.Now()
	task := &model.GeocodeTask{Status: "running", StartedAt: &now}
	active := &activeGeocodeTask{stopCh: make(chan struct{}), done: make(chan struct{})}
	s.task = task
	s.active = active
	s.resetBackgroundLogs()
	s.appendBackgroundLog("GPS 逆地理编码后台任务已启动")
	go s.runBackground(active)
	return cloneGeocodeTask(task), nil
}

func (s *geocodeTaskService) StopBackground() error {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.active == nil {
		return fmt.Errorf("geocode task not running")
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

func (s *geocodeTaskService) GetTaskStatus() *model.GeocodeTask {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()
	return cloneGeocodeTask(s.task)
}

func (s *geocodeTaskService) RepairLegacyStatus() (int64, error) {
	now := time.Now()
	result := s.db.Model(&model.Photo{}).
		Where("status = ?", model.PhotoStatusActive).
		Where("location != '' AND (geocode_status IS NULL OR geocode_status = '' OR geocode_status = 'none')").
		Updates(map[string]interface{}{
			"geocode_status":   "ready",
			"geocode_provider": gorm.Expr("COALESCE(NULLIF(geocode_provider, ''), ?)", "legacy"),
			"geocoded_at":      gorm.Expr("COALESCE(geocoded_at, updated_at, created_at, ?)", now),
		})
	if result.Error != nil {
		return 0, result.Error
	}
	s.appendBackgroundLog(fmt.Sprintf("历史 GPS 状态修复完成，回填 %d 张照片", result.RowsAffected))
	return result.RowsAffected, nil
}

func (s *geocodeTaskService) GetStats() (*model.GeocodeStatsResponse, error) {
	stats, err := s.jobRepo.GetStats()
	if err != nil {
		return nil, err
	}
	return &model.GeocodeStatsResponse{Total: stats.Total, Pending: stats.Pending, Queued: stats.Queued, Processing: stats.Processing, Completed: stats.Completed, Failed: stats.Failed, Cancelled: stats.Cancelled}, nil
}

func (s *geocodeTaskService) GetBackgroundLogs() []string {
	s.backgroundLogMu.RLock()
	defer s.backgroundLogMu.RUnlock()
	logs := make([]string, len(s.backgroundLogs))
	copy(logs, s.backgroundLogs)
	return logs
}

func (s *geocodeTaskService) HandleShutdown() error {
	s.taskMutex.RLock()
	active := s.active
	s.taskMutex.RUnlock()
	if active == nil {
		return nil
	}
	return s.StopBackground()
}

func (s *geocodeTaskService) EnqueuePhoto(photoID uint, source string, priority int, force bool) error {
	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		return err
	}
	return s.enqueuePhotoModel(photo, source, priority, force)
}

func (s *geocodeTaskService) EnqueueByPath(path string, source string, priority int) (int, error) {
	photos, err := s.photoRepo.ListByPathPrefix(path)
	if err != nil {
		return 0, err
	}
	count := 0
	for i := range photos {
		if photos[i].Status == model.PhotoStatusExcluded {
			continue
		}
		if err := s.enqueuePhotoModel(photos[i], source, priority, false); err != nil {
			logger.Warnf("enqueue geocode by path failed for photo %d: %v", photos[i].ID, err)
			continue
		}
		count++
	}
	return count, nil
}

// GeocodePhoto 直接为单张照片执行 GPS 逆地理编码（同步执行，不经过队列）
func (s *geocodeTaskService) GeocodePhoto(photoID uint) error {
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
	if photo.GPSLatitude == nil || photo.GPSLongitude == nil {
		return fmt.Errorf("照片没有 GPS 坐标")
	}
	if *photo.GPSLatitude == 0 && *photo.GPSLongitude == 0 {
		return fmt.Errorf("GPS 坐标无效 (0, 0)")
	}

	loc, err := s.geocodeService.ReverseGeocode(*photo.GPSLatitude, *photo.GPSLongitude)
	now := time.Now()
	if err != nil {
		_ = s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
			"geocode_status": "failed",
		}).Error
		return fmt.Errorf("GPS 解析失败: %w", err)
	}

	provider := ""
	if loc != nil {
		provider = loc.Provider
	}

	updates := geocodeLocationFields(loc)
	updates["geocode_status"] = "ready"
	updates["geocode_provider"] = provider
	updates["geocoded_at"] = &now

	return s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(updates).Error
}

func (s *geocodeTaskService) enqueuePhotoModel(photo *model.Photo, source string, priority int, force bool) error {
	if photo == nil {
		return fmt.Errorf("photo is nil")
	}
	if photo.Status == model.PhotoStatusExcluded {
		return nil
	}
	// 排除无效 GPS 坐标（为 nil 或为 0,0）
	if photo.GPSLatitude == nil || photo.GPSLongitude == nil {
		return nil
	}
	if *photo.GPSLatitude == 0 && *photo.GPSLongitude == 0 {
		return nil
	}
	if source == "" {
		source = geocodeSourceManual
	}
	if priority <= 0 {
		priority = geocodePriorityManual
	}
	if !force && strings.TrimSpace(photo.Location) != "" && (photo.GeocodeStatus == "ready" || photo.GeocodeStatus == "" || photo.GeocodeStatus == "none") {
		now := time.Now()
		return s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
			"geocode_status": "ready",
			"geocoded_at":    gorm.Expr("COALESCE(geocoded_at, ?)", &now),
		}).Error
	}
	now := time.Now()
	if err := s.db.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
		"geocode_status": "pending",
		"geocoded_at":    nil,
	}).Error; err != nil {
		return err
	}
	activeJob, err := s.jobRepo.GetActiveByPhotoID(photo.ID)
	if err != nil {
		return err
	}
	if activeJob != nil {
		return s.jobRepo.UpdateFields(activeJob.ID, map[string]interface{}{"priority": priority, "source": source, "last_requested_at": &now, "status": "queued"})
	}
	job := &model.GeocodeJob{PhotoID: photo.ID, Status: "queued", Priority: priority, Source: source, QueuedAt: now, LastRequestedAt: &now}
	return s.jobRepo.Create(job)
}

func (s *geocodeTaskService) runBackground(active *activeGeocodeTask) {
	defer func() {
		now := time.Now()
		s.taskMutex.Lock()
		if s.task != nil && (s.task.Status == "running" || s.task.Status == "stopping") {
			s.task.Status = "stopped"
			s.task.StoppedAt = &now
		}
		s.appendBackgroundLog("GPS 逆地理编码后台任务已停止")
		s.active = nil
		s.taskMutex.Unlock()
		close(active.done)
	}()
	if err := s.seedPendingJobs(); err != nil {
		s.appendBackgroundLog(fmt.Sprintf("补齐历史待解析任务失败：%v", err))
	}
	workers := 1
	if svc, ok := s.geocodeService.(*geocodeService); ok {
		_ = svc
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
			s.appendBackgroundLog(fmt.Sprintf("领取 geocode 任务失败：%v", err))
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if job == nil {
			time.Sleep(800 * time.Millisecond)
			continue
		}
		// 提前检查坐标，避免无效请求
		photo, err := s.photoRepo.GetByID(job.PhotoID)
		if err != nil {
			s.appendBackgroundLog(fmt.Sprintf("获取照片 #%d 失败：%v", job.PhotoID, err))
			continue
		}
		if photo.GPSLatitude == nil || photo.GPSLongitude == nil ||
			(*photo.GPSLatitude == 0 && *photo.GPSLongitude == 0) {
			now := time.Now()
			_ = s.updateJobWithRetry(job.ID, map[string]interface{}{"status": "cancelled", "completed_at": &now})
			s.updateTaskProgress(func(task *model.GeocodeTask) { task.ProcessedJobs++ })
			continue
		}
		if photo.Status == model.PhotoStatusExcluded {
			now := time.Now()
			_ = s.updateJobWithRetry(job.ID, map[string]interface{}{"status": "cancelled", "completed_at": &now})
			s.updateTaskProgress(func(task *model.GeocodeTask) { task.ProcessedJobs++ })
			continue
		}
		s.updateTaskProgress(func(task *model.GeocodeTask) { task.CurrentPhotoID = job.PhotoID })
		s.appendBackgroundLog(fmt.Sprintf("开始解析照片 #%d 的 GPS 位置", job.PhotoID))
		_ = workers
		if err := s.processJob(job, photo); err != nil {
			logger.Warnf("process geocode job %d failed: %v", job.ID, err)
		}
	}
}

func (s *geocodeTaskService) processJob(job *model.GeocodeJob, photo *model.Photo) error {
	// 坐标检查已在外层完成，直接执行 geocode
	loc, err := s.geocodeService.ReverseGeocode(*photo.GPSLatitude, *photo.GPSLongitude)
	now := time.Now()
	if err != nil {
		_ = s.updatePhotoWithRetry(photo.ID, map[string]interface{}{"geocode_status": "failed"})
		_ = s.updateJobWithRetry(job.ID, map[string]interface{}{"status": "failed", "last_error": err.Error(), "completed_at": &now})
		s.updateTaskProgress(func(task *model.GeocodeTask) { task.ProcessedJobs++ })
		s.appendBackgroundLog(fmt.Sprintf("解析照片 #%d 位置失败：%v", photo.ID, err))
		return err
	}
	provider := ""
	if loc != nil {
		provider = loc.Provider
	}
	updates := geocodeLocationFields(loc)
	updates["geocode_status"] = "ready"
	updates["geocode_provider"] = provider
	updates["geocoded_at"] = &now
	if err := s.updatePhotoWithRetry(photo.ID, updates); err != nil {
		logger.Warnf("update photo %d after geocode success failed: %v", photo.ID, err)
	}
	if err := s.updateJobWithRetry(job.ID, map[string]interface{}{"status": "completed", "completed_at": &now, "last_error": ""}); err != nil {
		logger.Warnf("update geocode job %d status failed: %v", job.ID, err)
	}
	s.updateTaskProgress(func(task *model.GeocodeTask) { task.ProcessedJobs++ })
	s.appendBackgroundLog(fmt.Sprintf("解析照片 #%d 位置成功（provider=%s）", photo.ID, provider))
	return nil
}

// updatePhotoWithRetry 带重试机制的 photo 更新
func (s *geocodeTaskService) updatePhotoWithRetry(photoID uint, updates map[string]interface{}) error {
	var lastErr error
	for i := 0; i < 3; i++ {
		err := s.db.Model(&model.Photo{}).Where("id = ?", photoID).Updates(updates).Error
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

// updateJobWithRetry 带重试机制的 job 更新
func (s *geocodeTaskService) updateJobWithRetry(jobID uint, updates map[string]interface{}) error {
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

func (s *geocodeTaskService) seedPendingJobs() error {
	// 先检查是否已有足够的待处理任务
	stats, err := s.jobRepo.GetStats()
	if err != nil {
		return fmt.Errorf("get geocode job stats: %w", err)
	}
	// 如果已有待处理任务，跳过补齐
	if stats.Pending > 0 || stats.Queued > 0 {
		s.appendBackgroundLog(fmt.Sprintf("已有 %d 个待处理 geocode 任务，跳过补齐", stats.Pending+stats.Queued))
		return nil
	}

	var photos []model.Photo
	// 排除 GPS 为 0,0 的无效坐标，排除 excluded 照片
	err = s.db.Model(&model.Photo{}).
		Where("status = ?", model.PhotoStatusActive).
		Where("gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL").
		Where("gps_latitude != 0 OR gps_longitude != 0").
		Where("(geocode_status != ? OR geocode_status IS NULL)", "ready").
		FindInBatches(&photos, 200, func(tx *gorm.DB, batch int) error {
			for i := range photos {
				if err := s.enqueuePhotoModel(&photos[i], geocodeSourceManual, geocodePriorityManual, false); err != nil {
					if !isSQLiteLockError(err) {
						logger.Warnf("seed geocode job failed for photo %d: %v", photos[i].ID, err)
					}
				}
			}
			return nil
		}).Error
	return err
}

func (s *geocodeTaskService) updateTaskProgress(fn func(task *model.GeocodeTask)) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.task == nil {
		return
	}
	fn(s.task)
}

func (s *geocodeTaskService) appendBackgroundLog(message string) {
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

func (s *geocodeTaskService) resetBackgroundLogs() {
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()
	s.backgroundLogs = make([]string, 0, 100)
}

func cloneGeocodeTask(task *model.GeocodeTask) *model.GeocodeTask {
	if task == nil {
		return nil
	}
	cp := *task
	return &cp
}

func formatGeocodeLocation(loc *internalgeocode.Location) string {
	if loc == nil {
		return ""
	}
	return loc.FormatDisplay()
}

// geocodeLocationFields 从 geocode.Location 提取所有位置字段为 map，供 processJob 和 GeocodePhoto 共用
func geocodeLocationFields(loc *internalgeocode.Location) map[string]interface{} {
	fields := map[string]interface{}{
		"location": "",
		"country":  "",
		"province": "",
		"city":     "",
		"district": "",
		"street":   "",
		"poi":      "",
	}
	if loc != nil {
		fields["location"] = loc.FormatDisplay()
		fields["country"] = loc.Country
		fields["province"] = loc.Province
		fields["city"] = loc.City
		fields["district"] = loc.District
		fields["street"] = loc.Street
		fields["poi"] = loc.POI
	}
	return fields
}
