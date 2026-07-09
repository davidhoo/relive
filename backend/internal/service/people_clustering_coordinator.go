package service

import (
	"fmt"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
)

// clusterSource labels the origin of a clustering batch for structured logging.
type clusterSource string

const (
	clusterSourceBackground clusterSource = "background"
	clusterSourceFeedback   clusterSource = "feedback"
)

// backgroundClusterResult is the outcome of a single incremental clustering
// batch, delivered back to the background caller that requested it.
type backgroundClusterResult struct {
	affectedPersonIDs []uint
	affectedPhotoIDs  []uint
	err               error

	// shadowObservations 携带本批次的身份画像 shadow 观测。由 runIncrementalClustering
	// 在持有 writeGate.RLock 期间收集，但画像 matcher 与遥测必须由 runClusterBatch 在
	// 释放 writeGate.RLock 之后处理（见 processIdentityShadowObservations），不得长期占用
	// writeGate。legacy 模式下为 nil。
	shadowObservations []identityShadowObservation
}

var errCoordinatorStopped = fmt.Errorf("people clustering coordinator stopped")

// protoCache refresh cooldown / coalescing 常量（Task 10）。
const (
	// protoCacheRefreshMinInterval 成功刷新后再次允许刷新的最小间隔，避免每个 batch 都
	// 全量重载 prototype embeddings（220K+ rows）。
	protoCacheRefreshMinInterval = 10 * time.Minute
	// protoCacheRefreshFailCooldown 刷新失败/DB locked 后的退避间隔，避免 spin。
	protoCacheRefreshFailCooldown = 2 * time.Minute
)

// peopleClusteringCoordinator is the single entry point for all incremental
// face clustering in the people subsystem. It owns one worker goroutine that
// is the only thing permitted to execute runIncrementalClustering (and thus
// the only thing that touches protoCache).
//
// Scheduling priority (non-preemptive):
//
//	foreground mutation > feedback recluster > background clustering
//
// A running batch is never interrupted; instead the worker checks
// foregroundWaiters before starting each new batch and yields if a foreground
// mutation is waiting or in progress.
type peopleClusteringCoordinator struct {
	svc *peopleService

	mu   sync.Mutex
	cond *sync.Cond

	// running is true while the worker is executing a clustering job (a
	// feedback recluster or a background batch). It is for observability only
	// — the worker goroutine itself provides the actual mutual exclusion.
	running bool

	foregroundWaiters int
	feedbackPending   bool
	backgroundPending bool
	stopping          bool

	// bgWaiters collects result channels for all goroutines currently blocked
	// in submitBackground. The worker serves them with the result of a single
	// shared batch (coalescing concurrent background requests). This is not a
	// work queue — it is bounded by the number of concurrent callers.
	bgWaiters []chan backgroundClusterResult

	// feedbackCooldownUntil suppresses feedback recluster startup briefly after
	// a zero-result run, replacing the old CPU-spin cooldown. Background
	// clustering is still allowed to run during cooldown.
	feedbackCooldownUntil time.Time

	// mergedFeedbackRequests counts feedback requests that were coalesced into
	// an already-pending slot (for observability). Reset when a feedback job
	// starts. Guarded by c.mu.
	mergedFeedbackRequests int

	// feedback configuration (test-configurable). Guarded by fbMu.
	fbMu             sync.Mutex
	feedbackHook     func() model.ReclusterResult
	feedbackCooldown time.Duration

	// protoCache refresh 状态（Task 10 cooldown + coalescing）。全部内存态，不写 DB。
	// 只由 worker goroutine 在 runClusterBatch 中读写，无需额外同步（与 protoCache
	// single-owner 不变量一致）。
	//   - protoCacheRefreshRunning：当前是否有一个 refresh 在执行（runClusterBatch 内同步
	//     构建，故实际上恒为 false 的瞬时态——保留字段以表达“至多一 running”语义并供未来
	//     异步 refresh 使用）。
	//   - protoCacheRefreshPending：stale 检测到但本批因 foreground/cooldown 未刷新，
	//     标记 pending；被拒绝的 refresh 不清 pending。
	//   - protoCacheRefreshCooldownUntil：成功后最小间隔 / 失败后退避的截止时刻。
	protoCacheRefreshRunning       bool
	protoCacheRefreshPending       bool
	protoCacheRefreshCooldownUntil time.Time

	// protoCacheRefreshMinInterval / protoCacheRefreshFailCooldown 的可配置覆盖（测试用）。
	// 0 时使用包级常量。仅供测试通过 setProtoCacheRefreshIntervalsForTest 设置。
	protoCacheMinInterval time.Duration
	protoCacheFailCool    time.Duration

	workerDone chan struct{}
}

func newPeopleClusteringCoordinator(svc *peopleService) *peopleClusteringCoordinator {
	c := &peopleClusteringCoordinator{
		svc:              svc,
		feedbackCooldown: peopleFeedbackZeroResultWait,
	}
	c.cond = sync.NewCond(&c.mu)
	return c
}

// start launches the worker goroutine. It is safe to call once.
func (c *peopleClusteringCoordinator) start() {
	c.workerDone = make(chan struct{})
	go c.run()
}

// run is the worker loop. It is the only goroutine that calls
// runIncrementalClustering (via runClusterBatch).
func (c *peopleClusteringCoordinator) run() {
	defer close(c.workerDone)
	for {
		c.mu.Lock()
		for !c.stopping {
			if c.foregroundWaiters > 0 {
				// A foreground mutation is waiting or in progress — it has
				// priority. Do not start any clustering batch. Wait to be
				// woken (foreground end, new request, or stop).
				c.cond.Wait()
				continue
			}
			if c.feedbackRunnable() {
				break
			}
			if c.backgroundPending {
				break
			}
			c.cond.Wait()
		}
		if c.stopping {
			c.feedbackPending = false
			c.backgroundPending = false
			waiters := c.bgWaiters
			c.bgWaiters = nil
			c.mu.Unlock()
			c.drainWaiters(waiters, backgroundClusterResult{err: errCoordinatorStopped})
			return
		}

		// Decide which job to run. Feedback has priority over background.
		if c.feedbackRunnable() {
			c.feedbackPending = false
			mergedCount := c.mergedFeedbackRequests
			c.mergedFeedbackRequests = 0
			c.running = true
			c.mu.Unlock()
			c.runFeedbackJob(mergedCount)
			c.mu.Lock()
			c.running = false
			c.cond.Broadcast()
			c.mu.Unlock()
			continue
		}

		// Background batch.
		c.backgroundPending = false
		waiters := c.bgWaiters
		c.bgWaiters = nil
		c.running = true
		c.mu.Unlock()

		res := c.runClusterBatch(clusterSourceBackground)

		c.mu.Lock()
		c.running = false
		c.cond.Broadcast()
		c.mu.Unlock()

		c.drainWaiters(waiters, res)
	}
}

// feedbackRunnable reports whether a pending feedback recluster may start now
// (i.e. not suppressed by the zero-result cooldown). Caller must hold c.mu.
func (c *peopleClusteringCoordinator) feedbackRunnable() bool {
	return c.feedbackPending && !time.Now().Before(c.feedbackCooldownUntil)
}

// drainWaiters delivers a result to every blocked background caller.
func (c *peopleClusteringCoordinator) drainWaiters(waiters []chan backgroundClusterResult, res backgroundClusterResult) {
	for _, w := range waiters {
		select {
		case w <- res:
		default:
		}
	}
}

// runFeedbackJob executes one feedback recluster (hook or triggerRecluster).
// mergedRequests is the number of feedback requests coalesced into this run.
// Called only from the worker goroutine.
func (c *peopleClusteringCoordinator) runFeedbackJob(mergedRequests int) {
	startedAt := time.Now()
	source := clusterSourceFeedback

	hook := c.feedbackHookValue()
	var result model.ReclusterResult
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("feedback recluster panic: %v", r)
				logger.Errorf("people clustering coordinator: feedback recluster panic: %v", r)
			}
		}()
		if hook != nil {
			result = hook()
		} else {
			result = c.svc.triggerRecluster()
		}
	}()
	if err != nil {
		logger.Warnf("people clustering coordinator: source=%s feedback recluster failed: %v mergedRequests=%d elapsed=%s",
			source, err, mergedRequests, time.Since(startedAt).Round(time.Millisecond))
	} else {
		logger.Infof("people clustering coordinator: source=%s feedback recluster complete evaluated=%d reassigned=%d iterations=%d mergedRequests=%d elapsed=%s",
			source, result.Evaluated, result.Reassigned, result.Iterations, mergedRequests, time.Since(startedAt).Round(time.Millisecond))
	}

	// Cooldown after a zero-result run to avoid spinning on feedback when
	// nothing changed. Background clustering is still allowed during cooldown.
	if result.Reassigned == 0 {
		cooldown := c.feedbackCooldownValue()
		c.mu.Lock()
		c.feedbackCooldownUntil = time.Now().Add(cooldown)
		c.mu.Unlock()
		// Wake the worker when the cooldown expires so a pending feedback
		// request can be re-evaluated.
		time.AfterFunc(cooldown, c.cond.Broadcast)
	}
}

// runClusterBatch executes exactly one runIncrementalClustering call under
// writeGate.RLock, yielding to foreground waiters first. Called only from the
// worker goroutine (directly for background, and via triggerRecluster for
// feedback). protoCache is touched only here, so it stays single-goroutine.
//
// Task 9：protoCache cold/stale 构建在获取 writeGate.RLock 之前完成（buildClustProtoCache
// 不持锁）。foreground active 时跳过冷缓存构建并 return no work，避免冷启动时唯一能刷新
// 缓存的 worker 永远因 foreground 而不执行 refresh。stale 但 non-nil 的缓存本批次继续用
// 旧值，标记 refresh pending（Task 10 接入 coalescing/cooldown）。永远不在 writeGate.RLock
// 内同步 rebuild。
//
// 身份画像 shadow 处理在释放 writeGate.RLock 之后执行：matcher 的 ANN/Repository
// 查询与遥测写入不得长期占用 writeGate，否则会阻塞 foreground merge/split/move。
// shadow 处理失败不影响已经成功的 legacy 结果；其耗时不混入 writeGate 持有时间。
func (c *peopleClusteringCoordinator) runClusterBatch(source clusterSource) backgroundClusterResult {
	waitStart := time.Now()
	if c.waitForegroundClear() {
		// Stopped while waiting for foreground to clear.
		return backgroundClusterResult{err: errCoordinatorStopped}
	}
	foregroundWait := time.Since(waitStart)

	// Task 9/10：在获取 writeGate.RLock 前同步构建/刷新 protoCache（cold 或 stale），
	// 并施加 cooldown + coalescing。构建期间若 foreground 变 active，跳过构建并 return
	// no work，不触碰 writeGate。protoCache single-owner 不变量保持：只有本 worker
	// goroutine 读写 s.protoCache。
	if c.shouldRefreshProtoCache() {
		refreshed, skip, err := c.refreshProtoCacheOutsideGate(source)
		if err != nil {
			// 刷新失败：进入失败 cooldown，保持 pending 不清（下次再试）。
			c.recordProtoCacheRefreshFailure()
			return backgroundClusterResult{err: err}
		}
		if skip {
			// foreground active 阻止了 refresh startup 或构建中途变 active。
			// 保持 pending（不清），下次 worker 唤醒时再试。
			c.protoCacheRefreshPending = true
			return backgroundClusterResult{}
		}
		if refreshed {
			// 成功刷新：清 pending，进入成功最小间隔 cooldown。
			c.protoCacheRefreshPending = false
			c.protoCacheRefreshCooldownUntil = time.Now().Add(c.protoCacheRefreshMinIntervalValue())
		}
	}

	gateStart := time.Now()
	c.svc.writeGate.RLock()
	var res backgroundClusterResult
	func() {
		defer c.svc.writeGate.RUnlock()
		defer func() {
			if r := recover(); r != nil {
				res.err = fmt.Errorf("clustering panic: %v", r)
				logger.Errorf("people clustering coordinator: source=%s clustering panic: %v", source, r)
			}
		}()
		res.affectedPersonIDs, res.affectedPhotoIDs, res.shadowObservations, res.err = c.svc.runIncrementalClustering()
	}()
	elapsed := time.Since(gateStart)

	// shadow 处理在 writeGate 释放后执行：foreground merge/split/move 无需等待画像
	// 评分。处理失败不修改已成功的 legacy 结果；其耗时单独记录。
	if len(res.shadowObservations) > 0 {
		shadowStart := time.Now()
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("people clustering coordinator: source=%s shadow observation panic: %v", source, r)
				}
			}()
			c.svc.processIdentityShadowObservations(res.shadowObservations)
		}()
		// 处理完成后清空，避免结果回传时携带大 slice（调用方不再需要 observation）。
		res.shadowObservations = nil
		logger.Infof("people clustering coordinator: source=%s shadow observations processed elapsed=%s",
			source, time.Since(shadowStart).Round(time.Millisecond))
	}

	// Task 13：聚类批次成功后，对实际发生身份成员变化的人物批量触发统一画像失效。
	// affectedPersonIDs 已包含 legacy attach 目标、失去 Face 的来源人物、rescue attach 目标
	// 与新创建人物；pending-only 组件不会把人物加入 affected（markComponentPending 不写入
	// affectedPersonIDs）。批次失败（res.err != nil）不提交本批失效：仅在成功路径执行。
	// 一个批次最多调用一次；Person ID 由 hook 端去重。不在持有 writeGate 时写 profile 表。
	if res.err == nil && len(res.affectedPersonIDs) > 0 {
		c.svc.invalidateIdentityProfiles(IdentityProfileInvalidation{
			DirtyPersonIDs: res.affectedPersonIDs,
			Reason:         "clustering_assignment",
		})
	}

	if res.err != nil {
		logger.Warnf("people clustering coordinator: source=%s clustering failed foregroundWait=%s writeGateWait=%s batchElapsed=%s yieldedForForeground=%v err=%v",
			source, foregroundWait.Round(time.Millisecond), time.Since(gateStart).Round(time.Millisecond), elapsed.Round(time.Millisecond), foregroundWait > 0, res.err)
	} else {
		logger.Infof("people clustering coordinator: source=%s clustering done persons=%d photos=%d foregroundWait=%s batchElapsed=%s yieldedForForeground=%v",
			source, len(res.affectedPersonIDs), len(res.affectedPhotoIDs), foregroundWait.Round(time.Millisecond), elapsed.Round(time.Millisecond), foregroundWait > 0)
	}
	return res
}

// waitForegroundClear blocks (without holding writeGate) until no foreground
// mutation is waiting or in progress, or until the coordinator is stopping.
// Returns true if stopped.
func (c *peopleClusteringCoordinator) waitForegroundClear() bool {
	c.mu.Lock()
	for c.foregroundWaiters > 0 && !c.stopping {
		c.cond.Wait()
	}
	stopped := c.stopping
	c.mu.Unlock()
	return stopped
}

// protoCacheRefreshMinIntervalValue 返回成功刷新后的最小间隔（测试可覆盖，0 用包级常量）。
func (c *peopleClusteringCoordinator) protoCacheRefreshMinIntervalValue() time.Duration {
	if c.protoCacheMinInterval > 0 {
		return c.protoCacheMinInterval
	}
	return protoCacheRefreshMinInterval
}

// protoCacheRefreshFailCooldownValue 返回失败后退避间隔（测试可覆盖）。
func (c *peopleClusteringCoordinator) protoCacheRefreshFailCooldownValue() time.Duration {
	if c.protoCacheFailCool > 0 {
		return c.protoCacheFailCool
	}
	return protoCacheRefreshFailCooldown
}

// shouldRefreshProtoCache 报告本批次是否应尝试刷新 protoCache。规则（Task 10）：
//   - protoCache 不需要 refresh（fresh）→ false。
//   - 已有一个 refresh running → false（至多一 running，coalesce 进 running）。
//   - 在成功最小间隔 cooldown 内 → false（保持 pending）。
//   - 否则 → true。
//
// pending 状态不阻止下一次尝试：pending 表示“上次想刷但没刷成”，本次应继续尝试。
// 只由 worker goroutine 调用，无需加锁。
func (c *peopleClusteringCoordinator) shouldRefreshProtoCache() bool {
	if !c.svc.protoCacheNeedsRefresh() {
		return false
	}
	if c.protoCacheRefreshRunning {
		return false
	}
	if time.Now().Before(c.protoCacheRefreshCooldownUntil) {
		// 成功 cooldown 内：保持 pending，不刷新。
		c.protoCacheRefreshPending = true
		return false
	}
	return true
}

// refreshProtoCacheOutsideGate 在 writeGate 外构建 protoCache。返回：
//   - refreshed=true：成功构建并赋值给 s.protoCache。
//   - skip=true：foreground active 阻止了 refresh（启动前或构建中途变 active），调用方应
//     return no work 并保持 pending。
//   - err!=nil：构建失败，调用方应进入失败 cooldown。
//
// refreshed 与 skip 互斥；err 非 nil 时其余两者为零值。只由 worker goroutine 调用。
func (c *peopleClusteringCoordinator) refreshProtoCacheOutsideGate(source clusterSource) (refreshed, skip bool, err error) {
	if c.svc.backgroundCoordinator != nil && c.svc.backgroundCoordinator.ForegroundActive() {
		logger.Infof("people clustering coordinator: source=%s skip protoCache refresh: foreground active", source)
		return false, true, nil
	}
	c.protoCacheRefreshRunning = true
	defer func() { c.protoCacheRefreshRunning = false }()

	newCache, buildErr := c.svc.buildClustProtoCache()
	if buildErr != nil {
		return false, false, buildErr
	}
	// 构建完成后再检查 foreground：若变 active，丢弃结果 return no work。
	if c.svc.backgroundCoordinator != nil && c.svc.backgroundCoordinator.ForegroundActive() {
		logger.Infof("people clustering coordinator: source=%s discard rebuilt protoCache: foreground became active", source)
		return false, true, nil
	}
	c.svc.protoCache = newCache
	return true, false, nil
}

// recordProtoCacheRefreshFailure 记录一次失败刷新：进入失败 cooldown，保持 pending 不清。
func (c *peopleClusteringCoordinator) recordProtoCacheRefreshFailure() {
	c.protoCacheRefreshPending = true
	c.protoCacheRefreshCooldownUntil = time.Now().Add(c.protoCacheRefreshFailCooldownValue())
}

// setProtoCacheRefreshIntervalsForTest 覆盖 cooldown/min-interval（仅供测试）。
func (c *peopleClusteringCoordinator) setProtoCacheRefreshIntervalsForTest(minInterval, failCooldown time.Duration) {
	c.protoCacheMinInterval = minInterval
	c.protoCacheFailCool = failCooldown
}

// submitBackground requests one background clustering batch and blocks until
// the worker executes it (coalesced with any concurrent background requests)
// and returns the batch result. Safe to call from multiple goroutines.
func (c *peopleClusteringCoordinator) submitBackground() backgroundClusterResult {
	ch := make(chan backgroundClusterResult, 1)
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		return backgroundClusterResult{err: errCoordinatorStopped}
	}
	c.bgWaiters = append(c.bgWaiters, ch)
	c.backgroundPending = true
	c.cond.Broadcast()
	c.mu.Unlock()

	return <-ch
}

// scheduleFeedbackRecluster requests a feedback recluster. Multiple calls are
// coalesced: at most one running feedback plus one pending makeup run exist at
// any time. Calls received while a request is already pending (or while a
// feedback run is in progress) are counted as merged for observability.
func (c *peopleClusteringCoordinator) scheduleFeedbackRecluster() {
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		return
	}
	if c.feedbackPending {
		// Already pending — this request merges into the single pending slot.
		c.mergedFeedbackRequests++
		c.mu.Unlock()
		return
	}
	c.feedbackPending = true
	c.cond.Broadcast()
	c.mu.Unlock()
}

// addForegroundWaiter / removeForegroundWaiter register a foreground mutation
// in progress so the worker yields before starting the next clustering batch.
func (c *peopleClusteringCoordinator) addForegroundWaiter() {
	c.mu.Lock()
	c.foregroundWaiters++
	// Broadcast so a worker about to start a batch re-checks and yields.
	c.cond.Broadcast()
	c.mu.Unlock()
}

func (c *peopleClusteringCoordinator) removeForegroundWaiter() {
	c.mu.Lock()
	c.foregroundWaiters--
	if c.foregroundWaiters < 0 {
		c.foregroundWaiters = 0
	}
	// Broadcast so the worker can resume once foreground clears.
	c.cond.Broadcast()
	c.mu.Unlock()
}

// foregroundWaiterCount returns the current count (for tests/observability).
func (c *peopleClusteringCoordinator) foregroundWaiterCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.foregroundWaiters
}

// isRunning reports whether the worker is currently executing a clustering job.
func (c *peopleClusteringCoordinator) isRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

// stop shuts the coordinator down: no new requests are accepted, pending
// background work is cleared, the worker is signalled to exit, and the call
// blocks until the worker goroutine has finished. It is idempotent.
func (c *peopleClusteringCoordinator) stop() {
	c.mu.Lock()
	if c.stopping {
		c.mu.Unlock()
		return
	}
	c.stopping = true
	c.cond.Broadcast()
	c.mu.Unlock()

	if c.workerDone != nil {
		<-c.workerDone
	}
}

// --- feedback configuration (test-configurable) ---

func (c *peopleClusteringCoordinator) setFeedbackHook(hook func() model.ReclusterResult) {
	c.fbMu.Lock()
	defer c.fbMu.Unlock()
	c.feedbackHook = hook
}

func (c *peopleClusteringCoordinator) feedbackHookValue() func() model.ReclusterResult {
	c.fbMu.Lock()
	defer c.fbMu.Unlock()
	return c.feedbackHook
}

func (c *peopleClusteringCoordinator) setFeedbackCooldown(d time.Duration) {
	c.fbMu.Lock()
	defer c.fbMu.Unlock()
	c.feedbackCooldown = d
}

func (c *peopleClusteringCoordinator) feedbackCooldownValue() time.Duration {
	c.fbMu.Lock()
	defer c.fbMu.Unlock()
	if c.feedbackCooldown <= 0 {
		return peopleFeedbackZeroResultWait
	}
	return c.feedbackCooldown
}
