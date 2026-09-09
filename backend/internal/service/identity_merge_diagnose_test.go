package service

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/require"
)

// TestDiagnoseMergeSuggestionTargets_FourPairFixture 用合成向量复现「两目标四候选」链路阶段：
// 三对可召回且策略接受；一对精确可接受但被召回截断挤出（模拟第四对漏召回形态）。
func TestDiagnoseMergeSuggestionTargets_FourPairFixture(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	targetA := createMatcherPerson(t, db)
	c1 := createMatcherPerson(t, db)
	c2 := createMatcherPerson(t, db)
	c3 := createMatcherPerson(t, db)
	targetB := createMatcherPerson(t, db)
	c4 := createMatcherPerson(t, db)
	for _, p := range []*model.Person{targetA, c1, c2, c3, targetB, c4} {
		require.NoError(t, db.Model(p).Updates(map[string]interface{}{
			"category": model.PersonCategoryFamily, "face_count": 5, "hidden": false,
		}).Error)
	}

	// 目标 A 中心；三候选近似（可召回）；另造大量无关中心占满截断窗口，使 c4 相关对在 B 上被挤出。
	taCenters := seedActiveProfile(t, db, targetA.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 8, p10: 0.4},
	})
	c1Centers := seedActiveProfile(t, db, c1.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.99, 0.01, 0}, supportCount: 8, p10: 0.4},
	})
	c2Centers := seedActiveProfile(t, db, c2.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.98, 0.02, 0}, supportCount: 8, p10: 0.4},
	})
	c3Centers := seedActiveProfile(t, db, c3.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.97, 0.03, 0}, supportCount: 8, p10: 0.4},
	})
	tbCenters := seedActiveProfile(t, db, targetB.ID, "emb-v1", []centerSpec{
		{emb: []float32{0, 1, 0}, supportCount: 8, p10: 0.4},
	})
	// c4 与 B 高度相似，但 ANN 查询会先撞上 filler 高 ID/近邻；用 MaxCandidates=2 强制截断。
	c4Centers := seedActiveProfile(t, db, c4.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.02, 0.98, 0}, supportCount: 8, p10: 0.4},
	})

	fillerCenters := make([]*model.PersonIdentityCenter, 0, 6)
	for i := 0; i < 3; i++ {
		fp := createMatcherPerson(t, db)
		require.NoError(t, db.Model(fp).Updates(map[string]interface{}{
			"category": model.PersonCategoryFamily, "face_count": 2, "hidden": false,
		}).Error)
		// 与 targetB 正交，占召回槽：向量略偏 x，Search 仍可能返回；主要靠 MaxCandidates 截断。
		fillerCenters = append(fillerCenters, seedActiveProfile(t, db, fp.ID, "emb-v1", []centerSpec{
			{emb: []float32{0.1, 0.9, float32(i) * 0.01}, supportCount: 5, p10: 0.4},
		})...)
	}

	all := append([]*model.PersonIdentityCenter{}, taCenters...)
	all = append(all, c1Centers...)
	all = append(all, c2Centers...)
	all = append(all, c3Centers...)
	all = append(all, tbCenters...)
	all = append(all, c4Centers...)
	all = append(all, fillerCenters...)
	ann := buildMatcherANN(t, "emb-v1", all)

	engine := NewIdentityMatchingEngine(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		IdentityProfileMatcherConfig{EmbeddingModel: "emb-v1", RescueThreshold: 0.65, Margin: 0.05, MinCenterFaces: 3},
	)
	people := repository.NewPersonRepository(db)
	strategy := NewIdentitySuggestStrategy(config.PeopleConfig{
		MergeSuggestionThreshold:      0.55,
		IdentityProfileMinCenterFaces: 3,
	})

	report, err := DiagnoseMergeSuggestionTargets(engine, people, MergeSuggestionDiagnoseRequest{
		TargetIDs: []uint{targetA.ID, targetB.ID},
		FocusCandidates: map[uint][]uint{
			targetA.ID: {c1.ID, c2.ID, c3.ID},
			targetB.ID: {c4.ID},
		},
		Strategy: strategy,
		Recall: IdentityRecallOptions{
			ANNK:          50,
			ExactK:        -1, // 本夹具只验证 ANN+截断丢召回，关闭精确补召
			MaxCandidates: 2, // 强制截断，制造「精确可过但召回丢失」
			TopK:          10,
		},
	})
	require.NoError(t, err)
	require.Equal(t, 2, report.StageStats.EligibleTargets)
	require.Equal(t, 2, report.StageStats.ProfileReadyTargets)
	require.Len(t, report.FocusPairs, 4)

	acceptedExact := 0
	recalledOK := 0
	var pairB *MergeSuggestionFocusPairDiag
	for i := range report.FocusPairs {
		fp := &report.FocusPairs[i]
		if fp.StrategyAccepted {
			acceptedExact++
		}
		if fp.Recalled {
			recalledOK++
		}
		if fp.TargetID == targetB.ID && fp.CandidateID == c4.ID {
			pairB = fp
		}
	}
	require.Equal(t, 4, acceptedExact, "四对精确路径均应被推荐策略接受（阈值 0.55）")
	require.GreaterOrEqual(t, recalledOK, 1, "至少 targetA 侧应对有召回")
	require.NotNil(t, pairB)
	// 在 MaxCandidates=2 且存在更近 filler 时，c4 可能被截断或未进前 2；若未召回则 DropStage 必须是 recall*。
	if !pairB.Recalled {
		require.Contains(t, []string{"recall", "recall_truncated"}, pairB.DropStage)
		require.True(t, pairB.StrategyAccepted, "漏召回不得掩盖精确策略仍接受")
	}
}

func TestMarkPendingStaleReason_RetryExhausted(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	require.NoError(t, db.AutoMigrate(&model.PersonMergeSuggestion{}, &model.PersonMergeSuggestionItem{}))

	repo := repository.NewPersonMergeSuggestionRepository(db)
	require.NoError(t, repo.ReplacePendingForTarget(11, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 22, SimilarityScore: 0.7, Rank: 1, MatchSource: model.PersonMergeMatchSourceIdentityProfile},
	}))
	n, err := repo.MarkPendingStaleReason([]uint{11}, model.PersonMergeStaleReasonRetryExhausted)
	require.NoError(t, err)
	require.Equal(t, int64(1), n)

	pending, err := repo.FindPendingByTarget(11)
	require.NoError(t, err)
	require.NotNil(t, pending)
	require.Equal(t, model.PersonMergeSuggestionStatusPending, pending.Status)
	require.Equal(t, model.PersonMergeStaleReasonRetryExhausted, pending.StaleReason)
}

func TestApplySuggestion_RejectsStaleReason(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	require.NoError(t, db.AutoMigrate(&model.PersonMergeSuggestion{}, &model.PersonMergeSuggestionItem{}, &model.AppConfig{}))

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	require.NoError(t, db.Model(target).Updates(map[string]interface{}{"category": model.PersonCategoryFamily, "face_count": 2, "hidden": false}).Error)
	require.NoError(t, db.Model(cand).Updates(map[string]interface{}{"category": model.PersonCategoryFamily, "face_count": 2, "hidden": false}).Error)

	repos := repository.NewRepositories(db)
	svcIface := NewPersonMergeSuggestionService(db, repos.Photo, repos.Face, repos.Person, repos.PeopleJob, repos.CannotLink, repos.MergeSuggestion, NewConfigService(repos.Config), &config.Config{People: config.PeopleConfig{MergeSuggestionThreshold: 0.55}})
	svc := svcIface.(*personMergeSuggestionService)

	require.NoError(t, repos.MergeSuggestion.ReplacePendingForTarget(target.ID, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: cand.ID, SimilarityScore: 0.8, Rank: 1, MatchSource: model.PersonMergeMatchSourceIdentityProfile},
	}))
	pending, err := repos.MergeSuggestion.FindPendingByTarget(target.ID)
	require.NoError(t, err)
	require.NotNil(t, pending)
	_, err = repos.MergeSuggestion.MarkPendingStaleReason([]uint{target.ID}, model.PersonMergeStaleReasonRetryExhausted)
	require.NoError(t, err)

	err = svc.ApplySuggestion(pending.ID, []uint{cand.ID})
	require.Error(t, err)
	require.Contains(t, err.Error(), "needs revalidation")
	require.Contains(t, err.Error(), model.PersonMergeStaleReasonRetryExhausted)

	still, err := repos.MergeSuggestion.FindPendingByTarget(target.ID)
	require.NoError(t, err)
	require.NotNil(t, still)
	require.Equal(t, model.PersonMergeSuggestionStatusPending, still.Status)
}
