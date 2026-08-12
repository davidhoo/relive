package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// FaceQualityQuery 审核页查询过滤条件。零值为忽略该维度。
type FaceQualityQuery struct {
	Decision    string   // accepted/non_face/low_quality/review_required
	Decisions   []string // 多判定 OR 过滤（如 auto_excluded Tab 需 non_face|low_quality）
	Source      string   // auto/manual
	RuleVersion string
	Reason      string
	// 当前是否最新结论：true 仅返回 is_current=true，false 仅返回 is_current=false，
	// 零值忽略。
	IsCurrent *bool
	StartTime *time.Time
	EndTime   *time.Time
	Page      int
	PageSize  int
}

// FaceQualityStats 质检全局统计。
type FaceQualityStats struct {
	PendingReview   int64 `json:"pending_review"`   // review_required 且 is_current
	AutoExcluded    int64 `json:"auto_excluded"`    // auto + (non_face|low_quality) + is_current
	ManualConfirmed int64 `json:"manual_confirmed"` // manual + (non_face|low_quality|accepted) + is_current
	Total           int64 `json:"total"`            // is_current 总数
	ByReason        map[string]int64 `json:"by_reason"`
	ByRuleVersion   map[string]int64 `json:"by_rule_version"`
}

// FaceQualityRepository 管理追加式质检审计表。
type FaceQualityRepository interface {
	Create(record *model.FaceQualityEvent) error
	Update(record *model.FaceQualityEvent) error
	GetByID(id uint) (*model.FaceQualityEvent, error)
	// ListByPhotoID 返回某照片的全部质检事件（含历史），按 id 倒序。
	ListByPhotoID(photoID uint) ([]*model.FaceQualityEvent, error)
	// ListCurrentByPhotoID 返回某照片 is_current=true 的事件。
	ListCurrentByPhotoID(photoID uint) ([]*model.FaceQualityEvent, error)
	// List 按 FaceQualityQuery 过滤分页。
	List(q FaceQualityQuery) ([]*model.FaceQualityEvent, int64, error)
	// Stats 全局统计。
	Stats() (*FaceQualityStats, error)
	// ClearCurrentByPhoto 把某照片全部事件置为 is_current=false（重检后旧结论失活）。
	ClearCurrentByPhoto(tx *gorm.DB, photoID uint) error
	// ListAutoByRuleVersion 列出某规则版本的自动排除事件（用于按规则版本恢复）。
	ListAutoByRuleVersion(ruleVersion string, limit int) ([]*model.FaceQualityEvent, error)
	// ListAutoExcludedFaceIDs 列出某规则版本下自动排除且当前仍排除的 Face ID。
	ListAutoExcludedFaceIDs(ruleVersion string, limit int) ([]uint, error)
}

type faceQualityRepository struct {
	db *gorm.DB
}

func NewFaceQualityRepository(db *gorm.DB) FaceQualityRepository {
	return &faceQualityRepository{db: db}
}

func (r *faceQualityRepository) Create(record *model.FaceQualityEvent) error {
	return r.db.Create(record).Error
}

func (r *faceQualityRepository) Update(record *model.FaceQualityEvent) error {
	return r.db.Save(record).Error
}

func (r *faceQualityRepository) GetByID(id uint) (*model.FaceQualityEvent, error) {
	var rec model.FaceQualityEvent
	if err := r.db.Where("id = ?", id).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

func (r *faceQualityRepository) ListByPhotoID(photoID uint) ([]*model.FaceQualityEvent, error) {
	var records []*model.FaceQualityEvent
	err := r.db.Where("photo_id = ?", photoID).Order("id DESC").Find(&records).Error
	return records, err
}

func (r *faceQualityRepository) ListCurrentByPhotoID(photoID uint) ([]*model.FaceQualityEvent, error) {
	var records []*model.FaceQualityEvent
	err := r.db.Where("photo_id = ? AND is_current = ?", photoID, true).
		Order("id DESC").Find(&records).Error
	return records, err
}

func (r *faceQualityRepository) List(q FaceQualityQuery) ([]*model.FaceQualityEvent, int64, error) {
	tx := r.db.Model(&model.FaceQualityEvent{}).Where("is_current = ?", true)

	if q.Decision != "" {
		tx = tx.Where("decision = ?", q.Decision)
	}
	if len(q.Decisions) > 0 {
		tx = tx.Where("decision IN ?", q.Decisions)
	}
	if q.Source != "" {
		tx = tx.Where("source = ?", q.Source)
	}
	if q.RuleVersion != "" {
		tx = tx.Where("rule_version = ?", q.RuleVersion)
	}
	if q.Reason != "" {
		tx = tx.Where("reason = ?", q.Reason)
	}
	if q.IsCurrent != nil {
		tx = tx.Where("is_current = ?", *q.IsCurrent)
	}
	if q.StartTime != nil {
		tx = tx.Where("created_at >= ?", *q.StartTime)
	}
	if q.EndTime != nil {
		tx = tx.Where("created_at < ?", *q.EndTime)
	}

	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := q.Page
	if page < 1 {
		page = 1
	}
	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	var records []*model.FaceQualityEvent
	err := tx.Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&records).Error
	return records, total, err
}

func (r *faceQualityRepository) Stats() (*FaceQualityStats, error) {
	stats := &FaceQualityStats{
		ByReason:      make(map[string]int64),
		ByRuleVersion: make(map[string]int64),
	}

	type countRow struct {
		Bucket string
		Count  int64
	}

	// 总数（is_current）
	if err := r.db.Model(&model.FaceQualityEvent{}).
		Where("is_current = ?", true).Count(&stats.Total).Error; err != nil {
		return nil, err
	}

	// 按判定统计
	var decisionRows []countRow
	if err := r.db.Model(&model.FaceQualityEvent{}).
		Select("decision as bucket, count(*) as count").
		Where("is_current = ?", true).
		Group("decision").Scan(&decisionRows).Error; err != nil {
		return nil, err
	}
	for _, row := range decisionRows {
		switch row.Bucket {
		case model.FaceQualityDecisionReviewRequired:
			stats.PendingReview = row.Count
		case model.FaceQualityDecisionAccepted:
			// accepted 不计入 auto/manual 分类
		}
	}

	// auto 排除数
	if err := r.db.Model(&model.FaceQualityEvent{}).
		Where("is_current = ? AND source = ? AND decision IN ?",
			true, model.FaceQualitySourceAuto,
			[]string{model.FaceQualityDecisionNonFace, model.FaceQualityDecisionLowQuality}).
		Count(&stats.AutoExcluded).Error; err != nil {
		return nil, err
	}

	// manual 确认数（任意 manual 判定，当前态）
	if err := r.db.Model(&model.FaceQualityEvent{}).
		Where("is_current = ? AND source = ?", true, model.FaceQualitySourceManual).
		Count(&stats.ManualConfirmed).Error; err != nil {
		return nil, err
	}

	// 按原因码统计（reason 非空）
	var reasonRows []countRow
	if err := r.db.Model(&model.FaceQualityEvent{}).
		Select("reason as bucket, count(*) as count").
		Where("is_current = ? AND reason != ''", true).
		Group("reason").Scan(&reasonRows).Error; err != nil {
		return nil, err
	}
	for _, row := range reasonRows {
		stats.ByReason[row.Bucket] = row.Count
	}

	// 按规则版本统计
	var ruleRows []countRow
	if err := r.db.Model(&model.FaceQualityEvent{}).
		Select("rule_version as bucket, count(*) as count").
		Where("is_current = ?", true).
		Group("rule_version").Scan(&ruleRows).Error; err != nil {
		return nil, err
	}
	for _, row := range ruleRows {
		stats.ByRuleVersion[row.Bucket] = row.Count
	}

	return stats, nil
}

func (r *faceQualityRepository) ClearCurrentByPhoto(tx *gorm.DB, photoID uint) error {
	db := tx
	if db == nil {
		db = r.db
	}
	return db.Model(&model.FaceQualityEvent{}).
		Where("photo_id = ? AND is_current = ?", photoID, true).
		Update("is_current", false).Error
}

func (r *faceQualityRepository) ListAutoByRuleVersion(ruleVersion string, limit int) ([]*model.FaceQualityEvent, error) {
	if limit <= 0 {
		limit = 500
	}
	var records []*model.FaceQualityEvent
	err := r.db.Where("rule_version = ? AND source = ? AND is_current = ?",
		ruleVersion, model.FaceQualitySourceAuto, true).
		Where("decision IN ?",
			[]string{model.FaceQualityDecisionNonFace, model.FaceQualityDecisionLowQuality}).
		Limit(limit).Find(&records).Error
	return records, err
}

func (r *faceQualityRepository) ListAutoExcludedFaceIDs(ruleVersion string, limit int) ([]uint, error) {
	if limit <= 0 {
		limit = 500
	}
	var ids []uint
	err := r.db.Model(&model.FaceQualityEvent{}).
		Where("rule_version = ? AND source = ? AND is_current = ?",
			ruleVersion, model.FaceQualitySourceAuto, true).
		Where("decision IN ?",
			[]string{model.FaceQualityDecisionNonFace, model.FaceQualityDecisionLowQuality}).
		Where("face_id IS NOT NULL").
		Limit(limit).Pluck("face_id", &ids).Error
	return ids, err
}
