package service

import (
	"encoding/json"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupPromptServiceForTest(t *testing.T) PromptService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AppConfig{}))
	return NewPromptService(repository.NewConfigRepository(db))
}

func TestPromptService_GetPromptConfig_ReturnsDefaultsWhenEmpty(t *testing.T) {
	svc := setupPromptServiceForTest(t)
	cfg, err := svc.GetPromptConfig()
	require.NoError(t, err)
	defaults := model.GetDefaultPromptConfig()
	assert.Equal(t, defaults.AnalysisPrompt, cfg.AnalysisPrompt)
	assert.Equal(t, defaults.CaptionPrompt, cfg.CaptionPrompt)
	assert.Equal(t, defaults.BatchPrompt, cfg.BatchPrompt)
}

func TestPromptService_GetPromptConfig_ReturnsSavedConfig(t *testing.T) {
	svc := setupPromptServiceForTest(t)
	saved := &model.PromptConfig{
		AnalysisPrompt: "custom analysis",
		CaptionPrompt:  "custom caption",
		BatchPrompt:    "custom batch",
	}
	require.NoError(t, svc.SetPromptConfig(saved))

	got, err := svc.GetPromptConfig()
	require.NoError(t, err)
	assert.Equal(t, "custom analysis", got.AnalysisPrompt)
	assert.Equal(t, "custom caption", got.CaptionPrompt)
	assert.Equal(t, "custom batch", got.BatchPrompt)
}

func TestPromptService_GetPromptConfig_FillsMissingFieldsWithDefaults(t *testing.T) {
	svc := setupPromptServiceForTest(t)
	// 只设置 AnalysisPrompt，其余为空
	partial := &model.PromptConfig{AnalysisPrompt: "only analysis"}
	require.NoError(t, svc.SetPromptConfig(partial))

	got, err := svc.GetPromptConfig()
	require.NoError(t, err)
	assert.Equal(t, "only analysis", got.AnalysisPrompt)
	defaults := model.GetDefaultPromptConfig()
	assert.Equal(t, defaults.CaptionPrompt, got.CaptionPrompt)
	assert.Equal(t, defaults.BatchPrompt, got.BatchPrompt)
}

func TestPromptService_GetPromptConfig_MalformedJSONFallsBackToDefaults(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AppConfig{}))
	configRepo := repository.NewConfigRepository(db)
	// 写入非法 JSON
	require.NoError(t, configRepo.Set(PromptConfigKey, "{not valid json"))
	svc := NewPromptService(configRepo)

	got, err := svc.GetPromptConfig()
	require.NoError(t, err) // 应该不报错
	defaults := model.GetDefaultPromptConfig()
	assert.Equal(t, defaults.AnalysisPrompt, got.AnalysisPrompt)
}

func TestPromptService_SetPromptConfig_NilReturnsError(t *testing.T) {
	svc := setupPromptServiceForTest(t)
	err := svc.SetPromptConfig(nil)
	assert.Error(t, err)
}

func TestPromptService_SetPromptConfig_PersistsToRepo(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AppConfig{}))
	configRepo := repository.NewConfigRepository(db)
	svc := NewPromptService(configRepo)

	cfg := &model.PromptConfig{AnalysisPrompt: "test prompt"}
	require.NoError(t, svc.SetPromptConfig(cfg))

	// Verify the value was written to the repo directly
	appCfg, err := configRepo.Get(PromptConfigKey)
	require.NoError(t, err)
	var got model.PromptConfig
	require.NoError(t, json.Unmarshal([]byte(appCfg.Value), &got))
	assert.Equal(t, "test prompt", got.AnalysisPrompt)
}

func TestPromptService_ResetToDefaults_RestoresDefaults(t *testing.T) {
	svc := setupPromptServiceForTest(t)
	custom := &model.PromptConfig{
		AnalysisPrompt: "custom",
		CaptionPrompt:  "custom",
		BatchPrompt:    "custom",
	}
	require.NoError(t, svc.SetPromptConfig(custom))

	require.NoError(t, svc.ResetToDefaults())

	got, err := svc.GetPromptConfig()
	require.NoError(t, err)
	defaults := model.GetDefaultPromptConfig()
	assert.Equal(t, defaults.AnalysisPrompt, got.AnalysisPrompt)
	assert.Equal(t, defaults.CaptionPrompt, got.CaptionPrompt)
	assert.Equal(t, defaults.BatchPrompt, got.BatchPrompt)
}
