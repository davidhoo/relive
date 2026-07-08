package service

import (
	"math"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testBuilderConfig 返回测试用确定性配置。
func testBuilderConfig() identityProfileBuilderConfig {
	return identityProfileBuilderConfig{
		MaxCenters:               6,
		MinCenterFaces:           3,
		MinCenterPhotos:          2,
		MaxIterations:            5,
		AssignmentThreshold:      0.50,
		MergeThreshold:           0.75,
		MinQuality:               0.30,
		MinAutomaticClusterScore: 0.50,
	}
}

// mkFace 构造测试用 Face（embedding 为归一化向量的编码）。
func mkFace(id, photoID, personID uint, emb []float32, manual bool, quality, score float64) *model.Face {
	pid := personID
	f := &model.Face{
		ID:           id,
		PhotoID:      photoID,
		PersonID:     &pid,
		QualityScore: quality,
		ClusterScore: score,
		Confidence:   0.9,
		Embedding:    model.EncodeEmbedding(emb),
		ManualLocked: manual,
	}
	if manual {
		f.ClusterStatus = model.FaceClusterStatusManual
	} else {
		f.ClusterStatus = model.FaceClusterStatusAssigned
	}
	return f
}

// 归一化的 4D 测试向量。
var (
	vA   = []float32{1, 0, 0, 0}
	vA2  = normv([]float32{0.999, 0.012, 0, 0})
	vA3  = normv([]float32{0.998, 0.020, 0, 0})
	vB   = []float32{0, 1, 0, 0}
	vB2  = normv([]float32{0, 0.999, 0.012, 0})
	vB3  = normv([]float32{0, 0.998, 0.020, 0})
	vOut = []float32{0, 0, 0, 1}
)

func normv(v []float32) []float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	n := math.Sqrt(s)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}

// ---- Build 级测试 ----

func TestIdentityProfileBuilder_SingleStablePattern(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, false, 0.8, 0.8),
		mkFace(2, 101, pid, vA2, false, 0.8, 0.8),
		mkFace(3, 102, pid, vA3, false, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.NotNil(t, build)
	require.Len(t, build.Centers, 1)
	require.Len(t, build.Members, 3)
	c := build.Centers[0]
	assert.Equal(t, 0, c.Ordinal)
	assert.Equal(t, 3, c.SupportCount)
	assert.False(t, c.Confirmed)
	assert.NotNil(t, c.MedoidFaceID)
	for _, m := range build.Members {
		assert.Equal(t, model.PersonIdentityMemberStateAccepted, m.State)
		assert.NotNil(t, m.CenterID)
	}
}

func TestIdentityProfileBuilder_TwoDistinctPatterns(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, false, 0.8, 0.8),
		mkFace(2, 101, pid, vA2, false, 0.8, 0.8),
		mkFace(3, 102, pid, vA3, false, 0.8, 0.8),
		mkFace(4, 100, pid, vB, false, 0.8, 0.8),
		mkFace(5, 101, pid, vB2, false, 0.8, 0.8),
		mkFace(6, 103, pid, vB3, false, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Len(t, build.Centers, 2)
	assert.Equal(t, 6, len(build.Members))
	// 两个中心 ordinal 分别为 0、1。
	ordinals := map[int]bool{}
	for _, c := range build.Centers {
		ordinals[c.Ordinal] = true
		assert.Equal(t, 3, c.SupportCount)
	}
	assert.True(t, ordinals[0] && ordinals[1])
}

func TestIdentityProfileBuilder_OutlierCandidate(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, false, 0.8, 0.8),
		mkFace(2, 101, pid, vA2, false, 0.8, 0.8),
		mkFace(3, 102, pid, vA3, false, 0.8, 0.8),
		mkFace(4, 200, pid, vOut, false, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Len(t, build.Centers, 1)
	var outlier *model.PersonIdentityCenterMember
	for _, m := range build.Members {
		if m.FaceID == 4 {
			outlier = m
		}
	}
	require.NotNil(t, outlier)
	assert.Equal(t, model.PersonIdentityMemberStateCandidate, outlier.State)
	assert.Nil(t, outlier.CenterID)
}

func TestIdentityProfileBuilder_ThreeFacesTwoPhotosFormCenter(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, false, 0.8, 0.8),
		mkFace(2, 100, pid, vA2, false, 0.8, 0.8),
		mkFace(3, 101, pid, vA3, false, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Len(t, build.Centers, 1)
	assert.Equal(t, 3, build.Centers[0].SupportCount)
	for _, m := range build.Members {
		assert.Equal(t, model.PersonIdentityMemberStateAccepted, m.State)
	}
}

func TestIdentityProfileBuilder_ThreeFacesOnePhotoNoCenter(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, false, 0.8, 0.8),
		mkFace(2, 100, pid, vA2, false, 0.8, 0.8),
		mkFace(3, 100, pid, vA3, false, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Empty(t, build.Centers)
	for _, m := range build.Members {
		assert.Equal(t, model.PersonIdentityMemberStateCandidate, m.State, "face %d", m.FaceID)
		assert.Nil(t, m.CenterID)
	}
}

func TestIdentityProfileBuilder_SingleManualConfirmed(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, true, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Len(t, build.Centers, 1)
	c := build.Centers[0]
	assert.True(t, c.Confirmed)
	assert.Equal(t, 1, c.SupportCount)
	assert.Equal(t, uint(1), *c.MedoidFaceID)
	// 单样本质心即自身，相似度应等于 1；float32/SIMD 累积误差可能使原始值略大于
	// 1.0，cosineSimilarity 已钳制到 [-1, 1]，故断言近似 1 且不超过 1.0。
	assert.InDelta(t, 1.0, c.SimilarityP10, 1e-6)
	assert.InDelta(t, 1.0, c.SimilarityP50, 1e-6)
	assert.LessOrEqual(t, c.SimilarityP10, 1.0)
	assert.LessOrEqual(t, c.SimilarityP50, 1.0)
	require.Len(t, build.Members, 1)
	assert.Equal(t, model.PersonIdentityMemberStateAccepted, build.Members[0].State)
}

func TestIdentityProfileBuilder_AutoSingleNoStableCenter(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, false, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Empty(t, build.Centers)
	require.Len(t, build.Members, 1)
	assert.Equal(t, model.PersonIdentityMemberStateCandidate, build.Members[0].State)
}

func TestIdentityProfileBuilder_ManualPriorityOverAuto(t *testing.T) {
	// 人工权重严格大于自动权重。
	wm, okm := faceWeight(true, 0.8, 0.8)
	require.True(t, okm)
	wa, oka := faceWeight(false, 0.8, 0.8)
	require.True(t, oka)
	assert.Greater(t, wm, wa)
	assert.Equal(t, ipWeightMax, wm)
	assert.LessOrEqual(t, wa, ipAutoWeightCeil)

	// Build 层：一个 manual + 两个 auto 同模式，confirmed 中心存在且 manual 为 medoid。
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, true, 0.8, 0.8), // manual，最小 faceID
		mkFace(2, 101, pid, vA2, false, 0.8, 0.8),
		mkFace(3, 102, pid, vA3, false, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Len(t, build.Centers, 1)
	assert.True(t, build.Centers[0].Confirmed)
	assert.Equal(t, 3, build.Centers[0].SupportCount)
	// 两个人工/auto 证据均归入同一 confirmed 中心。
	for _, m := range build.Members {
		assert.Equal(t, model.PersonIdentityMemberStateAccepted, m.State)
	}
}

func TestIdentityProfileBuilder_InputOrderInvariant(t *testing.T) {
	cfg := testBuilderConfig()
	pid := uint(10)
	mk := func() []*model.Face {
		return []*model.Face{
			mkFace(3, 102, pid, vA3, false, 0.8, 0.8),
			mkFace(1, 100, pid, vA, true, 0.8, 0.8),
			mkFace(5, 101, pid, vB2, false, 0.8, 0.8),
			mkFace(2, 101, pid, vA2, false, 0.8, 0.8),
			mkFace(4, 100, pid, vB, false, 0.8, 0.8),
			mkFace(6, 103, pid, vB3, false, 0.8, 0.8),
		}
	}
	b1 := NewIdentityProfileBuilder(cfg)
	build1, err := b1.Build(pid, mk())
	require.NoError(t, err)

	// 不同顺序。
	b2 := NewIdentityProfileBuilder(cfg)
	build2, err := b2.Build(pid, []*model.Face{
		mkFace(6, 103, pid, vB3, false, 0.8, 0.8),
		mkFace(1, 100, pid, vA, true, 0.8, 0.8),
		mkFace(4, 100, pid, vB, false, 0.8, 0.8),
		mkFace(2, 101, pid, vA2, false, 0.8, 0.8),
		mkFace(3, 102, pid, vA3, false, 0.8, 0.8),
		mkFace(5, 101, pid, vB2, false, 0.8, 0.8),
	})
	require.NoError(t, err)

	// 中心数量、ordinal、medoid、support_count 完全一致。
	require.Equal(t, len(build1.Centers), len(build2.Centers))
	for i := range build1.Centers {
		assert.Equal(t, build1.Centers[i].Ordinal, build2.Centers[i].Ordinal)
		assert.Equal(t, build1.Centers[i].MedoidFaceID, build2.Centers[i].MedoidFaceID)
		assert.Equal(t, build1.Centers[i].SupportCount, build2.Centers[i].SupportCount)
		assert.Equal(t, build1.Centers[i].Confirmed, build2.Centers[i].Confirmed)
		assert.Equal(t, build1.Centers[i].CentroidEmbedding, build2.Centers[i].CentroidEmbedding)
	}
	// 成员归属一致。
	m1 := map[uint]*model.PersonIdentityCenterMember{}
	for _, m := range build1.Members {
		m1[m.FaceID] = m
	}
	for _, m := range build2.Members {
		o := m1[m.FaceID]
		require.NotNil(t, o)
		assert.Equal(t, o.State, m.State)
		assert.Equal(t, o.CenterID, m.CenterID)
	}
}

func TestIdentityProfileBuilder_DoesNotMutateInput(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(2, 101, pid, vA2, false, 0.8, 0.8),
		mkFace(1, 100, pid, vA, true, 0.8, 0.8),
		mkFace(3, 102, pid, vA3, false, 0.8, 0.8),
	}
	// 记录原始顺序与字段快照。
	origOrder := []uint{faces[0].ID, faces[1].ID, faces[2].ID}
	origEmb := append([]byte(nil), faces[0].Embedding...)
	origManual := faces[1].ManualLocked

	_, err := b.Build(pid, faces)
	require.NoError(t, err)

	// 输入切片顺序不变。
	assert.Equal(t, origOrder, []uint{faces[0].ID, faces[1].ID, faces[2].ID})
	// 字段未被修改。
	assert.Equal(t, origEmb, faces[0].Embedding)
	assert.Equal(t, origManual, faces[1].ManualLocked)
}

func TestIdentityProfileBuilder_InvalidEmbeddings(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	zero := []float32{0, 0, 0, 0}
	nan := []float32{float32(math.NaN()), 0, 0, 0}
	inf := []float32{float32(math.Inf(1)), 0, 0, 0}
	wrongDim := []float32{1, 0, 0} // 3D，与其它 4D 不一致
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, false, 0.8, 0.8),       // 有效
		mkFace(2, 101, pid, zero, false, 0.8, 0.8),     // 零范数
		mkFace(3, 102, pid, nan, false, 0.8, 0.8),      // NaN
		mkFace(4, 103, pid, inf, false, 0.8, 0.8),      // Inf
		mkFace(5, 104, pid, wrongDim, false, 0.8, 0.8), // 错误维度
	}
	// 仅一张有效自动脸，无法形成中心。
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Empty(t, build.Centers)
	require.Len(t, build.Members, 5)
	excluded := 0
	for _, m := range build.Members {
		if m.FaceID == 1 {
			assert.Equal(t, model.PersonIdentityMemberStateCandidate, m.State)
		} else {
			assert.Equal(t, model.PersonIdentityMemberStateExcluded, m.State, "face %d", m.FaceID)
			excluded++
		}
	}
	assert.Equal(t, 4, excluded)
}

func TestIdentityProfileBuilder_EmptyEmbeddingExcluded(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	f := mkFace(1, 100, pid, nil, false, 0.8, 0.8)
	f.Embedding = nil
	build, err := b.Build(pid, []*model.Face{f})
	require.NoError(t, err)
	require.Empty(t, build.Centers)
	require.Len(t, build.Members, 1)
	assert.Equal(t, model.PersonIdentityMemberStateExcluded, build.Members[0].State)
}

func TestIdentityProfileBuilder_PersonIDMismatch(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	other := uint(99)
	f := mkFace(1, 100, other, vA, false, 0.8, 0.8) // PersonID=99 != 10
	build, err := b.Build(pid, []*model.Face{f})
	require.NoError(t, err)
	require.Empty(t, build.Centers)
	require.Len(t, build.Members, 1)
	assert.Equal(t, model.PersonIdentityMemberStateExcluded, build.Members[0].State)
}

func TestIdentityProfileBuilder_NilPersonIDExcluded(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	f := mkFace(1, 100, pid, vA, false, 0.8, 0.8)
	f.PersonID = nil // 未指派
	build, err := b.Build(pid, []*model.Face{f})
	require.NoError(t, err)
	require.Empty(t, build.Centers)
	assert.Equal(t, model.PersonIdentityMemberStateExcluded, build.Members[0].State)
}

func TestIdentityProfileBuilder_EmptyInput(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	build, err := b.Build(uint(10), nil)
	require.NoError(t, err)
	require.NotNil(t, build)
	require.NotNil(t, build.Profile)
	require.Empty(t, build.Centers)
	require.Empty(t, build.Members)
	assert.Equal(t, uint(10), build.Profile.PersonID)
}

func TestIdentityProfileBuilder_LowQualityExcluded(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	// quality < MinQuality(0.3) → excluded（非 manual）。
	f := mkFace(1, 100, pid, vA, false, 0.1, 0.8)
	build, err := b.Build(pid, []*model.Face{f})
	require.NoError(t, err)
	assert.Equal(t, model.PersonIdentityMemberStateExcluded, build.Members[0].State)
}

func TestIdentityProfileBuilder_LowScoreCandidate(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	// quality 合格但 cluster_score < MinAutomaticClusterScore → candidate（eligible=false）。
	f := mkFace(1, 100, pid, vA, false, 0.8, 0.2)
	build, err := b.Build(pid, []*model.Face{f})
	require.NoError(t, err)
	assert.Equal(t, model.PersonIdentityMemberStateCandidate, build.Members[0].State)
	require.Empty(t, build.Centers)
}

func TestIdentityProfileBuilder_CompatibleCentersMerge(t *testing.T) {
	// 两个人工确认人脸，embedding 极相似（cosine ≥ MergeThreshold）→ 合并为一个 confirmed 中心。
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, true, 0.8, 0.8),
		mkFace(2, 101, pid, vA2, true, 0.8, 0.8), // cosine(vA,vA2) ≈ 0.9999
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Len(t, build.Centers, 1, "两个相似人工中心应合并")
	c := build.Centers[0]
	assert.True(t, c.Confirmed)
	assert.Equal(t, 2, c.SupportCount)
}

func TestIdentityProfileBuilder_DissimilarManualCentersDoNotMerge(t *testing.T) {
	// 两个人工确认人脸，embedding 正交（cosine=0 < MergeThreshold）→ 不合并。
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, true, 0.8, 0.8),
		mkFace(2, 101, pid, vB, true, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.Len(t, build.Centers, 2)
}

func TestIdentityProfileBuilder_MaxCentersConstraint(t *testing.T) {
	// 三个明显不同的模式，MaxCenters=2 → 仅保留 2 个中心，最弱中心成员退回 candidate。
	cfg := testBuilderConfig()
	cfg.MaxCenters = 2
	b := NewIdentityProfileBuilder(cfg)
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, false, 0.8, 0.8),
		mkFace(2, 101, pid, vA2, false, 0.8, 0.8),
		mkFace(3, 102, pid, vA3, false, 0.8, 0.8),
		mkFace(4, 100, pid, vB, false, 0.8, 0.8),
		mkFace(5, 101, pid, vB2, false, 0.8, 0.8),
		mkFace(6, 103, pid, vB3, false, 0.8, 0.8),
		// 第三个模式（正交于 vA、vB），仅 3 张脸但属于最弱中心之一。
		mkFace(7, 200, pid, vOut, false, 0.8, 0.8),
		mkFace(8, 201, pid, vOut, false, 0.8, 0.8),
		mkFace(9, 202, pid, vOut, false, 0.8, 0.8),
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.LessOrEqual(t, len(build.Centers), 2, "不得超过 MaxCenters")
	// 被退回的成员应为 candidate。
	for _, m := range build.Members {
		if m.State != model.PersonIdentityMemberStateAccepted {
			assert.Equal(t, model.PersonIdentityMemberStateCandidate, m.State)
			assert.Nil(t, m.CenterID)
		}
	}
	// 至少有一个 candidate（第三个模式的成员）。
	hasCandidate := false
	for _, m := range build.Members {
		if m.State == model.PersonIdentityMemberStateCandidate {
			hasCandidate = true
		}
	}
	assert.True(t, hasCandidate, "超额弱中心应退回 candidate")
}

func TestIdentityProfileBuilder_MaxIterationsTerminates(t *testing.T) {
	// 复杂场景：确保最多 MaxIterations 轮后终止，不无限迭代。
	cfg := testBuilderConfig()
	cfg.MaxIterations = 5
	b := NewIdentityProfileBuilder(cfg)
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, true, 0.8, 0.8),
		mkFace(2, 101, pid, vA2, false, 0.8, 0.8),
		mkFace(3, 102, pid, vA3, false, 0.8, 0.8),
		mkFace(4, 100, pid, vB, false, 0.8, 0.8),
		mkFace(5, 101, pid, vB2, false, 0.8, 0.8),
		mkFace(6, 103, pid, vB3, false, 0.8, 0.8),
		mkFace(7, 200, pid, vOut, false, 0.8, 0.8),
	}
	// 只要能正常返回即证明未无限迭代。
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	require.NotNil(t, build)
	require.Len(t, build.Members, 7)
}

func TestIdentityProfileBuilder_EveryFaceAppearsOnce(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	pid := uint(10)
	faces := []*model.Face{
		mkFace(1, 100, pid, vA, true, 0.8, 0.8),                     // accepted (confirmed center)
		mkFace(2, 101, pid, vA2, false, 0.8, 0.8),                   // accepted
		mkFace(3, 102, pid, vOut, false, 0.8, 0.8),                  // candidate
		mkFace(4, 103, pid, []float32{0, 0, 0, 0}, false, 0.8, 0.8), // excluded
		mkFace(5, 104, uint(99), vA, false, 0.8, 0.8),               // excluded (person mismatch)
	}
	build, err := b.Build(pid, faces)
	require.NoError(t, err)
	// 每张输入人脸恰好出现一次。
	seen := map[uint]int{}
	for _, m := range build.Members {
		seen[m.FaceID]++
	}
	require.Len(t, seen, 5)
	for id, n := range seen {
		assert.Equal(t, 1, n, "face %d appears %d times", id, n)
	}
}

func TestIdentityProfileBuilder_ConfigValidationRejectsNaN(t *testing.T) {
	cfg := testBuilderConfig()
	cfg.AssignmentThreshold = math.NaN()
	b := NewIdentityProfileBuilder(cfg)
	_, err := b.Build(uint(1), nil)
	assert.Error(t, err)
}

// ---- 纯函数辅助测试 ----

func TestNormalizeEmbedding(t *testing.T) {
	v, ok := normalizeEmbedding([]float32{3, 4})
	require.True(t, ok)
	assert.InDelta(t, 0.6, float64(v[0]), 1e-6)
	assert.InDelta(t, 0.8, float64(v[1]), 1e-6)

	_, ok = normalizeEmbedding(nil)
	assert.False(t, ok)
	_, ok = normalizeEmbedding([]float32{0, 0, 0})
	assert.False(t, ok)
	_, ok = normalizeEmbedding([]float32{float32(math.NaN()), 1})
	assert.False(t, ok)
	_, ok = normalizeEmbedding([]float32{float32(math.Inf(1)), 1})
	assert.False(t, ok)
}

func TestFaceWeight(t *testing.T) {
	// 人工确认：固定最大权重。
	w, ok := faceWeight(true, 0.0, 0.0)
	require.True(t, ok)
	assert.Equal(t, ipWeightMax, w)

	// 自动：高质量高分数 → 接近上限但 < 人工权重。
	w, ok = faceWeight(false, 1.0, 1.0)
	require.True(t, ok)
	assert.InDelta(t, ipAutoWeightCeil, w, 1e-9)
	assert.Less(t, w, ipWeightMax)

	// 自动：低质量低分数 → 接近下限。
	w, ok = faceWeight(false, 0.0, 0.0)
	require.True(t, ok)
	assert.InDelta(t, ipWeightMin, w, 1e-9)

	// NaN/Inf/负值被拒绝。
	_, ok = faceWeight(false, math.NaN(), 0.5)
	assert.False(t, ok)
	_, ok = faceWeight(false, 0.5, math.Inf(1))
	assert.False(t, ok)
	_, ok = faceWeight(false, -0.1, 0.5)
	assert.False(t, ok)
	_, ok = faceWeight(false, 0.5, -0.1)
	assert.False(t, ok)

	// 权重范围 [ipWeightMin, ipAutoWeightCeil]（自动）。
	for q := 0.0; q <= 1.0; q += 0.25 {
		for s := 0.0; s <= 1.0; s += 0.25 {
			w, ok := faceWeight(false, q, s)
			require.True(t, ok)
			assert.GreaterOrEqual(t, w, ipWeightMin-1e-9)
			assert.LessOrEqual(t, w, ipAutoWeightCeil+1e-9)
		}
	}
}

func TestWeightedCentroid(t *testing.T) {
	// 两个等权归一化向量 [1,0] 与 [0,1]：加权和 = [w,w]，归一化后 = [1/√2, 1/√2]。
	members := []profileMember{
		{faceID: 1, vec: []float32{1, 0}, weight: 1.0},
		{faceID: 2, vec: []float32{0, 1}, weight: 1.0},
	}
	centroid, sum, total, ok := weightedCentroid(members)
	require.True(t, ok)
	assert.InDelta(t, 1/math.Sqrt2, float64(centroid[0]), 1e-6)
	assert.InDelta(t, 1/math.Sqrt2, float64(centroid[1]), 1e-6)
	assert.InDelta(t, 1.0, float64(sum[0]), 1e-6) // 加权和保留原始值
	assert.InDelta(t, 1.0, float64(sum[1]), 1e-6)
	assert.Equal(t, 2.0, total)

	// 维度不一致 → 失败。
	mixed := []profileMember{
		{faceID: 1, vec: []float32{1, 0}, weight: 1.0},
		{faceID: 2, vec: []float32{1, 0, 0}, weight: 1.0},
	}
	_, _, _, ok = weightedCentroid(mixed)
	assert.False(t, ok)

	// 空输入 → 失败。
	_, _, _, ok = weightedCentroid(nil)
	assert.False(t, ok)

	// 加权和为零 → 失败。
	zero := []profileMember{{faceID: 1, vec: []float32{1, 0}, weight: 0}}
	_, _, _, ok = weightedCentroid(zero)
	assert.False(t, ok)
}

func TestWeightedCentroid_StableFloatOrder(t *testing.T) {
	// 改变成员输入顺序（faceID 不同），结果应一致（按 faceID 内部排序累加）。
	m1 := []profileMember{
		{faceID: 1, vec: []float32{1, 0}, weight: 1.0},
		{faceID: 2, vec: []float32{0, 1}, weight: 2.0},
	}
	m2 := []profileMember{
		{faceID: 2, vec: []float32{0, 1}, weight: 2.0},
		{faceID: 1, vec: []float32{1, 0}, weight: 1.0},
	}
	c1, s1, t1, ok := weightedCentroid(m1)
	require.True(t, ok)
	c2, s2, t2, ok := weightedCentroid(m2)
	require.True(t, ok)
	assert.Equal(t, t1, t2)
	assert.Equal(t, s1, s2)
	assert.Equal(t, c1, c2)
}

func TestCenterMedoid(t *testing.T) {
	centroid := []float32{1, 0, 0, 0}
	// faceID 2 最接近质心。
	members := []profileMember{
		{faceID: 1, vec: []float32{0, 1, 0, 0}},
		{faceID: 2, vec: []float32{0.999, 0.01, 0, 0}},
		{faceID: 3, vec: []float32{0, 0, 1, 0}},
	}
	assert.Equal(t, uint(2), centerMedoid(centroid, members))

	// 同分时取最小 faceID。
	centroid2 := []float32{1, 0}
	tie := []profileMember{
		{faceID: 5, vec: []float32{1, 0}},
		{faceID: 3, vec: []float32{1, 0}},
	}
	assert.Equal(t, uint(3), centerMedoid(centroid2, tie))

	// 空输入。
	assert.Equal(t, uint(0), centerMedoid(nil, nil))
}

func TestPercentileSimilarity(t *testing.T) {
	vals := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	// nearest-rank：P10 → rank=ceil(0.5)=1 → 0.1；P50 → rank=ceil(2.5)=3 → 0.3。
	assert.InDelta(t, 0.1, percentileSimilarity(vals, 10), 1e-9)
	assert.InDelta(t, 0.3, percentileSimilarity(vals, 50), 1e-9)

	// 不修改原 slice（验证拷贝：原顺序保持）。
	assert.Equal(t, []float64{0.1, 0.2, 0.3, 0.4, 0.5}, vals)

	// 空输入 → 0。
	assert.Equal(t, 0.0, percentileSimilarity(nil, 50))

	// NaN/Inf 被过滤。
	withBad := []float64{0.2, math.NaN(), math.Inf(1), 0.4}
	assert.InDelta(t, 0.2, percentileSimilarity(withBad, 10), 1e-9)

	// p 钳制。
	assert.InDelta(t, 0.1, percentileSimilarity(vals, 0), 1e-9)
	assert.InDelta(t, 0.5, percentileSimilarity(vals, 100), 1e-9)
}

func TestCentersMergeable(t *testing.T) {
	b := NewIdentityProfileBuilder(testBuilderConfig())
	// 两个单成员中心，质心高度相似 → 可合并。
	c1 := &profileCenter{centroid: vA, members: []int{0}}
	c2 := &profileCenter{centroid: vA2, members: []int{1}}
	members := []profileMember{
		{faceID: 1, vec: vA, weight: 1.0},
		{faceID: 2, vec: vA2, weight: 1.0},
	}
	assert.True(t, b.centersMergeable(c1, c2, members))

	// 质心正交 → 不可合并。
	c3 := &profileCenter{centroid: vB, members: []int{1}}
	members[1] = profileMember{faceID: 2, vec: vB, weight: 1.0}
	assert.False(t, b.centersMergeable(c1, c3, members))
}

func TestCentersMergeable_DistributionTooWide(t *testing.T) {
	// 两个多成员中心：质心相似（≥ MergeThreshold），但合并后某成员离合并质心过远
	// （< AssignmentThreshold）→ 不得合并。
	b := NewIdentityProfileBuilder(testBuilderConfig())
	// c1: 两个成员都近似 vA，质心 ≈ vA。
	c1 := &profileCenter{
		centroid: vA,
		members:  []int{0, 1},
	}
	// c2: 质心也接近 vA（用一个 vA 成员表示），但含一个远离成员 vB。
	c2 := &profileCenter{
		centroid: vA,
		members:  []int{2, 3},
	}
	members := []profileMember{
		{faceID: 1, vec: vA, weight: 1.0},
		{faceID: 2, vec: vA, weight: 1.0},
		{faceID: 3, vec: vA, weight: 1.0},
		{faceID: 4, vec: vB, weight: 1.0}, // 远离合并质心
	}
	// 质心相似度 = 1 ≥ MergeThreshold，但合并后 vB 成员 sim≈0 < AssignmentThreshold。
	assert.False(t, b.centersMergeable(c1, c2, members))
}

func TestMembershipSignature(t *testing.T) {
	// 同一分组、中心位于切片不同位置（centerIdx 同步重排）→ 签名应一致。
	members := []profileMember{
		{faceID: 1, vec: vA, centerIdx: 0},
		{faceID: 2, vec: vA, centerIdx: 0},
		{faceID: 3, vec: vB, centerIdx: -1},
	}
	centers := []*profileCenter{
		{members: []int{0, 1}},
	}
	s1 := membershipSignature(members, centers)

	// 中心位于切片索引 2，成员 centerIdx 同步为 2。
	dummy := &profileCenter{}
	members2 := []profileMember{
		{faceID: 1, vec: vA, centerIdx: 2},
		{faceID: 2, vec: vA, centerIdx: 2},
		{faceID: 3, vec: vB, centerIdx: -1},
	}
	centers2 := []*profileCenter{dummy, dummy, {members: []int{0, 1}}}
	s2 := membershipSignature(members2, centers2)
	assert.Equal(t, s1, s2)
}
