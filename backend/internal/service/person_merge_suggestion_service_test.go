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
			MergeSuggestionThreshold:       0.90,
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
	svc, _, repos, _ := newPersonMergeSuggestionServiceForTest(t)

	bestTarget := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	otherTarget := createSuggestionTestPerson(t, repos, model.PersonCategoryFriend, []float32{0.8, 0.2}, []float32{0.78, 0.22})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.05})

	require.NoError(t, svc.MarkDirty("best-target-only"))
	require.NoError(t, svc.RunBackgroundSlice())

	got := pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	require.Len(t, got, 1)
	assert.Equal(t, []uint{candidate.ID}, got[bestTarget.ID])
	_, existsOnOther := got[otherTarget.ID]
	assert.False(t, existsOnOther)
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
				MergeSuggestionThreshold:       0.90,
				MergeSuggestionMaxPairsPerRun:  100,
				MergeSuggestionBatchSize:       10,
				MergeSuggestionCooldownSeconds: 1,
			},
		},
	)
	state = readSuggestionStateConfig(t, db)
	assert.Equal(t, true, state["paused"])

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

func TestPersonMergeSuggestionService_SlowsDownWhenPeopleBacklogIsHigh(t *testing.T) {
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
	require.Len(t, got, 1)
	assert.Equal(t, []uint{candidateA.ID}, got[targetA.ID])
	_, hasTargetB := got[targetB.ID]
	assert.False(t, hasTargetB)

	require.NoError(t, repos.PeopleJob.UpdateFields(1, map[string]interface{}{"status": model.PeopleJobStatusCompleted}))
	require.NoError(t, svc.RunBackgroundSlice())

	got = pendingSuggestionCandidatesByTarget(t, repos.MergeSuggestion)
	require.Len(t, got, 2)
	assert.Equal(t, []uint{candidateA.ID}, got[targetA.ID])
	assert.Equal(t, []uint{candidateB.ID}, got[targetB.ID])
}
