package service

import (
	"errors"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---- 测试 DB 与种子辅助 ----

func setupMatcherDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Photo{}, &model.Person{}, &model.Face{}, &model.CannotLinkConstraint{},
		&model.PersonIdentityProfile{}, &model.PersonIdentityCenter{}, &model.PersonIdentityCenterMember{},
	))
	return db
}

func closeMatcherDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
		_ = sqlDB.Close()
	}
}

type centerSpec struct {
	emb          []float32
	supportCount int
	p10          float64
	confirmed    bool
}

// seedActiveProfile 直接写入一条 ready profile（active_generation=1）与若干中心，
// 返回带真实主键的中心切片（同时用于 ANN Rebuild，保证 ANN 与数据库一致）。
func seedActiveProfile(t *testing.T, db *gorm.DB, personID uint, embeddingModel string, centers []centerSpec) []*model.PersonIdentityCenter {
	t.Helper()
	now := time.Now().UTC()
	prof := &model.PersonIdentityProfile{
		PersonID:         personID,
		Status:           model.PersonIdentityProfileStatusReady,
		ActiveGeneration: 1,
		NextGeneration:   2,
		EmbeddingModel:   embeddingModel,
		AlgorithmVersion: identityProfileAlgorithmVersion,
		UpdatedAt:        now,
		BuiltAt:          &now,
	}
	require.NoError(t, db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "person_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{"status": prof.Status, "active_generation": prof.ActiveGeneration, "embedding_model": prof.EmbeddingModel, "updated_at": now}),
	}).Create(prof).Error)

	out := make([]*model.PersonIdentityCenter, 0, len(centers))
	for i, cs := range centers {
		emb := model.EncodeEmbedding(cs.emb)
		c := &model.PersonIdentityCenter{
			PersonID:          personID,
			Generation:        1,
			Ordinal:           i + 1,
			CentroidEmbedding: emb,
			SumEmbedding:      emb,
			SupportCount:      cs.supportCount,
			SimilarityP10:     cs.p10,
			Confirmed:         cs.confirmed,
		}
		require.NoError(t, db.Create(c).Error)
		out = append(out, c)
	}
	return out
}

func createMatcherPerson(t *testing.T, db *gorm.DB) *model.Person {
	t.Helper()
	p := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, db.Create(p).Error)
	return p
}

func matcherCfg() IdentityProfileMatcherConfig {
	return IdentityProfileMatcherConfig{
		EmbeddingModel:  "emb-v1",
		RescueThreshold: 0.60,
		Margin:          0.05,
		MinCenterFaces:  3,
	}
}

// makeFace 构造查询人脸。
func makeFace(id, photoID uint, personID *uint, emb []float32, quality float64, manual bool, retry int) *model.Face {
	return &model.Face{
		ID:           id,
		PhotoID:      photoID,
		PersonID:     personID,
		Embedding:    model.EncodeEmbedding(emb),
		QualityScore: quality,
		ManualLocked: manual,
		RetryCount:   retry,
	}
}

func buildMatcherANN(t *testing.T, modelSig string, centers []*model.PersonIdentityCenter) *identityProfileANN {
	t.Helper()
	ann := newIdentityProfileANN(modelSig)
	require.NoError(t, ann.Rebuild(centers, modelSig))
	return ann
}

// ---- 纯聚合函数测试 ----

func TestIdentityProfileMatcher_AggregateSingleFace(t *testing.T) {
	items := []aggregateInput{{value: 0.9, weight: 0.5, faceID: 1}}
	score, contrib := aggregateWeighted(items)
	assert.InDelta(t, 0.9, score, 1e-9)
	assert.Equal(t, []int{0}, contrib)
}

func TestIdentityProfileMatcher_AggregateWeightedMedian(t *testing.T) {
	// 高权重脸的值应被选中。
	items := []aggregateInput{
		{value: 0.8, weight: 0.9, faceID: 1},
		{value: 0.6, weight: 0.1, faceID: 2},
	}
	score, _ := aggregateWeighted(items)
	assert.InDelta(t, 0.8, score, 1e-9)

	// 三脸中位数。
	items3 := []aggregateInput{
		{value: 0.9, weight: 0.2, faceID: 3},
		{value: 0.7, weight: 0.5, faceID: 1},
		{value: 0.8, weight: 0.3, faceID: 2},
	}
	score3, _ := aggregateWeighted(items3)
	assert.InDelta(t, 0.7, score3, 1e-9)
}

func TestIdentityProfileMatcher_AggregateTrimmedMean(t *testing.T) {
	items := []aggregateInput{
		{value: 0.5, weight: 1.0, faceID: 1},
		{value: 0.6, weight: 1.0, faceID: 2},
		{value: 0.7, weight: 1.0, faceID: 3},
		{value: 0.8, weight: 1.0, faceID: 4},
		{value: 0.9, weight: 1.0, faceID: 5},
	}
	score, _ := aggregateWeighted(items)
	// kept = [0.5,1,1,1,0.5], sumW=4, sumWV=2.8 → 2.8/4 = 0.7
	assert.InDelta(t, 2.8/4.0, score, 1e-9)
}

func TestIdentityProfileMatcher_AggregateLowQualityOutlierDoesNotDominate(t *testing.T) {
	// 一个低质量离群脸（权重 0.05、值 0.1）不应把分数拉低。
	items := []aggregateInput{
		{value: 0.1, weight: 0.05, faceID: 1},
		{value: 0.9, weight: 1.0, faceID: 2},
		{value: 0.9, weight: 1.0, faceID: 3},
		{value: 0.9, weight: 1.0, faceID: 4},
		{value: 0.9, weight: 1.0, faceID: 5},
	}
	score, _ := aggregateWeighted(items)
	assert.InDelta(t, 0.9, score, 1e-9, "trimmed mean recovers majority value despite outlier")
}

func TestIdentityProfileMatcher_AggregateHighSimSingleDoesNotInflate(t *testing.T) {
	// 一个极高相似度脸不应把整体分数虚高到接近它。
	items := []aggregateInput{
		{value: 0.5, weight: 1.0, faceID: 1},
		{value: 0.5, weight: 1.0, faceID: 2},
		{value: 0.5, weight: 1.0, faceID: 3},
		{value: 0.5, weight: 1.0, faceID: 4},
		{value: 0.99, weight: 1.0, faceID: 5},
	}
	score, _ := aggregateWeighted(items)
	assert.Less(t, score, 0.99)
	assert.Greater(t, score, 0.5)
}

func TestIdentityProfileMatcher_AggregateDeterministicOrderInvariance(t *testing.T) {
	base := []aggregateInput{
		{value: 0.82, weight: 0.3, faceID: 3},
		{value: 0.71, weight: 0.6, faceID: 1},
		{value: 0.65, weight: 0.2, faceID: 5},
		{value: 0.90, weight: 0.4, faceID: 2},
		{value: 0.77, weight: 0.5, faceID: 4},
		{value: 0.88, weight: 0.35, faceID: 6},
	}
	scoreA, _ := aggregateWeighted(base)

	// 多种乱序输入应得到相同结果。
	for _, perm := range [][]int{{5, 4, 3, 2, 1, 0}, {1, 0, 2, 3, 5, 4}, {3, 1, 5, 0, 4, 2}} {
		shuffled := make([]aggregateInput, len(base))
		for i, j := range perm {
			shuffled[i] = base[j]
		}
		scoreB, _ := aggregateWeighted(shuffled)
		assert.InDelta(t, scoreA, scoreB, 1e-12, "order must not affect aggregate")
	}
}

func TestIdentityProfileMatcher_AggregateTiebreakFaceID(t *testing.T) {
	// 同值同权重时，加权中位数选择较小 faceID 对应的值（已由稳定排序保证）。
	items := []aggregateInput{
		{value: 0.8, weight: 0.5, faceID: 5},
		{value: 0.8, weight: 0.5, faceID: 1},
	}
	score, _ := aggregateWeighted(items)
	assert.InDelta(t, 0.8, score, 1e-9)
	// 排序后 faceID 1 在前；cum=0.5>=half(0.5) 命中 faceID 1，结果仍 0.8（值相同，仅验证确定性）。
}

// ---- 清洗与 RetryCount 不变性 ----

func TestIdentityProfileMatcher_InvalidQueryFailClosed(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	ann := buildMatcherANN(t, "emb-v1", nil)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	// 全部无效人脸：nil、空 embedding、NaN、Inf、零范数、负质量。
	cases := []*model.Face{
		nil,
		{ID: 1, PhotoID: 1, Embedding: nil, QualityScore: 0.9},
		{ID: 2, PhotoID: 1, Embedding: model.EncodeEmbedding([]float32{float32(math.NaN()), 0, 0}), QualityScore: 0.9},
		{ID: 3, PhotoID: 1, Embedding: model.EncodeEmbedding([]float32{0, 0, 0}), QualityScore: 0.9},
		{ID: 4, PhotoID: 1, Embedding: model.EncodeEmbedding([]float32{float32(math.Inf(1)), 0, 0}), QualityScore: 0.9},
		{ID: 5, PhotoID: 1, Embedding: model.EncodeEmbedding([]float32{1, 0, 0}), QualityScore: -1}, // 负质量
	}
	res := m.Match(cases)
	assert.False(t, res.Available)
	assert.Equal(t, blockInvalidQuery, res.BlockReason)
}

func TestIdentityProfileMatcher_RetryCountInvariance(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	pa := createMatcherPerson(t, db)
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	emb := []float32{0.99, 0.01, 0}
	r1 := m.Match([]*model.Face{makeFace(10, 1, nil, emb, 0.9, false, 0)})
	r2 := m.Match([]*model.Face{makeFace(10, 1, nil, emb, 0.9, false, 7)})
	assert.Equal(t, r1, r2, "RetryCount must not affect match result")
	assert.True(t, r1.AutoEligible)
}

// ---- 端到端召回与精确评分 ----

func TestIdentityProfileMatcher_RecallAndExactScoring(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	pa := createMatcherPerson(t, db)
	pb := createMatcherPerson(t, db)
	// A 的次级中心（非主外观）也应被 ANN 召回。
	centersA := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
		{emb: []float32{0.9, 0.1, 0}, supportCount: 4, p10: 0.5},
	})
	centersB := seedActiveProfile(t, db, pb.ID, "emb-v1", []centerSpec{
		{emb: []float32{0, 1, 0}, supportCount: 5, p10: 0.5},
	})
	allCenters := append(append([]*model.PersonIdentityCenter{}, centersA...), centersB...)
	ann := buildMatcherANN(t, "emb-v1", allCenters)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.Equal(t, pa.ID, res.PersonID, "best person is A")
	assert.True(t, res.AutoEligible)
	assert.NotEmpty(t, res.CenterIDs)
	// 最终分数来自数据库活动中心精确评分（≈0.99995），非 ANN 距离。
	assert.Greater(t, res.Score, 0.99)
	assert.Less(t, res.Score, 1.0+1e-6)
	// 同一人物多个中心只产生一个候选人物；次佳为 B。
	assert.Equal(t, pb.ID, res.SecondPersonID)
}

func TestIdentityProfileMatcher_ANNNotReadyUnavailable(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	ann := newIdentityProfileANN("emb-v1") // 未 Rebuild → snapshot nil
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{1, 0, 0}, 0.9, false, 0)})
	assert.False(t, res.Available)
	assert.Equal(t, blockIndexUnavailable, res.BlockReason)
}

func TestIdentityProfileMatcher_StaleCandidateBecomesUnavailable(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	pa := createMatcherPerson(t, db)
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	// ANN 已含 A，但人物被删除 → ListActiveCentersByPersonIDs 返回空 → unavailable。
	require.NoError(t, db.Delete(&model.Person{}, pa.ID).Error)

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0)})
	assert.False(t, res.Available)
	assert.Equal(t, blockProfileUnavailable, res.BlockReason)
	assert.True(t, ann.RebuildRequested(), "stale index should request rebuild")
}

func TestIdentityProfileMatcher_NoCandidates(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	// 空 snapshot（ready 但无中心）→ ANN 召回空 → Available=true, PersonID=0。
	ann := buildMatcherANN(t, "emb-v1", nil)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{1, 0, 0}, 0.9, false, 0)})
	assert.True(t, res.Available)
	assert.Equal(t, uint(0), res.PersonID)
	assert.False(t, res.AutoEligible)
}

// ---- 自动资格护栏 ----

func TestIdentityProfileMatcher_EligibleWhenAllGuardsPass(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.True(t, res.AutoEligible, "all guards pass")
	assert.Empty(t, res.BlockReason)
}

func TestIdentityProfileMatcher_ScoreBelowThreshold(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	// A 中心与查询夹角较大 → 分数低于 rescueThreshold(0.6)。
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.7, 0.7, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	cfg := matcherCfg()
	cfg.RescueThreshold = 0.8
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), cfg)

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{1, 0, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.False(t, res.AutoEligible)
	assert.Equal(t, blockScoreBelowThreshold, res.BlockReason)
	assert.Equal(t, pa.ID, res.PersonID, "candidate preserved for shadow")
}

func TestIdentityProfileMatcher_MarginTooSmall(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	pb := createMatcherPerson(t, db)
	centersA := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
	})
	centersB := seedActiveProfile(t, db, pb.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.98, 0.02, 0}, supportCount: 5, p10: 0.5},
	})
	all := append(append([]*model.PersonIdentityCenter{}, centersA...), centersB...)
	ann := buildMatcherANN(t, "emb-v1", all)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{1, 0, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.False(t, res.AutoEligible)
	assert.Equal(t, blockMarginTooSmall, res.BlockReason)
	assert.Less(t, res.Margin, matcherCfg().Margin)
}

func TestIdentityProfileMatcher_BelowCenterBoundary(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	// score ≈ 0.9939，p10 设为 0.995 → score < boundary。
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.9, 0.1, 0}, supportCount: 5, p10: 0.995},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{1, 0, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.False(t, res.AutoEligible)
	assert.Equal(t, blockBelowCenterBoundary, res.BlockReason)
}

func TestIdentityProfileMatcher_ManualSingletonRecallOnly(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	// Confirmed 但 supportCount=1 < MinCenterFaces(3) → manual singleton，仅召回不可自动聚合。
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 1, p10: 0.5, confirmed: true},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.Equal(t, pa.ID, res.PersonID, "recalled for shadow/suggestion")
	assert.False(t, res.AutoEligible, "manual singleton cannot auto-merge")
	assert.Equal(t, blockUnstableCenter, res.BlockReason)
}

func TestIdentityProfileMatcher_CannotLinkBlock(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	psrc := createMatcherPerson(t, db) // 查询脸来源人物
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	clRepo := repository.NewCannotLinkRepository(db)
	require.NoError(t, clRepo.Create(psrc.ID, pa.ID))
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), clRepo, matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, &psrc.ID, []float32{0.99, 0.01, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.Equal(t, pa.ID, res.PersonID, "candidate preserved")
	assert.False(t, res.AutoEligible)
	assert.Equal(t, blockCannotLink, res.BlockReason)
}

func TestIdentityProfileMatcher_SamePhotoCooccurrenceBlock(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	faceRepo := repository.NewFaceRepository(db)
	// 在 photo 1 中已存在一张属于 A 的人脸（与查询脸同照片共现）。
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: 1, PersonID: &pa.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		faceRepo, repository.NewCannotLinkRepository(db), matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.Equal(t, pa.ID, res.PersonID, "candidate preserved for manual suggestion")
	assert.False(t, res.AutoEligible)
	assert.Equal(t, blockSamePhotoCooccurrence, res.BlockReason)
}

func TestIdentityProfileMatcher_BestBlockedDoesNotSelectSecond(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	pb := createMatcherPerson(t, db)
	psrc := createMatcherPerson(t, db)
	centersA := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	centersB := seedActiveProfile(t, db, pb.ID, "emb-v1", []centerSpec{
		{emb: []float32{0, 1, 0}, supportCount: 5, p10: 0.5},
	})
	all := append(append([]*model.PersonIdentityCenter{}, centersA...), centersB...)
	ann := buildMatcherANN(t, "emb-v1", all)
	clRepo := repository.NewCannotLinkRepository(db)
	require.NoError(t, clRepo.Create(psrc.ID, pa.ID)) // 阻断最佳 A
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), clRepo, matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, &psrc.ID, []float32{0.99, 0.01, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.Equal(t, pa.ID, res.PersonID, "best candidate kept, not replaced by second")
	assert.False(t, res.AutoEligible)
	assert.Equal(t, blockCannotLink, res.BlockReason)
	assert.Equal(t, pb.ID, res.SecondPersonID)
}

// ---- 负证据查询失败 fake ----

type errCannotLinkRepo struct{ err error }

func (r *errCannotLinkRepo) ListByPersonID(uint) ([]uint, error) { return nil, r.err }

type errFaceRepo struct{ err error }

func (r *errFaceRepo) ListPersonIDsCooccurringWithPhotos(_, _ []uint) ([]uint, error) {
	return nil, r.err
}

func TestIdentityProfileMatcher_NegativeEvidenceUnavailableFailClosed(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	psrc := createMatcherPerson(t, db)
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)

	// cannot-link 查询失败 → fail closed。
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), &errCannotLinkRepo{err: errors.New("db down")}, matcherCfg())
	res := m.Match([]*model.Face{makeFace(10, 1, &psrc.ID, []float32{0.99, 0.01, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.False(t, res.AutoEligible)
	assert.Equal(t, blockNegativeEvidenceUnavail, res.BlockReason)

	// 同照片共现查询失败 → fail closed。
	m2 := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		&errFaceRepo{err: errors.New("db down")}, repository.NewCannotLinkRepository(db), matcherCfg())
	res2 := m2.Match([]*model.Face{makeFace(10, 1, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0)})
	require.True(t, res2.Available)
	assert.False(t, res2.AutoEligible)
	assert.Equal(t, blockNegativeEvidenceUnavail, res2.BlockReason)
}

// ---- 批量加载（无 N+1）与候选截断 ----

type countingProfileRepo struct {
	inner         matcherProfileRepo
	calls         int
	lastCandCount int
}

func (c *countingProfileRepo) ListActiveCentersByPersonIDs(ids []uint, m string) (map[uint][]*model.PersonIdentityCenter, error) {
	c.calls++
	c.lastCandCount = len(ids)
	return c.inner.ListActiveCentersByPersonIDs(ids, m)
}

func TestIdentityProfileMatcher_BatchLoadNoNPlusOne(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	// 5 个候选人物，均被 ANN 召回。
	var allCenters []*model.PersonIdentityCenter
	for i := 0; i < 5; i++ {
		p := createMatcherPerson(t, db)
		cs := seedActiveProfile(t, db, p.ID, "emb-v1", []centerSpec{
			{emb: []float32{float32(1 - float64(i)*0.01), float32(float64(i) * 0.01), 0}, supportCount: 5, p10: 0.5},
		})
		allCenters = append(allCenters, cs...)
	}
	ann := buildMatcherANN(t, "emb-v1", allCenters)
	cp := &countingProfileRepo{inner: repository.NewPersonIdentityProfileRepository(db)}
	m := NewIdentityProfileMatcher(ann, cp, repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	res := m.Match([]*model.Face{makeFace(10, 1, nil, []float32{1, 0, 0}, 0.9, false, 0)})
	require.True(t, res.Available)
	assert.Equal(t, 1, cp.calls, "centers loaded in a single batch call, not per-candidate")
	assert.Equal(t, 5, cp.lastCandCount)
}

func TestIdentityProfileMatcher_CandidateTruncationAt200(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	// 250 个人物中心均匀分布在单位圆上。首分量固定 0.5（其二进制 LSB 为 0x00），
	// 避免 model.DecodeEmbedding 对首字节恰为 '[' 的向量误判为 JSON 的既有边界问题。
	var allCenters []*model.PersonIdentityCenter
	for i := 0; i < 250; i++ {
		p := createMatcherPerson(t, db)
		ang := 2 * math.Pi * float64(i) / 250
		emb := []float32{0.5, float32(math.Cos(ang)), float32(math.Sin(ang))}
		cs := seedActiveProfile(t, db, p.ID, "emb-v1", []centerSpec{
			{emb: emb, supportCount: 5, p10: 0.5},
		})
		allCenters = append(allCenters, cs...)
	}
	ann := buildMatcherANN(t, "emb-v1", allCenters)
	cp := &countingProfileRepo{inner: repository.NewPersonIdentityProfileRepository(db)}
	m := NewIdentityProfileMatcher(ann, cp, repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	// 5 张查询脸均匀分布，每张召回 50 个候选，并集 > 200 → 稳定截断到 200。
	queryFaces := make([]*model.Face, 0, 5)
	for i := 0; i < 5; i++ {
		ang := 2 * math.Pi * float64(i) / 5
		queryFaces = append(queryFaces, makeFace(uint(100+i), 1, nil,
			[]float32{0.5, float32(math.Cos(ang)), float32(math.Sin(ang))}, 0.9, false, 0))
	}
	res := m.Match(queryFaces)
	require.True(t, res.Available)
	assert.Equal(t, identityProfileMatcherMaxCandidates, cp.lastCandCount, "candidates truncated to 200")
	_ = res
}

func TestIdentityProfileMatcher_DimMismatchIgnored(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	// 一张有效 dim-3 脸 + 一张 dim-2 脸（应被忽略），结果应正常。
	res := m.Match([]*model.Face{
		makeFace(10, 1, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0),
		{ID: 11, PhotoID: 1, Embedding: model.EncodeEmbedding([]float32{1, 0}), QualityScore: 0.9},
	})
	require.True(t, res.Available)
	assert.Equal(t, pa.ID, res.PersonID)
	assert.True(t, res.AutoEligible)
}

// ---- 多脸聚合与确定性 ----

func TestIdentityProfileMatcher_MultiFaceDeterministic(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	pa := createMatcherPerson(t, db)
	centers := seedActiveProfile(t, db, pa.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	m := NewIdentityProfileMatcher(ann, repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), matcherCfg())

	faces := []*model.Face{
		makeFace(11, 1, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0),
		makeFace(12, 1, nil, []float32{0.97, 0.02, 0}, 0.7, false, 0),
		makeFace(13, 1, nil, []float32{0.98, 0.015, 0}, 0.5, false, 0),
	}
	// 乱序两次结果一致。
	res1 := m.Match(append([]*model.Face{}, faces...))
	sort.SliceStable(faces, func(i, j int) bool { return faces[i].ID > faces[j].ID })
	res2 := m.Match(faces)
	assert.Equal(t, res1, res2)
	assert.True(t, res1.AutoEligible)
}
