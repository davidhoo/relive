package model

import "time"

// 扫描任务类型常量
const (
	ScanJobTypeScan    = "scan"
	ScanJobTypeRebuild = "rebuild"
)

// 扫描任务状态常量
const (
	ScanJobStatusPending     = "pending"
	ScanJobStatusRunning     = "running"
	ScanJobStatusStopping    = "stopping"
	ScanJobStatusStopped     = "stopped"
	ScanJobStatusCompleted   = "completed"
	ScanJobStatusFailed      = "failed"
	ScanJobStatusInterrupted = "interrupted"
)

// 扫描任务阶段常量
const (
	ScanJobPhasePending     = "pending"
	ScanJobPhaseDiscovering = "discovering"
	ScanJobPhaseProcessing  = "processing"
	ScanJobPhaseFinalizing  = "finalizing"
	ScanJobPhaseStopping    = "stopping"
)

// ScanJob 扫描/重建任务持久化模型
// 状态：pending / running / stopping / stopped / completed / failed / interrupted
// 阶段：pending / discovering / processing / finalizing / stopping
// 由于当前系统同一时刻只允许一个任务运行，因此表主要用于状态持久化与重启恢复。
type ScanJob struct {
	ID              string     `gorm:"primaryKey;type:varchar(64)" json:"id"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	Type            string     `gorm:"type:varchar(20);index:idx_scan_job_type_status;check:chk_scan_job_type,type IN ('scan','rebuild')" json:"type"`
	Status          string     `gorm:"type:varchar(20);index:idx_scan_job_type_status;check:chk_scan_job_status,status IN ('pending','running','stopping','stopped','completed','failed','interrupted')" json:"status"`
	Path            string     `gorm:"type:text" json:"path"`
	Phase           string     `gorm:"type:varchar(20);check:chk_scan_job_phase,phase IN ('pending','discovering','processing','finalizing','stopping')" json:"phase"`
	TotalFiles      int        `json:"total_files"`
	DiscoveredFiles int        `json:"discovered_files"`
	ProcessedFiles  int        `json:"processed_files"`
	NewPhotos       int        `json:"new_photos"`
	UpdatedPhotos   int        `json:"updated_photos"`
	DeletedPhotos   int        `json:"deleted_photos"`
	SkippedFiles    int        `json:"skipped_files"`
	CurrentFile     string     `gorm:"type:varchar(255)" json:"current_file,omitempty"`
	ErrorMessage    string     `gorm:"type:text" json:"error_message,omitempty"`
	StopRequestedAt *time.Time `json:"stop_requested_at,omitempty"`
	StartedAt       time.Time  `gorm:"index" json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
}

// TableName 指定表名
func (ScanJob) TableName() string {
	return "scan_jobs"
}
