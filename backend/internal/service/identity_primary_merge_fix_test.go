package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIdentityMatchingEngine_MissingTargetProfileIsUnavailable(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	candCenters := seedActiveProfile(t, db, cand.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", candCenters)
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
	require.Equal(t, IdentityMatchStatusUnavailable, res.Status)
	require.Equal(t, blockProfileUnavailable, res.BlockReason)
}

func TestPrimaryAssignments_DoesNotEmptyReplaceUnavailableTargets(t *testing.T) {
	peopleCfg := config.PeopleConfig{
		MergeSuggestionThreshold:       0.55,
		IdentityProfileRescueThreshold: 0.65,
		IdentityProfileMargin:          0.05,
		IdentityProfileMinCenterFaces:  3,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
		IdentityProfileMode:            model.PeopleIdentityModePrimary,
	}
	svcIface, db, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, peopleCfg)
	svc := svcIface.(*personMergeSuggestionService)
	require.NoError(t, db.AutoMigrate(
		&model.PersonIdentityProfile{},
		&model.PersonIdentityCenter{},
		&model.PersonIdentityCenterMember{},
	))

	// 先写入一条 pending，模拟线上已有建议。
	require.NoError(t, repos.MergeSuggestion.ReplacePendingForTarget(42, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 99, SimilarityScore: 0.9, Rank: 1, MatchSource: model.PersonMergeMatchSourceIdentityProfile},
	}))
	before, total, err := repos.MergeSuggestion.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, before, 1)
	oldID := before[0].ID

	// ANN 未 Rebuild → Search ready=false → 目标 unavailable。
	ann := newIdentityProfileANN("emb-v1")
	engine := NewIdentityMatchingEngine(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		IdentityProfileMatcherConfig{EmbeddingModel: "emb-v1", RescueThreshold: 0.65, Margin: 0.05, MinCenterFaces: 3},
	)
	target := &model.Person{Category: model.PersonCategoryFamily, FaceCount: 1}
	require.NoError(t, db.Create(target).Error)
	_ = seedActiveProfile(t, db, target.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})

	svc.SetIdentityMatchingEngine(engine)
	svc.SetIdentityProfileMode(model.PeopleIdentityModePrimary)
	svc.SetIdentitySuggestStrategy(NewIdentitySuggestStrategy(peopleCfg))
	svc.SetIdentityConfigFingerprint(IdentityStrategyFingerprint(peopleCfg))

	// 模拟写路径不够：补一条真正走 RunBackgroundSlice 的跳过分支。
	assignments, err := svc.primaryAssignments([]*model.Person{target}, nil)
	require.NoError(t, err)
	_, ok := assignments[target.ID]
	require.False(t, ok, "unavailable target must be absent from assignments map")

	require.NoError(t, svc.MarkDirty("test-unavailable-skip"))
	// primary 路径仍会走 ensureANNIndex（prototype ANN）；目标只有 face_count，无 prototype 脸也可。
	require.NoError(t, svc.RunBackgroundSlice())

	after, total, err := repos.MergeSuggestion.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, after, 1)
	assert.Equal(t, oldID, after[0].ID, "RunBackgroundSlice must not empty-replace unavailable targets")
	require.NotEmpty(t, svc.state.RetryTargets)
	found := false
	for _, e := range svc.state.RetryTargets {
		if e.TargetID == target.ID {
			found = true
			assert.GreaterOrEqual(t, e.Attempts, 1)
		}
	}
	require.True(t, found)
	// 延期重试已登记；单次 slice 后 Dirty 可能仍为 true（cursor 未扫完），不得误删 pending。
	require.False(t, svc.state.RetryTargets[0].NextRetryAt.IsZero())
}

func TestIdentityMatchingEngine_AllCandidateEvidenceMissingIsUnavailable(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	targetCenters := seedActiveProfile(t, db, target.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	candCenters := seedActiveProfile(t, db, cand.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", append(targetCenters, candCenters...))
	// 重建后再破坏候选中心：ANN 仍能召回人物，证据加载会跳过非法向量。
	require.NoError(t, db.Model(&model.PersonIdentityCenter{}).Where("id = ?", candCenters[0].ID).
		Update("centroid_embedding", []byte{0xff, 0xfe, 0xfd}).Error)

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
	require.Equal(t, IdentityMatchStatusUnavailable, res.Status)
	require.Equal(t, blockProfileUnavailable, res.BlockReason)
}

func TestPrimaryEngine_PersistsRealProfileGenerations(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	require.NoError(t, db.AutoMigrate(&model.PersonMergeSuggestion{}, &model.PersonMergeSuggestionItem{}, &model.AppConfig{}))

	peopleCfg := config.PeopleConfig{
		MergeSuggestionThreshold:       0.55,
		IdentityProfileRescueThreshold: 0.65,
		IdentityProfileMargin:          0.05,
		IdentityProfileMinCenterFaces:  3,
		IdentityProfileMinCenterPhotos: 2,
		IdentityProfileMaxCenters:      6,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
		IdentityProfileMode:            model.PeopleIdentityModePrimary,
	}

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	require.NoError(t, db.Model(target).Updates(map[string]interface{}{
		"category": model.PersonCategoryFamily, "face_count": 5, "hidden": false,
	}).Error)
	require.NoError(t, db.Model(cand).Updates(map[string]interface{}{
		"category": model.PersonCategoryStranger, "face_count": 5, "hidden": false,
	}).Error)

	targetCenters := seedActiveProfile(t, db, target.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.4},
	})
	candCenters := seedActiveProfile(t, db, cand.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.98, 0.02, 0}, supportCount: 5, p10: 0.4},
	})
	require.Equal(t, 1, targetCenters[0].Generation)
	require.Equal(t, 1, candCenters[0].Generation)

	ann := buildMatcherANN(t, "emb-v1", append(targetCenters, candCenters...))
	engine := NewIdentityMatchingEngine(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		IdentityProfileMatcherConfig{EmbeddingModel: "emb-v1", RescueThreshold: 0.65, Margin: 0.05, MinCenterFaces: 3},
	)
	repos := repository.NewRepositories(db)
	configService := NewConfigService(repos.Config)
	svcIface := NewPersonMergeSuggestionService(
		db, repos.Photo, repos.Face, repos.Person, repos.PeopleJob, repos.CannotLink, repos.MergeSuggestion, configService,
		&config.Config{People: peopleCfg},
	)
	svc := svcIface.(*personMergeSuggestionService)
	svc.SetIdentityMatchingEngine(engine)
	svc.SetIdentityProfileMode(model.PeopleIdentityModePrimary)
	svc.SetIdentitySuggestStrategy(NewIdentitySuggestStrategy(peopleCfg))
	svc.SetIdentityConfigFingerprint(IdentityStrategyFingerprint(peopleCfg))

	assignments, err := svc.primaryAssignments([]*model.Person{target}, map[uint]map[uint]bool{})
	require.NoError(t, err)
	items := assignments[target.ID]
	require.NotEmpty(t, items)
	require.Equal(t, 1, items[0].TargetProfileGeneration)
	require.Equal(t, 1, items[0].CandidateProfileGeneration)
}

func TestPrimaryEngine_PersistsAcceptedSuggestionWithMetadata(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	require.NoError(t, db.AutoMigrate(&model.PersonMergeSuggestion{}, &model.PersonMergeSuggestionItem{}, &model.AppConfig{}))

	peopleCfg := config.PeopleConfig{
		MergeSuggestionThreshold:       0.55,
		IdentityProfileRescueThreshold: 0.65,
		IdentityProfileMargin:          0.05,
		IdentityProfileMinCenterFaces:  3,
		IdentityProfileMinCenterPhotos: 2,
		IdentityProfileMaxCenters:      6,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
		IdentityProfileMode:            model.PeopleIdentityModePrimary,
	}

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	require.NoError(t, db.Model(target).Updates(map[string]interface{}{
		"category": model.PersonCategoryFamily, "face_count": 5, "hidden": false,
	}).Error)
	require.NoError(t, db.Model(cand).Updates(map[string]interface{}{
		"category": model.PersonCategoryStranger, "face_count": 5, "hidden": false,
	}).Error)

	targetCenters := seedActiveProfile(t, db, target.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.4},
	})
	candCenters := seedActiveProfile(t, db, cand.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.98, 0.02, 0}, supportCount: 5, p10: 0.4},
	})
	all := append(targetCenters, candCenters...)
	ann := buildMatcherANN(t, "emb-v1", all)
	engine := NewIdentityMatchingEngine(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		IdentityProfileMatcherConfig{
			EmbeddingModel:  "emb-v1",
			RescueThreshold: 0.65,
			Margin:          0.05,
			MinCenterFaces:  3,
		},
	)

	repos := repository.NewRepositories(db)
	configService := NewConfigService(repos.Config)
	svcIface := NewPersonMergeSuggestionService(
		db, repos.Photo, repos.Face, repos.Person, repos.PeopleJob, repos.CannotLink, repos.MergeSuggestion, configService,
		&config.Config{People: peopleCfg},
	)
	svc := svcIface.(*personMergeSuggestionService)
	svc.SetIdentityMatchingEngine(engine)
	svc.SetIdentityProfileMode(model.PeopleIdentityModePrimary)
	svc.SetIdentitySuggestStrategy(NewIdentitySuggestStrategy(peopleCfg))
	fp := IdentityStrategyFingerprint(peopleCfg)
	svc.SetIdentityConfigFingerprint(fp)

	assignments, err := svc.primaryAssignments([]*model.Person{target}, map[uint]map[uint]bool{})
	require.NoError(t, err)
	items := assignments[target.ID]
	require.NotEmpty(t, items, "high-similarity candidate must become a suggestion item")
	require.Equal(t, cand.ID, items[0].CandidatePersonID)
	require.GreaterOrEqual(t, items[0].SimilarityScore, 0.55)
	require.Equal(t, model.PersonMergeMatchSourceIdentityProfile, items[0].MatchSource)
	require.Equal(t, identityEngineVersion, items[0].EngineVersion)
	require.Equal(t, identitySuggestStrategyVersion, items[0].StrategyVersion)
	require.Equal(t, fp, items[0].ConfigFingerprint)

	require.NoError(t, repos.MergeSuggestion.ReplacePendingForTarget(target.ID, target.Category, items))
	pending, total, err := repos.MergeSuggestion.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Len(t, pending, 1)
	assert.Equal(t, identityEngineVersion, pending[0].EngineVersion)
	assert.Equal(t, identitySuggestStrategyVersion, pending[0].StrategyVersion)
	assert.Equal(t, fp, pending[0].ConfigFingerprint)

	dbItems, err := repos.MergeSuggestion.GetItems(pending[0].ID, model.PersonMergeSuggestionItemStatusPending)
	require.NoError(t, err)
	require.Len(t, dbItems, 1)
	assert.Equal(t, cand.ID, dbItems[0].CandidatePersonID)
	assert.Equal(t, model.PersonMergeMatchSourceIdentityProfile, dbItems[0].MatchSource)
}

func TestListDueRetryTargets_DropsIneligibleIDs(t *testing.T) {
	peopleCfg := config.PeopleConfig{
		MergeSuggestionThreshold:       0.55,
		IdentityProfileRescueThreshold: 0.65,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
		IdentityProfileMode:            model.PeopleIdentityModePrimary,
	}
	svcIface, db, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, peopleCfg)
	svc := svcIface.(*personMergeSuggestionService)

	alive := &model.Person{Category: model.PersonCategoryFamily, FaceCount: 2}
	require.NoError(t, db.Create(alive).Error)
	hidden := &model.Person{Category: model.PersonCategoryFamily, FaceCount: 2, Hidden: true}
	require.NoError(t, db.Create(hidden).Error)
	// deletedID 不存在于 people 表
	deletedID := uint(999001)

	now := time.Now()
	svc.state.RetryTargets = []mergeSuggestionRetryEntry{
		{TargetID: alive.ID, Attempts: 1, NextRetryAt: now.Add(-time.Minute)},
		{TargetID: hidden.ID, Attempts: 1, NextRetryAt: now.Add(-time.Minute)},
		{TargetID: deletedID, Attempts: 1, NextRetryAt: now.Add(-time.Minute)},
	}

	targets, dropped, err := svc.listDueRetryTargets()
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, alive.ID, targets[0].ID)
	require.ElementsMatch(t, []uint{hidden.ID, deletedID}, dropped)

	dropSet := map[uint]struct{}{hidden.ID: {}, deletedID: {}}
	svc.state.RetryTargets = removeMergeSuggestionRetries(svc.state.RetryTargets, dropSet)
	due, remaining, _ := classifyMergeSuggestionRetries(svc.state.RetryTargets, now)
	require.Len(t, due, 1)
	require.Equal(t, alive.ID, due[0].TargetID)
	require.Len(t, remaining, 1)
	_ = repos
}

func TestRankPersonCandidates_PartialMissingEvidenceMarksIncomplete(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	good := createMatcherPerson(t, db)
	bad := createMatcherPerson(t, db)
	targetCenters := seedActiveProfile(t, db, target.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	goodCenters := seedActiveProfile(t, db, good.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.2, 0.98, 0}, supportCount: 5, p10: 0.5}, // 低分，低于推荐阈值
	})
	badCenters := seedActiveProfile(t, db, bad.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", append(append(targetCenters, goodCenters...), badCenters...))
	require.NoError(t, db.Model(&model.PersonIdentityCenter{}).Where("id = ?", badCenters[0].ID).
		Update("centroid_embedding", []byte{0xff, 0xfe, 0xfd}).Error)

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
	require.NotEqual(t, IdentityMatchStatusNoCandidate, res.Status,
		"partial missing evidence must not look like a clean complete miss")
	if res.Status != IdentityMatchStatusUnavailable {
		require.True(t, res.IncompleteEvidence, "usable candidates with skipped evidence must set IncompleteEvidence")
	}
}

func TestPrimaryAssignments_PartialIncompleteEvidenceNotEmptySuccess(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)
	require.NoError(t, db.AutoMigrate(&model.PersonMergeSuggestion{}, &model.PersonMergeSuggestionItem{}, &model.AppConfig{}))

	peopleCfg := config.PeopleConfig{
		MergeSuggestionThreshold:       0.55,
		IdentityProfileRescueThreshold: 0.65,
		IdentityProfileMargin:          0.05,
		IdentityProfileMinCenterFaces:  3,
		MergeSuggestionBatchSize:       10,
		IdentityProfileMode:            model.PeopleIdentityModePrimary,
	}
	target := createMatcherPerson(t, db)
	good := createMatcherPerson(t, db)
	bad := createMatcherPerson(t, db)
	require.NoError(t, db.Model(target).Updates(map[string]interface{}{"category": model.PersonCategoryFamily, "face_count": 2, "hidden": false}).Error)

	targetCenters := seedActiveProfile(t, db, target.ID, "emb-v1", []centerSpec{{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5}})
	goodCenters := seedActiveProfile(t, db, good.ID, "emb-v1", []centerSpec{{emb: []float32{0.2, 0.98, 0}, supportCount: 5, p10: 0.5}})
	badCenters := seedActiveProfile(t, db, bad.ID, "emb-v1", []centerSpec{{emb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5}})
	ann := buildMatcherANN(t, "emb-v1", append(append(targetCenters, goodCenters...), badCenters...))
	require.NoError(t, db.Model(&model.PersonIdentityCenter{}).Where("id = ?", badCenters[0].ID).
		Update("centroid_embedding", []byte{0xff, 0xfe, 0xfd}).Error)

	repos := repository.NewRepositories(db)
	configService := NewConfigService(repos.Config)
	svcIface := NewPersonMergeSuggestionService(db, repos.Photo, repos.Face, repos.Person, repos.PeopleJob, repos.CannotLink, repos.MergeSuggestion, configService, &config.Config{People: peopleCfg})
	svc := svcIface.(*personMergeSuggestionService)
	engine := NewIdentityMatchingEngine(ann, repository.NewPersonIdentityProfileRepository(db), repository.NewFaceRepository(db), repository.NewCannotLinkRepository(db), repository.NewFaceRepository(db), IdentityProfileMatcherConfig{EmbeddingModel: "emb-v1", RescueThreshold: 0.65, Margin: 0.05, MinCenterFaces: 3})
	svc.SetIdentityMatchingEngine(engine)
	svc.SetIdentityProfileMode(model.PeopleIdentityModePrimary)
	svc.SetIdentitySuggestStrategy(NewIdentitySuggestStrategy(peopleCfg))

	require.NoError(t, repos.MergeSuggestion.ReplacePendingForTarget(target.ID, model.PersonCategoryFamily, []model.PersonMergeSuggestionItem{
		{CandidatePersonID: 42, SimilarityScore: 0.9, Rank: 1, MatchSource: model.PersonMergeMatchSourceIdentityProfile},
	}))

	before, total, err := repos.MergeSuggestion.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	oldID := before[0].ID

	assignments, err := svc.primaryAssignments([]*model.Person{target}, map[uint]map[uint]bool{})
	require.NoError(t, err)
	_, ok := assignments[target.ID]
	// 低分候选 + 部分证据不可用：不得完整成功空替换；写路径缺 key 时跳过 ReplacePending。
	require.False(t, ok, "incomplete evidence must omit target from complete assignments")
	require.NotEmpty(t, svc.state.RetryTargets)
	after, total, err := repos.MergeSuggestion.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, oldID, after[0].ID, "existing pending must survive incomplete evidence")
}

func TestMatchComponent_UsesRealMinSupportCount(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	person := createMatcherPerson(t, db)
	centers := seedActiveProfile(t, db, person.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 7, p10: 0.4},
	})
	ann := buildMatcherANN(t, "emb-v1", centers)
	engine := NewIdentityMatchingEngine(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		IdentityProfileMatcherConfig{EmbeddingModel: "emb-v1", RescueThreshold: 0.60, Margin: 0.05, MinCenterFaces: 3},
	)

	face := makeFace(1, 10, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0)
	res := engine.MatchComponent([]*model.Face{face}, DefaultIdentityRecallOptions())
	require.NotNil(t, res.Best)
	require.Equal(t, person.ID, res.Best.PersonID)
	require.Equal(t, 7, res.Best.MinSupportCount, "must not hardcode MinSupportCount=1")
	require.True(t, res.Best.StableCenters)
}

func TestScoreComponentAgainstPerson_DoesNotRequireReverseCoverage(t *testing.T) {
	component := IdentityEvidence{
		Kind: identityEvidenceKindComponent,
		Units: []IdentityEvidenceUnit{{
			FaceID: 1, Vector: []float32{1, 0, 0}, Weight: 1,
		}},
	}
	person := IdentityEvidence{
		Kind:     identityEvidenceKindPerson,
		PersonID: 9,
		Units: []IdentityEvidenceUnit{
			{CenterID: 1, Vector: []float32{1, 0, 0}, Weight: 1, SupportCount: 5, SimilarityP10: 0.4},
			{CenterID: 2, Vector: []float32{0, 1, 0}, Weight: 1, SupportCount: 5, SimilarityP10: 0.4}, // 正交外观
		},
	}
	pair := ScoreComponentAgainstPerson(component, person)
	require.NotEqual(t, IdentityMatchStatusInvalid, pair.Status)
	require.Greater(t, pair.Score, 0.9)
	require.Equal(t, 5, pair.MinSupportCount)
	require.True(t, pair.SingleDirection)
	require.Zero(t, pair.ReverseScore, "component scoring must not fabricate reverse score")

	// 人物对双向取最小值会被正交中心拉低。
	personAsLeft := person
	bidir := ScoreIdentityEvidence(personAsLeft, IdentityEvidence{
		Kind:     identityEvidenceKindPerson,
		PersonID: 8,
		Units: []IdentityEvidenceUnit{
			{CenterID: 9, Vector: []float32{1, 0, 0}, Weight: 1, SupportCount: 5, SimilarityP10: 0.4},
		},
	})
	require.Less(t, bidir.Score, pair.Score)
}

func TestEngine_AllMissingEvidenceRequestsANNRebuild(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	target := createMatcherPerson(t, db)
	cand := createMatcherPerson(t, db)
	targetCenters := seedActiveProfile(t, db, target.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 5, p10: 0.5},
	})
	candCenters := seedActiveProfile(t, db, cand.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.99, 0.01, 0}, supportCount: 5, p10: 0.5},
	})
	ann := buildMatcherANN(t, "emb-v1", append(targetCenters, candCenters...))
	require.False(t, ann.RebuildRequested())
	require.NoError(t, db.Model(&model.PersonIdentityCenter{}).Where("id = ?", candCenters[0].ID).
		Update("centroid_embedding", []byte{0xff, 0xfe, 0xfd}).Error)

	engine := NewIdentityMatchingEngine(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		matcherCfg(),
	)
	got := engine.SimilarPeople([]uint{target.ID}, DefaultIdentityRecallOptions())
	require.Equal(t, IdentityMatchStatusUnavailable, got[target.ID].Status)
	require.True(t, ann.RebuildRequested(), "stale ANN with empty evidence must request rebuild")
}

func TestDecidePrimaryComponent_IncompleteEvidenceWaits(t *testing.T) {
	db := setupMatcherDB(t)
	defer closeMatcherDB(t, db)

	person := createMatcherPerson(t, db)
	bad := createMatcherPerson(t, db)
	centers := seedActiveProfile(t, db, person.ID, "emb-v1", []centerSpec{
		{emb: []float32{1, 0, 0}, supportCount: 7, p10: 0.4},
	})
	badCenters := seedActiveProfile(t, db, bad.ID, "emb-v1", []centerSpec{
		{emb: []float32{0.98, 0.02, 0}, supportCount: 7, p10: 0.4},
	})
	ann := buildMatcherANN(t, "emb-v1", append(centers, badCenters...))
	require.NoError(t, db.Model(&model.PersonIdentityCenter{}).Where("id = ?", badCenters[0].ID).
		Update("centroid_embedding", []byte{0xff, 0xfe, 0xfd}).Error)

	engine := NewIdentityMatchingEngine(
		ann,
		repository.NewPersonIdentityProfileRepository(db),
		repository.NewFaceRepository(db),
		repository.NewCannotLinkRepository(db),
		repository.NewFaceRepository(db),
		IdentityProfileMatcherConfig{EmbeddingModel: "emb-v1", RescueThreshold: 0.60, Margin: 0.05, MinCenterFaces: 3},
	)
	svc := &peopleService{
		identityMatchingEngine: engine,
		identityProfileMode:    model.PeopleIdentityModePrimary,
		identityAutoStrategy: NewIdentityAutoStrategy(config.PeopleConfig{
			IdentityProfileRescueThreshold: 0.60,
			IdentityProfileMargin:          0.05,
			IdentityProfileMinCenterFaces:  3,
			MergeSuggestionThreshold:       0.55,
		}),
		config: &config.Config{People: config.PeopleConfig{
			IdentityProfileRescueThreshold: 0.60,
			IdentityProfileMargin:          0.05,
			IdentityProfileMinCenterFaces:  3,
		}},
	}
	face := makeFace(1, 10, nil, []float32{0.99, 0.01, 0}, 0.9, false, 0)
	dec := svc.decidePrimaryComponent([]*model.Face{face})
	require.Equal(t, primaryActionWait, dec.action)
	require.True(t, dec.engineRes.IncompleteEvidence)
	require.NotZero(t, dec.score, "wait path must keep Best.Score for diagnostics")
	require.Equal(t, person.ID, dec.personID)
}
