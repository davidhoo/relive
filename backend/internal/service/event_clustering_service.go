package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EventClusteringService 事件聚类服务接口
type EventClusteringService interface {
	StartClustering() (*model.EventClusteringTask, error)
	StartRebuild() (*model.EventClusteringTask, error)
	StopTask() error
	GetTask() *model.EventClusteringTask
	RunIncremental()
	// StopAutoWorker 停止自动增量聚类的 pending worker（Phase 3）。供 graceful shutdown 调用，
	// 不影响用户显式启动的 StartClustering/StartRebuild 任务（那些由 activeTask + StopTask 管理）。
	StopAutoWorker()
}

type eventClusteringService struct {
	db           *gorm.DB
	photoRepo    repository.PhotoRepository
	eventRepo    repository.EventRepository
	photoTagRepo repository.PhotoTagRepository
	config       model.EventClusteringConfig
	writeQueue   *database.WriteQueue

	activeTask *clusteringRuntime
	taskMutex  sync.RWMutex

	// backgroundCoordinator 是统一后台任务准入控制器（Phase 3）。nil 时退化为不 gating
	// （向后兼容测试桩与旧调用方）。扫描完成后自动触发的增量聚类经 coordinator 准入 +
	// coalescing（DedupeKey=autoIncrementalDedupeKey），foreground active / iowait_high /
	// cooldown 时保持 pending 不执行。用户显式 StartClustering/StartRebuild 仍走 P1 user。
	backgroundCoordinator *BackgroundTaskCoordinator

	// autoIncremental 的单槽 pending worker（Phase 3）。所有状态仅由 autoMu 保护，
	// 全内存态，不写 SQLite。pending=true 表示有一次自动增量聚类待执行；running=true 表示
	// worker goroutine 正在执行。扫描完成只调 RequestAutomaticIncremental 标记 pending +
	// wake，不直接无限制启动重工作。worker 在 SetBackgroundCoordinator 时启动，Stop 时退出。
	autoMu        sync.Mutex
	autoPending   bool
	autoRunning   bool
	autoWake      chan struct{}
	autoStop      chan struct{}
	autoWorkerDone chan struct{}
	autoWorkerStarted bool
}

// autoIncrementalDedupeKey 是自动增量聚类的固定 DedupeKey。多次扫描完成触发合并：
// 同一 (class, dedupeKey) 在 running 期间至多保留一个 pending，第二个被 coalesce。
const autoIncrementalDedupeKey = "scan_auto"

// autoIncrementalBatchSize 是自动增量聚类提交循环的批次大小：每处理这么多 cluster 后
// 检查一次 foreground / 负载，需要让路时保存下一个 cluster index 并退出本 slice。
// 测试通过 setAutoIncrementalBatchSize 临时调小以构造可让路窗口；生产默认 20。
const autoIncrementalBatchSize = 20

// autoBatchSizeOverride 允许测试把批次大小临时调小（如 1），以在少量 cluster 下触发让路。
// 仅测试使用；生产路径不设置。0 表示用默认常量。
var autoBatchSizeOverride int32

// clusteringRuntime 运行中的聚类任务
type clusteringRuntime struct {
	id              string
	taskType        string
	ctx             context.Context
	cancel          context.CancelFunc
	startedAt       time.Time
	completedAt     *time.Time
	status          string
	stopRequestedAt *time.Time
	errorMessage    string
	progress        model.EventClusteringProgress
	mu              sync.Mutex
}

// photoCluster 聚类中间结果
type photoCluster struct {
	photos []*model.Photo
}

// NewEventClusteringService 创建事件聚类服务
func NewEventClusteringService(db *gorm.DB, photoRepo repository.PhotoRepository, eventRepo repository.EventRepository, photoTagRepo repository.PhotoTagRepository) EventClusteringService {
	s := &eventClusteringService{
		db:           db,
		photoRepo:    photoRepo,
		eventRepo:    eventRepo,
		photoTagRepo: photoTagRepo,
		config:       model.DefaultEventClusteringConfig(),
		writeQueue:   database.GetWriteQueue(),
		autoWake:     make(chan struct{}, 1),
		autoStop:     make(chan struct{}),
		autoWorkerDone: make(chan struct{}),
	}
	return s
}

// SetBackgroundCoordinator 注入统一后台任务准入控制器并启动自动增量聚类的 pending
// worker（Phase 3）。nil 时不 gating 也不启动 worker（向后兼容测试桩）。worker 在收到
// StopAutoWorker 或 autoStop 后退出。
func (s *eventClusteringService) SetBackgroundCoordinator(c *BackgroundTaskCoordinator) {
	s.autoMu.Lock()
	s.backgroundCoordinator = c
	startWorker := c != nil && s.autoRunning == false && s.autoWorkerStarted == false
	if c != nil {
		s.autoWorkerStarted = true
	}
	s.autoMu.Unlock()
	if startWorker {
		go s.autoIncrementalWorker()
	}
}

// StopAutoWorker 停止自动增量聚类的 pending worker（供 graceful shutdown 调用）。
// 不影响用户显式启动的 StartClustering/StartRebuild 任务（那些由 activeTask 管理）。
func (s *eventClusteringService) StopAutoWorker() {
	s.autoMu.Lock()
	if s.autoStop != nil {
		select {
		case <-s.autoStop:
			// already closed
		default:
			close(s.autoStop)
		}
	}
	s.autoMu.Unlock()
	s.wakeAutoWorker()
	<-s.autoWorkerDone
}

// executeWrite runs fn through WriteQueue if available, otherwise directly.
func (s *eventClusteringService) executeWrite(fn func() error) error {
	if s.writeQueue != nil {
		return s.writeQueue.Execute(fn)
	}
	return fn()
}

// StartClustering 启动增量聚类任务
func (s *eventClusteringService) StartClustering() (*model.EventClusteringTask, error) {
	return s.startTask(model.ClusteringTaskTypeIncremental)
}

// StartRebuild 启动全量重建任务
func (s *eventClusteringService) StartRebuild() (*model.EventClusteringTask, error) {
	return s.startTask(model.ClusteringTaskTypeRebuild)
}

func (s *eventClusteringService) startTask(taskType string) (*model.EventClusteringTask, error) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()

	if s.activeTask != nil && (s.activeTask.status == model.ScanJobStatusRunning || s.activeTask.status == model.ScanJobStatusStopping) {
		return nil, fmt.Errorf("聚类任务正在运行中")
	}

	ctx, cancel := context.WithCancel(context.Background())
	runtime := &clusteringRuntime{
		id:        uuid.New().String()[:8],
		taskType:  taskType,
		ctx:       ctx,
		cancel:    cancel,
		startedAt: time.Now(),
		status:    model.ScanJobStatusRunning,
	}
	s.activeTask = runtime

	go s.runTask(runtime)

	return s.buildTaskDTO(runtime), nil
}

// StopTask 停止当前聚类任务
func (s *eventClusteringService) StopTask() error {
	s.taskMutex.RLock()
	task := s.activeTask
	s.taskMutex.RUnlock()

	if task == nil || task.status != model.ScanJobStatusRunning {
		return fmt.Errorf("没有正在运行的聚类任务")
	}

	task.mu.Lock()
	now := time.Now()
	task.status = model.ScanJobStatusStopping
	task.stopRequestedAt = &now
	task.mu.Unlock()
	task.cancel()

	return nil
}

// GetTask 获取当前任务状态
func (s *eventClusteringService) GetTask() *model.EventClusteringTask {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()

	if s.activeTask == nil {
		return nil
	}
	return s.buildTaskDTO(s.activeTask)
}

// RunIncremental 执行增量聚类（同步，扫描完成后调用）。
//
// Phase 3 行为：如果已注入 backgroundCoordinator，RunIncremental 不再直接无限制启动重工作，
// 而是标记一次 automatic 增量聚类 pending 并唤醒 worker。worker 经 coordinator 准入
// （foreground active / iowait_high / cooldown 时保持 pending）后，按小批次执行并可在批次
// 边界让路。多次扫描完成触发经 DedupeKey 合并，至多一 running + 一 pending。
//
// 未注入 coordinator 时（测试桩 / 旧调用方）保持旧行为：直接同步执行一次增量聚类。
func (s *eventClusteringService) RunIncremental() {
	s.taskMutex.RLock()
	active := s.activeTask
	s.taskMutex.RUnlock()

	// 如果已有用户任务在跑，跳过（用户显式任务优先，不与自动增量并发）。
	if active != nil && (active.status == model.ScanJobStatusRunning || active.status == model.ScanJobStatusStopping) {
		logger.Infof("[EventClustering] Skipping incremental: task already running")
		return
	}

	s.autoMu.Lock()
	coord := s.backgroundCoordinator
	s.autoMu.Unlock()

	if coord == nil {
		// 旧行为：直接同步执行（测试桩 / 未注入 coordinator）。
		logger.Infof("[EventClustering] Running incremental clustering after scan (no coordinator)")
		if err := s.runIncremental(context.Background(), nil); err != nil {
			logger.Warnf("[EventClustering] Incremental clustering failed: %v", err)
		}
		return
	}

	// Phase 3：只标记 pending 并唤醒 worker，不直接启动重工作。
	s.requestAutomaticIncremental()
}

// requestAutomaticIncremental 标记一次自动增量聚类 pending 并唤醒 worker。
// 多次调用合并：pending 已为 true 时只保留一次（coalesce）。
func (s *eventClusteringService) requestAutomaticIncremental() {
	s.autoMu.Lock()
	if s.autoPending {
		s.autoMu.Unlock()
		logger.Infof("[EventClustering] Auto incremental already pending (coalesced)")
		return
	}
	s.autoPending = true
	s.autoMu.Unlock()
	logger.Infof("[EventClustering] Auto incremental requested (pending)")
	s.wakeAutoWorker()
}

// wakeAutoWorker 向 wake channel 发送一个非阻塞信号。
func (s *eventClusteringService) wakeAutoWorker() {
	select {
	case s.autoWake <- struct{}{}:
	default:
	}
}

// autoIncrementalWorker 是自动增量聚类的 pending worker 主循环（Phase 3）。
// 它从 wake/stop 事件中醒来，在 pending 时请求 coordinator 准入；被拒绝则保持 pending，
// 短退避后重试；准入成功后按小批次执行增量聚类，批次边界检查 foreground / 负载，需要让路时
// 保存进度并退出本 slice（保持 pending），由下次 wake 或退避定时器继续。
func (s *eventClusteringService) autoIncrementalWorker() {
	defer close(s.autoWorkerDone)

	// retryTimer 在被拒绝/让路后驱动重试（pending 未清空时）；nil 表示当前无挂起重试。
	// 这样 foreground 释放或退避到期后能自动恢复，无需外部 wake。
	var retryTimer *time.Timer
	var retryCh <-chan time.Time

	backoff := retryBackoff
	if override := atomic.LoadInt64(&autoRetryBackoffOverride); override > 0 {
		backoff = time.Duration(override)
	}

	for {
		select {
		case <-s.autoStop:
			if retryTimer != nil {
				retryTimer.Stop()
			}
			return
		case <-s.autoWake:
		case <-retryCh:
			retryTimer = nil
			retryCh = nil
		}

		// drain：合并多次 wake。
		select {
		case <-s.autoWake:
		default:
		}

		for {
			stopped := s.autoStopped()
			if stopped {
				if retryTimer != nil {
					retryTimer.Stop()
				}
				return
			}

			s.autoMu.Lock()
			pending := s.autoPending
			running := s.autoRunning
			s.autoMu.Unlock()
			if !pending || running {
				// 无 pending 或已在执行。若仍有挂起重试定时器，清掉它（已无待办）。
				if retryTimer != nil {
					retryTimer.Stop()
					retryTimer = nil
					retryCh = nil
				}
				break
			}

			release, decision, ok := s.beginAutoIncremental()
			if !ok {
				// 被拒绝：Begin 返回 nil release（无 slot 占用），保持 pending，挂起重试定时器
				// 后回到外层 select 等待。
				logger.Infof("[EventClustering] Auto incremental deferred: %s", decision.Reason)
				if retryTimer == nil {
					retryTimer = time.NewTimer(backoff)
					retryCh = retryTimer.C
				}
				break
			}

			// 标记 running 并执行本 slice。
			s.autoMu.Lock()
			s.autoRunning = true
			s.autoPending = false // 消费本次 pending；执行期间新请求会重新置 pending
			s.autoMu.Unlock()

			yielded, err := s.runAutoIncrementalSlice(release)
			// release 已在 slice 内部按需释放（让路或完成都会调用）；这里兜底确保释放。
			release()

			s.autoMu.Lock()
			s.autoRunning = false
			if err != nil {
				// 执行失败：保持 pending 以便重试（若仍 stopped 则不重置）。
				if !s.autoStoppedLocked() {
					s.autoPending = true
				}
			} else if yielded {
				// 让路退出：保持 pending，等下次 wake / 退避继续。
				if !s.autoStoppedLocked() {
					s.autoPending = true
				}
			}
			s.autoMu.Unlock()

			if err != nil {
				logger.Warnf("[EventClustering] Auto incremental slice failed: %v", err)
			}

			// 让路或失败后挂起重试定时器（pending 未清空时），回到外层 select。
			if yielded || err != nil {
				if retryTimer == nil {
					retryTimer = time.NewTimer(backoff)
					retryCh = retryTimer.C
				}
				break
			}
			// 成功完成：清掉挂起重试定时器（若有），继续内层循环检查是否还有 pending。
			if retryTimer != nil {
				retryTimer.Stop()
				retryTimer = nil
				retryCh = nil
			}
		}
	}
}

const retryBackoff = 30 * time.Second

// autoRetryBackoffOverride 允许测试把被拒/让路后的重试退避调小，以快速验证恢复路径。
// 0 表示用默认 retryBackoff。仅测试使用；生产路径不设置。
var autoRetryBackoffOverride int64 // duration in nanoseconds; 0 = default

// beginAutoIncremental 请求 coordinator 准入。返回 release（成功时必须调用）与 decision。
func (s *eventClusteringService) beginAutoIncremental() (func(), BackgroundTaskDecision, bool) {
	coord := s.backgroundCoordinator
	if coord == nil {
		// 无 coordinator（不应到达此处，RunIncremental 已分流），放行 no-op release。
		noOp := func() {}
		return noOp, BackgroundTaskDecision{Allowed: true, Reason: BackgroundDecisionAllowed}, true
	}
	return coord.Begin(BackgroundTaskRequest{
		Class:     BackgroundTaskEventClustering,
		Priority:  BackgroundPriorityAutomatic,
		DedupeKey: autoIncrementalDedupeKey,
	})
}

// runAutoIncrementalSlice 执行一次自动增量聚类的可暂停小批次（Phase 3）。
// 返回 (yielded, error)：yielded=true 表示因 foreground/负载让路而中途退出（保持 pending）。
// release 在本方法内于完成或让路时调用。
func (s *eventClusteringService) runAutoIncrementalSlice(release func()) (bool, error) {
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 若 worker 收到 stop，取消本 slice。
	go func() {
		select {
		case <-s.autoStop:
			cancel()
		case <-ctx.Done():
		}
	}()

	return s.runIncrementalYieldable(ctx, nil)
}

func (s *eventClusteringService) autoStopped() bool {
	s.autoMu.Lock()
	defer s.autoMu.Unlock()
	return s.autoStoppedLocked()
}

func (s *eventClusteringService) autoStoppedLocked() bool {
	select {
	case <-s.autoStop:
		return true
	default:
		return false
	}
}

func (s *eventClusteringService) runTask(runtime *clusteringRuntime) {
	var err error
	switch runtime.taskType {
	case model.ClusteringTaskTypeRebuild:
		err = s.runRebuild(runtime.ctx, runtime)
	case model.ClusteringTaskTypeIncremental:
		err = s.runIncremental(runtime.ctx, runtime)
	}

	runtime.mu.Lock()
	now := time.Now()
	runtime.completedAt = &now
	if err != nil {
		if runtime.status == model.ScanJobStatusStopping {
			runtime.status = model.ScanJobStatusStopped
		} else {
			runtime.status = model.ScanJobStatusFailed
			runtime.errorMessage = err.Error()
		}
	} else {
		if runtime.status == model.ScanJobStatusStopping {
			runtime.status = model.ScanJobStatusStopped
		} else {
			runtime.status = model.ScanJobStatusCompleted
			runtime.progress.Phase = "completed"
		}
	}
	runtime.mu.Unlock()

	logger.Infof("[EventClustering] Task %s (%s) finished: status=%s, created=%d, updated=%d, skipped_photos=%d",
		runtime.id, runtime.taskType, runtime.status,
		runtime.progress.EventsCreated, runtime.progress.EventsUpdated, runtime.progress.PhotosSkipped)
}

// runRebuild 全量重建
func (s *eventClusteringService) runRebuild(ctx context.Context, runtime *clusteringRuntime) error {
	setPhase := func(phase string) {
		if runtime != nil {
			runtime.mu.Lock()
			runtime.progress.Phase = phase
			runtime.mu.Unlock()
		}
	}

	// 1. 清空所有事件和照片的 event_id
	setPhase("discovering")
	logger.Infof("[EventClustering] Rebuild: clearing all events and photo event_ids")

	if err := s.executeWrite(func() error {
		if err := s.eventRepo.DeleteAll(); err != nil {
			return fmt.Errorf("delete all events: %w", err)
		}
		if err := s.db.Model(&model.Photo{}).Where("event_id IS NOT NULL").Update("event_id", nil).Error; err != nil {
			return fmt.Errorf("clear photo event_ids: %w", err)
		}
		return nil
	}); err != nil {
		return err
	}

	// 2. 查询所有有 taken_at 且 active 的照片
	var photos []*model.Photo
	if err := s.db.Where("taken_at IS NOT NULL AND status = ?", model.PhotoStatusActive).
		Order("taken_at ASC").Find(&photos).Error; err != nil {
		return fmt.Errorf("query photos: %w", err)
	}

	if len(photos) == 0 {
		logger.Infof("[EventClustering] Rebuild: no photos with taken_at found")
		return nil
	}

	if runtime != nil {
		runtime.mu.Lock()
		runtime.progress.TotalPhotos = len(photos)
		runtime.mu.Unlock()
	}

	// 检查取消
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 3. 聚类
	setPhase("clustering")
	clusters := s.clusterPhotos(photos)

	// 4. 创建事件 + 画像
	setPhase("profiling")
	for _, cluster := range clusters {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 簇照片数不足，跳过（照片保持 event_id=NULL，由 hidden_gem 兜底）
		if s.config.MinPhotosPerEvent > 0 && len(cluster.photos) < s.config.MinPhotosPerEvent {
			if runtime != nil {
				runtime.mu.Lock()
				runtime.progress.PhotosSkipped += len(cluster.photos)
				runtime.progress.ProcessedPhotos += len(cluster.photos)
				runtime.mu.Unlock()
			}
			continue
		}

		event, err := s.createEventFromCluster(cluster)
		if err != nil {
			logger.Warnf("[EventClustering] Failed to create event: %v", err)
			continue
		}

		// 更新照片的 event_id
		photoIDs := make([]uint, len(cluster.photos))
		for i, p := range cluster.photos {
			photoIDs[i] = p.ID
		}
		if err := s.executeWrite(func() error {
			return s.db.Model(&model.Photo{}).Where("id IN ?", photoIDs).Update("event_id", event.ID).Error
		}); err != nil {
			logger.Warnf("[EventClustering] Failed to update photo event_ids: %v", err)
		}

		if runtime != nil {
			runtime.mu.Lock()
			runtime.progress.EventsCreated++
			runtime.progress.ProcessedPhotos += len(cluster.photos)
			runtime.mu.Unlock()
		}
	}

	return nil
}

// runIncremental 增量聚类
func (s *eventClusteringService) runIncremental(ctx context.Context, runtime *clusteringRuntime) error {
	setPhase := func(phase string) {
		if runtime != nil {
			runtime.mu.Lock()
			runtime.progress.Phase = phase
			runtime.mu.Unlock()
		}
	}

	// 1. 查询未聚类的照片
	setPhase("discovering")
	var photos []*model.Photo
	if err := s.db.Where("event_id IS NULL AND taken_at IS NOT NULL AND status = ?", model.PhotoStatusActive).
		Order("taken_at ASC").Find(&photos).Error; err != nil {
		return fmt.Errorf("query unclustered photos: %w", err)
	}

	if len(photos) == 0 {
		logger.Infof("[EventClustering] Incremental: no unclustered photos found")
		return nil
	}

	logger.Infof("[EventClustering] Incremental: found %d unclustered photos", len(photos))

	if runtime != nil {
		runtime.mu.Lock()
		runtime.progress.TotalPhotos = len(photos)
		runtime.mu.Unlock()
	}

	// 2. 聚类
	setPhase("clustering")
	clusters := s.clusterPhotos(photos)

	// 3. 尝试归入已有事件或创建新事件
	setPhase("profiling")
	for _, cluster := range clusters {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		s.commitCluster(ctx, cluster, runtime)
	}

	return nil
}

// runIncrementalYieldable 执行可暂停的增量聚类（Phase 3 自动增量专用）。
//
// 与 runIncremental 的差异仅在于提交循环：每处理 autoIncrementalBatchSize 个 cluster 后，
// 检查 context / coordinator foreground / 负载快照；需要让路时返回 yielded=true（保持 pending，
// 由 worker 下次 wake 继续）。discovery + clusterPhotos 保持 monolithic —— 它们是 O(n) 线性
// 单遍且 I/O 极轻，不是 NAS 磁盘争用源，分批会破坏聚类结果等价性（clusterPhotos 可跨任意
// 时间窗口合并）。结果等价于连续执行：本方法在不禁让路时与 runIncremental 逐 cluster 提交
// 完全一致；让路退出后，已提交 cluster 的照片 event_id 已落库，剩余未聚类照片下次 re-discovery
// 重新派生相同 clusters（clusterPhotos 对相同 sorted 输入确定性）。
func (s *eventClusteringService) runIncrementalYieldable(ctx context.Context, runtime *clusteringRuntime) (bool, error) {
	setPhase := func(phase string) {
		if runtime != nil {
			runtime.mu.Lock()
			runtime.progress.Phase = phase
			runtime.mu.Unlock()
		}
	}

	setPhase("discovering")
	var photos []*model.Photo
	if err := s.db.Where("event_id IS NULL AND taken_at IS NOT NULL AND status = ?", model.PhotoStatusActive).
		Order("taken_at ASC").Find(&photos).Error; err != nil {
		return false, fmt.Errorf("query unclustered photos: %w", err)
	}

	if len(photos) == 0 {
		logger.Infof("[EventClustering] Incremental: no unclustered photos found")
		return false, nil
	}

	logger.Infof("[EventClustering] Auto incremental: found %d unclustered photos", len(photos))

	if runtime != nil {
		runtime.mu.Lock()
		runtime.progress.TotalPhotos = len(photos)
		runtime.mu.Unlock()
	}

	setPhase("clustering")
	clusters := s.clusterPhotos(photos)

	setPhase("profiling")
	processed := 0
	batchSize := autoIncrementalBatchSize
	if override := atomic.LoadInt32(&autoBatchSizeOverride); override > 0 {
		batchSize = int(override)
	}
	for _, cluster := range clusters {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}

		s.commitCluster(ctx, cluster, runtime)
		processed++

		// 批次边界：每处理 batchSize 个 cluster 检查一次是否让路。用 cluster 计数而非照片计数：
		// cluster 是提交单位（每个 cluster 一次 createEvent + reprofile + photo UPDATE），是 I/O
		// 成本粒度。测试通过 autoBatchSizeOverride 调小以构造可让路窗口；生产用默认 20。
		if batchSize > 0 && processed%batchSize == 0 {
			yield, err := s.shouldYield(ctx)
			if err != nil {
				return false, err
			}
			if yield {
				logger.Infof("[EventClustering] Auto incremental yielding after %d/%d clusters (foreground/load)", processed, len(clusters))
				return true, nil
			}
		}
	}

	logger.Infof("[EventClustering] Auto incremental slice complete: %d clusters", len(clusters))
	return false, nil
}

// shouldYield 判断自动增量聚类是否应在批次边界让路。返回 true 表示应释放 coordinator slot
// 并保持 pending，由 worker 下次 wake 继续。规则：context 取消 → 返回 error；foreground active
// → 让路；负载快照 iowait/cpu/mem 超阈值（known）→ 让路。nil coordinator 时只看 context。
func (s *eventClusteringService) shouldYield(ctx context.Context) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	coord := s.backgroundCoordinator
	if coord == nil {
		return false, nil
	}
	if coord.ForegroundActive() {
		return true, nil
	}
	// advisory 负载检查：只看 known 且超阈值的值。unknown 不让路（计划硬约束：负载不能单独拒绝 P2，
	// 但这里是在已准入的执行中让路，不是准入拒绝；让路比拒绝更温和，仍遵循 advisory 语义）。
	snap := coord.LoadSnapshot()
	if isKnown(snap.CPUIOWaitPct) && coord.iowaitPauseThresholdLocked() > 0 && snap.CPUIOWaitPct >= coord.iowaitPauseThresholdLocked() {
		return true, nil
	}
	if isKnown(snap.CPUUserPct) && coord.cpuPauseThresholdLocked() > 0 && snap.CPUUserPct >= coord.cpuPauseThresholdLocked() {
		return true, nil
	}
	if isKnown(snap.MemUsedPct) && coord.memoryPauseThresholdLocked() > 0 && snap.MemUsedPct >= coord.memoryPauseThresholdLocked() {
		return true, nil
	}
	return false, nil
}

// commitCluster 把单个 cluster 归入已有事件或创建新事件，并更新照片 event_id。
// 抽自 runIncremental / runIncrementalYieldable 的共享提交逻辑，保证两者逐 cluster 行为一致。
func (s *eventClusteringService) commitCluster(ctx context.Context, cluster photoCluster, runtime *clusteringRuntime) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	clusterStart := *cluster.photos[0].TakenAt
	clusterEnd := *cluster.photos[len(cluster.photos)-1].TakenAt
	windowPadding := time.Duration(s.config.TimeGapSameEvent * float64(time.Hour))

	existingEvents, err := s.eventRepo.GetByTimeRange(
		clusterStart.Add(-windowPadding),
		clusterEnd.Add(windowPadding),
	)
	if err != nil {
		logger.Warnf("[EventClustering] Failed to query existing events: %v", err)
		existingEvents = nil
	}

	photoIDs := make([]uint, len(cluster.photos))
	for i, p := range cluster.photos {
		photoIDs[i] = p.ID
	}

	if len(existingEvents) > 0 {
		bestEvent := s.findBestMatchingEvent(cluster, existingEvents)
		if bestEvent != nil {
			if err := s.executeWrite(func() error {
				return s.db.Model(&model.Photo{}).Where("id IN ?", photoIDs).Update("event_id", bestEvent.ID).Error
			}); err != nil {
				logger.Warnf("[EventClustering] Failed to update photo event_ids: %v", err)
				return
			}
			if err := s.reprofileEvent(bestEvent.ID); err != nil {
				logger.Warnf("[EventClustering] Failed to reprofile event %d: %v", bestEvent.ID, err)
			}
			if runtime != nil {
				runtime.mu.Lock()
				runtime.progress.EventsUpdated++
				runtime.progress.ProcessedPhotos += len(cluster.photos)
				runtime.mu.Unlock()
			}
			return
		}
	}

	if s.config.MinPhotosPerEvent > 0 && len(cluster.photos) < s.config.MinPhotosPerEvent {
		if runtime != nil {
			runtime.mu.Lock()
			runtime.progress.PhotosSkipped += len(cluster.photos)
			runtime.progress.ProcessedPhotos += len(cluster.photos)
			runtime.mu.Unlock()
		}
		return
	}

	event, err := s.createEventFromCluster(cluster)
	if err != nil {
		logger.Warnf("[EventClustering] Failed to create event: %v", err)
		return
	}

	if err := s.executeWrite(func() error {
		return s.db.Model(&model.Photo{}).Where("id IN ?", photoIDs).Update("event_id", event.ID).Error
	}); err != nil {
		logger.Warnf("[EventClustering] Failed to update photo event_ids: %v", err)
	}

	if runtime != nil {
		runtime.mu.Lock()
		runtime.progress.EventsCreated++
		runtime.progress.ProcessedPhotos += len(cluster.photos)
		runtime.mu.Unlock()
	}
}

// clusterPhotos 按时空连续性聚类照片（照片必须已按 taken_at ASC 排序）
func (s *eventClusteringService) clusterPhotos(photos []*model.Photo) []photoCluster {
	if len(photos) == 0 {
		return nil
	}

	var clusters []photoCluster
	current := photoCluster{photos: []*model.Photo{photos[0]}}

	for i := 1; i < len(photos); i++ {
		prev := photos[i-1]
		curr := photos[i]
		timeDelta := curr.TakenAt.Sub(*prev.TakenAt).Hours()

		shouldSplit := false

		if timeDelta >= s.config.TimeGapNewEvent {
			// > 24h: 必切分
			shouldSplit = true
		} else if timeDelta >= s.config.TimeGapSameEvent {
			// 6h~24h 灰色地带：看 GPS 距离
			if prev.HasGPS() && curr.HasGPS() {
				dist := haversineDistance(*prev.GPSLatitude, *prev.GPSLongitude, *curr.GPSLatitude, *curr.GPSLongitude)
				if dist > s.config.DistanceForceSplit {
					shouldSplit = true
				}
			}
			// 没有 GPS 数据时，灰色地带默认不切分（保守策略）
		} else if timeDelta < s.config.TimeGapSameEvent {
			// < 6h: GPS > 50km 也切分
			if prev.HasGPS() && curr.HasGPS() {
				dist := haversineDistance(*prev.GPSLatitude, *prev.GPSLongitude, *curr.GPSLatitude, *curr.GPSLongitude)
				if dist > s.config.DistanceForceSplit {
					shouldSplit = true
				}
			}
		}

		if shouldSplit {
			clusters = append(clusters, current)
			current = photoCluster{photos: []*model.Photo{curr}}
		} else {
			current.photos = append(current.photos, curr)
		}
	}
	clusters = append(clusters, current)

	return clusters
}

// createEventFromCluster 从聚类创建事件并画像
func (s *eventClusteringService) createEventFromCluster(cluster photoCluster) (*model.Event, error) {
	photos := cluster.photos
	startTime := *photos[0].TakenAt
	endTime := *photos[len(photos)-1].TakenAt
	durationHours := endTime.Sub(startTime).Hours()

	event := &model.Event{
		StartTime:     startTime,
		EndTime:       endTime,
		DurationHours: durationHours,
		PhotoCount:    len(photos),
	}

	// 画像
	s.profileEvent(event, photos)

	err := s.executeWrite(func() error {
		return s.eventRepo.Create(event)
	})
	if err != nil {
		return nil, err
	}
	return event, nil
}

// profileEvent 计算事件画像
func (s *eventClusteringService) profileEvent(event *model.Event, photos []*model.Photo) {
	if len(photos) == 0 {
		return
	}

	// cover_photo_id = beauty_score 最高
	var bestPhoto *model.Photo
	for _, p := range photos {
		if bestPhoto == nil || p.BeautyScore > bestPhoto.BeautyScore {
			bestPhoto = p
		}
	}
	if bestPhoto != nil {
		event.CoverPhotoID = &bestPhoto.ID
	}

	// primary_category = 最频繁 main_category
	categoryCounts := make(map[string]int)
	for _, p := range photos {
		if p.MainCategory != "" {
			categoryCounts[p.MainCategory]++
		}
	}
	event.PrimaryCategory = mostFrequent(categoryCounts)

	// primary_tag = 最频繁标签（从 photo_tags 聚合）
	photoIDs := make([]uint, len(photos))
	for i, p := range photos {
		photoIDs[i] = p.ID
	}
	event.PrimaryTag = s.getMostFrequentTag(photoIDs)

	// location = 最频繁 photo.location
	locationCounts := make(map[string]int)
	for _, p := range photos {
		if p.Location != "" {
			locationCounts[p.Location]++
		}
	}
	event.Location = mostFrequent(locationCounts)

	// GPS = 簇内有效坐标均值
	var latSum, lngSum float64
	var gpsCount int
	for _, p := range photos {
		if p.HasGPS() {
			latSum += *p.GPSLatitude
			lngSum += *p.GPSLongitude
			gpsCount++
		}
	}
	if gpsCount > 0 {
		lat := latSum / float64(gpsCount)
		lng := lngSum / float64(gpsCount)
		event.GPSLatitude = &lat
		event.GPSLongitude = &lng
	}

	// event_score = avg(overall_score) * log2(photo_count + 1)
	var scoreSum float64
	for _, p := range photos {
		scoreSum += float64(p.OverallScore)
	}
	avgScore := scoreSum / float64(len(photos))
	event.EventScore = avgScore * math.Log2(float64(len(photos)+1))
}

// reprofileEvent 重新画像已有事件
func (s *eventClusteringService) reprofileEvent(eventID uint) error {
	event, err := s.eventRepo.GetByID(eventID)
	if err != nil {
		return err
	}

	// 查询事件内所有照片
	var photos []*model.Photo
	if err := s.db.Where("event_id = ? AND status = ?", eventID, model.PhotoStatusActive).
		Order("taken_at ASC").Find(&photos).Error; err != nil {
		return err
	}

	if len(photos) == 0 {
		return nil
	}

	// 更新时间范围
	event.StartTime = *photos[0].TakenAt
	event.EndTime = *photos[len(photos)-1].TakenAt
	event.DurationHours = event.EndTime.Sub(event.StartTime).Hours()
	event.PhotoCount = len(photos)

	// 重新画像
	s.profileEvent(event, photos)

	return s.executeWrite(func() error {
		return s.eventRepo.Update(event)
	})
}

// findBestMatchingEvent 在已有事件中找到最佳匹配（时间重叠最大的）
func (s *eventClusteringService) findBestMatchingEvent(cluster photoCluster, events []*model.Event) *model.Event {
	clusterStart := *cluster.photos[0].TakenAt
	clusterEnd := *cluster.photos[len(cluster.photos)-1].TakenAt
	clusterMid := clusterStart.Add(clusterEnd.Sub(clusterStart) / 2)

	var best *model.Event
	var bestDist time.Duration = math.MaxInt64

	for _, e := range events {
		eventMid := e.StartTime.Add(e.EndTime.Sub(e.StartTime) / 2)
		dist := absDuration(clusterMid.Sub(eventMid))
		if dist < bestDist {
			bestDist = dist
			best = e
		}
	}

	return best
}

// getMostFrequentTag 从 photo_tags 表获取照片集合的最频繁标签
func (s *eventClusteringService) getMostFrequentTag(photoIDs []uint) string {
	if len(photoIDs) == 0 {
		return ""
	}

	type tagCount struct {
		Tag   string
		Count int
	}
	var results []tagCount
	s.db.Table("photo_tags").
		Select("tag, COUNT(*) as count").
		Where("photo_id IN ?", photoIDs).
		Group("tag").
		Order("count DESC").
		Limit(1).
		Scan(&results)

	if len(results) > 0 {
		return results[0].Tag
	}
	return ""
}

// buildTaskDTO 构建任务 DTO
func (s *eventClusteringService) buildTaskDTO(runtime *clusteringRuntime) *model.EventClusteringTask {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	task := &model.EventClusteringTask{
		ID:              runtime.id,
		Type:            runtime.taskType,
		Status:          runtime.status,
		StartedAt:       runtime.startedAt,
		CompletedAt:     runtime.completedAt,
		StopRequestedAt: runtime.stopRequestedAt,
		ErrorMessage:    runtime.errorMessage,
		Progress:        &runtime.progress,
	}
	return task
}

// --- 纯函数工具 ---

// haversineDistance 计算两点间的 Haversine 距离（公里）
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0

	dLat := degreesToRadians(lat2 - lat1)
	dLon := degreesToRadians(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(degreesToRadians(lat1))*math.Cos(degreesToRadians(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

func degreesToRadians(deg float64) float64 {
	return deg * math.Pi / 180
}

// mostFrequent 返回 map 中出现次数最多的 key
func mostFrequent(counts map[string]int) string {
	var best string
	var bestCount int
	// 按 key 排序保证确定性
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if counts[k] > bestCount {
			bestCount = counts[k]
			best = k
		}
	}
	return best
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
