package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// FaceQualityRescoreRunQuery 列表查询过滤条件。
type FaceQualityRescoreRunQuery struct {
	Status string // 可选：仅返回某状态
	Limit  int
}

// FaceQualityRescoreRepository 管理历史重评分运行与目标快照。
type FaceQualityRescoreRepository interface {
	CreateRun(run *model.FaceQualityRescoreRun) error
	UpdateRun(run *model.FaceQualityRescoreRun) error
	GetRun(id uint) (*model.FaceQualityRescoreRun, error)
	ListRuns(q FaceQualityRescoreRunQuery) ([]*model.FaceQualityRescoreRun, error)
	// HasActiveRun 报告是否存在 status IN (running, paused) 的运行（单活跃 run 互斥）。
	HasActiveRun() (bool, error)
	// HasCompletedCalibration 报告是否存在已完成的 calibration 运行（full/enforce 前置条件）。
	// Deprecated: 保留向后兼容；full/enforce 门禁改用 GetEligibleCalibration 逐项验证。
	HasCompletedCalibration() (bool, error)
	// GetEligibleCalibration 读取并逐项验证某 run 是否为合格校准（计划 §3.4）。
	// 返回 (run, eligible, error)。run 为 nil 且 err 为 gorm.ErrRecordNotFound 时表示不存在。
	GetEligibleCalibration(runID uint) (*model.FaceQualityRescoreRun, error)
	// CountPendingOrProcessing 报告某 run 是否仍有未到终态的 item（pending/processing）。
	CountPendingOrProcessing(runID uint) (int64, error)
	// ListRetryableTargets 列出某来源 run 的当前 historical_rescore + retryable_error|unmatched 事件，
	// 供 retry 创建新 shadow calibration 精确快照失败集合。窄查询，不扫全部历史缺证据。
	ListRetryableTargets(sourceRunID uint) ([]model.FaceQualityRescoreRetryTarget, error)

	CreateItems(items []*model.FaceQualityRescoreItem) error
	ListItemsByRun(runID uint) ([]*model.FaceQualityRescoreItem, error)
	// ClaimNextPhotoItems 领取一组属于同一照片、status=pending 的 item（按 photo_id 分组），
	// 把它们置为 processing 并返回。无 pending 时返回空切片。
	ClaimNextPhotoItems(runID uint) ([]*model.FaceQualityRescoreItem, error)
	UpdateItem(item *model.FaceQualityRescoreItem) error
	// ResetProcessingItems 进程重启时把 processing item 回到 pending（不丢失进度）。
	ResetProcessingItems(runID uint) (int64, error)
	// CountItemsByStatus 按状态统计某 run 的 item 数。
	CountItemsByStatus(runID uint, status string) (int64, error)
	// CountTerminalPhotos 统计某 run 已到终态（item status 不为 pending/processing）的去重照片数。
	CountTerminalPhotos(runID uint) (int, error)
	// LatestItemError 返回某 run 最近一条非空 item last_error（按 id 倒序），无则返回空串。
	LatestItemError(runID uint) (string, error)
	// ListAutoExcludedByRun 列出某 run 产生的自动排除事件（rescore_run_id 匹配），
	// 供 run 级 restore-auto 精确恢复。
	ListAutoExcludedByRun(runID uint, limit int) ([]*model.FaceQualityEvent, error)
}

type faceQualityRescoreRepository struct {
	db *gorm.DB
}

func NewFaceQualityRescoreRepository(db *gorm.DB) FaceQualityRescoreRepository {
	return &faceQualityRescoreRepository{db: db}
}

func (r *faceQualityRescoreRepository) CreateRun(run *model.FaceQualityRescoreRun) error {
	return r.db.Create(run).Error
}

func (r *faceQualityRescoreRepository) UpdateRun(run *model.FaceQualityRescoreRun) error {
	return r.db.Save(run).Error
}

func (r *faceQualityRescoreRepository) GetRun(id uint) (*model.FaceQualityRescoreRun, error) {
	var run model.FaceQualityRescoreRun
	if err := r.db.Where("id = ?", id).First(&run).Error; err != nil {
		return nil, err
	}
	return &run, nil
}

func (r *faceQualityRescoreRepository) ListRuns(q FaceQualityRescoreRunQuery) ([]*model.FaceQualityRescoreRun, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	tx := r.db.Model(&model.FaceQualityRescoreRun{})
	if q.Status != "" {
		tx = tx.Where("status = ?", q.Status)
	}
	var runs []*model.FaceQualityRescoreRun
	err := tx.Order("id DESC").Limit(limit).Find(&runs).Error
	return runs, err
}

func (r *faceQualityRescoreRepository) HasActiveRun() (bool, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreRun{}).
		Where("status IN ?", []string{model.FaceQualityRescoreStatusRunning, model.FaceQualityRescoreStatusPaused}).
		Count(&cnt).Error
	return cnt > 0, err
}

func (r *faceQualityRescoreRepository) HasCompletedCalibration() (bool, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreRun{}).
		Where("mode = ? AND status = ?", model.FaceQualityRescoreModeCalibration, model.FaceQualityRescoreStatusCompleted).
		Count(&cnt).Error
	return cnt > 0, err
}

// GetEligibleCalibration 读取 run 并执行计划 §3.4 的合格校准逐项验证：
//   - mode=calibration、apply_mode=shadow、status=completed（completed_with_errors 不合格）；
//   - target_face_count > 0；
//   - retryable_count == 0；
//   - processed_face_count + superseded_manual_count == target_face_count（计数闭合）；
//   - 无 pending/processing item。
//
// 返回 (run, true, nil) 合格；(run, false, nil) 存在但不合格；nil,gorm.ErrRecordNotFound 不存在。
func (r *faceQualityRescoreRepository) GetEligibleCalibration(runID uint) (*model.FaceQualityRescoreRun, error) {
	run, err := r.GetRun(runID)
	if err != nil {
		return nil, err
	}
	return run, nil
}

// CountPendingOrProcessing 报告某 run 仍处于 pending/processing 的 item 数。
func (r *faceQualityRescoreRepository) CountPendingOrProcessing(runID uint) (int64, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND status IN ?", runID, []string{
			model.FaceQualityRescoreItemStatusPending,
			model.FaceQualityRescoreItemStatusProcessing,
		}).
		Count(&cnt).Error
	return cnt, err
}

// ListRetryableTargets 列出某来源 run 的当前失败事件（historical_rescore + retryable_error|unmatched），
// 按 photo_id、id 升序返回，供 retry 精确快照。
func (r *faceQualityRescoreRepository) ListRetryableTargets(sourceRunID uint) ([]model.FaceQualityRescoreRetryTarget, error) {
	type row struct {
		PhotoID         uint   `gorm:"column:photo_id"`
		FaceID          *uint  `gorm:"column:face_id"`
		BBoxX           float64 `gorm:"column:bbox_x"`
		BBoxY           float64 `gorm:"column:bbox_y"`
		BBoxWidth       float64 `gorm:"column:bbox_width"`
		BBoxHeight      float64 `gorm:"column:bbox_height"`
		ID              uint   `gorm:"column:id"`
		EvidenceState   string `gorm:"column:evidence_state"`
	}
	var rows []row
	err := r.db.Model(&model.FaceQualityEvent{}).
		Select("photo_id, face_id, bbox_x, bbox_y, bbox_width, bbox_height, id, evidence_state").
		Where("is_current = ? AND rescore_run_id = ? AND evidence_origin = ?",
			true, sourceRunID, model.FaceQualityEvidenceOriginHistoricalRescore).
		Where("evidence_state IN ?", []string{
			model.FaceQualityEvidenceStateRetryableError,
			model.FaceQualityEvidenceStateUnmatched,
		}).
		Where("face_id IS NOT NULL").
		Order("photo_id ASC, id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]model.FaceQualityRescoreRetryTarget, 0, len(rows))
	for _, r := range rows {
		if r.FaceID == nil {
			continue
		}
		out = append(out, model.FaceQualityRescoreRetryTarget{
			PhotoID:         r.PhotoID,
			FaceID:          *r.FaceID,
			BBoxX:           r.BBoxX,
			BBoxY:           r.BBoxY,
			BBoxWidth:       r.BBoxWidth,
			BBoxHeight:      r.BBoxHeight,
			BaselineEventID: r.ID,
			EvidenceState:   r.EvidenceState,
		})
	}
	return out, nil
}

func (r *faceQualityRescoreRepository) CreateItems(items []*model.FaceQualityRescoreItem) error {
	if len(items) == 0 {
		return nil
	}
	return r.db.CreateInBatches(items, 500).Error
}

func (r *faceQualityRescoreRepository) ListItemsByRun(runID uint) ([]*model.FaceQualityRescoreItem, error) {
	var items []*model.FaceQualityRescoreItem
	err := r.db.Where("run_id = ?", runID).Order("photo_id ASC, id ASC").Find(&items).Error
	return items, err
}

// ClaimNextPhotoItems 领取下一个待处理照片的全部 pending item。
// 在一个事务中：找到最小 photo_id（有 pending item），把该照片所有 pending item 置 processing。
func (r *faceQualityRescoreRepository) ClaimNextPhotoItems(runID uint) ([]*model.FaceQualityRescoreItem, error) {
	var items []*model.FaceQualityRescoreItem
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 找下一个有 pending item 的 photo_id。
		var photoID uint
		err := tx.Model(&model.FaceQualityRescoreItem{}).
			Where("run_id = ? AND status = ?", runID, model.FaceQualityRescoreItemStatusPending).
			Order("photo_id ASC").
			Limit(1).
			Pluck("photo_id", &photoID).Error
		if err != nil {
			return err
		}
		if photoID == 0 {
			return nil // 无 pending
		}

		// 领取该照片所有 pending item。
		if err := tx.Model(&model.FaceQualityRescoreItem{}).
			Where("run_id = ? AND photo_id = ? AND status = ?", runID, photoID, model.FaceQualityRescoreItemStatusPending).
			Update("status", model.FaceQualityRescoreItemStatusProcessing).Error; err != nil {
			return err
		}

		return tx.Where("run_id = ? AND photo_id = ? AND status = ?", runID, photoID, model.FaceQualityRescoreItemStatusProcessing).
			Order("id ASC").Find(&items).Error
	})
	return items, err
}

func (r *faceQualityRescoreRepository) UpdateItem(item *model.FaceQualityRescoreItem) error {
	return r.db.Save(item).Error
}

func (r *faceQualityRescoreRepository) ResetProcessingItems(runID uint) (int64, error) {
	res := r.db.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND status = ?", runID, model.FaceQualityRescoreItemStatusProcessing).
		Update("status", model.FaceQualityRescoreItemStatusPending)
	return res.RowsAffected, res.Error
}

func (r *faceQualityRescoreRepository) CountItemsByStatus(runID uint, status string) (int64, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND status = ?", runID, status).
		Count(&cnt).Error
	return cnt, err
}

// CountTerminalPhotos 统计某 run 已到终态（status 不为 pending/processing）的去重照片数。
func (r *faceQualityRescoreRepository) CountTerminalPhotos(runID uint) (int, error) {
	var cnt int64
	err := r.db.Model(&model.FaceQualityRescoreItem{}).
		Where("run_id = ? AND status NOT IN ?", runID, []string{
			model.FaceQualityRescoreItemStatusPending,
			model.FaceQualityRescoreItemStatusProcessing,
		}).
		Distinct("photo_id").Count(&cnt).Error
	return int(cnt), err
}

// LatestItemError 返回某 run 最近一条非空 last_error（按 id 倒序），无则返回空串。
func (r *faceQualityRescoreRepository) LatestItemError(runID uint) (string, error) {
	var item model.FaceQualityRescoreItem
	err := r.db.Where("run_id = ? AND last_error != ''", runID).
		Order("id DESC").Limit(1).First(&item).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", nil
		}
		return "", err
	}
	return item.LastError, nil
}

func (r *faceQualityRescoreRepository) ListAutoExcludedByRun(runID uint, limit int) ([]*model.FaceQualityEvent, error) {
	if limit <= 0 {
		limit = 5000
	}
	var records []*model.FaceQualityEvent
	err := r.db.Where("rescore_run_id = ? AND source = ? AND is_current = ?",
		runID, model.FaceQualitySourceAuto, true).
		Where("decision IN ?", []string{model.FaceQualityDecisionNonFace, model.FaceQualityDecisionLowQuality}).
		Where("face_id IS NOT NULL").
		Limit(limit).Find(&records).Error
	return records, err
}

// rescoreRunTimestamp 用于测试注入确定性时间。
var rescoreRunTimestamp = func() time.Time { return time.Now().UTC() }
