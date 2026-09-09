package service

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestSelectPersonIDsByRecallRank_KeepsHighIDRelevantCandidates(t *testing.T) {
	// 模拟：低 ID 噪声占满前 200 名（若按 ID 截断会挤掉高相关高 ID）。
	hits := make([]personRecallHit, 0, 220)
	for id := uint(1); id <= 200; id++ {
		hits = append(hits, personRecallHit{personID: id, minRank: 40, hitCount: 1})
	}
	// 高相关、高 ID：应保留。
	hits = append(hits, personRecallHit{personID: 265274, minRank: 0, hitCount: 3})
	hits = append(hits, personRecallHit{personID: 285426, minRank: 1, hitCount: 2})
	hits = append(hits, personRecallHit{personID: 382656, minRank: 2, hitCount: 2})

	got := selectPersonIDsByRecallRank(hits, 200, 0, 0)
	require.Len(t, got, 200)

	kept := make(map[uint]struct{}, len(got))
	for _, id := range got {
		kept[id] = struct{}{}
	}
	require.Contains(t, kept, uint(265274))
	require.Contains(t, kept, uint(285426))
	require.Contains(t, kept, uint(382656))
	// 低相关低 ID 应被挤出一部分。
	require.NotContains(t, kept, uint(200))
}

func TestSelectPersonIDsByRecallRank_IDOnlyBreaksTies(t *testing.T) {
	hits := []personRecallHit{
		{personID: 30, minRank: 1, hitCount: 2},
		{personID: 10, minRank: 1, hitCount: 2},
		{personID: 20, minRank: 1, hitCount: 2},
	}
	got := selectPersonIDsByRecallRank(hits, 2, 0, 0)
	require.Equal(t, []uint{10, 20}, got)
}

func TestRecallPersonCandidates_ReadyIndexZeroHitsIsNoCandidateNotUnavailable(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	require.NoError(t, db.Model(target).Updates(map[string]interface{}{
		"category": model.PersonCategoryFamily, "face_count": 2, "hidden": false,
	}).Error)
	// 仅目标自身中心：Search 可能返回自己，过滤后零命中 → 必须 ready + no_candidate。
	centers := seedActiveProfile(t, db, target.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.4},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	engine := NewIdentityMatchingEngine(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		matcherCfg(),
	)

	got := engine.SimilarPeople([]uint{target.ID}, DefaultIdentityRecallOptions())
	res := got[target.ID]
	require.Equal(t, IdentityMatchStatusNoCandidate, res.Status,
		"ready index with zero other-person hits must be no_candidate, not unavailable")
	require.Empty(t, res.BlockReason)

	hits, ready := engine.recallPersonHits(IdentityEvidence{
		Kind: identityEvidenceKindPerson,
		Units: []IdentityEvidenceUnit{{
			PersonID: target.ID,
			Vector:   []float32{1, 0, 0},
			Weight:   1,
		}},
	}, target.ID, DefaultIdentityRecallOptions())
	require.True(t, ready)
	require.NotNil(t, hits)
	require.Empty(t, hits)
}

func TestRecallPersonHits_ExactBoostRecoversANNMiss(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	near := createMatcherPerson(t, db)
	require.NoError(t, db.Model(target).Updates(map[string]interface{}{
		"category": model.PersonCategoryFamily, "face_count": 2, "hidden": false,
	}).Error)
	require.NoError(t, db.Model(near).Updates(map[string]interface{}{
		"category": model.PersonCategoryFamily, "face_count": 2, "hidden": false,
	}).Error)

	// 目标与 near 同向；filler 挤满 ANN top-1。
	tCenters := seedActiveProfile(t, db, target.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.4},
	})
	nCenters := seedActiveProfile(t, db, near.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.98, 0.02, 0}, supportCount: 5, p10: 0.4},
	})
	var fillers []*model.PersonIdentityCenter
	for i := 0; i < 8; i++ {
		fp := createMatcherPerson(t, db)
		fillers = append(fillers, seedActiveProfile(t, db, fp.ID, "emb-v1", []centerSpec{
			{emb: []float32{0.99, float32(i+1) * 0.001, 0}, supportCount: 5, p10: 0.4},
		})...)
	}
	all := append([]*model.PersonIdentityCenter{}, tCenters...)
	all = append(all, nCenters...)
	all = append(all, fillers...)
	ann := buildMatcherANN(t, "emb-v1", all)
	engine := NewIdentityMatchingEngine(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		matcherCfg(),
	)
	ev := IdentityEvidence{
		Kind: identityEvidenceKindPerson,
		Units: []IdentityEvidenceUnit{{
			PersonID: target.ID,
			Vector:   []float32{1, 0, 0},
			Weight:   1,
		}},
	}

	annOnly, ready := engine.recallPersonHits(ev, target.ID, IdentityRecallOptions{ANNK: 1, ExactK: -1, MaxCandidates: 50})
	require.True(t, ready)
	annIDs := selectPersonIDsByRecallRank(annOnly, 50, 1, 0)
	require.NotContains(t, annIDs, near.ID, "ANNK=1 should miss near behind fillers")

	boosted, ready := engine.recallPersonHits(ev, target.ID, IdentityRecallOptions{ANNK: 1, ExactK: 20, MaxCandidates: 50})
	require.True(t, ready)
	boostIDs := selectPersonIDsByRecallRank(boosted, 50, 1, 20)
	require.Contains(t, boostIDs, near.ID, "ExactK must recover ANN-missed near neighbor")
}

func TestSelectPersonIDsByRecallRank_ReservesExactBoostAgainstANNFlood(t *testing.T) {
	hits := make([]personRecallHit, 0, 220)
	for id := uint(1); id <= 200; id++ {
		hits = append(hits, personRecallHit{personID: id, minRank: int(id % 40), hitCount: 1})
	}
	// 精确补召：minRank >= annK=50
	hits = append(hits, personRecallHit{personID: 272927, minRank: 50 + 80, hitCount: 1})
	got := selectPersonIDsByRecallRank(hits, 200, 50, 100)
	require.Contains(t, got, uint(272927), "exact-boosted id must survive MaxCandidates flood via reserve")
}
