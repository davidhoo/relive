package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupEventTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Event{}, &model.Photo{}, &model.PhotoTag{}, &model.AppConfig{}))
	return db
}

func makeEvent(start time.Time, score float64, photoCount int) *model.Event {
	coverID := uint(1)
	return &model.Event{
		StartTime:    start,
		EndTime:      start.Add(2 * time.Hour),
		PhotoCount:   photoCount,
		EventScore:   score,
		CoverPhotoID: &coverID,
	}
}

// --- CRUD ---

func TestEventRepo_CreateAndGetByID(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	e := makeEvent(time.Now(), 80, 5)
	require.NoError(t, repo.Create(e))
	assert.NotZero(t, e.ID)

	got, err := repo.GetByID(e.ID)
	require.NoError(t, err)
	assert.Equal(t, e.ID, got.ID)
	assert.InDelta(t, 80.0, got.EventScore, 0.01)
}

func TestEventRepo_GetByID_NotFound(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	_, err := repo.GetByID(9999)
	assert.Error(t, err)
}

func TestEventRepo_Update(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	e := makeEvent(time.Now(), 50, 3)
	require.NoError(t, repo.Create(e))

	e.EventScore = 90
	require.NoError(t, repo.Update(e))

	got, err := repo.GetByID(e.ID)
	require.NoError(t, err)
	assert.InDelta(t, 90.0, got.EventScore, 0.01)
}

func TestEventRepo_Delete(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	e := makeEvent(time.Now(), 60, 2)
	require.NoError(t, repo.Create(e))
	require.NoError(t, repo.Delete(e.ID))

	_, err := repo.GetByID(e.ID)
	assert.Error(t, err)
}

func TestEventRepo_List_Pagination(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Create(makeEvent(base.Add(time.Duration(i)*time.Hour), float64(i*10), 2)))
	}

	events, total, err := repo.List(1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, events, 3)
}

func TestEventRepo_DeleteAll(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	require.NoError(t, repo.Create(makeEvent(time.Now(), 50, 2)))
	require.NoError(t, repo.Create(makeEvent(time.Now(), 70, 3)))
	require.NoError(t, repo.DeleteAll())

	_, total, err := repo.List(1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
}

func TestEventRepo_GetByTimeRange(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	base := time.Date(2024, 6, 1, 10, 0, 0, 0, time.UTC)
	e1 := &model.Event{
		StartTime:  base,
		EndTime:    base.Add(2 * time.Hour),
		PhotoCount: 2,
	}
	e2 := &model.Event{
		StartTime:  base.Add(10 * time.Hour),
		EndTime:    base.Add(12 * time.Hour),
		PhotoCount: 2,
	}
	require.NoError(t, repo.Create(e1))
	require.NoError(t, repo.Create(e2))

	// Query window overlaps only e1
	found, err := repo.GetByTimeRange(base.Add(-1*time.Hour), base.Add(1*time.Hour))
	require.NoError(t, err)
	assert.Len(t, found, 1)
	assert.Equal(t, e1.ID, found[0].ID)
}

func TestEventRepo_UpdateProfileFields(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	e := makeEvent(time.Now(), 50, 3)
	require.NoError(t, repo.Create(e))

	require.NoError(t, repo.UpdateProfileFields(e.ID, map[string]interface{}{
		"primary_category": "travel",
		"event_score":      99.5,
	}))

	got, err := repo.GetByID(e.ID)
	require.NoError(t, err)
	assert.Equal(t, "travel", got.PrimaryCategory)
	assert.InDelta(t, 99.5, got.EventScore, 0.01)
}

// --- Curation queries ---

func makeValidEvent(t *testing.T, repo EventRepository, start time.Time, score float64) *model.Event {
	t.Helper()
	coverID := uint(1)
	e := &model.Event{
		StartTime:    start,
		EndTime:      start.Add(2 * time.Hour),
		PhotoCount:   3,
		EventScore:   score,
		CoverPhotoID: &coverID,
	}
	require.NoError(t, repo.Create(e))
	return e
}

func TestEventRepo_GetTopScoredEvents(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	e1 := makeValidEvent(t, repo, base, 90)
	e2 := makeValidEvent(t, repo, base.Add(24*time.Hour), 70)
	e3 := makeValidEvent(t, repo, base.Add(48*time.Hour), 50)

	events, err := repo.GetTopScoredEvents(nil, 2)
	require.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, e1.ID, events[0].ID)
	assert.Equal(t, e2.ID, events[1].ID)

	// Exclude e1
	events, err = repo.GetTopScoredEvents([]uint{e1.ID}, 2)
	require.NoError(t, err)
	assert.Len(t, events, 2)
	assert.Equal(t, e2.ID, events[0].ID)
	assert.Equal(t, e3.ID, events[1].ID)
}

func TestEventRepo_GetNeverDisplayedEvents(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	e1 := makeValidEvent(t, repo, base, 80)
	e2 := makeValidEvent(t, repo, base.Add(24*time.Hour), 60)

	// Mark e1 as displayed
	require.NoError(t, repo.IncrementDisplayCount(e1.ID))

	events, err := repo.GetNeverDisplayedEvents(0, nil, 10)
	require.NoError(t, err)
	ids := make([]uint, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	assert.Contains(t, ids, e2.ID)
	assert.NotContains(t, ids, e1.ID)
}

func TestEventRepo_IncrementDisplayCount(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	e := makeValidEvent(t, repo, time.Now(), 70)
	assert.Equal(t, 0, e.DisplayCount)

	require.NoError(t, repo.IncrementDisplayCount(e.ID))
	require.NoError(t, repo.IncrementDisplayCount(e.ID))

	got, err := repo.GetByID(e.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.DisplayCount)
	assert.NotNil(t, got.LastDisplayedAt)
}

func TestEventRepo_GetRecentlyDisplayedEventIDs(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	e1 := makeValidEvent(t, repo, time.Now(), 80)
	e2 := makeValidEvent(t, repo, time.Now().Add(-24*time.Hour), 70)

	require.NoError(t, repo.IncrementDisplayCount(e1.ID))
	// e2 not displayed

	ids, err := repo.GetRecentlyDisplayedEventIDs(7)
	require.NoError(t, err)
	assert.Contains(t, ids, e1.ID)
	assert.NotContains(t, ids, e2.ID)
}

func TestEventRepo_GetOnThisDayEvents(t *testing.T) {
	db := setupEventTestDB(t)
	repo := NewEventRepository(db)

	// Event on Jan 15 (any year)
	e := makeValidEvent(t, repo, time.Date(2022, 1, 15, 10, 0, 0, 0, time.UTC), 75)
	// Event on Jun 20 (off-date)
	makeValidEvent(t, repo, time.Date(2022, 6, 20, 10, 0, 0, 0, time.UTC), 60)

	events, err := repo.GetOnThisDayEvents("01-15", 3, nil, 10)
	require.NoError(t, err)
	ids := make([]uint, len(events))
	for i, ev := range events {
		ids[i] = ev.ID
	}
	assert.Contains(t, ids, e.ID)
}
