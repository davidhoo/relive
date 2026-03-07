package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

func (s *displayService) GenerateDailyBatch(date time.Time, force bool) (*model.DailyDisplayBatch, error) {
	batchDate := normalizeBatchDate(date)
	existing, err := s.findDailyBatchByDate(batchDate)
	if err == nil && existing != nil && existing.Status == model.DailyDisplayBatchStatusReady && !force {
		return existing, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	strategyConfig := s.getDisplayStrategyConfig()
	var excludePhotoIDs []uint
	if s.config.Display.AvoidRepeatDays > 0 {
		excludePhotoIDs, err = s.displayRecordRepo.GetDisplayedPhotoIDsAll(s.config.Display.AvoidRepeatDays)
		if err != nil {
			logger.Warnf("Get globally displayed photo IDs failed: %v", err)
			excludePhotoIDs = nil
		}
	}
	photos, err := s.previewPhotosWithExcludes(&strategyConfig, &date, excludePhotoIDs)
	if err != nil {
		return nil, fmt.Errorf("preview daily batch: %w", err)
	}
	if len(photos) == 0 {
		return nil, fmt.Errorf("no display photos available for %s", batchDate)
	}

	displayRoot := util.DisplayBatchRoot(s.config.Photos.ThumbnailPath)
	finalRoot := filepath.Join(displayRoot, batchDate)
	tempRoot := filepath.Join(displayRoot, fmt.Sprintf("%s.tmp-%d", batchDate, time.Now().UnixNano()))
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create temp batch dir: %w", err)
	}
	defer os.RemoveAll(tempRoot)

	items := make([]*model.DailyDisplayItem, 0, len(photos))
	assetsBySequence := make(map[int][]*model.DailyDisplayAsset, len(photos))
	activeProfiles := util.ActiveEmbeddedRenderProfiles()

	for idx, photo := range photos {
		sequence := idx + 1
		previewRelPath := filepath.Join(batchDate, "preview", fmt.Sprintf("%03d.jpg", sequence))
		previewAbsPath := filepath.Join(tempRoot, "preview", fmt.Sprintf("%03d.jpg", sequence))
		title, subtitle := buildDisplayText(photo)
		if err := util.GenerateDisplayPreview(photo.FilePath, previewAbsPath, 480, 800, title, subtitle); err != nil {
			return nil, fmt.Errorf("generate preview for photo %d: %w", photo.ID, err)
		}

		item := &model.DailyDisplayItem{
			Sequence:       sequence,
			PhotoID:        photo.ID,
			PreviewJPGPath: previewRelPath,
			PreviewWidth:   480,
			PreviewHeight:  800,
			CanvasTemplate: util.DefaultCanvasTemplate,
		}
		items = append(items, item)

		sequenceAssets := make([]*model.DailyDisplayAsset, 0, len(activeProfiles))
		for _, profile := range activeProfiles {
			ditherPreviewRelPath := filepath.Join(batchDate, profile.Name, fmt.Sprintf("%03d-preview.jpg", sequence))
			binRelPath := filepath.Join(batchDate, profile.Name, fmt.Sprintf("%03d.bin", sequence))
			headerRelPath := filepath.Join(batchDate, profile.Name, fmt.Sprintf("%03d.h", sequence))
			ditherPreviewAbsPath := filepath.Join(tempRoot, profile.Name, fmt.Sprintf("%03d-preview.jpg", sequence))
			binAbsPath := filepath.Join(tempRoot, profile.Name, fmt.Sprintf("%03d.bin", sequence))
			headerAbsPath := filepath.Join(tempRoot, profile.Name, fmt.Sprintf("%03d.h", sequence))
			checksum, fileSize, err := util.BuildRenderArtifacts(previewAbsPath, profile, ditherPreviewAbsPath, binAbsPath, headerAbsPath)
			if err != nil {
				return nil, fmt.Errorf("generate render asset %s for photo %d: %w", profile.Name, photo.ID, err)
			}
			sequenceAssets = append(sequenceAssets, &model.DailyDisplayAsset{
				RenderProfile:     profile.Name,
				DitherPreviewPath: ditherPreviewRelPath,
				BinPath:           binRelPath,
				HeaderPath:        headerRelPath,
				Checksum:          checksum,
				FileSize:          fileSize,
			})
		}
		assetsBySequence[sequence] = sequenceAssets
	}

	now := time.Now().UTC()
	batch := &model.DailyDisplayBatch{
		BatchDate:        batchDate,
		Status:           model.DailyDisplayBatchStatusReady,
		ItemCount:        len(items),
		CanvasTemplate:   util.DefaultCanvasTemplate,
		StrategySnapshot: mustMarshalJSON(strategyConfig),
		GeneratedAt:      &now,
	}

	var saved *model.DailyDisplayBatch
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if existing != nil {
			if err := deleteDailyBatch(tx, existing.ID); err != nil {
				return err
			}
		}

		if err := tx.Create(batch).Error; err != nil {
			return err
		}

		for _, item := range items {
			item.BatchID = batch.ID
			if err := tx.Create(item).Error; err != nil {
				return err
			}
			for _, asset := range assetsBySequence[item.Sequence] {
				asset.ItemID = item.ID
				if err := tx.Create(asset).Error; err != nil {
					return err
				}
			}
		}

		if err := tx.Model(&model.DevicePlaybackState{}).
			Where("batch_date = ?", batchDate).
			Updates(map[string]interface{}{
				"batch_id":            batch.ID,
				"current_sequence":    1,
				"last_served_item_id": nil,
				"last_served_at":      nil,
			}).Error; err != nil {
			return err
		}

		loaded, err := s.loadDailyBatchWithTx(tx, batchDate)
		if err != nil {
			return err
		}
		saved = loaded
		return nil
	}); err != nil {
		return nil, fmt.Errorf("save daily batch: %w", err)
	}

	if err := os.RemoveAll(finalRoot); err != nil {
		return nil, fmt.Errorf("remove previous batch files: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(finalRoot), 0o755); err != nil {
		return nil, fmt.Errorf("ensure display batch parent dir: %w", err)
	}
	if err := os.Rename(tempRoot, finalRoot); err != nil {
		return nil, fmt.Errorf("activate display batch files: %w", err)
	}

	logger.Infof("Generated daily display batch for %s with %d items", batchDate, len(items))
	return saved, nil
}

func (s *displayService) GetDailyBatch(date time.Time) (*model.DailyDisplayBatch, error) {
	return s.findDailyBatchByDate(normalizeBatchDate(date))
}

func (s *displayService) ListDailyBatches(limit int) ([]*model.DailyDisplayBatch, error) {
	if limit <= 0 {
		limit = 30
	}
	var batches []*model.DailyDisplayBatch
	err := s.db.
		Order("batch_date DESC").
		Limit(limit).
		Find(&batches).Error
	if err != nil {
		return nil, err
	}
	for idx, batch := range batches {
		loaded, err := loadDailyBatchByID(s.db, batch.ID)
		if err != nil {
			return nil, err
		}
		batches[idx] = loaded
	}
	return batches, nil
}

func (s *displayService) GetDeviceDisplay(deviceID uint, renderProfile string) (*model.DeviceDisplaySelection, error) {
	profile, ok := util.GetRenderProfile(renderProfile)
	if !ok {
		profile, _ = util.GetRenderProfile(util.DefaultRenderProfile())
	}

	batch, err := s.ensureDailyBatch(time.Now())
	if err != nil {
		return nil, err
	}
	if batch.ItemCount == 0 {
		return nil, fmt.Errorf("daily batch %s has no items", batch.BatchDate)
	}

	selection := &model.DeviceDisplaySelection{}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var state model.DevicePlaybackState
		err := tx.Where("device_id = ?", deviceID).First(&state).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			state = model.DevicePlaybackState{
				DeviceID:        deviceID,
				BatchID:         batch.ID,
				BatchDate:       batch.BatchDate,
				CurrentSequence: 1,
			}
			if err := tx.Create(&state).Error; err != nil {
				return err
			}
		}

		state.BatchDate = normalizeBatchDateString(state.BatchDate)

		if state.BatchID != batch.ID || state.BatchDate != batch.BatchDate || state.CurrentSequence < 1 || state.CurrentSequence > batch.ItemCount {
			state.BatchID = batch.ID
			state.BatchDate = batch.BatchDate
			state.CurrentSequence = 1
			if err := tx.Model(&state).Updates(map[string]interface{}{
				"batch_id":            batch.ID,
				"batch_date":          batch.BatchDate,
				"current_sequence":    1,
				"last_served_item_id": nil,
				"last_served_at":      nil,
			}).Error; err != nil {
				return err
			}
		}

		var item model.DailyDisplayItem
		if err := tx.Where("batch_id = ? AND sequence = ?", batch.ID, state.CurrentSequence).First(&item).Error; err != nil {
			return err
		}

		var asset model.DailyDisplayAsset
		if err := tx.Where("item_id = ? AND render_profile = ?", item.ID, profile.Name).First(&asset).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := tx.Where("item_id = ?", item.ID).Order("id ASC").First(&asset).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}

		nextSequence := state.CurrentSequence + 1
		if nextSequence > batch.ItemCount {
			nextSequence = 1
		}
		now := time.Now().UTC()
		if err := tx.Model(&state).Updates(map[string]interface{}{
			"current_sequence":    nextSequence,
			"last_served_item_id": item.ID,
			"last_served_at":      &now,
			"batch_id":            batch.ID,
			"batch_date":          batch.BatchDate,
		}).Error; err != nil {
			return err
		}

		selection.BatchDate = normalizeBatchDateString(batch.BatchDate)
		selection.TotalCount = batch.ItemCount
		selection.Sequence = item.Sequence
		selection.Item = &item
		selection.Asset = &asset
		return nil
	}); err != nil {
		return nil, err
	}

	return selection, nil
}

func (s *displayService) GetDailyDisplayItem(id uint) (*model.DailyDisplayItem, error) {
	var item model.DailyDisplayItem
	err := s.db.Preload("Assets").Preload("Photo").First(&item, id).Error
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *displayService) GetDailyDisplayAsset(id uint) (*model.DailyDisplayAsset, error) {
	var asset model.DailyDisplayAsset
	err := s.db.First(&asset, id).Error
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func (s *displayService) GetRenderProfiles() []model.RenderProfileResponse {
	profiles := util.BuiltinRenderProfiles()
	responses := make([]model.RenderProfileResponse, 0, len(profiles))
	for _, profile := range profiles {
		responses = append(responses, model.RenderProfileResponse{
			Name:             profile.Name,
			DisplayName:      profile.DisplayName,
			Width:            profile.Width,
			Height:           profile.Height,
			Palette:          profile.PaletteName,
			DitherMode:       profile.DitherMode,
			CanvasTemplate:   profile.CanvasTemplate,
			DefaultForDevice: profile.DefaultForDevice,
		})
	}
	return responses
}

func (s *displayService) ensureDailyBatch(date time.Time) (*model.DailyDisplayBatch, error) {
	batch, err := s.GetDailyBatch(date)
	if err == nil && batch != nil && batch.Status == model.DailyDisplayBatchStatusReady {
		return batch, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	return s.GenerateDailyBatch(date, false)
}

func (s *displayService) findDailyBatchByDate(batchDate string) (*model.DailyDisplayBatch, error) {
	return s.loadDailyBatchWithTx(s.db, batchDate)
}

func loadDailyBatchByID(tx *gorm.DB, batchID uint) (*model.DailyDisplayBatch, error) {
	var batch model.DailyDisplayBatch
	err := tx.
		Preload("Items", func(db *gorm.DB) *gorm.DB { return db.Order("sequence ASC") }).
		Preload("Items.Photo").
		Preload("Items.Assets", func(db *gorm.DB) *gorm.DB { return db.Order("render_profile ASC") }).
		First(&batch, batchID).Error
	if err != nil {
		return nil, err
	}
	batch.BatchDate = normalizeBatchDateString(batch.BatchDate)
	batch.BatchDate = normalizeBatchDateString(batch.BatchDate)
	return &batch, nil
}

func (s *displayService) loadDailyBatchWithTx(tx *gorm.DB, batchDate string) (*model.DailyDisplayBatch, error) {
	var batch model.DailyDisplayBatch
	err := tx.
		Preload("Items", func(db *gorm.DB) *gorm.DB { return db.Order("sequence ASC") }).
		Preload("Items.Photo").
		Preload("Items.Assets", func(db *gorm.DB) *gorm.DB { return db.Order("render_profile ASC") }).
		Where("date(batch_date) = date(?)", batchDate).
		First(&batch).Error
	if err != nil {
		return nil, err
	}
	batch.BatchDate = normalizeBatchDateString(batch.BatchDate)
	return &batch, nil
}

func deleteDailyBatch(tx *gorm.DB, batchID uint) error {
	subQuery := tx.Model(&model.DailyDisplayItem{}).Select("id").Where("batch_id = ?", batchID)
	if err := tx.Unscoped().Where("item_id IN (?)", subQuery).Delete(&model.DailyDisplayAsset{}).Error; err != nil {
		return err
	}
	if err := tx.Unscoped().Where("batch_id = ?", batchID).Delete(&model.DailyDisplayItem{}).Error; err != nil {
		return err
	}
	if err := tx.Unscoped().Delete(&model.DailyDisplayBatch{}, batchID).Error; err != nil {
		return err
	}
	return nil
}

func normalizeBatchDate(date time.Time) string {
	return date.In(time.Local).Format("2006-01-02")
}

func normalizeBatchDateString(value string) string {
	if len(value) >= 10 {
		return value[:10]
	}
	return value
}

func mustMarshalJSON(value interface{}) string {
	payload, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func buildDisplayText(photo *model.Photo) (string, string) {
	if photo == nil {
		return "", ""
	}

	title := strings.TrimSpace(photo.Caption)
	if title == "" {
		title = strings.TrimSuffix(photo.FileName, filepath.Ext(photo.FileName))
	}

	parts := make([]string, 0, 2)
	if photo.TakenAt != nil {
		parts = append(parts, photo.TakenAt.In(time.Local).Format("2006.01.02"))
	}
	if location := strings.TrimSpace(photo.Location); location != "" {
		parts = append(parts, location)
	}

	return title, strings.Join(parts, " · ")
}
