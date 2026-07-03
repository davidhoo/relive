package model

import "time"

const (
	PeopleIdentityModeLegacy  = "legacy"
	PeopleIdentityModeShadow  = "shadow"
	PeopleIdentityModeRescue  = "rescue"
	PeopleIdentityModePrimary = "primary"
)

// 身份画像决策遥测的稳定 Decision 枚举。与 service 包的 identityDecision* 常量一一对应，
// 提供给 repository 层在 GetSummarySince 中按分类汇总，避免 service → repository 反向依赖。
const (
	PeopleIdentityDecisionAgree                 = "agree"
	PeopleIdentityDecisionDisagree              = "disagree"
	PeopleIdentityDecisionLegacyMissProfileHit  = "legacy_miss_profile_hit"
	PeopleIdentityDecisionLegacyMissProfileMiss = "legacy_miss_profile_miss"
	PeopleIdentityDecisionProfileMiss           = "profile_miss"
	PeopleIdentityDecisionProfileUnavailable    = "profile_unavailable"
	PeopleIdentityDecisionProfileBlocked        = "profile_blocked"
	PeopleIdentityDecisionRescueApplied         = "rescue_applied"
)

// PeopleIdentityDecision 保存 shadow/rescue 决策遥测，用于评估身份画像匹配器
// 相对 legacy 匹配器的表现。没有目标人物时，人物 ID 字段为 NULL，对应分数亦为 NULL。
// 不保存 embedding 或图片路径。
//
// 幂等去重：DecisionKey 唯一索引保证同一组件的完全相同决策只写入一条；组件结果
// 发生变化时 DecisionKey 不同，允许写入新记录。ComponentHash 覆盖全部排序去重后的
// Face ID（即使 ComponentFaceIDs 因超过 512 被截断展示），用于确定性采样与去重。
type PeopleIdentityDecision struct {
	ID                        uint      `gorm:"primarykey" json:"id"`
	CreatedAt                 time.Time `gorm:"index:idx_pid_created;index:idx_pid_mode_created,priority:2" json:"created_at"`
	Mode                      string    `gorm:"type:varchar(20);not null;index:idx_pid_mode_created,priority:1;check:chk_pid_mode,mode IN ('legacy','shadow','rescue','primary')" json:"mode"`
	ComponentHash             string    `gorm:"type:varchar(64);not null;index:idx_pid_component_hash" json:"component_hash"`
	ComponentSize             int       `gorm:"not null;default:0" json:"component_size"`
	ComponentFaceIDs          string    `gorm:"type:text" json:"component_face_ids"`
	ComponentFaceIDsTruncated bool      `gorm:"not null;default:false" json:"component_face_ids_truncated"`
	DecisionKey               string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_pid_decision_key" json:"decision_key"`
	LegacyTargetPersonID      *uint     `json:"legacy_target_person_id,omitempty"`
	LegacyScore               *float64  `json:"legacy_score,omitempty"`
	ProfileBestPersonID       *uint     `json:"profile_best_person_id,omitempty"`
	ProfileBestScore          *float64  `json:"profile_best_score,omitempty"`
	ProfileSecondPersonID     *uint     `json:"profile_second_person_id,omitempty"`
	ProfileSecondScore        *float64  `json:"profile_second_score,omitempty"`
	Margin                    float64   `gorm:"not null;default:0" json:"margin"`
	CenterIDs                 string    `gorm:"type:text" json:"center_ids"`
	Decision                  string    `gorm:"type:varchar(30);not null" json:"decision"`
	Reason                    string    `gorm:"type:varchar(100)" json:"reason,omitempty"`
	ElapsedMilliseconds       int       `gorm:"not null;default:0" json:"elapsed_milliseconds"`
	AlgorithmVersion          string    `gorm:"type:varchar(50)" json:"algorithm_version,omitempty"`
	IndexGeneration           int       `gorm:"not null;default:0" json:"index_generation"`
}

func (PeopleIdentityDecision) TableName() string {
	return "people_identity_decisions"
}
