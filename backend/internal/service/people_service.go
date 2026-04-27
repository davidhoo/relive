package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/viterin/vek/vek32"
	"gorm.io/gorm"
)

const (
	peoplePriorityScan         = 50
	peoplePriorityManual       = 80
	peoplePriorityPassive      = 100
	peopleClusterThreshold     = 0.45
	peoplePrototypeCount       = 5
	peoplePrototypeCandidates  = 10
	defaultLinkThreshold       = 0.62
	defaultAttachThreshold     = 0.65
	peopleMinClusterFaces      = 2
	peopleFeedbackPollInterval = 250 * time.Millisecond
	peopleStatsCacheTTL        = 30 * time.Second

	// confirmedPersonDiscount lowers the attach threshold for persons with manual-locked faces,
	// making it easier for new faces to join user-confirmed identities (e.g., family members).
	confirmedPersonDiscount = 0.05

	// Adaptive threshold decay for long-pending faces
	// retry_count 0-1: full threshold (no discount)
	// retry_count 2-4: linear decay to floor
	// retry_count 5+: floor threshold
	thresholdDecayStart    = 2  // retry_count at which decay begins
	thresholdDecayEnd      = 5  // retry_count at which floor is reached
	attachThresholdFloor   = 0.5
	linkThresholdFloor     = 0.5

	// Fallback: after this many retries, allow single-face person creation
	singleFaceFallbackRetries = 10

	// Clustering optimization constants to prevent CPU overload on NAS
	// See: https://github.com/davidhoo/relive/issues/XXX
	peopleClusteringBatchSize    = 50  // Max pending faces to cluster at once
	peopleClusteringTaskInterval = 5   // Cluster every N tasks (0 = always)
	clustProtoCacheTTL           = 5 * time.Minute // Prototype cache TTL; avoids reloading 220K rows per 50-face batch
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
	EnqueueUnprocessed() (int, error)
	ResetAllPeople() (int, error)
	MergePeople(targetPersonID uint, sourcePersonIDs []uint) (*model.ReclusterResult, error)
	MergePeopleAsync(targetPersonID uint, sourcePersonIDs []uint, jobType string) (uint, error)
	GetMergeJobStatus(jobID uint) (*model.PeopleMergeJob, error)
	SplitPerson(faceIDs []uint) (*model.Person, *model.ReclusterResult, error)
	MoveFaces(faceIDs []uint, targetPersonID uint) (*model.ReclusterResult, error)
	UpdatePersonCategory(personID uint, category string) error
	UpdatePersonName(personID uint, name string) error
	UpdatePersonAvatar(personID uint, faceID uint) error
	DissolvePerson(personID uint) (int, error)
	HandleShutdown() error
	ApplyDetectionResult(job *model.PeopleJob, photo *model.Photo, result *model.PeopleDetectionResult) error
}

type peopleService struct {
	db               *gorm.DB
	photoRepo        repository.PhotoRepository
	faceRepo         repository.FaceRepository
	personRepo       repository.PersonRepository
	jobRepo          repository.PeopleJobRepository
	mergeJobRepo     repository.PeopleMergeJobRepository
	cannotLinkRepo   repository.CannotLinkRepository
	config           *config.Config
	client           PeopleMLClient
	runtimeService   AnalysisRuntimeService

	taskMutex        sync.RWMutex
	task             *model.PeopleTask
	active           *activePeopleTask
	backgroundLogMu  sync.RWMutex
	backgroundLogs   []string
	backgroundBusyMu sync.RWMutex
	backgroundBusy   bool

	// writeGate serializes foreground mutations (merge/split/move) with background
	// clustering writes. Foreground ops take Lock (exclusive); worker takes RLock (shared).
	// This prevents SQLite "database is locked" when both paths write faces/people tables.
	writeGate  sync.RWMutex
	idleCount  int // consecutive idle loops, used for polling backoff

	feedbackMu            sync.Mutex
	feedbackRunning       bool
	feedbackPending       bool
	feedbackShutdown      bool
	feedbackStopCh        chan struct{}
	feedbackPollInterval  time.Duration
	feedbackReclusterHook func() model.ReclusterResult
	mergeSuggestionDirty  func(string) error

	// Clustering rate limiting to prevent CPU overload
	clusteringTaskCounter   int
	clusteringTaskCounterMu sync.Mutex

	// Prototype cache: avoids reloading all 22K person prototypes on every 50-face clustering batch.
	// Only accessed from the single background clustering goroutine; no locking required.
	protoCache *clustProtoCache

	// annCandidateFn, when set, pre-filters the O(N) attach scan to ~O(50) ANN candidates.
	// Injected from personMergeSuggestionService which already maintains the HNSW index.
	annCandidateFn func(probes []faceWithEmbedding, k int) map[uint]struct{}

	// In-memory cache for GetStats() to avoid scanning 78K+ faces rows on every poll.
	statsCache    *model.PeopleStatsResponse
	statsCacheAt  time.Time
	statsCacheMu  sync.RWMutex
}

type clustProtoCache struct {
	prototypesWithEmb map[uint][]faceWithEmbedding
	prototypesOrig    map[uint][]*model.Face
	builtAt           time.Time
}

type activePeopleTask struct {
	stopCh chan struct{}
	done   chan struct{}
	mu     sync.Mutex
	stop   bool
}

func NewPeopleService(db *gorm.DB, photoRepo repository.PhotoRepository, faceRepo repository.FaceRepository, personRepo repository.PersonRepository, jobRepo repository.PeopleJobRepository, mergeJobRepo repository.PeopleMergeJobRepository, cannotLinkRepo repository.CannotLinkRepository, cfg *config.Config, client PeopleMLClient, runtimeService AnalysisRuntimeService) PeopleService {
	// 清理上次异常退出遗留的非终态任务
	if err := jobRepo.InterruptNonTerminal("task interrupted because service restarted"); err != nil {
		logger.Errorf("Failed to interrupt non-terminal people jobs: %v", err)
	}

	// 清理上次异常退出遗留的合并任务（processing/pending → failed）
	recoverStuckMergeJobs(mergeJobRepo)

	// 重置被中断任务遗留的 stuck 照片状态（pending/processing → none）
	if err := db.Model(&model.Photo{}).
		Where("face_process_status IN ?", []string{model.FaceProcessStatusPending, model.FaceProcessStatusProcessing}).
		Update("face_process_status", model.FaceProcessStatusNone).Error; err != nil {
		logger.Errorf("Failed to reset stuck photo face_process_status on startup: %v", err)
	}

	return &peopleService{
		db:                   db,
		photoRepo:            photoRepo,
		faceRepo:             faceRepo,
		personRepo:           personRepo,
		jobRepo:              jobRepo,
		mergeJobRepo:         mergeJobRepo,
		cannotLinkRepo:       cannotLinkRepo,
		config:               cfg,
		client:               client,
		runtimeService:       runtimeService,
		feedbackStopCh:       make(chan struct{}),
		feedbackPollInterval: peopleFeedbackPollInterval,
	}
}

// linkThreshold returns the configured face graph link threshold, defaulting to 0.65.
func (s *peopleService) linkThreshold() float64 {
	if s.config != nil && s.config.People.LinkThreshold > 0 {
		return s.config.People.LinkThreshold
	}
	return defaultLinkThreshold
}

// attachThreshold returns the configured person attach threshold, defaulting to 0.65.
func (s *peopleService) attachThreshold() float64 {
	if s.config != nil && s.config.People.AttachThreshold > 0 {
		return s.config.People.AttachThreshold
	}
	return defaultAttachThreshold
}

// effectiveAttachThreshold decays the attach threshold based on max retry_count in a component.
// retry 0-1: full threshold, retry 2-4: linear decay, retry 5+: floor 0.5
func (s *peopleService) effectiveAttachThreshold(maxRetryCount int) float64 {
	base := s.attachThreshold()
	if maxRetryCount < thresholdDecayStart {
		return base
	}
	if maxRetryCount >= thresholdDecayEnd {
		return attachThresholdFloor
	}
	fraction := float64(maxRetryCount-thresholdDecayStart+1) / float64(thresholdDecayEnd-thresholdDecayStart+1)
	return base - (base-attachThresholdFloor)*fraction
}

// effectiveLinkThreshold decays the link threshold based on retry_count.
// retry 0-1: full threshold, retry 2-4: linear decay, retry 5+: floor 0.5
func (s *peopleService) effectiveLinkThreshold(retryCount int) float64 {
	base := s.linkThreshold()
	if retryCount < thresholdDecayStart {
		return base
	}
	if retryCount >= thresholdDecayEnd {
		return linkThresholdFloor
	}
	fraction := float64(retryCount-thresholdDecayStart+1) / float64(thresholdDecayEnd-thresholdDecayStart+1)
	return base - (base-linkThresholdFloor)*fraction
}

func (s *peopleService) StartBackground() (*model.PeopleTask, error) {
	if s.client == nil {
		return nil, fmt.Errorf("people ml client not configured")
	}

	// Acquire people runtime lease
	if s.runtimeService != nil {
		lease, err := s.runtimeService.Acquire(model.GlobalPeopleResourceKey, model.AnalysisOwnerTypeBackground, "local", "local background task")
		if err != nil {
			if err == ErrAnalysisRuntimeBusy {
				return nil, fmt.Errorf("people runtime busy (owned by %s/%s)", lease.OwnerType, lease.OwnerID)
			}
			return nil, fmt.Errorf("failed to acquire people runtime lease: %w", err)
		}
	}

	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.active != nil {
		// Release lease since task is already running
		if s.runtimeService != nil {
			s.runtimeService.Release(model.GlobalPeopleResourceKey, model.AnalysisOwnerTypeBackground, "local")
		}
		return nil, fmt.Errorf("people task already running")
	}

	now := time.Now()
	task := &model.PeopleTask{
		Status:         model.TaskStatusRunning,
		CurrentPhase:   "idle",
		CurrentMessage: "等待任务入队",
		StartedAt:      &now,
	}
	active := &activePeopleTask{
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	s.task = task
	s.active = active
	s.resetBackgroundLogs()
	s.appendBackgroundLog("人物后台任务已启动")
	s.invalidateStatsCache()
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
	if s.task != nil && (s.task.Status == model.TaskStatusRunning || s.task.Status == model.TaskStatusIdle) {
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
	s.statsCacheMu.RLock()
	if s.statsCache != nil && time.Since(s.statsCacheAt) < peopleStatsCacheTTL {
		cached := *s.statsCache
		s.statsCacheMu.RUnlock()
		return &cached, nil
	}
	s.statsCacheMu.RUnlock()

	stats, err := s.jobRepo.GetStats()
	if err != nil {
		return nil, err
	}
	pendingFaceStats, err := s.faceRepo.GetPendingStats()
	if err != nil {
		return nil, err
	}
	result := &model.PeopleStatsResponse{
		Total:                      stats.Total,
		Pending:                    stats.Pending,
		Queued:                     stats.Queued,
		Processing:                 stats.Processing,
		Completed:                  stats.Completed,
		Failed:                     stats.Failed,
		Cancelled:                  stats.Cancelled,
		PendingFacesTotal:          pendingFaceStats.Total,
		PendingFacesNeverClustered: pendingFaceStats.NeverClustered,
		PendingFacesRetried:        pendingFaceStats.Retried,
	}

	s.statsCacheMu.Lock()
	s.statsCache = result
	s.statsCacheAt = time.Now()
	s.statsCacheMu.Unlock()

	return &(*result), nil
}

func (s *peopleService) invalidateStatsCache() {
	s.statsCacheMu.Lock()
	s.statsCache = nil
	s.statsCacheMu.Unlock()
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

func (s *peopleService) EnqueueUnprocessed() (int, error) {
	photos, err := s.photoRepo.ListByFaceStatus(model.FaceProcessStatusNone)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, photo := range photos {
		if err := s.enqueuePhotoModel(photo, model.PeopleJobSourceManual, peoplePriorityManual, false); err != nil {
			logger.Warnf("enqueue unprocessed people failed for photo %d: %v", photo.ID, err)
			continue
		}
		count++
	}

	return count, nil
}

func (s *peopleService) HandleShutdown() error {
	s.stopFeedbackRecluster()

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

	enqueued := 0
	err = s.photoRepo.IterateActivePhotos([]string{"id", "status"}, 500, func(photos []*model.Photo) error {
		for _, photo := range photos {
			if err := s.enqueuePhotoModel(photo, "reset", peoplePriorityScan, true); err != nil {
				logger.Warnf("re-enqueue photo %d after reset failed: %v", photo.ID, err)
				continue
			}
			enqueued++
		}
		return nil
	})
	if err != nil {
		return count, fmt.Errorf("iterate photos for re-enqueue: %w", err)
	}
	logger.Infof("people reset complete: %d photos reset, %d jobs enqueued", count, enqueued)
	return enqueued, nil
}

func (s *peopleService) MergePeople(targetPersonID uint, sourcePersonIDs []uint) (*model.ReclusterResult, error) {
	s.writeGate.Lock()
	defer s.writeGate.Unlock()

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
	s.markMergeSuggestionsDirty("merge_people")
	s.scheduleFeedbackRecluster()
	return &model.ReclusterResult{}, nil
}

// MergePeopleAsync 异步创建人物合并任务，避免同步执行超时
func (s *peopleService) MergePeopleAsync(targetPersonID uint, sourcePersonIDs []uint, jobType string) (uint, error) {
	if len(sourcePersonIDs) == 0 {
		return 0, fmt.Errorf("no source persons to merge")
	}

	// 验证目标人物存在
	targetPerson, err := s.personRepo.GetByID(targetPersonID)
	if err != nil {
		return 0, fmt.Errorf("failed to get target person: %w", err)
	}
	if targetPerson == nil {
		return 0, fmt.Errorf("target person not found")
	}

	// 验证源人物存在
	for _, sourceID := range sourcePersonIDs {
		if sourceID == targetPersonID {
			return 0, fmt.Errorf("cannot merge person into itself")
		}
		sourcePerson, err := s.personRepo.GetByID(sourceID)
		if err != nil {
			return 0, fmt.Errorf("failed to get source person %d: %w", sourceID, err)
		}
		if sourcePerson == nil {
			return 0, fmt.Errorf("source person %d not found", sourceID)
		}
	}

	// 序列化源人物 ID 列表
	sourceIDsJSON, err := json.Marshal(sourcePersonIDs)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal source IDs: %w", err)
	}

	job := &model.PeopleMergeJob{
		Type:      jobType,
		Status:    model.PeopleMergeJobStatusPending,
		TargetID:  targetPersonID,
		SourceIDs: string(sourceIDsJSON),
	}

	if err := s.mergeJobRepo.Create(job); err != nil {
		return 0, fmt.Errorf("failed to create merge job: %w", err)
	}

	logger.Infof("Created people merge job %d: type=%s, target=%d, sources=%v", job.ID, jobType, targetPersonID, sourcePersonIDs)

	// 立即启动后台执行（也可以让调度器轮询执行）
	go s.executeMergeJob(job.ID)

	return job.ID, nil
}

// executeMergeJob 执行合并任务
func (s *peopleService) executeMergeJob(jobID uint) {
	job, err := s.mergeJobRepo.GetByID(jobID)
	if err != nil {
		logger.Errorf("Failed to get merge job %d: %v", jobID, err)
		return
	}

	// 更新状态为处理中
	now := time.Now()
	job.StartedAt = &now
	job.Status = model.PeopleMergeJobStatusProcessing
	if err := s.db.Model(job).Updates(map[string]interface{}{
		"status":      job.Status,
		"started_at":  job.StartedAt,
	}).Error; err != nil {
		logger.Errorf("Failed to update merge job %d status: %v", jobID, err)
		return
	}

	// 解析源人物 ID 列表
	var sourcePersonIDs []uint
	if err := json.Unmarshal([]byte(job.SourceIDs), &sourcePersonIDs); err != nil {
		logger.Errorf("Failed to unmarshal source IDs for job %d: %v", jobID, err)
		s.mergeJobRepo.Fail(jobID, fmt.Sprintf("invalid source IDs: %v", err))
		return
	}

	// 执行同步合并
	result, err := s.MergePeople(job.TargetID, sourcePersonIDs)
	if err != nil {
		logger.Errorf("Merge job %d failed: %v", jobID, err)
		s.mergeJobRepo.Fail(jobID, err.Error())
		return
	}

	// 序列化结果
	resultJSON, _ := json.Marshal(&model.PeopleMergeJobResult{
		AffectedPhotoCount: len(sourcePersonIDs), // 简化计数
		ReclusterResult:    result,
	})

	if err := s.mergeJobRepo.Complete(jobID, string(resultJSON)); err != nil {
		logger.Errorf("Failed to complete merge job %d: %v", jobID, err)
		return
	}

	logger.Infof("Merge job %d completed successfully", jobID)
}

// GetMergeJobStatus 获取合并任务状态
func (s *peopleService) GetMergeJobStatus(jobID uint) (*model.PeopleMergeJob, error) {
	return s.mergeJobRepo.GetByID(jobID)
}

func (s *peopleService) SplitPerson(faceIDs []uint) (*model.Person, *model.ReclusterResult, error) {
	s.writeGate.Lock()
	defer s.writeGate.Unlock()

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
	s.markMergeSuggestionsDirty("split_person")
	s.scheduleFeedbackRecluster()
	return person, &model.ReclusterResult{}, nil
}

func (s *peopleService) MoveFaces(faceIDs []uint, targetPersonID uint) (*model.ReclusterResult, error) {
	s.writeGate.Lock()
	defer s.writeGate.Unlock()

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
	s.markMergeSuggestionsDirty("move_faces")
	s.scheduleFeedbackRecluster()
	return &model.ReclusterResult{}, nil
}

func (s *peopleService) UpdatePersonCategory(personID uint, category string) error {
	if err := s.personRepo.UpdateFields(personID, map[string]interface{}{"category": category}); err != nil {
		return err
	}
	faces, err := s.faceRepo.ListByPersonIDSummary(personID)
	if err != nil {
		return err
	}
	if err := s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces)); err != nil {
		return err
	}
	s.markMergeSuggestionsDirty("update_person_category")
	return nil
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

// DissolvePerson 解散指定人物：将其所有人脸（含人工确认）打回 pending，删除人物记录和约束。
// 返回被释放的人脸数量。不触发自动重聚类，由后台任务自然处理。
func (s *peopleService) DissolvePerson(personID uint) (int, error) {
	s.writeGate.Lock()
	defer s.writeGate.Unlock()

	person, err := s.personRepo.GetByID(personID)
	if err != nil {
		return 0, err
	}
	if person == nil {
		return 0, fmt.Errorf("person %d not found", personID)
	}

	faces, err := s.faceRepo.ListByPersonIDSummary(personID)
	if err != nil {
		return 0, fmt.Errorf("list faces for person %d: %w", personID, err)
	}

	if len(faces) > 0 {
		faceIDs := make([]uint, len(faces))
		photoIDs := make(map[uint]bool)
		for i, f := range faces {
			faceIDs[i] = f.ID
			photoIDs[f.PhotoID] = true
		}

		// 强制重置所有人脸（含 manual_locked）回 pending 状态
		if err := s.faceRepo.UpdateClusterFields(faceIDs, map[string]interface{}{
			"person_id":            nil,
			"cluster_status":       model.FaceClusterStatusPending,
			"cluster_score":        0,
			"manual_locked":        false,
			"manual_lock_reason":   "",
			"manual_locked_at":     nil,
			"recluster_generation": 0,
		}); err != nil {
			return 0, fmt.Errorf("reset faces for person %d: %w", personID, err)
		}

		// 更新受影响照片的 top_person_category
		affectedPhotoIDs := make([]uint, 0, len(photoIDs))
		for pid := range photoIDs {
			affectedPhotoIDs = append(affectedPhotoIDs, pid)
		}
		if err := s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs); err != nil {
			logger.Warnf("recompute top person category after dissolve person %d: %v", personID, err)
		}
	}

	// 删除 cannot-link 约束
	if err := s.cannotLinkRepo.DeleteByPersonID(personID); err != nil {
		logger.Warnf("delete cannot-link for dissolved person %d: %v", personID, err)
	}

	// 删除人物记录
	if err := s.personRepo.Delete(personID); err != nil {
		return 0, fmt.Errorf("delete person %d: %w", personID, err)
	}

	// 异步触发重聚类，让 pending 人脸被重新分配
	s.scheduleFeedbackRecluster()

	return len(faces), nil
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
	// Heartbeat ticker for runtime lease
	var heartbeatTicker *time.Ticker
	var heartbeatStopCh chan struct{}
	if s.runtimeService != nil {
		heartbeatTicker = time.NewTicker(10 * time.Second)
		heartbeatStopCh = make(chan struct{})
		go func() {
			for {
				select {
				case <-heartbeatTicker.C:
					s.runtimeService.Heartbeat(model.GlobalPeopleResourceKey, model.AnalysisOwnerTypeBackground, "local")
				case <-heartbeatStopCh:
					return
				}
			}
		}()
	}

	defer func() {
		// Stop heartbeat goroutine
		if heartbeatTicker != nil {
			heartbeatTicker.Stop()
			close(heartbeatStopCh)
		}
		// Release runtime lease
		if s.runtimeService != nil {
			s.runtimeService.Release(model.GlobalPeopleResourceKey, model.AnalysisOwnerTypeBackground, "local")
		}

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
			s.idleCount = 0
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if job != nil {
			s.idleCount = 0
			s.setTaskState(model.TaskStatusRunning, "detecting",
				fmt.Sprintf("正在处理照片 #%d", job.PhotoID), &job.PhotoID)
			s.setBackgroundBusy(true)
			s.writeGate.RLock()
			err = s.processJob(job)
			s.writeGate.RUnlock()
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
			continue
		}

		// No detection job — check for pending faces and cluster
		pendingFaceStats, statsErr := s.faceRepo.GetPendingStats()
		if statsErr != nil {
			s.appendBackgroundLog(fmt.Sprintf("查询待聚类人脸失败：%v", statsErr))
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if pendingFaceStats.Total == 0 {
			s.setTaskState(model.TaskStatusIdle, "idle", "队列已清空，等待新任务入队", nil)
			s.idleCount++
			time.Sleep(s.idleInterval())
			continue
		}

		// Cluster pending faces — loop directly without re-checking ClaimNextJob/GetPendingStats
		// until no more work, then return to normal cycle.
		s.idleCount = 0
		s.setTaskState(model.TaskStatusRunning, "clustering",
			fmt.Sprintf("正在处理 %d 张待聚类人脸", pendingFaceStats.Total), nil)
		for {
			active.mu.Lock()
			stopRequested := active.stop
			active.mu.Unlock()
			if stopRequested {
				return
			}

			s.setBackgroundBusy(true)
			s.writeGate.RLock()
			hasMore, clusterErr := s.processPendingFaces()
			s.writeGate.RUnlock()
			s.setBackgroundBusy(false)
			if clusterErr != nil {
				s.appendBackgroundLog(fmt.Sprintf("处理待聚类人脸失败：%v", clusterErr))
				time.Sleep(300 * time.Millisecond)
				break
			}
			if !hasMore {
				break
			}
			time.Sleep(s.clusteringInterval())
		}
	}

}
func (s *peopleService) processJob(job *model.PeopleJob) error {
	photo, skip, err := s.preflightCheck(job)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	result, err := s.detectFacesLocally(photo)
	if err != nil {
		return err
	}

	// Convert mlclient result to model result
	detectionResult := convertMLResultToModel(result)
	return s.ApplyDetectionResult(job, photo, detectionResult)
}

func (s *peopleService) processPendingFaces() (bool, error) {
	affectedPersonIDs, affectedPhotoIDs, err := s.runIncrementalClustering()
	if err != nil {
		return false, err
	}

	hasMore := len(affectedPersonIDs) > 0 || len(affectedPhotoIDs) > 0

	for _, personID := range affectedPersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return false, err
		}
	}
	if len(affectedPhotoIDs) > 0 {
		if err := s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs); err != nil {
			return false, err
		}
	}
	return hasMore, nil
}

// preflightCheck performs pre-flight checks and returns the photo, whether to skip, and any error.
func (s *peopleService) preflightCheck(job *model.PeopleJob) (*model.Photo, bool, error) {
	photo, err := s.photoRepo.GetByID(job.PhotoID)
	if err != nil {
		return nil, false, err
	}

	now := time.Now()
	if photo == nil || photo.Status == model.PhotoStatusExcluded {
		s.appendBackgroundLog(fmt.Sprintf("照片 #%d 已排除，跳过", job.PhotoID))
		return nil, true, s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusCancelled,
			"completed_at": &now,
		})
	}

	existingFaces, err := s.faceRepo.ListByPhotoID(photo.ID)
	if err != nil {
		return nil, false, err
	}
	if hasManualLockedFaces(existingFaces) {
		s.appendBackgroundLog(fmt.Sprintf("照片 #%d 已有人工确认，跳过", photo.ID))
		if err := s.photoRepo.RecomputeTopPersonCategory([]uint{photo.ID}); err != nil {
			return nil, false, err
		}
		return nil, true, s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusCompleted,
			"last_error":   "",
			"completed_at": &now,
		})
	}

	if err := s.photoRepo.UpdateFields(photo.ID, map[string]interface{}{
		"face_process_status": model.FaceProcessStatusProcessing,
	}); err != nil {
		return nil, false, err
	}

	return photo, false, nil
}

// detectFacesLocally performs face detection using the local ML client.
func (s *peopleService) detectFacesLocally(photo *model.Photo) (*mlclient.DetectFacesResponse, error) {
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
		return nil, detectErr
	}

	if result == nil {
		result = &mlclient.DetectFacesResponse{}
	}

	return result, nil
}

// ApplyDetectionResult applies detection results: deletes old faces, creates new ones, runs clustering.
// This method is used by both local processing and remote worker submission.
func (s *peopleService) ApplyDetectionResult(job *model.PeopleJob, photo *model.Photo, result *model.PeopleDetectionResult) error {
	now := time.Now()

	existingFaces, err := s.faceRepo.ListByPhotoID(photo.ID)
	if err != nil {
		return err
	}
	previousPersonIDs := personIDsFromFaces(existingFaces)

	if len(result.Faces) == 0 {
		s.appendBackgroundLog(fmt.Sprintf("照片 #%d 无人脸", photo.ID))
		if err := s.db.Transaction(func(tx *gorm.DB) error {
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
		}); err != nil {
			return err
		}
		for _, pid := range previousPersonIDs {
			if err := s.syncPersonState(pid); err != nil {
				logger.Warnf("sync person %d state after zero-faces detection: %v", pid, err)
			}
		}
		return nil
	}

	createdFaces := make([]*model.Face, 0, len(result.Faces))
	thumbnailSpecs := make([]util.FaceThumbnailSpec, 0, len(result.Faces))
	for _, detected := range result.Faces {
		thumbnailSpecs = append(thumbnailSpecs, util.FaceThumbnailSpec{
			BBoxX:      detected.BBox.X,
			BBoxY:      detected.BBox.Y,
			BBoxWidth:  detected.BBox.Width,
			BBoxHeight: detected.BBox.Height,
		})
	}
	thumbnailPaths, err := util.GenerateFaceThumbnails(photo.FilePath, s.faceThumbnailRoot(), thumbnailSpecs, photo.ManualRotation)
	if err != nil {
		return err
	}
	if len(thumbnailPaths) != len(result.Faces) {
		return fmt.Errorf("expected %d face thumbnail paths, got %d", len(result.Faces), len(thumbnailPaths))
	}

	for i, detected := range result.Faces {
		embeddingPayload := model.EncodeEmbedding(detected.Embedding)
		face := &model.Face{
			PhotoID:       photo.ID,
			BBoxX:         detected.BBox.X,
			BBoxY:         detected.BBox.Y,
			BBoxWidth:     detected.BBox.Width,
			BBoxHeight:    detected.BBox.Height,
			Confidence:    detected.Confidence,
			QualityScore:  detected.QualityScore,
			Embedding:     embeddingPayload,
			ThumbnailPath: thumbnailPaths[i],
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

	// Rate limiting: only cluster every N tasks to prevent CPU overload
	// This is especially important for NAS devices with limited CPU resources
	s.clusteringTaskCounterMu.Lock()
	s.clusteringTaskCounter++
	shouldCluster := peopleClusteringTaskInterval <= 0 || s.clusteringTaskCounter >= peopleClusteringTaskInterval
	if shouldCluster {
		s.clusteringTaskCounter = 0
	}
	s.clusteringTaskCounterMu.Unlock()

	var affectedPersonIDs, affectedPhotoIDs []uint
	var clusterErr error
	if shouldCluster {
		affectedPersonIDs, affectedPhotoIDs, clusterErr = s.runIncrementalClustering()
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

	s.appendBackgroundLog(fmt.Sprintf("照片 #%d 检测到 %d 张人脸", photo.ID, len(createdFaces)))
	if err := s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
		"status":       model.PeopleJobStatusCompleted,
		"last_error":   "",
		"completed_at": &now,
	}); err != nil {
		return err
	}
	s.markMergeSuggestionsDirty("apply_detection_result")
	s.invalidateStatsCache()
	return nil
}

// convertMLResultToModel converts mlclient.DetectFacesResponse to model.PeopleDetectionResult
func convertMLResultToModel(result *mlclient.DetectFacesResponse) *model.PeopleDetectionResult {
	if result == nil {
		return &model.PeopleDetectionResult{Faces: []model.PeopleDetectionFace{}}
	}

	faces := make([]model.PeopleDetectionFace, len(result.Faces))
	for i, f := range result.Faces {
		faces[i] = model.PeopleDetectionFace{
			BBox: model.BoundingBox{
				X:      f.BBox.X,
				Y:      f.BBox.Y,
				Width:  f.BBox.Width,
				Height: f.BBox.Height,
			},
			Confidence:   f.Confidence,
			QualityScore: f.QualityScore,
			Embedding:    f.Embedding,
		}
	}

	return &model.PeopleDetectionResult{
		Faces:            faces,
		ProcessingTimeMS: result.ProcessingTimeMS,
	}
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
	if s.feedbackShutdown {
		s.feedbackMu.Unlock()
		return
	}
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
		if s.feedbackStopRequested() {
			s.markFeedbackLoopStopped()
			return
		}

		if s.shouldDelayFeedbackRecluster() {
			if s.waitFeedbackPollIntervalOrStop() {
				s.markFeedbackLoopStopped()
				return
			}
			continue
		}

		s.feedbackMu.Lock()
		if s.feedbackShutdown {
			s.feedbackPending = false
			s.feedbackRunning = false
			s.feedbackMu.Unlock()
			return
		}
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

func (s *peopleService) stopFeedbackRecluster() {
	s.feedbackMu.Lock()
	s.feedbackPending = false
	s.feedbackShutdown = true
	stopCh := s.feedbackStopCh
	s.feedbackStopCh = nil
	s.feedbackMu.Unlock()

	if stopCh != nil {
		close(stopCh)
	}
}

func (s *peopleService) feedbackStopRequested() bool {
	s.feedbackMu.Lock()
	stopCh := s.feedbackStopCh
	s.feedbackMu.Unlock()
	if stopCh == nil {
		return true
	}

	select {
	case <-stopCh:
		return true
	default:
		return false
	}
}

func (s *peopleService) waitFeedbackPollIntervalOrStop() bool {
	interval := s.feedbackReclusterPollIntervalValue()

	s.feedbackMu.Lock()
	stopCh := s.feedbackStopCh
	s.feedbackMu.Unlock()
	if stopCh == nil {
		return true
	}

	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-stopCh:
		return true
	case <-timer.C:
		return false
	}
}

func (s *peopleService) markFeedbackLoopStopped() {
	s.feedbackMu.Lock()
	defer s.feedbackMu.Unlock()
	s.feedbackRunning = false
	s.feedbackPending = false
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

func (s *peopleService) setMergeSuggestionDirtyHookForTest(hook func(string) error) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	s.mergeSuggestionDirty = hook
}

func (s *peopleService) setBackgroundBusy(busy bool) {
	s.backgroundBusyMu.Lock()
	defer s.backgroundBusyMu.Unlock()
	s.backgroundBusy = busy
}

// clusteringInterval returns the configured pause between clustering batches.
// This prevents CPU overload on NAS devices while allowing desktop users to
// tune for faster processing.
func (s *peopleService) clusteringInterval() time.Duration {
	if s.config != nil && s.config.People.ClusteringIntervalMs > 0 {
		return time.Duration(s.config.People.ClusteringIntervalMs) * time.Millisecond
	}
	return 300 * time.Millisecond
}

// idleInterval returns the polling interval when the worker is idle (no jobs, no pending faces).
// Uses exponential backoff: 300ms → 600ms → 1.2s → 2.4s → 3s (capped).
func (s *peopleService) idleInterval() time.Duration {
	base := 300 * time.Millisecond
	max := 3 * time.Second
	d := base
	for i := 0; i < s.idleCount; i++ {
		d *= 2
		if d >= max {
			return max
		}
	}
	return d
}

// AcquireWriteGate locks the write gate exclusively and returns a release function.
// Foreground mutations (merge/split/move) call this to ensure the background
// clustering worker yields before writing faces/people tables.
func (s *peopleService) AcquireWriteGate() func() {
	s.writeGate.Lock()
	return s.writeGate.Unlock
}

// PostMergeCleanup performs post-merge cleanup for suggestion-based merges.
// It mirrors the cleanup steps in MergePeople that ApplySuggestion would otherwise miss.
func (s *peopleService) PostMergeCleanup(targetPersonID uint, sourcePersonIDs []uint, affectedPhotoIDs []uint) {
	// Clean up cannot-link constraints for merged (deleted) persons
	for _, sourceID := range sourcePersonIDs {
		if err := s.cannotLinkRepo.DeleteByPersonID(sourceID); err != nil {
			logger.Warnf("failed to clean cannot-link for merged person %d: %v", sourceID, err)
		}
	}
	// Sync target person state (updates avatar, refreshes stats)
	if err := s.syncPersonState(targetPersonID); err != nil {
		logger.Warnf("post-merge syncPersonState failed for target %d: %v", targetPersonID, err)
	}
	// Recompute top_person_category on affected photos
	if len(affectedPhotoIDs) > 0 {
		if err := s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs); err != nil {
			logger.Warnf("post-merge RecomputeTopPersonCategory failed: %v", err)
		}
	}
	s.scheduleFeedbackRecluster()
}

func (s *peopleService) setMergeSuggestionDirtyHook(hook func(string) error) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	s.mergeSuggestionDirty = hook
}

func (s *peopleService) setANNCandidateFn(fn func(probes []faceWithEmbedding, k int) map[uint]struct{}) {
	s.annCandidateFn = fn
}

// recoverStuckMergeJobs marks processing/pending merge jobs as failed on startup.
// These are left over from a previous server crash or restart where the goroutine
// executing the job was lost (e.g., due to a deadlock or OOM kill).
func recoverStuckMergeJobs(repo repository.PeopleMergeJobRepository) {
	affected, err := repo.RecoverStuck("job interrupted by server restart")
	if err != nil {
		logger.Errorf("failed to recover stuck merge jobs: %v", err)
		return
	}
	if affected > 0 {
		logger.Infof("recovered %d stuck merge jobs (marked as failed)", affected)
	}
}

func (s *peopleService) markMergeSuggestionsDirty(reason string) {
	s.taskMutex.RLock()
	hook := s.mergeSuggestionDirty
	s.taskMutex.RUnlock()
	if hook == nil {
		return
	}
	if err := hook(reason); err != nil {
		logger.Warnf("failed to mark merge suggestions dirty: %v", err)
	}
}

func (s *peopleService) setTaskStatus(status string) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.task != nil && s.task.Status != model.TaskStatusStopping {
		s.task.Status = status
	}
}

func (s *peopleService) setTaskState(status string, phase string, message string, photoID *uint) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	if s.task == nil {
		return
	}
	if s.task.Status != model.TaskStatusStopping {
		s.task.Status = status
	}
	s.task.CurrentPhase = phase
	s.task.CurrentMessage = message
	if photoID != nil {
		s.task.CurrentPhotoID = *photoID
	} else {
		s.task.CurrentPhotoID = 0
	}
}

func (s *peopleService) generateFaceThumbnail(photo *model.Photo, bbox model.BoundingBox) (string, error) {
	if photo == nil {
		return "", fmt.Errorf("photo is nil")
	}
	return util.GenerateFaceThumbnail(photo.FilePath, s.faceThumbnailRoot(), bbox.X, bbox.Y, bbox.Width, bbox.Height, photo.ManualRotation)
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

func (s *peopleService) selectPersonPrototypes(faces []*model.Face, k int) map[uint][]*model.Face {
	return selectPersonPrototypesStatic(faces, k)
}

func selectPersonPrototypesStatic(faces []*model.Face, k int) map[uint][]*model.Face {
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

	// Pre-decode embeddings for selected faces (nil embeddings are preserved with nil slice)
	selectedEmbeddings := make([]faceWithEmbedding, 0, k)
	for _, f := range selected {
		emb := decodeEmbedding(f.Embedding)
		var norm float64
		if emb != nil {
			norm = calculateNorm(emb)
		}
		selectedEmbeddings = append(selectedEmbeddings, faceWithEmbedding{
			face:      f,
			embedding: emb,
			norm:      norm,
		})
	}

	// Pre-decode embeddings for auto faces (nil embeddings are preserved)
	autoWithEmb := make([]faceWithEmbedding, 0, len(auto))
	for _, f := range auto {
		emb := decodeEmbedding(f.Embedding)
		var norm float64
		if emb != nil {
			norm = calculateNorm(emb)
		}
		autoWithEmb = append(autoWithEmb, faceWithEmbedding{
			face:      f,
			embedding: emb,
			norm:      norm,
		})
	}

	// Farthest-first: greedily pick the face most different from all selected
	for len(selected) < k && len(autoWithEmb) > 0 {
		bestIdx := -1
		bestMinSim := float64(2) // start higher than any cosine similarity

		for i, candidate := range autoWithEmb {
			// Find minimum similarity to any already-selected prototype
			minSim := float64(2)
			for _, sel := range selectedEmbeddings {
				sim := cosineSimilarityPrecomputed(candidate.embedding, candidate.norm, sel.embedding, sel.norm)
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
		selected = append(selected, autoWithEmb[bestIdx].face)
		selectedEmbeddings = append(selectedEmbeddings, autoWithEmb[bestIdx])
		autoWithEmb = append(autoWithEmb[:bestIdx], autoWithEmb[bestIdx+1:]...)
	}

	return selected
}

func (s *peopleService) buildFaceGraph(faces []*model.Face) map[uint][]uint {
	graph := make(map[uint][]uint, len(faces))
	// Build per-face effective link thresholds based on retry_count
	retryCounts := make(map[uint]int, len(faces))
	thresholds := make(map[uint]float64, len(faces))
	for _, face := range faces {
		if face == nil || face.ID == 0 {
			continue
		}
		graph[face.ID] = []uint{}
		rc := face.RetryCount
		retryCounts[face.ID] = rc
		thresholds[face.ID] = s.effectiveLinkThreshold(rc)
	}

	// Pre-decode embeddings for all faces with valid embeddings
	faceEmbeddings := decodeFacesWithEmbeddings(faces)

	// Second pass: build edges using per-face effective thresholds
	// Edge exists if similarity >= min(threshold[A], threshold[B])
	for i := 0; i < len(faceEmbeddings); i++ {
		for j := i + 1; j < len(faceEmbeddings); j++ {
			score := cosineSimilarityPrecomputed(
				faceEmbeddings[i].embedding, faceEmbeddings[i].norm,
				faceEmbeddings[j].embedding, faceEmbeddings[j].norm,
			)
			edgeThreshold := thresholds[faceEmbeddings[i].face.ID]
			if t := thresholds[faceEmbeddings[j].face.ID]; t < edgeThreshold {
				edgeThreshold = t
			}
			if score < edgeThreshold {
				continue
			}

			graph[faceEmbeddings[i].face.ID] = append(graph[faceEmbeddings[i].face.ID], faceEmbeddings[j].face.ID)
			graph[faceEmbeddings[j].face.ID] = append(graph[faceEmbeddings[j].face.ID], faceEmbeddings[i].face.ID)
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

	// Pre-decode embeddings for component faces
	componentWithEmb := decodeFacesWithEmbeddings(component)
	if len(componentWithEmb) == 0 {
		return -1
	}

	// Pre-decode embeddings for prototypes
	prototypesWithEmb := decodeFacesWithEmbeddings(prototypes)
	if len(prototypesWithEmb) == 0 {
		return -1
	}

	return s.scoreComponentAgainstPersonWithEmbeddings(componentWithEmb, prototypesWithEmb)
}

// scoreComponentAgainstPersonWithEmbeddings computes score using pre-decoded embeddings.
func (s *peopleService) scoreComponentAgainstPersonWithEmbeddings(component []faceWithEmbedding, prototypes []faceWithEmbedding) float64 {
	if len(component) == 0 || len(prototypes) == 0 {
		return -1
	}

	var total float64
	var scored int

	for _, face := range component {
		bestScore := -1.0
		for _, proto := range prototypes {
			score := cosineSimilarityPrecomputed(
				face.embedding, face.norm,
				proto.embedding, proto.norm,
			)
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

	// Pre-decode embeddings for component faces
	componentWithEmb := decodeFacesWithEmbeddings(component)
	// Note: componentWithEmb may contain faces with nil embeddings

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

	// Pre-decode embeddings for all prototypes (once per call)
	prototypesWithEmb := make(map[uint][]faceWithEmbedding, len(prototypes))
	for personID, protoFaces := range prototypes {
		prototypesWithEmb[personID] = decodeFacesWithEmbeddings(protoFaces)
	}

	return s.attachComponentToExistingPersonWithEmbeddings(componentWithEmb, prototypesWithEmb, blockedPersons, prototypes, attachThreshold)
}

// attachComponentToExistingPersonWithEmbeddings is the core logic using pre-decoded embeddings.
// prototypesWithEmb contains pre-decoded embeddings; prototypesOriginal is needed for ManualLocked check.
func (s *peopleService) attachComponentToExistingPersonWithEmbeddings(
	component []faceWithEmbedding,
	prototypesWithEmb map[uint][]faceWithEmbedding,
	blockedPersons map[uint]bool,
	prototypesOriginal map[uint][]*model.Face,
	attachThreshold float64,
) (uint, float64, bool) {
	if len(component) == 0 || len(prototypesWithEmb) == 0 {
		return 0, -1, false
	}

	// ANN pre-filter: call candidate fn to reduce O(22K) full scan to O(~50).
	// nil result means index not ready; fall back to full scan.
	var candidateFilter map[uint]struct{}
	if s.annCandidateFn != nil {
		candidateFilter = s.annCandidateFn(component, annSearchK)
	}

	personIDs := make([]uint, 0, len(prototypesWithEmb))
	for personID := range prototypesWithEmb {
		if candidateFilter != nil {
			if _, ok := candidateFilter[personID]; !ok {
				continue
			}
		}
		personIDs = append(personIDs, personID)
	}
	sort.Slice(personIDs, func(i, j int) bool { return personIDs[i] < personIDs[j] })

	bestPersonID := uint(0)
	bestScore := -1.0
	for _, personID := range personIDs {
		if blockedPersons[personID] {
			continue
		}
		score := s.scoreComponentAgainstPersonWithEmbeddings(component, prototypesWithEmb[personID])
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
		for _, proto := range prototypesOriginal[bestPersonID] {
			if proto.ManualLocked {
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
	// 增加重试次数（用于退避策略）
	if err := s.db.Model(&model.Face{}).Where("id IN ?", ids).Updates(map[string]interface{}{
		"person_id":      nil,
		"cluster_status": model.FaceClusterStatusPending,
		"cluster_score":  score,
		"clustered_at":   &now,
		"retry_count":    gorm.Expr("retry_count + 1"),
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
		face.RetryCount++
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
			"retry_count":    0, // 聚类成功，重置重试次数
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
		face.RetryCount = 0
	}

	if err := s.syncPersonState(person.ID); err != nil {
		return nil, err
	}
	return s.personRepo.GetByID(person.ID)
}

func (s *peopleService) runIncrementalClustering() ([]uint, []uint, error) {
	pendingFaces, err := s.faceRepo.ListPending(peopleClusteringBatchSize)
	if err != nil {
		return nil, nil, err
	}
	if len(pendingFaces) == 0 {
		return nil, nil, nil
	}

	if s.protoCache == nil || time.Since(s.protoCache.builtAt) > clustProtoCacheTTL {
		assignedPersonIDs, err := s.faceRepo.ListAssignedPersonIDs()
		if err != nil {
			return nil, nil, err
		}
		var protoFaces []*model.Face
		if len(assignedPersonIDs) > 0 {
			protoFaces, err = s.faceRepo.ListPrototypeEmbeddings(assignedPersonIDs, peoplePrototypeCandidates)
			if err != nil {
				return nil, nil, err
			}
		}
		orig := s.selectPersonPrototypes(protoFaces, peoplePrototypeCount)
		withEmb := make(map[uint][]faceWithEmbedding, len(orig))
		for personID, protoList := range orig {
			withEmb[personID] = decodeFacesWithEmbeddings(protoList)
		}
		s.protoCache = &clustProtoCache{
			prototypesWithEmb: withEmb,
			prototypesOrig:    orig,
			builtAt:           time.Now(),
		}
	}
	prototypesWithEmb := s.protoCache.prototypesWithEmb
	prototypes := s.protoCache.prototypesOrig

	graph := s.buildFaceGraph(pendingFaces)
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

		// Pre-decode component embeddings
		componentWithEmb := decodeFacesWithEmbeddings(component)
		// Note: componentWithEmb may contain faces with nil embeddings, which is handled correctly
		// by scoreComponentAgainstPersonWithEmbeddings (returns -1 for nil embeddings)

		// Build cannot-link blocked set
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

		// Compute max retry_count in component for adaptive thresholds
		maxRetry := 0
		for _, face := range component {
			if face != nil && face.RetryCount > maxRetry {
				maxRetry = face.RetryCount
			}
		}

		personID, score, attached := s.attachComponentToExistingPersonWithEmbeddings(
			componentWithEmb, prototypesWithEmb, blockedPersons, prototypes, s.effectiveAttachThreshold(maxRetry),
		)
		componentScore := nonNegativeScore(score)

		if attached {
			now := time.Now()
			// Track previous person IDs to sync their state after face move
			prevPersonIDs := make(map[uint]struct{})
			for _, face := range component {
				if face != nil && face.PersonID != nil && *face.PersonID != 0 {
					prevPersonIDs[*face.PersonID] = struct{}{}
				}
			}
			if err := s.faceRepo.UpdateClusterFields(faceIDs(component), map[string]interface{}{
				"person_id":      personID,
				"cluster_status": model.FaceClusterStatusAssigned,
				"cluster_score":  componentScore,
				"clustered_at":   &now,
				"retry_count":    0, // 聚类成功，重置重试次数
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
				face.RetryCount = 0
			}
			affectedPersonIDs[personID] = struct{}{}
			// Also sync previous persons that lost faces
			for pid := range prevPersonIDs {
				affectedPersonIDs[pid] = struct{}{}
			}
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

		// Fallback: single-face person creation for long-pending faces
		if maxRetry >= singleFaceFallbackRetries {
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

	// Also cluster any remaining pending faces (e.g., from DissolvePerson)
	affectedPersonIDs, affectedPhotoIDs, err := s.runIncrementalClustering()
	if err != nil {
		logger.Warnf("recluster: pending face clustering failed: %v", err)
	} else {
		for _, pid := range affectedPersonIDs {
			_ = s.syncPersonState(pid)
		}
		if len(affectedPhotoIDs) > 0 {
			_ = s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs)
		}
	}

	if result.Evaluated > 0 || result.Reassigned > 0 || len(affectedPersonIDs) > 0 || len(affectedPhotoIDs) > 0 {
		s.markMergeSuggestionsDirty("trigger_recluster")
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

	faces, err := s.faceRepo.ListByPersonIDSummary(personID)
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
	return model.DecodeEmbedding(payload)
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return -1
	}
	normA := float64(vek32.Norm(a))
	normB := float64(vek32.Norm(b))
	if normA == 0 || normB == 0 {
		return -1
	}
	return float64(vek32.Dot(a, b)) / (normA * normB)
}

// faceWithEmbedding holds a face with its pre-decoded embedding and precomputed norm.
// Used to avoid repeated json.Unmarshal in clustering algorithms.
type faceWithEmbedding struct {
	face      *model.Face
	embedding []float32
	norm      float64
}

// decodeFacesWithEmbeddings pre-decodes embeddings for all faces.
// Returns all non-nil faces with their embeddings (embedding may be nil).
func decodeFacesWithEmbeddings(faces []*model.Face) []faceWithEmbedding {
	result := make([]faceWithEmbedding, 0, len(faces))
	for _, f := range faces {
		if f == nil {
			continue
		}
		emb := decodeEmbedding(f.Embedding)
		var norm float64
		if emb != nil {
			norm = calculateNorm(emb)
		}
		result = append(result, faceWithEmbedding{
			face:      f,
			embedding: emb,
			norm:      norm,
		})
	}
	return result
}

// calculateNorm computes the L2 norm of a float32 vector using SIMD acceleration.
func calculateNorm(v []float32) float64 {
	return float64(vek32.Norm(v))
}

// cosineSimilarityPrecomputed computes cosine similarity using precomputed norms.
// Uses SIMD-accelerated dot product; ArcFace embeddings are unit vectors so
// normA * normB ≈ 1.0 and the result is effectively just the dot product.
func cosineSimilarityPrecomputed(a []float32, normA float64, b []float32, normB float64) float64 {
	if len(a) == 0 || len(a) != len(b) || normA == 0 || normB == 0 {
		return -1
	}
	return float64(vek32.Dot(a, b)) / (normA * normB)
}
