package model

import "time"

const (
	PeopleIdentityModeLegacy  = "legacy"
	PeopleIdentityModeShadow  = "shadow"
	PeopleIdentityModeRescue  = "rescue"
	PeopleIdentityModePrimary = "primary"
)

// PeopleIdentityDecision 保存 shadow/rescue 决策遥测，用于评估身份画像匹配器
// 相对 legacy 匹配器的表现。没有目标人物时，人物 ID 字段为 NULL，对应分数亦为 NULL。
// 不保存 embedding 或图片路径。
type PeopleIdentityDecision struct {
	ID                    uint      `gorm:"primarykey" json:"id"`
	CreatedAt             time.Time `gorm:"index:idx_pid_mode_created,priority:2" json:"created_at"`
	Mode                  string    `gorm:"type:varchar(20);not null;index:idx_pid_mode_created,priority:1;check:chk_pid_mode,mode IN ('legacy','shadow','rescue','primary')" json:"mode"`
	ComponentFaceIDs      string    `gorm:"type:text" json:"component_face_ids"`
	LegacyTargetPersonID  *uint     `json:"legacy_target_person_id,omitempty"`
	LegacyScore           *float64  `json:"legacy_score,omitempty"`
	ProfileBestPersonID   *uint     `json:"profile_best_person_id,omitempty"`
	ProfileBestScore      *float64  `json:"profile_best_score,omitempty"`
	ProfileSecondPersonID *uint     `json:"profile_second_person_id,omitempty"`
	ProfileSecondScore    *float64  `json:"profile_second_score,omitempty"`
	Margin                float64   `gorm:"not null;default:0" json:"margin"`
	CenterIDs             string    `gorm:"type:text" json:"center_ids"`
	Decision              string    `gorm:"type:varchar(30);not null" json:"decision"`
	Reason                string    `gorm:"type:varchar(100)" json:"reason,omitempty"`
	ElapsedMilliseconds   int       `gorm:"not null;default:0" json:"elapsed_milliseconds"`
	AlgorithmVersion      string    `gorm:"type:varchar(50)" json:"algorithm_version,omitempty"`
	IndexGeneration       int       `gorm:"not null;default:0" json:"index_generation"`
}

func (PeopleIdentityDecision) TableName() string {
	return "people_identity_decisions"
}
