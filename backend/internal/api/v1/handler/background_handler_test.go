package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toJSON 把 interface{} 序列化为 []byte，供 json.Unmarshal 二次解析（测试辅助）。
func toJSON(v interface{}) []byte {
	b, _ := json.Marshal(v)
	return b
}

// TestBackgroundHandler_GetStatus_ReturnsSnapshot 验证 GET /background/status 返回
// coordinator 快照（foreground/cooldown/running/thresholds），只读无副作用。
func TestBackgroundHandler_GetStatus_ReturnsSnapshot(t *testing.T) {
	coord := service.NewBackgroundTaskCoordinator()
	coord.SetBackgroundConfig(true, 70, 15, 85, nil, 120*time.Second)
	sampler := service.NewBackgroundLoadSamplerForTest()

	// 注册一个 foreground scope + 一个 cooldown，使快照非空。
	release := coord.BeginForeground()
	defer release()
	coord.Cooldown(service.BackgroundTaskMergeSuggestion, 50*time.Millisecond, "db_busy")

	h := NewBackgroundHandler(coord, sampler)
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/background/status", nil, nil, h.GetStatus)
	assert.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)

	var status model.BackgroundTaskStatusResponse
	require.NoError(t, json.Unmarshal(toJSON(resp.Data), &status))
	assert.True(t, status.ForegroundActive)
	assert.Equal(t, 1, status.ForegroundCount)
	assert.True(t, status.AutoTasksEnabled)
	assert.Contains(t, status.Cooldowns, string(service.BackgroundTaskMergeSuggestion))
	assert.Equal(t, 70.0, status.Thresholds.CPUPauseThreshold)
	assert.Equal(t, int64(120000), status.Thresholds.DBLockedCooldownMs)
	assert.NotEmpty(t, status.CapturedAt)
}

// TestBackgroundHandler_GetStatus_NilCoordinatorNoPanic 验证 coordinator 为 nil 时
// 返回空快照不 panic。
func TestBackgroundHandler_GetStatus_NilCoordinatorNoPanic(t *testing.T) {
	h := NewBackgroundHandler(nil, nil)
	rec := performJSONRequest(t, http.MethodGet, "/api/v1/background/status", nil, nil, h.GetStatus)
	assert.Equal(t, http.StatusOK, rec.Code)

	resp := decodeAPIResponse(t, rec)
	assert.True(t, resp.Success)

	var status model.BackgroundTaskStatusResponse
	require.NoError(t, json.Unmarshal(toJSON(resp.Data), &status))
	assert.False(t, status.ForegroundActive)
	assert.True(t, status.AutoTasksEnabled)
	// 负载字段为 unknown（-1）。
	assert.Equal(t, -1.0, status.Load.CPUUserPct)
}
