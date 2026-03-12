package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ===== CRUD =====

func TestDeviceRepo_Create(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	d := &model.Device{DeviceID: "D001", Name: "Frame", APIKey: "sk-1", DeviceType: "embedded", IsEnabled: true}
	require.NoError(t, repo.Create(d))
	assert.NotZero(t, d.ID)
}

func TestDeviceRepo_GetByID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	d := &model.Device{DeviceID: "D001", Name: "Frame", APIKey: "sk-1", DeviceType: "embedded"}
	require.NoError(t, repo.Create(d))

	got, err := repo.GetByID(d.ID)
	require.NoError(t, err)
	assert.Equal(t, "Frame", got.Name)
}

func TestDeviceRepo_GetByID_NotFound(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	_, err := repo.GetByID(999)
	require.Error(t, err)
}

func TestDeviceRepo_GetByDeviceID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	d := &model.Device{DeviceID: "FRAME-X", Name: "X", APIKey: "sk-x", DeviceType: "embedded"}
	require.NoError(t, repo.Create(d))

	got, err := repo.GetByDeviceID("FRAME-X")
	require.NoError(t, err)
	assert.Equal(t, "X", got.Name)
}

func TestDeviceRepo_GetByAPIKey(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	d := &model.Device{DeviceID: "D001", Name: "Frame", APIKey: "sk-test-key", DeviceType: "embedded"}
	require.NoError(t, repo.Create(d))

	got, err := repo.GetByAPIKey("sk-test-key")
	require.NoError(t, err)
	assert.Equal(t, d.ID, got.ID)
}

func TestDeviceRepo_Update(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	d := &model.Device{DeviceID: "D001", Name: "Old", APIKey: "sk-1", DeviceType: "embedded"}
	require.NoError(t, repo.Create(d))

	d.Name = "New"
	require.NoError(t, repo.Update(d))

	got, err := repo.GetByID(d.ID)
	require.NoError(t, err)
	assert.Equal(t, "New", got.Name)
}

func TestDeviceRepo_Delete(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	d := &model.Device{DeviceID: "D001", Name: "Frame", APIKey: "sk-1", DeviceType: "embedded"}
	require.NoError(t, repo.Create(d))
	require.NoError(t, repo.Delete(d.ID))

	_, err := repo.GetByID(d.ID)
	require.Error(t, err)
}

// ===== List and Filter =====

func TestDeviceRepo_List_Pagination(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	for i := 0; i < 5; i++ {
		d := &model.Device{
			DeviceID:   "D" + string(rune('A'+i)),
			Name:       "Device" + string(rune('A'+i)),
			APIKey:     "sk-" + string(rune('A'+i)),
			DeviceType: "embedded",
		}
		require.NoError(t, repo.Create(d))
	}

	devices, total, err := repo.List(1, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total)
	assert.Len(t, devices, 3)

	devices2, _, err := repo.List(2, 3)
	require.NoError(t, err)
	assert.Len(t, devices2, 2)
}

func TestDeviceRepo_ListAll(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	for i := 0; i < 3; i++ {
		d := &model.Device{
			DeviceID: "D" + string(rune('0'+i)), Name: "N", APIKey: "sk-" + string(rune('0'+i)), DeviceType: "embedded",
		}
		require.NoError(t, repo.Create(d))
	}

	all, err := repo.ListAll()
	require.NoError(t, err)
	assert.Len(t, all, 3)
}

func TestDeviceRepo_ListByDeviceType(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	require.NoError(t, repo.Create(&model.Device{DeviceID: "D1", Name: "N1", APIKey: "sk-1", DeviceType: "embedded"}))
	require.NoError(t, repo.Create(&model.Device{DeviceID: "D2", Name: "N2", APIKey: "sk-2", DeviceType: "offline"}))
	require.NoError(t, repo.Create(&model.Device{DeviceID: "D3", Name: "N3", APIKey: "sk-3", DeviceType: "embedded"}))

	embedded, err := repo.ListByDeviceType("embedded")
	require.NoError(t, err)
	assert.Len(t, embedded, 2)
}

// ===== Exists =====

func TestDeviceRepo_Exists(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	d := &model.Device{DeviceID: "D1", Name: "N", APIKey: "sk-1", DeviceType: "embedded"}
	require.NoError(t, repo.Create(d))

	exists, err := repo.Exists(d.ID)
	require.NoError(t, err)
	assert.True(t, exists)

	exists2, err := repo.Exists(999)
	require.NoError(t, err)
	assert.False(t, exists2)
}

func TestDeviceRepo_ExistsByDeviceID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	require.NoError(t, repo.Create(&model.Device{DeviceID: "D1", Name: "N", APIKey: "sk-1"}))

	ok, err := repo.ExistsByDeviceID("D1")
	require.NoError(t, err)
	assert.True(t, ok)

	ok2, err := repo.ExistsByDeviceID("D999")
	require.NoError(t, err)
	assert.False(t, ok2)
}

func TestDeviceRepo_ExistsByAPIKey(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	require.NoError(t, repo.Create(&model.Device{DeviceID: "D1", Name: "N", APIKey: "sk-unique"}))

	ok, _ := repo.ExistsByAPIKey("sk-unique")
	assert.True(t, ok)
	ok2, _ := repo.ExistsByAPIKey("sk-nope")
	assert.False(t, ok2)
}

// ===== Online/Offline =====

func TestDeviceRepo_OnlineOffline(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	recent := time.Now().Add(-2 * time.Minute)
	old := time.Now().Add(-10 * time.Minute)

	require.NoError(t, repo.Create(&model.Device{DeviceID: "D1", Name: "Online", APIKey: "sk-1", LastSeen: &recent, Online: true}))
	require.NoError(t, repo.Create(&model.Device{DeviceID: "D2", Name: "Offline", APIKey: "sk-2", LastSeen: &old, Online: false}))

	online, err := repo.GetOnlineDevices()
	require.NoError(t, err)
	assert.Len(t, online, 1)
	assert.Equal(t, "Online", online[0].Name)

	offline, err := repo.GetOfflineDevices()
	require.NoError(t, err)
	assert.Len(t, offline, 1)
}

// ===== Counts =====

func TestDeviceRepo_Count(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	require.NoError(t, repo.Create(&model.Device{DeviceID: "D1", Name: "N1", APIKey: "sk-1", DeviceType: "embedded"}))
	require.NoError(t, repo.Create(&model.Device{DeviceID: "D2", Name: "N2", APIKey: "sk-2", DeviceType: "offline"}))

	total, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)

	byType, err := repo.CountByDeviceType("embedded")
	require.NoError(t, err)
	assert.Equal(t, int64(1), byType)
}

func TestDeviceRepo_CountOnline(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	recent := time.Now().Add(-2 * time.Minute)
	old := time.Now().Add(-10 * time.Minute)
	require.NoError(t, repo.Create(&model.Device{DeviceID: "D1", Name: "N1", APIKey: "sk-1", LastSeen: &recent}))
	require.NoError(t, repo.Create(&model.Device{DeviceID: "D2", Name: "N2", APIKey: "sk-2", LastSeen: &old}))

	online, err := repo.CountOnline()
	require.NoError(t, err)
	assert.Equal(t, int64(1), online)

	offline, err := repo.CountOffline()
	require.NoError(t, err)
	assert.Equal(t, int64(1), offline)
}

func TestDeviceRepo_UpdateStatus(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewDeviceRepository(db)

	require.NoError(t, repo.Create(&model.Device{DeviceID: "D1", Name: "N", APIKey: "sk-1", Online: false}))

	require.NoError(t, repo.UpdateStatus("D1", true))

	got, _ := repo.GetByDeviceID("D1")
	assert.True(t, got.Online)
}
