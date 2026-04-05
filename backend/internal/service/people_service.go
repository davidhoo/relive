package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/mlclient"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

const (
	peoplePriorityScan         = 50
	peoplePriorityManual       = 80
	peoplePriorityPassive      = 100
	peopleClusterThreshold     = 0.45
	peoplePrototypeCount       = 5
	peoplePrototypeCandidates  = 10
	peopleLinkThreshold        = 0.35
	peopleAttachThreshold      = 0.50
	peopleMinClusterFaces      = 2
	peopleFeedbackPollInterval = 250 * time.Millisecond

	// confirmedPersonDiscount lowers the attach threshold for persons with manual-locked faces,
	// making it easier for new faces to join user-confirmed identities (e.g., family members).
	confirmedPersonDiscount = 0.05
)

type PeopleMLClient interface {
	DetectFaces(ctx context.Context, request mlclient.DetectFacesRequest) (*mlclient.DetectFacesResponse, error)
}

type PeopleService interface {
	StartBackground() (*model.PeopleTask, error)
	StopBackground() error
	GetTaskStatus() *model.PeopleTask
	GetStats() (*model.PeopleStatsResponse, error)
	GetBackgroundLogs() []string
	EnqueuePhoto(photoID uint, source string, priority int, force bool) error
	EnqueueByPath(path string, source string, priority int) (int, error)
	ResetAllPeople() (int, error)
	MergePeople(targetPersonID uint, sourcePersonIDs []uint) (*model.ReclusterResult, error)
	SplitPerson(faceIDs []uint) (*model.Person, *model.ReclusterResult, error)
	MoveFaces(faceIDs []uint, targetPersonID uint) (*model.ReclusterResult, error)
	UpdatePersonCategory(personID uint, category string) error
	UpdatePersonName(personID uint, name string) error
	UpdatePersonAvatar(personID uint, faceID uint) error
	HandleShutdown() error
}

type peopleService struct {
	db             *gorm.DB
	photoRepo      repository.PhotoRepository
	faceRepo       repository.FaceRepository
	personRepo     repository.PersonRepository
	jobRepo        repository.PeopleJobRepository
	cannotLinkRepo repository.CannotLinkRepository
	config         *config.Config
	client         PeopleMLClient

	taskMutex        sync.RWMutex
	task             *model.PeopleTask
	active           *activePeopleTask
	backgroundLogMu  sync.RWMutex
	backgroundLogs   []string
	backgroundBusyMu sync.RWMutex
	backgroundBusy   bool

	feedbackMu            sync.Mutex
	feedbackRunning       bool
	feedbackPending       bool
	feedbackPollInterval  time.Duration
	feedbackReclusterHook func() model.ReclusterResult
}

type activePeopleTask struct {
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	stop   bool
}

func NewPeopleService(db *gorm.DB, photoRepo repository.PhotoRepository, faceRepo repository.FaceRepository, personRepo repository.PersonRepository, jobRepo repository.PeopleJobRepository, cannotLinkRepo repository.CannotLinkRepository, cfg *config.Config, client PeopleMLClient) PeopleService {
	// 清理上次异常退出遗留的非终态任务
	if err := jobRepo.InterruptNonTerminal("task interrupted because service restarted"); err != nil {
		logger.Errorf("Failed to interrupt non-terminal people jobs: %v", err)
	}

	return &peopleService{
		db:                   db,
		photoRepo:            photoRepo,
		faceRepo:             faceRepo,
		personRepo:           personRepo,
		jobRepo:              jobRepo,
		cannotLinkRepo:       cannotLinkRepo,
		config:               cfg,
		client:               client,
		feedbackPollInterval: peopleFeedbackPollInterval,
	}
}

func (s *peopleService) StartBackground() (*model.PeopleTask, error) {
	if s.client == nil {
		return nil, fmt.Errorf("people ml client not configured")
	}
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.active != nil {
		return nil, fmt.Errorf("people task already running")
	}

	now := time.Now()
	task := &model.PeopleTask{
		Status:    model.TaskStatusRunning,
		StartedAt: &now,
	}
	active := &activePeopleTask{
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	s.task = task
	s.active = active
	s.resetBackgroundLogs()
	s.appendBackgroundLog("人物后台任务已启动")
	go s.runBackground(active)
	return clonePeopleTask(task), nil
}

func (s *peopleService) StopBackground() error {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.active == nil {
		return fmt.Errorf("people task not running")
	}
	s.active.mu.Lock()
	if !s.active.stop {
		s.active.stop = true
		close(s.active.stopCh)
	}
	s.active.mu.Unlock()
	if s.task != nil && s.task.Status == model.TaskStatusRunning {
		s.task.Status = model.TaskStatusStopping
		s.appendBackgroundLog("收到停止请求，等待当前人物任务处理完成")
	}
	return nil
}

func (s *peopleService) GetTaskStatus() *model.PeopleTask {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()
	return clonePeopleTask(s.task)
}

func (s *peopleService) GetStats() (*model.PeopleStatsResponse, error) {
	stats, err := s.jobRepo.GetStats()
	if err != nil {
		return nil, err
	}
	return &model.PeopleStatsResponse{
		Total:      stats.Total,
		Pending:    stats.Pending,
		Queued:     stats.Queued,
		Processing: stats.Processing,
		Completed:  stats.Completed,
		Failed:     stats.Failed,
		Cancelled:  stats.Cancelled,
	}, nil
}

func (s *peopleService) GetBackgroundLogs() []string {
	s.backgroundLogMu.RLock()
	defer s.backgroundLogMu.RUnlock()
	logs := make([]string, len(s.backgroundLogs))
	copy(logs, s.backgroundLogs)
	return logs
}

func (s *peopleService) EnqueuePhoto(photoID uint, source string, priority int, force bool) error {
	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		return err
	}
	return s.enqueuePhotoModel(photo, source, priority, force)
}

func (s *peopleService) EnqueueByPath(path string, source string, priority int) (int, error) {
	photos, err := s.photoRepo.ListByPathPrefix(path)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, photo := range photos {
		if photo.Status == model.PhotoStatusExcluded {
			continue
		}
		if err := s.enqueuePhotoModel(photo, source, priority, false); err != nil {
			logger.Warnf("enqueue people by path failed for photo %d: %v", photo.ID, err)
			continue
		}
		count++
	}

	return count, nil
}

func (s *peopleService) HandleShutdown() error {
	s.taskMutex.RLock()
	active := s.active
	s.taskMutex.RUnlock()
	if active == nil {
		return nil
	}
	return s.StopBackground()
}

func (s *peopleService) ResetAllPeople() (int, error) {
	s.taskMutex.RLock()
	active := s.active
	s.taskMutex.RUnlock()

	if active != nil {
		_ = s.StopBackground()
		select {
		case <-active.done:
		case <-time.After(30 * time.Second):
			return 0, fmt.Errorf("timeout waiting for background task to stop")
		}
	}

	var count int
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM faces").Error; err != nil {
			return fmt.Errorf("delete faces: %w", err)
		}
		if err := tx.Exec("DELETE FROM people").Error; err != nil {
			return fmt.Errorf("delete people: %w", err)
		}
		if err := tx.Exec("DELETE FROM people_jobs").Error; err != nil {
			return fmt.Errorf("delete people_jobs: %w", err)
		}
		if err := tx.Exec("DELETE FROM cannot_link_constraints").Error; err != nil {
			return fmt.Errorf("delete cannot_link_constraints: %w", err)
		}
		if err := tx.Model(&model.Photo{}).
			Where("1 = 1").
			Updates(map[string]interface{}{
				"face_process_status": model.FaceProcessStatusNone,
				"face_count":          0,
				"top_person_category": "",
			}).Error; err != nil {
			return fmt.Errorf("reset photos: %w", err)
		}
		var affected int64
		tx.Model(&model.Photo{}).Where("status != ?", model.PhotoStatusExcluded).Count(&affected)
		count = int(affected)
		return nil
	})
	if err != nil {
		return 0, err
	}

	photos, err := s.photoRepo.ListAll()
	if err != nil {
		return count, fmt.Errorf("list photos for re-enqueue: %w", err)
	}
	enqueued := 0
	for _, photo := range photos {
		if photo.Status == model.PhotoStatusExcluded {
			continue
		}
		if err := s.enqueuePhotoModel(photo, "reset", peoplePriorityScan, true); err != nil {
			logger.Warnf("re-enqueue photo %d after reset failed: %v", photo.ID, err)
			continue
		}
		enqueued++
	}
	logger.Infof("people reset complete: %d photos reset, %d jobs enqueued", count, enqueued)
	return enqueued, nil
}

func (s *peopleService) MergePeople(targetPersonID uint, sourcePersonIDs []uint) (*model.ReclusterResult, error) {
	affectedPhotoIDs, err := s.personRepo.MergeInto(targetPersonID, sourcePersonIDs)
	if err != nil {
		return nil, err
	}
	// Clean up cannot-link constraints for merged (deleted) persons
	for _, sourceID := range sourcePersonIDs {
		if err := s.cannotLinkRepo.DeleteByPersonID(sourceID); err != nil {
			logger.Warnf("failed to clean cannot-link for merged person %d: %v", sourceID, err)
		}
	}
	if err := s.syncPersonState(targetPersonID); err != nil {
		return nil, err
	}
	if err := s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs); err != nil {
		return nil, err
	}
	s.scheduleFeedbackRecluster()
	return &model.ReclusterResult{}, nil
}

func (s *peopleService) SplitPerson(faceIDs []uint) (*model.Person, *model.ReclusterResult, error) {
	faces, err := s.faceRepo.ListByIDs(faceIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(faces) == 0 {
		return nil, nil, fmt.Errorf("faces not found")
	}

	var sourcePersonID uint
	for _, face := range faces {
		if face.PersonID == nil || *face.PersonID == 0 {
			return nil, nil, fmt.Errorf("face %d has no person", face.ID)
		}
		if sourcePersonID == 0 {
			sourcePersonID = *face.PersonID
			continue
		}
		if sourcePersonID != *face.PersonID {
			return nil, nil, fmt.Errorf("split faces must belong to the same person")
		}
	}

	sourcePerson, err := s.personRepo.GetByID(sourcePersonID)
	if err != nil {
		return nil, nil, err
	}
	if sourcePerson == nil {
		return nil, nil, fmt.Errorf("source person not found")
	}

	newPerson := &model.Person{Category: sourcePerson.Category}
	if err := s.personRepo.Create(newPerson); err != nil {
		return nil, nil, err
	}
	if err := s.faceRepo.ReassignFaces(faceIDs, newPerson.ID, "split"); err != nil {
		return nil, nil, err
	}

	if err := s.syncPersonState(sourcePersonID); err != nil {
		return nil, nil, err
	}
	if err := s.syncPersonState(newPerson.ID); err != nil {
		return nil, nil, err
	}
	if err := s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces)); err != nil {
		return nil, nil, err
	}

	// Record cannot-link: source person and new person must not be merged
	if err := s.cannotLinkRepo.Create(sourcePersonID, newPerson.ID); err != nil {
		logger.Warnf("failed to create cannot-link constraint: %v", err)
	}

	person, err := s.personRepo.GetByID(newPerson.ID)
	if err != nil {
		return nil, nil, err
	}
	s.scheduleFeedbackRecluster()
	return person, &model.ReclusterResult{}, nil
}

func (s *peopleService) MoveFaces(faceIDs []uint, targetPersonID uint) (*model.ReclusterResult, error) {
	faces, err := s.faceRepo.ListByIDs(faceIDs)
	if err != nil {
		return nil, err
	}
	if len(faces) == 0 {
		return nil, fmt.Errorf("faces not found")
	}

	sourcePersonIDs := make(map[uint]struct{})
	for _, face := range faces {
		if face.PersonID != nil && *face.PersonID != 0 && *face.PersonID != targetPersonID {
			sourcePersonIDs[*face.PersonID] = struct{}{}
		}
	}

	if err := s.faceRepo.ReassignFaces(faceIDs, targetPersonID, "move"); err != nil {
		return nil, err
	}

	if err := s.syncPersonState(targetPersonID); err != nil {
		return nil, err
	}
	for personID := range sourcePersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return nil, err
		}
	}

	if err := s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces)); err != nil {
		return nil, err
	}
	s.scheduleFeedbackRecluster()
	return &model.ReclusterResult{}, nil
}

func (s *peopleService) UpdatePersonCategory(personID uint, category string) error {
	if err := s.personRepo.UpdateFields(personID, map[string]interface{}{"category": category}); err != nil {
		return err
	}
	faces, err := s.faceRepo.ListByPersonID(personID)
	if err != nil {
		return err
	}
	return s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces))
}

func (s *peopleService) UpdatePersonName(personID uint, name string) error {
	return s.personRepo.UpdateFields(personID, map[string]interface{}{"name": name})
}

func (s *peopleService) UpdatePersonAvatar(personID uint, faceID uint) error {
	face, err := s.faceRepo.GetByID(faceID)
	if err != nil {
		return err
	}
	if face == nil || face.PersonID == nil || *face.PersonID != personID {
		return fmt.Errorf("face %d does not belong to person %d", faceID, personID)
	}
	return s.personRepo.UpdateFields(personID, map[string]interface{}{
		"representative_face_id": faceID,
		"avatar_locked":          true,
	})
}

func (s *peopleService) enqueuePhotoModel(photo *model.Photo, source string, priority int, force bool) error {
	if photo == nil {
		return fmt.Errorf("photo is nil")
	}
	if photo.Status == model.PhotoStatusExcluded {
		return nil
	}
	if source == "" {
		source = model.PeopleJobSourceManual
	}
	if priority <= 0 {
		priority = peoplePriorityManual
	}

	now := time.Now()
	if err := s.photoRepo.UpdateFields(photo.ID, map[string]interface{}{
		"face_process_status": model.FaceProcessStatusPending,
	}); err != nil {
		return err
	}

	activeJob, err := s.jobRepo.GetActiveByPhotoID(photo.ID)
	if err != nil {
		return err
	}
	if activeJob != nil {
		updates := map[string]interface{}{
			"priority":          priority,
			"source":            source,
			"last_requested_at": &now,
		}
		if force || activeJob.Status == model.PeopleJobStatusPending {
			updates["status"] = model.PeopleJobStatusQueued
		}
		return s.jobRepo.UpdateFields(activeJob.ID, updates)
	}

	job := &model.PeopleJob{
		PhotoID:         photo.ID,
		FilePath:        photo.FilePath,
		Status:          model.PeopleJobStatusQueued,
		Priority:        priority,
		Source:          source,
		QueuedAt:        now,
		LastRequestedAt: &now,
	}
	return s.jobRepo.Create(job)
}

func (s *peopleService) runBackground(active *activePeopleTask) {
	defer func() {
		now := time.Now()
		s.taskMutex.Lock()
		if s.task != nil && (s.task.Status == model.TaskStatusRunning || s.task.Status == model.TaskStatusStopping) {
			s.task.Status = model.TaskStatusStopped
			s.task.StoppedAt = &now
		}
		s.appendBackgroundLog("人物后台任务已停止")
		s.active = nil
		s.taskMutex.Unlock()
		close(active.done)
	}()

	for {
		active.mu.Lock()
		stopRequested := active.stop
		active.mu.Unlock()
		if stopRequested {
			return
		}

		job, err := s.jobRepo.ClaimNextJob()
		if err != nil {
			s.appendBackgroundLog(fmt.Sprintf("领取人物任务失败：%v", err))
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if job == nil {
			time.Sleep(300 * time.Millisecond)
			continue
		}

		s.setBackgroundBusy(true)
		err = s.processJob(job)
		s.setBackgroundBusy(false)
		if err != nil {
			s.appendBackgroundLog(fmt.Sprintf("处理人物任务 %d 失败：%v", job.ID, err))
		}

		s.taskMutex.Lock()
		if s.task != nil {
			s.task.CurrentPhotoID = job.PhotoID
			s.task.ProcessedJobs++
		}
		s.taskMutex.Unlock()
	}
}

func (s *peopleService) processJob(job *model.PeopleJob) error {
	photo, err := s.photoRepo.GetByID(job.PhotoID)
	if err != nil {
		return err
	}

	now := time.Now()
	if photo == nil || photo.Status == model.PhotoStatusExcluded {
		return s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusCancelled,
			"completed_at": &now,
		})
	}

	existingFaces, err := s.faceRepo.ListByPhotoID(photo.ID)
	if err != nil {
		return err
	}
	if hasManualLockedFaces(existingFaces) {
		if err := s.photoRepo.RecomputeTopPersonCategory([]uint{photo.ID}); err != nil {
			return err
		}
		return s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusCompleted,
			"last_error":   "",
			"completed_at": &now,
		})
	}

	if err := s.photoRepo.UpdateFields(photo.ID, map[string]interface{}{
		"face_process_status": model.FaceProcessStatusProcessing,
	}); err != nil {
		return err
	}

	processor := util.NewImageProcessor(1024, 85)
	processedImage, processErr := processor.ProcessForAI(photo.FilePath)
	if processErr != nil {
		logger.Warnf("process photo %d for people detect failed, falling back to image path: %v", photo.ID, processErr)
	}

	var imageBase64 string
	if len(processedImage) > 0 {
		imageBase64 = base64.StdEncoding.EncodeToString(processedImage)
	}

	result, detectErr := s.client.DetectFaces(context.Background(), mlclient.DetectFacesRequest{
		ImagePath:     photo.FilePath,
		ImageBase64:   imageBase64,
		MinConfidence: 0.5,
		MaxFaces:      20,
	})
	if detectErr != nil {
		if updateErr := s.photoRepo.UpdateFields(photo.ID, map[string]interface{}{
			"face_process_status": model.FaceProcessStatusFailed,
		}); updateErr != nil {
			logger.Warnf("update photo %d failed status after people detect error failed: %v", photo.ID, updateErr)
		}
		return s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusFailed,
			"last_error":   detectErr.Error(),
			"completed_at": &now,
		})
	}

	if result == nil {
		result = &mlclient.DetectFacesResponse{}
	}

	if len(result.Faces) == 0 {
		return s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("photo_id = ?", photo.ID).Delete(&model.Face{}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
				"face_process_status": model.FaceProcessStatusNoFace,
				"face_count":          0,
				"top_person_category": "",
			}).Error; err != nil {
				return err
			}
			return tx.Model(&model.PeopleJob{}).Where("id = ?", job.ID).Updates(map[string]interface{}{
				"status":       model.PeopleJobStatusCompleted,
				"last_error":   "",
				"completed_at": &now,
			}).Error
		})
	}

	previousPersonIDs := personIDsFromFaces(existingFaces)
	createdFaces := make([]*model.Face, 0, len(result.Faces))
	for _, detected := range result.Faces {
		embeddingPayload, err := json.Marshal(detected.Embedding)
		if err != nil {
			return err
		}
		thumbnailPath, err := s.generateFaceThumbnail(photo, detected.BBox)
		if err != nil {
			return err
		}
		face := &model.Face{
			PhotoID:       photo.ID,
			BBoxX:         detected.BBox.X,
			BBoxY:         detected.BBox.Y,
			BBoxWidth:     detected.BBox.Width,
			BBoxHeight:    detected.BBox.Height,
			Confidence:    detected.Confidence,
			QualityScore:  detected.QualityScore,
			Embedding:     embeddingPayload,
			ThumbnailPath: thumbnailPath,
			ClusterStatus: model.FaceClusterStatusPending,
			ClusterScore:  0,
			ClusteredAt:   nil,
		}
		createdFaces = append(createdFaces, face)
	}

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("photo_id = ?", photo.ID).Delete(&model.Face{}).Error; err != nil {
			return err
		}

		for _, face := range createdFaces {
			if err := tx.Create(face).Error; err != nil {
				return err
			}
		}

		if err := tx.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
			"face_process_status": model.FaceProcessStatusReady,
			"face_count":          len(createdFaces),
			"top_person_category": "",
		}).Error; err != nil {
			return err
		}
		return nil
	}); err != nil {
		return err
	}

	affectedPersonIDs, affectedPhotoIDs, clusterErr := s.runIncrementalClustering()
	if clusterErr != nil {
		if updateErr := s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusFailed,
			"last_error":   clusterErr.Error(),
			"completed_at": &now,
		}); updateErr != nil {
			logger.Warnf("update people job %d failed after clustering error: %v", job.ID, updateErr)
		}
		return clusterErr
	}

	for _, personID := range previousPersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return err
		}
	}
	for _, personID := range affectedPersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return err
		}
	}

	affectedPhotoIDs = append(affectedPhotoIDs, photo.ID)
	if err := s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs); err != nil {
		return err
	}

	return s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
		"status":       model.PeopleJobStatusCompleted,
		"last_error":   "",
		"completed_at": &now,
	})
}

func (s *peopleService) resetBackgroundLogs() {
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()
	s.backgroundLogs = nil
}

func (s *peopleService) appendBackgroundLog(message string) {
	entry := fmt.Sprintf("%s %s", time.Now().Format("15:04:05"), message)
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()
	s.backgroundLogs = append(s.backgroundLogs, entry)
	if len(s.backgroundLogs) > 100 {
		s.backgroundLogs = s.backgroundLogs[len(s.backgroundLogs)-100:]
	}
}

func (s *peopleService) scheduleFeedbackRecluster() {
	s.feedbackMu.Lock()
	s.feedbackPending = true
	if s.feedbackRunning {
		s.feedbackMu.Unlock()
		return
	}
	s.feedbackRunning = true
	s.feedbackMu.Unlock()

	go s.runFeedbackReclusterLoop()
}

func (s *peopleService) runFeedbackReclusterLoop() {
	for {
		if s.shouldDelayFeedbackRecluster() {
			time.Sleep(s.feedbackReclusterPollIntervalValue())
			continue
		}

		s.feedbackMu.Lock()
		if !s.feedbackPending {
			s.feedbackRunning = false
			s.feedbackMu.Unlock()
			return
		}
		s.feedbackPending = false
		hook := s.feedbackReclusterHook
		s.feedbackMu.Unlock()

		startedAt := time.Now()
		var result model.ReclusterResult
		if hook != nil {
			result = hook()
		} else {
			result = s.triggerRecluster()
		}
		logger.Infof("feedback recluster complete: evaluated=%d reassigned=%d iterations=%d elapsed=%s",
			result.Evaluated, result.Reassigned, result.Iterations, time.Since(startedAt).Round(time.Millisecond))
	}
}

func (s *peopleService) shouldDelayFeedbackRecluster() bool {
	s.backgroundBusyMu.RLock()
	defer s.backgroundBusyMu.RUnlock()
	return s.backgroundBusy
}

func (s *peopleService) feedbackReclusterPollIntervalValue() time.Duration {
	s.feedbackMu.Lock()
	defer s.feedbackMu.Unlock()
	if s.feedbackPollInterval <= 0 {
		return peopleFeedbackPollInterval
	}
	return s.feedbackPollInterval
}

func (s *peopleService) setFeedbackReclusterHookForTest(hook func() model.ReclusterResult) {
	s.feedbackMu.Lock()
	defer s.feedbackMu.Unlock()
	s.feedbackReclusterHook = hook
}

func (s *peopleService) setFeedbackReclusterPollIntervalForTest(interval time.Duration) {
	s.feedbackMu.Lock()
	defer s.feedbackMu.Unlock()
	s.feedbackPollInterval = interval
}

func (s *peopleService) setBackgroundBusy(busy bool) {
	s.backgroundBusyMu.Lock()
	defer s.backgroundBusyMu.Unlock()
	s.backgroundBusy = busy
}

func (s *peopleService) generateFaceThumbnail(photo *model.Photo, bbox mlclient.BoundingBox) (string, error) {
	if photo == nil {
		return "", fmt.Errorf("photo is nil")
	}
	return util.GenerateFaceThumbnail(photo.FilePath, s.faceThumbnailRoot(), bbox.X, bbox.Y, bbox.Width, bbox.Height)
}

func (s *peopleService) faceThumbnailRoot() string {
	if s.config != nil && strings.TrimSpace(s.config.Photos.ThumbnailPath) != "" {
		return s.config.Photos.ThumbnailPath
	}
	return "./data/thumbnails"
}

func clonePeopleTask(task *model.PeopleTask) *model.PeopleTask {
	if task == nil {
		return nil
	}
	clone := *task
	return &clone
}

func (s *peopleService) ensurePersonForDetectedFace(detected mlclient.DetectedFace, candidates []*model.Face, people map[uint]*model.Person) (uint, error) {
	bestPersonID := uint(0)
	bestScore := -1.0

	for _, face := range candidates {
		if face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		score := cosineSimilarity(detected.Embedding, decodeEmbedding(face.Embedding))
		if score > bestScore {
			bestScore = score
			bestPersonID = *face.PersonID
		}
	}

	if bestPersonID != 0 && bestScore >= peopleClusterThreshold {
		if _, ok := people[bestPersonID]; ok {
			return bestPersonID, nil
		}
	}

	person := &model.Person{Category: model.PersonCategoryStranger}
	if err := s.personRepo.Create(person); err != nil {
		return 0, err
	}
	people[person.ID] = person
	return person.ID, nil
}

func (s *peopleService) selectPersonPrototypes(faces []*model.Face, k int) map[uint][]*model.Face {
	prototypes := make(map[uint][]*model.Face)
	if k <= 0 {
		return prototypes
	}

	grouped := make(map[uint][]*model.Face)
	for _, face := range faces {
		if face == nil || face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		personID := *face.PersonID
		grouped[personID] = append(grouped[personID], face)
	}

	for personID, personFaces := range grouped {
		prototypes[personID] = selectDiversePrototypes(personFaces, k)
	}

	return prototypes
}

// selectDiversePrototypes picks up to k faces maximizing embedding space coverage.
// Manual-locked faces are always included first (they are user-confirmed anchors).
// Remaining slots use farthest-first traversal for maximum diversity.
func selectDiversePrototypes(faces []*model.Face, k int) []*model.Face {
	if len(faces) == 0 {
		return faces
	}

	// Sort: manual-locked first, then quality descending for deterministic baseline
	sort.Slice(faces, func(i, j int) bool {
		if faces[i].ManualLocked != faces[j].ManualLocked {
			return faces[i].ManualLocked
		}
		if faces[i].QualityScore != faces[j].QualityScore {
			return faces[i].QualityScore > faces[j].QualityScore
		}
		return faces[i].ID < faces[j].ID
	})

	if len(faces) <= k {
		return faces
	}

	// Separate manual-locked and auto faces
	var locked, auto []*model.Face
	for _, f := range faces {
		if f.ManualLocked {
			locked = append(locked, f)
		} else {
			auto = append(auto, f)
		}
	}

	// Sort locked by quality descending for determinism
	sort.Slice(locked, func(i, j int) bool {
		if locked[i].QualityScore != locked[j].QualityScore {
			return locked[i].QualityScore > locked[j].QualityScore
		}
		return locked[i].ID < locked[j].ID
	})

	// Start with locked faces (up to k)
	selected := make([]*model.Face, 0, k)
	if len(locked) >= k {
		return locked[:k]
	}
	selected = append(selected, locked...)

	// Sort auto by quality descending
	sort.Slice(auto, func(i, j int) bool {
		if auto[i].QualityScore != auto[j].QualityScore {
			return auto[i].QualityScore > auto[j].QualityScore
		}
		return auto[i].ID < auto[j].ID
	})

	// If no selected yet, seed with highest quality auto face
	if len(selected) == 0 && len(auto) > 0 {
		selected = append(selected, auto[0])
		auto = auto[1:]
	}

	// Farthest-first: greedily pick the face most different from all selected
	selectedEmbeddings := make([][]float32, 0, k)
	for _, f := range selected {
		selectedEmbeddings = append(selectedEmbeddings, decodeEmbedding(f.Embedding))
	}

	for len(selected) < k && len(auto) > 0 {
		bestIdx := -1
		bestMinSim := float64(2) // start higher than any cosine similarity

		for i, candidate := range auto {
			candEmb := decodeEmbedding(candidate.Embedding)
			// Find minimum similarity to any already-selected prototype
			minSim := float64(2)
			for _, selEmb := range selectedEmbeddings {
				sim := cosineSimilarity(candEmb, selEmb)
				if sim < minSim {
					minSim = sim
				}
			}
			// Farthest-first: pick candidate with lowest min-similarity (most different)
			if bestIdx == -1 || minSim < bestMinSim {
				bestMinSim = minSim
				bestIdx = i
			}
		}

		if bestIdx < 0 {
			break
		}
		selected = append(selected, auto[bestIdx])
		selectedEmbeddings = append(selectedEmbeddings, decodeEmbedding(auto[bestIdx].Embedding))
		auto = append(auto[:bestIdx], auto[bestIdx+1:]...)
	}

	return selected
}

func (s *peopleService) buildFaceGraph(faces []*model.Face, linkThreshold float64) map[uint][]uint {
	graph := make(map[uint][]uint, len(faces))
	for _, face := range faces {
		if face == nil || face.ID == 0 {
			continue
		}
		graph[face.ID] = []uint{}
	}

	for i := 0; i < len(faces); i++ {
		if faces[i] == nil || faces[i].ID == 0 {
			continue
		}
		for j := i + 1; j < len(faces); j++ {
			if faces[j] == nil || faces[j].ID == 0 {
				continue
			}

			score := cosineSimilarity(decodeEmbedding(faces[i].Embedding), decodeEmbedding(faces[j].Embedding))
			if score < linkThreshold {
				continue
			}

			graph[faces[i].ID] = append(graph[faces[i].ID], faces[j].ID)
			graph[faces[j].ID] = append(graph[faces[j].ID], faces[i].ID)
		}
	}

	for faceID := range graph {
		sort.Slice(graph[faceID], func(i, j int) bool {
			return graph[faceID][i] < graph[faceID][j]
		})
	}

	return graph
}

func (s *peopleService) findConnectedComponents(graph map[uint][]uint) [][]uint {
	if len(graph) == 0 {
		return nil
	}

	nodeIDs := make([]uint, 0, len(graph))
	for faceID := range graph {
		nodeIDs = append(nodeIDs, faceID)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })

	visited := make(map[uint]bool, len(graph))
	components := make([][]uint, 0)

	for _, startID := range nodeIDs {
		if visited[startID] {
			continue
		}

		queue := []uint{startID}
		visited[startID] = true
		component := make([]uint, 0)

		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			component = append(component, current)

			for _, neighbor := range graph[current] {
				if visited[neighbor] {
					continue
				}
				visited[neighbor] = true
				queue = append(queue, neighbor)
			}
		}

		sort.Slice(component, func(i, j int) bool { return component[i] < component[j] })
		components = append(components, component)
	}

	return components
}

func (s *peopleService) scoreComponentAgainstPerson(component []*model.Face, prototypes []*model.Face) float64 {
	if len(component) == 0 || len(prototypes) == 0 {
		return -1
	}

	var total float64
	var scored int

	for _, face := range component {
		if face == nil {
			continue
		}

		bestScore := -1.0
		embedding := decodeEmbedding(face.Embedding)
		for _, prototype := range prototypes {
			if prototype == nil {
				continue
			}
			score := cosineSimilarity(embedding, decodeEmbedding(prototype.Embedding))
			if score > bestScore {
				bestScore = score
			}
		}

		if bestScore < 0 {
			continue
		}
		total += bestScore
		scored++
	}

	if scored == 0 {
		return -1
	}
	return total / float64(scored)
}

func (s *peopleService) attachComponentToExistingPerson(component []*model.Face, prototypes map[uint][]*model.Face, attachThreshold float64) (uint, float64, bool) {
	if len(component) == 0 || len(prototypes) == 0 {
		return 0, -1, false
	}

	// Build cannot-link blocked set: collect previous person IDs from component faces,
	// then look up which target persons are blocked via cannot-link constraints.
	blockedPersons := make(map[uint]bool)
	if s.cannotLinkRepo != nil {
		prevPersonIDs := make(map[uint]bool)
		for _, face := range component {
			if face != nil && face.PersonID != nil && *face.PersonID != 0 {
				prevPersonIDs[*face.PersonID] = true
			}
		}
		for pid := range prevPersonIDs {
			blocked, err := s.cannotLinkRepo.ListByPersonID(pid)
			if err == nil {
				for _, bid := range blocked {
					blockedPersons[bid] = true
				}
			}
		}
	}

	personIDs := make([]uint, 0, len(prototypes))
	for personID := range prototypes {
		personIDs = append(personIDs, personID)
	}
	sort.Slice(personIDs, func(i, j int) bool { return personIDs[i] < personIDs[j] })

	bestPersonID := uint(0)
	bestScore := -1.0
	for _, personID := range personIDs {
		if blockedPersons[personID] {
			continue
		}
		score := s.scoreComponentAgainstPerson(component, prototypes[personID])
		if score > bestScore {
			bestScore = score
			bestPersonID = personID
		}
	}

	if bestScore >= attachThreshold {
		return bestPersonID, bestScore, true
	}

	// Apply discount for confirmed persons (have manual-locked faces)
	if bestPersonID != 0 && bestScore >= attachThreshold-confirmedPersonDiscount {
		for _, proto := range prototypes[bestPersonID] {
			if proto != nil && proto.ManualLocked {
				return bestPersonID, bestScore, true
			}
		}
	}

	return 0, bestScore, false
}

func (s *peopleService) markComponentPending(component []*model.Face, score float64) error {
	ids := faceIDs(component)
	if len(ids) == 0 {
		return nil
	}

	now := time.Now()
	if err := s.db.Model(&model.Face{}).Where("id IN ?", ids).Updates(map[string]interface{}{
		"person_id":      nil,
		"cluster_status": model.FaceClusterStatusPending,
		"cluster_score":  score,
		"clustered_at":   &now,
	}).Error; err != nil {
		return err
	}

	for _, face := range component {
		if face == nil {
			continue
		}
		face.PersonID = nil
		face.ClusterStatus = model.FaceClusterStatusPending
		face.ClusterScore = score
		face.ClusteredAt = &now
	}

	return nil
}

func (s *peopleService) createPersonFromComponent(component []*model.Face, score float64) (*model.Person, error) {
	ids := faceIDs(component)
	if len(ids) == 0 {
		return nil, fmt.Errorf("component is empty")
	}

	now := time.Now()
	person := &model.Person{Category: model.PersonCategoryStranger}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(person).Error; err != nil {
			return err
		}
		return tx.Model(&model.Face{}).Where("id IN ?", ids).Updates(map[string]interface{}{
			"person_id":      person.ID,
			"cluster_status": model.FaceClusterStatusAssigned,
			"cluster_score":  score,
			"clustered_at":   &now,
		}).Error
	}); err != nil {
		return nil, err
	}

	personID := person.ID
	for _, face := range component {
		if face == nil {
			continue
		}
		face.PersonID = &personID
		face.ClusterStatus = model.FaceClusterStatusAssigned
		face.ClusterScore = score
		face.ClusteredAt = &now
	}

	if err := s.syncPersonState(person.ID); err != nil {
		return nil, err
	}
	return s.personRepo.GetByID(person.ID)
}

func (s *peopleService) runIncrementalClustering() ([]uint, []uint, error) {
	pendingFaces, err := s.faceRepo.ListPending(0)
	if err != nil {
		return nil, nil, err
	}
	if len(pendingFaces) == 0 {
		return nil, nil, nil
	}

	assignedPersonIDs, err := s.faceRepo.ListAssignedPersonIDs()
	if err != nil {
		return nil, nil, err
	}
	var protoFaces []*model.Face
	if len(assignedPersonIDs) > 0 {
		protoFaces, err = s.faceRepo.ListTopByPersonIDs(assignedPersonIDs, peoplePrototypeCandidates)
		if err != nil {
			return nil, nil, err
		}
	}
	prototypes := s.selectPersonPrototypes(protoFaces, peoplePrototypeCount)

	graph := s.buildFaceGraph(pendingFaces, peopleLinkThreshold)
	components := s.findConnectedComponents(graph)
	pendingByID := make(map[uint]*model.Face, len(pendingFaces))
	for _, face := range pendingFaces {
		if face == nil || face.ID == 0 {
			continue
		}
		pendingByID[face.ID] = face
	}

	affectedPersonIDs := make(map[uint]struct{})
	affectedPhotoIDs := make(map[uint]struct{})

	for _, componentIDs := range components {
		component := make([]*model.Face, 0, len(componentIDs))
		for _, faceID := range componentIDs {
			face, ok := pendingByID[faceID]
			if !ok {
				continue
			}
			component = append(component, face)
		}
		if len(component) == 0 {
			continue
		}

		personID, score, attached := s.attachComponentToExistingPerson(component, prototypes, peopleAttachThreshold)
		componentScore := nonNegativeScore(score)

		if attached {
			now := time.Now()
			if err := s.faceRepo.UpdateClusterFields(faceIDs(component), map[string]interface{}{
				"person_id":      personID,
				"cluster_status": model.FaceClusterStatusAssigned,
				"cluster_score":  componentScore,
				"clustered_at":   &now,
			}); err != nil {
				return nil, nil, err
			}
			for _, face := range component {
				if face == nil {
					continue
				}
				face.PersonID = &personID
				face.ClusterStatus = model.FaceClusterStatusAssigned
				face.ClusterScore = componentScore
				face.ClusteredAt = &now
			}
			affectedPersonIDs[personID] = struct{}{}
			for _, photoID := range facePhotoIDs(component) {
				affectedPhotoIDs[photoID] = struct{}{}
			}
			continue
		}

		if len(component) >= peopleMinClusterFaces && componentPhotoCount(component) >= 2 {
			person, err := s.createPersonFromComponent(component, componentScore)
			if err != nil {
				return nil, nil, err
			}
			if person != nil && person.ID != 0 {
				affectedPersonIDs[person.ID] = struct{}{}
			}
			for _, photoID := range facePhotoIDs(component) {
				affectedPhotoIDs[photoID] = struct{}{}
			}
			continue
		}

		if err := s.markComponentPending(component, componentScore); err != nil {
			return nil, nil, err
		}
		for _, photoID := range facePhotoIDs(component) {
			affectedPhotoIDs[photoID] = struct{}{}
		}
	}

	return mapKeys(affectedPersonIDs), mapKeys(affectedPhotoIDs), nil
}

// triggerRecluster re-evaluates low-confidence face assignments using current prototypes.
// Called after manual corrections (merge/split/move) to propagate user feedback.
func (s *peopleService) triggerRecluster() model.ReclusterResult {
	threshold := s.config.People.ReclusterThreshold
	if threshold <= 0 {
		threshold = 0.55
	}
	maxIter := s.config.People.ReclusterMaxIter
	if maxIter <= 0 {
		maxIter = 3
	}

	result := model.ReclusterResult{}

	for iter := 0; iter < maxIter; iter++ {
		candidates, err := s.faceRepo.ListLowConfidence(threshold, maxIter)
		if err != nil {
			logger.Warnf("recluster: failed to list low confidence faces: %v", err)
			break
		}
		if len(candidates) == 0 {
			break
		}

		result.Evaluated += len(candidates)
		result.Iterations = iter + 1

		// Record current assignments for change detection
		prevAssign := make(map[uint]uint, len(candidates))
		candidateIDs := make([]uint, 0, len(candidates))
		for _, f := range candidates {
			candidateIDs = append(candidateIDs, f.ID)
			if f.PersonID != nil {
				prevAssign[f.ID] = *f.PersonID
			}
		}

		// Reset to pending for re-clustering
		if err := s.faceRepo.ResetForRecluster(candidateIDs); err != nil {
			logger.Warnf("recluster: failed to reset faces: %v", err)
			break
		}

		// Re-run incremental clustering with updated prototypes
		affectedPersonIDs, affectedPhotoIDs, err := s.runIncrementalClustering()
		if err != nil {
			logger.Warnf("recluster: clustering failed: %v", err)
			break
		}

		// Sync affected persons and photos
		for _, pid := range affectedPersonIDs {
			_ = s.syncPersonState(pid)
		}
		if len(affectedPhotoIDs) > 0 {
			_ = s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs)
		}
		// Also sync persons that lost faces
		for _, oldPID := range prevAssign {
			_ = s.syncPersonState(oldPID)
		}

		// Count actual reassignments
		reassigned := 0
		for _, fid := range candidateIDs {
			updated, err := s.faceRepo.GetByID(fid)
			if err != nil {
				continue
			}
			oldPID := prevAssign[fid]
			newPID := uint(0)
			if updated.PersonID != nil {
				newPID = *updated.PersonID
			}
			if oldPID != newPID {
				reassigned++
			}
		}
		result.Reassigned += reassigned

		if reassigned == 0 {
			break // converged
		}
	}

	return result
}

func (s *peopleService) syncPersonState(personID uint) error {
	person, err := s.personRepo.GetByID(personID)
	if err != nil {
		return err
	}
	if person == nil {
		return nil
	}

	faces, err := s.faceRepo.ListByPersonID(personID)
	if err != nil {
		return err
	}
	if len(faces) == 0 {
		_ = s.cannotLinkRepo.DeleteByPersonID(personID)
		return s.personRepo.Delete(personID)
	}

	if err := s.personRepo.RefreshStats(personID); err != nil {
		return err
	}

	if person.AvatarLocked && person.RepresentativeFaceID != nil {
		for _, face := range faces {
			if face.ID == *person.RepresentativeFaceID {
				return nil
			}
		}
		person.AvatarLocked = false
	}

	bestFace := faces[0]
	for _, face := range faces[1:] {
		if face.QualityScore > bestFace.QualityScore {
			bestFace = face
			continue
		}
		if face.QualityScore == bestFace.QualityScore && face.Confidence > bestFace.Confidence {
			bestFace = face
		}
	}

	updates := map[string]interface{}{
		"representative_face_id": bestFace.ID,
	}
	if !person.AvatarLocked {
		updates["avatar_locked"] = false
	}
	return s.personRepo.UpdateFields(personID, updates)
}

func facePhotoIDs(faces []*model.Face) []uint {
	seen := make(map[uint]struct{})
	photoIDs := make([]uint, 0, len(faces))
	for _, face := range faces {
		if face == nil || face.PhotoID == 0 {
			continue
		}
		if _, ok := seen[face.PhotoID]; ok {
			continue
		}
		seen[face.PhotoID] = struct{}{}
		photoIDs = append(photoIDs, face.PhotoID)
	}
	return photoIDs
}

func componentPhotoCount(component []*model.Face) int {
	return len(facePhotoIDs(component))
}

func faceIDs(faces []*model.Face) []uint {
	seen := make(map[uint]struct{})
	ids := make([]uint, 0, len(faces))
	for _, face := range faces {
		if face == nil || face.ID == 0 {
			continue
		}
		if _, ok := seen[face.ID]; ok {
			continue
		}
		seen[face.ID] = struct{}{}
		ids = append(ids, face.ID)
	}
	return ids
}

func mapKeys(values map[uint]struct{}) []uint {
	keys := make([]uint, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func nonNegativeScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	return score
}

func hasManualLockedFaces(faces []*model.Face) bool {
	for _, face := range faces {
		if face != nil && face.ManualLocked {
			return true
		}
	}
	return false
}

func filterFacesByOtherPhotos(faces []*model.Face, photoID uint) []*model.Face {
	filtered := make([]*model.Face, 0, len(faces))
	for _, face := range faces {
		if face == nil || face.PhotoID == photoID {
			continue
		}
		filtered = append(filtered, face)
	}
	return filtered
}

func personIDsFromFaces(faces []*model.Face) []uint {
	seen := make(map[uint]struct{})
	personIDs := make([]uint, 0, len(faces))
	for _, face := range faces {
		if face == nil || face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		personID := *face.PersonID
		if _, ok := seen[personID]; ok {
			continue
		}
		seen[personID] = struct{}{}
		personIDs = append(personIDs, personID)
	}
	return personIDs
}

func decodeEmbedding(payload []byte) []float32 {
	if len(payload) == 0 {
		return nil
	}
	var embedding []float32
	if err := json.Unmarshal(payload, &embedding); err != nil {
		return nil
	}
	return embedding
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return -1
	}

	var dot float64
	var normA float64
	var normB float64
	for i := range a {
		af := float64(a[i])
		bf := float64(b[i])
		dot += af * bf
		normA += af * af
		normB += bf * bf
	}
	if normA == 0 || normB == 0 {
		return -1
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
