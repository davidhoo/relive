package model

import "time"

// CannotLinkConstraint records that two persons must not be merged.
// Created when a user splits faces from a person — the source and new person are constrained.
type CannotLinkConstraint struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	PersonIDA uint      `gorm:"not null;index:idx_cl_person_a" json:"person_id_a"`
	PersonIDB uint      `gorm:"not null;index:idx_cl_person_b" json:"person_id_b"`
}

func (CannotLinkConstraint) TableName() string {
	return "cannot_link_constraints"
}
