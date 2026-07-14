package service

import (
	"sync"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// BackgroundTaskPriority 标记一个后台任务请求的优先级层级。
//
//   - user：用户显式触发、用户可见的持久任务（P1）。不被 foreground scope 或资源背压拒绝，
//     最多在系统繁忙时标记 throttled，仍允许运行。
//   - automatic：系统自动维护（P2）。可被 foreground scope、cooldown、资源背压延迟或跳过，
//     且允许落后。
//
// P0 前台用户操作不经过本 coordinator（它们通过 BeginForeground 注册 foreground scope）。
type BackgroundTaskPriority string

const (
	BackgroundPriorityUser      BackgroundTaskPriority = "user"
	BackgroundPriorityAutomatic BackgroundTaskPriority = "automatic"
)

// BackgroundTaskClass 标识一类后台任务。同一 class 共享一条 cooldown 线，用于按类别限流。
// Phase 2 第一波只接 People 相关重路径；其余 class 预留给后续波次，不会在第一波接入。
type BackgroundTaskClass string

const (
	BackgroundTaskPeopleClustering     BackgroundTaskClass = "people_clustering"
	BackgroundTaskFeedbackRecluster    BackgroundTaskClass = "people_feedback_recluster"
	BackgroundTaskProtoCacheRefresh    BackgroundTaskClass = "people_proto_cache_refresh"
	BackgroundTaskIdentityProfileBuild BackgroundTaskClass = "identity_profile_build"
	BackgroundTaskIdentityANNRebuild   BackgroundTaskClass = "identity_ann_rebuild"
	BackgroundTaskMergeSuggestion      BackgroundTaskClass = "merge_suggestion_refresh"
	// BackgroundTaskEventClustering：扫描完成后自动触发的事件增量聚类（P2 automatic）。
	// 经 coordinator 准入 + coalescing，foreground active / iowait_high / cooldown 时保持
	// pending 不执行；用户显式 StartClustering/StartRebuild 仍走 P1 user，不被拒绝。
	BackgroundTaskEventClustering BackgroundTaskClass = "event_clustering"
)

// BackgroundTaskRequest 描述一次后台任务准入请求。
//
//   - Class：任务类别，决定 cooldown 归属。
//   - Priority：user 不被拒绝；automatic 受 foreground/cooldown 背压约束。
//   - DedupeKey：非空时，相同 (Class, DedupeKey) 的 automatic 请求在 pending/running 期间
//     被合并（coalesce）——只保留一个 running + 一个 pending，第二个被拒绝为 coalesced。
//   - MaxRuntime：预留字段，第一波仅记录到 decision，不强制 kill（执行预算由各 worker
//     自身的 checkpoint 保证）。
type BackgroundTaskRequest struct {
	Class      BackgroundTaskClass
	Priority   BackgroundTaskPriority
	DedupeKey  string
	MaxRuntime time.Duration
}

// BackgroundDecisionReason 是准入决策的原因码，用于可观测性与状态 API。
type BackgroundDecisionReason string

const (
	BackgroundDecisionAllowed           BackgroundDecisionReason = "allowed"
	BackgroundDecisionCoalesced         BackgroundDecisionReason = "coalesced"
	BackgroundDecisionForeground        BackgroundDecisionReason = "foreground_active"
	BackgroundDecisionCooldown          BackgroundDecisionReason = "cooldown"
	BackgroundDecisionAlreadyRunning    BackgroundDecisionReason = "already_running"
	BackgroundDecisionCPUHigh           BackgroundDecisionReason = "cpu_high"
	BackgroundDecisionIOWaitHigh        BackgroundDecisionReason = "iowait_high"
	BackgroundDecisionMemoryHigh        BackgroundDecisionReason = "memory_high"
	BackgroundDecisionAutomaticDisabled BackgroundDecisionReason = "automatic_disabled"
)

// BackgroundTaskDecision 是一次准入决策的结果。Reason=allowed 且 Allowed=true 时可运行；
// 其余情况调用方应跳过/延迟本次 work，但绝不能把对应的持久化任务标记为完成。
type BackgroundTaskDecision struct {
	Allowed bool
	Reason  BackgroundDecisionReason
	// CooldownUntil 非 zero 表示该 class 当前 cooldown 到该时刻（被拒绝时供调用方记录）。
	CooldownUntil time.Time
}

// BackgroundTaskSnapshot 是 coordinator 状态的只读快照，供状态 API 与日志使用。
type BackgroundTaskSnapshot struct {
	ForegroundActive bool
	ForegroundCount  int
	// Running 列出当前 running 的 (class, dedupeKey, startedAt)。
	Running []BackgroundTaskRuntime
	// Cooldowns 列出 class → cooldownUntil（仅未过期的）。
	Cooldowns map[BackgroundTaskClass]time.Time
	// PendingDedupe 列出已 coalesce pending 的 (class, dedupeKey)。
	PendingDedupe []BackgroundTaskDedupeEntry
}

// BackgroundTaskRuntime 描述一个正在运行的后台任务。
type BackgroundTaskRuntime struct {
	Class     BackgroundTaskClass
	DedupeKey string
	Priority  BackgroundTaskPriority
	StartedAt time.Time
}

// BackgroundTaskDedupeEntry 描述一个 pending 的去重槽位。
type BackgroundTaskDedupeEntry struct {
	Class     BackgroundTaskClass
	DedupeKey string
}

// dedupeSlot 记录一个 (class, dedupeKey) 的 running/pending 状态。同一 slot 同时最多
// 一个 running + 一个 pending；超过的请求被 coalesce 拒绝。
type dedupeSlot struct {
	running   bool
	pending   bool
	priority  BackgroundTaskPriority
	startedAt time.Time
}

// BackgroundTaskCoordinator 是自动后台 slice 的前台优先准入控制器（P2 gating）。
//
// 它是进程内、内存态的轻量协调器，不写 SQLite、不充当任务队列。职责仅限：
//   - 跟踪 foreground scope（P0 前台操作进行中时拒绝 P2 automatic）；
//   - 按 class 维护 cooldown（失败/DB busy 后退避）；
//   - 按 (class, dedupeKey) 合并并发 automatic 请求（至多一 running + 一 pending）；
//   - 暴露 Snapshot 供状态 API。
//
// 权威性：foreground scope 与 cooldown 是权威机制；host-level CPU/iowait 背压（Phase 4
// 接入）只是 advisory，不能单独拒绝 P2。P0 前台操作永远不被本 coordinator 拒绝——它们
// 通过 BeginForeground 注册 scope，coordinator 仅据此让 P2 让路。
type BackgroundTaskCoordinator struct {
	mu sync.Mutex

	// foregroundActive 是当前持有 foreground scope 的 P0 操作计数（>0 即 active）。
	foregroundActive int

	// cooldowns[class] = until。表示该 class 在 until 之前不允许 automatic 启动。
	cooldowns map[BackgroundTaskClass]time.Time

	// slots[(class, dedupeKey)] 跟踪 running/pending，用于 coalescing。
	slots map[BackgroundTaskDedupeEntry]*dedupeSlot

	// thresholds 是 advisory 资源背压阈值（Task 14）。0 表示禁用对应检查。autoTasksEnabled
	// 为 false 时所有 automatic 任务被拒绝为 automatic_disabled。loadFn 注入负载采样，
	// nil 时不做负载检查（unknown 不单独拒绝 P2）。
	autoTasksEnabled     bool
	cpuPauseThreshold    float64
	iowaitPauseThreshold float64
	memoryPauseThreshold float64
	loadFn               func() BackgroundLoadSnapshot

	// dbLockedCooldown 是 SQLite busy/locked 后 P2 automatic 进入 cooldown 的时长（Task 15）。
	// 0 表示不 cooldown（仅记录日志）。由 SetBackgroundConfig 设置。
	dbLockedCooldown time.Duration
}

// SetBackgroundConfig 注入后台任务治理配置与负载采样函数（Task 14/15）。loadFn 为 nil 时
// 不做负载背压（advisory）。dbLockedCooldown 为 SQLite busy/locked 后 P2 cooldown 时长，
// 0 表示不 cooldown。生产由 service.go 装配时调用。
func (c *BackgroundTaskCoordinator) SetBackgroundConfig(autoTasksEnabled bool, cpuPause, iowaitPause, memPause float64, loadFn func() BackgroundLoadSnapshot, dbLockedCooldown time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.autoTasksEnabled = autoTasksEnabled
	c.cpuPauseThreshold = cpuPause
	c.iowaitPauseThreshold = iowaitPause
	c.memoryPauseThreshold = memPause
	c.loadFn = loadFn
	c.dbLockedCooldown = dbLockedCooldown
}

// NewBackgroundTaskCoordinator 构造一个空 coordinator。默认 auto_tasks_enabled=true
// （与配置默认一致）；调用方可用 SetBackgroundConfig 覆盖。
func NewBackgroundTaskCoordinator() *BackgroundTaskCoordinator {
	return &BackgroundTaskCoordinator{
		cooldowns:        make(map[BackgroundTaskClass]time.Time),
		slots:            make(map[BackgroundTaskDedupeEntry]*dedupeSlot),
		autoTasksEnabled: true,
	}
}

// BeginForeground 注册一个 P0 前台操作正在进行，返回 release 函数。foreground active
// 期间，所有 automatic P2 请求被拒绝为 foreground_active。P0 操作本身不受影响。
//
// release 必须被调用（通常 defer），即使在 error/panic 路径——调用方负责配对。
func (c *BackgroundTaskCoordinator) BeginForeground() func() {
	c.mu.Lock()
	c.foregroundActive++
	active := c.foregroundActive
	c.mu.Unlock()
	logger.Debugf("background coordinator: foreground scope begin count=%d", active)

	once := sync.Once{}
	return func() {
		once.Do(c.releaseForeground)
	}
}

func (c *BackgroundTaskCoordinator) releaseForeground() {
	c.mu.Lock()
	c.foregroundActive--
	if c.foregroundActive < 0 {
		c.foregroundActive = 0
	}
	active := c.foregroundActive
	c.mu.Unlock()
	logger.Debugf("background coordinator: foreground scope end count=%d", active)
}

// ForegroundActive 报告当前是否有 P0 前台操作正在进行。
func (c *BackgroundTaskCoordinator) ForegroundActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.foregroundActive > 0
}

// LoadSnapshot 返回当前系统负载快照。loadFn 未注入时返回零值（所有字段 -1=unknown）。
// 供 protoCache rebuild 等后台任务做动态速度控制。
func (c *BackgroundTaskCoordinator) LoadSnapshot() BackgroundLoadSnapshot {
	c.mu.Lock()
	fn := c.loadFn
	c.mu.Unlock()
	if fn == nil {
		return BackgroundLoadSnapshot{CPUUserPct: -1, CPUIOWaitPct: -1, MemUsedPct: -1}
	}
	return fn()
}

// iowaitPauseThresholdLocked / cpuPauseThresholdLocked / memoryPauseThresholdLocked 返回对应
// advisory 阈值（调用方持锁或接受短暂竞态的只读访问）。供已准入的后台任务在批次边界做让路
// 判定（区别于准入拒绝：让路更温和，仍遵循 advisory 语义，unknown 不让路）。
func (c *BackgroundTaskCoordinator) iowaitPauseThresholdLocked() float64 { return c.iowaitPauseThreshold }
func (c *BackgroundTaskCoordinator) cpuPauseThresholdLocked() float64    { return c.cpuPauseThreshold }
func (c *BackgroundTaskCoordinator) memoryPauseThresholdLocked() float64 { return c.memoryPauseThreshold }

// IOWaitPauseThreshold 返回 iowait advisory 暂停阈值（线程安全只读）。
// 供包外后台任务（如 person_photos 回填）在批次边界做让路判定，避免直接调用 *Locked 命名方法。
func (c *BackgroundTaskCoordinator) IOWaitPauseThreshold() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.iowaitPauseThreshold
}

// CanRun 评估一次后台任务请求是否可以运行，不占用 slot。用于调用方在启动重工作前快速
// 检查。返回的 decision 不改变 coordinator 状态。
//
// 规则：
//   - Priority=user：永远 Allowed=true（P1 不被 foreground/cooldown 拒绝）。
//   - Priority=automatic：foreground active → foreground_active；class cooldown 未过期
//     → cooldown；否则 allowed。
//   - DedupeKey 非空的 automatic 若已有 running+pending，CanRun 报告 coalesced，但
//     真正的 coalesce 计数在 Begin 中完成（CanRun 不改变状态）。
func (c *BackgroundTaskCoordinator) CanRun(req BackgroundTaskRequest) (BackgroundTaskDecision, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.decideLocked(req, false)
}

// Begin 评估并尝试占用一个 running slot。成功时返回 (release, decision{Allowed:true}, true)，
// 调用方在 work 完成后必须调用 release 释放 slot 并唤醒可能 pending 的请求。
// 失败时返回 (nil, decision, false)，调用方应跳过本次 work，不得把持久化任务标记为完成。
//
// 对于 DedupeKey 非空的 automatic 请求，若当前已有 running：
//   - 若尚无 pending → 记录一个 pending slot 并返回 coalesced（调用方可选择稍后重试）；
//   - 若已有 pending → 返回 coalesced，不增加 pending（至多一 pending）。
//
// running 释放时不会自动触发 pending（pending 仅作为“有积压”的可观测标记；实际重试由
// worker 调度循环驱动，避免引入任务队列语义）。
func (c *BackgroundTaskCoordinator) Begin(req BackgroundTaskRequest) (func(), BackgroundTaskDecision, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	decision, ok := c.decideLocked(req, true)
	if !ok {
		return nil, decision, false
	}
	if req.Priority == BackgroundPriorityAutomatic && req.DedupeKey != "" {
		entry := BackgroundTaskDedupeEntry{Class: req.Class, DedupeKey: req.DedupeKey}
		slot := c.slots[entry]
		if slot == nil {
			slot = &dedupeSlot{}
			c.slots[entry] = slot
		}
		slot.running = true
		slot.priority = req.Priority
		slot.startedAt = time.Now()
	}
	return c.makeRelease(req), decision, true
}

// decideLocked 是准入决策的核心（调用方持锁）。allocate=true 表示允许占用 pending slot
// （仅 Begin 路径）；CanRun 用 allocate=false 避免改变 pending 状态。
func (c *BackgroundTaskCoordinator) decideLocked(req BackgroundTaskRequest, allocate bool) (BackgroundTaskDecision, bool) {
	// P1 user 永远允许——不被 foreground/cooldown/load 拒绝。
	if req.Priority == BackgroundPriorityUser {
		return BackgroundTaskDecision{Allowed: true, Reason: BackgroundDecisionAllowed}, true
	}

	// P2 automatic：auto_tasks_enabled 关闭 → 拒绝。
	if !c.autoTasksEnabled {
		return BackgroundTaskDecision{Allowed: false, Reason: BackgroundDecisionAutomaticDisabled}, false
	}
	// foreground active 优先拒绝。
	if c.foregroundActive > 0 {
		return BackgroundTaskDecision{Allowed: false, Reason: BackgroundDecisionForeground}, false
	}
	// class cooldown 未过期 → 拒绝。
	if until, ok := c.cooldowns[req.Class]; ok && time.Now().Before(until) {
		return BackgroundTaskDecision{Allowed: false, Reason: BackgroundDecisionCooldown, CooldownUntil: until}, false
	}
	// advisory 负载背压：只在 sampler 值 known 且超阈值时拒绝。unknown 不单独拒绝 P2。
	if c.loadFn != nil {
		snap := c.loadFn()
		if isKnown(snap.CPUUserPct) && c.cpuPauseThreshold > 0 && snap.CPUUserPct >= c.cpuPauseThreshold {
			return BackgroundTaskDecision{Allowed: false, Reason: BackgroundDecisionCPUHigh}, false
		}
		if isKnown(snap.CPUIOWaitPct) && c.iowaitPauseThreshold > 0 && snap.CPUIOWaitPct >= c.iowaitPauseThreshold {
			return BackgroundTaskDecision{Allowed: false, Reason: BackgroundDecisionIOWaitHigh}, false
		}
		if isKnown(snap.MemUsedPct) && c.memoryPauseThreshold > 0 && snap.MemUsedPct >= c.memoryPauseThreshold {
			return BackgroundTaskDecision{Allowed: false, Reason: BackgroundDecisionMemoryHigh}, false
		}
	}

	// DedupeKey 非空时做 coalescing 检查。
	if req.DedupeKey != "" {
		entry := BackgroundTaskDedupeEntry{Class: req.Class, DedupeKey: req.DedupeKey}
		slot := c.slots[entry]
		if slot != nil {
			if slot.running {
				// 已有 running：若无 pending，可记一个 pending（仅 Begin 路径）；否则 coalesce。
				if !slot.pending && allocate {
					slot.pending = true
				}
				return BackgroundTaskDecision{Allowed: false, Reason: BackgroundDecisionCoalesced}, false
			}
		}
	}
	return BackgroundTaskDecision{Allowed: true, Reason: BackgroundDecisionAllowed}, true
}

// makeRelease 返回一个释放 running slot 的闭包。对无 DedupeKey 的请求，release 是 no-op
// （无 slot 可释放）。release 用 sync.Once 保护，多次调用安全。
func (c *BackgroundTaskCoordinator) makeRelease(req BackgroundTaskRequest) func() {
	once := sync.Once{}
	return func() {
		once.Do(func() {
			c.mu.Lock()
			defer c.mu.Unlock()
			if req.Priority == BackgroundPriorityAutomatic && req.DedupeKey != "" {
				entry := BackgroundTaskDedupeEntry{Class: req.Class, DedupeKey: req.DedupeKey}
				if slot, ok := c.slots[entry]; ok {
					slot.running = false
					// pending 仅作可观测标记，running 释放时清掉 pending，避免无限累积。
					// 实际重试由 worker 调度循环驱动。
					slot.pending = false
					if !slot.running && !slot.pending {
						delete(c.slots, entry)
					}
				}
			}
		})
	}
}

// Cooldown 为指定 class 设置一段 cooldown，期间 automatic 请求被拒绝。reason 仅用于日志。
// 用于失败/DB busy 后退避。cooldown 会覆盖该 class 已有的未过期 cooldown（取较晚者）。
func (c *BackgroundTaskCoordinator) Cooldown(class BackgroundTaskClass, duration time.Duration, reason string) {
	if duration <= 0 {
		return
	}
	until := time.Now().Add(duration)
	c.mu.Lock()
	if existing, ok := c.cooldowns[class]; ok && existing.After(until) {
		// 已有更晚的 cooldown，保留它。
		c.mu.Unlock()
		return
	}
	c.cooldowns[class] = until
	c.mu.Unlock()
	logger.Infof("background coordinator: cooldown class=%s until=%s reason=%s", class, until.Format(time.RFC3339), reason)
}

// ClearCooldown 清除指定 class 的 cooldown（供测试与运维复位）。
func (c *BackgroundTaskCoordinator) ClearCooldown(class BackgroundTaskClass) {
	c.mu.Lock()
	delete(c.cooldowns, class)
	c.mu.Unlock()
}

// ReportDBBusy 让 P2 automatic 后台代码在遇到 SQLite busy/locked 时通知 coordinator。
// coordinator 对该 class 设置 dbLockedCooldown 的 cooldown（>0 时），使后续 automatic
// 请求在 cooldown 内被拒绝为 cooldown（保持 pending，不 spin）。err 仅用于日志判定。
//
// 调用方契约：仅 P2 automatic 路径调用；前台操作绝不因此 sleep——前台可上报 telemetry
// 但不能改变用户可见错误。err 非 busy/locked 时本方法 no-op（调用方应先用 isSQLiteBusyOrLocked
// 判定，或直接传入让本方法判定）。
func (c *BackgroundTaskCoordinator) ReportDBBusy(class BackgroundTaskClass, err error) {
	if err == nil {
		return
	}
	if !isSQLiteBusyOrLocked(err) {
		return
	}
	c.mu.Lock()
	dur := c.dbLockedCooldown
	c.mu.Unlock()
	if dur <= 0 {
		logger.Warnf("background coordinator: db busy/locked class=%s but cooldown disabled; err=%v", class, err)
		return
	}
	c.Cooldown(class, dur, "db_busy_or_locked")
}

// Snapshot 返回 coordinator 当前状态的只读快照（供状态 API）。
func (c *BackgroundTaskCoordinator) Snapshot() BackgroundTaskSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	snap := BackgroundTaskSnapshot{
		ForegroundActive: c.foregroundActive > 0,
		ForegroundCount:  c.foregroundActive,
		Cooldowns:        make(map[BackgroundTaskClass]time.Time, len(c.cooldowns)),
	}
	for class, until := range c.cooldowns {
		if now.Before(until) {
			snap.Cooldowns[class] = until
		}
	}
	for entry, slot := range c.slots {
		if slot.running {
			snap.Running = append(snap.Running, BackgroundTaskRuntime{
				Class: entry.Class, DedupeKey: entry.DedupeKey,
				Priority: slot.priority, StartedAt: slot.startedAt,
			})
		}
		if slot.pending {
			snap.PendingDedupe = append(snap.PendingDedupe, BackgroundTaskDedupeEntry{
				Class: entry.Class, DedupeKey: entry.DedupeKey,
			})
		}
	}
	return snap
}

// BackgroundTaskStatus 是 Snapshot 加上配置/阈值信息的完整状态视图（供状态 API）。
type BackgroundTaskStatus struct {
	BackgroundTaskSnapshot
	AutoTasksEnabled     bool
	CPUPauseThreshold    float64
	IOWaitPauseThreshold float64
	MemoryPauseThreshold float64
	DBLockedCooldownMs   int64
}

// Status 返回 Snapshot + 配置/阈值的完整状态视图（供状态 API，Task 16）。
func (c *BackgroundTaskCoordinator) Status() BackgroundTaskStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	status := BackgroundTaskStatus{
		BackgroundTaskSnapshot: BackgroundTaskSnapshot{
			ForegroundActive: c.foregroundActive > 0,
			ForegroundCount:  c.foregroundActive,
			Cooldowns:        make(map[BackgroundTaskClass]time.Time, len(c.cooldowns)),
		},
		AutoTasksEnabled:     c.autoTasksEnabled,
		CPUPauseThreshold:    c.cpuPauseThreshold,
		IOWaitPauseThreshold: c.iowaitPauseThreshold,
		MemoryPauseThreshold: c.memoryPauseThreshold,
		DBLockedCooldownMs:   c.dbLockedCooldown.Milliseconds(),
	}
	now := time.Now()
	for class, until := range c.cooldowns {
		if now.Before(until) {
			status.Cooldowns[class] = until
		}
	}
	for entry, slot := range c.slots {
		if slot.running {
			status.Running = append(status.Running, BackgroundTaskRuntime{
				Class: entry.Class, DedupeKey: entry.DedupeKey,
				Priority: slot.priority, StartedAt: slot.startedAt,
			})
		}
		if slot.pending {
			status.PendingDedupe = append(status.PendingDedupe, BackgroundTaskDedupeEntry{
				Class: entry.Class, DedupeKey: entry.DedupeKey,
			})
		}
	}
	return status
}
