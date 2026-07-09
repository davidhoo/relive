package service

import (
	"fmt"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackgroundTaskCoordinator_AllowsP2WhenIdle 验证空闲时 automatic P2 请求被允许。
func TestBackgroundTaskCoordinator_AllowsP2WhenIdle(t *testing.T) {
	c := NewBackgroundTaskCoordinator()

	decision, ok := c.CanRun(BackgroundTaskRequest{
		Class:    BackgroundTaskPeopleClustering,
		Priority: BackgroundPriorityAutomatic,
	})
	assert.True(t, ok)
	assert.True(t, decision.Allowed)
	assert.Equal(t, BackgroundDecisionAllowed, decision.Reason)

	// Begin 占用并释放。
	release, decision, ok := c.Begin(BackgroundTaskRequest{
		Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic, DedupeKey: "batch-1",
	})
	require.True(t, ok)
	require.True(t, decision.Allowed)
	require.NotNil(t, release)
	release()

	// 释放后再次允许。
	_, ok = c.CanRun(BackgroundTaskRequest{
		Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic, DedupeKey: "batch-1",
	})
	assert.True(t, ok)
}

// TestBackgroundTaskCoordinator_BlocksP2WhenForegroundActive 验证 foreground active 时
// automatic P2 被拒绝为 foreground_active，而 P1 user 仍允许。
func TestBackgroundTaskCoordinator_BlocksP2WhenForegroundActive(t *testing.T) {
	c := NewBackgroundTaskCoordinator()

	release := c.BeginForeground()
	defer release()
	require.True(t, c.ForegroundActive())

	// P2 automatic 被拒绝。
	decision, ok := c.CanRun(BackgroundTaskRequest{
		Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic,
	})
	assert.False(t, ok)
	assert.False(t, decision.Allowed)
	assert.Equal(t, BackgroundDecisionForeground, decision.Reason)

	// P1 user 不被 foreground 拒绝。
	_, ok = c.CanRun(BackgroundTaskRequest{
		Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityUser,
	})
	assert.True(t, ok)

	// Begin 对 P2 同样拒绝，不占用 slot。
	release2, _, ok := c.Begin(BackgroundTaskRequest{
		Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic, DedupeKey: "x",
	})
	assert.False(t, ok)
	assert.Nil(t, release2)

	// 释放 foreground 后 P2 恢复允许。
	release()
	require.False(t, c.ForegroundActive())
	_, ok = c.CanRun(BackgroundTaskRequest{
		Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic,
	})
	assert.True(t, ok)
}

// TestBackgroundTaskCoordinator_BlocksP2DuringCooldown 验证 class cooldown 未过期时
// automatic 被拒绝为 cooldown，过期后恢复；P1 user 不受 cooldown 影响。
func TestBackgroundTaskCoordinator_BlocksP2DuringCooldown(t *testing.T) {
	c := NewBackgroundTaskCoordinator()

	c.Cooldown(BackgroundTaskProtoCacheRefresh, 50*time.Millisecond, "db_busy")

	decision, ok := c.CanRun(BackgroundTaskRequest{
		Class: BackgroundTaskProtoCacheRefresh, Priority: BackgroundPriorityAutomatic,
	})
	assert.False(t, ok)
	assert.False(t, decision.Allowed)
	assert.Equal(t, BackgroundDecisionCooldown, decision.Reason)
	assert.False(t, decision.CooldownUntil.IsZero())

	// P1 user 不受 cooldown 影响。
	_, ok = c.CanRun(BackgroundTaskRequest{
		Class: BackgroundTaskProtoCacheRefresh, Priority: BackgroundPriorityUser,
	})
	assert.True(t, ok)

	// 另一 class 不受本 class cooldown 影响。
	_, ok = c.CanRun(BackgroundTaskRequest{
		Class: BackgroundTaskMergeSuggestion, Priority: BackgroundPriorityAutomatic,
	})
	assert.True(t, ok)

	// 等待 cooldown 过期。
	time.Sleep(60 * time.Millisecond)
	_, ok = c.CanRun(BackgroundTaskRequest{
		Class: BackgroundTaskProtoCacheRefresh, Priority: BackgroundPriorityAutomatic,
	})
	assert.True(t, ok)
}

// TestBackgroundTaskCoordinator_CoalescesDedupeKeys 验证相同 (class, dedupeKey) 的
// automatic 请求被 coalesce：第一个 Begin 占用 running，第二个被拒绝为 coalesced；
// 释放后恢复。
func TestBackgroundTaskCoordinator_CoalescesDedupeKeys(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	req := BackgroundTaskRequest{
		Class: BackgroundTaskIdentityANNRebuild, Priority: BackgroundPriorityAutomatic, DedupeKey: "rebuild-1",
	}

	// 第一次 Begin：占用 running。
	release, decision, ok := c.Begin(req)
	require.True(t, ok)
	require.True(t, decision.Allowed)
	require.NotNil(t, release)

	// 第二次相同请求：被 coalesce。
	release2, decision2, ok2 := c.Begin(req)
	assert.False(t, ok2)
	assert.False(t, decision2.Allowed)
	assert.Equal(t, BackgroundDecisionCoalesced, decision2.Reason)
	assert.Nil(t, release2)

	// 第三次：仍 coalesce（至多一 pending，第二、三次都进 coalesce）。
	_, decision3, ok3 := c.Begin(req)
	assert.False(t, ok3)
	assert.Equal(t, BackgroundDecisionCoalesced, decision3.Reason)

	// 释放 running 后再次允许。
	release()
	_, ok = c.CanRun(req)
	assert.True(t, ok, "after release, dedupe slot should be free")

	// Snapshot 反映 running 状态。
	release, _, _ = c.Begin(req)
	snap := c.Snapshot()
	assert.True(t, snap.ForegroundActive == false)
	require.Len(t, snap.Running, 1)
	assert.Equal(t, BackgroundTaskIdentityANNRebuild, snap.Running[0].Class)
	assert.Equal(t, "rebuild-1", snap.Running[0].DedupeKey)
	release()

	snap2 := c.Snapshot()
	assert.Empty(t, snap2.Running, "release should clear running slot")
}

// TestBackgroundTaskCoordinator_DedupeKeyNilDoesNotCoalesce 验证空 DedupeKey 的 automatic
// 请求不参与 coalescing（无 slot），多个并发请求都被允许。用于不天然去重的 P2 work。
func TestBackgroundTaskCoordinator_DedupeKeyNilDoesNotCoalesce(t *testing.T) {
	c := NewBackgroundTaskCoordinator()

	r1, _, ok := c.Begin(BackgroundTaskRequest{Class: BackgroundTaskMergeSuggestion, Priority: BackgroundPriorityAutomatic})
	require.True(t, ok)
	r2, _, ok := c.Begin(BackgroundTaskRequest{Class: BackgroundTaskMergeSuggestion, Priority: BackgroundPriorityAutomatic})
	require.True(t, ok, "nil dedupe key must not coalesce")
	r1()
	r2()

	// 无 running 残留。
	snap := c.Snapshot()
	assert.Empty(t, snap.Running)
}

// TestBackgroundTaskCoordinator_ForegroundReleaseIdempotent 验证 release 多次调用安全。
func TestBackgroundTaskCoordinator_ForegroundReleaseIdempotent(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	release := c.BeginForeground()
	assert.True(t, c.ForegroundActive())
	release()
	release() // 重复调用不应使计数变负
	assert.False(t, c.ForegroundActive())
}

// ---- Task 14: advisory 资源背压 ----

// fixedLoadFn 返回固定负载快照的 loadFn。
func fixedLoadFn(cpu, iowait, mem float64) func() BackgroundLoadSnapshot {
	return func() BackgroundLoadSnapshot {
		return BackgroundLoadSnapshot{
			CPUUserPct: cpu, CPUIOWaitPct: iowait, MemUsedPct: mem,
		}
	}
}

// TestBackgroundTaskCoordinator_CPUHighRejectsP2 验证 CPU 超阈值拒绝 P2 automatic。
func TestBackgroundTaskCoordinator_CPUHighRejectsP2(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(true, 70, 15, 85, fixedLoadFn(80, 5, 50), 0)

	decision, ok := c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic})
	assert.False(t, ok)
	assert.False(t, decision.Allowed)
	assert.Equal(t, BackgroundDecisionCPUHigh, decision.Reason)

	// P1 user 不受 CPU 背压影响。
	_, ok = c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityUser})
	assert.True(t, ok)
}

// TestBackgroundTaskCoordinator_IOWaitHighRejectsP2 验证 iowait 超阈值拒绝 P2。
func TestBackgroundTaskCoordinator_IOWaitHighRejectsP2(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(true, 70, 15, 85, fixedLoadFn(20, 20, 50), 0)

	decision, ok := c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskProtoCacheRefresh, Priority: BackgroundPriorityAutomatic})
	assert.False(t, ok)
	assert.Equal(t, BackgroundDecisionIOWaitHigh, decision.Reason)
}

// TestBackgroundTaskCoordinator_MemoryHighRejectsP2 验证内存超阈值拒绝 P2。
func TestBackgroundTaskCoordinator_MemoryHighRejectsP2(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(true, 70, 15, 85, fixedLoadFn(20, 5, 90), 0)

	decision, ok := c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskMergeSuggestion, Priority: BackgroundPriorityAutomatic})
	assert.False(t, ok)
	assert.Equal(t, BackgroundDecisionMemoryHigh, decision.Reason)
}

// TestBackgroundTaskCoordinator_UnknownLoadDoesNotRejectP2 验证 unknown 采样值不单独拒绝 P2。
func TestBackgroundTaskCoordinator_UnknownLoadDoesNotRejectP2(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(true, 70, 15, 85, func() BackgroundLoadSnapshot {
		return BackgroundLoadSnapshot{CPUUserPct: unknownLoad, CPUIOWaitPct: unknownLoad, MemUsedPct: unknownLoad}
	}, 0)

	_, ok := c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic})
	assert.True(t, ok, "unknown load must not reject P2")
}

// TestBackgroundTaskCoordinator_AutomaticDisabledRejectsP2 验证 auto_tasks_enabled=false
// 拒绝所有 P2 automatic。
func TestBackgroundTaskCoordinator_AutomaticDisabledRejectsP2(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(false, 70, 15, 85, nil, 0)

	decision, ok := c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic})
	assert.False(t, ok)
	assert.Equal(t, BackgroundDecisionAutomaticDisabled, decision.Reason)

	// P1 user 仍允许。
	_, ok = c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityUser})
	assert.True(t, ok)
}

// TestBackgroundTaskCoordinator_ForegroundNotRejectedByLoad 验证 P0 foreground scope 永远
// 不被负载背压拒绝（BeginForeground 不经过 decideLocked）。
func TestBackgroundTaskCoordinator_ForegroundNotRejectedByLoad(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(true, 70, 15, 85, fixedLoadFn(99, 99, 99), 0)

	// foreground scope 注册不报错，即使负载极高。
	release := c.BeginForeground()
	assert.True(t, c.ForegroundActive())
	release()
	assert.False(t, c.ForegroundActive())
}

// TestBackgroundTaskCoordinator_LoadBelowThresholdAllowsP2 验证负载低于阈值允许 P2。
func TestBackgroundTaskCoordinator_LoadBelowThresholdAllowsP2(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(true, 70, 15, 85, fixedLoadFn(50, 5, 60), 0)

	_, ok := c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic})
	assert.True(t, ok, "load below threshold must allow P2")
}

// ---- Task 15: SQLite busy/locked 反馈 coordinator ----

// TestBackgroundTaskCoordinator_DatabaseLockedStartsCooldown 验证 ReportDBBusy 后该 class
// 进入 cooldown，P2 automatic 被拒绝为 cooldown；P1 user 不受影响。
func TestBackgroundTaskCoordinator_DatabaseLockedStartsCooldown(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(true, 70, 15, 85, nil, 50*time.Millisecond)

	// 报告一个 SQLite busy 错误。
	c.ReportDBBusy(BackgroundTaskMergeSuggestion, fmt.Errorf("background slice: %w", sqlite3.Error{Code: sqlite3.ErrBusy}))

	// P2 automatic 被拒绝为 cooldown。
	decision, ok := c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskMergeSuggestion, Priority: BackgroundPriorityAutomatic})
	assert.False(t, ok)
	assert.Equal(t, BackgroundDecisionCooldown, decision.Reason)

	// P1 user 不受影响。
	_, ok = c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskMergeSuggestion, Priority: BackgroundPriorityUser})
	assert.True(t, ok)

	// 另一 class 不受影响。
	_, ok = c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskPeopleClustering, Priority: BackgroundPriorityAutomatic})
	assert.True(t, ok)

	// cooldown 过期后恢复。
	time.Sleep(60 * time.Millisecond)
	_, ok = c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskMergeSuggestion, Priority: BackgroundPriorityAutomatic})
	assert.True(t, ok)
}

// TestBackgroundTaskCoordinator_ReportDBBusyIgnoresNonBusyError 验证非 busy/locked 错误
// 不触发 cooldown。
func TestBackgroundTaskCoordinator_ReportDBBusyIgnoresNonBusyError(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(true, 70, 15, 85, nil, 50*time.Millisecond)

	c.ReportDBBusy(BackgroundTaskMergeSuggestion, fmt.Errorf("no such table: faces"))

	_, ok := c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskMergeSuggestion, Priority: BackgroundPriorityAutomatic})
	assert.True(t, ok, "non-busy error must not trigger cooldown")
}

// TestBackgroundTaskCoordinator_ReportDBBusyNoOpWhenCooldownDisabled 验证 dbLockedCooldown=0
// 时不 cooldown（仅记录日志）。
func TestBackgroundTaskCoordinator_ReportDBBusyNoOpWhenCooldownDisabled(t *testing.T) {
	c := NewBackgroundTaskCoordinator()
	c.SetBackgroundConfig(true, 70, 15, 85, nil, 0) // cooldown disabled

	c.ReportDBBusy(BackgroundTaskMergeSuggestion, sqlite3.Error{Code: sqlite3.ErrLocked})

	_, ok := c.CanRun(BackgroundTaskRequest{Class: BackgroundTaskMergeSuggestion, Priority: BackgroundPriorityAutomatic})
	assert.True(t, ok, "cooldown disabled must not reject P2")
}
