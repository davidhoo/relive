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

	// Hidden 是人物参与识别、聚类、画像和合并建议的唯一开关：
	//   hidden=false：正常显示并参与人物识别、聚类、画像和合并建议；
	//   hidden=true ：从默认人物列表隐藏，同时退出识别、聚类、画像和合并建议。
	// 原有人脸的 person_id/cluster_status/人工锁定/聚类分数一律保留，不转成 excluded，
	// 照片计数、照片关联与 top_person_category 不因隐藏重算。仅控制参与资格，与分类独立。
	Hidden bool `gorm:"not null;default:false;index:idx_people_hidden" json:"hidden"`
}

func (Person) TableName() string {
	return "people"
}
