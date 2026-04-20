package repository

import (
	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// CannotLinkRepository manages person-level cannot-link constraints.
type CannotLinkRepository interface {
	Create(personIDA, personIDB uint) error
	ExistsBetween(personIDA, personIDB uint) (bool, error)
	ListByPersonID(personID uint) ([]uint, error)
	ListAll() ([]model.CannotLinkConstraint, error)
	DeleteByPersonID(personID uint) error
}

type cannotLinkRepository struct {
	db *gorm.DB
}

func NewCannotLinkRepository(db *gorm.DB) CannotLinkRepository {
	return &cannotLinkRepository{db: db}
}

func (r *cannotLinkRepository) Create(personIDA, personIDB uint) error {
	if personIDA == personIDB {
		return nil
	}
	// Normalize order for consistency
	a, b := personIDA, personIDB
	if a > b {
		a, b = b, a
	}
	// Skip if already exists
	var count int64
	r.db.Model(&model.CannotLinkConstraint{}).
		Where("person_id_a = ? AND person_id_b = ?", a, b).
		Count(&count)
	if count > 0 {
		return nil
	}
	return r.db.Create(&model.CannotLinkConstraint{
		PersonIDA: a,
		PersonIDB: b,
	}).Error
}

func (r *cannotLinkRepository) ExistsBetween(personIDA, personIDB uint) (bool, error) {
	a, b := personIDA, personIDB
	if a > b {
		a, b = b, a
	}
	var count int64
	err := r.db.Model(&model.CannotLinkConstraint{}).
		Where("person_id_a = ? AND person_id_b = ?", a, b).
		Count(&count).Error
	return count > 0, err
}

func (r *cannotLinkRepository) ListByPersonID(personID uint) ([]uint, error) {
	var constraints []model.CannotLinkConstraint
	err := r.db.Where("person_id_a = ? OR person_id_b = ?", personID, personID).
		Find(&constraints).Error
	if err != nil {
		return nil, err
	}

	ids := make([]uint, 0, len(constraints))
	for _, c := range constraints {
		if c.PersonIDA == personID {
			ids = append(ids, c.PersonIDB)
		} else {
			ids = append(ids, c.PersonIDA)
		}
	}
	return ids, nil
}

func (r *cannotLinkRepository) DeleteByPersonID(personID uint) error {
	return r.db.Where("person_id_a = ? OR person_id_b = ?", personID, personID).
		Delete(&model.CannotLinkConstraint{}).Error
}

func (r *cannotLinkRepository) ListAll() ([]model.CannotLinkConstraint, error) {
	var constraints []model.CannotLinkConstraint
	err := r.db.Find(&constraints).Error
	return constraints, err
}
