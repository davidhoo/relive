package repository

import (
	"sort"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// sqliteVarLimit is the maximum number of host parameters per SQLite statement.
// SQLite default is 999 (older builds) or 32766 (3.32+). Use 500 for safety.
const sqliteVarLimit = 500

// chunkIDs splits a slice into chunks no larger than sqliteVarLimit.
func chunkIDs(ids []uint) [][]uint {
	if len(ids) <= sqliteVarLimit {
		return [][]uint{ids}
	}
	var chunks [][]uint
	for i := 0; i < len(ids); i += sqliteVarLimit {
		end := i + sqliteVarLimit
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[i:end])
	}
	return chunks
}

type FaceRepository interface {
	Create(face *model.Face) error
	Update(face *model.Face) error
	UpdateFields(id uint, fields map[string]interface{}) error
	UpdateClusterFields(ids []uint, fields map[string]interface{}) error
	GetByID(id uint) (*model.Face, error)
	DeleteByPhotoID(photoID uint) error
	ListByPhotoID(photoID uint) ([]*model.Face, error)
	ListByPersonID(personID uint) ([]*model.Face, error)
	ListByPersonIDSummary(personID uint) ([]*model.Face, error) // 排除 embedding，按 quality_score 排序
	ListByPersonIDPaginated(personID uint, page, pageSize int) ([]*model.Face, int64, error)
	ListByIDs(ids []uint) ([]*model.Face, error)
	ListAssigned() ([]*model.Face, error)
	ListAssignedPersonIDs() ([]uint, error)
	ListPending(limit int) ([]*model.Face, error)
	GetPendingStats() (*PendingFaceStats, error)
	ListPrototypeEmbeddings(personIDs []uint, perPerson int) ([]*model.Face, error)
	// ListProfileFaces loads the lightweight fields plus embedding needed to build an
	// identity profile for a person, ordered deterministically by manual lock, cluster
	// confidence, quality, then ID.
	ListProfileFaces(personID uint) ([]*model.Face, error)
	ReassignFaces(faceIDs []uint, personID uint, reason string) error
	ListLowConfidence(threshold float64, maxGeneration int) ([]*model.Face, error)
	ResetForRecluster(ids []uint) error
	// ListPersonIDsCooccurringWithPhotos 批量返回在指定照片中出现、且属于候选人物集合的
	// 人物 ID（去重升序）。用于 matcher 同照片共现负证据判断，避免逐候选 N+1 查询。
	// 两组 ID 均去重并忽略 0；按 SQLite 参数上限对两个维度分块；任一输入为空直接返回 nil。
	ListPersonIDsCooccurringWithPhotos(photoIDs []uint, candidatePersonIDs []uint) ([]uint, error)
	// ListPersonIDsSharingPhotos 返回与 targetPersonID 出现在同一照片中的候选人物 ID
	// （去重升序）。用于合并建议人工审核的同照片共现警告判断：仅判断两个已有人物是否
	// 同照片共现，不加载目标人物全部照片，也不逐候选查询。
	// 候选 ID 去重并忽略 0；按 SQLite 参数上限分块；利用 faces.photo_id / faces.person_id
	// 索引；任一候选为空直接返回 nil。查询失败时调用方须回退 legacy，禁止静默忽略。
	ListPersonIDsSharingPhotos(targetPersonID uint, candidatePersonIDs []uint) ([]uint, error)
}

type PendingFaceStats struct {
	Total          int64 `json:"total"`
	NeverClustered int64 `json:"never_clustered"`
	Retried        int64 `json:"retried"`
	TotalFaces     int64 `json:"total_faces"`
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
	for _, chunk := range chunkIDs(ids) {
		if err := r.db.Model(&model.Face{}).Where("id IN ?", chunk).Updates(fields).Error; err != nil {
			return err
		}
	}
	return nil
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
	if len(ids) == 0 {
		return nil, nil
	}
	var allFaces []*model.Face
	for _, chunk := range chunkIDs(ids) {
		var faces []*model.Face
		if err := r.db.Where("id IN ?", chunk).Order("id ASC").Find(&faces).Error; err != nil {
			return nil, err
		}
		allFaces = append(allFaces, faces...)
	}
	return allFaces, nil
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
		Order("retry_count ASC").              // 重试次数少的优先
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
	err := r.db.Model(&model.Face{}).
		Select(`
			COUNT(*) AS total,
			SUM(CASE WHEN clustered_at IS NULL THEN 1 ELSE 0 END) AS never_clustered,
			SUM(CASE WHEN clustered_at IS NOT NULL THEN 1 ELSE 0 END) AS retried
		`).
		Where("cluster_status = ?", model.FaceClusterStatusPending).
		Scan(stats).Error
	if err != nil {
		return nil, err
	}

	r.db.Model(&model.Face{}).Count(&stats.TotalFaces)

	return stats, nil
}

// ListPrototypeEmbeddings loads lightweight metadata and embedding for the top perPerson
// faces per person, using a window function to avoid fetching all faces from the DB.
func (r *faceRepository) ListPrototypeEmbeddings(personIDs []uint, perPerson int) ([]*model.Face, error) {
	if len(personIDs) == 0 {
		return nil, nil
	}
	if perPerson <= 0 {
		perPerson = 1
	}

	var allFaces []*model.Face
	for _, chunk := range chunkIDs(personIDs) {
		var faces []*model.Face
		err := r.db.Raw(`
			SELECT id, person_id, quality_score, manual_locked, embedding FROM (
				SELECT id, person_id, quality_score, manual_locked, embedding,
					ROW_NUMBER() OVER (
						PARTITION BY person_id
						ORDER BY manual_locked DESC, quality_score DESC, confidence DESC, id ASC
					) AS rn
				FROM faces
				WHERE person_id IN ?
			) sub
			WHERE rn <= ?
		`, chunk, perPerson).Scan(&faces).Error
		if err != nil {
			return nil, err
		}
		allFaces = append(allFaces, faces...)
	}
	return allFaces, nil
}

func (r *faceRepository) ReassignFaces(faceIDs []uint, personID uint, reason string) error {
	if len(faceIDs) == 0 {
		return nil
	}
	now := time.Now()
	fields := map[string]interface{}{
		"person_id":          personID,
		"cluster_status":     model.FaceClusterStatusManual,
		"cluster_score":      1.0,
		"manual_locked":      true,
		"manual_lock_reason": reason,
		"manual_locked_at":   &now,
		"clustered_at":       &now,
	}
	for _, chunk := range chunkIDs(faceIDs) {
		if err := r.db.Model(&model.Face{}).Where("id IN ?", chunk).Updates(fields).Error; err != nil {
			return err
		}
	}
	return nil
}

// ListProfileFaces selects only the fields an identity profile build needs (lightweight
// metadata plus the embedding blob), avoiding thumbnails/bbox payloads. Ordering matches
// the prototype loader so builds are deterministic regardless of insertion order.
func (r *faceRepository) ListProfileFaces(personID uint) ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Select("id, photo_id, person_id, confidence, quality_score, embedding, cluster_status, cluster_score, manual_locked, manual_lock_reason").
		Where("person_id = ?", personID).
		Order("manual_locked DESC, cluster_score DESC, quality_score DESC, confidence DESC, id ASC").
		Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ListLowConfidence(threshold float64, maxGeneration int) ([]*model.Face, error) {
	var faces []*model.Face
	err := r.db.Select("id, person_id").
		Where("manual_locked = ? AND cluster_status = ? AND cluster_score < ? AND recluster_generation < ?",
			false, model.FaceClusterStatusAssigned, threshold, maxGeneration).
		Find(&faces).Error
	return faces, err
}

func (r *faceRepository) ResetForRecluster(ids []uint) error {
	if len(ids) == 0 {
		return nil
	}
	fields := map[string]interface{}{
		"person_id":            nil,
		"cluster_status":       model.FaceClusterStatusPending,
		"recluster_generation": gorm.Expr("recluster_generation + 1"),
	}
	for _, chunk := range chunkIDs(ids) {
		if err := r.db.Model(&model.Face{}).
			Where("id IN ? AND manual_locked = ?", chunk, false).
			Updates(fields).Error; err != nil {
			return err
		}
	}
	return nil
}

// ListPersonIDsCooccurringWithPhotos 批量返回在指定照片中出现、且属于候选人物集合的
// 人物 ID。SQL 语义：
//
//	SELECT DISTINCT person_id FROM faces
//	WHERE photo_id IN (...) AND person_id IN (...) AND person_id IS NOT NULL;
//
// photo/person ID 均去重并忽略 0；同时对两组参数分块，保证每条 SQL 总参数数不超过
// sqliteVarLimit；利用现有 idx_face_photo / idx_face_person 索引，不做全表加载；
// 返回人物 ID 去重升序；任一输入为空直接返回 nil；不允许每个候选执行一次 SQL。
func (r *faceRepository) ListPersonIDsCooccurringWithPhotos(photoIDs []uint, candidatePersonIDs []uint) ([]uint, error) {
	photos := dedupIDs(photoIDs)
	persons := dedupIDs(candidatePersonIDs)
	if len(photos) == 0 || len(persons) == 0 {
		return nil, nil
	}
	seen := make(map[uint]struct{})
	var out []uint
	for _, pchunk := range chunkIDs(photos) {
		for _, cchunk := range chunkIDs(persons) {
			var ids []uint
			err := r.db.Model(&model.Face{}).
				Where("photo_id IN ? AND person_id IN ?", pchunk, cchunk).
				Distinct("person_id").
				Pluck("person_id", &ids).Error
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				if _, ok := seen[id]; !ok {
					seen[id] = struct{}{}
					out = append(out, id)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// ListPersonIDsSharingPhotos 返回与 targetPersonID 出现在同一照片中的候选人物 ID。
// SQL 语义：
//
//	SELECT DISTINCT candidate.person_id
//	FROM faces AS target
//	JOIN faces AS candidate ON candidate.photo_id = target.photo_id
//	WHERE target.person_id = ?
//	  AND candidate.person_id IN (...)
//	  AND candidate.person_id != target.person_id;
//
// 仅做已有人物同照片共现判断：不加载目标人物全部 Photo ID，也不逐候选查询；候选 ID
// 去重并忽略 0；按 SQLite 参数上限分块；利用 faces.photo_id / faces.person_id 索引；
// 返回人物 ID 去重升序；任一候选为空直接返回 nil。
func (r *faceRepository) ListPersonIDsSharingPhotos(targetPersonID uint, candidatePersonIDs []uint) ([]uint, error) {
	persons := dedupIDs(candidatePersonIDs)
	if targetPersonID == 0 || len(persons) == 0 {
		return nil, nil
	}
	seen := make(map[uint]struct{})
	var out []uint
	for _, cchunk := range chunkIDs(persons) {
		var ids []uint
		err := r.db.Model(&model.Face{}).
			Where("person_id IN ? AND photo_id IN (?)",
				cchunk,
				r.db.Model(&model.Face{}).Select("photo_id").Where("person_id = ?", targetPersonID),
			).
			Where("person_id != ?", targetPersonID).
			Distinct("person_id").
			Pluck("person_id", &ids).Error
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
