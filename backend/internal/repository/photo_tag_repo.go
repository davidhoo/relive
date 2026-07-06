package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PhotoTagRepository 照片标签仓库接口
type PhotoTagRepository interface {
	// SyncTags 同步照片标签（DELETE + INSERT，幂等）— 开独立事务
	SyncTags(photoID uint, commaSeparated string) error
	// SyncTagsTx 在已有事务内同步照片标签（避免嵌套事务导致 SQLite 自死锁）
	SyncTagsTx(tx *gorm.DB, photoID uint, commaSeparated string) error
	// GetTagsByPhotoIDs 批量加载多照片标签
	GetTagsByPhotoIDs(ids []uint) (map[uint][]string, error)
	// BatchMigrate 批量写入（启动迁移用）
	BatchMigrate(items []struct {
		ID   uint
		Tags string
	}) error
	// DeleteTagsByPhotoID 删除某照片的全部标签并同步统计表（开独立事务）
	DeleteTagsByPhotoID(photoID uint) error
	// DeleteTagsByPhotoIDTx 在已有事务内删除某照片的全部标签并同步统计表
	DeleteTagsByPhotoIDTx(tx *gorm.DB, photoID uint) error
}

type photoTagRepository struct {
	db *gorm.DB
}

// NewPhotoTagRepository 创建照片标签仓库
func NewPhotoTagRepository(db *gorm.DB) PhotoTagRepository {
	return &photoTagRepository{db: db}
}

func (r *photoTagRepository) SyncTags(photoID uint, commaSeparated string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		return r.syncTagsInTx(tx, photoID, commaSeparated)
	})
}

func (r *photoTagRepository) SyncTagsTx(tx *gorm.DB, photoID uint, commaSeparated string) error {
	return r.syncTagsInTx(tx, photoID, commaSeparated)
}

func (r *photoTagRepository) syncTagsInTx(tx *gorm.DB, photoID uint, commaSeparated string) error {
	newTags := model.SplitTags(commaSeparated)

	// 读取旧标签以计算增量，避免对统计表全量重算
	var oldTags []string
	if err := tx.Model(&model.PhotoTag{}).Where("photo_id = ?", photoID).Pluck("tag", &oldTags).Error; err != nil {
		return err
	}
	added, removed := diffStringSets(oldTags, newTags)

	// 删除旧标签
	if err := tx.Where("photo_id = ?", photoID).Delete(&model.PhotoTag{}).Error; err != nil {
		return err
	}
	// 插入新标签
	if len(newTags) > 0 {
		records := make([]model.PhotoTag, 0, len(newTags))
		for _, tag := range newTags {
			records = append(records, model.PhotoTag{PhotoID: photoID, Tag: tag})
		}
		if err := tx.Create(&records).Error; err != nil {
			return err
		}
	}

	// 同步统计表（与 photo_tags 修改同事务，保证一致回滚）
	return applyStatsDelta(tx, added, removed)
}

func (r *photoTagRepository) GetTagsByPhotoIDs(ids []uint) (map[uint][]string, error) {
	result := make(map[uint][]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}

	var allTags []model.PhotoTag
	for _, chunk := range chunkIDs(ids) {
		var tags []model.PhotoTag
		if err := r.db.Where("photo_id IN ?", chunk).Order("photo_id, tag").Find(&tags).Error; err != nil {
			return nil, err
		}
		allTags = append(allTags, tags...)
	}

	for _, t := range allTags {
		result[t.PhotoID] = append(result[t.PhotoID], t.Tag)
	}
	return result, nil
}

func (r *photoTagRepository) BatchMigrate(items []struct {
	ID   uint
	Tags string
}) error {
	if len(items) == 0 {
		return nil
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		// affectedTags 收集本次涉及的标签，迁移结束后按标签重算统计。
		// 使用重算而非计数增减，以正确处理 OnConflict 跳过的重复行。
		affectedTags := make(map[string]struct{})
		for _, item := range items {
			tags := model.SplitTags(item.Tags)
			if len(tags) == 0 {
				continue
			}
			records := make([]model.PhotoTag, 0, len(tags))
			for _, tag := range tags {
				records = append(records, model.PhotoTag{PhotoID: item.ID, Tag: tag})
				affectedTags[tag] = struct{}{}
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&records).Error; err != nil {
				return err
			}
		}
		for tag := range affectedTags {
			if err := upsertTagStatsFromCount(tx, tag); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteTagsByPhotoID 删除某照片的全部标签并同步统计表（开独立事务）
func (r *photoTagRepository) DeleteTagsByPhotoID(photoID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		return r.DeleteTagsByPhotoIDTx(tx, photoID)
	})
}

// DeleteTagsByPhotoIDTx 在已有事务内删除某照片的全部标签并同步统计表
func (r *photoTagRepository) DeleteTagsByPhotoIDTx(tx *gorm.DB, photoID uint) error {
	var oldTags []string
	if err := tx.Model(&model.PhotoTag{}).Where("photo_id = ?", photoID).Pluck("tag", &oldTags).Error; err != nil {
		return err
	}
	if len(oldTags) == 0 {
		return nil
	}
	if err := tx.Where("photo_id = ?", photoID).Delete(&model.PhotoTag{}).Error; err != nil {
		return err
	}
	return applyStatsDelta(tx, nil, oldTags)
}

// applyStatsDelta 在当前事务内按增量更新 photo_tag_stats。
//   - added：新增的标签，photo_count +1（不存在则插入）
//   - removed：移除的标签，photo_count -1，降至 0 时删除该行
//
// 使用 CASE 守护避免出现负数；SQLite 写入串行（MaxOpenConns=1），
// 且调用方均在事务内完成读取-计算-更新，不会出现计数丢失或重复记录。
func applyStatsDelta(tx *gorm.DB, added, removed []string) error {
	now := time.Now()

	for _, tag := range added {
		if err := tx.Exec(
			`INSERT INTO photo_tag_stats(tag, photo_count, updated_at) VALUES(?, 1, ?)
			 ON CONFLICT(tag) DO UPDATE SET photo_count = photo_tag_stats.photo_count + 1, updated_at = excluded.updated_at`,
			tag, now,
		).Error; err != nil {
			return err
		}
	}

	for _, tag := range removed {
		if err := tx.Exec(
			`UPDATE photo_tag_stats
			    SET photo_count = CASE WHEN photo_count > 0 THEN photo_count - 1 ELSE 0 END,
			        updated_at = ?
			  WHERE tag = ?`,
			now, tag,
		).Error; err != nil {
			return err
		}
		// 数量降至零时删除对应统计记录
		if err := tx.Exec(
			`DELETE FROM photo_tag_stats WHERE tag = ? AND photo_count <= 0`,
			tag,
		).Error; err != nil {
			return err
		}
	}
	return nil
}

// upsertTagStatsFromCount 用 photo_tags 的实际行数重算单个标签的统计（幂等）。
// 用于批量迁移等 OnConflict 场景，保证统计与明细表一致。
func upsertTagStatsFromCount(tx *gorm.DB, tag string) error {
	if err := tx.Exec(
		`INSERT INTO photo_tag_stats(tag, photo_count, updated_at)
		 SELECT ?, COUNT(*), ?
		   FROM photo_tags
		  WHERE tag = ?
		 ON CONFLICT(tag) DO UPDATE SET photo_count = excluded.photo_count, updated_at = excluded.updated_at`,
		tag, time.Now(), tag,
	).Error; err != nil {
		return err
	}
	// 实际行数为 0 时清除残留
	return tx.Exec(`DELETE FROM photo_tag_stats WHERE tag = ? AND photo_count <= 0`, tag).Error
}

// diffStringSets 计算新旧标签集合的差量：added 为新增，removed 为消失。
func diffStringSets(old, current []string) (added, removed []string) {
	oldSet := make(map[string]struct{}, len(old))
	for _, t := range old {
		oldSet[t] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(current))
	for _, t := range current {
		newSet[t] = struct{}{}
	}
	for _, t := range current {
		if _, ok := oldSet[t]; !ok {
			added = append(added, t)
		}
	}
	for _, t := range old {
		if _, ok := newSet[t]; !ok {
			removed = append(removed, t)
		}
	}
	return added, removed
}
