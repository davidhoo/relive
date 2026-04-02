package model

import "time"

const (
	PersonCategoryFamily       = "family"
	PersonCategoryFriend       = "friend"
	PersonCategoryAcquaintance = "acquaintance"
	PersonCategoryStranger     = "stranger"
)

var PersonCategories = []string{
	PersonCategoryFamily,
	PersonCategoryFriend,
	PersonCategoryAcquaintance,
	PersonCategoryStranger,
}

// Person 系统聚类后的真实人物对象
type Person struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Name string `gorm:"type:varchar(100)" json:"name,omitempty"`

	Category string `gorm:"type:varchar(20);default:'stranger';index:idx_people_category;check:chk_people_category,category IN ('family','friend','acquaintance','stranger')" json:"category"`

	RepresentativeFaceID *uint `gorm:"index:idx_people_representative_face" json:"representative_face_id,omitempty"`
	AvatarLocked         bool  `gorm:"not null;default:false" json:"avatar_locked"`
	FaceCount            int   `gorm:"not null;default:0" json:"face_count"`
	PhotoCount           int   `gorm:"not null;default:0" json:"photo_count"`
}

func (Person) TableName() string {
	return "people"
}
