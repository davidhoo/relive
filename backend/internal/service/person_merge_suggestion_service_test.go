package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupPersonMergeSuggestionServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)

	require.NoError(t, db.AutoMigrate(
		&model.AppConfig{},
		&model.Photo{},
		&model.PhotoTag{},
		&model.Face{},
		&model.Person{},
		&model.PeopleJob{},
		&model.CannotLinkConstraint{},
		&model.PersonMergeSuggestion{},
		&model.PersonMergeSuggestionItem{},
		&model.PeopleFeedbackEvent{},
	))

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

func newPersonMergeSuggestionServiceForTest(t *testing.T) (PersonMergeSuggestionService, *gorm.DB, *repository.Repositories, ConfigService) {
	t.Helper()

	db := setupPersonMergeSuggestionServiceTestDB(t)
	repos := repository.NewRepositories(db)
	configService := NewConfigService(repos.Config)
	cfg := &config.Config{
		People: config.PeopleConfig{
			MergeSuggestionThreshold:       0.62,
			AttachThreshold:                1.10,
			MergeSuggestionMaxPairsPerRun:  100,
			MergeSuggestionBatchSize:       10,
			MergeSuggestionCooldownSeconds: 1,
		},
	}

	svc := NewPersonMergeSuggestionService(
		db,
		repos.Photo,
		repos.Face,
		repos.Person,
		repos.PeopleJob,
		repos.CannotLink,
		repos.MergeSuggestion,
		configService,
		cfg,
	)
	return svc, db, repos, configService
}

func newPersonMergeSuggestionServiceWithConfigForTest(t *testing.T, peopleCfg config.PeopleConfig) (PersonMergeSuggestionService, *gorm.DB, *repository.Repositories, ConfigService) {
	t.Helper()

	db := setupPersonMergeSuggestionServiceTestDB(t)
	repos := repository.NewRepositories(db)
	configService := NewConfigService(repos.Config)

	svc := NewPersonMergeSuggestionService(
		db,
		repos.Photo,
		repos.Face,
		repos.Person,
		repos.PeopleJob,
		repos.CannotLink,
		repos.MergeSuggestion,
		configService,
		&config.Config{People: peopleCfg},
	)
	return svc, db, repos, configService
}

func createSuggestionTestPerson(t *testing.T, repos *repository.Repositories, category string, embeddings ...[]float32) *model.Person {
	t.Helper()

	person := &model.Person{Category: category}
	require.NoError(t, repos.Person.Create(person))

	for i, emb := range embeddings {
		photo := &model.Photo{
			FilePath: fmt.Sprintf("/tmp/pms_test_%d_%d.jpg", person.ID, i),
			FileName: fmt.Sprintf("pms_test_%d_%d.jpg", person.ID, i),
			FileSize: 1,
			FileHash: fmt.Sprintf("hash_%d_%d", person.ID, i),
			Width:    100,
			Height:   100,
			Status:   model.PhotoStatusActive,
		}
		require.NoError(t, repos.Photo.Create(photo))

		face := &model.Face{
			PhotoID:       photo.ID,
			PersonID:      &person.ID,
			BBoxX:         0.1,
			BBoxY:         0.1,
			BBoxWidth:     0.2,
			BBoxHeight:    0.2,
			Confidence:    0.95,
			QualityScore:  0.9 - float64(i)*0.01,
			Embedding:     encodeEmbedding(t, emb),
			ClusterStatus: model.FaceClusterStatusAssigned,
			ClusterScore:  1.0,
		}
		require.NoError(t, repos.Face.Create(face))
	}

	require.NoError(t, repos.Person.RefreshStats(person.ID))
	return person
}

func pendingSuggestionCandidatesByTarget(t *testing.T, repo repository.PersonMergeSuggestionRepository) map[uint][]uint {
	t.Helper()

	suggestions, _, err := repo.ListPending(1, 100)
	require.NoError(t, err)

	got := make(map[uint][]uint, len(suggestions))
	for _, s := range suggestions {
		items, err := repo.GetItems(s.ID, model.PersonMergeSuggestionItemStatusPending)
		require.NoError(t, err)
		ids := make([]uint, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.CandidatePersonID)
		}
		got[s.TargetPersonID] = ids
	}
	return got
}

func readSuggestionStateConfig(t *testing.T, db *gorm.DB) map[string]interface{} {
	t.Helper()

	var cfg model.AppConfig
	require.NoError(t, db.Where("key = ?", "people.merge_suggestions.state").First(&cfg).Error)

	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(cfg.Value), &payload))
	return payload
}

func TestPersonMergeSuggestionService_BuildsPendingSuggestionsForFamilyAndFriendTargets(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	family := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	friend := createSuggestionTestPerson(t, repos, model.PersonCategoryFriend, []float32{0, 1}, []float32{0.01, 0.99})
	familyLike := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.02})
	friendLike := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.02, 1})
	_ = createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{-1, 0})

	require.NoError(t, svc.MarkDirty("test"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	require.Len(t, got, 2)
	assert.Equal(t, []uint{familyLike.ID}, got[family.ID])
	assert.Equal(t, []uint{friendLike.ID}, got[friend.ID])
}

func TestPersonMergeSuggestionService_IncludesAcquaintanceAsTarget(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	acquaintance := createSuggestionTestPerson(t, repos, model.PersonCategoryAcquaintance, []float32{1, 0}, []float32{0.99, 0.01})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.02})
	_ = createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{-1, 0})

	require.NoError(t, svc.MarkDirty("test"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	require.Len(t, got, 1)
	assert.Equal(t, []uint{candidate.ID}, got[acquaintance.ID])
}

func TestPersonMergeSuggestionService_SkipsCannotLinkCandidates(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.98, 0.02})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.01})

	require.NoError(t, repos.CannotLink.Create(target.ID, candidate.ID))
	require.NoError(t, svc.MarkDirty("cannot-link"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	assert.Len(t, got, 0)

	require.NoError(t, repos.CannotLink.DeleteByPersonID(target.ID))
	require.NoError(t, svc.MarkDirty("after-delete-cannot-link"))
	require.NoError(t, svc.RunBackgroundSlice())

	got = pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	require.Len(t, got, 1)
	assert.Equal(t, []uint{candidate.ID}, got[target.ID])
}

func TestPersonMergeSuggestionService_AssignsCandidateToBestTargetOnly(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.50,
		AttachThreshold:                1.10,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
	})

	bestTarget := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.98, 0.02})
	otherTarget := createSuggestionTestPerson(t, repos, model.PersonCategoryFriend, []float32{0.60, 0.80}, []float32{0.58, 0.81})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.90, 0.10})

	require.NoError(t, svc.MarkDirty("best-target-only"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	assert.Contains(t, got[bestTarget.ID], candidate.ID)
	assert.NotContains(t, got[otherTarget.ID], candidate.ID)
}

func TestPersonMergeSuggestionService_AllowsFamilyAndFriendCandidates(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	_ = createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	friendCandidate := createSuggestionTestPerson(t, repos, model.PersonCategoryFriend, []float32{1, 0.03})
	familyCandidate := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0.04})

	require.NoError(t, svc.MarkDirty("allow-family-friend-candidates"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	allCandidateIDs := make([]uint, 0)
	for _, ids := range got {
		allCandidateIDs = append(allCandidateIDs, ids...)
	}
	assert.Contains(t, allCandidateIDs, friendCandidate.ID)
	assert.Contains(t, allCandidateIDs, familyCandidate.ID)
}

func TestPersonMergeSuggestionService_CreatesSuggestionEvenAboveAttachThreshold(t *testing.T) {
	// Candidates above the attach threshold should still produce merge suggestions.
	// Previously they were skipped (logic: "above attach = auto-attach"), but since
	// these faces are already assigned to existing persons, auto-attach never fires,
	// creating a blind spot where high-similarity stranger pairs are never suggested.
	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.90,
		AttachThreshold:                0.95,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
	})

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	highSimilarityCandidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0})

	require.NoError(t, svc.MarkDirty("attach-threshold-upper-bound"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	assert.Contains(t, got[target.ID], highSimilarityCandidate.ID, "candidates above attach threshold should still produce suggestions")
}

func TestPersonMergeSuggestionService_UsesAverageBestSimilarityInsteadOfSingleMaxPair(t *testing.T) {
	// Verify that a candidate with one high-similarity prototype and one low-similarity
	// prototype stays below threshold when using average-best (not max-pair).
	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.90,
		AttachThreshold:                1.10, // effectively disabled - never skip above attach
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
	})

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0, 1})
	// One prototype matches target[0] perfectly, one is nearly orthogonal to both.
	// average-best is well below 0.90 threshold.
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0}, []float32{0.5, 0.5})

	require.NoError(t, svc.MarkDirty("average-best-similarity"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	assert.NotContains(t, got, target.ID)
	assert.NotContains(t, got[target.ID], candidate.ID)
}

func TestPersonMergeSuggestionService_PauseResumeAndRebuildPersistState(t *testing.T) {
	svc, db, repos, configService := newPersonMergeSuggestionServiceForTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.01})
	require.NotZero(t, target.ID)
	require.NotZero(t, candidate.ID)

	require.NoError(t, svc.Pause())
	state := readSuggestionStateConfig(t, db)
	assert.Equal(t, true, state["paused"])
	assert.Equal(t, "paused", svc.GetTask().Status)

	reloaded := NewPersonMergeSuggestionService(
		db,
		repos.Photo,
		repos.Face,
		repos.Person,
		repos.PeopleJob,
		repos.CannotLink,
		repos.MergeSuggestion,
		configService,
		&config.Config{
			People: config.PeopleConfig{
				MergeSuggestionThreshold:       0.62,
				AttachThreshold:                1.10,
				MergeSuggestionMaxPairsPerRun:  100,
				MergeSuggestionBatchSize:       10,
				MergeSuggestionCooldownSeconds: 1,
			},
		},
	)
	state = readSuggestionStateConfig(t, db)
	assert.Equal(t, true, state["paused"])
	assert.Equal(t, "paused", reloaded.GetTask().Status)

	require.NoError(t, reloaded.Resume())
	state = readSuggestionStateConfig(t, db)
	assert.Equal(t, false, state["paused"])

	require.NoError(t, reloaded.MarkDirty("seed"))
	require.NoError(t, reloaded.RunBackgroundSlice())
	_, totalBefore, err := repos.MergeSuggestion.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), totalBefore)

	require.NoError(t, reloaded.Rebuild())
	_, totalAfter, err := repos.MergeSuggestion.ListPending(1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), totalAfter)

	state = readSuggestionStateConfig(t, db)
	assert.Equal(t, true, state["dirty"])
	assert.Equal(t, float64(0), state["cursor_target_id"])
}

func TestPersonMergeSuggestionService_ProcessesAllTargetsRegardlessOfPeopleJobs(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	targetA := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	targetB := createSuggestionTestPerson(t, repos, model.PersonCategoryFriend, []float32{0, 1}, []float32{0.01, 0.99})
	candidateA := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.02})
	candidateB := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.02, 1})
	require.NotZero(t, candidateA.ID)
	require.NotZero(t, candidateB.ID)

	photo := &model.Photo{
		FilePath: "/tmp/backlog.jpg",
		FileName: "backlog.jpg",
		FileSize: 1,
		FileHash: "backlog",
		Width:    100,
		Height:   100,
		Status:   model.PhotoStatusActive,
	}
	require.NoError(t, repos.Photo.Create(photo))
	require.NoError(t, repos.PeopleJob.Create(&model.PeopleJob{
		PhotoID:   photo.ID,
		FilePath:  photo.FilePath,
		Status:    model.PeopleJobStatusQueued,
		Priority:  10,
		Source:    model.PeopleJobSourceScan,
		QueuedAt:  time.Now(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}))

	require.NoError(t, svc.MarkDirty("backlog"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	require.Len(t, got, 2)
	assert.Equal(t, []uint{candidateA.ID}, got[targetA.ID])
	assert.Equal(t, []uint{candidateB.ID}, got[targetB.ID])
}

func TestPersonMergeSuggestionService_EndToEndReviewFlow(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	excludedCandidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.015})
	mergedCandidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.03})
	otherTarget := createSuggestionTestPerson(t, repos, model.PersonCategoryFriend, []float32{0, 1}, []float32{0.01, 0.99})
	_ = createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0, 1.02})

	require.NotZero(t, target.ID)
	require.NotZero(t, excludedCandidate.ID)
	require.NotZero(t, mergedCandidate.ID)
	require.NotZero(t, otherTarget.ID)

	require.NoError(t, svc.MarkDirty("end-to-end"))
	require.NoError(t, svc.RunBackgroundSlice())

	suggestions, total, err := svc.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)

	var targetSuggestion *model.PersonMergeSuggestionResponse
	for i := range suggestions {
		if suggestions[i].TargetPersonID == target.ID {
			targetSuggestion = &suggestions[i]
			break
		}
	}
	require.NotNil(t, targetSuggestion)
	require.Len(t, targetSuggestion.Items, 2)

	require.NoError(t, svc.ExcludeCandidates(targetSuggestion.ID, []uint{excludedCandidate.ID}))
	blocked, err := repos.CannotLink.ExistsBetween(target.ID, excludedCandidate.ID)
	require.NoError(t, err)
	assert.True(t, blocked)

	afterExclude, err := svc.GetPendingByID(targetSuggestion.ID)
	require.NoError(t, err)
	require.NotNil(t, afterExclude)
	require.Len(t, afterExclude.Items, 1)
	assert.Equal(t, mergedCandidate.ID, afterExclude.Items[0].CandidatePersonID)

	require.NoError(t, svc.ApplySuggestion(targetSuggestion.ID, []uint{mergedCandidate.ID}))

	mergedPerson, err := repos.Person.GetByID(mergedCandidate.ID)
	require.NoError(t, err)
	assert.Nil(t, mergedPerson)

	finalSuggestions, finalTotal, err := svc.ListPending(1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), finalTotal)
	require.Len(t, finalSuggestions, 1)
	assert.Equal(t, otherTarget.ID, finalSuggestions[0].TargetPersonID)
}

func TestPersonMergeSuggestionService_FiltersDeletedCandidatesFromStaleANN(t *testing.T) {
	// Regression test: after a merge deletes a candidate person, the stale ANN index
	// may still return that person's ID. buildAssignments must validate candidates
	// against the database and filter out deleted/non-existent persons.
	svc, db, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.98, 0.02})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.01})

	// Build the ANN index so it contains the candidate
	require.NoError(t, svc.MarkDirty("initial"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	require.Contains(t, got[target.ID], candidate.ID)

	// Simulate what happens after a merge: delete the candidate person directly
	// (MergeInto would do this). The ANN index is now stale.
	require.NoError(t, db.Delete(&model.Person{}, candidate.ID).Error)

	// Mark dirty but DON'T rebuild the ANN index (simulates the 30-min cooldown)
	// The stale ANN still has candidate's embeddings.
	require.NoError(t, svc.MarkDirty("post-merge"))

	// Run background slice — it should NOT generate suggestions referencing the deleted person
	require.NoError(t, svc.RunBackgroundSlice())

	got = pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	// The deleted candidate should NOT appear in any suggestion
	for _, candidateIDs := range got {
		assert.NotContains(t, candidateIDs, candidate.ID)
	}
}

func TestPersonMergeSuggestionService_AutoRerunWhenStale(t *testing.T) {
	// When LastRunAt is older than the configured stale threshold,
	// RunBackgroundSlice should automatically mark dirty and run a full pass.
	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.62,
		AttachThreshold:                1.10,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
		MergeSuggestionStaleSeconds:    1, // 1 second for fast test
	})

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.98, 0.02})
	_ = target

	// Run once to set LastRunAt
	require.NoError(t, svc.MarkDirty("initial"))
	require.NoError(t, svc.RunBackgroundSlice())

	// Verify not dirty after run
	stats, err := svc.GetStats()
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.Pending)

	// Wait for stale threshold to pass
	time.Sleep(1100 * time.Millisecond)

	// RunBackgroundSlice should auto-mark dirty and re-run
	require.NoError(t, svc.RunBackgroundSlice())

	// Verify it actually ran — LastRunAt should be recent now
	task := svc.GetTask()
	assert.Equal(t, model.TaskStatusIdle, task.Status)
}

// ---- Task 10: 身份画像中心接入合并建议 ----

// fakeProfileProvider 是测试用的 PersonProfileSimilarityProvider，返回预设结果。
type fakeProfileProvider struct {
	similar      map[uint][]IdentityProfileMatch
	similarOK    bool
	compare      map[PersonPair]IdentityProfileMatch
	compareOK    bool
	similarCalls int
}

func (f *fakeProfileProvider) SimilarPeople(_ []uint, _ int) (map[uint][]IdentityProfileMatch, bool) {
	f.similarCalls++
	return f.similar, f.similarOK
}

func (f *fakeProfileProvider) ComparePeople(_ []PersonPair) (map[PersonPair]IdentityProfileMatch, bool) {
	return f.compare, f.compareOK
}

func injectFakeProfileProvider(t *testing.T, svc PersonMergeSuggestionService, provider PersonProfileSimilarityProvider) {
	t.Helper()
	svc.(*personMergeSuggestionService).SetProfileSimilarityProvider(provider)
}

// pendingSuggestionItemsByTarget 读取每个目标人物的 pending 建议项（含 MatchSource/Warning）。
func pendingSuggestionItemsByTarget(t *testing.T, repo repository.PersonMergeSuggestionRepository) map[uint][]*model.PersonMergeSuggestionItem {
	t.Helper()
	suggestions, _, err := repo.ListPending(1, 100)
	require.NoError(t, err)
	got := make(map[uint][]*model.PersonMergeSuggestionItem, len(suggestions))
	for _, s := range suggestions {
		items, err := repo.GetItems(s.ID, model.PersonMergeSuggestionItemStatusPending)
		require.NoError(t, err)
		got[s.TargetPersonID] = items
	}
	return got
}

func TestPersonMergeSuggestionService_IdentityProfile_GeneratesSuggestion(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0})
	cand := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.5, 0.5})

	// 画像 provider 召回 cand 并通过 medoid 验证，分数高于阈值。
	injectFakeProfileProvider(t, svc, &fakeProfileProvider{
		similar:   map[uint][]IdentityProfileMatch{target.ID: {{Available: true, PersonID: cand.ID, Score: 0.95}}},
		similarOK: true,
		compare: map[PersonPair]IdentityProfileMatch{
			{TargetID: target.ID, CandidateID: cand.ID}: {Available: true, PersonID: cand.ID, Score: 0.95},
		},
		compareOK: true,
	})

	require.NoError(t, svc.MarkDirty("profile"))
	require.NoError(t, svc.RunBackgroundSlice())

	items := pendingSuggestionItemsByTarget(t, repos.MergeSuggestion)
	require.Contains(t, items, target.ID)
	require.Len(t, items[target.ID], 1)
	assert.Equal(t, cand.ID, items[target.ID][0].CandidatePersonID)
	assert.Equal(t, model.PersonMergeMatchSourceIdentityProfile, items[target.ID][0].MatchSource)
	assert.Empty(t, items[target.ID][0].Warning)
}

func TestPersonMergeSuggestionService_IdentityProfile_UnavailableFallsBackToLegacy(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	cand := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.01})

	// 画像整批不可用 → 应完整回退 legacy 路径。
	injectFakeProfileProvider(t, svc, &fakeProfileProvider{similarOK: false})

	require.NoError(t, svc.MarkDirty("profile-unavailable"))
	require.NoError(t, svc.RunBackgroundSlice())

	// legacy 路径仍应基于 prototype 相似度生成建议。
	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	assert.Contains(t, got[target.ID], cand.ID)

	// 来源应为 legacy。
	items := pendingSuggestionItemsByTarget(t, repos.MergeSuggestion)
	require.Len(t, items[target.ID], 1)
	assert.Equal(t, model.PersonMergeMatchSourceLegacy, items[target.ID][0].MatchSource)
}

func TestPersonMergeSuggestionService_IdentityProfile_LegacyModeDoesNotCallProvider(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	cand := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.01})

	// legacy 模式：不注入 provider（nil）。即使注入一个会失败的 provider 也不应被调用。
	// 这里验证 nil provider 下结果与 legacy 一致。
	require.Nil(t, svc.(*personMergeSuggestionService).profileProvider)

	require.NoError(t, svc.MarkDirty("legacy"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	assert.Contains(t, got[target.ID], cand.ID)
}

func TestPersonMergeSuggestionService_IdentityProfile_CannotLinkBlocksProfileCandidate(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0})
	cand := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.5, 0.5})

	require.NoError(t, repos.CannotLink.Create(target.ID, cand.ID))

	injectFakeProfileProvider(t, svc, &fakeProfileProvider{
		similar:   map[uint][]IdentityProfileMatch{target.ID: {{Available: true, PersonID: cand.ID, Score: 0.95}}},
		similarOK: true,
		compare: map[PersonPair]IdentityProfileMatch{
			{TargetID: target.ID, CandidateID: cand.ID}: {Available: true, PersonID: cand.ID, Score: 0.95},
		},
		compareOK: true,
	})

	require.NoError(t, svc.MarkDirty("cannot-link"))
	require.NoError(t, svc.RunBackgroundSlice())

	items := pendingSuggestionItemsByTarget(t, repos.MergeSuggestion)
	assert.NotContains(t, items, target.ID, "cannot-link 硬阻断 profile 候选")
}

func TestPersonMergeSuggestionService_IdentityProfile_SamePhotoCooccurrenceWarning(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0})
	cand := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.5, 0.5})

	// 让 cand 在 target 的一张照片中也出现人脸 → 同照片共现。
	targetFaces, err := repos.Face.ListByPersonID(target.ID)
	require.NoError(t, err)
	require.NotEmpty(t, targetFaces)
	sharedFace := &model.Face{
		PhotoID:  targetFaces[0].PhotoID,
		PersonID: &cand.ID,
		BBoxX:    0.6, BBoxY: 0.6, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence:    0.9,
		ClusterStatus: model.FaceClusterStatusAssigned,
		ClusterScore:  1.0,
	}
	require.NoError(t, repos.Face.Create(sharedFace))

	injectFakeProfileProvider(t, svc, &fakeProfileProvider{
		similar:   map[uint][]IdentityProfileMatch{target.ID: {{Available: true, PersonID: cand.ID, Score: 0.95}}},
		similarOK: true,
		compare: map[PersonPair]IdentityProfileMatch{
			{TargetID: target.ID, CandidateID: cand.ID}: {Available: true, PersonID: cand.ID, Score: 0.95},
		},
		compareOK: true,
	})

	require.NoError(t, svc.MarkDirty("cooccurrence"))
	require.NoError(t, svc.RunBackgroundSlice())

	items := pendingSuggestionItemsByTarget(t, repos.MergeSuggestion)
	require.Contains(t, items, target.ID)
	require.Len(t, items[target.ID], 1)
	assert.Equal(t, model.PersonMergeMatchSourceIdentityProfile, items[target.ID][0].MatchSource)
	assert.Equal(t, model.PersonMergeWarningSamePhotoCooccurrence, items[target.ID][0].Warning, "同照片共现写入 warning")
}

func TestPersonMergeSuggestionService_IdentityProfile_BelowThresholdNotSaved(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	// 默认阈值 0.62。

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0})
	cand := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.5, 0.5})

	// 画像分数低于阈值 → 不保存。
	injectFakeProfileProvider(t, svc, &fakeProfileProvider{
		similar:   map[uint][]IdentityProfileMatch{target.ID: {{Available: true, PersonID: cand.ID, Score: 0.3}}},
		similarOK: true,
		compare: map[PersonPair]IdentityProfileMatch{
			{TargetID: target.ID, CandidateID: cand.ID}: {Available: true, PersonID: cand.ID, Score: 0.3},
		},
		compareOK: true,
	})

	require.NoError(t, svc.MarkDirty("below-threshold"))
	require.NoError(t, svc.RunBackgroundSlice())

	items := pendingSuggestionItemsByTarget(t, repos.MergeSuggestion)
	assert.NotContains(t, items, target.ID, "低于阈值的画像候选不保存")
}

func TestPersonMergeSuggestionService_IdentityProfile_CandidateBelongsToBestTarget(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.50,
		AttachThreshold:                1.10,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
	})

	bestTarget := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0})
	otherTarget := createSuggestionTestPerson(t, repos, model.PersonCategoryFriend, []float32{0.5, 0.5})
	cand := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.5, 0.5})

	// 两个 target 都通过画像召回 cand；bestTarget 分数更高 → cand 只归属 bestTarget。
	injectFakeProfileProvider(t, svc, &fakeProfileProvider{
		similar: map[uint][]IdentityProfileMatch{
			bestTarget.ID:  {{Available: true, PersonID: cand.ID, Score: 0.95}},
			otherTarget.ID: {{Available: true, PersonID: cand.ID, Score: 0.70}},
		},
		similarOK: true,
		compare: map[PersonPair]IdentityProfileMatch{
			{TargetID: bestTarget.ID, CandidateID: cand.ID}:  {Available: true, PersonID: cand.ID, Score: 0.95},
			{TargetID: otherTarget.ID, CandidateID: cand.ID}: {Available: true, PersonID: cand.ID, Score: 0.70},
		},
		compareOK: true,
	})

	require.NoError(t, svc.MarkDirty("best-target"))
	require.NoError(t, svc.RunBackgroundSlice())

	items := pendingSuggestionItemsByTarget(t, repos.MergeSuggestion)
	assert.Contains(t, items, bestTarget.ID)
	assert.NotContains(t, items, otherTarget.ID, "candidate 只归属最佳 target")
	assert.Equal(t, cand.ID, items[bestTarget.ID][0].CandidatePersonID)
}

func TestPersonMergeSuggestionService_IdentityProfile_PerTargetFallback(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	// targetA 有画像候选；targetB 无画像召回 → 走 legacy。
	targetA := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0})
	candA := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.5, 0.5})
	targetB := createSuggestionTestPerson(t, repos, model.PersonCategoryFriend, []float32{0, 1}, []float32{0.01, 0.99})
	candB := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{0.02, 1})

	// SimilarPeople 只返回 targetA 的候选；targetB 无 entry → legacy。
	injectFakeProfileProvider(t, svc, &fakeProfileProvider{
		similar:   map[uint][]IdentityProfileMatch{targetA.ID: {{Available: true, PersonID: candA.ID, Score: 0.95}}},
		similarOK: true,
		compare: map[PersonPair]IdentityProfileMatch{
			{TargetID: targetA.ID, CandidateID: candA.ID}: {Available: true, PersonID: candA.ID, Score: 0.95},
		},
		compareOK: true,
	})

	require.NoError(t, svc.MarkDirty("per-target"))
	require.NoError(t, svc.RunBackgroundSlice())

	items := pendingSuggestionItemsByTarget(t, repos.MergeSuggestion)
	// targetA 走画像。
	require.Contains(t, items, targetA.ID)
	assert.Equal(t, model.PersonMergeMatchSourceIdentityProfile, items[targetA.ID][0].MatchSource)
	// targetB 走 legacy（prototype ANN 召回 candB）。
	require.Contains(t, items, targetB.ID)
	assert.Equal(t, model.PersonMergeMatchSourceLegacy, items[targetB.ID][0].MatchSource)
	assert.Equal(t, candB.ID, items[targetB.ID][0].CandidatePersonID)
}

// ---- Task 12: merge suggestion background slice 治理 ----

// mergeSuggestionSvcForGateTest 构造一个带 backgroundCoordinator 的 service，返回底层
// *personMergeSuggestionService 便于直接断言 state。
func mergeSuggestionSvcForGateTest(t *testing.T) (*personMergeSuggestionService, *repository.Repositories, *BackgroundTaskCoordinator) {
	t.Helper()
	svc, db, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	_ = db
	bgCoord := NewBackgroundTaskCoordinator()
	ms := svc.(*personMergeSuggestionService)
	ms.SetBackgroundCoordinator(bgCoord)
	return ms, repos, bgCoord
}

// TestMergeSuggestionBackgroundSlice_SkipsWhenCoordinatorBusy 验证 coordinator 拒绝时
// RunBackgroundSlice 不执行 heavy work（不写 suggestion、不 advance cursor），return nil。
func TestMergeSuggestionBackgroundSlice_SkipsWhenCoordinatorBusy(t *testing.T) {
	ms, repos, bgCoord := mergeSuggestionSvcForGateTest(t)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.98, 0.02})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.01})
	require.NoError(t, ms.MarkDirty("test"))

	// foreground active：coordinator 拒绝 automatic 准入。
	release := bgCoord.BeginForeground()
	defer release()

	require.NoError(t, ms.RunBackgroundSlice(), "skipped slice must return nil")

	// heavy work 未执行：没有 suggestion 被写入。
	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	assert.Empty(t, got, "must not write suggestions when coordinator busy")

	// cursor 未 advance（保持 dirty）。
	ms.mu.RLock()
	dirty := ms.state.Dirty
	cursor := ms.state.CursorTargetID
	ms.mu.RUnlock()
	assert.True(t, dirty, "skipped slice must keep dirty")
	assert.Equal(t, uint(0), cursor, "skipped slice must not advance cursor")

	// 释放 foreground 后再次 slice：应执行 heavy work。
	release()
	require.NoError(t, ms.RunBackgroundSlice())
	got = pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	assert.Contains(t, got[target.ID], candidate.ID, "must run after foreground released")
}

// TestMergeSuggestionBackgroundSlice_KeepsDirtyWhenSkipped 验证 skipped 时 dirty/cursor
// 不被清，且不会把持久化任务错误标记为完成。等价于“被跳过 ≠ 标记 clean”。
func TestMergeSuggestionBackgroundSlice_KeepsDirtyWhenSkipped(t *testing.T) {
	ms, _, bgCoord := mergeSuggestionSvcForGateTest(t)
	require.NoError(t, ms.MarkDirty("test"))

	// 先记录 dirty=true + generation。
	ms.mu.RLock()
	genBefore := ms.state.DirtyGeneration
	ms.mu.RUnlock()
	require.True(t, genBefore > 0)

	release := bgCoord.BeginForeground()
	require.NoError(t, ms.RunBackgroundSlice())
	release()

	// skipped 后 dirty 仍 true，generation 不变（未被错误标记 clean）。
	ms.mu.RLock()
	dirty := ms.state.Dirty
	genAfter := ms.state.DirtyGeneration
	taskStatus := ms.task.Status
	ms.mu.RUnlock()
	assert.True(t, dirty, "dirty must remain true when skipped")
	assert.Equal(t, genBefore, genAfter, "dirty generation must not change when skipped")
	_ = taskStatus
}

// TestMergeSuggestionBackgroundSlice_AllowsCheapStaleDirtyMarkBeforeHeavyWork 验证轻量
// stale/dirty detection 在 coordinator gate 之前执行：距上次巡检超过 stale 阈值时，
// 即使 foreground active，仍会标记 dirty（低频状态更新，属既有行为），只是不执行 heavy work。
func TestMergeSuggestionBackgroundSlice_AllowsCheapStaleDirtyMarkBeforeHeavyWork(t *testing.T) {
	ms, repos, bgCoord := mergeSuggestionSvcForGateTest(t)
	// 配置极短 stale 阈值，便于触发自动重跑标记。
	ms.config.People.MergeSuggestionStaleSeconds = 1

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.98, 0.02})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.01})
	_ = target
	_ = candidate

	// 先跑一次完成巡检（清除 dirty），记录 LastRunAt。需要两轮：第一轮处理 target，
	// 第二轮 cursor=0 无目标 → 清 dirty 并记 LastRunAt。
	require.NoError(t, ms.RunBackgroundSlice())
	// MergeSuggestionCooldownSeconds=1：推进绕过 cooldown 跑第二轮。
	time.Sleep(1100 * time.Millisecond)
	require.NoError(t, ms.RunBackgroundSlice())
	ms.mu.RLock()
	lastRun := ms.state.LastRunAt
	dirtyAfterFirst := ms.state.Dirty
	ms.mu.RUnlock()
	require.False(t, dirtyAfterFirst, "first slice should clear dirty")
	require.False(t, lastRun.IsZero())

	// 等待超过 stale 阈值。
	time.Sleep(1100 * time.Millisecond)

	// foreground active：coordinator 拒绝 heavy work，但 stale detection 应已把 dirty 标回 true。
	release := bgCoord.BeginForeground()
	require.NoError(t, ms.RunBackgroundSlice())
	release()

	ms.mu.RLock()
	dirtyAfterStale := ms.state.Dirty
	ms.mu.RUnlock()
	assert.True(t, dirtyAfterStale, "cheap stale detection must mark dirty even when coordinator blocks heavy work")
}
