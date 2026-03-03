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
	SubmitResults(results []model.AnalysisResult, apiKeyID uint) (*model.SubmitResultsResponse, error)
	GetStats(apiKeyID uint) (*model.AnalyzerStatsResponse, error)
	CleanExpiredLocks() (int64, error)
}

// analysisService 分析服务实现
type analysisService struct {
	db         *gorm.DB
	photoRepo  repository.PhotoRepository
	cfg        *config.Config
}

// NewAnalysisService 创建分析服务
func NewAnalysisService(db *gorm.DB, photoRepo repository.PhotoRepository, cfg *config.Config) AnalysisService {
	return &analysisService{
		db:        db,
		photoRepo: photoRepo,
		cfg:       cfg,
	}
}

// GetPendingTasks 获取待分析任务列表
func (s *analysisService) GetPendingTasks(limit int, analyzerID string) ([]model.AnalysisTask, int64, error) {
	var tasks []model.AnalysisTask
	var totalRemaining int64

	// 1. 统计剩余待分析数量
	err := s.db.Model(&model.Photo{}).
		Where("ai_analyzed = ? AND (analysis_lock_expired_at IS NULL OR analysis_lock_expired_at < ?)",
			false, time.Now()).
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
			WHERE ai_analyzed = ?
			  AND (analysis_lock_expired_at IS NULL OR analysis_lock_expired_at < ?)
			  AND deleted_at IS NULL
			ORDER BY id ASC
			LIMIT ?
		)`, false, time.Now(), limit).
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
		downloadURL := fmt.Sprintf("%s/api/v1/photos/%d/image?token=%s", baseURL, photo.ID, "temp-token")
		tokenExpiresAt := time.Now().Add(30 * time.Minute)

		task := model.AnalysisTask{
			ID:                     fmt.Sprintf("task_%d_%d", photo.ID, time.Now().Unix()),
			PhotoID:                photo.ID,
			FilePath:               photo.FilePath,
			DownloadURL:            downloadURL,
			DownloadTokenExpiresAt: &tokenExpiresAt,
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

	var photo model.Photo
	err = s.db.First(&photo, photoID).Error
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
	err = s.db.Model(&photo).Updates(map[string]interface{}{
		"analysis_lock_expired_at": newExpiredAt,
	}).Error
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

	var photo model.Photo
	err = s.db.First(&photo, photoID).Error
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

	err = s.db.Model(&photo).Updates(updates).Error
	if err != nil {
		return err
	}

	logger.Infof("Task %s released by analyzer %s, reason: %s", taskID, analyzerID, reason)
	return nil
}

// SubmitResults 提交分析结果（幂等性处理）
func (s *analysisService) SubmitResults(results []model.AnalysisResult, apiKeyID uint) (*model.SubmitResultsResponse, error) {
	resp := &model.SubmitResultsResponse{
		Accepted:      0,
		Rejected:      0,
		RejectedItems: make([]model.RejectedItem, 0),
		FailedPhotos:  make([]uint, 0),
	}

	err := s.db.Transaction(func(tx *gorm.DB) error {
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

			// 获取照片
			var photo model.Photo
			err := tx.First(&photo, result.PhotoID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					resp.Rejected++
					resp.RejectedItems = append(resp.RejectedItems, model.RejectedItem{
						PhotoID: result.PhotoID,
						Reason:  "invalid_photo_id",
						Message: "Photo not found",
					})
				} else {
					resp.FailedPhotos = append(resp.FailedPhotos, result.PhotoID)
				}
				continue
			}

			// 如果照片已经分析过了（幂等性）
			if photo.AIAnalyzed {
				resp.Rejected++
				resp.RejectedItems = append(resp.RejectedItems, model.RejectedItem{
					PhotoID: result.PhotoID,
					Reason:  "duplicate_result",
					Message: "Photo already analyzed",
				})
				continue
			}

			// 计算综合评分
			overallScore := int(float64(result.MemoryScore)*0.7 + float64(result.BeautyScore)*0.3)
			now := time.Now()

			// 使用提交结果中的 AI provider，如果没有提供则使用 "analyzer"
			aiProvider := result.AIProvider
			if aiProvider == "" {
				aiProvider = "analyzer"
			}

			// 更新照片分析结果
			updates := map[string]interface{}{
				"ai_analyzed":               true,
				"analyzed_at":               now,
				"ai_provider":               aiProvider,
				"description":               result.Description,
				"caption":                   result.Caption,
				"memory_score":              result.MemoryScore,
				"beauty_score":              result.BeautyScore,
				"overall_score":             overallScore,
				"main_category":             result.MainCategory,
				"tags":                      result.Tags,
				"analysis_lock_id":          nil,
				"analysis_lock_expired_at":  nil,
				"analysis_retry_count":      0,
			}

			err = tx.Model(&photo).Updates(updates).Error
			if err != nil {
				resp.FailedPhotos = append(resp.FailedPhotos, result.PhotoID)
				continue
			}

			resp.Accepted++
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	logger.Infof("Submitted %d results: accepted=%d, rejected=%d, failed=%d",
		len(results), resp.Accepted, resp.Rejected, len(resp.FailedPhotos))

	return resp, nil
}

// GetStats 获取分析统计
func (s *analysisService) GetStats(apiKeyID uint) (*model.AnalyzerStatsResponse, error) {
	var stats model.AnalyzerStatsResponse

	// 统计总数
	err := s.db.Model(&model.Photo{}).Count(&stats.TotalPhotos).Error
	if err != nil {
		return nil, err
	}

	// 统计已分析
	err = s.db.Model(&model.Photo{}).Where("ai_analyzed = ?", true).Count(&stats.Analyzed).Error
	if err != nil {
		return nil, err
	}

	// 统计待分析（不含被锁定的）
	err = s.db.Model(&model.Photo{}).
		Where("ai_analyzed = ? AND (analysis_lock_expired_at IS NULL OR analysis_lock_expired_at < ?)",
			false, time.Now()).
		Count(&stats.Pending).Error
	if err != nil {
		return nil, err
	}

	// 统计被锁定的
	err = s.db.Model(&model.Photo{}).
		Where("analysis_lock_expired_at >= ?", time.Now()).
		Count(&stats.Locked).Error
	if err != nil {
		return nil, err
	}

	// 统计失败的（重试次数超过3次）
	err = s.db.Model(&model.Photo{}).
		Where("ai_analyzed = ? AND analysis_retry_count >= ?", false, 3).
		Count(&stats.Failed).Error
	if err != nil {
		return nil, err
	}

	// 计算队列压力
	stats.QueuePressure = model.GetQueuePressure(stats.Pending)

	// TODO: 计算平均分析时间（需要记录分析开始时间）
	stats.AvgAnalysisTime = 0

	return &stats, nil
}

// CleanExpiredLocks 清理过期的任务锁
func (s *analysisService) CleanExpiredLocks() (int64, error) {
	result := s.db.Model(&model.Photo{}).
		Where("analysis_lock_expired_at < ? AND ai_analyzed = ?", time.Now(), false).
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
