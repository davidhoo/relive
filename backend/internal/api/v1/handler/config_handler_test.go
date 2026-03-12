package handler

import (
	"net/http"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newConfigHandlerForTest(t *testing.T) (*ConfigHandler, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&model.AppConfig{}))
	repo := repository.NewConfigRepository(db)
	svc := service.NewConfigService(repo)

	// ConfigHandler with only the config service wired — other services nil
	h := &ConfigHandler{service: svc}
	return h, db
}

func TestConfigHandler_GetConfig_Success(t *testing.T) {
	h, db := newConfigHandlerForTest(t)

	db.Create(&model.AppConfig{Key: "theme", Value: `"dark"`})

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/config/theme", nil,
		gin.Params{{Key: "key", Value: "theme"}}, h.GetConfig)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
}

func TestConfigHandler_GetConfig_NotFound(t *testing.T) {
	h, _ := newConfigHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/config/missing", nil,
		gin.Params{{Key: "key", Value: "missing"}}, h.GetConfig)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestConfigHandler_GetConfig_EmptyKey(t *testing.T) {
	h, _ := newConfigHandlerForTest(t)

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/config/", nil,
		gin.Params{{Key: "key", Value: ""}}, h.GetConfig)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestConfigHandler_SetConfig_Success(t *testing.T) {
	h, _ := newConfigHandlerForTest(t)

	body := []byte(`{"value":"light"}`)
	rec := performJSONRequest(t, http.MethodPut, "/api/v1/config/theme", body,
		gin.Params{{Key: "key", Value: "theme"}}, h.SetConfig)

	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify it was saved
	getRec := performJSONRequest(t, http.MethodGet, "/api/v1/config/theme", nil,
		gin.Params{{Key: "key", Value: "theme"}}, h.GetConfig)
	assert.Equal(t, http.StatusOK, getRec.Code)
}

func TestConfigHandler_ListConfigs(t *testing.T) {
	h, db := newConfigHandlerForTest(t)

	db.Create(&model.AppConfig{Key: "a", Value: "1"})
	db.Create(&model.AppConfig{Key: "b", Value: "2"})

	rec := performJSONRequest(t, http.MethodGet, "/api/v1/config", nil, nil, h.ListConfigs)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)
}
