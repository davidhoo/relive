package repository

import (
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
	RefreshStats(personID uint) error
	MergeInto(targetPersonID uint, sourcePersonIDs []uint) ([]uint, error)
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

func (r *personRepository) ListByIDs(ids []uint) ([]*model.Person, error) {
	var people []*model.Person
	if len(ids) == 0 {
		return people, nil
	}
	err := r.db.Where("id IN ?", ids).Order("id ASC").Find(&people).Error
	return people, err
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

		if err := tx.Model(&model.Face{}).
			Distinct("photo_id").
			Where("person_id IN ?", append([]uint{targetPersonID}, sourceIDs...)).
			Order("photo_id ASC").
			Pluck("photo_id", &affectedPhotoIDs).Error; err != nil {
			return err
		}

		if err := tx.Model(&model.Face{}).
			Where("person_id IN ?", sourceIDs).
			Updates(map[string]interface{}{
				"person_id":          targetPersonID,
				"manual_locked":      true,
				"manual_lock_reason": "merge",
				"manual_locked_at":   time.Now(),
			}).Error; err != nil {
			return err
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
