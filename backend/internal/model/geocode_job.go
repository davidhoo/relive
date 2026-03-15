package model

import "time"

// Geocode 任务状态常量
const (
	GeocodeJobStatusPending    = "pending"
	GeocodeJobStatusQueued     = "queued"
	GeocodeJobStatusProcessing = "processing"
	GeocodeJobStatusCompleted  = "completed"
	GeocodeJobStatusFailed     = "failed"
	GeocodeJobStatusCancelled  = "cancelled"
)

// Geocode 任务来源常量
const (
	GeocodeJobSourceScan    = "scan"
	GeocodeJobSourcePassive = "passive"
	GeocodeJobSourceManual  = "manual"
)

// GeocodeJob GPS 逆地理编码任务
// 状态：pending / queued / processing / completed / failed / cancelled
// source：scan / passive / manual
// priority：越大越优先，结合 last_requested_at 实现热点优先 + FIFO。
type GeocodeJob struct {
	ID              uint       `gorm:"primarykey" json:"id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	PhotoID         uint       `gorm:"not null;index:idx_geocode_job_photo" json:"photo_id"`
	Status          string     `gorm:"type:varchar(20);index:idx_geocode_job_status;index:idx_geocode_job_claim,priority:1;check:chk_geocode_job_status,status IN ('pending','queued','processing','completed','failed','cancelled')" json:"status"`
	Priority        int        `gorm:"not null;default:0;index:idx_geocode_job_priority;index:idx_geocode_job_claim,priority:2,sort:desc" json:"priority"`
	Source          string     `gorm:"type:varchar(20);not null;check:chk_geocode_job_source,source IN ('scan','passive','manual')" json:"source"`
	AttemptCount    int        `gorm:"not null;default:0" json:"attempt_count"`
	LastError       string     `gorm:"type:text" json:"last_error,omitempty"`
	QueuedAt        time.Time  `gorm:"index;index:idx_geocode_job_claim,priority:3" json:"queued_at"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	LastRequestedAt *time.Time `gorm:"index" json:"last_requested_at,omitempty"`
}

func (GeocodeJob) TableName() string {
	return "geocode_jobs"
}
