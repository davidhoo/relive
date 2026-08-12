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
	HasCompletedCalibration() (bool, error)

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
