package repository

import (
	"errors"
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PersonIdentityProfileRepository 持久化人物身份画像的派生数据：profile 元数据、
// 各 generation 的 centers 与 members。所有写操作以 SQLite 事务保证原子性；
// ReplaceGeneration 在事务内写入新 generation 的 center/member 后才原子切换 active_generation，
// 任何失败都回滚，旧 generation 保持激活。画像是派生数据，legacy 指派（faces.person_id）
// 始终是真相来源，画像可安全丢弃重建。
type PersonIdentityProfileRepository interface {
	// MarkDirty 将指定人物标记为待重建。去重 ID 并忽略 0；已有 profile 仅更新状态与原因，
	// 不改动 active_generation，也不删除既有中心/成员；不存在则创建 active_generation=0 的 profile。
	MarkDirty(personIDs []uint, reason string) error
	// ListDirty 返回 status=dirty 且 person_id > cursor 的 profile，按 person_id ASC，
	// 严格应用 limit，游标分页避免全量加载。
	ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error)
	// GetActive 加载人物活动 generation 的完整构建（profile + centers + members）。
	// profile 不存在或 active_generation=0 时返回 (nil, nil)。
	GetActive(personID uint) (*model.PersonIdentityProfileBuild, error)
	// ListAllActiveCenters 通过 JOIN 一次查询返回所有人物活动 generation 的中心，
	// 按 person_id ASC, ordinal ASC，不返回历史中心。
	ListAllActiveCenters() ([]*model.PersonIdentityCenter, error)
	// ReplaceGeneration 在单事务内写入新 generation（centers + members）并原子激活。
	// build 中 accepted member 的 CenterID 为所属 center 的 Ordinal（逻辑引用），
	// 持久化 center 取得真实主键后重映射为真实 ID。任何步骤失败整体回滚。
	ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error
	// MarkFailed 标记构建失败并记录原因，保留活动 generation、中心与成员。
	MarkFailed(personID uint, message string) error
	// DeleteByPersonIDs 按人物 ID 删除画像派生数据（members → centers → profiles），
	// 不触碰 faces / people。
	DeleteByPersonIDs(personIDs []uint) error
	// InvalidateDeletedPeople 清理 people 表中已不存在人物的画像派生数据，
	// 使用子查询定位，不做全量内存扫描。
	InvalidateDeletedPeople() error
	// DeleteInactiveGenerations 保留最近 keep 个非活动 generation，删除其余非活动 generation。
	// 永远不删除当前活动 generation；keep=0 删除全部非活动 generation。
	DeleteInactiveGenerations(personID uint, keep int) error
}

type personIdentityProfileRepository struct {
	db *gorm.DB
}

// NewPersonIdentityProfileRepository constructs the repository.
func NewPersonIdentityProfileRepository(db *gorm.DB) PersonIdentityProfileRepository {
	return &personIdentityProfileRepository{db: db}
}

// dedupIDs 去重并忽略 0，保持首次出现的顺序。
func dedupIDs(ids []uint) []uint {
	seen := make(map[uint]struct{})
	var out []uint
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (r *personIdentityProfileRepository) MarkDirty(personIDs []uint, reason string) error {
	ids := dedupIDs(personIDs)
	if len(ids) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, chunk := range chunkIDs(ids) {
		rows := make([]*model.PersonIdentityProfile, 0, len(chunk))
		for _, pid := range chunk {
			rows = append(rows, &model.PersonIdentityProfile{
				PersonID:    pid,
				Status:      model.PersonIdentityProfileStatusDirty,
				DirtyReason: reason,
				UpdatedAt:   now,
			})
		}
		// Upsert on person_id: 仅更新 status/reason/updated_at，绝不触碰 active/next generation。
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
	var profiles []*model.PersonIdentityProfile
	q := r.db.Where("status = ? AND person_id > ?", model.PersonIdentityProfileStatusDirty, cursor).
		Order("person_id ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&profiles).Error; err != nil {
		return nil, err
	}
	return profiles, nil
}

func (r *personIdentityProfileRepository) GetActive(personID uint) (*model.PersonIdentityProfileBuild, error) {
	var profile model.PersonIdentityProfile
	err := r.db.Where("person_id = ?", personID).First(&profile).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if profile.ActiveGeneration == 0 {
		// profile 存在但尚无激活的 generation。
		return nil, nil
	}
	gen := profile.ActiveGeneration

	var centers []*model.PersonIdentityCenter
	if err := r.db.Where("person_id = ? AND generation = ?", personID, gen).
		Order("ordinal ASC, id ASC").Find(&centers).Error; err != nil {
		return nil, err
	}
	var members []*model.PersonIdentityCenterMember
	if err := r.db.Where("person_id = ? AND generation = ?", personID, gen).
		Order("face_id ASC").Find(&members).Error; err != nil {
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
	// JOIN 一次查询：center generation 必须等于 profile active generation，且存在活动 generation。
	err := r.db.Table("person_identity_centers AS c").
		Select("c.*").
		Joins("INNER JOIN person_identity_profiles AS p ON p.person_id = c.person_id").
		Where("c.generation = p.active_generation AND p.active_generation > 0").
		Order("c.person_id ASC, c.ordinal ASC").
		Find(&centers).Error
	if err != nil {
		return nil, err
	}
	return centers, nil
}

// ReplaceGeneration 在单个 SQLite 事务内完成新 generation 的写入与激活。
// 失败时整体回滚：旧 active_generation 不变，旧中心/成员仍可读取，不残留半成品 generation。
func (r *personIdentityProfileRepository) ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error {
	if build == nil || build.Profile == nil {
		return errors.New("identity profile build is nil")
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		// 1. 验证目标 Person 存在。
		var person model.Person
		if err := tx.Where("id = ?", personID).First(&person).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("person %d not found", personID)
			}
			return err
		}

		// 2. 锁定/读取当前 profile 状态；不存在则创建空 dirty profile。
		var profile model.PersonIdentityProfile
		err := tx.Where("person_id = ?", personID).First(&profile).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			profile = model.PersonIdentityProfile{
				PersonID: personID,
				Status:   model.PersonIdentityProfileStatusDirty,
			}
			if err := tx.Create(&profile).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}

		// 3. 分配严格大于活动 generation 的新 generation。next_generation 记录高水位，
		//    仅在成功时推进，故失败重试不会跳号。
		newGen := profile.NextGeneration
		if newGen <= profile.ActiveGeneration {
			newGen = profile.ActiveGeneration + 1
			if newGen == 0 {
				newGen = 1
			}
		}

		// 4. 写入新 centers，建立 ordinal → 真实 ID 映射。
		ordinalToID := make(map[int]uint, len(build.Centers))
		for _, c := range build.Centers {
			c.ID = 0
			c.PersonID = personID
			c.Generation = newGen
			if err := tx.Create(c).Error; err != nil {
				return err
			}
			ordinalToID[c.Ordinal] = c.ID
		}

		// 5. 写入 members，将 CenterID 从 ordinal 重映射为真实主键。
		//    6. 校验：所有 center/member 归属同一人物与 generation（由强制赋值保证），
		//    且 accepted member 引用的 center ordinal 必须存在；重复 face_id 会触发
		//    (person_id, generation, face_id) 唯一约束冲突，使事务回滚。
		for _, m := range build.Members {
			m.ID = 0
			m.PersonID = personID
			m.Generation = newGen
			if m.CenterID != nil {
				realID, ok := ordinalToID[int(*m.CenterID)]
				if !ok {
					return fmt.Errorf("member face %d references unknown center ordinal %d", m.FaceID, *m.CenterID)
				}
				m.CenterID = &realID
			}
			if err := tx.Create(m).Error; err != nil {
				return err
			}
		}

		// 7-11. 最后才激活：推进 active_generation、置 ready、清空 dirty_reason/last_error、
		//       更新 face-count 快照与算法/embedding 版本、推进 next_generation、记录 built_at。
		//       在此之前任何失败（含唯一约束冲突）都会回滚，旧 generation 保持激活。
		now := time.Now().UTC()
		if err := tx.Model(&profile).Updates(map[string]interface{}{
			"active_generation":   newGen,
			"next_generation":     newGen + 1,
			"status":              model.PersonIdentityProfileStatusReady,
			"dirty_reason":        "",
			"last_error":          "",
			"face_count_snapshot": len(build.Members),
			"algorithm_version":   build.Profile.AlgorithmVersion,
			"embedding_model":     build.Profile.EmbeddingModel,
			"built_at":            &now,
			"updated_at":          now,
		}).Error; err != nil {
			return err
		}
		return nil
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
	ids := dedupIDs(personIDs)
	if len(ids) == 0 {
		return nil
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, chunk := range chunkIDs(ids) {
			// 安全顺序：members → centers → profiles；不触碰 faces / people。
			if err := tx.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
				return err
			}
			if err := tx.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityCenter{}).Error; err != nil {
				return err
			}
			if err := tx.Where("person_id IN ?", chunk).Delete(&model.PersonIdentityProfile{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *personIdentityProfileRepository) InvalidateDeletedPeople() error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 子查询定位 people 表中已不存在人物的派生数据，不做全量内存扫描。
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

func (r *personIdentityProfileRepository) DeleteInactiveGenerations(personID uint, keep int) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var profile model.PersonIdentityProfile
		err := tx.Where("person_id = ?", personID).First(&profile).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		active := profile.ActiveGeneration

		// 收集非活动 generation，按 generation DESC 排序（最近在前）。
		var gens []int
		if err := tx.Model(&model.PersonIdentityCenter{}).
			Where("person_id = ? AND generation != ?", personID, active).
			Distinct("generation").
			Order("generation DESC").
			Pluck("generation", &gens).Error; err != nil {
			return err
		}

		// 保留最近 keep 个非活动 generation，删除其余；keep<=0 删除全部非活动 generation。
		// 活动 generation 已被 WHERE 排除，永远不会被删除。
		var toDelete []int
		for i, g := range gens {
			if keep > 0 && i < keep {
				continue
			}
			toDelete = append(toDelete, g)
		}
		if len(toDelete) == 0 {
			return nil
		}
		// 保持成员与中心一致删除。
		if err := tx.Where("person_id = ? AND generation IN ?", personID, toDelete).
			Delete(&model.PersonIdentityCenterMember{}).Error; err != nil {
			return err
		}
		return tx.Where("person_id = ? AND generation IN ?", personID, toDelete).
			Delete(&model.PersonIdentityCenter{}).Error
	})
}
