package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAnalysisRuntimeBusy         = errors.New("analysis runtime busy")
	ErrAnalysisRuntimeOwnedByOther = errors.New("analysis runtime owned by other")
)

type AnalysisRuntimeService interface {
	AcquireGlobal(ownerType, ownerID, message string) (*model.AnalysisRuntimeLease, error)
	HeartbeatGlobal(ownerType, ownerID string) (*model.AnalysisRuntimeLease, error)
	ReleaseGlobal(ownerType, ownerID string) error
	GetGlobalStatus() (*model.AnalysisRuntimeStatusResponse, error)
}

type analysisRuntimeService struct {
	db       *gorm.DB
	leaseTTL time.Duration
}

func NewAnalysisRuntimeService(db *gorm.DB) AnalysisRuntimeService {
	return &analysisRuntimeService{
		db:       db,
		leaseTTL: 30 * time.Second,
	}
}

func (s *analysisRuntimeService) AcquireGlobal(ownerType, ownerID, message string) (*model.AnalysisRuntimeLease, error) {
	if ownerType == "" || ownerID == "" {
		return nil, fmt.Errorf("owner type and owner id are required")
	}

	now := time.Now().UTC()
	expiresAt := now.Add(s.leaseTTL)

	err := s.db.Transaction(func(tx *gorm.DB) error {
		placeholder := &model.AnalysisRuntimeLease{
			ResourceKey: model.GlobalAnalysisResourceKey,
			OwnerType:   "",
			Status:      model.AnalysisRuntimeStatusIdle,
		}

		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(placeholder).Error; err != nil {
			return err
		}

		result := tx.Model(&model.AnalysisRuntimeLease{}).
			Where("resource_key = ? AND (lease_expires_at IS NULL OR lease_expires_at < ? OR (owner_type = ? AND owner_id = ?))",
				model.GlobalAnalysisResourceKey, now, ownerType, ownerID).
			Updates(map[string]interface{}{
				"owner_type":        ownerType,
				"owner_id":          ownerID,
				"status":            model.AnalysisRuntimeStatusRunning,
				"message":           message,
				"started_at":        now,
				"last_heartbeat_at": now,
				"lease_expires_at":  expiresAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}
		return ErrAnalysisRuntimeBusy
	})
	if err != nil {
		if errors.Is(err, ErrAnalysisRuntimeBusy) {
			status, statusErr := s.GetGlobalStatus()
			if statusErr != nil {
				return nil, err
			}
			return leaseFromStatus(status), err
		}
		return nil, err
	}

	status, err := s.GetGlobalStatus()
	if err != nil {
		return nil, err
	}
	return leaseFromStatus(status), nil
}

func (s *analysisRuntimeService) HeartbeatGlobal(ownerType, ownerID string) (*model.AnalysisRuntimeLease, error) {
	if ownerType == "" || ownerID == "" {
		return nil, fmt.Errorf("owner type and owner id are required")
	}

	now := time.Now().UTC()
	expiresAt := now.Add(s.leaseTTL)
	result := s.db.Model(&model.AnalysisRuntimeLease{}).
		Where("resource_key = ? AND owner_type = ? AND owner_id = ? AND status = ? AND lease_expires_at >= ?",
			model.GlobalAnalysisResourceKey, ownerType, ownerID, model.AnalysisRuntimeStatusRunning, now).
		Updates(map[string]interface{}{
			"last_heartbeat_at": now,
			"lease_expires_at":  expiresAt,
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrAnalysisRuntimeOwnedByOther
	}

	status, err := s.GetGlobalStatus()
	if err != nil {
		return nil, err
	}
	return leaseFromStatus(status), nil
}

func (s *analysisRuntimeService) ReleaseGlobal(ownerType, ownerID string) error {
	if ownerType == "" || ownerID == "" {
		return fmt.Errorf("owner type and owner id are required")
	}

	result := s.db.Model(&model.AnalysisRuntimeLease{}).
		Where("resource_key = ? AND owner_type = ? AND owner_id = ?",
			model.GlobalAnalysisResourceKey, ownerType, ownerID).
		Updates(map[string]interface{}{
			"owner_type":        "",
			"owner_id":          "",
			"status":            model.AnalysisRuntimeStatusIdle,
			"message":           "",
			"started_at":        nil,
			"last_heartbeat_at": nil,
			"lease_expires_at":  nil,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrAnalysisRuntimeOwnedByOther
	}
	return nil
}

func (s *analysisRuntimeService) GetGlobalStatus() (*model.AnalysisRuntimeStatusResponse, error) {
	var lease model.AnalysisRuntimeLease
	err := s.db.Where("resource_key = ?", model.GlobalAnalysisResourceKey).First(&lease).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &model.AnalysisRuntimeStatusResponse{
				ResourceKey: model.GlobalAnalysisResourceKey,
				Status:      model.AnalysisRuntimeStatusIdle,
				IsActive:    false,
			}, nil
		}
		return nil, err
	}

	now := time.Now().UTC()
	if !lease.IsActive(now) {
		return &model.AnalysisRuntimeStatusResponse{
			ResourceKey: model.GlobalAnalysisResourceKey,
			Status:      model.AnalysisRuntimeStatusIdle,
			IsActive:    false,
		}, nil
	}

	return &model.AnalysisRuntimeStatusResponse{
		ResourceKey:     lease.ResourceKey,
		Status:          lease.Status,
		OwnerType:       lease.OwnerType,
		OwnerID:         lease.OwnerID,
		Message:         lease.Message,
		StartedAt:       lease.StartedAt,
		LastHeartbeatAt: lease.LastHeartbeatAt,
		LeaseExpiresAt:  lease.LeaseExpiresAt,
		IsActive:        true,
	}, nil
}

func leaseFromStatus(status *model.AnalysisRuntimeStatusResponse) *model.AnalysisRuntimeLease {
	if status == nil {
		return nil
	}
	return &model.AnalysisRuntimeLease{
		ResourceKey:     status.ResourceKey,
		OwnerType:       status.OwnerType,
		OwnerID:         status.OwnerID,
		Status:          status.Status,
		Message:         status.Message,
		StartedAt:       status.StartedAt,
		LastHeartbeatAt: status.LastHeartbeatAt,
		LeaseExpiresAt:  status.LeaseExpiresAt,
	}
}
