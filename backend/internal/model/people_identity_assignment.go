package model

import "time"

// People identity assignment batch statuses.
const (
	PeopleIdentityAssignmentBatchRunning     = "running"
	PeopleIdentityAssignmentBatchCompleted   = "completed"
	PeopleIdentityAssignmentBatchPartial     = "partial"
	PeopleIdentityAssignmentBatchFailed      = "failed"
	PeopleIdentityAssignmentBatchInterrupted = "interrupted"
	PeopleIdentityAssignmentBatchRevoked     = "revoked"
)

// Decision sources recorded on per-face assignment changes (primary mode).
const (
	PeopleIdentityDecisionSourceProfileAttach = "profile_attach"
	PeopleIdentityDecisionSourceCreatePerson  = "create_person"
)

// Revocation statuses.
const (
	PeopleIdentityRevocationStatusRunning   = "running"
	PeopleIdentityRevocationStatusCompleted = "completed"
	PeopleIdentityRevocationStatusFailed    = "failed"
)

const (
	PeopleIdentityRevocationItemReverted = "reverted"
	PeopleIdentityRevocationItemSkipped  = "skipped"
)

// PeopleIdentityAssignmentBatch is one coordinator incremental clustering batch
// under primary mode. Unlike people_identity_decisions telemetry, this log is
// complete (no sampling/truncation) and is the source of truth for undo.
type PeopleIdentityAssignmentBatch struct {
	ID                 uint       `gorm:"primarykey" json:"id"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	OperationID        string     `gorm:"type:varchar(64);not null;uniqueIndex" json:"operation_id"`
	Source             string     `gorm:"type:varchar(20);not null" json:"source"`
	Mode               string     `gorm:"type:varchar(20);not null" json:"mode"`
	EngineVersion      string     `gorm:"type:varchar(50);not null" json:"engine_version"`
	StrategyVersion    string     `gorm:"type:varchar(50);not null" json:"strategy_version"`
	ConfigFingerprint  string     `gorm:"type:varchar(64);not null" json:"config_fingerprint"`
	IndexGeneration    int        `gorm:"not null;default:0" json:"index_generation"`
	Status             string     `gorm:"type:varchar(24);not null;index" json:"status"`
	AssignedComponents int        `gorm:"not null;default:0" json:"assigned_components"`
	AssignedFaces      int        `gorm:"not null;default:0" json:"assigned_faces"`
	PendingComponents  int        `gorm:"not null;default:0" json:"pending_components"`
	ErrorCategory      string     `gorm:"type:varchar(100);not null;default:''" json:"error_category,omitempty"`
	StartedAt          time.Time  `gorm:"not null" json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

func (PeopleIdentityAssignmentBatch) TableName() string {
	return "people_identity_assignment_batches"
}

// PeopleIdentityAssignmentChange records one face's before/after state for a
// primary assignment write. ComponentKey groups faces that must revoke atomically.
type PeopleIdentityAssignmentChange struct {
	ID                         uint       `gorm:"primarykey" json:"id"`
	CreatedAt                  time.Time  `json:"created_at"`
	BatchID                    uint       `gorm:"not null;uniqueIndex:idx_piac_batch_face,priority:1;index:idx_piac_batch_component,priority:1" json:"batch_id"`
	ComponentKey               string     `gorm:"type:varchar(64);not null;index:idx_piac_batch_component,priority:2" json:"component_key"`
	FaceID                     uint       `gorm:"not null;uniqueIndex:idx_piac_batch_face,priority:2;index:idx_piac_face_version,priority:1" json:"face_id"`
	DecisionSource             string     `gorm:"type:varchar(24);not null" json:"decision_source"`
	OldPersonID                *uint      `json:"old_person_id,omitempty"`
	NewPersonID                *uint      `json:"new_person_id,omitempty"`
	OldClusterStatus           string     `gorm:"type:varchar(20);not null" json:"old_cluster_status"`
	NewClusterStatus           string     `gorm:"type:varchar(20);not null" json:"new_cluster_status"`
	OldClusterScore            float64    `gorm:"not null;default:0" json:"old_cluster_score"`
	NewClusterScore            float64    `gorm:"not null;default:0" json:"new_cluster_score"`
	OldClusteredAt             *time.Time `json:"old_clustered_at,omitempty"`
	NewClusteredAt             *time.Time `json:"new_clustered_at,omitempty"`
	OldRetryCount              int        `gorm:"not null;default:0" json:"old_retry_count"`
	NewRetryCount              int        `gorm:"not null;default:0" json:"new_retry_count"`
	OldIdentityRetryAfter      *time.Time `json:"old_identity_retry_after,omitempty"`
	NewIdentityRetryAfter      *time.Time `json:"new_identity_retry_after,omitempty"`
	OldIdentityFailureReason   string     `gorm:"type:varchar(100);not null;default:''" json:"old_identity_failure_reason,omitempty"`
	NewIdentityFailureReason   string     `gorm:"type:varchar(100);not null;default:''" json:"new_identity_failure_reason,omitempty"`
	PreviousAssignmentVersion  uint64     `gorm:"not null;default:0" json:"previous_assignment_version"`
	CommittedAssignmentVersion uint64     `gorm:"not null;default:0;index:idx_piac_face_version,priority:2" json:"committed_assignment_version"`
	Score                      float64    `gorm:"not null;default:0" json:"score"`
	Margin                     *float64   `json:"margin,omitempty"`
	Reason                     string     `gorm:"type:varchar(100);not null;default:''" json:"reason,omitempty"`
	CenterIDsJSON              string     `gorm:"type:text;not null;default:'[]'" json:"center_ids_json"`
	NewPersonCreated           bool       `gorm:"not null;default:false" json:"new_person_created"`
}

func (PeopleIdentityAssignmentChange) TableName() string {
	return "people_identity_assignment_changes"
}

// PeopleIdentityAssignmentRevocation records one revoke operation against a batch.
type PeopleIdentityAssignmentRevocation struct {
	ID                 uint       `gorm:"primarykey" json:"id"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	BatchID            uint       `gorm:"not null;index" json:"batch_id"`
	OperationID        string     `gorm:"type:varchar(64);not null;uniqueIndex" json:"operation_id"`
	Status             string     `gorm:"type:varchar(24);not null" json:"status"`
	RevertedComponents int        `gorm:"not null;default:0" json:"reverted_components"`
	SkippedComponents  int        `gorm:"not null;default:0" json:"skipped_components"`
	StartedAt          time.Time  `gorm:"not null" json:"started_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
}

func (PeopleIdentityAssignmentRevocation) TableName() string {
	return "people_identity_assignment_revocations"
}

// PeopleIdentityAssignmentRevocationItem is one component outcome within a revocation.
type PeopleIdentityAssignmentRevocationItem struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	RevocationID  uint      `gorm:"not null;uniqueIndex:idx_piari_rev_component,priority:1" json:"revocation_id"`
	ComponentKey  string    `gorm:"type:varchar(64);not null;uniqueIndex:idx_piari_rev_component,priority:2" json:"component_key"`
	Status        string    `gorm:"type:varchar(20);not null" json:"status"`
	Reason        string    `gorm:"type:varchar(100);not null;default:''" json:"reason,omitempty"`
	RevertedFaces int       `gorm:"not null;default:0" json:"reverted_faces"`
}

func (PeopleIdentityAssignmentRevocationItem) TableName() string {
	return "people_identity_assignment_revocation_items"
}

// PeopleIdentityAssignmentBatchDetail is the API detail payload.
type PeopleIdentityAssignmentBatchDetail struct {
	Batch   PeopleIdentityAssignmentBatch     `json:"batch"`
	Changes []PeopleIdentityAssignmentChange  `json:"changes"`
	Total   int64                             `json:"total"`
}

// PeopleIdentityRevokePreview summarizes what a revoke would do without writing.
type PeopleIdentityRevokePreview struct {
	BatchID              uint                              `json:"batch_id"`
	RevertableComponents int                               `json:"revertable_components"`
	SkippedComponents    int                               `json:"skipped_components"`
	AlreadyRevoked       bool                              `json:"already_revoked"`
	Items                []PeopleIdentityRevokePreviewItem `json:"items"`
}

// PeopleIdentityRevokePreviewItem is one component preview row.
type PeopleIdentityRevokePreviewItem struct {
	ComponentKey string `json:"component_key"`
	Status       string `json:"status"` // revertable / skipped
	Reason       string `json:"reason,omitempty"`
	FaceCount    int    `json:"face_count"`
}

// PeopleIdentityRevokeResult is the execute-revoke response.
type PeopleIdentityRevokeResult struct {
	BatchID            uint                             `json:"batch_id"`
	RevocationID       uint                             `json:"revocation_id"`
	Status             string                           `json:"status"`
	RevertedComponents int                              `json:"reverted_components"`
	SkippedComponents  int                              `json:"skipped_components"`
	Items              []PeopleIdentityAssignmentRevocationItem `json:"items"`
}
