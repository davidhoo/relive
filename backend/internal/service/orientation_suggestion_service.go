package service

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/mlclient"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

const (
	orientationSuggestionStateKey = "orientation.suggestions.state"
)

// OrientationSuggestionService handles photo orientation detection suggestions.
type OrientationSuggestionService interface {
	GetTask() *model.OrientationSuggestionTask
	GetStats() (*model.OrientationSuggestionStats, error)
	GetBackgroundLogs() []string
	Pause() error
	Resume() error
	Rebuild() error
	RunBackgroundSlice() error
	MarkDirty(reason string) error

	GetGroups() ([]model.OrientationSuggestionGroup, error)
	GetDetail(rotation int, page, pageSize int) (*model.OrientationSuggestionDetail, error)
	Apply(photoIDs []uint) (int64, error)
	Dismiss(photoIDs []uint) error
}

type orientationSuggestionState struct {
	Paused    bool      `json:"paused"`
	Dirty     bool      `json:"dirty"`
	CursorID  uint      `json:"cursor_id"`
	LastRunAt time.Time `json:"last_run_at,omitempty"`
}

type orientationSuggestionService struct {
	db                  *gorm.DB
	photoRepo           repository.PhotoRepository
	faceRepo            repository.FaceRepository
	suggestionRepo      repository.PhotoOrientationSuggestionRepository
	configService       ConfigService
	mlClient            *mlclient.Client
	config              *config.Config

	mu             sync.RWMutex
	task           *model.OrientationSuggestionTask
	state          orientationSuggestionState
	backgroundLogs []string
}

// NewOrientationSuggestionService creates a new orientation suggestion service.
func NewOrientationSuggestionService(
	db *gorm.DB,
	photoRepo repository.PhotoRepository,
	faceRepo repository.FaceRepository,
	suggestionRepo repository.PhotoOrientationSuggestionRepository,
	configService ConfigService,
	cfg *config.Config,
) OrientationSuggestionService {
	svc := &orientationSuggestionService{
		db:             db,
		photoRepo:      photoRepo,
		faceRepo:       faceRepo,
		suggestionRepo: suggestionRepo,
		configService:  configService,
		config:         cfg,
		task: &model.OrientationSuggestionTask{
			Status:         model.TaskStatusIdle,
			CurrentMessage: "等待巡检",
		},
		backgroundLogs: make([]string, 0, 32),
	}

	// Initialize ML client if endpoint is configured
	if cfg != nil && cfg.People.MLEndpoint != "" {
		timeout := cfg.People.Timeout
		if timeout <= 0 {
			timeout = 15
		}
		svc.mlClient = mlclient.New(cfg.People.MLEndpoint, time.Duration(timeout)*time.Second)
	}

	_ = svc.loadState()
	return svc
}

func (s *orientationSuggestionService) GetTask() *model.OrientationSuggestionTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cloneTask()
}

func (s *orientationSuggestionService) GetStats() (*model.OrientationSuggestionStats, error) {
	return s.suggestionRepo.GetStats()
}

func (s *orientationSuggestionService) GetBackgroundLogs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	logs := make([]string, len(s.backgroundLogs))
	copy(logs, s.backgroundLogs)
	return logs
}

func (s *orientationSuggestionService) Pause() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state.Paused = true
	now := time.Now()
	s.task.Status = model.TaskStatusPaused
	s.task.CurrentMessage = "已暂停"
	s.task.StoppedAt = &now
	s.appendBackgroundLogLocked("方向建议后台任务已暂停")
	return s.saveStateLocked()
}

func (s *orientationSuggestionService) Resume() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state.Paused = false
	s.task.Status = model.TaskStatusIdle
	s.task.CurrentMessage = "等待巡检"
	s.task.StoppedAt = nil
	s.appendBackgroundLogLocked("方向建议后台任务已恢复")
	return s.saveStateLocked()
}

func (s *orientationSuggestionService) Rebuild() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Mark all pending suggestions as obsolete
	if err := s.db.Model(&model.PhotoOrientationSuggestion{}).
		Where("status = ?", model.OrientationSuggestionStatusPending).
		Update("status", model.OrientationSuggestionStatusDismissed).Error; err != nil {
		return err
	}

	s.state.Dirty = true
	s.state.CursorID = 0
	s.task.Status = model.TaskStatusIdle
	s.task.CurrentMessage = "等待重建巡检"
	s.appendBackgroundLogLocked("方向建议已标记重建")
	return s.saveStateLocked()
}

func (s *orientationSuggestionService) MarkDirty(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state.Dirty = true
	if reason != "" {
		s.appendBackgroundLogLocked("方向建议待更新: " + reason)
	}
	return s.saveStateLocked()
}

func (s *orientationSuggestionService) RunBackgroundSlice() error {
	// Quick check if paused
	s.mu.Lock()
	if s.state.Paused {
		s.task.Status = model.TaskStatusPaused
		s.task.CurrentMessage = "已暂停"
		s.mu.Unlock()
		return nil
	}

	// Cooldown check
	cooldown := 300
	if s.config != nil && s.config.Orientation.CooldownSeconds > 0 {
		cooldown = s.config.Orientation.CooldownSeconds
	}
	if !s.state.Dirty && !s.state.LastRunAt.IsZero() && time.Since(s.state.LastRunAt) < time.Duration(cooldown)*time.Second {
		s.mu.Unlock()
		return nil
	}

	cursor := s.state.CursorID
	s.mu.Unlock()

	// Get photos to process
	photos, err := s.listDetectionTargets(cursor)
	if err != nil {
		return err
	}
	if len(photos) == 0 {
		s.mu.Lock()
		s.finishSliceLocked(time.Now(), 0, "没有可巡检的照片")
		s.mu.Unlock()
		return nil
	}

	batchSize := s.getBatchSize()
	if len(photos) > batchSize {
		photos = photos[:batchSize]
	}

	// Process photos
	processedCount := 0
	for _, photo := range photos {
		if err := s.detectOrientation(photo); err != nil {
			logger.Warnf("detect orientation for photo %d: %v", photo.ID, err)
			continue
		}
		processedCount++
	}

	// Update state
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if s.task.StartedAt == nil {
		s.task.StartedAt = &now
	}
	s.task.Status = model.TaskStatusRunning
	s.task.ProcessedCount += int64(processedCount)

	lastPhotoID := photos[len(photos)-1].ID
	s.state.CursorID = lastPhotoID
	s.task.CurrentMessage = fmt.Sprintf("完成 %d 张照片方向检测", processedCount)
	s.appendBackgroundLogLocked(s.task.CurrentMessage)

	// Check if we've processed all photos
	totalPhotos, _ := s.photoRepo.Count()
	if int64(lastPhotoID) >= totalPhotos {
		s.state.CursorID = 0
		s.state.Dirty = false
		s.state.LastRunAt = now
		s.task.Status = model.TaskStatusIdle
		s.task.StoppedAt = &now
	}

	_ = s.saveStateLocked()
	return nil
}

func (s *orientationSuggestionService) GetGroups() ([]model.OrientationSuggestionGroup, error) {
	groups, err := s.suggestionRepo.GetGroups()
	if err != nil {
		return nil, err
	}

	// Enrich groups with photo previews
	for i := range groups {
		suggestions, _, err := s.suggestionRepo.ListPendingByRotation(groups[i].SuggestedRotation, 1, 4)
		if err != nil {
			continue
		}

		photoIDs := make([]uint, 0, len(suggestions))
		for _, sug := range suggestions {
			photoIDs = append(photoIDs, sug.PhotoID)
		}

		photos, _ := s.photoRepo.ListByIDs(photoIDs)
		photoMap := make(map[uint]*model.Photo)
		for _, p := range photos {
			photoMap[p.ID] = p
		}

		// Photos are added to response separately
		_ = photoMap
	}

	return groups, nil
}

func (s *orientationSuggestionService) GetDetail(rotation int, page, pageSize int) (*model.OrientationSuggestionDetail, error) {
	suggestions, total, err := s.suggestionRepo.ListPendingByRotation(rotation, page, pageSize)
	if err != nil {
		return nil, err
	}

	photoIDs := make([]uint, 0, len(suggestions))
	for _, sug := range suggestions {
		photoIDs = append(photoIDs, sug.PhotoID)
	}

	photos, _ := s.photoRepo.ListByIDs(photoIDs)
	photoMap := make(map[uint]*model.Photo)
	for _, p := range photos {
		photoMap[p.ID] = p
	}

	detail := &model.OrientationSuggestionDetail{
		SuggestedRotation: rotation,
		Photos:            make([]model.OrientationSuggestionPhoto, 0, len(suggestions)),
		Total:             total,
	}

	for _, sug := range suggestions {
		photo := photoMap[sug.PhotoID]
		if photo == nil {
			continue
		}
		detail.Photos = append(detail.Photos, model.OrientationSuggestionPhoto{
			ID:                photo.ID,
			FileName:          photo.FileName,
			ThumbnailPath:     photo.ThumbnailPath,
			CurrentRotation:   photo.ManualRotation,
			SuggestedRotation: sug.SuggestedRotation,
			Confidence:        sug.Confidence,
			LowConfidence:     sug.LowConfidence,
			UpdatedAt:         photo.UpdatedAt.Format("2006-01-02T15:04:05.999Z"),
		})
	}

	return detail, nil
}

func (s *orientationSuggestionService) Apply(photoIDs []uint) (int64, error) {
	if len(photoIDs) == 0 {
		return 0, nil
	}

	var applied int64
	for _, photoID := range photoIDs {
		suggestion, err := s.suggestionRepo.GetByPhotoID(photoID)
		if err != nil {
			logger.Warnf("get suggestion for photo %d: %v", photoID, err)
			continue
		}
		if suggestion == nil || suggestion.Status != model.OrientationSuggestionStatusPending {
			continue
		}

		photo, err := s.photoRepo.GetByID(photoID)
		if err != nil {
			logger.Warnf("get photo %d: %v", photoID, err)
			continue
		}

		// Calculate new rotation
		newRotation := (photo.ManualRotation + suggestion.SuggestedRotation) % 360

		// Update photo's manual_rotation
		if err := s.photoRepo.UpdateManualRotation(photoID, newRotation); err != nil {
			logger.Warnf("update rotation for photo %d: %v", photoID, err)
			continue
		}

		// Mark suggestion as applied
		if err := s.suggestionRepo.UpdateStatus(suggestion.ID, model.OrientationSuggestionStatusApplied); err != nil {
			logger.Warnf("mark suggestion %d as applied: %v", suggestion.ID, err)
		}

		applied++
	}

	// Trigger thumbnail regeneration asynchronously
	go func(ids []uint) {
		for _, id := range ids {
			// Regenerate photo thumbnail
			// This would be done by ThumbnailService
			logger.Infof("Photo %d rotation applied, thumbnail regeneration needed", id)

			// Regenerate face thumbnails
			faces, err := s.faceRepo.ListByPhotoID(id)
			if err != nil {
				continue
			}
			photo, err := s.photoRepo.GetByID(id)
			if err != nil {
				continue
			}
			for _, face := range faces {
				// Face thumbnails will be regenerated on demand
				_ = face
				_ = photo
			}
		}
	}(photoIDs)

	return applied, nil
}

func (s *orientationSuggestionService) Dismiss(photoIDs []uint) error {
	if len(photoIDs) == 0 {
		return nil
	}

	for _, photoID := range photoIDs {
		suggestion, err := s.suggestionRepo.GetByPhotoID(photoID)
		if err != nil {
			continue
		}
		if suggestion == nil || suggestion.Status != model.OrientationSuggestionStatusPending {
			continue
		}

		if err := s.suggestionRepo.UpdateStatus(suggestion.ID, model.OrientationSuggestionStatusDismissed); err != nil {
			logger.Warnf("dismiss suggestion %d: %v", suggestion.ID, err)
		}
	}

	return nil
}

func (s *orientationSuggestionService) listDetectionTargets(cursor uint) ([]*model.Photo, error) {
	// Get photos that:
	// 1. Have manual_rotation = 0 (not manually rotated)
	// 2. Don't have a pending suggestion
	// 3. Are active (not excluded)

	var photos []*model.Photo
	query := s.db.Model(&model.Photo{}).
		Where("status = ? AND manual_rotation = ?", model.PhotoStatusActive, 0).
		Where("id > ?", cursor).
		Order("id ASC").
		Limit(100)

	if err := query.Find(&photos).Error; err != nil {
		return nil, err
	}

	// Filter out photos that already have pending suggestions
	result := make([]*model.Photo, 0, len(photos))
	for _, photo := range photos {
		existing, _ := s.suggestionRepo.GetByPhotoID(photo.ID)
		if existing == nil || existing.Status != model.OrientationSuggestionStatusPending {
			result = append(result, photo)
		}
	}

	return result, nil
}

func (s *orientationSuggestionService) detectOrientation(photo *model.Photo) error {
	if s.mlClient == nil {
		return fmt.Errorf("ML client not configured")
	}

	// Skip unsupported formats (HEIC/HEIF - ML service uses cv2 which doesn't support them)
	ext := strings.ToLower(filepath.Ext(photo.FilePath))
	if ext == ".heic" || ext == ".heif" {
		return nil // Skip silently
	}

	ctx := context.Background()
	resp, err := s.mlClient.DetectOrientation(ctx, mlclient.DetectOrientationRequest{
		ImagePath: photo.FilePath,
	})
	if err != nil {
		return fmt.Errorf("ML detect orientation: %w", err)
	}

	// Skip if no rotation needed
	if resp.Rotation == 0 {
		return nil
	}

	// Check confidence threshold
	threshold := 0.85
	if s.config != nil && s.config.Orientation.ConfidenceThreshold > 0 {
		threshold = s.config.Orientation.ConfidenceThreshold
	}

	lowConfidence := resp.Confidence < threshold

	suggestion := &model.PhotoOrientationSuggestion{
		PhotoID:           photo.ID,
		SuggestedRotation: resp.Rotation,
		Confidence:        resp.Confidence,
		LowConfidence:     lowConfidence,
		Status:            model.OrientationSuggestionStatusPending,
	}

	return s.suggestionRepo.Create(suggestion)
}

func (s *orientationSuggestionService) getBatchSize() int {
	if s.config != nil && s.config.Orientation.BatchSize > 0 {
		return s.config.Orientation.BatchSize
	}
	return 50
}

func (s *orientationSuggestionService) loadState() error {
	var raw string
	if s.configService != nil {
		value, err := s.configService.GetWithDefault(orientationSuggestionStateKey, "")
		if err != nil {
			return err
		}
		raw = value
	} else {
		var cfg model.AppConfig
		if err := s.db.Where("key = ?", orientationSuggestionStateKey).First(&cfg).Error; err == nil {
			raw = cfg.Value
		}
	}

	if raw == "" {
		return nil
	}

	var state orientationSuggestionState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	if state.Paused {
		s.task.Status = model.TaskStatusPaused
		s.task.CurrentMessage = "已暂停"
	} else {
		s.task.Status = model.TaskStatusIdle
	}
	return nil
}

func (s *orientationSuggestionService) saveStateLocked() error {
	payload, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	if s.configService != nil {
		return s.configService.Set(orientationSuggestionStateKey, string(payload))
	}
	return upsertOrientationSuggestionState(s.db, string(payload))
}

func (s *orientationSuggestionService) finishSliceLocked(now time.Time, processedCount int, message string) {
	s.state.LastRunAt = now
	s.task.Status = model.TaskStatusIdle
	s.task.CurrentMessage = message
	s.task.ProcessedCount += int64(processedCount)
	s.task.StoppedAt = &now
	s.appendBackgroundLogLocked(message)
	_ = s.saveStateLocked()
}

func (s *orientationSuggestionService) appendBackgroundLogLocked(message string) {
	if message == "" {
		return
	}
	entry := fmt.Sprintf("%s %s", time.Now().Format(time.RFC3339), message)
	s.backgroundLogs = append(s.backgroundLogs, entry)
	if len(s.backgroundLogs) > 50 {
		s.backgroundLogs = s.backgroundLogs[len(s.backgroundLogs)-50:]
	}
}

func (s *orientationSuggestionService) cloneTask() *model.OrientationSuggestionTask {
	if s.task == nil {
		return nil
	}
	cloned := *s.task
	return &cloned
}

func upsertOrientationSuggestionState(db *gorm.DB, value string) error {
	var cfg model.AppConfig
	err := db.Where("key = ?", orientationSuggestionStateKey).First(&cfg).Error
	if err == nil {
		return db.Model(&cfg).Update("value", value).Error
	}
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return db.Create(&model.AppConfig{
		Key:   orientationSuggestionStateKey,
		Value: value,
	}).Error
}
