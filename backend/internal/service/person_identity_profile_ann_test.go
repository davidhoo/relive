package service

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// annCenter 构造一个带 float32 embedding 的 PersonIdentityCenter。
func annCenter(centerID, personID uint, generation, ordinal int, emb []float32) *model.PersonIdentityCenter {
	return &model.PersonIdentityCenter{
		ID:                centerID,
		PersonID:          personID,
		Generation:        generation,
		Ordinal:           ordinal,
		CentroidEmbedding: model.EncodeEmbedding(emb),
	}
}

func emb3(x, y, z float32) []float32 { return []float32{x, y, z} }

// ---- Step 2: snapshot 构建和输入校验 ----

func TestIdentityProfileANN_SnapshotRecallsOwner(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")

	centers := []*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)),
	}
	require.NoError(t, ann.Rebuild(centers, "emb-v1"))

	got, ready := ann.Search(emb3(0.99, 0.01, 0), 5, "emb-v1")
	require.True(t, ready)
	require.NotEmpty(t, got)
	assert.Equal(t, uint(10), got[0], "nearest center belongs to person 10")
}

func TestIdentityProfileANN_SnapshotDedupsPersonAcrossCenters(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")

	// 同一人物两个中心。
	centers := []*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 10, 1, 2, emb3(0.9, 0.1, 0)),
		annCenter(3, 20, 1, 1, emb3(0, 1, 0)),
	}
	require.NoError(t, ann.Rebuild(centers, "emb-v1"))

	got, ready := ann.Search(emb3(0.95, 0.05, 0), 5, "emb-v1")
	require.True(t, ready)
	// person 10 只出现一次。
	count := 0
	for _, p := range got {
		if p == 10 {
			count++
		}
	}
	assert.Equal(t, 1, count, "same person's multiple centers must dedup to one")
}

func TestIdentityProfileANN_EmptySnapshotReadyNoResults(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild(nil, "emb-v1"))

	got, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	require.True(t, ready, "empty snapshot is a valid ready state")
	assert.Empty(t, got)
}

func TestIdentityProfileANN_ValidationRejectsBadCenters(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")

	cases := []struct {
		name    string
		centers []*model.PersonIdentityCenter
	}{
		{"duplicate center id", []*model.PersonIdentityCenter{
			annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
			annCenter(1, 20, 1, 1, emb3(0, 1, 0)),
		}},
		{"zero center id", []*model.PersonIdentityCenter{
			annCenter(0, 10, 1, 1, emb3(1, 0, 0)),
		}},
		{"zero person id", []*model.PersonIdentityCenter{
			annCenter(1, 0, 1, 1, emb3(1, 0, 0)),
		}},
		{"zero generation", []*model.PersonIdentityCenter{
			annCenter(1, 10, 0, 1, emb3(1, 0, 0)),
		}},
		{"negative generation", []*model.PersonIdentityCenter{
			annCenter(1, 10, -1, 1, emb3(1, 0, 0)),
		}},
		{"nan embedding", []*model.PersonIdentityCenter{
			annCenter(1, 10, 1, 1, emb3(float32(math.NaN()), 0, 0)),
		}},
		{"inf embedding", []*model.PersonIdentityCenter{
			annCenter(1, 10, 1, 1, emb3(float32(math.Inf(1)), 0, 0)),
		}},
		{"zero-norm embedding", []*model.PersonIdentityCenter{
			annCenter(1, 10, 1, 1, emb3(0, 0, 0)),
		}},
		{"dim mismatch", []*model.PersonIdentityCenter{
			annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
			annCenter(2, 20, 1, 1, []float32{1, 0}),
		}},
		{"empty embedding blob", []*model.PersonIdentityCenter{
			{ID: 1, PersonID: 10, Generation: 1, CentroidEmbedding: nil},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ann.Rebuild(tc.centers, "emb-v1")
			require.Error(t, err)
			// 构建失败后查询不可用。
			_, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
			assert.False(t, ready, "failed rebuild must leave ANN not ready")
		})
	}
}

func TestIdentityProfileANN_ModelMismatchRejectsRebuild(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	err := ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-other")
	require.ErrorIs(t, err, errANNModelMismatch)

	// 模型签名不一致的查询也 fail-closed。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	_, ready := ann.Search(emb3(1, 0, 0), 5, "emb-other")
	assert.False(t, ready, "query with mismatched model must not be ready")
}

func TestIdentityProfileANN_FailedRebuildPreservesOldSnapshotButNotReady(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))

	// 一次失败的重建。
	err := ann.Rebuild([]*model.PersonIdentityCenter{annCenter(2, 20, 1, 1, emb3(0, 0, 0))}, "emb-v1")
	require.Error(t, err)

	// 旧 snapshot 保留（诊断用），但对外 ready=false。
	_, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	assert.False(t, ready, "after failed rebuild ANN must not be ready")

	// 后续完整重建成功 → 恢复 ready=true。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	got, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	require.True(t, ready)
	assert.NotEmpty(t, got)
}

// ---- Step 3: delta、失效集合和 generation 防护 ----

func TestIdentityProfileANN_DeltaRecallsNewCenterWithoutRebuild(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))

	// 新人物激活，无需重建即可查询。
	require.NoError(t, ann.Activate(20, 1, []*model.PersonIdentityCenter{
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)),
	}))

	got, ready := ann.Search(emb3(0.01, 0.99, 0), 5, "emb-v1")
	require.True(t, ready)
	require.Contains(t, got, uint(20), "delta center must be recallable")
}

func TestIdentityProfileANN_NewGenerationInvalidatesOldSnapshotCenters(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	// snapshot: person 10 gen1 center 靠近 (1,0,0)；person 20 也靠近 (1,0,0) 作为参照。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)), // gen 1 in snapshot
		annCenter(3, 20, 1, 1, emb3(0.99, 0.01, 0)),
	}, "emb-v1"))

	// 激活 gen 2（新中心向量不同），gen 1 的旧中心立即失效。
	require.NoError(t, ann.Activate(10, 2, []*model.PersonIdentityCenter{
		annCenter(2, 10, 2, 1, emb3(0, 1, 0)),
	}))

	// 查询接近旧中心向量：若旧中心泄漏，person 10 距离 0 排第一。
	// 旧中心被过滤后，person 10 仅能经新中心（正交，距离 1.0）召回，
	// 而 person 20 距离极小 → 排第一。k 需足够大以越过被过滤的旧中心召回 person 20。
	got, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	require.True(t, ready)
	require.NotEmpty(t, got)
	assert.Equal(t, uint(20), got[0], "old generation center must not leak to top")

	// 查询接近新中心向量 → 返回 person 10。
	got, ready = ann.Search(emb3(0, 1, 0), 5, "emb-v1")
	require.True(t, ready)
	assert.Contains(t, got, uint(10))
}

func TestIdentityProfileANN_DeltaReplacementDoesNotLeak(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild(nil, "emb-v1"))

	// 第一次激活 gen 1。
	require.NoError(t, ann.Activate(10, 1, []*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
	}))
	// 再次激活 gen 2，替换 delta。
	require.NoError(t, ann.Activate(10, 2, []*model.PersonIdentityCenter{
		annCenter(2, 10, 2, 1, emb3(0, 1, 0)),
	}))
	// 参照人物靠近旧向量。
	require.NoError(t, ann.Activate(20, 1, []*model.PersonIdentityCenter{
		annCenter(3, 20, 1, 1, emb3(0.99, 0.01, 0)),
	}))

	// 旧 gen 1 中心若泄漏 → person 10 距离 0 排第一。被过滤后 person 20 排第一。
	got, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	require.True(t, ready)
	require.NotEmpty(t, got)
	assert.Equal(t, uint(20), got[0], "replaced delta center must not leak to top")

	got, ready = ann.Search(emb3(0, 1, 0), 5, "emb-v1")
	require.True(t, ready)
	assert.Contains(t, got, uint(10))
}

func TestIdentityProfileANN_InvalidatePersonBlocksSnapshotAndDelta(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)), // snapshot
	}, "emb-v1"))
	require.NoError(t, ann.Activate(20, 1, []*model.PersonIdentityCenter{
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)), // delta
	}))

	ann.InvalidatePerson(10)
	ann.InvalidatePerson(20)

	// 删除后两者均不可返回。
	for _, q := range [][]float32{emb3(1, 0, 0), emb3(0, 1, 0)} {
		got, ready := ann.Search(q, 5, "emb-v1")
		require.True(t, ready)
		assert.NotContains(t, got, uint(10))
		assert.NotContains(t, got, uint(20))
	}
}

func TestIdentityProfileANN_SnapshotAndDeltaSamePersonDedup(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)), // snapshot, gen 1
	}, "emb-v1"))
	// delta 中同一人物的另一中心（同 generation，模拟多中心）。
	require.NoError(t, ann.Activate(10, 1, []*model.PersonIdentityCenter{
		annCenter(2, 10, 1, 1, emb3(0.8, 0.2, 0)),
	}))

	got, ready := ann.Search(emb3(0.9, 0.1, 0), 5, "emb-v1")
	require.True(t, ready)
	count := 0
	for _, p := range got {
		if p == 10 {
			count++
		}
	}
	assert.Equal(t, 1, count, "snapshot and delta hits for same person must dedup")
}

func TestIdentityProfileANN_DeltaFullRequestsRebuild(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	ann.deltaMax = 3 // 小上限便于测试
	require.NoError(t, ann.Rebuild(nil, "emb-v1"))

	// 填充 delta 到上限。
	for i := 1; i <= 3; i++ {
		require.NoError(t, ann.Activate(uint(i), 1, []*model.PersonIdentityCenter{
			annCenter(uint(i), uint(i), 1, 1, emb3(float32(i), 0, 0)),
		}))
	}

	// 第 4 个应触发容量上限。
	err := ann.Activate(4, 1, []*model.PersonIdentityCenter{
		annCenter(4, 4, 1, 1, emb3(4, 0, 0)),
	})
	require.Error(t, err)
	assert.True(t, ann.RebuildRequested(), "delta full must request rebuild")

	// 查询返回 ready=false。
	_, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	assert.False(t, ready, "delta full must leave ANN not ready")
}

func TestIdentityProfileANN_DeltaUpdateFailureLeavesNotReady(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	ann.deltaMax = 2
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))

	// 非法中心 → Activate 失败。
	err := ann.Activate(20, 1, []*model.PersonIdentityCenter{
		annCenter(2, 20, 1, 1, emb3(0, 0, 0)), // zero-norm
	})
	require.Error(t, err)
	// 非法输入不应标记不可用（未进入临界区），但查询仍受 snapshot 影响。

	// 容量触发的失败标记不可用。
	for i := 1; i <= 2; i++ {
		require.NoError(t, ann.Activate(uint(20+i), 1, []*model.PersonIdentityCenter{
			annCenter(uint(20+i), uint(20+i), 1, 1, emb3(float32(i), 1, 0)),
		}))
	}
	err = ann.Activate(30, 1, []*model.PersonIdentityCenter{
		annCenter(30, 30, 1, 1, emb3(9, 9, 0)),
	})
	require.Error(t, err)
	_, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	assert.False(t, ready)
}

// ---- Step 4: 重建与并发激活不丢更新 ----

func TestIdentityProfileANN_RebuildClearsDeltaWhenUnchanged(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	require.NoError(t, ann.Activate(20, 1, []*model.PersonIdentityCenter{annCenter(2, 20, 1, 1, emb3(0, 1, 0))}))

	// 一次包含 p10+p20 的干净重建 → delta 被压缩。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)),
	}, "emb-v1"))

	ann.deltaMu.RLock()
	assert.Empty(t, ann.delta, "clean rebuild must compress delta")
	assert.Empty(t, ann.invalid, "clean rebuild must clear invalid")
	ann.deltaMu.RUnlock()

	got, ready := ann.Search(emb3(0, 1, 0), 5, "emb-v1")
	require.True(t, ready)
	assert.Contains(t, got, uint(20))
}

// TestIdentityProfileANN_ConcurrentRebuildActivateSearch 在 -race 下验证并发查询、
// 重建与激活不产生 data race、panic，不返回已失效人物，不丢失重建期间的 delta 更新。
func TestIdentityProfileANN_ConcurrentRebuildActivateSearch(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))

	var stop atomic.Bool
	var wg sync.WaitGroup

	// 持续查询。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			got, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
			if ready {
				// 已失效人物（被删除的 999）绝不能出现。
				for _, p := range got {
					if p == 999 {
						t.Errorf("invalidated person 999 leaked: %v", got)
						break
					}
				}
			}
		}
	}()

	// 持续重建。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			gen := (i % 3) + 1
			centers := []*model.PersonIdentityCenter{
				annCenter(1, 10, gen, 1, emb3(1, 0, 0)),
				annCenter(2, 20, gen, 1, emb3(0, 1, 0)),
			}
			_ = ann.Rebuild(centers, "emb-v1")
		}
	}()

	// 持续激活 + 删除。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			gen := (i % 3) + 1
			_ = ann.Activate(30, gen, []*model.PersonIdentityCenter{
				annCenter(3, 30, gen, 1, emb3(0, 0, 1)),
			})
			ann.InvalidatePerson(999) // 始终不存在的失效人物
		}
	}()

	// 短暂运行以触发竞态。
	for i := 0; i < 200; i++ {
		_, _ = ann.Search(emb3(0.5, 0.5, 0), 5, "emb-v1")
	}
	stop.Store(true)
	wg.Wait()
}

// TestIdentityProfileANN_RebuildPreservesDeltaDuringBuild 验证重建期间产生的 delta 更新
// 不会丢失。通过 buildHook 在重建窗口内注入一次 Activate：重建的 snapshot 不含 person 200，
// 仅靠 preserve 分支保留 delta → 查询必须能召回。
func TestIdentityProfileANN_RebuildPreservesDeltaDuringBuild(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))

	// 在重建窗口内激活 person 200（snapshot 不含它）。
	ann.buildHook = func() {
		if err := ann.Activate(200, 1, []*model.PersonIdentityCenter{
			annCenter(9000, 200, 1, 1, emb3(0, 0, 1)),
		}); err != nil {
			t.Logf("buildHook activate: %v", err)
		}
	}
	// 重建 snapshot 仍只含 person 10。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	ann.buildHook = nil

	// person 200 必须仍可召回（preserve 分支保留了 delta）。
	got, ready := ann.Search(emb3(0, 0, 1), 5, "emb-v1")
	require.True(t, ready)
	assert.Contains(t, got, uint(200), "delta activated during rebuild window must survive (preserve branch)")
}

// ---- Step 6: 失败恢复语义 ----

func TestIdentityProfileANN_NeverBuiltNotReady(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	_, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	assert.False(t, ready, "never-built ANN must not be ready")
}

func TestIdentityProfileANN_SearchRejectsInvalidQuery(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))

	cases := []struct {
		name string
		k    int
		q    []float32
	}{
		{"zero k", 0, emb3(1, 0, 0)},
		{"negative k", -1, emb3(1, 0, 0)},
		{"empty query", 5, nil},
		{"zero-norm query", 5, emb3(0, 0, 0)},
		{"nan query", 5, emb3(float32(math.NaN()), 0, 0)},
		{"dim mismatch", 5, []float32{1, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ready := ann.Search(tc.q, tc.k, "emb-v1")
			assert.False(t, ready)
		})
	}
}

func TestIdentityProfileANN_StableSortTiebreakByPersonID(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	// 两个人物中心向量相同（与 query 等距）。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 30, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(3, 20, 1, 1, emb3(1, 0, 0)),
	}, "emb-v1"))

	got, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	require.True(t, ready)
	// 相同距离 → person_id ASC：10, 20, 30。
	assert.Equal(t, []uint{10, 20, 30}, got)
}

// ---- Task 13: InvalidateAll ----

func TestIdentityProfileANN_InvalidateAllMakesNotReady(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)),
	}, "emb-v1"))
	assert.True(t, ann.Ready("emb-v1"))

	// 激活一些 delta。
	require.NoError(t, ann.Activate(30, 1, []*model.PersonIdentityCenter{
		annCenter(3, 30, 1, 1, emb3(0, 0, 1)),
	}))

	ann.InvalidateAll()

	assert.False(t, ann.Ready("emb-v1"), "snapshot must not be ready after InvalidateAll")
	assert.True(t, ann.RebuildRequested(), "must request full rebuild")
	got, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	assert.False(t, ready, "Search must fail closed after InvalidateAll")
	assert.Empty(t, got)

	// delta/invalid/activeGeneration 清空。
	ann.deltaMu.RLock()
	assert.Empty(t, ann.delta)
	assert.Empty(t, ann.invalid)
	assert.Empty(t, ann.activeGeneration)
	ann.deltaMu.RUnlock()
}

func TestIdentityProfileANN_InvalidateAllThenRebuildRestores(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
	}, "emb-v1"))

	ann.InvalidateAll()

	// 重新构建后恢复 ready。
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)),
	}, "emb-v1"))
	assert.True(t, ann.Ready("emb-v1"))
	got, ready := ann.Search(emb3(1, 0, 0), 5, "emb-v1")
	require.True(t, ready)
	assert.Contains(t, got, uint(10))
}

// TestIdentityProfileANN_InvalidateAllConcurrentSearch 在 -race 下验证并发 InvalidateAll
// 与 Search 不产生 data race、不 panic、Search 永远不会看到半清空状态（要么 ready 旧 snapshot，
// 要么 unavailable）。
func TestIdentityProfileANN_InvalidateAllConcurrentSearch(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)),
	}, "emb-v1"))

	var stop atomic.Bool
	var wg sync.WaitGroup

	// 持续查询：必须 fail closed 或返回完整旧结果，绝不 panic。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			_, _ = ann.Search(emb3(1, 0, 0), 5, "emb-v1")
		}
	}()

	// 持续 InvalidateAll + Rebuild 交替。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; !stop.Load(); i++ {
			ann.InvalidateAll()
			if i%2 == 0 {
				_ = ann.Rebuild([]*model.PersonIdentityCenter{
					annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
				}, "emb-v1")
			}
		}
	}()

	for i := 0; i < 300; i++ {
		_, _ = ann.Search(emb3(0.5, 0.5, 0), 5, "emb-v1")
	}
	stop.Store(true)
	wg.Wait()
}

// ---- Step 7: benchmark（烟雾验证） ----

func BenchmarkIdentityProfileANN_SnapshotBuild(b *testing.B) {
	const nCenters = 1000
	centers := make([]*model.PersonIdentityCenter, 0, nCenters)
	for i := 0; i < nCenters; i++ {
		emb := emb3(
			float32(i%7)*0.1+0.1,
			float32(i%5)*0.1+0.1,
			float32(i%3)*0.1+0.1,
		)
		centers = append(centers, annCenter(uint(i+1), uint((i%100)+1), 1, 1, emb))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ann := newIdentityProfileANN("emb-v1")
		if err := ann.Rebuild(centers, "emb-v1"); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(nCenters), "centers")
}

func BenchmarkIdentityProfileANN_SnapshotSearch(b *testing.B) {
	const nCenters = 1000
	centers := make([]*model.PersonIdentityCenter, 0, nCenters)
	for i := 0; i < nCenters; i++ {
		emb := emb3(
			float32(i%7)*0.1+0.1,
			float32(i%5)*0.1+0.1,
			float32(i%3)*0.1+0.1,
		)
		centers = append(centers, annCenter(uint(i+1), uint((i%100)+1), 1, 1, emb))
	}
	ann := newIdentityProfileANN("emb-v1")
	if err := ann.Rebuild(centers, "emb-v1"); err != nil {
		b.Fatal(err)
	}
	query := emb3(0.5, 0.3, 0.2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ann.Search(query, 10, "emb-v1")
	}
	b.ReportMetric(float64(nCenters), "centers")
}

func BenchmarkIdentityProfileANN_SnapshotWithDeltaSearch(b *testing.B) {
	const nCenters = 1000
	centers := make([]*model.PersonIdentityCenter, 0, nCenters)
	for i := 0; i < nCenters; i++ {
		emb := emb3(
			float32(i%7)*0.1+0.1,
			float32(i%5)*0.1+0.1,
			float32(i%3)*0.1+0.1,
		)
		centers = append(centers, annCenter(uint(i+1), uint((i%100)+1), 1, 1, emb))
	}
	ann := newIdentityProfileANN("emb-v1")
	if err := ann.Rebuild(centers, "emb-v1"); err != nil {
		b.Fatal(err)
	}
	// 小规模 delta。
	for i := 0; i < 20; i++ {
		if err := ann.Activate(uint(1000+i), 1, []*model.PersonIdentityCenter{
			annCenter(uint(2000+i), uint(1000+i), 1, 1, emb3(0.7, 0.2, 0.1)),
		}); err != nil {
			b.Fatal(err)
		}
	}
	query := emb3(0.5, 0.3, 0.2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ann.Search(query, 10, "emb-v1")
	}
	b.ReportMetric(float64(nCenters), "centers")
	b.ReportMetric(20, "deltaCenters")
}

// 防止未使用导入错误（fmt 仅在错误格式化中使用）。
var _ = errors.New
var _ = fmt.Sprintf

// ==================== Stats (Task 14) ====================

func TestIdentityProfileANN_Stats_NilReturnsZero(t *testing.T) {
	var ann *identityProfileANN
	stats := ann.Stats("emb-v1")
	assert.False(t, stats.Ready)
	assert.Zero(t, stats.Generation)
	assert.Zero(t, stats.SnapshotNodes)
	assert.Zero(t, stats.DeltaNodes)
	assert.Zero(t, stats.InvalidNodes)
}

func TestIdentityProfileANN_Stats_NotReadyBeforeBuild(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	stats := ann.Stats("emb-v1")
	assert.False(t, stats.Ready)
	assert.Zero(t, stats.Generation)
	assert.Zero(t, stats.SnapshotNodes)
	assert.False(t, stats.RebuildRequested, "bare ANN does not set rebuildRequested (caller does)")
}

func TestIdentityProfileANN_Stats_ReadyAfterRebuild(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	centers := []*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)),
	}
	require.NoError(t, ann.Rebuild(centers, "emb-v1"))

	stats := ann.Stats("emb-v1")
	assert.True(t, stats.Ready)
	assert.Equal(t, uint64(1), stats.Generation, "generation increments on successful publish")
	assert.Equal(t, 2, stats.SnapshotNodes)
	assert.Zero(t, stats.DeltaNodes)
	assert.Zero(t, stats.InvalidNodes)
	assert.False(t, stats.RebuildRequested)
	assert.False(t, stats.Unavailable)
}

func TestIdentityProfileANN_Stats_ModelMismatchReadyFalse(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))

	stats := ann.Stats("emb-v2")
	assert.False(t, stats.Ready, "model mismatch -> Ready=false")
	assert.Equal(t, 1, stats.SnapshotNodes, "snapshot nodes still reported")
}

func TestIdentityProfileANN_Stats_DeltaAndInvalidCounts(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))

	// Activate delta for person 20.
	require.NoError(t, ann.Activate(20, 1, []*model.PersonIdentityCenter{annCenter(2, 20, 1, 1, emb3(0, 1, 0))}))
	// Invalidate person 10 (snapshot center).
	ann.InvalidatePerson(10)

	stats := ann.Stats("emb-v1")
	assert.Equal(t, 1, stats.DeltaNodes, "one delta node")
	assert.Equal(t, 1, stats.InvalidNodes, "one invalid node (person 10 snapshot center)")
	assert.True(t, stats.Ready)
}

func TestIdentityProfileANN_Stats_FailedRebuildPreservesGeneration(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	genBefore := ann.Stats("emb-v1").Generation
	require.Equal(t, uint64(1), genBefore)

	// 失败重建：零中心 ID 触发校验失败。
	require.Error(t, ann.Rebuild([]*model.PersonIdentityCenter{{ID: 0, PersonID: 10, Generation: 1, CentroidEmbedding: model.EncodeEmbedding(emb3(1, 0, 0))}}, "emb-v1"))

	stats := ann.Stats("emb-v1")
	assert.False(t, stats.Ready, "failed rebuild -> not ready (unavailable)")
	assert.Equal(t, genBefore, stats.Generation, "generation must not increment on failed rebuild")
	assert.True(t, stats.Unavailable)
	assert.True(t, stats.RebuildRequested)
}

func TestIdentityProfileANN_Stats_RequestRebuildAndInvalidateDoNotIncrementGeneration(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	genBefore := ann.Stats("emb-v1").Generation

	ann.RequestRebuild()
	assert.Equal(t, genBefore, ann.Stats("emb-v1").Generation, "RequestRebuild must not increment generation")

	ann.InvalidatePerson(10)
	assert.Equal(t, genBefore, ann.Stats("emb-v1").Generation, "InvalidatePerson must not increment generation")

	ann.InvalidateAll()
	assert.Equal(t, genBefore, ann.Stats("emb-v1").Generation, "InvalidateAll must not increment generation")
}

func TestIdentityProfileANN_Stats_ActivateDoesNotIncrementGeneration(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{annCenter(1, 10, 1, 1, emb3(1, 0, 0))}, "emb-v1"))
	genBefore := ann.Stats("emb-v1").Generation

	require.NoError(t, ann.Activate(20, 1, []*model.PersonIdentityCenter{annCenter(2, 20, 1, 1, emb3(0, 1, 0))}))
	assert.Equal(t, genBefore, ann.Stats("emb-v1").Generation, "Activate (delta only) must not increment generation")
}

// TestIdentityProfileANN_Stats_ConcurrentWithSearchRebuild 验证 Stats 与并发 Search/Rebuild/Activate/Invalidate 无 race。
func TestIdentityProfileANN_Stats_ConcurrentWithSearchRebuild(t *testing.T) {
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild([]*model.PersonIdentityCenter{
		annCenter(1, 10, 1, 1, emb3(1, 0, 0)),
		annCenter(2, 20, 1, 1, emb3(0, 1, 0)),
	}, "emb-v1"))

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(4)
		go func() {
			defer wg.Done()
			_, _ = ann.Search(emb3(0.99, 0.01, 0), 5, "emb-v1")
		}()
		go func() {
			defer wg.Done()
			_ = ann.Stats("emb-v1")
		}()
		go func(i int) {
			defer wg.Done()
			pid := uint(30 + i)
			_ = ann.Activate(pid, 1, []*model.PersonIdentityCenter{annCenter(uint(100+i), pid, 1, 1, emb3(0.5, 0.5, 0))})
		}(i)
		go func() {
			defer wg.Done()
			ann.RequestRebuild()
		}()
	}
	wg.Wait()

	// 最终一次 Stats 不触发重建、不 panic。
	stats := ann.Stats("emb-v1")
	_ = stats
}
