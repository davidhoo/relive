package service

import (
	"errors"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// centerWithMedoid 描述一个带 medoid 的身份中心：质心向量、medoid 人脸向量、支持数与 P10。
type centerWithMedoid struct {
	centroid     []float32
	medoidEmb    []float32
	supportCount int
	p10          float64
	confirmed    bool
}

// seedProfileWithMedoids 写入一条 ready profile 与若干带 medoid 的中心，返回带真实主键的中心切片。
// 每个中心会创建一张属于该人物的 medoid 人脸（embedding=medoidEmb），并将 MedoidFaceID 指向它。
func seedProfileWithMedoids(t *testing.T, db *gorm.DB, personID uint, embeddingModel string, centers []centerWithMedoid) []*model.PersonIdentityCenter {
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
		// 创建 medoid 人脸，属于该人物。
		face := &model.Face{
			PhotoID:       uint(1000000 + int(personID)*100 + i),
			PersonID:      &personID,
			Embedding:     model.EncodeEmbedding(cs.medoidEmb),
			QualityScore:  0.9,
			ClusterStatus: model.FaceClusterStatusAssigned,
			ClusterScore:  1.0,
		}
		require.NoError(t, db.Create(face).Error)
		emb := model.EncodeEmbedding(cs.centroid)
		c := &model.PersonIdentityCenter{
			PersonID:          personID,
			Generation:        1,
			Ordinal:           i + 1,
			CentroidEmbedding: emb,
			SumEmbedding:      emb,
			MedoidFaceID:      &face.ID,
			SupportCount:      cs.supportCount,
			SimilarityP10:     cs.p10,
			Confirmed:         cs.confirmed,
		}
		require.NoError(t, db.Create(c).Error)
		out = append(out, c)
	}
	return out
}

func newProfileSimilarityProvider(t *testing.T, db *gorm.DB, modelSig string, allCenters []*model.PersonIdentityCenter) PersonProfileSimilarityProvider {
	t.Helper()
	ann := newIdentityProfileANN(modelSig)
	require.NoError(t, ann.Rebuild(allCenters, modelSig))
	return NewPersonProfileSimilarityProvider(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		modelSig,
	)
}

func TestPersonProfileSimilarity_RecallSecondaryCenterCandidate(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	// target 主中心远离候选，次级中心接近候选 → 次级中心应召回候选。
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
		{centroid: []float32{0, 1, 0}, medoidEmb: []float32{0, 1, 0}, supportCount: 4, p10: 0.5},
	})
	candCenters := seedProfileWithMedoids(t, db, cand.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{0, 0.99, 0.01}, medoidEmb: []float32{0, 0.99, 0.01}, supportCount: 5, p10: 0.5},
	})
	all := append(append([]*model.PersonIdentityCenter{}, targetCenters...), candCenters...)
	provider := newProfileSimilarityProvider(t, db, "emb-v1", all)

	got, ok := provider.SimilarPeople([]uint{target.ID}, 10)
	require.True(t, ok)
	require.Contains(t, got, target.ID)
	require.Len(t, got[target.ID], 1)
	assert.Equal(t, cand.ID, got[target.ID][0].PersonID)
	assert.Greater(t, got[target.ID][0].Score, 0.9, "中心精确分数应接近 1")
}

func TestPersonProfileSimilarity_DuplicateCandidateReturnedOnce(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	// target 两个中心都接近候选 → 候选只返回一次。
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{0.99, 0.01, 0}, medoidEmb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
		{centroid: []float32{0.98, 0.02, 0}, medoidEmb: []float32{0.98, 0.02, 0}, supportCount: 4, p10: 0.5},
	})
	candCenters := seedProfileWithMedoids(t, db, cand.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	all := append(append([]*model.PersonIdentityCenter{}, targetCenters...), candCenters...)
	provider := newProfileSimilarityProvider(t, db, "emb-v1", all)

	got, ok := provider.SimilarPeople([]uint{target.ID}, 10)
	require.True(t, ok)
	require.Len(t, got[target.ID], 1, "同一候选多中心命中只返回一次")
	assert.Equal(t, cand.ID, got[target.ID][0].PersonID)
}

func TestPersonProfileSimilarity_ModelMismatchFiltersCandidate(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	// 候选 profile 使用不同 embedding 模型 → ListActiveCentersByPersonIDs 不返回其中心 → 候选被丢弃。
	candCenters := seedProfileWithMedoids(t, db, cand.ID, "emb-v2", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	all := append(append([]*model.PersonIdentityCenter{}, targetCenters...), candCenters...)
	// ANN 以 emb-v1 构建；候选中心虽在 ANN 中（按向量），但数据库侧模型签名过滤会丢弃它。
	provider := newProfileSimilarityProvider(t, db, "emb-v1", all)

	got, ok := provider.SimilarPeople([]uint{target.ID}, 10)
	require.True(t, ok)
	require.NotContains(t, got, target.ID, "候选模型签名不匹配 → 无候选")
}

func TestPersonProfileSimilarity_ComparePeopleMedoidVerified(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	// 中心质心与 medoid 都高度相似 → finalScore = min ≈ 1。
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	candCenters := seedProfileWithMedoids(t, db, cand.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{0.99, 0.01, 0}, medoidEmb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
	})
	all := append(append([]*model.PersonIdentityCenter{}, targetCenters...), candCenters...)
	provider := newProfileSimilarityProvider(t, db, "emb-v1", all)

	got, ok := provider.ComparePeople([]PersonPair{{TargetID: target.ID, CandidateID: cand.ID}})
	require.True(t, ok)
	m := got[PersonPair{TargetID: target.ID, CandidateID: cand.ID}]
	assert.True(t, m.Available, "medoid 验证通过")
	assert.Greater(t, m.Score, 0.99)
}

func TestPersonProfileSimilarity_HighCenterLowMedoidUsesMin(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	// 中心质心高度相似，但 medoid 人脸正交 → finalScore = min(高, ~0) ≈ 0。
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	candCenters := seedProfileWithMedoids(t, db, cand.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{0.99, 0.01, 0}, medoidEmb: []float32{0, 1, 0}, supportCount: 5, p10: 0.5},
	})
	all := append(append([]*model.PersonIdentityCenter{}, targetCenters...), candCenters...)
	provider := newProfileSimilarityProvider(t, db, "emb-v1", all)

	got, ok := provider.ComparePeople([]PersonPair{{TargetID: target.ID, CandidateID: cand.ID}})
	require.True(t, ok)
	m := got[PersonPair{TargetID: target.ID, CandidateID: cand.ID}]
	assert.True(t, m.Available, "medoid 有效（仅分数低），仍返回")
	assert.Less(t, m.Score, 0.1, "finalScore=min(center,medoid)≈0，不生成高分画像建议")
}

func TestPersonProfileSimilarity_BestMedoidInvalidTriesNextCenter(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	// target 两个中心：c1 与候选最佳（质心最相似），但 c1 的 medoid 被删除（无效）；
	// c2 与候选次佳，medoid 有效 → 应退而使用 c2 的 medoid 验证。
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
		{centroid: []float32{0.9, 0.1, 0}, medoidEmb: []float32{0.9, 0.1, 0}, supportCount: 5, p10: 0.5},
	})
	candCenters := seedProfileWithMedoids(t, db, cand.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{0.99, 0.01, 0}, medoidEmb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
	})
	// 删除 target c1 的 medoid 人脸，使最佳中心对 medoid 无效。
	c1Medoid := targetCenters[0].MedoidFaceID
	require.NotNil(t, c1Medoid)
	require.NoError(t, db.Delete(&model.Face{}, *c1Medoid).Error)

	all := append(append([]*model.PersonIdentityCenter{}, targetCenters...), candCenters...)
	provider := newProfileSimilarityProvider(t, db, "emb-v1", all)

	got, ok := provider.ComparePeople([]PersonPair{{TargetID: target.ID, CandidateID: cand.ID}})
	require.True(t, ok)
	m := got[PersonPair{TargetID: target.ID, CandidateID: cand.ID}]
	assert.True(t, m.Available, "最佳中心 medoid 无效时尝试次佳中心并验证通过")
	assert.Greater(t, m.Score, 0.8)
}

func TestPersonProfileSimilarity_AllMedoidsInvalidReturnsUnavailable(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	candCenters := seedProfileWithMedoids(t, db, cand.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{0.99, 0.01, 0}, medoidEmb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
	})
	// 删除双方 medoid 人脸 → 所有中心对 medoid 均无效。
	require.NoError(t, db.Delete(&model.Face{}, *targetCenters[0].MedoidFaceID).Error)
	require.NoError(t, db.Delete(&model.Face{}, *candCenters[0].MedoidFaceID).Error)

	all := append(append([]*model.PersonIdentityCenter{}, targetCenters...), candCenters...)
	provider := newProfileSimilarityProvider(t, db, "emb-v1", all)

	got, ok := provider.ComparePeople([]PersonPair{{TargetID: target.ID, CandidateID: cand.ID}})
	require.True(t, ok)
	m := got[PersonPair{TargetID: target.ID, CandidateID: cand.ID}]
	assert.False(t, m.Available, "所有支持人脸无效 → 该对 unavailable，调用方回退 legacy")
}

func TestPersonProfileSimilarity_ANNNotReadyReturnsFalse(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	// 目标有活动中心，但 ANN 未 Rebuild → Search 返回 ready=false → 整批回退 legacy。
	target := createMatcherPerson(t, db)
	seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := newIdentityProfileANN("emb-v1")
	provider := NewPersonProfileSimilarityProvider(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		"emb-v1",
	)
	got, ok := provider.SimilarPeople([]uint{target.ID}, 10)
	assert.False(t, ok, "ANN 未 ready → 整批回退 legacy")
	assert.Empty(t, got)
}

func TestPersonProfileSimilarity_EmptyInputReturnsTrue(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	provider := newProfileSimilarityProvider(t, db, "emb-v1", nil)

	got, ok := provider.SimilarPeople(nil, 10)
	require.True(t, ok)
	assert.Empty(t, got)

	cg, cok := provider.ComparePeople(nil)
	require.True(t, cok)
	assert.Empty(t, cg)
}

func TestPersonProfileSimilarity_OrderInvariance(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	c1 := createMatcherPerson(t, db)
	c2 := createMatcherPerson(t, db)
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	c1Centers := seedProfileWithMedoids(t, db, c1.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{0.99, 0.01, 0}, medoidEmb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
	})
	c2Centers := seedProfileWithMedoids(t, db, c2.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{0.97, 0.03, 0}, medoidEmb: []float32{0.97, 0.03, 0}, supportCount: 5, p10: 0.5},
	})
	all := append(append(append([]*model.PersonIdentityCenter{}, targetCenters...), c1Centers...), c2Centers...)
	provider := newProfileSimilarityProvider(t, db, "emb-v1", all)

	got1, ok1 := provider.SimilarPeople([]uint{target.ID, c1.ID, c2.ID}, 10)
	got2, ok2 := provider.SimilarPeople([]uint{c2.ID, c1.ID, target.ID}, 10)
	require.True(t, ok1)
	require.True(t, ok2)
	assert.Equal(t, got1, got2, "输入顺序变化不影响结果")
}

func TestPersonProfileSimilarity_BatchNoNPlusOne(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	// 5 个候选人物，均被 ANN 召回。
	var allCenters []*model.PersonIdentityCenter
	allCenters = append(allCenters, targetCenters...)
	for i := 0; i < 5; i++ {
		c := createMatcherPerson(t, db)
		cs := seedProfileWithMedoids(t, db, c.ID, "emb-v1", []centerWithMedoid{
			{centroid: []float32{0.99, 0.01, 0}, medoidEmb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
		})
		allCenters = append(allCenters, cs...)
	}
	cp := &countingProfileRepo{inner: repository.NewPersonIdentityProfileRepository(db)}
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild(allCenters, "emb-v1"))
	provider := NewPersonProfileSimilarityProvider(ann, cp, repository.NewFaceRepository(db), "emb-v1")

	got, ok := provider.SimilarPeople([]uint{target.ID}, 10)
	require.True(t, ok)
	// 一次加载目标中心 + 一次加载候选中心 = 2 次（非逐候选 N+1）。
	assert.Equal(t, 2, cp.calls, "centers loaded in batch, not per-candidate")
	assert.NotEmpty(t, got[target.ID])
}

// ---- 失败 fake ----

type errProfileSimilarityFaceRepo struct{ err error }

func (r *errProfileSimilarityFaceRepo) ListByIDs([]uint) ([]*model.Face, error) {
	return nil, r.err
}

func TestPersonProfileSimilarity_ComparePeopleFaceRepoErrorReturnsFalse(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	targetCenters := seedProfileWithMedoids(t, db, target.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{1, 0, 0}, medoidEmb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	candCenters := seedProfileWithMedoids(t, db, cand.ID, "emb-v1", []centerWithMedoid{
		{centroid: []float32{0.99, 0.01, 0}, medoidEmb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
	})
	all := append(append([]*model.PersonIdentityCenter{}, targetCenters...), candCenters...)
	ann := newIdentityProfileANN("emb-v1")
	require.NoError(t, ann.Rebuild(all, "emb-v1"))
	provider := NewPersonProfileSimilarityProvider(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		&errProfileSimilarityFaceRepo{err: errors.New("db down")},
		"emb-v1",
	)

	got, ok := provider.ComparePeople([]PersonPair{{TargetID: target.ID, CandidateID: cand.ID}})
	assert.False(t, ok, "medoid 查询失败 → 整批回退 legacy")
	assert.Empty(t, got)
}
