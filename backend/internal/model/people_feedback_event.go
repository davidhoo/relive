package model

import "time"

// PeopleFeedbackEvent 保存人工反馈以供后续阈值校准。
// 仅记录决策上下文（事件类型、涉及人物/人脸 ID、相似度快照），不保存 embedding、
// 图片路径或缩略图路径。JSON 字段沿用仓库现有 string + type:text 风格。
type PeopleFeedbackEvent struct {
	ID                 uint      `gorm:"primarykey" json:"id"`
	CreatedAt          time.Time `gorm:"index:idx_pfe_event_created,priority:2" json:"created_at"`
	EventType          string    `gorm:"type:varchar(50);not null;index:idx_pfe_event_created,priority:1" json:"event_type"`
	TargetPersonID     uint      `gorm:"not null" json:"target_person_id"`
	SourcePersonIDs    string    `gorm:"type:text" json:"source_person_ids"`
	FaceIDs            string    `gorm:"type:text" json:"face_ids"`
	AlgorithmVersion   string    `gorm:"type:varchar(50)" json:"algorithm_version,omitempty"`
	SimilaritySnapshot string    `gorm:"type:text" json:"similarity_snapshot"`
}

func (PeopleFeedbackEvent) TableName() string {
	return "people_feedback_events"
}
