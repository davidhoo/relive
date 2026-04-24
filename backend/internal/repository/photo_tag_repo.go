package repository

import (
	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
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
	BatchMigrate(items []struct{ ID uint; Tags string }) error
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
	tags := model.SplitTags(commaSeparated)

	// 删除旧标签
	if err := tx.Where("photo_id = ?", photoID).Delete(&model.PhotoTag{}).Error; err != nil {
		return err
	}
	// 插入新标签
	if len(tags) == 0 {
		return nil
	}
	records := make([]model.PhotoTag, 0, len(tags))
	for _, tag := range tags {
		records = append(records, model.PhotoTag{PhotoID: photoID, Tag: tag})
	}
	return tx.Create(&records).Error
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

func (r *photoTagRepository) BatchMigrate(items []struct{ ID uint; Tags string }) error {
	if len(items) == 0 {
		return nil
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			tags := model.SplitTags(item.Tags)
			if len(tags) == 0 {
				continue
			}
			records := make([]model.PhotoTag, 0, len(tags))
			for _, tag := range tags {
				records = append(records, model.PhotoTag{PhotoID: item.ID, Tag: tag})
			}
			if err := tx.Create(&records).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
