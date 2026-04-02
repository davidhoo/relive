package model

import "time"

const (
	PeopleJobStatusPending    = "pending"
	PeopleJobStatusQueued     = "queued"
	PeopleJobStatusProcessing = "processing"
	PeopleJobStatusCompleted  = "completed"
	PeopleJobStatusFailed     = "failed"
	PeopleJobStatusCancelled  = "cancelled"
)

const (
	PeopleJobSourceScan    = "scan"
	PeopleJobSourcePassive = "passive"
	PeopleJobSourceManual  = "manual"
)

// PeopleJob 人物处理后台任务
type PeopleJob struct {
	ID              uint       `gorm:"primarykey" json:"id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	PhotoID         uint       `gorm:"not null;index:idx_people_job_photo" json:"photo_id"`
	FilePath        string     `gorm:"type:text;not null" json:"file_path"`
	Status          string     `gorm:"type:varchar(20);index:idx_people_job_status;index:idx_people_job_claim,priority:1;check:chk_people_job_status,status IN ('pending','queued','processing','completed','failed','cancelled')" json:"status"`
	Priority        int        `gorm:"not null;default:0;index:idx_people_job_priority;index:idx_people_job_claim,priority:2,sort:desc" json:"priority"`
	Source          string     `gorm:"type:varchar(20);not null;check:chk_people_job_source,source IN ('scan','passive','manual')" json:"source"`
	AttemptCount    int        `gorm:"not null;default:0" json:"attempt_count"`
	LastError       string     `gorm:"type:text" json:"last_error,omitempty"`
	QueuedAt        time.Time  `gorm:"index;index:idx_people_job_claim,priority:3" json:"queued_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	LastRequestedAt *time.Time `gorm:"index" json:"last_requested_at,omitempty"`
}

func (PeopleJob) TableName() string {
	return "people_jobs"
}
