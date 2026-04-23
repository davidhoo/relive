package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

type FaceRepository interface {
	Create(face *model.Face) error
	Update(face *model.Face) error
	UpdateFields(id uint, fields map[string]interface{}) error
	UpdateClusterFields(ids []uint, fields map[string]interface{}) error
	GetByID(id uint) (*model.Face, error)
	DeleteByPhotoID(photoID uint) error
	ListByPhotoID(photoID uint) ([]*model.Face, error)
	ListByPersonID(personID uint) ([]*model.Face, error)
	ListByPersonIDSummary(personID uint) ([]*model.Face, error)       // 排除 embedding，按 quality_score 排序
	ListByPersonIDPaginated(personID uint, page, pageSize int) ([]*model.Face, int64, error)
	ListByIDs(ids []uint) ([]*model.Face, error)
	ListAssigned() ([]*model.Face, error)
	ListAssignedPersonIDs() ([]uint, error)
	ListPending(limit int) ([]*model.Face, error)
	GetPendingStats() (*PendingFaceStats, error)
	ListTopByPersonIDs(personIDs []uint, perPerson int) ([]*model.Face, error)
	ReassignFaces(faceIDs []uint, personID uint, reason string) error
	ListLowConfidence(threshold float64, maxGeneration int) ([]*model.Face, error)
	ResetForRecluster(ids []uint) error
}

type PendingFaceStats struct {
	Total          int64 `json:"total"`
	NeverClustered int64 `json:"never_clustered"`
	Retried        int64 `json:"retried"`
	Active         int64 `json:"active"`   // 当前可重试的人脸
	Backoff        int64 `json:"backoff"`  // 处于退避等待期的人脸
}

type faceRepository struct {
	db *gorm.DB
}

func NewFaceRepository(db *gorm.DB) FaceRepository {
	return &faceRepository{db: db}
}

func (r *faceRepository) Create(face *model.Face) error {
	return r.db.Create(face).Error
}

func (r *faceRepository) Update(face *model.Face) error {
	return r.db.Save(face).Error
}

func (r *faceRepository) UpdateFields(id uint, fields map[string]interface{}) error {
	return r.db.Model(&model.Face{}).Where("id = ?", id).Updates(fields).Error
}

func (r *faceRepository) UpdateClusterFields(ids []uint, fields map[string]interface{}) error {
	if len(ids) == 0 || len(fields) == 0 {
		return nil
	}
	return r.db.Model(&model.Face{}).Where("id IN ?", ids).Updates(fields).Error
}

func (r *faceRepository) GetByID(id uint) (*model.Face, error) {
	var face model.Face
	if err := r.db.Where("id = ?", id).First(&face).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &face, nil
}

func (r *faceRepository) DeleteByPhotoID(photoID uint) error {
	return r.db.Where("photo_id = ?", photoID).Delete(&model.Face{}).Error
}

func (r *faceRepository) ListByPhotoID(photoID uint) ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Where("photo_id = ?", photoID).Order("id ASC").Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListByPersonID(personID uint) ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Where("person_id = ?", personID).Order("id ASC").Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListByPersonIDSummary(personID uint) ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Select("id, created_at, updated_at, photo_id, person_id, b_box_x, b_box_y, b_box_width, b_box_height, confidence, quality_score, thumbnail_path, cluster_status, cluster_score, clustered_at, manual_locked, manual_lock_reason, manual_locked_at, recluster_generation, retry_count").
		Where("person_id = ?", personID).
		Order("quality_score DESC, id ASC").
		Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListByPersonIDPaginated(personID uint, page, pageSize int) ([]*model.Face, int64, error) {
	var total int64
	if err := r.db.Model(&model.Face{}).Where("person_id = ?", personID).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var faces []*model.Face
	offset := (page - 1) * pageSize
	err := r.db.Select("id, created_at, updated_at, photo_id, person_id, b_box_x, b_box_y, b_box_width, b_box_height, confidence, quality_score, thumbnail_path, cluster_status, cluster_score, clustered_at, manual_locked, manual_lock_reason, manual_locked_at, recluster_generation, retry_count").
		Where("person_id = ?", personID).
		Order("quality_score DESC, id ASC").
		Offset(offset).
		Limit(pageSize).
		Find(&faces).Error
	return faces, total, err
}

func (r *faceRepository) ListByIDs(ids []uint) ([]*model.Face, error) {
	var faces []*model.Face
	if len(ids) == 0 {
		return faces, nil
	}
	err := r.db.Where("id IN ?", ids).Order("id ASC").Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListAssigned() ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Where("person_id IS NOT NULL").Order("id ASC").Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListAssignedPersonIDs() ([]uint, error) {
	var ids []uint
	err := r.db.Model(&model.Face{}).
		Where("person_id IS NOT NULL").
		Distinct("person_id").
		Pluck("person_id", &ids).Error
	return ids, err
}

func (r *faceRepository) ListPending(limit int) ([]*model.Face, error) {
	var faces []*model.Face
	// 退避策略：根据 retry_count 计算最小重试间隔
	// retry_count = 0: 立即重试（从未尝试）
	// retry_count = 1: 立即重试（刚尝试过，可能马上有新数据）
	// retry_count = 2: 等待 1 分钟
	// retry_count = 3: 等待 5 分钟
	// retry_count = 4: 等待 15 分钟
	// retry_count >= 5: 等待 60 分钟
	// 使用 julianday 计算时间差（单位：天），然后与分钟阈值比较
	query := r.db.
		Where("cluster_status = ?", model.FaceClusterStatusPending).
		Where("clustered_at IS NULL OR " +
			"(julianday('now') - julianday(clustered_at)) * 24 * 60 >= " +
			"CASE retry_count " +
			"WHEN 0 THEN 0 " +
			"WHEN 1 THEN 0 " +
			"WHEN 2 THEN 1 " +
			"WHEN 3 THEN 5 " +
			"WHEN 4 THEN 15 " +
			"ELSE 60 END").
		Order("retry_count ASC").           // 重试次数少的优先
		Order("clustered_at ASC NULLS FIRST"). // 从未尝试的优先
		Order("id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&faces).Error
	return faces, err
}

func (r *faceRepository) GetPendingStats() (*PendingFaceStats, error) {
	stats := &PendingFaceStats{}
	// 计算活跃人脸：满足重试条件的人脸
	// 退避策略与 ListPending 一致
	// 使用 julianday 计算时间差（单位：天），转换为分钟后与阈值比较
	err := r.db.Model(&model.Face{}).
		Select(`
			COUNT(*) AS total,
			SUM(CASE WHEN clustered_at IS NULL THEN 1 ELSE 0 END) AS never_clustered,
			SUM(CASE WHEN clustered_at IS NOT NULL THEN 1 ELSE 0 END) AS retried,
			SUM(CASE WHEN clustered_at IS NULL OR
				(julianday('now') - julianday(clustered_at)) * 24 * 60 >=
				CASE retry_count
				WHEN 0 THEN 0
				WHEN 1 THEN 0
				WHEN 2 THEN 1
				WHEN 3 THEN 5
				WHEN 4 THEN 15
				ELSE 60 END
			THEN 1 ELSE 0 END) AS active,
			SUM(CASE WHEN clustered_at IS NOT NULL AND
				(julianday('now') - julianday(clustered_at)) * 24 * 60 <
				CASE retry_count
				WHEN 0 THEN 0
				WHEN 1 THEN 0
				WHEN 2 THEN 1
				WHEN 3 THEN 5
				WHEN 4 THEN 15
				ELSE 60 END
			THEN 1 ELSE 0 END) AS backoff
		`).
		Where("cluster_status = ?", model.FaceClusterStatusPending).
		Scan(stats).Error
	if err != nil {
		return nil, err
	}
	return stats, nil
}

func (r *faceRepository) ListTopByPersonIDs(personIDs []uint, perPerson int) ([]*model.Face, error) {
	var faces []*model.Face
	if len(personIDs) == 0 {
		return faces, nil
	}

	err := r.db.
		Where("person_id IN ?", personIDs).
		Order("person_id ASC").
		Order("manual_locked DESC").
		Order("quality_score DESC").
		Order("confidence DESC").
		Order("id ASC").
		Find(&faces).Error
	if err != nil {
		return nil, err
	}

	if perPerson <= 0 {
		return faces, nil
	}

	topFaces := make([]*model.Face, 0, len(faces))
	counts := make(map[uint]int, len(personIDs))
	for _, face := range faces {
		if face == nil || face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		personID := *face.PersonID
		if counts[personID] >= perPerson {
			continue
		}
		topFaces = append(topFaces, face)
		counts[personID]++
	}
	return topFaces, nil
}

func (r *faceRepository) ReassignFaces(faceIDs []uint, personID uint, reason string) error {
	if len(faceIDs) == 0 {
		return nil
	}
	now := time.Now()
	return r.db.Model(&model.Face{}).Where("id IN ?", faceIDs).Updates(map[string]interface{}{
		"person_id":          personID,
		"cluster_status":     model.FaceClusterStatusManual,
		"cluster_score":      1.0,
		"manual_locked":      true,
		"manual_lock_reason": reason,
		"manual_locked_at":   &now,
		"clustered_at":       &now,
	}).Error
}

func (r *faceRepository) ListLowConfidence(threshold float64, maxGeneration int) ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Where("manual_locked = ? AND cluster_status = ? AND cluster_score < ? AND recluster_generation < ?",
		false, model.FaceClusterStatusAssigned, threshold, maxGeneration).
		Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ResetForRecluster(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	return r.db.Model(&model.Face{}).
		Where("id IN ? AND manual_locked = ?", ids, false).
		Updates(map[string]interface{}{
			"person_id":            nil,
			"cluster_status":       model.FaceClusterStatusPending,
			"recluster_generation": gorm.Expr("recluster_generation + 1"),
		}).Error
}
