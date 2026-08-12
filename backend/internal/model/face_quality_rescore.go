package model

import "time"

// 历史重评分运行模式。
const (
	FaceQualityRescoreModeCalibration = "calibration" // 校准：固定 shadow
	FaceQualityRescoreModeFull        = "full"        // 全量：可 enforce
)

// 历史重评分应用模式。
const (
	FaceQualityRescoreApplyModeShadow  = "shadow"  // 只写证据/候选，不自动排除
	FaceQualityRescoreApplyModeEnforce = "enforce" // 高置信非人脸/严重低质量自动隔离
)

// 历史重评分运行状态。
const (
	FaceQualityRescoreStatusQueued             = "queued"
	FaceQualityRescoreStatusRunning            = "running"
	FaceQualityRescoreStatusPaused             = "paused"
	FaceQualityRescoreStatusCompleted          = "completed"            // 无技术错误终态，可作为校准候选
	FaceQualityRescoreStatusCompletedWithError = "completed_with_errors" // 队列耗尽但存在 retryable/unmatched，不能放行 full/enforce
	FaceQualityRescoreStatusFailed             = "failed"
	FaceQualityRescoreStatusCancelled          = "cancelled"
)

// 历史重评分 item 状态。
const (
	FaceQualityRescoreItemStatusPending           = "pending"
	FaceQualityRescoreItemStatusProcessing        = "processing"
	FaceQualityRescoreItemStatusProcessed         = "processed"
	FaceQualityRescoreItemStatusSupersededManual  = "superseded_manual" // 运行中被人工作出结论，跳过
	FaceQualityRescoreItemStatusRetryableError    = "retryable_error"
	FaceQualityRescoreItemStatusUnmatched         = "unmatched"
)

// IsValidRescoreMode 校验运行模式是否合法
func IsValidRescoreMode(s string) bool {
	return s == FaceQualityRescoreModeCalibration || s == FaceQualityRescoreModeFull
}

// IsValidRescoreApplyMode 校验应用模式是否合法
func IsValidRescoreApplyMode(s string) bool {
	return s == FaceQualityRescoreApplyModeShadow || s == FaceQualityRescoreApplyModeEnforce
}

// FaceQualityRescoreRun 历史重评分运行。
// 创建时快照当前 historical_backfill + missing 的目标 Face 集合到 items 表，
// 使暂停/重启/人工并发/回滚都不改变目标集合。
type FaceQualityRescoreRun struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Mode      string `gorm:"type:varchar(20);not null;index:idx_fqr_run_mode" json:"mode"`             // calibration / full
	ApplyMode string `gorm:"type:varchar(10);not null" json:"apply_mode"`                              // shadow / enforce
	Status    string `gorm:"type:varchar(20);not null;index:idx_fqr_run_status" json:"status"`         // queued/running/paused/completed/failed/cancelled

	// 目标快照计数（创建时冻结）。
	TargetPhotoCount int `gorm:"not null;default:0" json:"target_photo_count"`
	TargetFaceCount  int `gorm:"not null;default:0" json:"target_face_count"`

	// 进度计数（worker 持久化）。
	ProcessedPhotoCount int   `gorm:"not null;default:0" json:"processed_photo_count"`
	ProcessedFaceCount  int   `gorm:"not null;default:0" json:"processed_face_count"`
	AcceptedCount       int   `gorm:"not null;default:0" json:"accepted_count"`
	ReviewRequiredCount int   `gorm:"not null;default:0" json:"review_required_count"`
	AutoExcludedCount   int   `gorm:"not null;default:0" json:"auto_excluded_count"`
	RetryableCount      int   `gorm:"not null;default:0" json:"retryable_count"`
	LastError           string `gorm:"type:text" json:"last_error,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	CompletedAt         *time.Time `json:"completed_at,omitempty"`

	// 当次 rule/model 版本快照（与 items 的 evidence 版本对齐，便于按 run 恢复）。
	RuleVersion  string `gorm:"type:varchar(20);not null" json:"rule_version"`
	ModelVersion string `gorm:"type:varchar(40);not null" json:"model_version"`

	// 校准选择策略/种子快照（calibration 时记录，便于复现）。
	SelectionPolicy string `gorm:"type:varchar(32);default:''" json:"selection_policy,omitempty"`
	PhotoLimit      int    `gorm:"not null;default:0" json:"photo_limit"`

	// superseded_manual_count：worker 发现人工已覆盖而跳过的 Face 数（安全终态，无新增模型证据）。
	SupersededManualCount int `gorm:"not null;default:0" json:"superseded_manual_count"`

	// retry_of_run_id：本 run 重试的来源 run（仅 retry 创建的 shadow calibration 有值）。
	RetryOfRunID *uint `gorm:"index:idx_fqr_run_retry_of" json:"retry_of_run_id,omitempty"`

	// calibration_run_id：full/enforce run 引用并通过验证的合格校准 run（校准 run 为空）。
	CalibrationRunID *uint `gorm:"index:idx_fqr_run_calibration" json:"calibration_run_id,omitempty"`
}

func (FaceQualityRescoreRun) TableName() string {
	return "face_quality_rescore_runs"
}

// FaceQualityRescoreItem 单个重评分目标快照。
// 创建 run 时为每个 historical_backfill + missing 的 Face 写一行，含起始 BBox 与 baseline_event_id。
type FaceQualityRescoreItem struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	RunID  uint `gorm:"not null;uniqueIndex:idx_fqr_item_run_face,priority:1;index:idx_fqr_item_status_photo,priority:1" json:"run_id"`
	PhotoID uint `gorm:"not null;index:idx_fqr_item_status_photo,priority:3" json:"photo_id"`
	FaceID  uint `gorm:"not null;uniqueIndex:idx_fqr_item_run_face,priority:2" json:"face_id"`

	// 快照归一化 BBox（创建时从 baseline 事件复制，永不随重检变化）。
	BBoxX      float64 `gorm:"column:bbox_x;not null" json:"bbox_x"`
	BBoxY      float64 `gorm:"column:bbox_y;not null" json:"bbox_y"`
	BBoxWidth  float64 `gorm:"column:bbox_width;not null" json:"bbox_width"`
	BBoxHeight float64 `gorm:"column:bbox_height;not null" json:"bbox_height"`

	// BaselineEventID 创建时该 Face 的当前历史缺证据事件 ID。
	// 写入结果时若该 Face 当前事件不再是 baseline（被人工作出结论），标 superseded_manual。
	BaselineEventID uint `gorm:"not null" json:"baseline_event_id"`

	Status        string `gorm:"type:varchar(24);not null;index:idx_fqr_item_status_photo,priority:2" json:"status"`
	AttemptCount  int    `gorm:"not null;default:0" json:"attempt_count"`
	LastError     string `gorm:"type:text" json:"last_error,omitempty"`
	MatchedIoU    *float64 `json:"matched_iou,omitempty"`
}

func (FaceQualityRescoreItem) TableName() string {
	return "face_quality_rescore_items"
}

// FaceQualityRescoreRetryTarget 是 retry 创建新 run 时从来源 run 当前失败事件快照的单个目标。
type FaceQualityRescoreRetryTarget struct {
	PhotoID         uint
	FaceID          uint
	BBoxX           float64
	BBoxY           float64
	BBoxWidth       float64
	BBoxHeight      float64
	BaselineEventID uint
	EvidenceState   string
}
