package model

import "time"

// PeopleMergeJobType 任务类型
const (
	PeopleMergeJobTypeMergeInto   = "merge_into"   // 合并其他人物到当前人物
	PeopleMergeJobTypeMergeTo     = "merge_to"     // 合并当前人物到目标人物
)

// PeopleMergeJobStatus 任务状态
const (
	PeopleMergeJobStatusPending    = "pending"
	PeopleMergeJobStatusProcessing = "processing"
	PeopleMergeJobStatusCompleted  = "completed"
	PeopleMergeJobStatusFailed     = "failed"
)

// PeopleMergeJob 人物合并后台任务
// 由于人物合并涉及的数据量可能很大（大量 faces 需要更新），同步执行容易超时
// 所以采用异步任务模式，创建任务后立即返回 job_id，前端轮询任务状态
type PeopleMergeJob struct {
	ID            uint       `gorm:"primarykey" json:"id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Type          string     `gorm:"type:varchar(20);not null;check:chk_people_merge_job_type,type IN ('merge_into','merge_to')" json:"type"`
	Status        string     `gorm:"type:varchar(20);not null;check:chk_people_merge_job_status,status IN ('pending','processing','completed','failed');index" json:"status"`
	TargetID      uint       `gorm:"not null" json:"target_id"`       // 目标人物 ID
	SourceIDs     string     `gorm:"type:text" json:"source_ids"`    // 源人物 ID 列表（JSON 数组）
	Result        string     `gorm:"type:text" json:"result,omitempty"` // 执行结果（JSON）
	ErrorMessage  string     `gorm:"type:text" json:"error_message,omitempty"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
}

func (PeopleMergeJob) TableName() string {
	return "people_merge_jobs"
}

// PeopleMergeJobResult 合并任务结果
type PeopleMergeJobResult struct {
	AffectedPhotoCount int              `json:"affected_photo_count"`
	ReclusterResult    *ReclusterResult `json:"recluster_result,omitempty"`
}
