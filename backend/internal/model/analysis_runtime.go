package model

import (
	"time"

	"gorm.io/gorm"
)

const GlobalAnalysisResourceKey = "global_analysis"

const (
	AnalysisOwnerTypeBatch      = "batch"
	AnalysisOwnerTypeBackground = "background"
	AnalysisOwnerTypeAnalyzer   = "analyzer"

	AnalysisRuntimeStatusIdle    = "idle"
	AnalysisRuntimeStatusRunning = "running"
)

type AnalysisRuntimeLease struct {
	ID              uint           `gorm:"primarykey" json:"id"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
	ResourceKey     string         `gorm:"type:varchar(64);not null;uniqueIndex:idx_runtime_resource" json:"resource_key"`
	OwnerType       string         `gorm:"type:varchar(32);not null;default:idle;check:chk_runtime_owner_type,owner_type IN ('idle','batch','background','analyzer')" json:"owner_type"`
	OwnerID         string         `gorm:"type:varchar(128)" json:"owner_id"`
	Status          string         `gorm:"type:varchar(16);not null;default:idle;check:chk_runtime_status,status IN ('idle','running')" json:"status"`
	Message         string         `gorm:"type:varchar(255)" json:"message"`
	StartedAt       *time.Time     `json:"started_at,omitempty"`
	LastHeartbeatAt *time.Time     `json:"last_heartbeat_at,omitempty"`
	LeaseExpiresAt  *time.Time     `gorm:"index:idx_runtime_lease_expires_at" json:"lease_expires_at,omitempty"`
}

func (AnalysisRuntimeLease) TableName() string {
	return "analysis_runtime_leases"
}

func (l *AnalysisRuntimeLease) IsActive(now time.Time) bool {
	return l != nil && l.Status == AnalysisRuntimeStatusRunning && l.LeaseExpiresAt != nil && l.LeaseExpiresAt.After(now)
}

type AnalysisRuntimeStatusResponse struct {
	ResourceKey     string     `json:"resource_key"`
	Status          string     `json:"status"`
	OwnerType       string     `json:"owner_type,omitempty"`
	OwnerID         string     `json:"owner_id,omitempty"`
	Message         string     `json:"message,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty"`
	IsActive        bool       `json:"is_active"`
}

type AnalysisRuntimeAcquireRequest struct {
	OwnerType string `json:"owner_type" binding:"required"`
	OwnerID   string `json:"owner_id" binding:"required"`
	Message   string `json:"message"`
}

type AnalysisRuntimeHeartbeatRequest struct {
	OwnerType string `json:"owner_type" binding:"required"`
	OwnerID   string `json:"owner_id" binding:"required"`
}

type AnalysisRuntimeReleaseRequest struct {
	OwnerType string `json:"owner_type" binding:"required"`
	OwnerID   string `json:"owner_id" binding:"required"`
}
