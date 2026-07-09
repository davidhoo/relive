package service

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
)

// identityProfileCoordinator 是身份画像后台构建的轻量调度器。
//
// 它把原有的“每轮串行切片”升级为“有界并发构建 + 串行写入提交 + 批末 ANN 合并调度”，
// 同时在启动新批次前向前台 People 操作让路。它不替代 repository / builder / ANN 组件，
// 只负责编排。
//
// 并发模型：
//   - worker 仅执行只读与纯计算（ListProfileFaces + builder.Build），结果通过 channel 回收；
//   - 所有写入（ReplaceGeneration / DeleteInactiveGenerations / MarkFailed / 删除清理）在协调器
//     goroutine 内串行提交，仍走全局 WriteQueue，禁止并发写 SQLite generation；
//   - 单个 build 失败只 MarkFailed 该人物，不阻断同批其他 person；
//   - 已开始的小批次允许完成，但批次规模受 dirtyBatchSize 与 slice budget 约束，避免长时间阻塞前台。
//
// 让路模型：
//   - foregroundBusyFn 在启动新批次前被检查；若前台 People 写操作等待/进行中则跳过本轮构建，
//     但已开始的单个人物构建不会被中断；
//   - 让路不阻塞前台，协调器直接返回，下一个调度 tick 再尝试。
type identityProfileCoordinator struct {
	owner *personIdentityProfileService

	workers       int
	dirtyBatch    int
	budget        time.Duration
	annDeltaThresh float64

	// foregroundBusyFn 返回 true 表示前台 People 写操作正在等待或进行中，应让路。
	// nil 时视为始终空闲（legacy / 未注入），不影响功能。
	foregroundBusyFn func() bool

	// backgroundCoordinator 是统一后台任务准入控制器（Task 11）。nil 时不 gating。
	// ANN full rebuild 调用前请求 BackgroundTaskIdentityANNRebuild 准入，被拒绝则保持
	// rebuildRequested=true（pending），下次切片再试。
	backgroundCoordinator *BackgroundTaskCoordinator

	// annRebuildCooldownUntil 是 ANN full rebuild 的成功最小间隔截止时刻。cooldown 内
	// 即使 rebuildRequested=true 也不触发 full rebuild，保持 pending。Task 11。
	annRebuildCooldownUntil time.Time
	// annRebuildMinInterval 成功 rebuild 后的最小间隔，默认 10 分钟。0 用默认值。
	annRebuildMinInterval time.Duration

	// repoFace 是后台 face 仓库（ListProfileFaces）；与 owner.bgFaceRepo 一致，抽出便于注入测试。
	repoFace repository.FaceRepository
	// builder 与 owner.builder 一致，抽出便于注入测试。
	builder identityProfileBuilderIface

	nowFn func() time.Time

	// runMu 保证 RunBackgroundSlice 串行进入（调度器每分钟一次，理论不会重入；
	// 显式锁防止误用与测试并发触发）。
	runMu   sync.Mutex
	running bool

	statsMu sync.RWMutex
	stats   identityCoordinatorStats
}

// annRebuildDefaultMinInterval 是 ANN full rebuild 成功后的默认最小间隔。
const annRebuildDefaultMinInterval = 10 * time.Minute

// annRebuildMinIntervalValue 返回生效的最小间隔（0 用默认）。
func (c *identityProfileCoordinator) annRebuildMinIntervalValue() time.Duration {
	if c.annRebuildMinInterval > 0 {
		return c.annRebuildMinInterval
	}
	return annRebuildDefaultMinInterval
}

// identityCoordinatorStats 是协调器最近一轮的运行统计快照（脱敏，不含 ID/embedding/路径）。
type identityCoordinatorStats struct {
	lastSliceStartedAt *time.Time
	lastSliceEndedAt   *time.Time

	lastDirtySelected int
	lastBuiltSuccess  int
	lastBuiltFailed   int
	lastSkipped       int

	lastBuildDurationMs int64
	maxBuildDurationMs  int64
	lastWriteDurationMs int64

	lastAnnActivated     int
	lastAnnRebuild       bool
	lastAnnRebuildReason string
}

// identityProfileBuildResult 是单个 worker 的构建结果。
type identityProfileBuildResult struct {
	personID uint
	build    *model.PersonIdentityProfileBuild
	err      error
	// listErr 为读取人脸阶段的错误（与构建错误区分，便于观测瓶颈在读取还是构建）。
	listErr error
	// buildDuration 是纯构建（含读脸）耗时。
	buildDuration time.Duration
	// deleted 标记人物在读取/构建期间被删除，应走清理路径而非 MarkFailed。
	deleted bool
}

// ANN rebuild 触发原因（脱敏稳定字符串，不含 ID/路径/SQL）。
const (
	annRebuildReasonDeltaFull    = "delta_full"
	annRebuildReasonDeltaRatio   = "delta_ratio_threshold"
	annRebuildReasonRequested    = "rebuild_requested"
	annRebuildReasonBatchLarge   = "batch_large"
	annRebuildReasonFirstBuild   = "first_build"
)

// newIdentityProfileCoordinator 构造协调器。owner 提供 repo/builder/ann/nowFn 等依赖；
// workers/dirtyBatch/budget/annDeltaThresh 来自配置。foregroundBusyFn 由 service 注入。
func newIdentityProfileCoordinator(owner *personIdentityProfileService, workers, dirtyBatch, budgetMs int, annDeltaThresh float64) *identityProfileCoordinator {
	if workers < 1 {
		workers = 1
	}
	if workers > 4 {
		workers = 4
	}
	if dirtyBatch < 1 {
		dirtyBatch = 1
	}
	budget := time.Duration(budgetMs) * time.Millisecond
	if budget <= 0 {
		budget = 5 * time.Second
	}
	return &identityProfileCoordinator{
		owner:          owner,
		workers:        workers,
		dirtyBatch:     dirtyBatch,
		budget:         budget,
		annDeltaThresh: annDeltaThresh,
		nowFn:          time.Now,
	}
}

// setForegroundBusyFn 注入前台让路判定函数。service 装配时调用一次。
func (c *identityProfileCoordinator) setForegroundBusyFn(fn func() bool) {
	c.foregroundBusyFn = fn
}

// setBackgroundCoordinator 注入统一后台任务准入控制器（Task 11）。nil 时不 gating。
func (c *identityProfileCoordinator) setBackgroundCoordinator(coord *BackgroundTaskCoordinator) {
	c.backgroundCoordinator = coord
}

// setAnnRebuildMinIntervalForTest 覆盖 ANN rebuild 最小间隔（仅供测试）。
func (c *identityProfileCoordinator) setAnnRebuildMinIntervalForTest(d time.Duration) {
	c.annRebuildMinInterval = d
}

// maybeRebuildANN 是 ANN full rebuild 的统一入口（Task 11）。在调用 owner.rebuildANNFromCoordinator
// 前施加 cooldown + coordinator 准入：
//   - cooldown 内（成功最小间隔未到）：记录 skip，保持 rebuildRequested=true（pending），return。
//   - coordinator 拒绝（foreground active / cooldown）：保持 pending，return。
//   - 通过：执行 rebuild。成功后 ann.RebuildRequested() 变 false 并进入成功 cooldown。
//
// reason 仅用于日志/统计。调用方持有 runMu（RunBackgroundSlice 串行），无需额外同步。
//
// 注意：cooldown 在 rebuildRequested=true 时仍跳过会延迟首次失败重试。为不破坏既有失败语义
// （失败后应尽快重试并记录 failed 状态），仅当上一次 rebuild 成功后才进入成功 cooldown；
// 失败路径（rebuildRequested 仍 true 且上一次未成功）不设 cooldown，由 rebuildANNIfNeeded
// 自身的失败重试语义处理。
func (c *identityProfileCoordinator) maybeRebuildANN(reason string) {
	if c.owner == nil || c.owner.ann == nil || !c.owner.ann.RebuildRequested() {
		return
	}
	// coordinator 准入：被拒绝则保持 pending，不阻塞 foreground。
	if c.backgroundCoordinator != nil {
		release, decision, ok := c.backgroundCoordinator.Begin(BackgroundTaskRequest{
			Class:    BackgroundTaskIdentityANNRebuild,
			Priority: BackgroundPriorityAutomatic,
		})
		if !ok {
			logger.Infof("identity profile coordinator: ann rebuild skipped (coordinator: %s) reason=%s pending=true", decision.Reason, reason)
			return
		}
		defer release()
	}
	logger.Infof("identity profile coordinator: ann rebuild requested reason=%s", reason)
	c.recordAnnRebuild(reason)
	wasRequested := true
	c.owner.rebuildANNFromCoordinator()
	// rebuildANNFromCoordinator 成功会把 rebuildRequested 置 false；失败保持 true。
	if c.owner.ann.RebuildRequested() {
		// 仍 pending：rebuild 失败，不进入成功 cooldown，下次切片立即重试（保持既有失败语义）。
		wasRequested = false
	}
	if wasRequested {
		// 成功：进入成功最小间隔 cooldown，合并短时间内的 delta_full 重复请求。
		c.annRebuildCooldownUntil = c.nowFn().Add(c.annRebuildMinIntervalValue())
	}
}

// setNowFn 注入时钟，测试无需真实 sleep。
func (c *identityProfileCoordinator) setNowFn(fn func() time.Time) {
	c.nowFn = fn
}

// now 返回当前时钟，默认 owner 的 nowFn。
func (c *identityProfileCoordinator) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	if c.owner != nil && c.owner.nowFn != nil {
		return c.owner.nowFn()
	}
	return time.Now()
}

// isRunning 返回协调器是否正在执行一个 slice（仅观测用）。
func (c *identityProfileCoordinator) isRunning() bool {
	c.statsMu.RLock()
	defer c.statsMu.RUnlock()
	return c.running
}

// snapshotStats 返回协调器统计的只读副本。
func (c *identityProfileCoordinator) snapshotStats() identityCoordinatorStats {
	c.statsMu.RLock()
	defer c.statsMu.RUnlock()
	return c.stats
}

// runSlice 执行一个有界 slice。返回是否实际执行了构建批次（用于观测与让路判定）。
//
// 流程：
//  1. 前台让路检查；
//  2. 拉取高/低优先级 dirty 批次；
//  3. 有界并发构建（只读+纯计算）；
//  4. 串行写入提交（WriteQueue）；
//  5. 批末 ANN 合并调度（activate 或 full rebuild）。
//  6. 即便本轮无 dirty，也按需处理 ANN rebuild_requested（首次构建/InvalidateAll 后）。
func (c *identityProfileCoordinator) runSlice() bool {
	// 串行进入：避免调度器并发触发或测试误用导致重入。
	c.runMu.Lock()
	defer c.runMu.Unlock()

	start := c.now()
	c.markSliceStart(start)
	defer c.markSliceEnd(c.now())

	if c.foregroundBusyFn != nil && c.foregroundBusyFn() {
		logger.Infof("identity profile coordinator slice skipped: foreground yielded")
		c.recordSkipped(0)
		// 让路时仍处理已被外部请求的 ANN rebuild，避免长时间不可用。
		c.processPendingAnnRebuild(start)
		return false
	}

	// 拉取 dirty 批次（高优先级优先，不足再补低优先级）。
	dirty, err := c.selectDirtyBatch()
	if err != nil {
		logger.Warnf("identity profile coordinator: list dirty failed err_category=%T", err)
		c.recordSkipped(0)
		// 拉取失败也按需处理 ANN rebuild。
		c.processPendingAnnRebuild(start)
		return false
	}
	if len(dirty) == 0 {
		c.recordSkipped(0)
		// 无 dirty 但存在 rebuild_requested → 处理 ANN 重建（首次构建/InvalidateAll 后）。
		c.processPendingAnnRebuild(start)
		return false
	}
	logger.Infof("identity profile coordinator slice started: dirty_batch_selected=%d workers=%d", len(dirty), c.workers)

	// 并发构建（只读+纯计算），结果按 person_id ASC 稳定回收。
	results := c.buildConcurrent(dirty, start)

	// 串行写入提交。
	committedIDs := c.commitSerial(results)

	// 批末 ANN 合并调度。
	c.applyAnnMerge(committedIDs, start)

	c.recordSliceResult(len(dirty), len(committedIDs))
	return true
}

// processPendingAnnRebuild 在无 dirty 或让路时，若 ANN 存在 rebuild_requested 则触发一次 full rebuild。
// 确保 InvalidateAll / 首次构建后即使没有 dirty 也能恢复 ANN ready。
// Task 11：经 maybeRebuildANN 统一施加 cooldown + coordinator 准入。
func (c *identityProfileCoordinator) processPendingAnnRebuild(sliceStart time.Time) {
	if c.owner == nil || c.owner.ann == nil {
		return
	}
	c.maybeRebuildANN(annRebuildReasonRequested)
}

// identityProfileHighPriorityReasons 是前台 People 操作产生的 dirty_reason 白名单，
// 这些 dirty 视为高优先级优先构建。与 repository 的失效原因白名单保持一致
//（reset_all_people 走 ResetAll 路径，不落 dirty_reason，故不在此列）。
var identityProfileHighPriorityReasons = []string{
	"detection_replaced_faces",
	"people_merged",
	"person_split",
	"faces_moved",
	"person_dissolved",
	"clustering_assignment",
	"recluster_assignment",
	"rescue_attach",
}

// selectDirtyBatch 拉取一批 dirty 人物，高优先级（前台操作产生）优先。
//
// 实现分两次查询，避免单次 ListDirty 仅按 person_id 排序导致高优先级被低优先级挤出的缺陷
//（当总 dirty > dirtyBatch 且高优先级 person_id 较大时，单次按 person_id 拉取会漏掉高优先级）：
//  1. 先按高优先级 reason 过滤拉取（ListDirtyByReasons），填满 limit；
//  2. 不足时从 cursor=0 重新拉取剩余所有 dirty（不按 reason 过滤），按 person_id ASC，
//     跳过已选的高优先级，补足到 limit。
//
// 步骤 2 从 0 重新拉取而非使用游标，因为低优先级人物 person_id 既可能小于也可能大于高优先级，
// 单一方向游标无法覆盖；多拉取 limit+已选数 后在内存去重，受 dirtyBatch（默认 10）约束规模可控。
func (c *identityProfileCoordinator) selectDirtyBatch() ([]*model.PersonIdentityProfile, error) {
	if c.owner == nil || c.owner.bgRepo == nil {
		return nil, nil
	}
	limit := c.dirtyBatch

	// 高优先级：前台 People 操作产生的 dirty。
	high, err := c.listDirtyByReasons(identityProfileHighPriorityReasons, 0, limit)
	if err != nil {
		return nil, err
	}
	selected := high
	if len(selected) < limit {
		// 补足低优先级：从 0 拉取剩余所有 dirty（含已选高优先级），内存去重补足。
		need := limit - len(selected)
		// 多拉 need+len(selected) 条以保证去重后仍有 need 条可用。
		rest, err := c.listDirtyByReasons(nil, 0, need+len(selected))
		if err != nil {
			return nil, err
		}
		dup := make(map[uint]struct{}, len(selected))
		for _, p := range selected {
			dup[p.PersonID] = struct{}{}
		}
		for _, p := range rest {
			if _, ok := dup[p.PersonID]; ok {
				continue
			}
			selected = append(selected, p)
			if len(selected) >= limit {
				break
			}
		}
	}
	// 稳定排序：高优先级在前，person_id ASC。
	sort.SliceStable(selected, func(i, j int) bool {
		hi, hj := isHighPriorityDirty(selected[i]), isHighPriorityDirty(selected[j])
		if hi != hj {
			return hi // 高优先级在前
		}
		return selected[i].PersonID < selected[j].PersonID
	})
	return selected, nil
}

// listDirtyByReasons 调用仓库的 ListDirtyByReasons；若仓库未实现该方法（旧测试桩）则回退到 ListDirty。
func (c *identityProfileCoordinator) listDirtyByReasons(reasons []string, cursor uint, limit int) ([]*model.PersonIdentityProfile, error) {
	type reasonFilterRepo interface {
		ListDirtyByReasons(reasons []string, cursor uint, limit int) ([]*model.PersonIdentityProfile, error)
	}
	if r, ok := c.owner.bgRepo.(reasonFilterRepo); ok {
		return r.ListDirtyByReasons(reasons, cursor, limit)
	}
	// 回退：不按 reason 过滤（行为等同 ListDirty）。
	return c.owner.bgRepo.ListDirty(cursor, limit)
}

// isHighPriorityDirty 判断 dirty 是否由前台 People 操作产生（高优先级）。
// backfill 与空 reason 视为低优先级。
func isHighPriorityDirty(p *model.PersonIdentityProfile) bool {
	if p == nil {
		return false
	}
	switch p.DirtyReason {
	case "backfill":
		return false
	case "":
		return false
	default:
		return true
	}
}

// buildConcurrent 并发执行只读+纯计算构建。worker 数受 c.workers 限制。
// 结果按 person_id ASC 稳定返回。预算用尽后不再派发新 worker，但已派发的允许完成。
func (c *identityProfileCoordinator) buildConcurrent(dirty []*model.PersonIdentityProfile, sliceStart time.Time) []identityProfileBuildResult {
	if len(dirty) == 0 {
		return nil
	}
	type job struct {
		personID uint
	}
	jobs := make(chan job, len(dirty))
	for _, p := range dirty {
		jobs <- job{personID: p.PersonID}
	}
	close(jobs)

	deadline := sliceStart.Add(c.budget)
	results := make([]identityProfileBuildResult, 0, len(dirty))
	var resultsMu sync.Mutex
	var wg sync.WaitGroup

	workerCount := c.workers
	if workerCount > len(dirty) {
		workerCount = len(dirty)
	}

	for w := 0; w < workerCount; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				// 预算检查：在取下一个 job 前检查时间预算，超时则停止派发。
				if !c.nowFn().Before(deadline) {
					return
				}
				j, ok := <-jobs
				if !ok {
					return
				}
				res := c.buildOne(j.personID)
				resultsMu.Lock()
				results = append(results, res)
				resultsMu.Unlock()
			}
		}()
	}
	wg.Wait()

	// 稳定排序：person_id ASC。
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].personID < results[j].personID
	})
	return results
}

// buildOne 执行单个人物的只读+纯计算构建。不写数据库。
func (c *identityProfileCoordinator) buildOne(personID uint) identityProfileBuildResult {
	start := c.now()
	res := identityProfileBuildResult{personID: personID}

	faceRepo := c.repoFace
	if faceRepo == nil {
		faceRepo = c.owner.bgFaceRepo
	}
	faces, err := faceRepo.ListProfileFaces(personID)
	if err != nil {
		res.listErr = err
		res.buildDuration = c.now().Sub(start)
		return res
	}

	builder := c.builder
	if builder == nil {
		builder = c.owner.builder
	}
	build, err := builder.Build(personID, faces)
	if err != nil {
		// 人物中途被删除通常表现为 builder 无法构建；这里不在此判定删除，
		// 删除判定交给提交阶段的 ReplaceGeneration（ErrPersonNotFound）。
		res.err = err
		res.buildDuration = c.now().Sub(start)
		return res
	}
	build.Profile.EmbeddingModel = c.owner.embeddingModel
	res.build = build
	res.buildDuration = c.now().Sub(start)
	if d := res.buildDuration.Milliseconds(); d > 0 {
		c.updateMaxBuildDuration(d)
	}
	return res
}

// commitSerial 串行提交构建结果到数据库（通过 WriteQueue）。返回成功激活的人物 ID 列表。
func (c *identityProfileCoordinator) commitSerial(results []identityProfileBuildResult) []uint {
	if c.owner == nil {
		return nil
	}
	var committed []uint
	for _, r := range results {
		writeStart := c.nowFn()
		if r.listErr != nil {
			// 读脸失败：MarkFailed 记录原因，保留旧 generation。
			c.owner.markFailed(r.personID, truncateMessage("list profile faces: "+r.listErr.Error(), identityProfileErrMsgMax))
			c.recordWriteDuration(writeStart)
			continue
		}
		if r.err != nil {
			c.owner.markFailed(r.personID, truncateMessage(r.err.Error(), identityProfileErrMsgMax))
			c.recordWriteDuration(writeStart)
			continue
		}
		if r.build == nil {
			// 无构建结果（理论上不会出现），跳过。
			c.recordWriteDuration(writeStart)
			continue
		}

		writeErr := c.owner.executeWrite(func() error {
			return c.owner.bgRepo.ReplaceGeneration(r.personID, r.build)
		})
		c.recordWriteDuration(writeStart)
		if writeErr != nil {
			if errors.Is(writeErr, repository.ErrPersonNotFound) {
				// 人物在构建后、提交前被删除：清理派生画像，不计系统失败。
				c.owner.cleanupDeletedPerson(r.personID)
				continue
			}
			// 写入失败：ReplaceGeneration 已原子回滚，旧 generation 保留。
			c.owner.markFailed(r.personID, truncateMessage("replace generation: "+writeErr.Error(), identityProfileErrMsgMax))
			logger.Warnf("identity profile coordinator: generation commit failed err_category=%T", writeErr)
			continue
		}

		// 成功：清理过期 generation，保留 active + 最近一个历史 generation。
		if err := c.owner.executeWrite(func() error {
			return c.owner.bgRepo.DeleteInactiveGenerations(r.personID, 1)
		}); err != nil {
			logger.Warnf("identity profile coordinator: cleanup inactive generations err_category=%T", err)
		}
		committed = append(committed, r.personID)
		logger.Infof("identity profile coordinator: generation commit finished person_id_order=%d", len(committed))
	}
	return committed
}

// applyAnnMerge 批末统一处理 ANN：小批量成功且 delta 空间充足 → 批量 activate；
// 否则合并为一次 full rebuild（只由协调器触发，避免多路径重复请求）。
// rebuild 失败保持 fail-closed，不回滚已激活的数据库 generation。
func (c *identityProfileCoordinator) applyAnnMerge(committedIDs []uint, sliceStart time.Time) {
	if c.owner == nil || c.owner.ann == nil {
		return
	}
	successCount := len(committedIDs)

	// 判断是否需要 full rebuild。
	rebuild, reason := c.shouldFullRebuild(successCount)

	if rebuild {
		// Task 11：经 maybeRebuildANN 统一施加 cooldown + coordinator 准入。
		c.maybeRebuildANN(reason)
		return
	}

	// 小批量 activate：对每个成功提交的人物，从数据库读取当前活动 generation 中心接入 delta。
	if successCount == 0 {
		c.recordAnnActivate(0)
		return
	}
	activated := 0
	for _, pid := range committedIDs {
		if c.activateOne(pid) {
			activated++
		}
	}
	logger.Infof("identity profile coordinator: ann activate batch count=%d", activated)
	c.recordAnnActivate(activated)
	// 若 activate 过程中触发 delta full（RequestRebuild 被置位），合并为一次 full rebuild。
	// Task 11：经 maybeRebuildANN 统一施加 cooldown + coordinator 准入。
	c.maybeRebuildANN(annRebuildReasonDeltaFull)
}

// activateOne 从数据库读取人物活动 generation 的真实中心并接入 ANN delta。
// 复用 owner.activateANN 逻辑（保持 fail-closed 语义）。返回是否成功 activate。
func (c *identityProfileCoordinator) activateOne(personID uint) bool {
	if c.owner == nil || c.owner.ann == nil {
		return false
	}
	ann := c.owner.ann
	build, err := c.owner.bgRepo.GetActive(personID)
	if err != nil {
		logger.Warnf("identity profile coordinator: ann get active err_category=%T", err)
		ann.RequestRebuild()
		return false
	}
	if build == nil || build.Profile == nil || build.Profile.ActiveGeneration == 0 {
		ann.InvalidatePerson(personID)
		return false
	}
	if err := ann.Activate(personID, build.Profile.ActiveGeneration, build.Centers); err != nil {
		logger.Warnf("identity profile coordinator: ann activate err_category=%T", err)
		// delta 更新失败（容量上限或非法中心）：标记不可用并请求完整重建。
		ann.RequestRebuild()
		return false
	}
	return true
}

// shouldFullRebuild 决定批末是否触发 full rebuild。
//
// 触发条件（任一）：
//   - ANN 尚无 snapshot（首次构建）；
//   - rebuild_requested 已被置位（delta full / InvalidateAll 等）；
//   - delta 占用比例达到阈值；
//   - 本批成功人数较多（>= deltaMax 的一半），继续 activate 风险高。
func (c *identityProfileCoordinator) shouldFullRebuild(successCount int) (bool, string) {
	ann := c.owner.ann
	if ann == nil {
		return false, ""
	}
	snap := ann.Stats(c.owner.embeddingModel)
	// rebuild_requested 优先：delta full / InvalidateAll 等外部请求必须立即 rebuild。
	if snap.RebuildRequested {
		return true, annRebuildReasonRequested
	}
	if snap.SnapshotNodes == 0 && successCount > 0 {
		return true, annRebuildReasonFirstBuild
	}
	if snap.DeltaMax > 0 {
		ratio := float64(snap.DeltaNodes) / float64(snap.DeltaMax)
		if ratio >= c.annDeltaThresh {
			return true, annRebuildReasonDeltaRatio
		}
		if snap.DeltaNodes >= snap.DeltaMax {
			return true, annRebuildReasonDeltaFull
		}
	}
	if successCount > 0 && snap.DeltaMax > 0 && successCount >= snap.DeltaMax/2 {
		return true, annRebuildReasonBatchLarge
	}
	return false, ""
}

// ---- stats 记录（脱敏） ----

func (c *identityProfileCoordinator) markSliceStart(t time.Time) {
	c.statsMu.Lock()
	c.running = true
	c.stats.lastSliceStartedAt = &t
	c.stats.lastDirtySelected = 0
	c.stats.lastBuiltSuccess = 0
	c.stats.lastBuiltFailed = 0
	c.stats.lastSkipped = 0
	c.stats.lastAnnActivated = 0
	c.stats.lastAnnRebuild = false
	c.stats.lastAnnRebuildReason = ""
	c.statsMu.Unlock()
}

func (c *identityProfileCoordinator) markSliceEnd(t time.Time) {
	c.statsMu.Lock()
	c.running = false
	c.stats.lastSliceEndedAt = &t
	c.statsMu.Unlock()
}

func (c *identityProfileCoordinator) recordSkipped(n int) {
	c.statsMu.Lock()
	c.stats.lastSkipped = n
	c.statsMu.Unlock()
}

func (c *identityProfileCoordinator) recordSliceResult(dirty, success int) {
	c.statsMu.Lock()
	c.stats.lastDirtySelected = dirty
	c.stats.lastBuiltSuccess = success
	c.stats.lastBuiltFailed = dirty - success
	// 记录构建耗时（最近值已在 buildOne 更新）。
	c.statsMu.Unlock()
}

func (c *identityProfileCoordinator) updateMaxBuildDuration(d int64) {
	c.statsMu.Lock()
	if d > c.stats.maxBuildDurationMs {
		c.stats.maxBuildDurationMs = d
	}
	c.stats.lastBuildDurationMs = d
	c.statsMu.Unlock()
}

func (c *identityProfileCoordinator) recordWriteDuration(start time.Time) {
	d := c.now().Sub(start).Milliseconds()
	if d < 0 {
		d = 0
	}
	c.statsMu.Lock()
	c.stats.lastWriteDurationMs = d
	c.statsMu.Unlock()
}

func (c *identityProfileCoordinator) recordAnnRebuild(reason string) {
	c.statsMu.Lock()
	c.stats.lastAnnRebuild = true
	c.stats.lastAnnRebuildReason = reason
	c.stats.lastAnnActivated = 0
	c.statsMu.Unlock()
}

func (c *identityProfileCoordinator) recordAnnActivate(count int) {
	c.statsMu.Lock()
	c.stats.lastAnnRebuild = false
	c.stats.lastAnnRebuildReason = ""
	c.stats.lastAnnActivated = count
	c.statsMu.Unlock()
}

// toStatsResponse 将协调器统计转为只读 DTO。
func (c *identityProfileCoordinator) toStatsResponse() model.IdentityCoordinatorStats {
	if c == nil {
		return model.IdentityCoordinatorStats{Workers: 0}
	}
	s := c.snapshotStats()
	return model.IdentityCoordinatorStats{
		Running:             c.isRunning(),
		LastSliceStartedAt:  s.lastSliceStartedAt,
		LastSliceEndedAt:    s.lastSliceEndedAt,
		LastDirtySelected:   s.lastDirtySelected,
		LastBuiltSuccess:    s.lastBuiltSuccess,
		LastBuiltFailed:     s.lastBuiltFailed,
		LastSkipped:         s.lastSkipped,
		Workers:             c.workers,
		LastBuildDurationMs: s.lastBuildDurationMs,
		MaxBuildDurationMs:  s.maxBuildDurationMs,
		LastWriteDurationMs: s.lastWriteDurationMs,
		LastAnnActivated:    s.lastAnnActivated,
		LastAnnRebuild:      s.lastAnnRebuild,
		LastAnnRebuildReason: s.lastAnnRebuildReason,
	}
}
