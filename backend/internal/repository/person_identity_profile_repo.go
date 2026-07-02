package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PersonIdentityProfileRepository 持久化人物身份画像的派生数据：profile 元数据、
// 各 generation 的 centers 与 members。所有写操作以 SQLite 事务保证原子性；
// ReplaceGeneration 在事务内写入新 generation 的 center/member 后才原子切换 active_generation，
// 任何失败都回滚，旧 generation 保持激活。
type PersonIdentityProfileRepository interface {
	// MarkDirty upsert profiles for the given persons without altering an active
	// generation. Persons without a profile get a fresh dirty profile (generation 0).
	MarkDirty(personIDs []uint, reason string) error
	// ListDirty returns dirty profiles with person_id > cursor, deterministic by person ID.
	ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error)
	// GetActive loads the profile plus centers/members of its active generation.
	// Returns a nil build (no error) when no profile exists.
	GetActive(personID uint) (*model.PersonIdentityProfileBuild, error)
	// ListAllActiveCenters returns centers belonging to each profile's active generation.
	ListAllActiveCenters() ([]*model.PersonIdentityCenter, error)
	// ReplaceGeneration writes a new generation (centers + members) and atomically
	// activates it. The build's centers carry Ordinal; accepted members reference a
	// center by Ordinal via CenterID, which is remapped to the persisted center ID.
	ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error
	// MarkFailed sets status=failed with a message, preserving the active generation.
	MarkFailed(personID uint, message string) error
	// DeleteByPersonIDs removes all profile/center/member rows for the given persons.
	DeleteByPersonIDs(personIDs []uint) error
	// DeleteInactiveGenerations prunes center/member rows beyond the `keep` most recent
	// generations for a person, retaining the active and previous generation(s).
	DeleteInactiveGenerations(personID uint, keep int) error
	// InvalidateDeletedPeople removes derived rows whose person no longer exists.
	InvalidateDeletedPeople() error
}

type personIdentityProfileRepository struct {
	db                 *gorm.DB
	beforeActivateHook func() error
}

// NewPersonIdentityProfileRepository constructs the repository.
func NewPersonIdentityProfileRepository(db *gorm.DB) PersonIdentityProfileRepository {
	return &personIdentityProfileRepository{db: db}
}

// setBeforeActivateHookForTest installs a callback invoked immediately before the
// active-generation switch inside ReplaceGeneration. A non-nil error aborts the
// transaction, leaving the prior generation active. Test-only.
func (r *personIdentityProfileRepository) setBeforeActivateHookForTest(fn func() error) {
	r.beforeActivateHook = fn
}

func (r *personIdentityProfileRepository) MarkDirty(personIDs []uint, reason string) error {
	if len(personIDs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, chunk := range chunkIDs(personIDs) {
		rows := make([]*model.PersonIdentityProfile, 0, len(chunk))
		for _, pid := range chunk {
			rows = append(rows, &model.PersonIdentityProfile{
				PersonID:    pid,
				Status:      model.PersonIdentityProfileStatusDirty,
				DirtyReason: reason,
				UpdatedAt:   now,
			})
		}
		// Upsert on person_id: update status/reason/updated_at; never touch active generation.
		if err := r.db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "person_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"status":       model.PersonIdentityProfileStatusDirty,
				"dirty_reason": reason,
				"updated_at":   now,
			}),
		}).Create(rows).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *personIdentityProfileRepository) ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error) {
	if limit <= 0 {
		limit = 100
	}
	var profiles []*model.PersonIdentityProfile
	q := r.db.Where("status = ?", model.PersonIdentityProfileStatusDirty)
	if cursor > 0 {
		q = q.Where("person_id > ?", cursor)
	}
	err := q.Order("person_id ASC").Limit(limit).Find(&profiles).Error
	return profiles, err
}

func (r *personIdentityProfileRepository) GetActive(personID uint) (*model.PersonIdentityProfileBuild, error) {
	var profile model.PersonIdentityProfile
	err := r.db.Where("person_id = ?", personID).First(&profile).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	if profile.ActiveGeneration == 0 {
		// Profile exists but no generation has been built yet.
		return &model.PersonIdentityProfileBuild{Profile: &profile}, nil
	}

	var centers []*model.PersonIdentityCenter
	if err := r.db.Where("person_id = ? AND generation = ?", personID, profile.ActiveGeneration).
		Order("ordinal ASC").Find(&centers).Error; err != nil {
		return nil, err
	}
	var members []*model.PersonIdentityCenterMember
	if err := r.db.Where("person_id = ? AND generation = ?", personID, profile.ActiveGeneration).
		Order("id ASC").Find(&members).Error; err != nil {
		return nil, err
	}
	return &model.PersonIdentityProfileBuild{
		Profile: &profile,
		Centers: centers,
		Members: members,
	}, nil
}

func (r *personIdentityProfileRepository) ListAllActiveCenters() ([]*model.PersonIdentityCenter, error) {
	var centers []*model.PersonIdentityCenter
	// Join centers against profiles to keep only each person's active generation.
	err := r.db.Where(
		"person_id IN (SELECT person_id FROM person_identity_profiles WHERE active_generation > 0) AND " +
			"generation = (SELECT active_generation FROM person_identity_profiles p WHERE p.person_id = person_identity_centers.person_id)",
	).Order("person_id ASC, ordinal ASC").Find(&centers).Error
	return centers, err
}

func (r *personIdentityProfileRepository) ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error {
	if build == nil || build.Profile == nil {
		return gorm.ErrInvalidData
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Validate person existence inside the transaction.
		var count int64
		if err := tx.Model(&model.Person{}).Where("id = ?", personID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return gorm.ErrRecordNotFound
		}

		// Load the existing profile (if any) to compute the next generation.
		var existing model.PersonIdentityProfile
		err := tx.Where("person_id = ?", personID).First(&existing).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		newGen := existing.NextGeneration
		if newGen == 0 {
			newGen = existing.ActiveGeneration + 1
			if newGen == 0 {
				newGen = 1
			}
		}

		// Insert centers, capturing real IDs and mapping ordinal -> ID.
		ordinalToID := make(map[uint]uint, len(build.Centers))
		for _, c := range build.Centers {
			c.ID = 0
			c.PersonID = personID
			c.Generation = newGen
			if err := tx.Create(c).Error; err != nil {
				return err
			}
			if c.Ordinal != 0 {
				ordinalToID[uint(c.Ordinal)] = c.ID
			}
		}

		// Insert members, remapping CenterID from ordinal to the persisted center ID.
		for _, m := range build.Members {
			m.ID = 0
			m.PersonID = personID
			m.Generation = newGen
			if m.CenterID != nil {
				ordinal := *m.CenterID
				if realID, ok := ordinalToID[ordinal]; ok {
					m.CenterID = &realID
				} else {
					m.CenterID = nil
				}
			}
			if err := tx.Create(m).Error; err != nil {
				return err
			}
		}

		// Activation boundary: a test hook can force a failure here so the whole
		// transaction rolls back and the prior generation stays active.
		if r.beforeActivateHook != nil {
			if err := r.beforeActivateHook(); err != nil {
				return err
			}
		}

		now := time.Now().UTC()
		faceCount := len(build.Members)
		// Upsert the profile with the new active generation.
		profile := &model.PersonIdentityProfile{
			PersonID:          personID,
			ActiveGeneration:  newGen,
			NextGeneration:    newGen + 1,
			Status:            model.PersonIdentityProfileStatusReady,
			DirtyReason:       "",
			AlgorithmVersion:  build.Profile.AlgorithmVersion,
			EmbeddingModel:    build.Profile.EmbeddingModel,
			FaceCountSnapshot: faceCount,
			LastError:         "",
			BuiltAt:           &now,
			UpdatedAt:         now,
		}
		return tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "person_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"active_generation", "next_generation", "status", "dirty_reason",
				"algorithm_version", "embedding_model", "face_count_snapshot",
				"last_error", "built_at", "updated_at",
			}),
		}).Create(profile).Error
	})
}

func (r *personIdentityProfileRepository) MarkFailed(personID uint, message string) error {
	now := time.Now().UTC()
	return r.db.Model(&model.PersonIdentityProfile{}).
		Where("person_id = ?", personID).
		Updates(map[string]interface{}{
			"status":     model.PersonIdentityProfileStatusFailed,
			"last_error": message,
			"updated_at": now,
		}).Error
}

func (r *personIdentityProfileRepository) DeleteByPersonIDs(personIDs []uint) error {
	if len(personIDs) == 0 {
		return nil
	}
	for _, chunk := range chunkIDs(personIDs) {
		if err := r.db.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
			return err
		}
		if err := r.db.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityCenter{}).Error; err != nil {
			return err
		}
		if err := r.db.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityProfile{}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (r *personIdentityProfileRepository) DeleteInactiveGenerations(personID uint, keep int) error {
	if keep < 1 {
		keep = 1
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var gens []int
		if err := tx.Model(&model.PersonIdentityCenter{}).
			Where("person_id = ?", personID).
			Distinct("generation").
			Order("generation DESC").
			Pluck("generation", &gens).Error; err != nil {
			return err
		}
		if len(gens) <= keep {
			return nil
		}
		// Retain the `keep` most recent generations; prune the rest.
		prune := gens[keep:]
		if err := tx.Where("person_id = ? AND generation IN ?", personID, prune).
			Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
			return err
		}
		return tx.Where("person_id = ? AND generation IN ?", personID, prune).
			Delete(&model.PersonIdentityCenter{}).Error
	})
}

func (r *personIdentityProfileRepository) InvalidateDeletedPeople() error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		orphan := "person_id NOT IN (SELECT id FROM people)"
		if err := tx.Where(orphan).Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
			return err
		}
		if err := tx.Where(orphan).Delete(&model.PersonIdentityCenter{}).Error; err != nil {
			return err
		}
		return tx.Where(orphan).Delete(&model.PersonIdentityProfile{}).Error
	})
}
