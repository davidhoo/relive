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
