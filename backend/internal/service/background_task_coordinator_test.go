package service

import (
	"testing"
	"time"

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
