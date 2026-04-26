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
	friendLike := createSuggestionTestPerson(t, repos, model.PersonCategoryAcquaintance, []float32{0.02, 1})
	_ = createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{-1, 0})

	require.NoError(t, svc.MarkDirty("test"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	require.Len(t, got, 2)
	assert.Equal(t, []uint{familyLike.ID}, got[family.ID])
	assert.Equal(t, []uint{friendLike.ID}, got[friend.ID])
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

func TestPersonMergeSuggestionService_DoesNotCreateSuggestionAtOrAboveAttachThreshold(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.90,
		AttachThreshold:                0.95,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
	})

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	tooCloseCandidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0})

	require.NoError(t, svc.MarkDirty("attach-threshold-upper-bound"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	assert.NotContains(t, got, target.ID)
	assert.NotContains(t, got[target.ID], tooCloseCandidate.ID)
}

func TestPersonMergeSuggestionService_UsesAverageBestSimilarityInsteadOfSingleMaxPair(t *testing.T) {
	svc, _, repos, _ := newPersonMergeSuggestionServiceWithConfigForTest(t, config.PeopleConfig{
		MergeSuggestionThreshold:       0.80,
		AttachThreshold:                0.95,
		MergeSuggestionMaxPairsPerRun:  100,
		MergeSuggestionBatchSize:       10,
		MergeSuggestionCooldownSeconds: 1,
	})

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0, 1})
	// One prototype matches perfectly, one is nearly orthogonal.
	// Max-pair logic would suggest; average-best should stay below threshold.
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0}, []float32{0.2, 0.98})

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
	mergedCandidate := createSuggestionTestPerson(t, repos, model.PersonCategoryAcquaintance, []float32{1, 0.03})
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
