package service

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCoordinatorTestService 构造一个 shadow 模式服务，注入可控时钟，并暴露协调器。
func newCoordinatorTestService(t *testing.T) (*personIdentityProfileService, *ipTestDeps) {
	t.Helper()
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	require.NotNil(t, svc.coordinator)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return t0 }
	svc.coordinator.setNowFn(func() time.Time { return t0 })
	return svc, deps
}

// advanceClock 推进服务与协调器时钟并执行一次 slice（绕过 cooldown）。
func advanceClockAndRun(svc *personIdentityProfileService, advance time.Duration) {
	next := svc.lastRunAt.Add(advance)
	svc.nowFn = func() time.Time { return next }
	if svc.coordinator != nil {
		svc.coordinator.setNowFn(func() time.Time { return next })
	}
	_ = svc.RunBackgroundSlice()
}

// TestCoordinator_PriorityHighBeforeLow 验证协调器优先构建高优先级 dirty（前台操作），
// 再补低优先级（backfill）。
func TestCoordinator_PriorityHighBeforeLow(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)

	// 低优先级人物（backfill 发现）：先创建并标记 backfill。
	lowP := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	highP := createPersonWithFaces(t, deps.repos, 200, vec3(0, 1, 0), vec3(0.01, 0.98, 0), vec3(0, 0.99, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{lowP.ID}, "backfill"))
	require.NoError(t, svc.MarkDirty([]uint{highP.ID}, "people_merged"))

	// dirtyBatch 默认 10，两个都会被选中；高优先级排在前面。
	dirty, err := svc.coordinator.selectDirtyBatch()
	require.NoError(t, err)
	require.Len(t, dirty, 2)
	assert.Equal(t, highP.ID, dirty[0].PersonID, "high priority dirty must come first")
	assert.Equal(t, lowP.ID, dirty[1].PersonID, "low priority dirty comes after")
}

// TestCoordinator_PriorityHighNotSqueezedOutByLow 验证当总 dirty > dirtyBatch 且
// 高优先级 person_id 较大时，单次按 person_id 排序的拉取不会漏掉高优先级。
// 这是 codex review 指出的设计缺陷回归测试：旧实现 ListDirty(0, limit) 仅按 person_id
// 拉取，会把高 person_id 的高优先级挤出批次。
func TestCoordinator_PriorityHighNotSqueezedOutByLow(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	// dirtyBatch 缩到 2，强制批次边界。
	svc.coordinator.dirtyBatch = 2

	// 3 个低优先级（backfill）person_id 较小，1 个高优先级 person_id 较大。
	for i := 0; i < 3; i++ {
		p := createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
		require.NoError(t, svc.MarkDirty([]uint{p.ID}, "backfill"))
	}
	highP := createPersonWithFaces(t, deps.repos, 500, vec3(0, 1, 0), vec3(0.01, 0.98, 0), vec3(0, 0.99, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{highP.ID}, "people_merged"))

	dirty, err := svc.coordinator.selectDirtyBatch()
	require.NoError(t, err)
	require.Len(t, dirty, 2, "batch bounded by dirtyBatch")
	// 高优先级必须在批次中且排在最前，即便其 person_id 最大。
	assert.Equal(t, highP.ID, dirty[0].PersonID, "high priority must not be squeezed out by lower person_ids")
}

// TestCoordinator_ConcurrentBuildSerialWrite 验证 worker 并发构建但写入串行提交，
// 且写入阶段不会出现并发 SQLite 写（通过 WriteQueue 串行化）。
func TestCoordinator_ConcurrentBuildSerialWrite(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	// 4 个 dirty 人物，workers=2。
	for i := 0; i < 4; i++ {
		p := createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
		require.NoError(t, svc.MarkDirty([]uint{p.ID}, "manual"))
	}

	require.NoError(t, svc.RunBackgroundSlice())

	// 4 个全部 ready。
	var ready int64
	require.NoError(t, deps.db.Model(&model.PersonIdentityProfile{}).Where("status = ?", model.PersonIdentityProfileStatusReady).Count(&ready).Error)
	assert.Equal(t, int64(4), ready)
	// ReplaceGeneration 调用 4 次（每个成功人物一次）。
	assert.Equal(t, 4, deps.countingRepo.replaceGeneration)

	// stats 反映本轮结果。
	resp, err := svc.GetOperationalStats(nil)
	require.NoError(t, err)
	assert.Equal(t, 4, resp.Coordinator.LastDirtySelected)
	assert.Equal(t, 4, resp.Coordinator.LastBuiltSuccess)
	assert.Equal(t, 0, resp.Coordinator.LastBuiltFailed)
	assert.Equal(t, 2, resp.Coordinator.Workers)
}

// TestCoordinator_ForegroundYieldSkipsBatch 验证前台让路时不启动新批次。
func TestCoordinator_ForegroundYieldSkipsBatch(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	for i := 0; i < 3; i++ {
		p := createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
		require.NoError(t, svc.MarkDirty([]uint{p.ID}, "manual"))
	}

	// 注入前台忙 → 让路。
	var fgBusy atomic.Bool
	fgBusy.Store(true)
	svc.SetForegroundBusyFn(func() bool { return fgBusy.Load() })

	advanceClockAndRun(svc, time.Second)

	// 让路：无 ready。
	var ready int64
	require.NoError(t, deps.db.Model(&model.PersonIdentityProfile{}).Where("status = ?", model.PersonIdentityProfileStatusReady).Count(&ready).Error)
	assert.Equal(t, int64(0), ready, "foreground yield must not start build batch")

	// 前台释放 → 下一个 slice 构建成功。
	fgBusy.Store(false)
	advanceClockAndRun(svc, time.Second)
	require.NoError(t, deps.db.Model(&model.PersonIdentityProfile{}).Where("status = ?", model.PersonIdentityProfileStatusReady).Count(&ready).Error)
	assert.Equal(t, int64(3), ready, "build proceeds after foreground clears")
}

// TestCoordinator_SingleBuildFailureDoesNotBlockBatch 验证单个 build 失败不阻断同批其他 person。
func TestCoordinator_SingleBuildFailureDoesNotBlockBatch(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	p1 := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	p2 := createPersonWithFaces(t, deps.repos, 200, vec3(0, 1, 0), vec3(0.01, 0.98, 0), vec3(0, 0.99, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{p1.ID}, "manual"))
	require.NoError(t, svc.MarkDirty([]uint{p2.ID}, "manual"))

	// 注入对 p1 失败的 builder。
	svc.coordinator.builder = &personSpecificFailingBuilder{failID: p1.ID}

	require.NoError(t, svc.RunBackgroundSlice())

	// p1 failed，p2 ready。
	var prof1, prof2 model.PersonIdentityProfile
	require.NoError(t, deps.db.Where("person_id = ?", p1.ID).First(&prof1).Error)
	require.NoError(t, deps.db.Where("person_id = ?", p2.ID).First(&prof2).Error)
	assert.Equal(t, model.PersonIdentityProfileStatusFailed, prof1.Status)
	assert.Equal(t, model.PersonIdentityProfileStatusReady, prof2.Status)

	resp, err := svc.GetOperationalStats(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.Coordinator.LastBuiltSuccess)
	assert.Equal(t, 1, resp.Coordinator.LastBuiltFailed)
}

// personSpecificFailingBuilder 对指定 personID 返回错误，其余正常构建。
type personSpecificFailingBuilder struct {
	failID uint
	inner  identityProfileBuilderIface
}

func (b *personSpecificFailingBuilder) Build(personID uint, faces []*model.Face) (*model.PersonIdentityProfileBuild, error) {
	if personID == b.failID {
		return nil, errBoom
	}
	if b.inner == nil {
		b.inner = NewIdentityProfileBuilder(identityProfileBuilderConfig{
			MaxCenters: 6, MinCenterFaces: 3, MinCenterPhotos: 2,
		})
	}
	return b.inner.Build(personID, faces)
}

// TestCoordinator_ReplaceGenerationFailurePreservesOldGen 验证写入失败保留旧 generation 并 MarkFailed。
func TestCoordinator_ReplaceGenerationFailurePreservesOldGen(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "first"))
	require.NoError(t, svc.RunBackgroundSlice())

	old, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	require.True(t, old.Profile.ActiveGeneration > 0)
	genBefore := old.Profile.ActiveGeneration

	// 注入 ReplaceGeneration 失败的仓库并再次标记 dirty。
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "second"))
	svc.bgRepo = &failingReplaceRepo{inner: svc.bgRepo}
	advanceClockAndRun(svc, time.Second)

	cur, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, genBefore, cur.Profile.ActiveGeneration, "old generation preserved on write failure")
	assert.Equal(t, model.PersonIdentityProfileStatusFailed, cur.Profile.Status)
}

// TestCoordinator_AnnActivateWhenDeltaSufficient 验证 delta 充足时批量 activate，
// 新人物无需 full rebuild 即可召回。
func TestCoordinator_AnnActivateWhenDeltaSufficient(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	p1 := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{p1.ID}, "first"))
	require.NoError(t, svc.RunBackgroundSlice()) // 首次 full rebuild

	// 第二个人物：delta 充足 → activate 而非 full rebuild。
	p2 := createPersonWithFaces(t, deps.repos, 200, vec3(0, 1, 0), vec3(0.01, 0.98, 0), vec3(0, 0.99, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{p2.ID}, "second"))
	advanceClockAndRun(svc, time.Second)

	got, ready := svc.ann.Search(vec3(0, 1, 0), 5, "emb")
	require.True(t, ready)
	assert.Contains(t, got, p2.ID, "newly activated person recallable via delta")

	resp, err := svc.GetOperationalStats(nil)
	require.NoError(t, err)
	assert.False(t, resp.Coordinator.LastAnnRebuild, "delta sufficient: activate not full rebuild")
	assert.Equal(t, 1, resp.Coordinator.LastAnnActivated)
}

// TestCoordinator_AnnFullRebuildWhenDeltaNearLimit 验证 delta 接近上限或 rebuild_requested 时合并触发 full rebuild。
func TestCoordinator_AnnFullRebuildWhenDeltaNearLimit(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	// Task 11：禁用 ANN 成功 cooldown，本测试验证 delta full 立即触发 rebuild 语义，
	// 不应被成功 cooldown 跳过。
	svc.coordinator.setAnnRebuildMinIntervalForTest(0)
	svc.coordinator.annRebuildCooldownUntil = time.Time{}
	p1 := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{p1.ID}, "first"))
	require.NoError(t, svc.RunBackgroundSlice()) // 首次 full rebuild

	// 模拟 delta full：直接 RequestRebuild。
	svc.ann.RequestRebuild()
	// 禁用 cooldown：确保本次 delta full 立即触发 rebuild。
	svc.coordinator.annRebuildCooldownUntil = time.Time{}

	p2 := createPersonWithFaces(t, deps.repos, 200, vec3(0, 1, 0), vec3(0.01, 0.98, 0), vec3(0, 0.99, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{p2.ID}, "second"))
	advanceClockAndRun(svc, time.Second)

	// rebuild_requested → full rebuild，两个人物都可召回。
	got1, ready := svc.ann.Search(vec3(1, 0, 0), 5, "emb")
	require.True(t, ready)
	assert.Contains(t, got1, p1.ID)
	got2, ready := svc.ann.Search(vec3(0, 1, 0), 5, "emb")
	require.True(t, ready)
	assert.Contains(t, got2, p2.ID)

	resp, err := svc.GetOperationalStats(nil)
	require.NoError(t, err)
	assert.True(t, resp.Coordinator.LastAnnRebuild, "rebuild_requested triggers full rebuild")
	assert.NotEmpty(t, resp.Coordinator.LastAnnRebuildReason)
}

// TestCoordinator_AnnRebuildFailureFailClosed 验证 full rebuild 失败后 stats 暴露 failed，
// 且不回滚已激活的数据库 generation（fail-closed）。
func TestCoordinator_AnnRebuildFailureFailClosed(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	// Task 11：禁用 ANN 成功 cooldown，本测试验证失败 fail-closed 语义，不应被 cooldown 跳过。
	svc.coordinator.setAnnRebuildMinIntervalForTest(0)
	svc.coordinator.annRebuildCooldownUntil = time.Time{}
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())

	active, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	genBefore := active.Profile.ActiveGeneration

	// 注入 ListAllActiveCenters 失败 + InvalidateAll + RequestRebuild。
	svc.bgRepo = &failingListCentersRepo{inner: svc.bgRepo}
	svc.ann.InvalidateAll()
	svc.ann.RequestRebuild()
	// 清零 cooldown：失败 rebuild 应立即触发（不被成功 cooldown gate 跳过）。
	svc.coordinator.annRebuildCooldownUntil = time.Time{}
	advanceClockAndRun(svc, time.Second)

	// generation 不变（fail-closed）。
	cur, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, genBefore, cur.Profile.ActiveGeneration, "rebuild failure must not roll back generation")

	resp, err := svc.GetOperationalStats(nil)
	require.NoError(t, err)
	assert.True(t, resp.ANN.RebuildRequested, "failed rebuild keeps rebuild requested")
	assert.True(t, resp.Coordinator.LastAnnRebuild)
}

// TestCoordinator_LegacyNoCoordinator 验证 legacy 模式不构造协调器、不访问 profile repository。
func TestCoordinator_LegacyNoCoordinator(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "legacy", "emb", 25, 500)
	assert.Nil(t, svc.coordinator, "legacy must not construct coordinator")

	require.NoError(t, svc.MarkDirty([]uint{1}, "x"))
	require.NoError(t, svc.RunBackgroundSlice())

	c := deps.countingRepo
	assert.Equal(t, 0, c.listDirty, "legacy coordinator path must not list dirty")
	assert.Equal(t, 0, c.replaceGeneration, "legacy must not build profiles")
}

// TestCoordinator_ConcurrentBuildersNoSharedMutation 验证并发 worker 不共享可变状态：
// 多 worker 并发调用纯 builder 不 panic，且各自产出有效构建（members 数量一致）。
// 读脸阶段串行以避免 SQLite 并发读锁；纯构建阶段并发，证明 builder 无共享可变状态。
func TestCoordinator_ConcurrentBuildersNoSharedMutation(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	type faceBatch struct {
		pid   uint
		faces []*model.Face
	}
	batches := make([]faceBatch, 4)
	for i := 0; i < 4; i++ {
		p := createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
		faces, err := svc.bgFaceRepo.ListProfileFaces(p.ID)
		require.NoError(t, err)
		batches[i] = faceBatch{pid: p.ID, faces: faces}
	}

	var wg sync.WaitGroup
	type buildOut struct {
		centers  int
		members  int
		personID uint
	}
	results := make([]buildOut, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(idx int, b faceBatch) {
			defer wg.Done()
			build, err := svc.builder.Build(b.pid, b.faces)
			if err != nil || build == nil {
				return
			}
			results[idx] = buildOut{
				centers:  len(build.Centers),
				members:  len(build.Members),
				personID: b.pid,
			}
		}(i, batches[i])
	}
	wg.Wait()
	for _, r := range results {
		assert.Equal(t, 3, r.members, "concurrent build must process all 3 faces")
	}
}



// ---- Task 11: identity ANN rebuild 频率限制 ----

// TestIdentityProfileCoordinator_AnnRebuildUsesCooldown 验证成功 rebuild 后进入 cooldown：
// cooldown 内即使 rebuildRequested 再次置位，也不触发新的 full rebuild；RebuildRequested
// 保持 true（pending），cooldown 过期后才执行。
func TestIdentityProfileCoordinator_AnnRebuildUsesCooldown(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	coord := svc.coordinator
	// 用短间隔便于测试。
	coord.setAnnRebuildMinIntervalForTest(50 * time.Millisecond)

	person := createPersonWithFaces(t, deps.repos, 200, vec3(1, 0, 0))
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))

	// 第一个 slice：构建人物 + 批末 ANN full rebuild（首次）。
	require.NoError(t, svc.RunBackgroundSlice())
	require.False(t, svc.ann.RebuildRequested(), "first rebuild should clear rebuildRequested")

	// 立即再请求 rebuild：cooldown 内不应触发 full rebuild。
	svc.ann.RequestRebuild()
	require.True(t, svc.ann.RebuildRequested())
	rebuildCountBefore := coordStatsLastAnnRebuildCount(coord)
	require.NoError(t, svc.RunBackgroundSlice())
	// cooldown 内：rebuildRequested 仍 true（pending），未执行 rebuild。
	assert.True(t, svc.ann.RebuildRequested(), "cooldown must keep rebuildRequested pending")
	assert.Equal(t, rebuildCountBefore, coordStatsLastAnnRebuildCount(coord), "must not rebuild during cooldown")

	// 推进时钟超过 service cooldown（默认 500ms）与 ANN cooldown（50ms），再 slice：应执行 rebuild。
	advanceClockAndRun(svc, 600*time.Millisecond)
	assert.False(t, svc.ann.RebuildRequested(), "rebuild must run after cooldown expires")
}

// TestIdentityProfileCoordinator_DeltaFullCoalescesRebuildRequest 验证 cooldown 内多个
// delta_full 不触发多次 full rebuild：连续 RequestRebuild + slice 只在第一次允许时重建。
func TestIdentityProfileCoordinator_DeltaFullCoalescesRebuildRequest(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	coord := svc.coordinator
	coord.setAnnRebuildMinIntervalForTest(50 * time.Millisecond)

	person := createPersonWithFaces(t, deps.repos, 300, vec3(1, 0, 0))
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice()) // 首次 rebuild
	require.False(t, svc.ann.RebuildRequested())

	// 模拟多次 delta_full：连续请求 rebuild + slice。
	svc.ann.RequestRebuild()
	require.NoError(t, svc.RunBackgroundSlice())
	countAfter1 := coordStatsLastAnnRebuildCount(coord)
	svc.ann.RequestRebuild()
	require.NoError(t, svc.RunBackgroundSlice())
	countAfter2 := coordStatsLastAnnRebuildCount(coord)
	svc.ann.RequestRebuild()
	require.NoError(t, svc.RunBackgroundSlice())
	countAfter3 := coordStatsLastAnnRebuildCount(coord)

	// cooldown 内：三次请求只可能在第一次（如果恰好过期）重建，后两次被 coalesce。
	// 关键断言：countAfter2 == countAfter1 且 countAfter3 == countAfter1（cooldown 内不重复重建）。
	assert.Equal(t, countAfter1, countAfter2, "second delta_full must be coalesced during cooldown")
	assert.Equal(t, countAfter1, countAfter3, "third delta_full must be coalesced during cooldown")
	// pending 保持 true。
	assert.True(t, svc.ann.RebuildRequested(), "coalesced rebuilds must keep pending")
}

// TestIdentityProfileCoordinator_AnnRebuildCoordinatorRejectedKeepsPending 验证
// BackgroundTaskCoordinator 拒绝时（foreground active）ANN rebuild 保持 pending，不执行。
func TestIdentityProfileCoordinator_AnnRebuildCoordinatorRejectedKeepsPending(t *testing.T) {
	svc, deps := newCoordinatorTestService(t)
	coord := svc.coordinator
	bgCoord := NewBackgroundTaskCoordinator()
	coord.setBackgroundCoordinator(bgCoord)
	coord.setAnnRebuildMinIntervalForTest(50 * time.Millisecond)

	person := createPersonWithFaces(t, deps.repos, 400, vec3(1, 0, 0))
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	// 首次构建 + rebuild（无 foreground，允许）。
	require.NoError(t, svc.RunBackgroundSlice())
	require.False(t, svc.ann.RebuildRequested())

	// foreground active：ANN rebuild 应被 coordinator 拒绝，保持 pending。
	svc.ann.RequestRebuild()
	release := bgCoord.BeginForeground()
	countBefore := coordStatsLastAnnRebuildCount(coord)
	require.NoError(t, svc.RunBackgroundSlice())
	assert.True(t, svc.ann.RebuildRequested(), "rejected rebuild must keep pending")
	assert.Equal(t, countBefore, coordStatsLastAnnRebuildCount(coord), "must not rebuild while foreground active")

	// 释放 foreground：下次 slice 允许 rebuild（推进超过 service cooldown + ANN cooldown）。
	release()
	advanceClockAndRun(svc, 600*time.Millisecond)
	assert.False(t, svc.ann.RebuildRequested(), "rebuild must run after foreground released and cooldown expired")
}

// coordStatsLastAnnRebuildCount 返回 maybeRebuildANN 实际触发 rebuild 的累计次数
//（annRebuildTotalCount），供测试断言 cooldown/coalescing 是否真正减少了 rebuild 次数。
func coordStatsLastAnnRebuildCount(coord *identityProfileCoordinator) int {
	return coord.annRebuildTotalCount
}
