package service

import (
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PeopleIdentityAssignmentService 管理 primary 归属批次查询与撤销。
type PeopleIdentityAssignmentService interface {
	ListBatches(page, pageSize int) ([]model.PeopleIdentityAssignmentBatch, int64, error)
	GetBatch(id uint, page, pageSize int) (*model.PeopleIdentityAssignmentBatchDetail, error)
	PreviewRevoke(batchID uint) (*model.PeopleIdentityRevokePreview, error)
	Revoke(batchID uint) (*model.PeopleIdentityRevokeResult, error)
	MarkInterruptedRunning() (int64, error)
}

type peopleIdentityAssignmentService struct {
	repo        repository.PeopleIdentityAssignmentRepository
	faceRepo    repository.FaceRepository
	personRepo  repository.PersonRepository
	writeGateFn func() func()
	invalidate  identityProfileInvalidateFn
}

// NewPeopleIdentityAssignmentService 构造归属批次服务。
func NewPeopleIdentityAssignmentService(
	repo repository.PeopleIdentityAssignmentRepository,
	faceRepo repository.FaceRepository,
	personRepo repository.PersonRepository,
) PeopleIdentityAssignmentService {
	return &peopleIdentityAssignmentService{
		repo:       repo,
		faceRepo:   faceRepo,
		personRepo: personRepo,
	}
}

// SetWriteGateFn 注入写门禁（撤销前暂停聚类写入）。
func (s *peopleIdentityAssignmentService) SetWriteGateFn(fn func() func()) {
	s.writeGateFn = fn
}

// SetInvalidationHook 注入画像失效 hook。
func (s *peopleIdentityAssignmentService) SetInvalidationHook(fn identityProfileInvalidateFn) {
	s.invalidate = fn
}

func (s *peopleIdentityAssignmentService) ListBatches(page, pageSize int) ([]model.PeopleIdentityAssignmentBatch, int64, error) {
	return s.repo.ListBatches(page, pageSize)
}

func (s *peopleIdentityAssignmentService) GetBatch(id uint, page, pageSize int) (*model.PeopleIdentityAssignmentBatchDetail, error) {
	batch, err := s.repo.GetBatch(id)
	if err != nil {
		return nil, err
	}
	changes, total, err := s.repo.ListChanges(id, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &model.PeopleIdentityAssignmentBatchDetail{
		Batch:   *batch,
		Changes: changes,
		Total:   total,
	}, nil
}

func (s *peopleIdentityAssignmentService) MarkInterruptedRunning() (int64, error) {
	return s.repo.MarkInterruptedRunning()
}

func (s *peopleIdentityAssignmentService) PreviewRevoke(batchID uint) (*model.PeopleIdentityRevokePreview, error) {
	batch, err := s.repo.GetBatch(batchID)
	if err != nil {
		return nil, err
	}
	if batch.Status == model.PeopleIdentityAssignmentBatchRevoked {
		return &model.PeopleIdentityRevokePreview{
			BatchID:        batchID,
			AlreadyRevoked: true,
		}, nil
	}
	changes, err := s.repo.ListChangesByBatch(batchID)
	if err != nil {
		return nil, err
	}
	preview := &model.PeopleIdentityRevokePreview{BatchID: batchID}
	for _, item := range s.evaluateComponents(changes) {
		preview.Items = append(preview.Items, model.PeopleIdentityRevokePreviewItem{
			ComponentKey: item.componentKey,
			Status:       item.status,
			Reason:       item.reason,
			FaceCount:    item.faceCount,
		})
		if item.status == "revertable" {
			preview.RevertableComponents++
		} else {
			preview.SkippedComponents++
		}
	}
	return preview, nil
}

func (s *peopleIdentityAssignmentService) Revoke(batchID uint) (*model.PeopleIdentityRevokeResult, error) {
	batch, err := s.repo.GetBatch(batchID)
	if err != nil {
		return nil, err
	}

	// 幂等：已有完成的撤销直接返回。
	if existing, err := s.repo.GetLatestRevocation(batchID); err == nil && existing != nil &&
		existing.Status == model.PeopleIdentityRevocationStatusCompleted {
		items, _ := s.listRevocationItems(existing.ID)
		return &model.PeopleIdentityRevokeResult{
			BatchID:            batchID,
			RevocationID:       existing.ID,
			Status:             existing.Status,
			RevertedComponents: existing.RevertedComponents,
			SkippedComponents:  existing.SkippedComponents,
			Items:              items,
		}, nil
	}

	if s.writeGateFn != nil {
		release := s.writeGateFn()
		defer release()
	}

	changes, err := s.repo.ListChangesByBatch(batchID)
	if err != nil {
		return nil, err
	}
	evals := s.evaluateComponents(changes)

	now := time.Now()
	rev := &model.PeopleIdentityAssignmentRevocation{
		BatchID:     batchID,
		OperationID: uuid.NewString(),
		Status:      model.PeopleIdentityRevocationStatusRunning,
		StartedAt:   now,
	}
	if err := s.repo.CreateRevocation(rev); err != nil {
		return nil, err
	}

	var items []model.PeopleIdentityAssignmentRevocationItem
	affectedPersons := make(map[uint]struct{})
	reverted, skipped := 0, 0

	for _, ev := range evals {
		item := model.PeopleIdentityAssignmentRevocationItem{
			RevocationID: rev.ID,
			ComponentKey: ev.componentKey,
			Status:       model.PeopleIdentityRevocationItemSkipped,
			Reason:       ev.reason,
		}
		if ev.status == "revertable" {
			if err := s.revertComponent(ev.changes); err != nil {
				item.Reason = err.Error()
				skipped++
			} else {
				item.Status = model.PeopleIdentityRevocationItemReverted
				item.Reason = ""
				item.RevertedFaces = len(ev.changes)
				reverted++
				for _, ch := range ev.changes {
					if ch.OldPersonID != nil {
						affectedPersons[*ch.OldPersonID] = struct{}{}
					}
					if ch.NewPersonID != nil {
						affectedPersons[*ch.NewPersonID] = struct{}{}
					}
				}
			}
		} else {
			skipped++
		}
		items = append(items, item)
	}

	if err := s.repo.CreateRevocationItems(nil, items); err != nil {
		logger.Warnf("identity assignment revoke: persist items failed: %v", err)
	}

	completed := now
	rev.Status = model.PeopleIdentityRevocationStatusCompleted
	rev.RevertedComponents = reverted
	rev.SkippedComponents = skipped
	rev.CompletedAt = &completed
	_ = s.repo.UpdateRevocation(rev)

	batch.Status = model.PeopleIdentityAssignmentBatchRevoked
	batch.CompletedAt = &completed
	_ = s.repo.UpdateBatch(batch)

	if s.invalidate != nil && len(affectedPersons) > 0 {
		ids := make([]uint, 0, len(affectedPersons))
		for id := range affectedPersons {
			ids = append(ids, id)
		}
		_ = s.invalidate(IdentityProfileInvalidation{
			DirtyPersonIDs: ids,
			Reason:         string(repository.InvalidationReasonClusteringAssign),
		})
	}

	return &model.PeopleIdentityRevokeResult{
		BatchID:            batchID,
		RevocationID:       rev.ID,
		Status:             rev.Status,
		RevertedComponents: reverted,
		SkippedComponents:  skipped,
		Items:              items,
	}, nil
}

type componentEval struct {
	componentKey string
	status       string
	reason       string
	faceCount    int
	changes      []model.PeopleIdentityAssignmentChange
}

func (s *peopleIdentityAssignmentService) evaluateComponents(changes []model.PeopleIdentityAssignmentChange) []componentEval {
	byComp := make(map[string][]model.PeopleIdentityAssignmentChange)
	order := make([]string, 0)
	for _, ch := range changes {
		if _, ok := byComp[ch.ComponentKey]; !ok {
			order = append(order, ch.ComponentKey)
		}
		byComp[ch.ComponentKey] = append(byComp[ch.ComponentKey], ch)
	}
	out := make([]componentEval, 0, len(order))
	for _, key := range order {
		chs := byComp[key]
		ev := componentEval{componentKey: key, faceCount: len(chs), changes: chs, status: "revertable"}
		for _, ch := range chs {
			face, err := s.faceRepo.GetByID(ch.FaceID)
			if err != nil || face == nil {
				ev.status = "skipped"
				ev.reason = "face_missing"
				break
			}
			if face.AssignmentVersion != ch.CommittedAssignmentVersion {
				ev.status = "skipped"
				ev.reason = "assignment_version_conflict"
				break
			}
			if ch.OldPersonID != nil && *ch.OldPersonID != 0 {
				if _, err := s.personRepo.GetByID(*ch.OldPersonID); err != nil {
					ev.status = "skipped"
					ev.reason = "old_person_missing"
					break
				}
			}
		}
		out = append(out, ev)
	}
	return out
}

func (s *peopleIdentityAssignmentService) revertComponent(changes []model.PeopleIdentityAssignmentChange) error {
	now := time.Now()
	holdUntil := now.Add(5 * time.Minute)
	return s.repo.WithTx(func(tx *gorm.DB) error {
		for _, ch := range changes {
			face, err := s.faceRepo.GetByID(ch.FaceID)
			if err != nil {
				return err
			}
			if face.AssignmentVersion != ch.CommittedAssignmentVersion {
				return fmt.Errorf("assignment_version_conflict")
			}
			fields := map[string]interface{}{
				"person_id":               ch.OldPersonID,
				"cluster_status":          ch.OldClusterStatus,
				"cluster_score":           ch.OldClusterScore,
				"clustered_at":            ch.OldClusteredAt,
				"retry_count":             ch.OldRetryCount,
				"identity_retry_after":    holdUntil,
				"identity_failure_reason": "assignment_revoked",
			}
			if err := tx.Model(&model.Face{}).Where("id = ? AND assignment_version = ?", ch.FaceID, ch.CommittedAssignmentVersion).
				Updates(fields).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *peopleIdentityAssignmentService) listRevocationItems(revocationID uint) ([]model.PeopleIdentityAssignmentRevocationItem, error) {
	// 仓库未暴露专用 List；通过 WithTx 临时查询。
	var items []model.PeopleIdentityAssignmentRevocationItem
	err := s.repo.WithTx(func(tx *gorm.DB) error {
		return tx.Where("revocation_id = ?", revocationID).Order("id ASC").Find(&items).Error
	})
	return items, err
}
