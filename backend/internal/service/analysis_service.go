package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// 错误定义
var (
	ErrTaskNotFound      = errors.New("task not found or expired")
	ErrTaskLockedByOther = errors.New("task locked by another analyzer")
	ErrTaskLockStale     = errors.New("task lock stale: version mismatch")
	// ErrTaskAlreadyReleased 表示同版本重复 release（幂等）。
	ErrTaskAlreadyReleased = errors.New("task already released")
	ErrInvalidResult       = errors.New("invalid analysis result")
)

// AnalysisService 分析服务接口
type AnalysisService interface {
	GetPendingTasks(limit int, analyzerID string) ([]model.AnalysisTask, int64, error)
	ExtendTaskLock(taskID, analyzerID string, lockVersion int64) (time.Time, int64, error)
	ReleaseTask(taskID, analyzerID string, req model.ReleaseTaskRequest) (*model.ReleaseTaskResult, error)
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
	writeQueue   *database.WriteQueue
}

// NewAnalysisService 创建分析服务
func NewAnalysisService(db *gorm.DB, photoRepo repository.PhotoRepository, photoTagRepo repository.PhotoTagRepository, cfg *config.Config) AnalysisService {
	return &analysisService{
		db:           db,
		photoRepo:    photoRepo,
		photoTagRepo: photoTagRepo,
		cfg:          cfg,
		writeQueue:   database.GetWriteQueue(),
	}
}

// executeWrite runs fn through WriteQueue if available, otherwise directly.
func (s *analysisService) executeWrite(fn func() error) error {
	if s.writeQueue != nil {
		return s.writeQueue.Execute(fn)
	}
	return fn()
}

// SetResultQueue 设置结果队列（必须在 Start 之前调用）
func (s *analysisService) SetResultQueue(queue *ResultQueue) {
	s.resultQueue = queue
}

// pendingTaskLockDuration 单次领取/续租的锁有效期。
const pendingTaskLockDuration = 5 * time.Minute

// GetPendingTasks 获取待分析任务列表
//
// 领取条件：status=active、ai_analyzed=false、thumbnail_status=ready、
// 锁未持有或已过期、retry_count < max、next_retry_at 为空或已到。
// 领取时递增 analysis_lock_version，响应携带 lock_version。
func (s *analysisService) GetPendingTasks(limit int, analyzerID string) ([]model.AnalysisTask, int64, error) {
	if limit <= 0 {
		limit = 10
	}
	var tasks []model.AnalysisTask
	var totalRemaining int64

	err := s.executeWrite(func() error {
		now := time.Now()
		// 1. 统计剩余待分析数量（与领取条件一致，排除 retry_wait / failed）
		err := s.db.Model(&model.Photo{}).
			Where(`status = ?
			AND ai_analyzed = ?
			AND thumbnail_status = ?
			AND (analysis_lock_expired_at IS NULL OR analysis_lock_expired_at < ?)
			AND analysis_retry_count < ?
			AND (analysis_next_retry_at IS NULL OR analysis_next_retry_at <= ?)`,
				model.PhotoStatusActive, false, model.ThumbnailStatusReady, now, AnalysisMaxAttempts, now).
			Count(&totalRemaining).Error
		if err != nil {
			return err
		}

		// 2. 锁定一批记录（条件 UPDATE，原子递增 lock_version）
		lockExpiredAt := now.Add(pendingTaskLockDuration)
		result := s.db.Model(&model.Photo{}).
			Where(`id IN (
			SELECT id FROM photos
			WHERE status = ?
			  AND ai_analyzed = ?
			  AND thumbnail_status = ?
			  AND (analysis_lock_expired_at IS NULL OR analysis_lock_expired_at < ?)
			  AND analysis_retry_count < ?
			  AND (analysis_next_retry_at IS NULL OR analysis_next_retry_at <= ?)
			  AND deleted_at IS NULL
			ORDER BY id ASC
			LIMIT ?
		)`, model.PhotoStatusActive, false, model.ThumbnailStatusReady, now, AnalysisMaxAttempts, now, limit).
			Updates(map[string]interface{}{
				"analysis_lock_id":         analyzerID,
				"analysis_lock_expired_at": lockExpiredAt,
				"analysis_lock_version":    gorm.Expr("analysis_lock_version + 1"),
				"analysis_next_retry_at":   nil,
			})

		if result.Error != nil {
			return result.Error
		}

		// 3. 查询刚刚被锁定的照片
		var photos []model.Photo
		err = s.db.Where("analysis_lock_id = ? AND analysis_lock_expired_at = ?",
			analyzerID, lockExpiredAt).
			Order("id ASC").
			Find(&photos).Error
		if err != nil {
			return err
		}

		// 4. 构建任务响应
		tasks = make([]model.AnalysisTask, 0, len(photos))
		baseURL := ""
		if s.cfg != nil {
			baseURL = s.cfg.Server.ExternalURL
			if baseURL == "" {
				baseURL = fmt.Sprintf("http://%s:%d", s.cfg.Server.Host, s.cfg.Server.Port)
			}
		}
		baseURL = strings.TrimSuffix(baseURL, "/")

		for _, photo := range photos {
			downloadURL := fmt.Sprintf("%s/api/v1/photos/%d/image", baseURL, photo.ID)

			task := model.AnalysisTask{
				ID:            fmt.Sprintf("task_%d_%d", photo.ID, now.Unix()),
				PhotoID:       photo.ID,
				FilePath:      photo.FilePath,
				DownloadURL:   downloadURL,
				Width:         photo.Width,
				Height:        photo.Height,
				TakenAt:       photo.TakenAt,
				Location:      photo.Location,
				CameraModel:   photo.CameraModel,
				LockExpiresAt: &lockExpiredAt,
				LockVersion:   photo.AnalysisLockVersion,
			}
			tasks = append(tasks, task)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return tasks, totalRemaining, nil
}

// ExtendTaskLock 续期任务锁（带版本校验），返回新的过期时间与当前锁版本。
func (s *analysisService) ExtendTaskLock(taskID, analyzerID string, lockVersion int64) (time.Time, int64, error) {
	photoID, ok := parseTaskID(taskID)
	if !ok {
		return time.Time{}, 0, ErrTaskNotFound
	}

	var newExpiredAt time.Time
	var currentVersion int64
	err := s.executeWrite(func() error {
		// 条件 UPDATE：匹配 analyzer、版本、未完成状态。
		newExpiredAt = time.Now().Add(pendingTaskLockDuration)
		res := s.db.Model(&model.Photo{}).
			Where("id = ? AND analysis_lock_id = ? AND analysis_lock_version = ? AND ai_analyzed = ? AND status = ?",
				photoID, analyzerID, lockVersion, false, model.PhotoStatusActive).
			Update("analysis_lock_expired_at", newExpiredAt)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var p model.Photo
			if err := s.db.Select("id, analysis_lock_id, analysis_lock_version").First(&p, photoID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrTaskNotFound
				}
				return err
			}
			return ErrTaskLockedByOther
		}
		currentVersion = lockVersion
		return nil
	})
	if err != nil {
		return time.Time{}, 0, err
	}
	return newExpiredAt, currentVersion, nil
}

// ReleaseTask 释放任务，原子条件 UPDATE，递增 retry 并按策略设置 next_retry_at。
//
// 返回结构化 ReleaseTaskResult。RowsAffected=0 时区分 stale（版本/analyzer 不匹配）
// 与已释放的幂等请求（lock 已清空）。
func (s *analysisService) ReleaseTask(taskID, analyzerID string, req model.ReleaseTaskRequest) (*model.ReleaseTaskResult, error) {
	photoID, ok := parseTaskID(taskID)
	if !ok {
		return nil, ErrTaskNotFound
	}

	// 旧 Analyzer 未发送 failure_class 时按 reason 保守映射。
	class := req.FailureClass
	if class == "" {
		class = classifyLegacyReason(req.Reason, req.RetryLater)
	}

	result := &model.ReleaseTaskResult{TaskID: taskID}

	err := s.executeWrite(func() error {
		// 1. 读取当前状态，区分 stale / 幂等 / 正常释放。
		var p model.Photo
		if err := s.db.Select("id, analysis_lock_id, analysis_lock_version, analysis_retry_count, ai_analyzed").
			First(&p, photoID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTaskNotFound
			}
			return err
		}

		// 已被其他 analyzer 持锁或版本不匹配 → stale。
		lockHeldByOther := p.AnalysisLockID != nil && *p.AnalysisLockID != analyzerID
		versionMismatch := req.LockVersion != 0 && p.AnalysisLockVersion != req.LockVersion
		if lockHeldByOther || versionMismatch {
			result.LockStale = true
			return ErrTaskLockStale
		}

		// 同版本重复 release：锁已被本 analyzer 清空（lock_id 为空），幂等返回。
		if p.AnalysisLockID == nil {
			result.Idempotent = true
			return nil
		}

		// 2. 计算退避决策。
		now := time.Now()
		retryAfter := time.Duration(req.RetryAfterSeconds) * time.Second
		decision := nextAnalysisRetry(p.AnalysisRetryCount, class, retryAfter, now)

		// 3. 构造条件 UPDATE。
		updates := map[string]interface{}{
			"analysis_lock_id":         nil,
			"analysis_lock_expired_at": nil,
		}
		if decision.Counted {
			updates["analysis_retry_count"] = decision.NewAttempts
			updates["analysis_last_error_code"] = class
			updates["analysis_last_error"] = sanitizeError(req.ErrorMsg)
			updates["analysis_last_failed_at"] = now
		}
		if decision.Final {
			updates["analysis_next_retry_at"] = nil
		} else if decision.Counted {
			updates["analysis_next_retry_at"] = decision.NextRetryAt
		} else {
			// client_cancelled：不计数，回到 pending。
			updates["analysis_next_retry_at"] = nil
		}

		// 条件：必须匹配 analyzer + 版本（防止并发改写）。
		cond := s.db.Model(&model.Photo{}).
			Where("id = ? AND analysis_lock_id = ?", photoID, analyzerID)
		if req.LockVersion != 0 {
			cond = cond.Where("analysis_lock_version = ?", req.LockVersion)
		}
		res := cond.Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 并发竞争：锁已易主，按 stale 处理。
			result.LockStale = true
			return ErrTaskLockStale
		}

		// 4. 填充返回结构。
		result.Attempts = decision.NewAttempts
		result.NextRetryAt = decision.NextRetryAt
		result.Final = decision.Final
		if decision.Final {
			result.NewStatus = "failed"
		} else if decision.Counted {
			result.NewStatus = "retry_wait"
		} else {
			result.NewStatus = "pending"
		}
		return nil
	})
	if err != nil && !errors.Is(err, ErrTaskLockStale) {
		return nil, err
	}
	if errors.Is(err, ErrTaskLockStale) {
		// stale 仍返回 result 给 handler 判 409。
		return result, ErrTaskLockStale
	}
	return result, nil
}

// SubmitResults 提交分析结果（使用队列，立即返回）
func (s *analysisService) SubmitResults(results []model.AnalysisResult, deviceID uint) (*model.SubmitResultsResponse, error) {
	if s.resultQueue == nil {
		logger.Warn("ResultQueue not set, using direct write")
		return s.SubmitResultsDirectly(results, deviceID)
	}
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

	type validResult struct {
		result       model.AnalysisResult
		overallScore int
		aiProvider   string
	}
	validResults := make([]validResult, 0, len(results))

	for _, result := range results {
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

	err := s.executeWrite(func() error {
		return s.db.Transaction(func(tx *gorm.DB) error {
			photoIDs := make([]uint, 0, len(validResults))
			for _, vr := range validResults {
				photoIDs = append(photoIDs, vr.result.PhotoID)
			}

			var photos []model.Photo
			if err := tx.Where("id IN ?", photoIDs).Find(&photos).Error; err != nil {
				return fmt.Errorf("fetch photos: %w", err)
			}

			photoMap := make(map[uint]model.Photo)
			for _, p := range photos {
				photoMap[p.ID] = p
			}

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

			now := time.Now()
			if err := s.batchUpdatePhotos(tx, toUpdate, now); err != nil {
				logger.Errorf("Batch update failed: %v", err)
				for _, vr := range toUpdate {
					resp.FailedPhotos = append(resp.FailedPhotos, vr.result.PhotoID)
				}
				return err
			}

			resp.Accepted = len(toUpdate)
			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	logger.Debugf("Directly submitted %d results: accepted=%d, rejected=%d, failed=%d",
		len(results), resp.Accepted, resp.Rejected, len(resp.FailedPhotos))

	return resp, nil
}

// batchUpdatePhotos 在事务内逐条更新照片。
// 成功提交时清空锁、attempts、next-retry 和 last-error 字段。
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
			"analysis_lock_version":    gorm.Expr("analysis_lock_version + 1"),
			"analysis_retry_count":     0,
			"analysis_next_retry_at":   nil,
			"analysis_last_error_code": "",
			"analysis_last_error":      "",
			"analysis_last_failed_at":  nil,
		}).Error; err != nil {
			return fmt.Errorf("update photo %d: %w", vr.result.PhotoID, err)
		}

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

	now := time.Now()
	// 待分析：未被锁、未进入 retry_wait / failed。
	err = s.db.Model(&model.Photo{}).
		Where(`status = ? AND ai_analyzed = ?
			AND (analysis_lock_expired_at IS NULL OR analysis_lock_expired_at < ?)
			AND analysis_retry_count < ?
			AND (analysis_next_retry_at IS NULL OR analysis_next_retry_at <= ?)`,
			model.PhotoStatusActive, false, now, AnalysisMaxAttempts, now).
		Count(&stats.Pending).Error
	if err != nil {
		return nil, err
	}

	// 退避等待：next_retry_at 未到。
	err = s.db.Model(&model.Photo{}).
		Where(`status = ? AND ai_analyzed = ? AND analysis_retry_count < ?
			AND analysis_next_retry_at IS NOT NULL AND analysis_next_retry_at > ?`,
			model.PhotoStatusActive, false, AnalysisMaxAttempts, now).
		Count(&stats.RetryWaiting).Error
	if err != nil {
		return nil, err
	}

	// 被锁定：锁未过期且未完成分析。
	err = s.db.Model(&model.Photo{}).
		Where("status = ? AND ai_analyzed = ? AND analysis_lock_expired_at >= ?",
			model.PhotoStatusActive, false, now).
		Count(&stats.Locked).Error
	if err != nil {
		return nil, err
	}

	// 最终失败：达到统一 max attempts。
	err = s.db.Model(&model.Photo{}).
		Where("status = ? AND ai_analyzed = ? AND analysis_retry_count >= ?",
			model.PhotoStatusActive, false, AnalysisMaxAttempts).
		Count(&stats.Failed).Error
	if err != nil {
		return nil, err
	}

	stats.QueuePressure = model.GetQueuePressure(stats.Pending)
	stats.AvgAnalysisTime = 0

	return &stats, nil
}

// CleanExpiredLocks 清理过期的任务锁
func (s *analysisService) CleanExpiredLocks() (int64, error) {
	var cleanedCount int64
	err := s.executeWrite(func() error {
		result := s.db.Model(&model.Photo{}).
			Where("analysis_lock_expired_at < ? AND ai_analyzed = ? AND status = ?", time.Now(), false, model.PhotoStatusActive).
			Updates(map[string]interface{}{
				"analysis_lock_id":         nil,
				"analysis_lock_expired_at": nil,
			})

		if result.Error != nil {
			return result.Error
		}

		cleanedCount = result.RowsAffected
		return nil
	})
	if err != nil {
		return 0, err
	}

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

// parseTaskID 解析 task_{photo_id}_{timestamp} 格式。
func parseTaskID(taskID string) (uint, bool) {
	var photoID uint
	n, err := fmt.Sscanf(taskID, "task_%d_", &photoID)
	if err != nil || n != 1 {
		return 0, false
	}
	return photoID, true
}

// classifyLegacyReason 在旧 Analyzer 未发送 failure_class 时按 reason 保守映射。
// 保守原则：不可识别的 reason 一律视为 provider_transient（计退避，可熔断），避免热循环。
func classifyLegacyReason(reason string, retryLater bool) string {
	switch reason {
	case model.ReleaseReasonFileCorrupted, model.ReleaseReasonUnsupportedFormat:
		return FailureClassInputPermanent
	case model.ReleaseReasonDownloadFailed:
		// 下载失败多为临时网络问题，按 transient 处理。
		return FailureClassProviderTransient
	default:
		return FailureClassProviderTransient
	}
}

// sensitivePatterns 匹配并脱敏 Authorization / API key / Bearer token，
// 以及 URL query 中的 ZINFOID_XXQ 标记。分多条规则以避免误吞相邻业务文本。
var sensitivePatterns = []*regexp.Regexp{
	// Authorization: <scheme> <token> —— 吞掉 scheme 和 token 两个词。
	regexp.MustCompile(`(?i)authorization\s*[:=]?\s*\S+(?:\s+\S+)?`),
	// api_key=... / apikey=... / token=...
	regexp.MustCompile(`(?i)(api[_-]?key|token)\s*[:=]?\s*[A-Za-z0-9_\-\.=]+`),
	// 单独的 Bearer <token>。
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9_\-\.]+`),
	// ZINFOID_XX 形式的敏感标记。
	regexp.MustCompile(`(?i)ZINFOID_[0-9A-Z]{2,}`),
}

// sanitizeError 去除敏感信息并压缩空白、截断到 500 字符。
func sanitizeError(msg string) string {
	if msg == "" {
		return ""
	}
	for _, re := range sensitivePatterns {
		msg = re.ReplaceAllString(msg, "[redacted]")
	}
	// 压缩连续空白。
	msg = regexp.MustCompile(`\s+`).ReplaceAllString(msg, " ")
	msg = strings.TrimSpace(msg)
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return msg
}
