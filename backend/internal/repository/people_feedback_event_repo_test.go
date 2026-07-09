package repository

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newFeedbackEventRepo(t *testing.T) (*peopleFeedbackEventRepository, *gorm.DB) {
	t.Helper()
	db := setupTestDB(t)
	return NewPeopleFeedbackEventRepository(db).(*peopleFeedbackEventRepository), db
}

func TestPeopleFeedbackEventRepository_CreateRejectsNil(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	require.Error(t, repo.Create(nil))
}

func TestPeopleFeedbackEventRepository_CreateRejectsUnknownEventType(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	err := repo.Create(&model.PeopleFeedbackEvent{
		EventType:      "not_a_real_event",
		TargetPersonID: 1,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid feedback event type")
}

func TestPeopleFeedbackEventRepository_CreateRejectsInvalidJSON(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	err := repo.Create(&model.PeopleFeedbackEvent{
		EventType:       PeopleFeedbackEventMergeConfirmed,
		TargetPersonID:  1,
		SourcePersonIDs: "[not json",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source_person_ids")
}

func TestPeopleFeedbackEventRepository_CreateAssignsIDAndCreatedAt(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	event := &model.PeopleFeedbackEvent{
		EventType:          PeopleFeedbackEventMergeConfirmed,
		TargetPersonID:     7,
		SourcePersonIDs:    MarshalFeedbackIDs([]uint{3, 1, 2}),
		FaceIDs:            MarshalFeedbackIDs(nil),
		AlgorithmVersion:   "manual",
		SimilaritySnapshot: MarshalFeedbackSnapshot(nil),
	}
	require.NoError(t, repo.Create(event))
	assert.NotZero(t, event.ID, "DB assigns ID")
	assert.False(t, event.CreatedAt.IsZero(), "DB assigns CreatedAt")

	// Stored fields are exactly the normalized JSON the helper produced.
	var stored model.PeopleFeedbackEvent
	require.NoError(t, db.First(&stored, event.ID).Error)
	assert.Equal(t, "[1,2,3]", stored.SourcePersonIDs)
	assert.Equal(t, "[]", stored.FaceIDs)
	assert.Equal(t, "{}", stored.SimilaritySnapshot)
}

func TestPeopleFeedbackEventRepository_CreateNormalizesEmptyStrings(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	// Caller forgot to use helpers — Create still persists canonical defaults.
	event := &model.PeopleFeedbackEvent{
		EventType:      PeopleFeedbackEventFaceMoved,
		TargetPersonID: 5,
	}
	require.NoError(t, repo.Create(event))

	var stored model.PeopleFeedbackEvent
	require.NoError(t, db.First(&stored, event.ID).Error)
	assert.Equal(t, "[]", stored.SourcePersonIDs)
	assert.Equal(t, "[]", stored.FaceIDs)
	assert.Equal(t, "{}", stored.SimilaritySnapshot)
}

func TestPeopleFeedbackEventRepository_ListForCalibrationCursorAndLimit(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Create(&model.PeopleFeedbackEvent{
			EventType:      PeopleFeedbackEventPersonSplit,
			TargetPersonID: uint(i + 1),
		}))
	}

	all, err := repo.ListForCalibration(0, 100)
	require.NoError(t, err)
	require.Len(t, all, 5)
	// Ascending by id.
	assert.Less(t, all[0].ID, all[1].ID)
	assert.Less(t, all[1].ID, all[2].ID)

	// Cursor: skip the first two.
	page, err := repo.ListForCalibration(all[1].ID, 100)
	require.NoError(t, err)
	require.Len(t, page, 3)
	assert.Equal(t, all[2].ID, page[0].ID)

	// Limit honored.
	one, err := repo.ListForCalibration(0, 1)
	require.NoError(t, err)
	require.Len(t, one, 1)
	assert.Equal(t, all[0].ID, one[0].ID)
}

func TestPeopleFeedbackEventRepository_ListForCalibrationClampsLimit(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	// Insert more than the cap to prove clamping does not error and returns at most the cap.
	for i := 0; i < peopleFeedbackEventListLimit+5; i++ {
		require.NoError(t, repo.Create(&model.PeopleFeedbackEvent{
			EventType:      PeopleFeedbackEventPersonDissolved,
			TargetPersonID: 1,
		}))
	}
	got, err := repo.ListForCalibration(0, peopleFeedbackEventListLimit+1000)
	require.NoError(t, err)
	assert.Len(t, got, peopleFeedbackEventListLimit, "limit clamped to cap")
}

func TestPeopleFeedbackEventRepository_ListForCalibrationEmpty(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	// limit<=0 → empty slice, no error.
	got, err := repo.ListForCalibration(0, 0)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)

	// No rows → empty slice, no error.
	got, err = repo.ListForCalibration(0, 10)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Empty(t, got)
}

func TestPeopleFeedbackEventRepository_DoesNotLoadEmbeddings(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	require.NoError(t, repo.Create(&model.PeopleFeedbackEvent{
		EventType:      PeopleFeedbackEventMergeConfirmed,
		TargetPersonID: 1,
	}))
	got, err := repo.ListForCalibration(0, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	// PeopleFeedbackEvent has no embedding/path fields; verify the struct literally
	// has none of the sensitive columns by checking the stored JSON excludes them.
	raw, err := json.Marshal(got[0])
	require.NoError(t, err)
	lower := strings.ToLower(string(raw))
	assert.NotContains(t, lower, "embedding")
	assert.NotContains(t, lower, "thumbnail")
	assert.NotContains(t, lower, "file_path")
	assert.NotContains(t, lower, "api_key")
}

// TestPeopleFeedbackEventRepository_FindByEventTypeTargetAndFaceIDs 验证幂等查询的精确匹配：
// 只有 eventType + target_person_id + face_ids 三者全等才算同一事件。乱序/不同 face 集合
// 必须不匹配；空 face_ids 用 "[]" 规范化匹配。
func TestPeopleFeedbackEventRepository_FindByEventTypeTargetAndFaceIDs(t *testing.T) {
	repo, db := newFeedbackEventRepo(t)
	defer teardownTestDB(db)

	// 一条 split 事件：target=10, faces=[2,3]（写入时 MarshalFeedbackIDs 升序）。
	require.NoError(t, repo.Create(&model.PeopleFeedbackEvent{
		EventType:       PeopleFeedbackEventPersonSplit,
		TargetPersonID:  10,
		SourcePersonIDs: MarshalFeedbackIDs([]uint{1}),
		FaceIDs:         MarshalFeedbackIDs([]uint{3, 2}),
	}))
	// 一条 move 事件：target=20, faces=[5]。
	require.NoError(t, repo.Create(&model.PeopleFeedbackEvent{
		EventType:      PeopleFeedbackEventFaceMoved,
		TargetPersonID: 20,
		FaceIDs:        MarshalFeedbackIDs([]uint{5}),
	}))

	// 精确匹配 split 事件（target=10）。
	got, err := repo.FindByEventTypeTargetAndFaceIDs(PeopleFeedbackEventPersonSplit, 10, MarshalFeedbackIDs([]uint{2, 3}))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, uint(10), got[0].TargetPersonID)

	// target=0 时不按 target 过滤，仅按 face_ids 匹配，仍能查到 split 事件。
	got, err = repo.FindByEventTypeTargetAndFaceIDs(PeopleFeedbackEventPersonSplit, 0, MarshalFeedbackIDs([]uint{2, 3}))
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, uint(10), got[0].TargetPersonID)

	// 乱序输入经 helper 规范化后仍匹配。
	got, err = repo.FindByEventTypeTargetAndFaceIDs(PeopleFeedbackEventPersonSplit, 10, MarshalFeedbackIDs([]uint{3, 2, 2}))
	require.NoError(t, err)
	require.Len(t, got, 1)

	// 不同 face 集合不匹配。
	got, err = repo.FindByEventTypeTargetAndFaceIDs(PeopleFeedbackEventPersonSplit, 10, MarshalFeedbackIDs([]uint{2}))
	require.NoError(t, err)
	assert.Empty(t, got)

	// 不同 target 不匹配。
	got, err = repo.FindByEventTypeTargetAndFaceIDs(PeopleFeedbackEventPersonSplit, 99, MarshalFeedbackIDs([]uint{2, 3}))
	require.NoError(t, err)
	assert.Empty(t, got)

	// 空结果返回空 slice 而非 nil。
	got, err = repo.FindByEventTypeTargetAndFaceIDs(PeopleFeedbackEventPersonDissolved, 1, "[]")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got)
}

// TestRepositories_FeedbackEventInitialized confirms the aggregate Repositories
// wires the feedback event repository on construction.
func TestRepositories_FeedbackEventInitialized(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repos := NewRepositories(db)
	assert.NotNil(t, repos.FeedbackEvent)

	require.NoError(t, repos.FeedbackEvent.Create(&model.PeopleFeedbackEvent{
		EventType:      PeopleFeedbackEventMergeConfirmed,
		TargetPersonID: 42,
	}))
	got, err := repos.FeedbackEvent.ListForCalibration(0, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, uint(42), got[0].TargetPersonID)
}

func TestMarshalFeedbackIDsNormalization(t *testing.T) {
	// Dedup, drop zero, ascending.
	assert.Equal(t, "[]", MarshalFeedbackIDs(nil))
	assert.Equal(t, "[]", MarshalFeedbackIDs([]uint{}))
	assert.Equal(t, "[]", MarshalFeedbackIDs([]uint{0, 0}))
	assert.Equal(t, "[1,2,3]", MarshalFeedbackIDs([]uint{3, 1, 2, 1, 0}))
	// Deterministic regardless of input order.
	assert.Equal(t, "[5,9]", MarshalFeedbackIDs([]uint{9, 5}))
}

func TestMarshalFeedbackSnapshotNormalization(t *testing.T) {
	assert.Equal(t, "{}", MarshalFeedbackSnapshot(nil))
	assert.Equal(t, "{}", MarshalFeedbackSnapshot(map[string]interface{}{}))
	assert.Equal(t, `{"a":0.5}`, MarshalFeedbackSnapshot(map[string]interface{}{"a": 0.5}))
}
