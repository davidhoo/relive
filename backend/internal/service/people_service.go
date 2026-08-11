package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/mlclient"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/viterin/vek/vek32"
	"gorm.io/gorm"
)

const (
	peoplePriorityScan           = 50
	peoplePriorityManual         = 80
	peoplePriorityPassive        = 100
	peopleClusterThreshold       = 0.45
	peoplePrototypeCount         = 5
	peoplePrototypeCandidates    = 10
	defaultLinkThreshold         = 0.62
	defaultAttachThreshold       = 0.65
	peopleMinClusterFaces        = 2
	peopleFeedbackZeroResultWait = 30 * time.Second // cooldown after zero-result recluster
	peopleStatsCacheTTL          = 30 * time.Second

	// confirmedPersonDiscount lowers the attach threshold for persons with manual-locked faces,
	// making it easier for new faces to join user-confirmed identities (e.g., family members).
	confirmedPersonDiscount = 0.05

	// attachTopK is the number of highest-quality faces used for fallback scoring
	// when the full-component average falls below the attach threshold.
	attachTopK = 5

	// Adaptive threshold decay for long-pending faces
	// retry_count 0-1: full threshold (no discount)
	// retry_count 2-4: linear decay to floor
	// retry_count 5+: floor threshold
	thresholdDecayStart  = 2 // retry_count at which decay begins
	thresholdDecayEnd    = 5 // retry_count at which floor is reached
	attachThresholdFloor = 0.5
	linkThresholdFloor   = 0.5

	// Fallback: after this many retries, allow single-face person creation
	singleFaceFallbackRetries = 10

	// Clustering optimization constants to prevent CPU overload on NAS
	// See: https://github.com/davidhoo/relive/issues/XXX
	peopleClusteringBatchSize    = 50            // Max pending faces to cluster at once
	peopleClusteringTaskInterval = 5             // Cluster every N tasks (0 = always)
	clustProtoCacheTTL           = 6 * time.Hour // Safety TTL only; dirty-driven refresh is primary (Task: protoCache dirty queue)

	// protoCache dirty-queue / pressure-aware refresh constants (Task: protoCache dirty queue)
	protoCacheIncrementalThreshold = 20               // dirty persons below or equal to this go incremental refresh
	protoCacheQuietWindow          = 10 * time.Second // min idle time after last foreground change before refresh
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
	HandleAnalysisCompleted(photoID uint) error
	ReconcileAnalysisEligibility() error
	ResetAllPeople() (int, error)
	MergePeople(targetPersonID uint, sourcePersonIDs []uint) (*model.ReclusterResult, error)
	MergePeopleAsync(targetPersonID uint, sourcePersonIDs []uint, jobType string) (uint, error)
	GetMergeJobStatus(jobID uint) (*model.PeopleMergeJob, error)
	SplitPerson(sourcePersonID uint, faceIDs []uint) (*model.Person, *model.ReclusterResult, error)
	MoveFaces(faceIDs []uint, targetPersonID uint) (*model.ReclusterResult, error)
	// AssignFacePerson 针对单张人脸执行"改名"归属变更，供照片详情页使用：
	//   - targetPersonID 有值：移动到目标人物，忽略 name/category；
	//   - targetPersonID 为空但 name 命中已有人物：移动到命中人物；
	//   - targetPersonID 为空且 name 未命中：拆分创建新人物并命名/分类。
	// 返回操作涉及的照片 ID（face 所在照片），便于 handler 复用 GetPhotoPeople 重算返回。
	AssignFacePerson(faceID uint, req model.FacePersonAssignmentRequest) (photoID uint, err error)
	UpdateFaceExclusion(faceIDs []uint, excluded bool, reason string) (*model.FaceExclusionResult, error)
	UpdatePersonCategory(personID uint, category string) error
	UpdatePersonName(personID uint, name string) error
	UpdatePersonAvatar(personID uint, faceID uint) error
	DissolvePerson(personID uint) (int, error)
	// UpdateVisibility 批量设置人物隐藏状态。进入 foreground mutation/write gate，
	// 先更新 DB 再更新内存阻断集合（先 DB 后内存：DB 失败直接返回 error 不动内存，
	// 幂等性自然保证）。隐藏时触发 protoCacheDirty + ANN InvalidatePerson + merge
	// suggestion dirty；恢复时触发 protoCacheDirty + profile dirty + merge suggestion dirty。
	// 去重/500 限制/404 判断留在 handler 层。
	UpdateVisibility(personIDs []uint, hidden bool) (updated int64, err error)
	// InitHiddenPersons 从 DB 加载隐藏人物 ID 集合。在 NewServices wireup 时调用，
	// 失败则 hiddenPersonsLoaded=false，StartBackground 会拒绝启动（fail-closed）。
	InitHiddenPersons() error
	HandleShutdown() error
	ApplyDetectionResult(job *model.PeopleJob, photo *model.Photo, result *model.PeopleDetectionResult) error
}

type peopleService struct {
	db                *gorm.DB
	photoRepo         repository.PhotoRepository
	faceRepo          repository.FaceRepository
	personRepo        repository.PersonRepository
	jobRepo           repository.PeopleJobRepository
	mergeJobRepo      repository.PeopleMergeJobRepository
	cannotLinkRepo    repository.CannotLinkRepository
	faceExclusionRepo repository.FaceExclusionRepository
	feedbackEventRepo repository.PeopleFeedbackEventRepository
	config            *config.Config
	client            PeopleMLClient
	runtimeService    AnalysisRuntimeService

	taskMutex        sync.RWMutex
	task             *model.PeopleTask
	active           *activePeopleTask
	backgroundLogMu  sync.RWMutex
	backgroundLogs   []string
	backgroundBusyMu sync.RWMutex
	backgroundBusy   bool

	// writeGate serializes foreground mutations (merge/split/move) with the
	// clustering coordinator worker. Foreground ops take Lock (exclusive); the
	// coordinator worker takes RLock (shared) around each clustering batch.
	// This prevents SQLite "database is locked" when both paths write faces/people tables.
	writeGate  sync.RWMutex
	writeQueue *database.WriteQueue // serializes SQLite write operations
	idleCount  int                  // consecutive idle loops, used for polling backoff

	// clusteringCoordinator is the single entry point for all incremental
	// clustering. Its worker is the only goroutine permitted to call
	// runIncrementalClustering and touch protoCache.
	clusteringCoordinator *peopleClusteringCoordinator

	mergeSuggestionDirty func(string) error

	// Clustering rate limiting to prevent CPU overload
	clusteringTaskCounter   int
	clusteringTaskCounterMu sync.Mutex

	// Prototype cache: avoids reloading all person prototypes on every clustering batch.
	// Owned exclusively by the clustering coordinator worker (accessed only via
	// runIncrementalClustering); no locking required.
	protoCache *clustProtoCache

	// protoCacheDirty tracks foreground changes that need a cache refresh. Foreground
	// mutations call markProtoCacheDirty instead of triggering a rebuild. The coordinator
	// worker drains and applies the dirty set at the next safe opportunity (outside
	// writeGate, after quiet window, respecting cooldown). Thread-safe via its own mutex
	// because foreground goroutines write while the worker reads.
	protoCacheDirty *protoCacheDirtyState

	// annCandidateFn, when set, pre-filters the O(N) attach scan to ~O(50) ANN candidates.
	// Injected from personMergeSuggestionService which already maintains the HNSW index.
	annCandidateFn func(probes []faceWithEmbedding, k int) map[uint]struct{}

	// identityProfileMode / hooks wire the身份画像 matcher into incremental
	// clustering in Task 11 (shadow) / Task 12 (rescue). legacy 模式下四者均为零值，
	// runIncrementalClustering 不分配 observation、不复制 embedding、不调用 matcher/telemetry。
	// shadow/rescue/primary 模式由 service.go 注入真实 matcher.Match 与 telemetry.Record；
	// rescue 模式额外注入 markDirtyFn 以在 legacy miss 救回后标记目标人物画像 dirty。
	// primary 模式仍按 shadow-only 处理（Task 12 不实现 primary 应用）。
	//
	// hooks 在 service 装配时一次性注入（早于任何聚类批次），读取发生在 coordinator
	// worker goroutine 内，与 setANNCandidateFn 同样无需额外同步。
	identityProfileMode        string
	identityProfileMatchFn     identityProfileMatchFn
	identityDecisionRecordFn   identityDecisionRecordFn
	identityProfileMarkDirtyFn identityProfileMarkDirtyFn

	// identityProfileInvalidateFn 是 Task 13 统一身份画像失效 hook。所有改变 faces.person_id、
	// 人物成员组成或人物存续状态的业务路径都通过 invalidateIdentityProfiles 触发该 hook，
	// 禁止直接、零散地操作画像 Repository。生产由 service.go 注入
	// identityProfileService.Invalidate（仅非 legacy 模式）；legacy 模式下 hook 为 nil，
	// invalidateIdentityProfiles 直接返回不产生任何开销。
	identityProfileInvalidateFn identityProfileInvalidateFn

	// identityProfileANNInvalidateFn 仅失效 ANN 内存中心，不写 profile 表。用于人物隐藏：
	// ANN 立即 fail-closed，但不把 ready profile 改成 dirty。生产由 service.go 注入
	// identityProfileService.InvalidateANNOnly（仅非 legacy 模式）；legacy 模式下为 nil。
	identityProfileANNInvalidateFn func(personIDs []uint)

	// backgroundCoordinator 是后台任务治理的统一准入控制器（Phase 2）。Task 8 起前台
	// mutation 通过它注册 foreground scope，使 P2 automatic 后台 slice 在前台操作期间让路。
	// nil 时（旧测试桩）前台 mutation 仍走 clusteringCoordinator.foregroundWaiters 兼容桥。
	backgroundCoordinator *BackgroundTaskCoordinator

	// fgCoordinatorRelease 持有当前前台 mutation 注册到 backgroundCoordinator 的 release
	// 函数。由 acquireCoordinatorForeground 设置、releaseCoordinatorForeground 释放。
	// 仅在持有时非 nil；foreground mutation 串行（writeGate 独占），无需额外同步。
	fgCoordinatorRelease func()

	// protoCacheBuildHook 仅供测试：在 buildClustProtoCache 内调用，用于模拟慢速 refresh
	// 并验证 refresh 已移出 writeGate（Task 9）。生产为 nil，无开销。
	protoCacheBuildHook func()

	// In-memory cache for GetStats() to avoid scanning 78K+ faces rows on every poll.
	statsCache   *model.PeopleStatsResponse
	statsCacheAt time.Time
	statsCacheMu sync.RWMutex

	// hiddenPersonIDs is the runtime block-set for hidden persons. It mirrors
	// people.hidden=true from the database and is used to prevent hidden persons
	// from being matched by attachComponentToExistingPersonWithEmbeddings even
	// when the protoCache has not yet been refreshed. This is only a cache of DB
	// state, not a new business state source.
	// InitHiddenPersons must be called during wireup (service.go) before
	// StartBackground; StartBackground checks hiddenPersonsLoaded and refuses to
	// start if false (fail-closed).
	hiddenPersonIDs     map[uint]struct{}
	hiddenPersonsMu     sync.RWMutex
	hiddenPersonsLoaded bool
}

type clustProtoCache struct {
	prototypesWithEmb map[uint][]faceWithEmbedding
	prototypesOrig    map[uint][]*model.Face
	builtAt           time.Time
}

// protoCacheDirtyState tracks foreground mutations that require a protoCache refresh.
// Foreground goroutines call markProtoCacheDirty (thread-safe via mu); the coordinator
// worker calls snapshotProtoCacheDirty / clearProtoCacheDirty to read and clear the state.
type protoCacheDirtyState struct {
	mu                sync.Mutex
	generation        uint64
	dirtyPersonIDs    map[uint]struct{}
	deletedPersonIDs  map[uint]struct{}
	dirtyReasons      []string
	lastDirtyAt       time.Time
	lastRefreshAt     time.Time
	refreshRunning    bool
	refreshPendingGen uint64
	fullRebuildNeeded bool
}

func newProtoCacheDirtyState() *protoCacheDirtyState {
	return &protoCacheDirtyState{
		dirtyPersonIDs:   make(map[uint]struct{}),
		deletedPersonIDs: make(map[uint]struct{}),
	}
}

func (s *peopleService) markProtoCacheDirty(dirtyIDs, deletedIDs []uint, reason string) {
	if s.protoCacheDirty == nil {
		return
	}
	d := s.protoCacheDirty
	d.mu.Lock()
	defer d.mu.Unlock()
	d.generation++
	for _, id := range dirtyIDs {
		if id != 0 {
			d.dirtyPersonIDs[id] = struct{}{}
		}
	}
	for _, id := range deletedIDs {
		if id != 0 {
			d.deletedPersonIDs[id] = struct{}{}
			delete(d.dirtyPersonIDs, id)
		}
	}
	d.dirtyReasons = append(d.dirtyReasons, reason)
	d.lastDirtyAt = time.Now()
}

func (s *peopleService) markProtoCacheFullRebuild(reason string) {
	if s.protoCacheDirty == nil {
		return
	}
	d := s.protoCacheDirty
	d.mu.Lock()
	defer d.mu.Unlock()
	d.generation++
	d.fullRebuildNeeded = true
	d.dirtyPersonIDs = make(map[uint]struct{})
	d.deletedPersonIDs = make(map[uint]struct{})
	d.dirtyReasons = append(d.dirtyReasons, reason)
	d.lastDirtyAt = time.Now()
}

func (s *peopleService) snapshotProtoCacheDirty() (generation uint64, dirtyIDs, deletedIDs []uint, reasons []string, lastDirtyAt time.Time, fullRebuild bool) {
	if s.protoCacheDirty == nil {
		return 0, nil, nil, nil, time.Time{}, false
	}
	d := s.protoCacheDirty
	d.mu.Lock()
	defer d.mu.Unlock()
	dirtyIDs = make([]uint, 0, len(d.dirtyPersonIDs))
	for id := range d.dirtyPersonIDs {
		dirtyIDs = append(dirtyIDs, id)
	}
	deletedIDs = make([]uint, 0, len(d.deletedPersonIDs))
	for id := range d.deletedPersonIDs {
		deletedIDs = append(deletedIDs, id)
	}
	reasons = make([]string, len(d.dirtyReasons))
	copy(reasons, d.dirtyReasons)
	return d.generation, dirtyIDs, deletedIDs, reasons, d.lastDirtyAt, d.fullRebuildNeeded
}

func (s *peopleService) clearProtoCacheDirty(refreshGeneration uint64) {
	if s.protoCacheDirty == nil {
		return
	}
	d := s.protoCacheDirty
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.generation == refreshGeneration {
		d.dirtyPersonIDs = make(map[uint]struct{})
		d.deletedPersonIDs = make(map[uint]struct{})
		d.dirtyReasons = nil
		d.fullRebuildNeeded = false
	}
	d.lastRefreshAt = time.Now()
}

func (s *peopleService) protoCacheDirtyGeneration() uint64 {
	if s.protoCacheDirty == nil {
		return 0
	}
	d := s.protoCacheDirty
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.generation
}

func (s *peopleService) applyTombstonesToCache(deletedIDs []uint) {
	if s.protoCache == nil || len(deletedIDs) == 0 {
		return
	}
	for _, id := range deletedIDs {
		delete(s.protoCache.prototypesWithEmb, id)
		delete(s.protoCache.prototypesOrig, id)
	}
}

func (s *peopleService) refreshProtoCacheIncremental(dirtyIDs []uint) (int, error) {
	if s.protoCache == nil || len(dirtyIDs) == 0 {
		return 0, nil
	}
	protoFaces, err := s.faceRepo.ListPrototypeEmbeddings(dirtyIDs, peoplePrototypeCandidates)
	if err != nil {
		return 0, err
	}
	orig := s.selectPersonPrototypes(protoFaces, peoplePrototypeCount)
	withEmb := make(map[uint][]faceWithEmbedding, len(orig))
	for personID, protoList := range orig {
		withEmb[personID] = decodeFacesWithEmbeddings(protoList)
	}
	for _, id := range dirtyIDs {
		delete(s.protoCache.prototypesWithEmb, id)
		delete(s.protoCache.prototypesOrig, id)
	}
	for personID, protoList := range orig {
		s.protoCache.prototypesWithEmb[personID] = withEmb[personID]
		s.protoCache.prototypesOrig[personID] = protoList
	}
	s.protoCache.builtAt = time.Now()
	logger.Infof("people clustering: protoCache incremental refresh persons=%d prototypes=%d",
		len(dirtyIDs), len(protoFaces))
	return len(dirtyIDs), nil
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

	svc := &peopleService{
		db:                db,
		photoRepo:         photoRepo,
		faceRepo:          faceRepo,
		personRepo:        personRepo,
		jobRepo:           jobRepo,
		mergeJobRepo:      mergeJobRepo,
		cannotLinkRepo:    cannotLinkRepo,
		faceExclusionRepo: repository.NewFaceExclusionRepository(db),
		config:            cfg,
		client:            client,
		runtimeService:    runtimeService,
		writeQueue:        database.GetWriteQueue(),
	}
	svc.clusteringCoordinator = newPeopleClusteringCoordinator(svc)
	svc.clusteringCoordinator.start()
	svc.protoCacheDirty = newProtoCacheDirtyState()
	// Load hidden person IDs into the runtime block-set. Done in constructor so
	// all callers (production wireup + tests) get fail-closed for free. If this
	// fails, hiddenPersonsLoaded stays false and StartBackground will refuse to start.
	_ = svc.InitHiddenPersons()
	return svc
}

// executeWrite runs fn through WriteQueue if available, otherwise directly.
func (s *peopleService) executeWrite(fn func() error) error {
	if s.writeQueue != nil {
		return s.writeQueue.Execute(fn)
	}
	return fn()
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

	// Fail-closed: refuse to start background clustering if hidden person IDs
	// have not been loaded. This prevents running clustering with an unknown
	// participation state (e.g. InitHiddenPersons failed during wireup).
	if !s.isHiddenPersonsLoaded() {
		return nil, fmt.Errorf("hidden person IDs not loaded; call InitHiddenPersons before starting background")
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
	// 已检测/待检测照片数来自照片当前 face_process_status，独立于 people_jobs 任务明细，
	// 清理终态任务后仍保持一致（任务明细 completed/failed/cancelled 仅含保留期内数据）。
	detectedPhotos, err := s.photoRepo.CountActiveByFaceProcessStatuses([]string{
		model.FaceProcessStatusReady,
		model.FaceProcessStatusNoFace,
		model.FaceProcessStatusFailed,
	})
	if err != nil {
		return nil, err
	}
	pendingPhotos, err := s.photoRepo.CountActiveByFaceProcessStatuses([]string{
		model.FaceProcessStatusNone,
		model.FaceProcessStatusPending,
		model.FaceProcessStatusProcessing,
	})
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
		TotalFaces:                 pendingFaceStats.TotalFaces,
		DetectedPhotos:             detectedPhotos,
		PendingPhotos:              pendingPhotos,
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

// HandleAnalysisCompleted is the post-commit bridge from AI analysis into the
// People pipeline. It is intentionally idempotent so startup reconciliation can
// replay a photo after a missed notification.
func (s *peopleService) HandleAnalysisCompleted(photoID uint) error {
	photo, err := s.photoRepo.GetByID(photoID)
	if err != nil {
		return err
	}
	if photo == nil || photo.Status != model.PhotoStatusActive || !photo.AIAnalyzed {
		return nil
	}
	category := strings.TrimSpace(photo.MainCategory)
	if category == "" {
		return nil
	}
	if photo.PeopleExcluded || category == model.PhotoMainCategoryScreenshot {
		return s.excludeScreenshotPhoto(photo)
	}
	return s.enqueuePhotoModel(photo, model.PeopleJobSourcePassive, peoplePriorityPassive, false)
}

// ReconcileAnalysisEligibility repairs the durable AI→People handoff after an
// upgrade, restart, or a best-effort callback failure.
func (s *peopleService) ReconcileAnalysisEligibility() error {
	var photos []*model.Photo
	if err := s.db.
		Where(`status = ? AND ai_analyzed = ? AND (
			(TRIM(COALESCE(main_category, '')) = ? AND (
				people_excluded = ? OR
				COALESCE(people_exclusion_reason, '') != ? OR
				face_process_status != ? OR
				face_count != 0 OR
				EXISTS (SELECT 1 FROM faces WHERE faces.photo_id = photos.id) OR
				EXISTS (SELECT 1 FROM people_jobs WHERE people_jobs.photo_id = photos.id AND people_jobs.status IN (?, ?, ?))
			)) OR
			(TRIM(COALESCE(main_category, '')) != '' AND TRIM(main_category) != ? AND people_excluded = ? AND face_process_status = ?)
		)`,
			model.PhotoStatusActive,
			true,
			model.PhotoMainCategoryScreenshot,
			false,
			model.PeopleExclusionReasonScreenshot,
			model.FaceProcessStatusNone,
			model.PeopleJobStatusPending,
			model.PeopleJobStatusQueued,
			model.PeopleJobStatusProcessing,
			model.PhotoMainCategoryScreenshot,
			false,
			model.FaceProcessStatusNone,
		).
		Order("id ASC").
		Find(&photos).Error; err != nil {
		return fmt.Errorf("list AI/People reconciliation candidates: %w", err)
	}

	for _, photo := range photos {
		if err := s.HandleAnalysisCompleted(photo.ID); err != nil {
			return fmt.Errorf("reconcile photo %d: %w", photo.ID, err)
		}
	}
	return nil
}

func (s *peopleService) excludeScreenshotPhoto(photo *model.Photo) error {
	if photo == nil {
		return nil
	}

	totalStart := time.Now()
	lockWait := s.beginForegroundMutationTimed()
	defer s.endForegroundMutation()
	bizStart := time.Now()
	defer func() {
		logPeopleMutationTiming(peopleMutationTiming{
			Operation: "exclude_screenshot_photo",
			TargetID:  photo.ID,
			GateWait:  lockWait,
			Business:  time.Since(bizStart),
			Total:     time.Since(totalStart),
		})
	}()

	existingFaces, err := s.faceRepo.ListByPhotoID(photo.ID)
	if err != nil {
		return fmt.Errorf("list screenshot faces: %w", err)
	}
	affectedPersonIDs := personIDsFromFaces(existingFaces)
	now := time.Now()

	if err := s.executeWrite(func() error {
		return s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.PeopleJob{}).
				Where("photo_id = ? AND status IN ?", photo.ID, []string{
					model.PeopleJobStatusPending,
					model.PeopleJobStatusQueued,
					model.PeopleJobStatusProcessing,
				}).
				Updates(map[string]interface{}{
					"status":       model.PeopleJobStatusCancelled,
					"last_error":   "photo excluded from People after AI classified it as screenshot",
					"completed_at": &now,
				}).Error; err != nil {
				return err
			}
			if err := tx.Where("photo_id = ?", photo.ID).Delete(&model.FaceExclusion{}).Error; err != nil {
				return err
			}
			if err := tx.Where("photo_id = ?", photo.ID).Delete(&model.Face{}).Error; err != nil {
				return err
			}
			return tx.Model(&model.Photo{}).Where("id = ?", photo.ID).Updates(map[string]interface{}{
				"people_excluded":         true,
				"people_exclusion_reason": model.PeopleExclusionReasonScreenshot,
				"face_process_status":     model.FaceProcessStatusNone,
				"face_count":              0,
				"top_person_category":     "",
			}).Error
		})
	}); err != nil {
		return fmt.Errorf("exclude screenshot photo %d: %w", photo.ID, err)
	}

	var dirtyPersonIDs, deletedPersonIDs []uint
	for _, personID := range affectedPersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return fmt.Errorf("sync person %d after screenshot exclusion: %w", personID, err)
		}
		person, err := s.personRepo.GetByID(personID)
		if err != nil {
			return err
		}
		if person == nil {
			deletedPersonIDs = append(deletedPersonIDs, personID)
		} else {
			dirtyPersonIDs = append(dirtyPersonIDs, personID)
		}
	}
	if len(affectedPersonIDs) > 0 {
		s.markProtoCacheDirty(dirtyPersonIDs, deletedPersonIDs, "screenshot_photo_excluded")
		s.invalidateIdentityProfiles(IdentityProfileInvalidation{
			DirtyPersonIDs:   dirtyPersonIDs,
			DeletedPersonIDs: deletedPersonIDs,
			Reason:           "screenshot_photo_excluded",
		})
		s.markMergeSuggestionsDirty("screenshot_photo_excluded")
	}
	s.invalidateStatsCache()
	return nil
}

func (s *peopleService) HandleShutdown() error {
	// Stop the clustering coordinator first: no new clustering requests are
	// accepted, pending background work is cleared, and the worker goroutine
	// exits before we proceed to stop the background people task.
	s.clusteringCoordinator.stop()

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

	// Acquire the write gate exclusively as a foreground mutation so the
	// clustering coordinator yields and no clustering batch touches the
	// tables we are about to wipe.
	s.beginForegroundMutation()
	defer s.endForegroundMutation()

	var count int
	// coreCommitted 标记 ResetAll 核心事务是否已提交。一旦为 true，即便后续重新入队失败，
	// 也必须触发画像全量失效——所有人物身份成员已清空。Reset 事务失败则不失效。
	coreCommitted := false
	err := s.executeWrite(func() error {
		return s.db.Transaction(func(tx *gorm.DB) error {
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
	})
	if err != nil {
		return 0, err
	}
	coreCommitted = true

	// 核心事务已提交：清空全部身份画像派生数据 + ANN 不可用。不全量加载人物 ID。
	// 后续重新入队失败不恢复旧画像，也不影响此失效。
	s.invalidateIdentityProfiles(IdentityProfileInvalidation{
		ResetAll: true,
		Reason:   "reset_all_people",
	})
	s.markProtoCacheFullRebuild("reset_all_people")

	enqueued := 0
	err = s.photoRepo.IterateActivePhotos([]string{"id", "status", "ai_analyzed", "people_excluded", "main_category"}, 500, func(photos []*model.Photo) error {
		for _, photo := range photos {
			if err := s.enqueuePhotoModel(photo, model.PeopleJobSourceScan, peoplePriorityScan, true); err != nil {
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
	_ = coreCommitted
	return enqueued, nil
}

func (s *peopleService) MergePeople(targetPersonID uint, sourcePersonIDs []uint) (result *model.ReclusterResult, err error) {
	// Reject if target or any source is hidden.
	if err := s.rejectIfHidden(append([]uint{targetPersonID}, sourcePersonIDs...)...); err != nil {
		return nil, err
	}
	totalStart := time.Now()
	lockWait := s.beginForegroundMutationTimed()
	defer s.endForegroundMutation()
	bizStart := time.Now()
	defer func() {
		logPeopleMutationTiming(peopleMutationTiming{
			Operation: "merge_people",
			TargetID:  targetPersonID,
			FaceCount: len(sourcePersonIDs),
			GateWait:  lockWait,
			Business:  time.Since(bizStart),
			Total:     time.Since(totalStart),
			Err:       err,
		})
	}()

	var affectedPhotoIDs []uint
	// coreCommitted 标记核心人物合并是否已落库。一旦为 true，即便后续缓存同步或
	// 照片分类刷新失败，也必须记录反馈事件与触发画像失效——身份事实已经发生。
	coreCommitted := false
	defer func() {
		if coreCommitted {
			// 在 executeWrite 回调之外单独写事件，避免 WriteQueue 重入。
			s.recordFeedbackEvent(buildFeedbackEvent(
				repository.PeopleFeedbackEventMergeConfirmed,
				targetPersonID,
				sourcePersonIDs,
				nil,
				peopleManualAlgorithmVersion,
				nil, // 手工合并不现场计算相似度，无可用快照
			))
			// 统一画像失效：target dirty、sources deleted。source 去重并过滤 target/0。
			// 异步 MergePeople 复用同一路径（executeMergeJob 调用本方法）。
			s.invalidateIdentityProfiles(IdentityProfileInvalidation{
				DirtyPersonIDs:   []uint{targetPersonID},
				DeletedPersonIDs: sourcePersonIDs,
				Reason:           "people_merged",
			})
		}
	}()
	if err = s.executeWrite(func() error {
		var innerErr error
		affectedPhotoIDs, innerErr = s.personRepo.MergeInto(targetPersonID, sourcePersonIDs)
		if innerErr != nil {
			return innerErr
		}
		for _, sourceID := range sourcePersonIDs {
			_ = s.cannotLinkRepo.DeleteByPersonID(sourceID)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	coreCommitted = true
	if err = s.syncPersonState(targetPersonID); err != nil {
		return nil, err
	}
	if err = s.executeWrite(func() error {
		return s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs)
	}); err != nil {
		return nil, err
	}
	s.markMergeSuggestionsDirty("merge_people")
	s.markProtoCacheDirty([]uint{targetPersonID}, sourcePersonIDs, "merge_people")
	s.scheduleFeedbackRecluster()
	return &model.ReclusterResult{}, nil
}

// MergePeopleAsync 异步创建人物合并任务，避免同步执行超时
func (s *peopleService) MergePeopleAsync(targetPersonID uint, sourcePersonIDs []uint, jobType string) (uint, error) {
	if len(sourcePersonIDs) == 0 {
		return 0, fmt.Errorf("no source persons to merge")
	}

	// Reject if target or any source is hidden.
	if err := s.rejectIfHidden(append([]uint{targetPersonID}, sourcePersonIDs...)...); err != nil {
		return 0, err
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

	if err := s.executeWrite(func() error {
		return s.mergeJobRepo.Create(job)
	}); err != nil {
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
		"status":     job.Status,
		"started_at": job.StartedAt,
	}).Error; err != nil {
		logger.Errorf("Failed to update merge job %d status: %v", jobID, err)
		return
	}

	// 解析源人物 ID 列表
	var sourcePersonIDs []uint
	if err := json.Unmarshal([]byte(job.SourceIDs), &sourcePersonIDs); err != nil {
		logger.Errorf("Failed to unmarshal source IDs for job %d: %v", jobID, err)
		_ = s.executeWrite(func() error { return s.mergeJobRepo.Fail(jobID, fmt.Sprintf("invalid source IDs: %v", err)) })
		return
	}

	// 执行同步合并
	result, err := s.MergePeople(job.TargetID, sourcePersonIDs)
	if err != nil {
		logger.Errorf("Merge job %d failed: %v", jobID, err)
		_ = s.executeWrite(func() error { return s.mergeJobRepo.Fail(jobID, err.Error()) })
		return
	}

	// 序列化结果
	resultJSON, _ := json.Marshal(&model.PeopleMergeJobResult{
		AffectedPhotoCount: len(sourcePersonIDs), // 简化计数
		ReclusterResult:    result,
	})

	if err := s.executeWrite(func() error {
		return s.mergeJobRepo.Complete(jobID, string(resultJSON))
	}); err != nil {
		logger.Errorf("Failed to complete merge job %d: %v", jobID, err)
		return
	}

	logger.Infof("Merge job %d completed successfully", jobID)
}

// GetMergeJobStatus 获取合并任务状态
func (s *peopleService) GetMergeJobStatus(jobID uint) (*model.PeopleMergeJob, error) {
	return s.mergeJobRepo.GetByID(jobID)
}

func (s *peopleService) SplitPerson(sourcePersonID uint, faceIDs []uint) (person *model.Person, result *model.ReclusterResult, err error) {
	if err := s.rejectIfHidden(sourcePersonID); err != nil {
		return nil, nil, err
	}
	totalStart := time.Now()
	lockWait := s.beginForegroundMutationTimed()
	defer s.endForegroundMutation()
	bizStart := time.Now()

	// 归一化请求 face 集合（去零、去重、升序），用于存在性校验与幂等比较。
	normalizedFaceIDs := normalizeFaceIDs(faceIDs)

	splitOutcome := "failed"
	var replayTargetPersonID uint
	var currentOwnerIDs []uint
	defer func() {
		var targetID uint
		if person != nil {
			targetID = person.ID
		}
		logPeopleMutationTiming(peopleMutationTiming{
			Operation:            "split_person",
			TargetID:             targetID,
			SourcePersonID:       sourcePersonID,
			FaceCount:            len(normalizedFaceIDs),
			CurrentPersonIDs:     currentOwnerIDs,
			ReplayTargetPersonID: replayTargetPersonID,
			Result:               splitOutcome,
			GateWait:             lockWait,
			Business:             time.Since(bizStart),
			Total:                time.Since(totalStart),
			Err:                  err,
		})
	}()

	if len(normalizedFaceIDs) == 0 {
		// binding 已要求 min=1，此处仅作防御（全为零/空）。
		return nil, nil, fmt.Errorf("face_ids must contain at least one valid id")
	}

	faces, err := s.faceRepo.ListByIDs(normalizedFaceIDs)
	if err != nil {
		return nil, nil, err
	}
	// 所有人脸都必须存在；不允许忽略部分不存在的人脸继续处理。
	if len(faces) != len(normalizedFaceIDs) {
		return nil, nil, fmt.Errorf("some face ids not found")
	}

	// 收集请求 faces 当前的 distinct person_id 集合（过滤 0），并判断是否存在无归属人脸。
	currentOwnerIDs = distinctFacePersonIDs(faces)
	hasUnassigned := false
	for _, face := range faces {
		if face.PersonID == nil || *face.PersonID == 0 {
			hasUnassigned = true
			break
		}
	}

	// 情况二/三判定仅在同一归属（无未分配且恰好一个 owner）时才需要区分重放与冲突。
	isLegitFirstSplit := false
	if !hasUnassigned && len(currentOwnerIDs) == 1 {
		singleOwner := onlyValue(currentOwnerIDs)
		if singleOwner == sourcePersonID {
			// 情况一：合法首次拆分。所有人脸当前同属 sourcePersonID。
			// 无论来自聚类、merge、move 或其他人工归属，都视为合法新操作。
			// 不检查 manual_locked / manual_lock_reason。
			isLegitFirstSplit = true
		} else {
			// 人脸当前统一属于其他人物（singleOwner != sourcePersonID）。
			// 情况二候选：同一拆分请求的幂等重放。只有找到精确匹配的 person_split
			// 事件（source 含 sourcePersonID、face_ids 完全一致、target == 当前归属、
			// 目标人物仍存在）才返回已有目标人物，不创建新人物/事件/副作用。
			if target, ok := s.findReplaySplitTarget(sourcePersonID, normalizedFaceIDs, singleOwner); ok {
				existing, getErr := s.personRepo.GetByID(target)
				if getErr != nil {
					return nil, nil, getErr
				}
				if existing == nil {
					// 目标人物已不存在，不构成有效重放 → 归属冲突。
					splitOutcome = "conflict"
					return nil, nil, errPeopleSplitConflict
				}
				replayTargetPersonID = existing.ID
				splitOutcome = "replayed"
				return existing, &model.ReclusterResult{}, nil
			}
			// 找不到匹配事件 → 真实归属冲突（情况三）。
			splitOutcome = "conflict"
			return nil, nil, errPeopleSplitConflict
		}
	}
	if !isLegitFirstSplit {
		// 人脸当前分散在多个人物，或部分/全部无归属 → 真实归属冲突（情况三）。
		splitOutcome = "conflict"
		return nil, nil, errPeopleSplitConflict
	}

	sourcePerson, err := s.personRepo.GetByID(sourcePersonID)
	if err != nil {
		return nil, nil, err
	}
	if sourcePerson == nil {
		return nil, nil, fmt.Errorf("source person not found")
	}

	newPerson := &model.Person{Category: sourcePerson.Category}
	// coreCommitted 标记新人物创建与人脸重指派已落库；后续 syncPersonState/分类刷新
	// 失败仍需记录 person_split 事件与触发画像失效，因为身份事实已发生。
	coreCommitted := false
	defer func() {
		if coreCommitted {
			s.recordFeedbackEvent(buildFeedbackEvent(
				repository.PeopleFeedbackEventPersonSplit,
				newPerson.ID,
				[]uint{sourcePersonID},
				normalizedFaceIDs,
				peopleManualAlgorithmVersion,
				nil,
			))
			// 统一画像失效：原人物失去成员后重建，新人物获得成员后构建画像。
			s.invalidateIdentityProfiles(IdentityProfileInvalidation{
				DirtyPersonIDs: []uint{sourcePersonID, newPerson.ID},
				Reason:         "person_split",
			})
		}
	}()
	if err := s.executeWrite(func() error {
		if err := s.personRepo.Create(newPerson); err != nil {
			return err
		}
		return s.faceRepo.ReassignFaces(normalizedFaceIDs, newPerson.ID, "split")
	}); err != nil {
		return nil, nil, err
	}
	coreCommitted = true

	if err := s.syncPersonState(sourcePersonID); err != nil {
		return nil, nil, err
	}
	if err := s.syncPersonState(newPerson.ID); err != nil {
		return nil, nil, err
	}
	if err := s.executeWrite(func() error {
		if err := s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces)); err != nil {
			return err
		}
		return s.cannotLinkRepo.Create(sourcePersonID, newPerson.ID)
	}); err != nil {
		return nil, nil, err
	}

	person, err = s.personRepo.GetByID(newPerson.ID)
	if err != nil {
		return nil, nil, err
	}
	s.markMergeSuggestionsDirty("split_person")
	s.markProtoCacheDirty([]uint{sourcePersonID, newPerson.ID}, nil, "split_person")
	s.scheduleFeedbackRecluster()
	splitOutcome = "created"
	return person, &model.ReclusterResult{}, nil
}

func (s *peopleService) MoveFaces(faceIDs []uint, targetPersonID uint) (result *model.ReclusterResult, err error) {
	if err := s.rejectIfHidden(targetPersonID); err != nil {
		return nil, err
	}
	totalStart := time.Now()
	lockWait := s.beginForegroundMutationTimed()
	defer s.endForegroundMutation()
	bizStart := time.Now()
	defer func() {
		logPeopleMutationTiming(peopleMutationTiming{
			Operation: "move_faces",
			TargetID:  targetPersonID,
			FaceCount: len(faceIDs),
			GateWait:  lockWait,
			Business:  time.Since(bizStart),
			Total:     time.Since(totalStart),
			Err:       err,
		})
	}()

	faces, err := s.faceRepo.ListByIDs(faceIDs)
	if err != nil {
		return nil, err
	}
	if len(faces) == 0 {
		return nil, fmt.Errorf("faces not found")
	}

	// 幂等保护：检查是否存在已漂移到非 target 人物的 face（stale repeat 跨人物冲突）。
	// 请求的 face 可能处于三种归属状态之一：
	//  - 已属 target：本次无需移动（幂等 no-op 部分）。
	//  - 仍属原 source（或多个不同 source）：合法 move，按既有语义移动剩余 face。
	//  - 已属某个非 target 的不同人物（之前 move/assign 漂移）：stale repeat 冲突。
	//    若同时还有 face 仍在原 source，这是混合场景——不能安全地只移动剩余 face，因为
	//    那会把已漂移的 face 留在错误人物而把剩余 face 改到 target，产生不一致结果。
	//    按计划要求返回 conflict，不 mutate。
	// 判据：统计非 target 归属的 distinct person_id。若 >1 个不同非 target 人物 → conflict。
	// （全部同属一个非 target 人物 + target 的混合不在本 case；那是首次 move 的合法子集。）
	nonTargetOwners := make(map[uint]struct{})
	hasAtTarget := false
	for _, face := range faces {
		if face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		if *face.PersonID == targetPersonID {
			hasAtTarget = true
			continue
		}
		nonTargetOwners[*face.PersonID] = struct{}{}
	}
	if len(nonTargetOwners) > 1 {
		// 请求 face 跨多个不同非 target 人物 → stale repeat 漂移冲突，不 mutate。
		return nil, errPeopleMoveConflict
	}
	_ = hasAtTarget

	sourcePersonIDs := make(map[uint]struct{})
	movedFaceIDs := make([]uint, 0, len(faces))
	for _, face := range faces {
		if face.PersonID != nil && *face.PersonID != 0 && *face.PersonID != targetPersonID {
			sourcePersonIDs[*face.PersonID] = struct{}{}
			movedFaceIDs = append(movedFaceIDs, face.ID)
		}
	}

	// 若所有人脸本来就属于目标人物，没有实际变化，则不产生事件。
	willEmitEvent := len(movedFaceIDs) > 0
	// coreCommitted 标记人脸重指派已落库；后续 syncPersonState/分类刷新失败仍记录事件与失效。
	coreCommitted := false
	defer func() {
		if coreCommitted && willEmitEvent {
			s.recordFeedbackEvent(buildFeedbackEvent(
				repository.PeopleFeedbackEventFaceMoved,
				targetPersonID,
				mapKeys(sourcePersonIDs),
				movedFaceIDs,
				peopleManualAlgorithmVersion,
				nil,
			))
			// 统一画像失效：实际失去人脸的 source 与获得人脸的 target。
			// source 与 target 去重（target 不在 sourcePersonIDs 中，因 willEmitEvent 已过滤）。
			s.invalidateIdentityProfiles(IdentityProfileInvalidation{
				DirtyPersonIDs: append(mapKeys(sourcePersonIDs), targetPersonID),
				Reason:         "faces_moved",
			})
		}
	}()
	if err := s.executeWrite(func() error {
		return s.faceRepo.ReassignFaces(faceIDs, targetPersonID, "move")
	}); err != nil {
		return nil, err
	}
	coreCommitted = true

	if err := s.syncPersonState(targetPersonID); err != nil {
		return nil, err
	}
	for personID := range sourcePersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return nil, err
		}
	}

	if err := s.executeWrite(func() error {
		return s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces))
	}); err != nil {
		return nil, err
	}
	s.markMergeSuggestionsDirty("move_faces")
	s.markProtoCacheDirty(append(mapKeys(sourcePersonIDs), targetPersonID), nil, "move_faces")
	s.scheduleFeedbackRecluster()
	return &model.ReclusterResult{}, nil
}

// AssignFacePerson 针对单张人脸执行“改名”归属变更。复用 SplitPerson/MoveFaces
// 的核心逻辑与副作用链路（状态同步、top_person_category 重算、画像失效、合并建议
// dirty 标记、feedback recluster 调度、cannot-link 规则）。
//
// 决策规则：
//   - req.TargetPersonID 有值：移动到目标人物，忽略 name/category，分类用目标人物。
//   - req.TargetPersonID 为空但 name 命中已有人物：移动到命中人物，分类用命中人物。
//     同名多人物时取 id 最小者（与现有同名搜索选择目标语义一致）。
//   - req.TargetPersonID 为空且 name 未命中：拆分创建新人物，name=name，
//     category=req.Category（缺省 stranger）。
func (s *peopleService) AssignFacePerson(faceID uint, req model.FacePersonAssignmentRequest) (photoID uint, err error) {
	totalStart := time.Now()
	lockWait := s.beginForegroundMutationTimed()
	defer s.endForegroundMutation()
	bizStart := time.Now()
	defer func() {
		logPeopleMutationTiming(peopleMutationTiming{
			Operation: "assign_face_person",
			TargetID:  faceID,
			FaceCount: 1,
			GateWait:  lockWait,
			Business:  time.Since(bizStart),
			Total:     time.Since(totalStart),
			Err:       err,
		})
	}()

	// face 必须存在且已有 person_id；无归属 face 不支持改名。
	face, err := s.faceRepo.GetByID(faceID)
	if err != nil {
		return 0, err
	}
	if face == nil {
		return 0, fmt.Errorf("face %d not found", faceID)
	}
	if face.PersonID == nil || *face.PersonID == 0 {
		return 0, fmt.Errorf("face %d has no person", faceID)
	}
	sourcePersonID := *face.PersonID

	// Reject if source person is hidden.
	if err := s.rejectIfHidden(sourcePersonID); err != nil {
		return 0, err
	}

	targetPersonID, createNew, err := s.resolveAssignTarget(sourcePersonID, req)
	if err != nil {
		return 0, err
	}

	// Reject if target person (when not creating new) is hidden.
	if !createNew {
		if err := s.rejectIfHidden(targetPersonID); err != nil {
			return 0, err
		}
	}

	// 目标与当前归属相同：视为无变化，直接返回，不产生副作用。
	if !createNew && targetPersonID == sourcePersonID {
		return face.PhotoID, nil
	}

	if createNew {
		newPerson := &model.Person{Category: resolvePersonCategory(req.Category)}
		coreCommitted := false
		defer func() {
			if coreCommitted {
				s.recordFeedbackEvent(buildFeedbackEvent(
					repository.PeopleFeedbackEventPersonSplit,
					newPerson.ID,
					[]uint{sourcePersonID},
					[]uint{faceID},
					peopleManualAlgorithmVersion,
					nil,
				))
				s.invalidateIdentityProfiles(IdentityProfileInvalidation{
					DirtyPersonIDs: []uint{sourcePersonID, newPerson.ID},
					Reason:         "person_split",
				})
			}
		}()
		if err := s.executeWrite(func() error {
			if err := s.personRepo.Create(newPerson); err != nil {
				return err
			}
			return s.faceRepo.ReassignFaces([]uint{faceID}, newPerson.ID, "split")
		}); err != nil {
			return 0, err
		}
		coreCommitted = true

		// 命名/分类在状态同步后单独更新，避免 Create 时未带 name。
		if trimmedName := strings.TrimSpace(req.Name); trimmedName != "" {
			if err := s.executeWrite(func() error {
				return s.personRepo.UpdateFields(newPerson.ID, map[string]interface{}{"name": trimmedName})
			}); err != nil {
				return 0, err
			}
		}

		if err := s.syncPersonState(sourcePersonID); err != nil {
			return 0, err
		}
		if err := s.syncPersonState(newPerson.ID); err != nil {
			return 0, err
		}
		if err := s.executeWrite(func() error {
			if err := s.photoRepo.RecomputeTopPersonCategory([]uint{face.PhotoID}); err != nil {
				return err
			}
			return s.cannotLinkRepo.Create(sourcePersonID, newPerson.ID)
		}); err != nil {
			return 0, err
		}
		s.markMergeSuggestionsDirty("assign_face_person_split")
		s.markProtoCacheDirty([]uint{sourcePersonID, newPerson.ID}, nil, "assign_face_person_split")
		s.scheduleFeedbackRecluster()
		return face.PhotoID, nil
	}

	// 移动到已有人物（目标或命中人物）。
	movedFaceIDs := []uint{faceID}
	sourcePersonIDs := map[uint]struct{}{sourcePersonID: {}}
	coreCommitted := false
	defer func() {
		if coreCommitted {
			s.recordFeedbackEvent(buildFeedbackEvent(
				repository.PeopleFeedbackEventFaceMoved,
				targetPersonID,
				mapKeys(sourcePersonIDs),
				movedFaceIDs,
				peopleManualAlgorithmVersion,
				nil,
			))
			s.invalidateIdentityProfiles(IdentityProfileInvalidation{
				DirtyPersonIDs: append(mapKeys(sourcePersonIDs), targetPersonID),
				Reason:         "faces_moved",
			})
		}
	}()
	if err := s.executeWrite(func() error {
		return s.faceRepo.ReassignFaces([]uint{faceID}, targetPersonID, "move")
	}); err != nil {
		return 0, err
	}
	coreCommitted = true

	if err := s.syncPersonState(targetPersonID); err != nil {
		return 0, err
	}
	if err := s.syncPersonState(sourcePersonID); err != nil {
		return 0, err
	}
	if err := s.executeWrite(func() error {
		return s.photoRepo.RecomputeTopPersonCategory([]uint{face.PhotoID})
	}); err != nil {
		return 0, err
	}
	s.markMergeSuggestionsDirty("assign_face_person_move")
	s.markProtoCacheDirty([]uint{sourcePersonID, targetPersonID}, nil, "assign_face_person_move")
	s.scheduleFeedbackRecluster()
	return face.PhotoID, nil
}

// resolveAssignTarget 解析改名请求的目标人物。返回 (targetPersonID, createNew, err)。
func (s *peopleService) resolveAssignTarget(sourcePersonID uint, req model.FacePersonAssignmentRequest) (uint, bool, error) {
	if req.TargetPersonID != 0 {
		target, err := s.personRepo.GetByID(req.TargetPersonID)
		if err != nil {
			return 0, false, err
		}
		if target == nil {
			return 0, false, fmt.Errorf("target person %d not found", req.TargetPersonID)
		}
		return target.ID, false, nil
	}

	trimmedName := strings.TrimSpace(req.Name)
	if trimmedName == "" {
		return 0, false, fmt.Errorf("name is required when target_person_id is empty")
	}

	matches, err := s.personRepo.ListByNameExact(trimmedName)
	if err != nil {
		return 0, false, err
	}
	if len(matches) > 0 {
		// 同名命中已有人物：移动过去。同名多人物取 id 最小者。
		return matches[0].ID, false, nil
	}
	// 未命中：拆分创建新人物。分类由 resolvePersonCategory 决定。
	return 0, true, nil
}

// resolvePersonCategory 校验并归一化人物分类，缺省/非法值回退 stranger。
func resolvePersonCategory(category string) string {
	switch strings.TrimSpace(category) {
	case model.PersonCategoryFamily, model.PersonCategoryFriend, model.PersonCategoryAcquaintance, model.PersonCategoryStranger:
		return category
	default:
		return model.PersonCategoryStranger
	}
}

func (s *peopleService) UpdatePersonCategory(personID uint, category string) error {
	if err := s.executeWrite(func() error {
		return s.personRepo.UpdateFields(personID, map[string]interface{}{"category": category})
	}); err != nil {
		return err
	}
	faces, err := s.faceRepo.ListByPersonIDSummary(personID)
	if err != nil {
		return err
	}
	if err := s.executeWrite(func() error {
		return s.photoRepo.RecomputeTopPersonCategory(facePhotoIDs(faces))
	}); err != nil {
		return err
	}
	s.markMergeSuggestionsDirty("update_person_category")
	return nil
}

func (s *peopleService) UpdatePersonName(personID uint, name string) error {
	return s.executeWrite(func() error {
		return s.personRepo.UpdateFields(personID, map[string]interface{}{"name": name})
	})
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
func (s *peopleService) DissolvePerson(personID uint) (released int, err error) {
	if err := s.rejectIfHidden(personID); err != nil {
		return 0, err
	}
	totalStart := time.Now()
	lockWait := s.beginForegroundMutationTimed()
	defer s.endForegroundMutation()
	bizStart := time.Now()
	defer func() {
		logPeopleMutationTiming(peopleMutationTiming{
			Operation: "dissolve_person",
			TargetID:  personID,
			FaceCount: released,
			GateWait:  lockWait,
			Business:  time.Since(bizStart),
			Total:     time.Since(totalStart),
			Err:       err,
		})
	}()

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

	// releasedFaceIDs 记录被释放回 pending 的人脸；coreCommitted 标记核心人脸重置
	// 已落库。即便后续人物删除失败，只要人脸已释放，身份事实即已发生，须记录事件。
	// 事件保留原 personID——反馈表中的人物 ID 不依赖强外键存活，人物随后删除也不影响。
	var releasedFaceIDs []uint
	coreCommitted := false
	// personDeleted 标记人物记录是否已被删除，决定画像失效用 deleted 还是 dirty。
	// 若 Face 已释放但 Person 删除失败：人物仍存在但成员已变化，应标记 dirty。
	personDeleted := false
	defer func() {
		if coreCommitted {
			s.recordFeedbackEvent(buildFeedbackEvent(
				repository.PeopleFeedbackEventPersonDissolved,
				personID,
				nil,
				releasedFaceIDs,
				peopleManualAlgorithmVersion,
				nil,
			))
			// 统一画像失效：人物删除成功 → deleted；Face 已释放但 Person 删除失败 → dirty。
			if personDeleted {
				s.invalidateIdentityProfiles(IdentityProfileInvalidation{
					DeletedPersonIDs: []uint{personID},
					Reason:           "person_dissolved",
				})
			} else {
				s.invalidateIdentityProfiles(IdentityProfileInvalidation{
					DirtyPersonIDs: []uint{personID},
					Reason:         "person_dissolved",
				})
			}
		}
	}()

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
		releasedFaceIDs = faceIDs
		coreCommitted = true

		// 更新受影响照片的 top_person_category
		affectedPhotoIDs := make([]uint, 0, len(photoIDs))
		for pid := range photoIDs {
			affectedPhotoIDs = append(affectedPhotoIDs, pid)
		}
		if err := s.executeWrite(func() error {
			return s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs)
		}); err != nil {
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
	personDeleted = true

	// 异步触发重聚类，让 pending 人脸被重新分配
	if personDeleted {
		s.markProtoCacheDirty(nil, []uint{personID}, "dissolve_person")
	} else {
		s.markProtoCacheDirty([]uint{personID}, nil, "dissolve_person")
	}
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
	if err := photoPeopleEligibilityError(photo); err != nil {
		return err
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
		return s.executeWrite(func() error {
			return s.jobRepo.UpdateFields(activeJob.ID, updates)
		})
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
	return s.executeWrite(func() error {
		return s.jobRepo.Create(job)
	})
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
			// processJob self-manages the write gate: it releases the gate
			// during ML detection and only holds it for result writes, and
			// submits clustering to the coordinator without holding the gate.
			err = func() (retErr error) {
				defer func() {
					if r := recover(); r != nil {
						retErr = fmt.Errorf("processJob panic: %v", r)
					}
				}()
				return s.processJob(job)
			}()
			s.setBackgroundBusy(false)
			if err != nil {
				s.appendBackgroundLog(fmt.Sprintf("处理人物任务 %d 失败：%v", job.ID, err))
				s.markJobFailed(job.ID, job.PhotoID, err.Error())
			}

			s.taskMutex.Lock()
			if s.task != nil {
				s.task.CurrentPhotoID = job.PhotoID
				s.task.ProcessedJobs++
			}
			s.taskMutex.Unlock()
			continue
		}

		// No detection job — recover stale processing jobs before clustering.
		recovered, recoverErr := s.jobRepo.RecoverStaleProcessing(30 * time.Minute)
		if recoverErr != nil {
			s.appendBackgroundLog(fmt.Sprintf("恢复超时任务失败：%v", recoverErr))
		} else if recovered > 0 {
			s.appendBackgroundLog(fmt.Sprintf("已恢复 %d 个超时的检测任务", recovered))
			continue // re-check ClaimNextJob immediately
		}

		// No detection job — check for processable pending faces and cluster.
		// Use ListPending (same query as inner loop) to avoid mismatch with
		// GetPendingStats which doesn't apply backoff filtering.
		processable, listErr := s.faceRepo.ListPending(1)
		if listErr != nil {
			s.appendBackgroundLog(fmt.Sprintf("查询待聚类人脸失败：%v", listErr))
			time.Sleep(300 * time.Millisecond)
			continue
		}
		if len(processable) == 0 {
			s.setTaskState(model.TaskStatusIdle, "idle", "队列已清空，等待新任务入队", nil)
			s.idleCount++
			time.Sleep(s.idleInterval())
			continue
		}

		// Get full stats for display only (not used for control flow)
		pendingFaceStats, _ := s.faceRepo.GetPendingStats()

		// Cluster pending faces — loop directly without re-checking ClaimNextJob/ListPending
		// until no more work, then return to normal cycle.
		s.idleCount = 0
		s.setTaskState(model.TaskStatusRunning, "clustering",
			fmt.Sprintf("正在处理 %d 张待聚类人脸", pendingFaceStats.Total), nil)
		noProgressCount := 0
		for {
			active.mu.Lock()
			stopRequested := active.stop
			active.mu.Unlock()
			if stopRequested {
				return
			}

			s.setBackgroundBusy(true)
			// processPendingFaces submits clustering to the coordinator, which
			// acquires the write gate per batch. We must NOT hold the write
			// gate here while waiting for the coordinator.
			hasMore, clusterErr := func() (hm bool, ce error) {
				defer func() {
					if r := recover(); r != nil {
						ce = fmt.Errorf("processPendingFaces panic: %v", r)
					}
				}()
				return s.processPendingFaces()
			}()
			s.setBackgroundBusy(false)
			if clusterErr != nil {
				s.appendBackgroundLog(fmt.Sprintf("处理待聚类人脸失败：%v", clusterErr))
				time.Sleep(300 * time.Millisecond)
				break
			}
			if !hasMore {
				break
			}

			// Check if pending count is actually decreasing; if not, break out
			// to prevent spinning on stuck faces.
			currentStats, _ := s.faceRepo.GetPendingStats()
			if currentStats.Total >= pendingFaceStats.Total {
				noProgressCount++
				if noProgressCount >= 3 {
					break
				}
			} else {
				noProgressCount = 0
				pendingFaceStats = currentStats
			}

			time.Sleep(s.clusteringInterval())
		}
	}

}

// markJobFailed marks a processing job as failed and resets the photo's
// face_process_status so it can be re-enqueued later.
func (s *peopleService) markJobFailed(jobID uint, photoID uint, errMsg string) {
	now := time.Now()
	s.executeWrite(func() error {
		return s.db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&model.PeopleJob{}).Where("id = ? AND status = ?", jobID, model.PeopleJobStatusProcessing).
				Updates(map[string]interface{}{
					"status":       model.PeopleJobStatusFailed,
					"last_error":   errMsg,
					"completed_at": &now,
				}).Error; err != nil {
				return err
			}
			return tx.Model(&model.Photo{}).Where("id = ? AND face_process_status IN ?",
				photoID, []string{model.FaceProcessStatusPending, model.FaceProcessStatusProcessing}).
				Update("face_process_status", model.FaceProcessStatusNone).Error
		})
	})
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
	res := s.clusteringCoordinator.submitBackground()
	if res.err != nil {
		return false, res.err
	}

	hasMore := len(res.affectedPersonIDs) > 0 || len(res.affectedPhotoIDs) > 0

	for _, personID := range res.affectedPersonIDs {
		if err := s.syncPersonState(personID); err != nil {
			return false, err
		}
	}
	if len(res.affectedPhotoIDs) > 0 {
		if err := s.executeWrite(func() error {
			return s.photoRepo.RecomputeTopPersonCategory(res.affectedPhotoIDs)
		}); err != nil {
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
	if eligibilityErr := photoPeopleEligibilityError(photo); eligibilityErr != nil {
		s.appendBackgroundLog(fmt.Sprintf("照片 #%d 不满足人物识别条件，跳过：%v", photo.ID, eligibilityErr))
		if err := s.photoRepo.UpdateFields(photo.ID, map[string]interface{}{
			"face_process_status": model.FaceProcessStatusNone,
		}); err != nil {
			return nil, false, err
		}
		return nil, true, s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusCancelled,
			"last_error":   eligibilityErr.Error(),
			"completed_at": &now,
		})
	}

	existingFaces, err := s.faceRepo.ListByPhotoID(photo.ID)
	if err != nil {
		return nil, false, err
	}
	if hasManualLockedFaces(existingFaces) {
		s.appendBackgroundLog(fmt.Sprintf("照片 #%d 已有人工确认，跳过", photo.ID))
		if err := s.executeWrite(func() error {
			return s.photoRepo.RecomputeTopPersonCategory([]uint{photo.ID})
		}); err != nil {
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
// Uses the display thumbnail as input when available — it already has correct
// EXIF orientation and is sized to 1024px, ensuring face bounding boxes are
// in the same coordinate space as face thumbnail generation.
func (s *peopleService) detectFacesLocally(photo *model.Photo) (*mlclient.DetectFacesResponse, error) {
	var imageBase64 string
	var imagePath string

	// Prefer display thumbnail: already EXIF-oriented and correctly rotated.
	if thumbPath := s.displayThumbnailPath(photo); thumbPath != "" {
		if data, err := os.ReadFile(thumbPath); err == nil {
			imageBase64 = base64.StdEncoding.EncodeToString(data)
			imagePath = thumbPath
		}
	}

	// Fallback to ProcessForAI from original if thumbnail unavailable.
	if imageBase64 == "" {
		processor := util.NewImageProcessor(1024, 85)
		processedImage, processErr := processor.ProcessForAI(photo.FilePath)
		if processErr != nil {
			logger.Warnf("process photo %d for people detect failed, falling back to image path: %v", photo.ID, processErr)
		}
		if len(processedImage) > 0 {
			imageBase64 = base64.StdEncoding.EncodeToString(processedImage)
		}
		imagePath = photo.FilePath
	}

	result, detectErr := s.client.DetectFaces(context.Background(), mlclient.DetectFacesRequest{
		ImagePath:     imagePath,
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
	if job == nil || photo == nil {
		return fmt.Errorf("people job and photo are required")
	}
	currentPhoto, err := s.photoRepo.GetByID(photo.ID)
	if err != nil {
		return err
	}
	if eligibilityErr := photoPeopleEligibilityError(currentPhoto); eligibilityErr != nil {
		now := time.Now()
		if updateErr := s.jobRepo.UpdateFields(job.ID, map[string]interface{}{
			"status":       model.PeopleJobStatusCancelled,
			"last_error":   eligibilityErr.Error(),
			"completed_at": &now,
		}); updateErr != nil {
			return updateErr
		}
		return eligibilityErr
	}
	photo = currentPhoto
	now := time.Now()

	existingFaces, err := s.faceRepo.ListByPhotoID(photo.ID)
	if err != nil {
		return err
	}
	previousPersonIDs := personIDsFromFaces(existingFaces)
	// detectionReplaced 标记检测结果替换事务是否已提交。一旦为 true，即便后续 syncPersonState
	// 失败也必须触发画像失效——旧 Face 已被删除，曾归属人物的中心可能已过期。
	// 包括 0 张人脸的路径：删除旧 Face 仍使原人物失去成员。
	detectionReplaced := false
	defer func() {
		if detectionReplaced && len(previousPersonIDs) > 0 {
			// 只标记旧 Face 曾归属的人物；新 Face 尚未归属人物，不标记目标。
			// 同一人物在照片中有多张脸只标记一次（personIDsFromFaces 已去重）。
			s.invalidateIdentityProfiles(IdentityProfileInvalidation{
				DirtyPersonIDs: previousPersonIDs,
				Reason:         "detection_replaced_faces",
			})
		}
	}()

	if len(result.Faces) == 0 {
		s.appendBackgroundLog(fmt.Sprintf("照片 #%d 无人脸", photo.ID))
		s.writeGate.RLock()
		if err := s.executeWrite(func() error {
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
		}); err != nil {
			s.writeGate.RUnlock()
			return err
		}
		s.writeGate.RUnlock()
		detectionReplaced = true
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
	// Use display thumbnail as source for face thumbnails — same coordinate
	// space as face detection, already EXIF-oriented and correctly rotated.
	faceThumbnailSource := photo.FilePath
	faceThumbnailRotation := photo.ManualRotation
	if thumbPath := s.displayThumbnailPath(photo); thumbPath != "" {
		if _, err := os.Stat(thumbPath); err == nil {
			faceThumbnailSource = thumbPath
			faceThumbnailRotation = 0 // thumbnail already has manual rotation applied
		}
	}

	thumbnailPaths, err := util.GenerateFaceThumbnails(faceThumbnailSource, s.faceThumbnailRoot(), thumbnailSpecs, faceThumbnailRotation)
	if err != nil {
		return err
	}
	if len(thumbnailPaths) != len(result.Faces) {
		return fmt.Errorf("expected %d face thumbnail paths, got %d", len(result.Faces), len(thumbnailPaths))
	}

	// Load existing exclusion records for this photo to match against new detections
	var exclusionRecords []*model.FaceExclusion
	if s.faceExclusionRepo != nil {
		exclusionRecords, _ = s.faceExclusionRepo.ListByPhotoID(photo.ID)
	}

	// Build detection candidates for bbox matching
	detectionCandidates := make([]bboxCandidate, len(result.Faces))
	for i, detected := range result.Faces {
		detectionCandidates[i] = bboxCandidate{
			x: detected.BBox.X,
			y: detected.BBox.Y,
			w: detected.BBox.Width,
			h: detected.BBox.Height,
		}
	}
	exclusionMatches := matchExclusionRecords(detectionCandidates, exclusionRecords)

	faceCountForPhoto := 0
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

		// Apply exclusion if matched
		if rec, matched := exclusionMatches[i]; matched {
			face.ClusterStatus = model.FaceClusterStatusExcluded
			face.ExclusionReason = rec.Reason
			now := time.Now()
			face.ExcludedAt = &now
			// non_face does not count toward face_count; low_quality does
			if rec.Reason == model.ExclusionReasonLowQuality {
				faceCountForPhoto++
			}
		} else {
			faceCountForPhoto++
		}
		createdFaces = append(createdFaces, face)
	}

	s.writeGate.RLock()
	if err := s.executeWrite(func() error {
		return s.db.Transaction(func(tx *gorm.DB) error {
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
				"face_count":          faceCountForPhoto,
				"top_person_category": "",
			}).Error; err != nil {
				return err
			}
			return nil
		})
	}); err != nil {
		s.writeGate.RUnlock()
		return err
	}
	s.writeGate.RUnlock()
	detectionReplaced = true

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
		// Submit clustering to the coordinator without holding the write gate.
		clusterRes := s.clusteringCoordinator.submitBackground()
		affectedPersonIDs = clusterRes.affectedPersonIDs
		affectedPhotoIDs = clusterRes.affectedPhotoIDs
		clusterErr = clusterRes.err
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
	if err := s.executeWrite(func() error {
		return s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs)
	}); err != nil {
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
	entry := fmt.Sprintf("%s %s", time.Now().Format("2006-01-02 15:04:05"), message)
	s.backgroundLogMu.Lock()
	defer s.backgroundLogMu.Unlock()
	s.backgroundLogs = append(s.backgroundLogs, entry)
	if len(s.backgroundLogs) > 100 {
		s.backgroundLogs = s.backgroundLogs[len(s.backgroundLogs)-100:]
	}
}

// scheduleFeedbackRecluster requests a feedback recluster through the
// clustering coordinator. Multiple calls coalesce into at most one running
// feedback task plus one pending makeup run.
func (s *peopleService) scheduleFeedbackRecluster() {
	s.clusteringCoordinator.scheduleFeedbackRecluster()
}

// beginForegroundMutation registers an in-progress foreground people mutation
// (merge/split/move/dissolve/reset) so the clustering coordinator yields before
// starting the next clustering batch, then acquires the write gate exclusively.
// It must be paired with endForegroundMutation (typically via defer) so the
// foreground waiter count is restored on every return path, including panics.
//
// Order: increment foregroundWaiters → notify coordinator → acquire writeGate.
//
// Task 8: 同时向统一 BackgroundTaskCoordinator 注册 foreground scope，使 P2 automatic
// 后台 slice（merge suggestion / identity profile 等）在前台操作期间让路。coordinator
// 为 nil（旧测试桩）时仅走 clusteringCoordinator.foregroundWaiters 兼容桥。
// release 顺序与获取相反：先释放 writeGate，再 removeForegroundWaiter，最后 release
// coordinator foreground scope——保证 coordinator 在 writeGate 释放后才看到 foreground
// 结束，避免后台 slice 在 writeGate 仍持有时抢跑。
func (s *peopleService) beginForegroundMutation() {
	s.clusteringCoordinator.addForegroundWaiter()
	s.writeGate.Lock()
	s.acquireCoordinatorForeground()
}

// beginForegroundMutationTimed is like beginForegroundMutation but returns the
// time spent blocked acquiring writeGate.Lock() (the foreground wait), for
// merge-task observability.
func (s *peopleService) beginForegroundMutationTimed() time.Duration {
	s.clusteringCoordinator.addForegroundWaiter()
	lockStart := time.Now()
	s.writeGate.Lock()
	s.acquireCoordinatorForeground()
	return time.Since(lockStart)
}

// acquireCoordinatorForeground 向统一 coordinator 注册 foreground scope（若已注入）。
// 返回的 release 由 endForegroundMutation 在释放 writeGate 之后调用。coordinator 为
// nil 时为 no-op。release 用 sync.Once 保护，多次调用安全（defer + 显式调用混合场景）。
func (s *peopleService) acquireCoordinatorForeground() {
	if s.backgroundCoordinator == nil {
		s.fgCoordinatorRelease = nil
		return
	}
	s.fgCoordinatorRelease = s.backgroundCoordinator.BeginForeground()
}

// endForegroundMutation releases the write gate and deregisters the foreground
// waiter, waking the coordinator so it can resume clustering.
func (s *peopleService) endForegroundMutation() {
	s.writeGate.Unlock()
	s.clusteringCoordinator.removeForegroundWaiter()
	s.releaseCoordinatorForeground()
}

// releaseCoordinatorForeground 释放统一 coordinator foreground scope（若持有）。
func (s *peopleService) releaseCoordinatorForeground() {
	release := s.fgCoordinatorRelease
	s.fgCoordinatorRelease = nil
	if release != nil {
		release()
	}
}

// isHiddenPersonsLoaded returns whether InitHiddenPersons has successfully loaded
// the hidden person ID set from the database.
func (s *peopleService) isHiddenPersonsLoaded() bool {
	s.hiddenPersonsMu.RLock()
	defer s.hiddenPersonsMu.RUnlock()
	return s.hiddenPersonsLoaded
}

// InitHiddenPersons loads hidden=true person IDs from the database into the
// runtime block-set. Called during NewServices wireup. On failure, sets
// hiddenPersonsLoaded=false so StartBackground refuses to start (fail-closed).
func (s *peopleService) InitHiddenPersons() error {
	ids, err := s.personRepo.ListHiddenPersonIDs()
	if err != nil {
		logger.Errorf("Failed to load hidden person IDs: %v", err)
		s.hiddenPersonsMu.Lock()
		s.hiddenPersonsLoaded = false
		s.hiddenPersonsMu.Unlock()
		return fmt.Errorf("load hidden person IDs: %w", err)
	}
	m := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}
	s.hiddenPersonsMu.Lock()
	s.hiddenPersonIDs = m
	s.hiddenPersonsLoaded = true
	s.hiddenPersonsMu.Unlock()
	logger.Infof("Loaded %d hidden person IDs into runtime block-set", len(ids))
	return nil
}

// addToHiddenSet adds person IDs to the runtime block-set. Caller must hold writeGate.
func (s *peopleService) addToHiddenSet(ids []uint) {
	s.hiddenPersonsMu.Lock()
	defer s.hiddenPersonsMu.Unlock()
	if s.hiddenPersonIDs == nil {
		s.hiddenPersonIDs = make(map[uint]struct{})
	}
	for _, id := range ids {
		s.hiddenPersonIDs[id] = struct{}{}
	}
}

// removeFromHiddenSet removes person IDs from the runtime block-set. Caller must hold writeGate.
func (s *peopleService) removeFromHiddenSet(ids []uint) {
	s.hiddenPersonsMu.Lock()
	defer s.hiddenPersonsMu.Unlock()
	if s.hiddenPersonIDs == nil {
		return
	}
	for _, id := range ids {
		delete(s.hiddenPersonIDs, id)
	}
}

// mergeHiddenPersonsIntoBlocked merges the runtime hidden person set into the
// given blockedPersons map. Called from attachComponent callers to ensure
// hidden persons are never matched, even if protoCache still contains them.
func (s *peopleService) mergeHiddenPersonsIntoBlocked(blockedPersons map[uint]bool) {
	s.hiddenPersonsMu.RLock()
	defer s.hiddenPersonsMu.RUnlock()
	for id := range s.hiddenPersonIDs {
		blockedPersons[id] = true
	}
}

// UpdateVisibility 批量设置人物隐藏状态。
//
// 核心顺序约束：先更新 DB → 再更新内存阻断集合。DB 失败直接返回 error 不动内存，
// 保证幂等性。隐藏时触发 protoCacheDirty（移除隐藏人物）；恢复时触发 protoCacheDirty
// （重新装入可见人物）。隐藏时调用 ANN InvalidatePerson（内存操作，不删 profile）；
// 恢复时标记 profile dirty。
//
// 去重/500 限制/404 判断留在 handler 层。Service 返回 (updated, err)。
func (s *peopleService) UpdateVisibility(personIDs []uint, hidden bool) (updated int64, err error) {
	if len(personIDs) == 0 {
		return 0, nil
	}

	totalStart := time.Now()
	lockWait := s.beginForegroundMutationTimed()
	defer s.endForegroundMutation()

	// 1. 先更新 DB
	updated, err = s.personRepo.UpdateVisibility(personIDs, hidden)
	if err != nil {
		return 0, fmt.Errorf("update visibility: %w", err)
	}
	if updated == 0 {
		return 0, nil
	}

	// Determine which IDs were actually affected (only those that exist in DB).
	// personRepo.UpdateVisibility returns total rows affected, not the specific IDs.
	// We add/remove all requested IDs to/from the block-set; IDs that weren't in DB
	// are harmless no-ops in the set.
	if hidden {
		s.addToHiddenSet(personIDs)
	} else {
		s.removeFromHiddenSet(personIDs)
	}

	// 2. Trigger protoCache dirty so the cache is refreshed (hidden persons removed
	// or restored persons re-added) at the next safe opportunity.
	s.markProtoCacheDirty(personIDs, nil, "person_visibility_changed")

	// 3. ANN / identity profile handling
	if hidden {
		// InvalidatePerson is a pure in-memory ANN operation (no DB writes).
		// Do not mark profile dirty — hidden persons should not pollute dirty stats.
		if s.identityProfileANNInvalidateFn != nil {
			s.identityProfileANNInvalidateFn(personIDs)
		}
	} else {
		// Restore: mark profile dirty so background scheduler rebuilds and reactivates.
		if s.identityProfileMarkDirtyFn != nil {
			_ = s.identityProfileMarkDirtyFn(personIDs, "person_visibility_changed")
		}
	}

	// 4. Merge suggestion dirty
	if s.mergeSuggestionDirty != nil {
		_ = s.mergeSuggestionDirty("person_visibility_changed")
	}

	elapsed := time.Since(totalStart)
	action := "restore"
	if hidden {
		action = "hide"
	}
	logger.Infof("UpdateVisibility action=%s requested=%d updated=%d elapsed=%v gate_wait=%v",
		action, len(personIDs), updated, elapsed, lockWait)

	return updated, nil
}

// peopleMutationTiming captures the timing breakdown of a single foreground
// people mutation (split/move/merge/dissolve/assign) for structured observability.
// The coordinator uses foreground-vs-background priority decisions that can make
// writeGate wait non-trivial; recording the split between gate wait and business
// work keeps that cost visible without changing behavior.
//
// split_person 额外记录归属与结果字段：
//   - SourcePersonID：页面提交的来源人物；
//   - CurrentPersonIDs：请求 face 当前归属的去重非零集合（数量 + 归属，不输出 embedding/向量）；
//   - ReplayTargetPersonID：命中幂等重放时返回的已有目标人物，否则为 0；
//   - Result：created | replayed | conflict | failed。
type peopleMutationTiming struct {
	Operation            string
	TargetID             uint
	SourcePersonID       uint
	FaceCount            int
	CurrentPersonIDs     []uint
	ReplayTargetPersonID uint
	Result               string
	GateWait             time.Duration
	Business             time.Duration
	Total                time.Duration
	Err                  error
}

// logPeopleMutationTiming emits a single structured line per foreground mutation.
// It follows the existing MergePeople field naming (writeGateWaitMs/businessMs/
// totalMs) so all foreground mutations share one parseable format. For merge and
// dissolve the face-count field is reported as sourceCount; otherwise faceCount.
//
// split_person 在通用字段后追加归属/重放/结果字段。冲突日志只输出数量与人物归属集合，
// 绝不输出完整 embedding 或人脸向量；face_ids 不逐条输出，优先记录数量。
func logPeopleMutationTiming(t peopleMutationTiming) {
	countField := "faceCount"
	if t.Operation == "merge_people" || t.Operation == "dissolve_person" {
		countField = "sourceCount"
	}
	// 通用 timing 行（保持既有格式与解析契约）。
	if t.Err != nil {
		logger.Warnf("people_foreground_mutation operation=%s target_id=%d %s=%d writeGateWaitMs=%d businessMs=%d totalMs=%d err=%v",
			t.Operation, t.TargetID, countField, t.FaceCount, t.GateWait.Milliseconds(), t.Business.Milliseconds(), t.Total.Milliseconds(), t.Err)
	} else {
		logger.Infof("people_foreground_mutation operation=%s target_id=%d %s=%d writeGateWaitMs=%d businessMs=%d totalMs=%d",
			t.Operation, t.TargetID, countField, t.FaceCount, t.GateWait.Milliseconds(), t.Business.Milliseconds(), t.Total.Milliseconds())
	}
	// split_person 专属可观测性行：归属 + 结果，不含 embedding/向量/逐条 face_ids。
	if t.Operation != "split_person" {
		return
	}
	currentOwners := dedupSortedUint(t.CurrentPersonIDs)
	if t.Err != nil {
		logger.Warnf("people_split_detail source_person_id=%d face_count=%d current_person_ids=%v replay_target_person_id=%d result=%s err=%v",
			t.SourcePersonID, t.FaceCount, currentOwners, t.ReplayTargetPersonID, t.Result, t.Err)
		return
	}
	logger.Infof("people_split_detail source_person_id=%d face_count=%d current_person_ids=%v replay_target_person_id=%d result=%s",
		t.SourcePersonID, t.FaceCount, currentOwners, t.ReplayTargetPersonID, t.Result)
}

// dedupSortedUint 返回去重升序的 uint 切片，仅用于日志输出，避免 map 迭代顺序不确定。
func dedupSortedUint(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (s *peopleService) setFeedbackReclusterHookForTest(hook func() model.ReclusterResult) {
	s.clusteringCoordinator.setFeedbackHook(hook)
}

// setProtoCacheBuildHookForTest 注入一个在 buildClustProtoCache 内调用的 hook，仅供测试
// 用于模拟慢速 proto-cache refresh（Task 9 目标测试）。hook 在不持有 writeGate 时调用，
// hook 阻塞期间 foreground 应能获取 writeGate.Lock()，证明 refresh 已移出 writeGate。
func (s *peopleService) setProtoCacheBuildHookForTest(hook func()) {
	s.protoCacheBuildHook = hook
}

func (s *peopleService) setFeedbackCooldownForTest(d time.Duration) {
	s.clusteringCoordinator.setFeedbackCooldown(d)
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

// AcquireWriteGate locks the write gate exclusively as a foreground mutation
// and returns a release function. Foreground mutations (merge/split/move and
// suggestion-based merges) call this so the clustering coordinator yields
// before writing faces/people tables.
//
// Task 8: 同时向统一 BackgroundTaskCoordinator 注册 foreground scope，release 顺序与
// AcquireWriteGate 内部相反：先 Unlock writeGate，再 removeForegroundWaiter，最后
// release coordinator scope。
func (s *peopleService) AcquireWriteGate() func() {
	s.clusteringCoordinator.addForegroundWaiter()
	s.writeGate.Lock()
	s.acquireCoordinatorForeground()
	once := sync.Once{}
	return func() {
		once.Do(func() {
			s.writeGate.Unlock()
			s.clusteringCoordinator.removeForegroundWaiter()
			s.releaseCoordinatorForeground()
		})
	}
}

// PostMergeCleanup performs post-merge cleanup for suggestion-based merges.
// It mirrors the cleanup steps in MergePeople that ApplySuggestion would otherwise miss.
func (s *peopleService) PostMergeCleanup(targetPersonID uint, sourcePersonIDs []uint, affectedPhotoIDs []uint) {
	// Clean up cannot-link constraints for merged (deleted) persons
	for _, sourceID := range sourcePersonIDs {
		if err := s.executeWrite(func() error {
			return s.cannotLinkRepo.DeleteByPersonID(sourceID)
		}); err != nil {
			logger.Warnf("failed to clean cannot-link for merged person %d: %v", sourceID, err)
		}
	}
	// Sync target person state (updates avatar, refreshes stats)
	if err := s.syncPersonState(targetPersonID); err != nil {
		logger.Warnf("post-merge syncPersonState failed for target %d: %v", targetPersonID, err)
	}
	// Recompute top_person_category on affected photos
	if len(affectedPhotoIDs) > 0 {
		if err := s.executeWrite(func() error {
			return s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs)
		}); err != nil {
			logger.Warnf("post-merge RecomputeTopPersonCategory failed: %v", err)
		}
	}
	s.scheduleFeedbackRecluster()

	// Task 13：合并建议 Apply 通过与手工合并相同的统一失效路径触发画像失效，
	// 避免 merge suggestion service 直接操作画像 Repository，且只产生一次失效。
	// 核心合并已由 ApplySuggestion 提交（调用本方法时 coreCommitted=true），
	// 后续建议状态/分类刷新失败仍执行失效。target dirty、candidates deleted。
	s.invalidateIdentityProfiles(IdentityProfileInvalidation{
		DirtyPersonIDs:   []uint{targetPersonID},
		DeletedPersonIDs: sourcePersonIDs,
		Reason:           "people_merged",
	})
	s.markProtoCacheDirty([]uint{targetPersonID}, sourcePersonIDs, "merge_suggestion_apply")
}

func (s *peopleService) setMergeSuggestionDirtyHook(hook func(string) error) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()
	s.mergeSuggestionDirty = hook
}

func (s *peopleService) setANNCandidateFn(fn func(probes []faceWithEmbedding, k int) map[uint]struct{}) {
	s.annCandidateFn = fn
}

// identityProfileMatchFn 是身份画像 matcher 的可注入抽象，使 peopleService 不直接
// 依赖 *IdentityProfileMatcher 实现。生产由 service.go 注入 matcher.Match，测试可
// 注入 fake 以断言调用次数与输入。函数必须不访问 peopleService 的可变聚类缓存
// （protoCache 等）；其 panic 由 processIdentityShadowObservations 恢复。
type identityProfileMatchFn func(component []*model.Face) IdentityProfileMatch

// identityDecisionRecordFn 是 Task 9 遥测记录器的可注入抽象。生产注入
// telemetry.Record，best-effort、不返回错误、不重试、不持有 writeGate。
type identityDecisionRecordFn func(input IdentityTelemetryInput)

// identityProfileMarkDirtyFn 是 Task 12 rescue 成功后标记目标人物画像 dirty 的窄接口。
// 生产注入 identityProfileService.MarkDirty（仅非 legacy 模式）；rescue 在释放 writeGate
// 之后 best-effort 调用，失败只记录脱敏 warning，不回滚 rescue 挂靠、不返回聚类错误。
type identityProfileMarkDirtyFn func(personIDs []uint, reason string) error

// SetIdentityProfileShadowHooks 注入身份画像 shadow 模式所需的模式与回调。生产由
// service.go 装配时调用一次；测试可注入 fake matcher / recorder。
//
// legacy 模式（mode=="" 或 model.PeopleIdentityModeLegacy）不保存任何 hook：保持
// matchFn/recordFn 为 nil，runIncrementalClustering 不分配 observation slice、不
// 复制 embedding，processIdentityShadowObservations 直接返回。
func (s *peopleService) SetIdentityProfileShadowHooks(mode string, matchFn identityProfileMatchFn, recordFn identityDecisionRecordFn) {
	s.identityProfileMode = mode
	if mode == "" || mode == model.PeopleIdentityModeLegacy {
		s.identityProfileMatchFn = nil
		s.identityDecisionRecordFn = nil
		return
	}
	s.identityProfileMatchFn = matchFn
	s.identityDecisionRecordFn = recordFn
}

// SetIdentityProfileDirtyHook 注入 Task 12 rescue 成功后标记目标人物画像 dirty 的回调。
// 仅非 legacy 模式注入（rescue 真正会应用结果；shadow/primary 注入也无害，因这两模式
// 不产生 RescueApplied=true）。生产由 service.go 装配时调用一次；测试可注入 fake 以断言
// 仅目标人物被标记。nil 时 rescue 挂靠仍成功，仅跳过 dirty 标记。
func (s *peopleService) SetIdentityProfileDirtyHook(fn identityProfileMarkDirtyFn) {
	s.identityProfileMarkDirtyFn = fn
}

// identityProfileInvalidateFn 是 Task 13 统一身份画像失效 hook 的可注入抽象。生产由
// service.go 注入 identityProfileService.Invalidate（仅非 legacy 模式）；测试可注入 fake
// 断言调用次数与输入。hook 必须 fail-closed：失败返回错误但不得回滚已提交的业务身份变更。
type identityProfileInvalidateFn func(invalidation IdentityProfileInvalidation) error

// SetIdentityProfileInvalidationHook 注入 Task 13 统一身份画像失效 hook。生产由 service.go
// 装配时调用一次（仅非 legacy 模式）；测试可注入 fake 断言调用次数。nil 时
// invalidateIdentityProfiles 直接返回不产生任何开销。
func (s *peopleService) SetIdentityProfileInvalidationHook(fn identityProfileInvalidateFn) {
	s.identityProfileInvalidateFn = fn
}

// SetIdentityProfileANNInvalidateFn 注入 ANN-only 失效 hook（人物隐藏专用）。
// 生产由 service.go 装配时注入 identityProfileService.InvalidateANNOnly。
func (s *peopleService) SetIdentityProfileANNInvalidateFn(fn func(personIDs []uint)) {
	s.identityProfileANNInvalidateFn = fn
}

// invalidateIdentityProfiles 是所有业务路径触发身份画像失效的统一入口。
//
// 行为约定（Task 13 第四节）：
//   - nil hook 安全返回（legacy 模式或未注入）。
//   - legacy 模式不调用（hook 在 legacy 模式下应为 nil，但即便非 nil 也由 hook 自身 no-op）。
//   - recover hook panic，当前业务操作不崩溃，ANN 已完成的同步失效保持。
//   - 失败只记录脱敏 warning（reason、ID 数量、错误类别，不含具体 ID/路径/人名），不重试。
//   - 不在现有人物事务内调用（调用方必须在 executeWrite 回调之外、核心写入提交之后调用）。
//   - 不因失效失败伪装业务回滚：已提交的身份事实保持不变。
func (s *peopleService) invalidateIdentityProfiles(invalidation IdentityProfileInvalidation) {
	fn := s.identityProfileInvalidateFn
	if fn == nil {
		return
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Warnf("identity profile invalidate panic: reason=%s dirty=%d deleted=%d reset=%v err_category=%T",
					invalidation.Reason, len(invalidation.DirtyPersonIDs), len(invalidation.DeletedPersonIDs), invalidation.ResetAll, r)
			}
		}()
		if err := fn(invalidation); err != nil {
			logger.Warnf("identity profile invalidate failed: reason=%s dirty=%d deleted=%d reset=%v err_category=%T",
				invalidation.Reason, len(invalidation.DirtyPersonIDs), len(invalidation.DeletedPersonIDs), invalidation.ResetAll, err)
		}
	}()
}

// SetFeedbackEventRepo 注入反馈事件仓库。生产环境由 service.go 装配时注入；
// 测试可注入失败 stub 或 nil（nil 时反馈记录被静默跳过）。
func (s *peopleService) SetFeedbackEventRepo(repo repository.PeopleFeedbackEventRepository) {
	s.feedbackEventRepo = repo
}

// SetBackgroundCoordinator 注入后台任务治理准入控制器。生产由 service.go 装配时注入；
// nil 时前台 mutation 退化为仅使用 clusteringCoordinator.foregroundWaiters 兼容桥（Task 8
// 之前的既有行为），保证旧测试桩不依赖 coordinator 也能工作。
func (s *peopleService) SetBackgroundCoordinator(c *BackgroundTaskCoordinator) {
	s.backgroundCoordinator = c
}

// ProtoCacheRebuildStatus 返回 protoCache 分批 full rebuild 的只读进度快照，供后台状态 API
// 展示。线程安全（rebuildSnapshot 在 coordinator.mu 下读取）。nil 表示当前无 rebuild。
// cold_building=true 表示系统无可用旧缓存且 rebuild 进行中（running/paused），前端据此区分
// 冷启动构建与普通后台 refresh。
func (s *peopleService) ProtoCacheRebuildStatus() *model.ProtoCacheRebuildStatusResponse {
	if s.clusteringCoordinator == nil {
		return nil
	}
	state, gen, cursor, total, batches, reason, cold := s.clusteringCoordinator.rebuildSnapshot()
	if state == rebuildStateIdle {
		return nil
	}
	return &model.ProtoCacheRebuildStatusResponse{
		Generation:   gen,
		State:        string(state),
		ColdBuilding: cold,
		Cursor:       cursor,
		Total:        total,
		Batches:      batches,
		PauseReason:  reason,
	}
}

// recordFeedbackEvent 在核心人物变更已提交后单独写入一条反馈事件。必须在任何
// executeWrite 回调之外调用（由各业务方法在核心写入完成后调用），避免 WriteQueue
// 重入死锁。写入失败仅记录脱敏 warning，不影响已成功的业务结果。
func (s *peopleService) recordFeedbackEvent(event *model.PeopleFeedbackEvent) {
	emitFeedbackEvent(s.feedbackEventRepo, s.executeWrite, event)
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
	// Prefer display thumbnail — already EXIF-oriented and correctly rotated.
	sourcePath := photo.FilePath
	rotation := photo.ManualRotation
	if thumbPath := s.displayThumbnailPath(photo); thumbPath != "" {
		if _, err := os.Stat(thumbPath); err == nil {
			sourcePath = thumbPath
			rotation = 0
		}
	}
	return util.GenerateFaceThumbnail(sourcePath, s.faceThumbnailRoot(), bbox.X, bbox.Y, bbox.Width, bbox.Height, rotation)
}

func (s *peopleService) faceThumbnailRoot() string {
	if s.config != nil && strings.TrimSpace(s.config.Photos.ThumbnailPath) != "" {
		return s.config.Photos.ThumbnailPath
	}
	return "./data/thumbnails"
}

// displayThumbnailPath returns the full path of the photo's display thumbnail,
// or empty string if unavailable. The display thumbnail is already EXIF-oriented
// and resized to 1024px, making it suitable as the source for face detection
// and face thumbnail generation (consistent coordinate space, correct rotation).
func (s *peopleService) displayThumbnailPath(photo *model.Photo) string {
	if photo == nil || strings.TrimSpace(photo.ThumbnailPath) == "" {
		return ""
	}
	return filepath.Join(s.faceThumbnailRoot(), photo.ThumbnailPath)
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

// topKFacesByQuality returns the k faces with the highest QualityScore from the component.
func topKFacesByQuality(component []faceWithEmbedding, k int) []faceWithEmbedding {
	if len(component) <= k {
		return component
	}
	sorted := make([]faceWithEmbedding, len(component))
	copy(sorted, component)
	sort.Slice(sorted, func(i, j int) bool {
		qi := 0.0
		if sorted[i].face != nil {
			qi = sorted[i].face.QualityScore
		}
		qj := 0.0
		if sorted[j].face != nil {
			qj = sorted[j].face.QualityScore
		}
		return qi > qj
	})
	return sorted[:k]
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

	// Merge runtime hidden person block-set into cannot-link block-set.
	s.mergeHiddenPersonsIntoBlocked(blockedPersons)

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

	// Fallback: re-score using only the top-K highest-quality faces in the component.
	// Low-quality faces (blurry, side-angle) drag down the average score, causing
	// splits where a component should have attached to an existing person.
	// If the top-K faces score above the threshold, attach anyway.
	if bestPersonID != 0 && len(component) > attachTopK {
		topK := topKFacesByQuality(component, attachTopK)
		topKScore := s.scoreComponentAgainstPersonWithEmbeddings(topK, prototypesWithEmb[bestPersonID])
		if topKScore >= attachThreshold {
			return bestPersonID, topKScore, true
		}
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

// attachComponentToPerson 是 legacy attach 与 Task 12 rescue attach 共用的统一挂靠方法。
// 它把整个连通组件一次性 UpdateClusterFields 到目标人物，并同步内存中的 component face
// 状态，返回需要后续 syncPersonState 的旧人物 ID（去重，不含目标人物本身）。
//
// 职责边界（Task 12 第五节）：
//   - 一次调用 UpdateClusterFields 更新整个组件（person_id / cluster_status=assigned /
//     cluster_score=score / clustered_at=now / retry_count=0）。
//   - 同步内存 component face 字段。
//   - 不更新 profile、不执行 profile matcher、不调用 syncPersonState（由调用方在批次末尾
//     统一处理 affectedPersonIDs）、不持有额外事务。
//
// legacy attach 传入 legacy score；rescue attach 传入 profile.Score。两者写入路径完全一致，
// 避免复制两份数据库与内存更新逻辑。
func (s *peopleService) attachComponentToPerson(
	component []*model.Face,
	personID uint,
	score float64,
) ([]uint, error) {
	ids := faceIDs(component)
	if len(ids) == 0 {
		return nil, nil
	}

	now := time.Now()
	// 收集组件原有 Person ID，供调用方 syncPersonState。
	prevPersonIDs := make(map[uint]struct{})
	for _, face := range component {
		if face == nil || face.PersonID == nil {
			continue
		}
		if pid := *face.PersonID; pid != 0 && pid != personID {
			prevPersonIDs[pid] = struct{}{}
		}
	}

	if err := s.executeWrite(func() error {
		return s.faceRepo.UpdateClusterFields(ids, map[string]interface{}{
			"person_id":      personID,
			"cluster_status": model.FaceClusterStatusAssigned,
			"cluster_score":  score,
			"clustered_at":   &now,
			"retry_count":    0,
		})
	}); err != nil {
		return nil, err
	}

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

	return mapKeys(prevPersonIDs), nil
}

func (s *peopleService) markComponentPending(component []*model.Face, score float64) error {
	ids := faceIDs(component)
	if len(ids) == 0 {
		return nil
	}

	now := time.Now()
	// 增加重试次数（用于退避策略）
	if err := s.executeWrite(func() error {
		return s.db.Model(&model.Face{}).Where("id IN ?", ids).Updates(map[string]interface{}{
			"person_id":      nil,
			"cluster_status": model.FaceClusterStatusPending,
			"cluster_score":  score,
			"clustered_at":   &now,
			"retry_count":    gorm.Expr("retry_count + 1"),
		}).Error
	}); err != nil {
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
	if err := s.executeWrite(func() error {
		return s.db.Transaction(func(tx *gorm.DB) error {
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
		})
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

// runIncrementalClustering clusters one batch of pending faces.
//
// CALLER CONTRACT: this method is the sole consumer of s.protoCache and may
// only be invoked from the clustering coordinator worker goroutine (via
// peopleClusteringCoordinator.runClusterBatch). No other goroutine may call
// it. This keeps protoCache single-goroutine and avoids concurrent access.
//
// 返回值除 affectedPersonIDs / affectedPhotoIDs / err 外，还携带本批次的 identity
// shadow observations。observations 必须由调用方在释放 writeGate.RLock 后处理
// （画像 matcher 与遥测不得在持有 writeGate 时执行）。legacy 模式不分配该 slice。
// protoCacheNeedsRefresh 报告 prototype cache 是否需要重建（nil 或已过 TTL）。
// 仅供 people clustering coordinator worker 在获取 writeGate.RLock 前调用，决定是否
// 在锁外同步构建。读取 s.protoCache 无需同步——它只由同一 worker goroutine 读写。
func (s *peopleService) protoCacheNeedsRefresh() bool {
	if s.protoCache == nil {
		return true
	}
	// Safety TTL: long-period fallback only.
	if time.Since(s.protoCache.builtAt) > clustProtoCacheTTL {
		return true
	}
	// Dirty-driven: any pending foreground changes need a refresh.
	if s.protoCacheDirty != nil {
		d := s.protoCacheDirty
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.fullRebuildNeeded || len(d.dirtyPersonIDs) > 0 || len(d.deletedPersonIDs) > 0 {
			return true
		}
	}
	return false
}

// buildClustProtoCache 在不持有 writeGate 的情况下构建一份全新的 prototype cache。
// 列出 assigned person IDs → 取 prototype embeddings → 选择 prototypes → 解码 embeddings。
// 纯读取 + CPU，不写 DB、不持有 writeGate。调用方（people clustering coordinator worker）
// 负责把返回的 cache 赋值给 s.protoCache，保持 protoCache single-owner 不变量。
//
// Task 9：把 protoCache rebuild 从 runIncrementalClustering 的 writeGate.RLock 临界区
// 拆出，使慢速 refresh 不再阻塞 foreground merge/split/move。
func (s *peopleService) buildClustProtoCache() (*clustProtoCache, error) {
	rebuildStart := time.Now()
	assignedPersonIDs, err := s.faceRepo.ListAssignedPersonIDs()
	if err != nil {
		return nil, err
	}
	var protoFaces []*model.Face
	if len(assignedPersonIDs) > 0 {
		protoFaces, err = s.faceRepo.ListPrototypeEmbeddings(assignedPersonIDs, peoplePrototypeCandidates)
		if err != nil {
			return nil, err
		}
	}
	orig := s.selectPersonPrototypes(protoFaces, peoplePrototypeCount)
	// 测试 hook：模拟慢速 refresh。在 writeGate 外调用，foreground 可在此期间获取写锁。
	if s.protoCacheBuildHook != nil {
		s.protoCacheBuildHook()
	}
	withEmb := make(map[uint][]faceWithEmbedding, len(orig))
	for personID, protoList := range orig {
		withEmb[personID] = decodeFacesWithEmbeddings(protoList)
	}
	logger.Infof("people clustering: protoCache rebuilt persons=%d prototypes=%d elapsed=%s",
		len(orig), len(protoFaces), time.Since(rebuildStart).Round(time.Millisecond))
	return &clustProtoCache{
		prototypesWithEmb: withEmb,
		prototypesOrig:    orig,
		builtAt:           time.Now(),
	}, nil
}

// buildClustProtoCacheBatch loads prototype candidates for a subset of person IDs,
// selects prototypes, and decodes embeddings. It is the batch-level building block
// for the incremental (batched) full rebuild. The caller (coordinator) accumulates
// the returned maps into a staging cache and swaps atomically when all batches are done.
//
// Does NOT touch s.protoCache — the caller owns the staging accumulation and swap.
// Does NOT hold writeGate. Pure read + CPU.
func (s *peopleService) buildClustProtoCacheBatch(personIDs []uint) (withEmb map[uint][]faceWithEmbedding, orig map[uint][]*model.Face, err error) {
	if len(personIDs) == 0 {
		return nil, nil, nil
	}
	protoFaces, err := s.faceRepo.ListPrototypeEmbeddings(personIDs, peoplePrototypeCandidates)
	if err != nil {
		return nil, nil, err
	}
	orig = s.selectPersonPrototypes(protoFaces, peoplePrototypeCount)
	withEmb = make(map[uint][]faceWithEmbedding, len(orig))
	for personID, protoList := range orig {
		withEmb[personID] = decodeFacesWithEmbeddings(protoList)
	}
	// Test hook: same as buildClustProtoCache, allows tests to simulate slow refresh
	// and verify foreground does not block during batched rebuild.
	if s.protoCacheBuildHook != nil {
		s.protoCacheBuildHook()
	}
	return withEmb, orig, nil
}

func (s *peopleService) runIncrementalClustering() ([]uint, []uint, []identityShadowObservation, error) {
	pendingFaces, err := s.faceRepo.ListPending(peopleClusteringBatchSize)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(pendingFaces) == 0 {
		return nil, nil, nil, nil
	}

	// protoCache must be pre-built by the coordinator worker (runClusterBatch) before
	// calling runIncrementalClustering. The implicit buildClustProtoCache fallback has
	// been removed to prevent uncontrolled rebuilds inside writeGate.RLock. If protoCache
	// is nil, return an error so the caller (coordinator) can handle it by refreshing
	// outside the gate before retrying.
	if s.protoCache == nil {
		return nil, nil, nil, fmt.Errorf("protoCache is nil: coordinator must refresh before clustering")
	}
	prototypesWithEmb := s.protoCache.prototypesWithEmb
	prototypes := s.protoCache.prototypesOrig

	graph := s.buildFaceGraph(pendingFaces)
	components := s.findConnectedComponents(graph)
	logger.Infof("people clustering: batch faces=%d components=%d", len(pendingFaces), len(components))
	pendingByID := make(map[uint]*model.Face, len(pendingFaces))
	for _, face := range pendingFaces {
		if face == nil || face.ID == 0 {
			continue
		}
		pendingByID[face.ID] = face
	}

	affectedPersonIDs := make(map[uint]struct{})
	affectedPhotoIDs := make(map[uint]struct{})
	// shadow observations 只在非 legacy 模式下收集，避免 legacy 分配 slice 与复制
	// embedding。nil slice 在 processIdentityShadowObservations 中无副作用。
	shadowEnabled := s.identityShadowEnabled()
	var shadowObservations []identityShadowObservation
	if shadowEnabled {
		shadowObservations = make([]identityShadowObservation, 0, len(components))
	}

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

		// Merge runtime hidden person block-set into cannot-link block-set.
		s.mergeHiddenPersonsIntoBlocked(blockedPersons)

		personID, score, attached := s.attachComponentToExistingPersonWithEmbeddings(
			componentWithEmb, prototypesWithEmb, blockedPersons, prototypes, s.effectiveAttachThreshold(maxRetry),
		)
		componentScore := nonNegativeScore(score)

		// Shadow: 在任何 legacy 写入之前深拷贝组件，并记录 legacy 决策快照。observation
		// 不参与分支选择；只有对应 legacy 操作成功后才进入待处理列表。画像 matcher 不得
		// 在 executeWrite 闭包中执行（见 processIdentityShadowObservations，在 writeGate
		// 释放后运行）。rescue 模式下 legacy miss 会在持锁阶段同步计算 profile 并填入
		// observation，post-lock 阶段复用，避免重复调用 matcher。
		var shadowObs *identityShadowObservation
		if shadowEnabled {
			shadowObs = &identityShadowObservation{
				Component: cloneComponentForShadow(component),
				Legacy: legacyIdentityResult{
					Matched:  attached,
					PersonID: personID,
					Score:    nonNegativeScore(score),
				},
			}
		}

		if attached {
			// legacy 成功永远优先：绝不调用 rescue 决策，即使 profile 更喜欢另一个人物。
			prevIDs, err := s.attachComponentToPerson(component, personID, componentScore)
			if err != nil {
				return nil, nil, nil, err
			}
			affectedPersonIDs[personID] = struct{}{}
			for _, pid := range prevIDs {
				affectedPersonIDs[pid] = struct{}{}
			}
			for _, photoID := range facePhotoIDs(component) {
				affectedPhotoIDs[photoID] = struct{}{}
			}
			// legacy attach 仍可在 post-lock 阶段做 Task 11 shadow 比对（释放 writeGate 后），
			// 但绝不应用 profile 结果。matcher 在 post-lock 调用，不在持锁阶段。
			if shadowObs != nil {
				shadowObservations = append(shadowObservations, *shadowObs)
			}
			continue
		}

		// legacy miss：rescue 模式在此边界（fallback 写入之前）尝试 profile 救回。
		// 仅 rescue 模式调用 matcher；shadow/primary 仍只在 post-lock 做 shadow 计算与记录。
		// matcher 在 writeGate.RLock 保护范围内执行，但不在 executeWrite 闭包中（不持有
		// SQLite 写事务），且每个 legacy miss 最多调用一次。
		if shadowObs != nil && s.identityRescueEnabled() {
			rescued, rescueErr := s.attemptIdentityRescue(component, shadowObs, affectedPersonIDs, affectedPhotoIDs)
			if rescueErr != nil {
				return nil, nil, nil, rescueErr
			}
			if rescued {
				// rescue 已挂靠并记录 observation（含 ProfileComputed=true、RescueApplied=true）。
				// Task 13：rescue 目标人物的画像 dirty 由统一批次路径（runClusterBatch 末尾的
				// clustering_assignment 失效）完成，rescue 不再通过独立 hook 重复 MarkDirty。
				// rescue_attach reason 仅保留为 observation/遥测原因。
				shadowObservations = append(shadowObservations, *shadowObs)
				continue
			}
			// rescue 拒绝/不可用 → profile 已填入 observation，继续 legacy fallback。
		}

		if len(component) >= peopleMinClusterFaces && componentPhotoCount(component) >= 2 {
			person, err := s.createPersonFromComponent(component, componentScore)
			if err != nil {
				return nil, nil, nil, err
			}
			if person != nil && person.ID != 0 {
				affectedPersonIDs[person.ID] = struct{}{}
			}
			for _, photoID := range facePhotoIDs(component) {
				affectedPhotoIDs[photoID] = struct{}{}
			}
			if shadowObs != nil {
				shadowObservations = append(shadowObservations, *shadowObs)
			}
			continue
		}

		// Fallback: single-face person creation for long-pending faces
		if maxRetry >= singleFaceFallbackRetries {
			person, err := s.createPersonFromComponent(component, componentScore)
			if err != nil {
				return nil, nil, nil, err
			}
			if person != nil && person.ID != 0 {
				affectedPersonIDs[person.ID] = struct{}{}
			}
			for _, photoID := range facePhotoIDs(component) {
				affectedPhotoIDs[photoID] = struct{}{}
			}
			if shadowObs != nil {
				shadowObservations = append(shadowObservations, *shadowObs)
			}
			continue
		}

		if err := s.markComponentPending(component, componentScore); err != nil {
			return nil, nil, nil, err
		}
		// pending 更新成功也算一次完成的 legacy 决策，应进入 shadow。
		if shadowObs != nil {
			shadowObservations = append(shadowObservations, *shadowObs)
		}
		// Do NOT add affectedPhotoIDs for markComponentPending — these faces
		// were already pending and just got retry_count+1. Adding them would
		// cause processPendingFaces to return hasMore=true, spinning the inner
		// loop on the same stale faces indefinitely.
	}

	return mapKeys(affectedPersonIDs), mapKeys(affectedPhotoIDs), shadowObservations, nil
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
	// reclusterChangedPersons 收集所有轮次中实际发生 PersonID 变化的来源与目标人物，
	// 多轮合并去重后在末尾批量触发一次统一画像失效（recluster_assignment）。
	reclusterChangedPersons := make(map[uint]struct{})

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
		if err := s.executeWrite(func() error {
			return s.faceRepo.ResetForRecluster(candidateIDs)
		}); err != nil {
			logger.Warnf("recluster: failed to reset faces: %v", err)
			break
		}

		// Re-run incremental clustering with updated prototypes. This executes
		// on the coordinator worker thread; runClusterBatch acquires the write
		// gate for the batch and yields to foreground waiters between batches.
		clusterRes := s.clusteringCoordinator.runClusterBatch(clusterSourceFeedback)
		affectedPersonIDs, affectedPhotoIDs := clusterRes.affectedPersonIDs, clusterRes.affectedPhotoIDs
		if err := clusterRes.err; err != nil {
			logger.Warnf("recluster: clustering failed: %v", err)
			break
		}

		// Sync affected persons and photos
		for _, pid := range affectedPersonIDs {
			_ = s.syncPersonState(pid)
		}
		if len(affectedPhotoIDs) > 0 {
			_ = s.executeWrite(func() error {
				return s.photoRepo.RecomputeTopPersonCategory(affectedPhotoIDs)
			})
		}
		// Also sync persons that lost faces
		for _, oldPID := range prevAssign {
			_ = s.syncPersonState(oldPID)
		}

		// Count actual reassignments and collect changed persons (source + target).
		// 仅修改 score/retry/状态但 PersonID 未变化时不纳入失效（不重建身份中心）。
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
				if oldPID != 0 {
					reclusterChangedPersons[oldPID] = struct{}{}
				}
				if newPID != 0 {
					reclusterChangedPersons[newPID] = struct{}{}
				}
			}
		}
		result.Reassigned += reassigned

		if reassigned == 0 {
			break // converged
		}
	}

	// Cluster any remaining pending faces (e.g., from DissolvePerson).
	// Skip when recluster did nothing — avoids an extra clustering batch on every
	// zero-result recluster (takes seconds on large databases).
	var extraPersonIDs, extraPhotoIDs []uint
	if result.Evaluated > 0 || result.Reassigned > 0 {
		extraRes := s.clusteringCoordinator.runClusterBatch(clusterSourceFeedback)
		var clusteringErr error
		extraPersonIDs, extraPhotoIDs, clusteringErr = extraRes.affectedPersonIDs, extraRes.affectedPhotoIDs, extraRes.err
		if clusteringErr != nil {
			logger.Warnf("recluster: pending face clustering failed: %v", clusteringErr)
		} else {
			for _, pid := range extraPersonIDs {
				_ = s.syncPersonState(pid)
			}
			if len(extraPhotoIDs) > 0 {
				_ = s.photoRepo.RecomputeTopPersonCategory(extraPhotoIDs)
			}
		}
	}

	if result.Reassigned > 0 || len(extraPersonIDs) > 0 || len(extraPhotoIDs) > 0 {
		s.markMergeSuggestionsDirty("trigger_recluster")
	}

	// Task 13：若 recluster 实际改变了 Face 的 PersonID，对实际发生归属变化的人物批量
	// 触发统一画像失效（recluster_assignment）。extra 聚类批次若也改变了人物成员，
	// 其 affectedPersonIDs 已由 runClusterBatch 末尾的 clustering_assignment 失效处理，
	// 这里只补充 recluster 本轮直接重指派的人物（不在 extra 聚类批次内）。
	// 多轮去重合并后一次失效；失败迭代不会把未发生的 PersonID 纳入（仅 reassigned>0 时收集）。
	if len(reclusterChangedPersons) > 0 {
		s.invalidateIdentityProfiles(IdentityProfileInvalidation{
			DirtyPersonIDs: mapKeys(reclusterChangedPersons),
			Reason:         "recluster_assignment",
		})
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
		return s.executeWrite(func() error {
			_ = s.cannotLinkRepo.DeleteByPersonID(personID)
			return s.personRepo.Delete(personID)
		})
	}

	if err := s.executeWrite(func() error {
		return s.personRepo.RefreshStats(personID)
	}); err != nil {
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
	return s.executeWrite(func() error {
		return s.personRepo.UpdateFields(personID, updates)
	})
}

// ErrPeopleSplitConflict 表示 split 请求与当前归属状态冲突：所选人脸当前已不属于
// 页面提交的 source 人物，且无法匹配为同一拆分请求的幂等重放（人脸跨多人物、归属他
// 人物但无匹配 person_split 事件、部分人脸无归属，或历史事件 source/target 不一致）。
// handler 将其映射为 HTTP 409 / SPLIT_ASSIGNMENT_CONFLICT，而非通用 500。
// 返回此错误而非静默创建新人物或复用无关人物，避免重复 split 请求造成人物链或误归属。
var ErrPeopleSplitConflict = errors.New("split request conflicts with current face assignments")

var (
	ErrPhotoAnalysisPending = errors.New("照片 AI 分析尚未完成，不能进行人物检测")
	ErrPhotoPeopleExcluded  = errors.New("照片已退出人物识别")
)

func photoPeopleEligibilityError(photo *model.Photo) error {
	if photo == nil || photo.Status != model.PhotoStatusActive || photo.PeopleExcluded || strings.TrimSpace(photo.MainCategory) == model.PhotoMainCategoryScreenshot {
		return ErrPhotoPeopleExcluded
	}
	if !photo.AIAnalyzed || strings.TrimSpace(photo.MainCategory) == "" {
		return ErrPhotoAnalysisPending
	}
	return nil
}

// ErrPersonHidden indicates an operation was attempted on a hidden person.
// The handler maps this to HTTP 409 with code PERSON_HIDDEN.
var ErrPersonHidden = errors.New("人物已隐藏，请先恢复显示后再执行该操作")

// rejectIfHidden re-reads person state from the database and returns
// ErrPersonHidden if any of the given person IDs are hidden. Used by
// MergePeople/SplitPerson/MoveFaces/AssignFacePerson/DissolvePerson/
// UpdateFaceExclusion to fail-closed before mutating ownership.
func (s *peopleService) rejectIfHidden(personIDs ...uint) error {
	ids := make([]uint, 0, len(personIDs))
	for _, id := range personIDs {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	people, err := s.personRepo.ListByIDs(ids)
	if err != nil {
		return fmt.Errorf("check person hidden state: %w", err)
	}
	for _, p := range people {
		if p != nil && p.Hidden {
			return ErrPersonHidden
		}
	}
	return nil
}

// errPeopleSplitConflict 是 ErrPeopleSplitConflict 的内部别名，保留既有调用点与测试的
// 错误判定语义（errors.Is 仍成立），无需大范围改写。
var errPeopleSplitConflict = ErrPeopleSplitConflict

// errPeopleMoveConflict 表示 move 请求与当前归属状态冲突：请求的部分 face 已经移动到
// 一个非 target 的不同人物（stale repeat 跨人物漂移），无法安全合并移动。返回此错误
// 而非继续 mutate，避免重复 move 请求把已漂移的归属误改到 target。
var errPeopleMoveConflict = errors.New("move request conflicts with current face assignments")

// normalizeFaceIDs 返回去重、去零、升序排序的 face ID 切片。用于 split/move 幂等比较，
// 保证同一逻辑 face 集合无论输入顺序/重复都产生相同规范化结果。
func normalizeFaceIDs(ids []uint) []uint {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
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
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// distinctFacePersonIDs 返回 faces 当前归属的去重非零 person_id 集合（未排序）。
// 用于 split 幂等检测：判断请求 face 集合当前是同属一个人物、分散多人物，还是无归属。
func distinctFacePersonIDs(faces []*model.Face) []uint {
	seen := make(map[uint]struct{})
	for _, face := range faces {
		if face == nil || face.PersonID == nil || *face.PersonID == 0 {
			continue
		}
		seen[*face.PersonID] = struct{}{}
	}
	out := make([]uint, 0, len(seen))
	for pid := range seen {
		out = append(out, pid)
	}
	return out
}

// onlyValue 返回单元素切片的唯一值；调用方需保证 len==1。
func onlyValue(ids []uint) uint {
	if len(ids) == 0 {
		return 0
	}
	return ids[0]
}

// findReplaySplitTarget 判定一次 split 请求是否为同一拆分请求的幂等重放。
// 调用前提：请求 face 当前统一属于 expectedOwner（且 expectedOwner != sourcePersonID）。
// 只有同时满足以下条件，才返回匹配事件的 target_person_id（即 expectedOwner）：
//   - event_type = person_split
//   - source_person_ids 精确包含本次 sourcePersonID（单元素集合相等）
//   - face_ids 与归一化后的请求集合完全一致
//   - 事件 target_person_id == expectedOwner（当前归属）
//   - 对应目标人物仍然存在
//
// 若存在多条历史事件，选择与当前人脸归属（expectedOwner）匹配的事件，而不是返回第一条。
// feedbackEventRepo 为 nil（测试未注入）时不构成重放，返回 (0, false)。
func (s *peopleService) findReplaySplitTarget(sourcePersonID uint, normalizedFaceIDs []uint, expectedOwner uint) (uint, bool) {
	if s.feedbackEventRepo == nil {
		return 0, false
	}
	events, err := s.feedbackEventRepo.FindByEventTypeTargetAndFaceIDs(
		repository.PeopleFeedbackEventPersonSplit,
		0, // 不按 target 过滤，按 face_ids 精确匹配后逐条校验 source/target
		repository.MarshalFeedbackIDs(normalizedFaceIDs),
	)
	if err != nil || len(events) == 0 {
		return 0, false
	}
	wantSourceJSON := repository.MarshalFeedbackIDs([]uint{sourcePersonID})
	for _, ev := range events {
		if ev == nil {
			continue
		}
		if ev.SourcePersonIDs != wantSourceJSON {
			continue
		}
		if ev.TargetPersonID != expectedOwner {
			continue
		}
		return ev.TargetPersonID, true
	}
	return 0, false
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

// legacyIdentityResult 是一次 legacy 聚类决策的脱敏快照，供 shadow 比对使用。
//
//   - Matched=true：legacy 成功挂靠到已有人物（attach 分支），PersonID 为挂靠目标。
//   - Matched=false：legacy 未找到可挂靠人物（走 create / single-face fallback / pending）。
//     后续创建新人物不算 legacy identity match；PersonID 为 0。
//   - Score 保存 legacy 最佳分数（即使未达到挂靠阈值），供遥测比对。
type legacyIdentityResult struct {
	Matched  bool
	PersonID uint
	Score    float64
}

// identityShadowObservation 是一个组件在 legacy 决策完成、写入成功后待 shadow/rescue
// 处理的最小快照。Component 为深拷贝，仅保留 shadow 所需字段，不包含图片/缩略图路径、人名
// 等无关字段；observation 必须保存 legacy 写入前的组件状态（PersonID 等可能被 legacy
// 修改，shadow matcher 看到的是 legacy attach 前的来源人物）。
//
// Task 12 扩展：rescue miss 边界会在持锁阶段同步计算 profile 并填入 ProfileComputed/
// Profile/ProfileElapsed；post-lock 处理看到 ProfileComputed=true 时直接复用，不再重复
// 调用 matcher（每个 legacy miss 最多调用一次 matcher）。RescueApplied=true 表示 rescue
// 已把组件挂靠到 Profile.PersonID；DirtyPersonID 记录待标记 dirty 的目标人物。
type identityShadowObservation struct {
	Component []*model.Face
	Legacy    legacyIdentityResult

	ProfileComputed bool
	Profile         IdentityProfileMatch
	ProfileElapsed  time.Duration
	RescueApplied   bool
	DirtyPersonID   uint
}

// cloneComponentForShadow 深拷贝组件中 shadow matcher 实际读取的字段：ID、PhotoID、
// PersonID（复制值，不共享原指针）、QualityScore、ManualLocked、Embedding（byte slice
// 深拷贝）、RetryCount。不复制路径、缩略图、人名或时间字段。
//
// 必须在 legacy 写入前调用，确保 shadow matcher 看到的是 legacy attach 前的来源状态。
func cloneComponentForShadow(component []*model.Face) []*model.Face {
	cloned := make([]*model.Face, 0, len(component))
	for _, f := range component {
		if f == nil {
			cloned = append(cloned, nil)
			continue
		}
		var emb []byte
		if len(f.Embedding) > 0 {
			emb = make([]byte, len(f.Embedding))
			copy(emb, f.Embedding)
		}
		var personID *uint
		if f.PersonID != nil {
			pid := *f.PersonID
			personID = &pid
		}
		cloned = append(cloned, &model.Face{
			ID:           f.ID,
			PhotoID:      f.PhotoID,
			PersonID:     personID,
			QualityScore: f.QualityScore,
			ManualLocked: f.ManualLocked,
			Embedding:    emb,
			RetryCount:   f.RetryCount,
		})
	}
	return cloned
}

// identityShadowEnabled 报告当前是否启用身份画像 shadow 处理（非 legacy 模式且注入了
// match hook）。legacy 模式或未注入 hook 时为 false，runIncrementalClustering 据此避免
// 分配 observation slice 与复制 embedding。
func (s *peopleService) identityShadowEnabled() bool {
	return s.identityProfileMatchFn != nil && s.identityProfileMode != "" && s.identityProfileMode != model.PeopleIdentityModeLegacy
}

// identityRescueEnabled 报告当前是否启用 rescue（保守救回 legacy miss）。仅 rescue 模式
// 且注入了 match hook 时为 true。禁止用 mode != legacy 作为 rescue 判断，否则 shadow /
// primary 会提前改变人物归属。Task 12 新增。
func (s *peopleService) identityRescueEnabled() bool {
	return s.identityProfileMode == model.PeopleIdentityModeRescue && s.identityProfileMatchFn != nil
}

// processIdentityShadowObservations 在释放 writeGate.RLock 后对每个 legacy 决策计算画像
// 结果并记录遥测。画像结果永远不被应用：legacy miss/profile hit、legacy/profile 分歧均只
// 记录。matcher / ANN / Repository / telemetry 的任何失败或 panic 都不影响聚类，仅构造
// 不可用结果供遥测。
//
// Task 12：observation 若已 ProfileComputed=true（rescue 模式在持锁阶段计算过），则直接
// 复用其 Profile / ProfileElapsed，不再调用 matcher（每个 legacy miss 最多一次 matcher）。
// rescue 成功的 observation（RescueApplied=true）以 rescue_applied 全量记录遥测；rescue
// 拒绝的 observation 复用 profile 结果按现有 classify 规则记录（profile_blocked /
// legacy_miss_profile_miss 等），不重复匹配。
//
// legacy 模式直接返回（不遍历 observations、不调用 matcher/recorder、不访问画像
// Repository）。primary 模式仍按 shadow-only 处理：计算并记录，不应用结果。
func (s *peopleService) processIdentityShadowObservations(observations []identityShadowObservation) {
	if s.identityProfileMode == "" || s.identityProfileMode == model.PeopleIdentityModeLegacy {
		return
	}
	matchFn := s.identityProfileMatchFn
	recordFn := s.identityDecisionRecordFn

	for _, obs := range observations {
		faceIDs := faceIDs(obs.Component)

		// rescue 已在持锁阶段计算 profile；shadow/primary 在 post-lock 计算。
		profile := obs.Profile
		elapsed := obs.ProfileElapsed
		if !obs.ProfileComputed {
			start := time.Now()
			// matcher 失败/panic/非法结果一律 fail closed：构造不可用结果供遥测，保留 legacy。
			profile = IdentityProfileMatch{Available: false, BlockReason: blockProfileUnavailable}
			func() {
				defer func() {
					if r := recover(); r != nil {
						// 仅记录脱敏字段，不输出 Face ID / Person ID / embedding / 路径。
						logger.Warnf("identity profile shadow matcher panic: mode=%s err_category=%T elapsed=%s",
							s.identityProfileMode, r, time.Since(start).Round(time.Millisecond))
					}
				}()
				res := matchFn(obs.Component)
				// 非法分数（NaN/Inf）视为不可用，避免污染遥测。
				if mathIsNaNOrInf(res.Score) || mathIsNaNOrInf(res.SecondScore) || mathIsNaNOrInf(res.Margin) {
					profile = IdentityProfileMatch{Available: false, BlockReason: blockProfileUnavailable}
					return
				}
				profile = res
			}()
			elapsed = time.Since(start)
		}

		if recordFn != nil {
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.Warnf("identity decision telemetry panic: mode=%s err_category=%T elapsed=%s",
							s.identityProfileMode, r, elapsed.Round(time.Millisecond))
					}
				}()
				recordFn(IdentityTelemetryInput{
					Mode:                 s.identityProfileMode,
					ComponentFaceIDs:     faceIDs,
					LegacyTargetPersonID: obs.Legacy.PersonID,
					LegacyScore:          obs.Legacy.Score,
					LegacyMatched:        obs.Legacy.Matched,
					Profile:              profile,
					Elapsed:              elapsed,
					AlgorithmVersion:     identityProfileAlgorithmVersion,
					// IndexGeneration 暂为 0：Task 14 接入真实 ANN snapshot generation；
					// 不在本任务临时发明第二套 generation。
					IndexGeneration: 0,
					RescueApplied:   obs.RescueApplied,
				})
			}()
		}

		// Task 13：rescue 目标人物的画像持久化失效已由统一批次路径（runClusterBatch 末尾的
		// clustering_assignment 失效）完成，rescue 不再通过独立 hook 重复 MarkDirty，
		// 避免重复写入。rescue_attach 仅保留为 observation/遥测原因。
	}
}

// attemptIdentityRescue 在 legacy miss 边界尝试用 profile matcher 保守救回。仅 rescue 模式
// 调用（调用方已校验 identityRescueEnabled）。在 writeGate.RLock 保护范围内但不在
// executeWrite 闭包中执行 matcher（不持有 SQLite 写事务）。每个 legacy miss 最多调用一次。
//
// 接受条件（Task 8 matcher 已负责全部精度护栏，本方法只做最外层守卫）：
//
//	profile.Available && profile.PersonID != 0 && profile.AutoEligible &&
//	profile.BlockReason == "" && isFinite(profile.Score) && profile.Score >= 0
//
// 成功：把整个组件一次性挂靠到 profile.PersonID（复用 attachComponentToPerson），更新
// affectedPersonIDs / affectedPhotoIDs，在 observation 上标记 RescueApplied=true 并填入
// ProfileComputed=true + Profile + ProfileElapsed + DirtyPersonID，返回 rescued=true。
// rescue 写入失败：返回 rescueErr，调用方将其作为聚类错误返回，不继续创建新人物。
// matcher/画像不可用或护栏拒绝：填入 observation（ProfileComputed=true）后返回 rescued=false，
// 调用方继续 legacy fallback。
func (s *peopleService) attemptIdentityRescue(
	component []*model.Face,
	obs *identityShadowObservation,
	affectedPersonIDs map[uint]struct{},
	affectedPhotoIDs map[uint]struct{},
) (rescued bool, rescueErr error) {
	matchFn := s.identityProfileMatchFn
	if matchFn == nil {
		return false, nil
	}

	start := time.Now()
	profile := IdentityProfileMatch{Available: false, BlockReason: blockProfileUnavailable}
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Warnf("identity profile rescue matcher panic: mode=%s err_category=%T elapsed=%s",
					s.identityProfileMode, r, time.Since(start).Round(time.Millisecond))
			}
		}()
		res := matchFn(component)
		if mathIsNaNOrInf(res.Score) || mathIsNaNOrInf(res.SecondScore) || mathIsNaNOrInf(res.Margin) {
			profile = IdentityProfileMatch{Available: false, BlockReason: blockProfileUnavailable}
			return
		}
		profile = res
	}()
	elapsed := time.Since(start)

	// 无论是否救回，profile 都已计算，post-lock 阶段复用，不重复调用 matcher。
	obs.ProfileComputed = true
	obs.Profile = profile
	obs.ProfileElapsed = elapsed

	// 最外层接受守卫（精度护栏由 Task 8 matcher 的 AutoEligible 判定完成）。
	if !profile.Available || profile.PersonID == 0 || !profile.AutoEligible ||
		profile.BlockReason != "" || mathIsNaNOrInf(profile.Score) || profile.Score < 0 {
		return false, nil
	}

	// 防御：目标人物在写入前已消失 → 不救回，回退 legacy fallback。
	target, err := s.personRepo.GetByID(profile.PersonID)
	if err != nil {
		logger.Warnf("identity profile rescue: lookup target person failed: err_category=%T", err)
		return false, nil
	}
	if target == nil {
		return false, nil
	}

	// 整个组件一次性挂靠到目标人物，复用 legacy attach 的写入路径。
	prevIDs, err := s.attachComponentToPerson(component, profile.PersonID, profile.Score)
	if err != nil {
		// rescue 数据库挂靠写入失败：返回聚类错误，不允许随后创建新人物，否则可能产生
		// 部分写入或重复归属。
		return false, err
	}

	affectedPersonIDs[profile.PersonID] = struct{}{}
	for _, pid := range prevIDs {
		affectedPersonIDs[pid] = struct{}{}
	}
	for _, photoID := range facePhotoIDs(component) {
		affectedPhotoIDs[photoID] = struct{}{}
	}

	obs.RescueApplied = true
	obs.DirtyPersonID = profile.PersonID
	return true, nil
}

// mathIsNaNOrInf 报告分数是否为 NaN 或 Inf（matcher / 遥测非法结果守卫）。
func mathIsNaNOrInf(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0)
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
	// float32/SIMD 累积误差可能使余弦相似度略超出 [-1, 1]（如自身相似度得到
	// 1.0000001192093002），按余弦语义钳制到合法区间，避免下游断言与阈值比较脆弱。
	sim := float64(vek32.Dot(a, b)) / (normA * normB)
	if sim > 1 {
		return 1
	}
	if sim < -1 {
		return -1
	}
	return sim
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
