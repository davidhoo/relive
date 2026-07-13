package service

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIdentityANN_NewRequestDuringRebuildRemainsPending 验证重建期间出现新的 RequestRebuild
// 时，旧 rebuild 完成后必须保持 pending（rebuildRequested 仍为 true），新 request 不会被清除。
func TestIdentityANN_NewRequestDuringRebuildRemainsPending(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	require.False(t, ann.RebuildRequested(), "precondition: no pending rebuild after clean build")

	// 在重建窗口内推进 request generation（模拟并发 RequestRebuild）。
	ann.buildHook = func() {
		ann.RequestRebuild()
	}

	// 重建 snapshot 仍只含 person 10，构建应成功。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	ann.buildHook = nil

	// 构建成功发布（ready），但 rebuildRequested 必须保持 true：构建期间有新 request。
	assert.True(t, ann.Ready("emb-v1"), "successful rebuild must publish snapshot")
	assert.True(t, ann.RebuildRequested(), "new request during rebuild must keep pending")

	// 下一次无并发 request 的重建应清除 pending。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	assert.False(t, ann.RebuildRequested(), "clean rebuild with no concurrent request must clear pending")
}

// TestIdentityANN_ActivateDeltaFullIsAtomic 验证 Activate 容量不足时不会部分修改 delta、
// invalid 或 active generation：旧中心保留、active generation 不变、delta 规模不变。
func TestIdentityANN_ActivateDeltaFullIsAtomic(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	ann.deltaMax = 3
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))

	// 填充 delta 到上限（deltaMax=3）：person 20/30/40 各一个中心。
	require.NoError(t, ann.Activate(20, 1, []*model.PersonIdentityCenter{annCenter(2, 20, 1, 1, emb3(0, 1, 0))}))
	require.NoError(t, ann.Activate(30, 1, []*model.PersonIdentityCenter{annCenter(3, 30, 1, 1, emb3(0, 0, 1))}))
	require.NoError(t, ann.Activate(40, 1, []*model.PersonIdentityCenter{annCenter(4, 40, 1, 1, emb3(1, 1, 0))}))

	statsBefore := ann.Stats("emb-v1")
	require.Equal(t, 3, statsBefore.DeltaNodes)

	// 同一人物 person 40 已有 1 个旧中心；激活 2 个新中心会先移除 1 个旧的再加 2 个新的
	// → net +1 = 4 > deltaMax(3)，应触发容量失败。
	err := ann.Activate(40, 2, []*model.PersonIdentityCenter{
		annCenter(5, 40, 2, 1, emb3(1, 0, 1)),
		annCenter(6, 40, 2, 2, emb3(0, 1, 1)),
	})
	require.Error(t, err)

	// 失败不得部分修改状态：delta 规模、active generation、旧中心全部保持触发前。
	statsAfter := ann.Stats("emb-v1")
	assert.Equal(t, statsBefore.DeltaNodes, statsAfter.DeltaNodes, "delta size must be unchanged on capacity failure")

	ann.deltaMu.RLock()
	// person 40 旧中心（generation 1）必须仍在 delta 中。
	stillHasOld := false
	for _, v := range ann.delta {
		if v.PersonID == 40 && v.Generation == 1 {
			stillHasOld = true
		}
	}
	// 新中心（generation 2）不得被写入。
	hasNew := false
	for _, v := range ann.delta {
		if v.PersonID == 40 && v.Generation == 2 {
			hasNew = true
		}
	}
	// active generation 保持为旧 generation（未被推进）。
	activeGen, hasActive := ann.activeGeneration[40]
	ann.deltaMu.RUnlock()

	assert.True(t, stillHasOld, "old delta center must be preserved on capacity failure")
	assert.False(t, hasNew, "new delta centers must not be written on capacity failure")
	assert.True(t, hasActive, "person 40 must still have an active generation")
	assert.Equal(t, 1, activeGen, "active generation must not advance on capacity failure")
}

// TestIdentityANN_ActivateInvalidInputDoesNotMutate 验证 Activate 输入校验失败（非法中心）
// 时不修改任何 delta 状态，包括在容量已满情况下：校验发生在锁外，但即便进入锁内也不得部分写入。
func TestIdentityANN_ActivateInvalidInputDoesNotMutate(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	ann.deltaMax = 2
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	require.NoError(t, ann.Activate(20, 1, []*model.PersonIdentityCenter{annCenter(2, 20, 1, 1, emb3(0, 1, 0))}))

	statsBefore := ann.Stats("emb-v1")

	// 非法中心：零范数 embedding（validVector 失败），校验在锁外。
	err := ann.Activate(30, 1, []*model.PersonIdentityCenter{
		annCenter(3, 30, 1, 1, emb3(0, 0, 0)),
	})
	require.Error(t, err)

	statsAfter := ann.Stats("emb-v1")
	assert.Equal(t, statsBefore.DeltaNodes, statsAfter.DeltaNodes, "invalid input must not mutate delta")
	assert.False(t, statsAfter.Unavailable, "invalid input must not mark unavailable")
}

// TestIdentityANN_RebuildPreservesConcurrentDelta 验证 revision 变化但 delta 完整时，
// 重建保留 snapshot + delta 语义：构建期间 Activate 的中心不丢失，旧 snapshot 被新 snapshot
// 替换但 delta 保留。
func TestIdentityANN_RebuildPreservesConcurrentDelta(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	// 初始 snapshot：person 10 + person 20。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)),
	}, "emb-v1"))

	// 在重建窗口内激活 person 30（snapshot 不含它），推进 revision。
	ann.buildHook = func() {
		if err := ann.Activate(30, 1, []*model.PersonIdentityCenter{
			annCenter(9000, 30, 1, 1, emb3(0, 0, 1)),
		}); err != nil {
			t.Logf("buildHook activate: %v", err)
		}
	}
	// 重建的新 snapshot 只含 person 10（不含 person 20、person 30）。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
	}, "emb-v1"))
	ann.buildHook = nil

	// person 30 必须仍可召回：delta 在 revision 变化时被保留（preserve 语义）。
	got30, ready := ann.Search(emb3(0, 0, 1), 5, "emb-v1")
	require.True(t, ready)
	assert.Contains(t, got30, uint(30), "delta activated during rebuild must survive (snapshot + delta preserved)")

	// person 20 不在新 snapshot 中，也不在 delta 中 → 不可召回。
	got20, ready := ann.Search(emb3(0, 1, 0), 5, "emb-v1")
	require.True(t, ready)
	assert.NotContains(t, got20, uint(20), "person removed from new snapshot and not in delta must not be recalled")

	// person 10 仍在新 snapshot 中，可召回。
	got10, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	require.True(t, ready)
	assert.Contains(t, got10, uint(10))
}
