package service

import (
	"strings"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupDeviceServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&model.Device{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	return db
}

func newDeviceServiceForTest(t *testing.T) (DeviceService, *gorm.DB) {
	t.Helper()
	db := setupDeviceServiceTestDB(t)
	repo := repository.NewDeviceRepository(db)
	svc := NewDeviceService(repo, &config.Config{
		Security: config.SecurityConfig{APIKeyPrefix: "sk-test-"},
	})
	return svc, db
}

func TestDeviceService_Create_Success(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	resp, err := svc.Create(&model.CreateDeviceRequest{Name: "My Frame", DeviceType: model.DeviceTypeEmbedded})
	require.NoError(t, err)
	assert.NotZero(t, resp.ID)
	assert.NotEmpty(t, resp.DeviceID)
	assert.True(t, strings.HasPrefix(resp.APIKey, "sk-test-"))
	assert.Equal(t, model.DeviceTypeEmbedded, resp.DeviceType)
	assert.Equal(t, util.DefaultRenderProfile(), resp.RenderProfile)
}

func TestDeviceService_Create_DefaultType(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	resp, err := svc.Create(&model.CreateDeviceRequest{Name: "Frame"})
	require.NoError(t, err)
	assert.Equal(t, model.DeviceTypeEmbedded, resp.DeviceType)
}

func TestDeviceService_Create_NonEmbedded_NoRenderProfile(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	resp, err := svc.Create(&model.CreateDeviceRequest{Name: "Analyzer", DeviceType: model.DeviceTypeOffline})
	require.NoError(t, err)
	assert.Empty(t, resp.RenderProfile)
}

func TestDeviceService_GetByID(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	created, _ := svc.Create(&model.CreateDeviceRequest{Name: "Frame"})
	got, err := svc.GetByID(created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Frame", got.Name)
}

func TestDeviceService_GetByAPIKey(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	created, _ := svc.Create(&model.CreateDeviceRequest{Name: "Frame"})
	got, err := svc.GetByAPIKey(created.APIKey)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
}

func TestDeviceService_Delete(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	created, _ := svc.Create(&model.CreateDeviceRequest{Name: "Frame"})
	require.NoError(t, svc.Delete(created.ID))

	_, err := svc.GetByID(created.ID)
	require.Error(t, err)
}

func TestDeviceService_UpdateEnabled(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	created, _ := svc.Create(&model.CreateDeviceRequest{Name: "Frame"})

	require.NoError(t, svc.UpdateEnabled(created.ID, false))
	got, _ := svc.GetByID(created.ID)
	assert.False(t, got.IsEnabled)

	require.NoError(t, svc.UpdateEnabled(created.ID, true))
	got2, _ := svc.GetByID(created.ID)
	assert.True(t, got2.IsEnabled)
}

func TestDeviceService_UpdateRenderProfile_Embedded(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	created, _ := svc.Create(&model.CreateDeviceRequest{Name: "Frame", DeviceType: model.DeviceTypeEmbedded})
	require.NoError(t, svc.UpdateRenderProfile(created.ID, "waveshare_7in3e"))

	got, _ := svc.GetByID(created.ID)
	assert.Equal(t, "waveshare_7in3e", got.RenderProfile)
}

func TestDeviceService_UpdateRenderProfile_NonEmbedded(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	created, _ := svc.Create(&model.CreateDeviceRequest{Name: "Mobile", DeviceType: model.DeviceTypeMobile})
	require.NoError(t, svc.UpdateRenderProfile(created.ID, "any_profile"))

	got, _ := svc.GetByID(created.ID)
	assert.Empty(t, got.RenderProfile) // non-embedded gets empty
}

func TestDeviceService_List_Clamping(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	for i := 0; i < 3; i++ {
		svc.Create(&model.CreateDeviceRequest{Name: "D"})
	}

	// page < 1 → 1, pageSize < 1 → 20
	devices, total, err := svc.List(0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(3), total)
	assert.Len(t, devices, 3)
}

func TestDeviceService_UpdateLastSeen(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	created, _ := svc.Create(&model.CreateDeviceRequest{Name: "Frame"})

	svc.UpdateLastSeen(created.ID, "192.168.1.100")

	got, _ := svc.GetByID(created.ID)
	assert.True(t, got.Online)
	assert.NotNil(t, got.LastSeen)
	assert.True(t, time.Since(*got.LastSeen) < time.Second)
	assert.Equal(t, "192.168.1.100", got.IPAddress)
}

func TestDeviceService_CountAll(t *testing.T) {
	svc, _ := newDeviceServiceForTest(t)

	svc.Create(&model.CreateDeviceRequest{Name: "A"})
	svc.Create(&model.CreateDeviceRequest{Name: "B"})

	c, err := svc.CountAll()
	require.NoError(t, err)
	assert.Equal(t, int64(2), c)
}
