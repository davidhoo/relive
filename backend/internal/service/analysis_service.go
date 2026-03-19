package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// 错误定义
var (
	ErrTaskNotFound      = errors.New("task not found or expired")
	ErrTaskLockedByOther = errors.New("task locked by another analyzer")
	ErrInvalidResult     = errors.New("invalid analysis result")
)

// AnalysisService 分析服务接口
type AnalysisService interface {
	GetPendingTasks(limit int, analyzerID string) ([]model.AnalysisTask, int64, error)
	ExtendTaskLock(taskID, analyzerID string) (time.Time, error)
	ReleaseTask(taskID, analyzerID, reason, errorMsg string, retryLater bool) error
	SubmitResults(results []model.AnalysisResult, deviceID uint) (*model.SubmitResultsResponse, error)
	SubmitResultsDirectly(results []model.AnalysisResult, deviceID uint) (*model.SubmitResultsResponse, error)
	GetStats(deviceID uint) (*model.AnalyzerStatsResponse, error)
	CleanExpiredLocks() (int64, error)
	SetResultQueue(queue *ResultQueue)
}

// analysisService 分析服务实现
type analysisService struct {
	db           *gorm.DB
	photoRepo    repository.PhotoRepository
	photoTagRepo repository.PhotoTagRepository
	cfg          *config.Config
	resultQueue  *ResultQueue
}

// NewAnalysisService 创建分析服务
func NewAnalysisService(db *gorm.DB, photoRepo repository.PhotoRepository, photoTagRepo repository.PhotoTagRepository, cfg *config.Config) AnalysisService {
	return &analysisService{
		db:           db,
		photoRepo:    photoRepo,
		photoTagRepo: photoTagRepo,
		cfg:          cfg,
	}
}

// SetResultQueue 设置结果队列（必须在 Start 之前调用）
func (s *analysisService) SetResultQueue(queue *ResultQueue) {
	s.resultQueue = queue
}

// GetPendingTasks 获取待分析任务列表
func (s *analysisService) GetPendingTasks(limit int, analyzerID string) ([]model.AnalysisTask, int64, error) {
	var tasks []model.AnalysisTask
	var totalRemaining int64

	// 1. 统计剩余待分析数量
	// 注意：不再要求 geocode_status = ready，分析不应依赖地理编码完成
	err := s.db.Model(&model.Photo{}).
		Where(`status = ?
			AND ai_analyzed = ?
			AND thumbnail_status = ?
			AND (analysis_lock_expired_at IS NULL OR analysis_lock_expired_at < ?)
			AND analysis_retry_count < ?`,
			model.PhotoStatusActive, false, model.ThumbnailStatusReady, time.Now(), 10).
		Count(&totalRemaining).Error
	if err != nil {
		return nil, 0, err
	}

	// 2. 获取待分析的照片（使用行级锁模拟：更新 lock 字段）
	// SQLite 下使用单个 UPDATE 语句来"锁定"记录
	lockExpiredAt := time.Now().Add(5 * time.Minute)

	// 先更新一批记录来"锁定"它们
	result := s.db.Model(&model.Photo{}).
		Where(`id IN (
			SELECT id FROM photos
			WHERE status = ?
			  AND ai_analyzed = ?
			  AND thumbnail_status = ?
			  AND (analysis_lock_expired_at IS NULL OR analysis_lock_expired_at < ?)
			  AND analysis_retry_count < ?
			  AND deleted_at IS NULL
			ORDER BY id ASC
			LIMIT ?
		)`, model.PhotoStatusActive, false, model.ThumbnailStatusReady, time.Now(), 10, limit).
		Updates(map[string]interface{}{
			"analysis_lock_id":         analyzerID,
			"analysis_lock_expired_at": lockExpiredAt,
		})

	if result.Error != nil {
		return nil, 0, result.Error
	}

	// 3. 查询刚刚被锁定的照片
	var photos []model.Photo
	err = s.db.Where("analysis_lock_id = ? AND analysis_lock_expired_at = ?",
		analyzerID, lockExpiredAt).
		Find(&photos).Error
	if err != nil {
		return nil, 0, err
	}

	// 4. 构建任务响应
	tasks = make([]model.AnalysisTask, 0, len(photos))
	baseURL := s.cfg.Server.ExternalURL
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
	}
	baseURL = strings.TrimSuffix(baseURL, "/")

	for _, photo := range photos {
		downloadURL := fmt.Sprintf("%s/api/v1/photos/%d/image", baseURL, photo.ID)

		task := model.AnalysisTask{
			ID:                     fmt.Sprintf("task_%d_%d", photo.ID, time.Now().Unix()),
			PhotoID:                photo.ID,
			FilePath:               photo.FilePath,
			DownloadURL:            downloadURL,
			Width:                  photo.Width,
			Height:                 photo.Height,
			TakenAt:                photo.TakenAt,
			Location:               photo.Location,
			CameraModel:            photo.CameraModel,
			LockExpiresAt:          &lockExpiredAt,
		}
		tasks = append(tasks, task)
	}

	return tasks, totalRemaining, nil
}

// ExtendTaskLock 续期任务锁
func (s *analysisService) ExtendTaskLock(taskID, analyzerID string) (time.Time, error) {
	// 解析 taskID 获取 photoID
	// 格式：task_{photo_id}_{timestamp}
	var photoID uint
	_, err := fmt.Sscanf(taskID, "task_%d_", &photoID)
	if err != nil {
		return time.Time{}, ErrTaskNotFound
	}

	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, ErrTaskNotFound
		}
		return time.Time{}, err
	}

	// 检查锁是否属于当前分析器
	if photo.AnalysisLockID == nil || *photo.AnalysisLockID != analyzerID {
		return time.Time{}, ErrTaskLockedByOther
	}

	// 检查锁是否已过期
	if photo.AnalysisLockExpiredAt != nil && photo.AnalysisLockExpiredAt.Before(time.Now()) {
		return time.Time{}, ErrTaskLockedByOther
	}

	// 续期锁
	newExpiredAt := time.Now().Add(5 * time.Minute)
	err = s.photoRepo.UpdateFields(photoID, map[string]interface{}{
		"analysis_lock_expired_at": newExpiredAt,
	})
	if err != nil {
		return time.Time{}, err
	}

	return newExpiredAt, nil
}

// ReleaseTask 释放任务
func (s *analysisService) ReleaseTask(taskID, analyzerID, reason, errorMsg string, retryLater bool) error {
	// 解析 taskID 获取 photoID
	var photoID uint
	_, err := fmt.Sscanf(taskID, "task_%d_", &photoID)
	if err != nil {
		return ErrTaskNotFound
	}

	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTaskNotFound
		}
		return err
	}

	// 检查锁是否属于当前分析器
	if photo.AnalysisLockID == nil || *photo.AnalysisLockID != analyzerID {
		return ErrTaskLockedByOther
	}

	updates := map[string]interface{}{
		"analysis_lock_id":         nil,
		"analysis_lock_expired_at": nil,
	}

	// 如果不允许稍后重试，增加重试计数
	if !retryLater {
		updates["analysis_retry_count"] = gorm.Expr("analysis_retry_count + 1")
	}

	err = s.photoRepo.UpdateFields(photoID, updates)
	if err != nil {
		return err
	}

	logger.Infof("Task %s released by analyzer %s, reason: %s", taskID, analyzerID, reason)
	return nil
}

// SubmitResults 提交分析结果（使用队列，立即返回）
func (s *analysisService) SubmitResults(results []model.AnalysisResult, deviceID uint) (*model.SubmitResultsResponse, error) {
	// 如果队列未初始化，直接写入（向后兼容）
	if s.resultQueue == nil {
		logger.Warn("ResultQueue not set, using direct write")
		return s.SubmitResultsDirectly(results, deviceID)
	}

	// 入队（立即返回，不等待数据库写入）
	return s.resultQueue.Enqueue(results, deviceID)
}

// SubmitResultsDirectly 直接提交分析结果（供 BatchProcessor 内部使用）
func (s *analysisService) SubmitResultsDirectly(results []model.AnalysisResult, deviceID uint) (*model.SubmitResultsResponse, error) {
	logger.Infof("SubmitResultsDirectly called with %d results", len(results))

	resp := &model.SubmitResultsResponse{
		Accepted:      0,
		Rejected:      0,
		RejectedItems: make([]model.RejectedItem, 0),
		FailedPhotos:  make([]uint, 0),
	}

	// 第一阶段：验证和预筛选（在事务外做验证，减少事务持有时间）
	type validResult struct {
		result       model.AnalysisResult
		overallScore int
		aiProvider   string
	}
	validResults := make([]validResult, 0, len(results))

	for _, result := range results {
		// 验证结果
		if err := validateResult(result); err != nil {
			resp.Rejected++
			resp.RejectedItems = append(resp.RejectedItems, model.RejectedItem{
				PhotoID: result.PhotoID,
				Reason:  "validation_failed",
				Message: err.Error(),
			})
			continue
		}

		overallScore := model.CalcOverallScore(result.MemoryScore, result.BeautyScore)
		aiProvider := result.AIProvider
		if aiProvider == "" {
			aiProvider = "analyzer"
		}

		validResults = append(validResults, validResult{
			result:       result,
			overallScore: overallScore,
			aiProvider:   aiProvider,
		})
	}

	if len(validResults) == 0 {
		return resp, nil
	}

	// 第二阶段：批量处理（使用 CASE WHEN 减少锁定时间）
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// 获取所有待更新的照片（一次性查询）
		photoIDs := make([]uint, 0, len(validResults))
		for _, vr := range validResults {
			photoIDs = append(photoIDs, vr.result.PhotoID)
		}

		var photos []model.Photo
		if err := tx.Where("id IN ?", photoIDs).Find(&photos).Error; err != nil {
			return fmt.Errorf("fetch photos: %w", err)
		}

		// 构建照片状态映射
		photoMap := make(map[uint]model.Photo)
		for _, p := range photos {
			photoMap[p.ID] = p
		}

		// 筛选出可以更新的结果（未分析过的）
		toUpdate := make([]struct {
			result       model.AnalysisResult
			overallScore int
			aiProvider   string
		}, 0, len(validResults))
		for _, vr := range validResults {
			photo, exists := photoMap[vr.result.PhotoID]
			if !exists {
				resp.Rejected++
				resp.RejectedItems = append(resp.RejectedItems, model.RejectedItem{
					PhotoID: vr.result.PhotoID,
					Reason:  "invalid_photo_id",
					Message: "Photo not found",
				})
				continue
			}
			if photo.AIAnalyzed {
				resp.Rejected++
				resp.RejectedItems = append(resp.RejectedItems, model.RejectedItem{
					PhotoID: vr.result.PhotoID,
					Reason:  "duplicate_result",
					Message: "Photo already analyzed",
				})
				continue
			}
			toUpdate = append(toUpdate, vr)
		}

		if len(toUpdate) == 0 {
			return nil
		}

		// 构建批量 CASE WHEN 更新 SQL
		now := time.Now()
		if err := s.batchUpdatePhotos(tx, toUpdate, now); err != nil {
			logger.Errorf("Batch update failed: %v", err)
			// 批量失败，记录所有为失败（会触发客户端重试）
			for _, vr := range toUpdate {
				resp.FailedPhotos = append(resp.FailedPhotos, vr.result.PhotoID)
			}
			return err
		}

		resp.Accepted = len(toUpdate)
		return nil
	})

	if err != nil {
		return nil, err
	}

	logger.Debugf("Directly submitted %d results: accepted=%d, rejected=%d, failed=%d",
		len(results), resp.Accepted, resp.Rejected, len(resp.FailedPhotos))

	return resp, nil
}

// batchUpdatePhotos 在事务内逐条更新照片（参数化查询，安全可靠）
// 批量大小受 API 限制（最多 50 条），逐条更新在事务内性能完全可接受
func (s *analysisService) batchUpdatePhotos(tx *gorm.DB, results []struct {
	result       model.AnalysisResult
	overallScore int
	aiProvider   string
}, now time.Time) error {
	if len(results) == 0 {
		return nil
	}

	for _, vr := range results {
		if err := tx.Model(&model.Photo{}).Where("id = ?", vr.result.PhotoID).Updates(map[string]interface{}{
			"ai_analyzed":              true,
			"description":              vr.result.Description,
			"caption":                  vr.result.Caption,
			"memory_score":             vr.result.MemoryScore,
			"beauty_score":             vr.result.BeautyScore,
			"overall_score":            vr.overallScore,
			"score_reason":             vr.result.ScoreReason,
			"main_category":            vr.result.MainCategory,
			"tags":                     vr.result.Tags,
			"ai_provider":              vr.aiProvider,
			"analyzed_at":              now,
			"analysis_lock_id":         nil,
			"analysis_lock_expired_at": nil,
			"analysis_retry_count":     0,
		}).Error; err != nil {
			return fmt.Errorf("update photo %d: %w", vr.result.PhotoID, err)
		}

		// 双写 photo_tags 表（使用事务内版本，避免嵌套事务导致 SQLite 自死锁）
		if s.photoTagRepo != nil {
			if err := s.photoTagRepo.SyncTagsTx(tx, vr.result.PhotoID, vr.result.Tags); err != nil {
				logger.Warnf("Failed to sync tags for photo %d: %v", vr.result.PhotoID, err)
			}
		}
	}

	return nil
}

// GetStats 获取分析统计
func (s *analysisService) GetStats(deviceID uint) (*model.AnalyzerStatsResponse, error) {
	var stats model.AnalyzerStatsResponse

	// 统计总数
	err := s.db.Model(&model.Photo{}).Where("status = ?", model.PhotoStatusActive).Count(&stats.TotalPhotos).Error
	if err != nil {
		return nil, err
	}

	// 统计已分析
	err = s.db.Model(&model.Photo{}).Where("status = ? AND ai_analyzed = ?", model.PhotoStatusActive, true).Count(&stats.Analyzed).Error
	if err != nil {
		return nil, err
	}

	// 统计待分析（不含被锁定的）
	err = s.db.Model(&model.Photo{}).
		Where("status = ? AND ai_analyzed = ? AND (analysis_lock_expired_at IS NULL OR analysis_lock_expired_at < ?)",
			model.PhotoStatusActive, false, time.Now()).
		Count(&stats.Pending).Error
	if err != nil {
		return nil, err
	}

	// 统计被锁定的
	err = s.db.Model(&model.Photo{}).
		Where("status = ? AND analysis_lock_expired_at >= ?", model.PhotoStatusActive, time.Now()).
		Count(&stats.Locked).Error
	if err != nil {
		return nil, err
	}

	// 统计失败的（重试次数超过3次）
	err = s.db.Model(&model.Photo{}).
		Where("status = ? AND ai_analyzed = ? AND analysis_retry_count >= ?", model.PhotoStatusActive, false, 3).
		Count(&stats.Failed).Error
	if err != nil {
		return nil, err
	}

	// 计算队列压力
	stats.QueuePressure = model.GetQueuePressure(stats.Pending)

	// 当前未统计平均分析耗时
	stats.AvgAnalysisTime = 0

	return &stats, nil
}

// CleanExpiredLocks 清理过期的任务锁
func (s *analysisService) CleanExpiredLocks() (int64, error) {
	result := s.db.Model(&model.Photo{}).
		Where("analysis_lock_expired_at < ? AND ai_analyzed = ? AND status = ?", time.Now(), false, model.PhotoStatusActive).
		Updates(map[string]interface{}{
			"analysis_lock_id":         nil,
			"analysis_lock_expired_at": nil,
		})

	if result.Error != nil {
		return 0, result.Error
	}

	cleanedCount := result.RowsAffected
	if cleanedCount > 0 {
		logger.Infof("Cleaned %d expired locks", cleanedCount)
	}

	return cleanedCount, nil
}

// validateResult 验证分析结果
func validateResult(result model.AnalysisResult) error {
	if result.PhotoID == 0 {
		return errors.New("photo_id is required")
	}
	if result.Description == "" {
		return errors.New("description is required")
	}
	if result.MemoryScore < 0 || result.MemoryScore > 100 {
		return errors.New("memory_score must be between 0 and 100")
	}
	if result.BeautyScore < 0 || result.BeautyScore > 100 {
		return errors.New("beauty_score must be between 0 and 100")
	}
	return nil
}
