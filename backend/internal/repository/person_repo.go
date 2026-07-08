package repository

import (
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

type PersonRepository interface {
	Create(person *model.Person) error
	Update(person *model.Person) error
	UpdateFields(id uint, fields map[string]interface{}) error
	GetByID(id uint) (*model.Person, error)
	Delete(id uint) error
	ListAll() ([]*model.Person, error)
	ListByIDs(ids []uint) ([]*model.Person, error)
	// ListByNameExact 返回 name 完全匹配（大小写不敏感）的人物，用于人脸级改名时
	// 判断输入姓名是否命中已有人物。空名不返回结果。
	ListByNameExact(name string) ([]*model.Person, error)
	ListMergeSuggestionTargets(cursorID uint, limit int) ([]*model.Person, error) // cursor 分页，只返回 family/friend 且有脸的人物
	ListWithAvatar() ([]*model.Person, error)                                     // 只返回有头像的人物（用于合并/移动候选列表）
	ListPeople(opts ListPeopleOptions) ([]*model.Person, int64, error)            // 数据库层分页查询
	RefreshStats(personID uint) error
	MergeInto(targetPersonID uint, sourcePersonIDs []uint) ([]uint, error)
	// UpdateVisibility 批量设置人物隐藏状态，单次事务更新，返回实际更新行数。
	// 仅修改 hidden 字段，不影响分类、人脸、照片及 top_person_category。
	UpdateVisibility(personIDs []uint, hidden bool) (int64, error)
}

// 可见性筛选值：仅显示中 / 仅已隐藏 / 全部。空值按全部处理，保持合并、移动候选等现有调用兼容。
const (
	PersonVisibilityVisible = "visible"
	PersonVisibilityHidden  = "hidden"
	PersonVisibilityAll     = "all"
)

type ListPeopleOptions struct {
	Page       int
	PageSize   int
	Category   string
	Search     string
	HasAvatar  bool
	Visibility string // visible | hidden | all，缺省按 all
}

type personRepository struct {
	db *gorm.DB
}

func NewPersonRepository(db *gorm.DB) PersonRepository {
	return &personRepository{db: db}
}

func (r *personRepository) Create(person *model.Person) error {
	return r.db.Create(person).Error
}

func (r *personRepository) Update(person *model.Person) error {
	return r.db.Save(person).Error
}

func (r *personRepository) UpdateFields(id uint, fields map[string]interface{}) error {
	return r.db.Model(&model.Person{}).Where("id = ?", id).Updates(fields).Error
}

func (r *personRepository) GetByID(id uint) (*model.Person, error) {
	var person model.Person
	if err := r.db.Where("id = ?", id).First(&person).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &person, nil
}

func (r *personRepository) Delete(id uint) error {
	return r.db.Delete(&model.Person{}, "id = ?", id).Error
}

func (r *personRepository) ListAll() ([]*model.Person, error) {
	var people []*model.Person
	err := r.db.Order("id ASC").Find(&people).Error
	return people, err
}

func (r *personRepository) ListMergeSuggestionTargets(cursorID uint, limit int) ([]*model.Person, error) {
	if limit <= 0 {
		limit = 100
	}
	var people []*model.Person
	q := r.db.Where("category IN ? AND face_count > 0", []string{
		model.PersonCategoryFamily,
		model.PersonCategoryFriend,
		model.PersonCategoryAcquaintance,
	}).Order("id ASC").Limit(limit)
	if cursorID > 0 {
		q = q.Where("id > ?", cursorID)
	}
	err := q.Find(&people).Error
	return people, err
}

func (r *personRepository) ListByIDs(ids []uint) ([]*model.Person, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var allPeople []*model.Person
	for _, chunk := range chunkIDs(ids) {
		var people []*model.Person
		if err := r.db.Where("id IN ?", chunk).Order("id ASC").Find(&people).Error; err != nil {
			return nil, err
		}
		allPeople = append(allPeople, people...)
	}
	return allPeople, nil
}

func (r *personRepository) ListWithAvatar() ([]*model.Person, error) {
	var people []*model.Person
	err := r.db.Where("representative_face_id IS NOT NULL").
		Order("id ASC").
		Find(&people).Error
	return people, err
}

func (r *personRepository) ListByNameExact(name string) ([]*model.Person, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return nil, nil
	}
	var people []*model.Person
	err := r.db.Where("LOWER(name) = LOWER(?)", trimmed).Order("id ASC").Find(&people).Error
	return people, err
}

func (r *personRepository) ListPeople(opts ListPeopleOptions) ([]*model.Person, int64, error) {
	q := r.db.Model(&model.Person{})

	if opts.HasAvatar {
		q = q.Where("representative_face_id IS NOT NULL")
	}
	if opts.Category != "" {
		q = q.Where("category = ?", opts.Category)
	}
	switch opts.Visibility {
	case PersonVisibilityVisible:
		q = q.Where("hidden = ?", false)
	case PersonVisibilityHidden:
		q = q.Where("hidden = ?", true)
	} // all 或空值：不附加可见性条件
	if opts.Search != "" {
		like := "%" + opts.Search + "%"
		q = q.Where("LOWER(name) LIKE ? OR LOWER(category) LIKE ? OR CAST(id AS TEXT) LIKE ?", like, like, like)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	orderBy := "CASE category WHEN 'family' THEN 0 WHEN 'friend' THEN 1 WHEN 'acquaintance' THEN 2 WHEN 'stranger' THEN 3 ELSE 4 END, photo_count DESC, face_count DESC, id ASC"

	var people []*model.Person
	offset := (opts.Page - 1) * opts.PageSize
	err := q.Order(orderBy).Offset(offset).Limit(opts.PageSize).Find(&people).Error
	return people, total, err
}

func (r *personRepository) RefreshStats(personID uint) error {
	return refreshPersonStats(r.db, personID)
}

func (r *personRepository) MergeInto(targetPersonID uint, sourcePersonIDs []uint) ([]uint, error) {
	sourceIDs := make([]uint, 0, len(sourcePersonIDs))
	for _, id := range sourcePersonIDs {
		if id != 0 && id != targetPersonID {
			sourceIDs = append(sourceIDs, id)
		}
	}
	if len(sourceIDs) == 0 {
		return nil, nil
	}

	affectedPhotoIDs := make([]uint, 0)
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var targetExists int64
		if err := tx.Model(&model.Person{}).Where("id = ?", targetPersonID).Count(&targetExists).Error; err != nil {
			return err
		}
		if targetExists == 0 {
			return gorm.ErrRecordNotFound
		}

		// 仅收集来源人物涉及的照片 ID：合并只会改变来源人物人脸的归属，
		// 仅含目标人物且人脸归属未变的照片无需重算。Distinct 自带去重，
		// 多个来源人物或目标与来源出现在同一照片时不会重复。
		if err := tx.Model(&model.Face{}).
			Distinct("photo_id").
			Where("person_id IN ?", sourceIDs).
			Order("photo_id ASC").
			Pluck("photo_id", &affectedPhotoIDs).Error; err != nil {
			return err
		}

		mergeFields := map[string]interface{}{
			"person_id":          targetPersonID,
			"cluster_status":     model.FaceClusterStatusManual,
			"cluster_score":      1.0,
			"manual_locked":      true,
			"manual_lock_reason": "merge",
			"manual_locked_at":   time.Now(),
			"clustered_at":       time.Now(),
		}
		for _, chunk := range chunkIDs(sourceIDs) {
			if err := tx.Model(&model.Face{}).
				Where("person_id IN ?", chunk).
				Updates(mergeFields).Error; err != nil {
				return err
			}
		}

		if err := tx.Delete(&model.Person{}, sourceIDs).Error; err != nil {
			return err
		}

		return refreshPersonStats(tx, targetPersonID)
	})
	if err != nil {
		return nil, err
	}

	return affectedPhotoIDs, nil
}

// UpdateVisibility 批量设置人物隐藏状态。
// 单次事务内按 ID 分块更新，仅写入 hidden 字段；返回受影响行数。
// 不存在的 ID 不会报错，仅不计入更新行数。
func (r *personRepository) UpdateVisibility(personIDs []uint, hidden bool) (int64, error) {
	if len(personIDs) == 0 {
		return 0, nil
	}

	// 去重
	seen := make(map[uint]struct{}, len(personIDs))
	uniqueIDs := make([]uint, 0, len(personIDs))
	for _, id := range personIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}

	var total int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		for _, chunk := range chunkIDs(uniqueIDs) {
			res := tx.Model(&model.Person{}).
				Where("id IN ?", chunk).
				Update("hidden", hidden)
			if res.Error != nil {
				return res.Error
			}
			total += res.RowsAffected
		}
		return nil
	})
	return total, err
}

func refreshPersonStats(tx *gorm.DB, personID uint) error {
	type stats struct {
		FaceCount  int
		PhotoCount int
	}

	var result stats
	if err := tx.Model(&model.Face{}).
		Select("COUNT(*) as face_count, COUNT(DISTINCT photo_id) as photo_count").
		Where("person_id = ?", personID).
		Scan(&result).Error; err != nil {
		return err
	}

	return tx.Model(&model.Person{}).Where("id = ?", personID).Updates(map[string]interface{}{
		"face_count":  result.FaceCount,
		"photo_count": result.PhotoCount,
	}).Error
}
