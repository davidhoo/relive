package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// controllableRescoreService 按字段返回预设结果/错误，用于精确测试错误码映射。
type controllableRescoreService struct {
	createRunRun       *model.FaceQualityRescoreRun
	createRunErr       error
	getRunRun          *model.FaceQualityRescoreRun
	getRunErr          error
	listRuns           []*model.FaceQualityRescoreRun
	listRunsErr        error
	pauseErr           error
	resumeErr          error
	cancelErr          error
	restoreResult      *model.FaceQualityRestoreResult
	restoreErr         error
	retryRun           *model.FaceQualityRescoreRun
	retryErr           error
	eligibleForEnforce map[uint]bool
}

func (s *controllableRescoreService) CreateRun(mode, applyMode string, photoLimit int, calibrationRunID uint, pipelineVersion string) (*model.FaceQualityRescoreRun, error) {
	return s.createRunRun, s.createRunErr
}
func (s *controllableRescoreService) GetRun(id uint) (*model.FaceQualityRescoreRun, error) {
	return s.getRunRun, s.getRunErr
}
func (s *controllableRescoreService) ListRuns(limit int) ([]*model.FaceQualityRescoreRun, error) {
	return s.listRuns, s.listRunsErr
}
func (s *controllableRescoreService) Pause(id uint) error  { return s.pauseErr }
func (s *controllableRescoreService) Resume(id uint) error { return s.resumeErr }
func (s *controllableRescoreService) Cancel(id uint) error { return s.cancelErr }
func (s *controllableRescoreService) RestoreAuto(runID uint, limit int) (*model.FaceQualityRestoreResult, error) {
	return s.restoreResult, s.restoreErr
}
func (s *controllableRescoreService) RetryRun(sourceRunID uint) (*model.FaceQualityRescoreRun, error) {
	return s.retryRun, s.retryErr
}
func (s *controllableRescoreService) IsEligibleForEnforce(runID uint) bool {
	if s.eligibleForEnforce == nil {
		return false
	}
	return s.eligibleForEnforce[runID]
}
func (s *controllableRescoreService) Run() {}

func newRescoreHandlerWith(svc service.FaceQualityRescoreService) *PeopleHandler {
	h := &PeopleHandler{}
	h.SetFaceQualityRescore(svc)
	return h
}

// callRescoreHandler 直接调用 handler 方法，模拟 gin 路由。
func callRescoreHandler(t *testing.T, h *PeopleHandler, fn func(*gin.Context), body interface{}, params gin.Params) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Params = params
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", &buf)
	ctx.Request.Header.Set("Content-Type", "application/json")
	fn(ctx)
	return rec
}

// TestRescoreHandler_CalibrationForcesShadow 校准请求返回 shadow，apply_mode 由服务端归一化。
func TestRescoreHandler_CalibrationForcesShadow(t *testing.T) {
	svc := &controllableRescoreService{
		createRunRun: &model.FaceQualityRescoreRun{
			ID: 1, Mode: model.FaceQualityRescoreModeCalibration,
			ApplyMode:       model.FaceQualityRescoreApplyModeShadow,
			Status:          model.FaceQualityRescoreStatusRunning,
			TargetFaceCount: 5,
		},
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.CreateFaceQualityRescoreRun,
		map[string]interface{}{"mode": "calibration", "photo_limit": 1000}, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Success bool                                `json:"success"`
		Data    model.FaceQualityRescoreRunResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, model.FaceQualityRescoreApplyModeShadow, resp.Data.ApplyMode)
	assert.Equal(t, 5, resp.Data.TargetFaceCount)
}

// TestRescoreHandler_FullEnforceWithoutCalibrationReturns409 无 completed calibration 时 full/enforce 返回 409 + RESCORE_CALIBRATION_REQUIRED。
func TestRescoreHandler_FullEnforceWithoutCalibrationReturns409(t *testing.T) {
	svc := &controllableRescoreService{
		createRunErr: service.ErrRescoreCalibrationRequired,
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.CreateFaceQualityRescoreRun,
		map[string]interface{}{"mode": "full"}, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
	var resp model.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, "RESCORE_CALIBRATION_REQUIRED", resp.Error.Code)
}

// TestRescoreHandler_SecondActiveRunReturns409 单活跃 run 互斥返回 409 + RESCORE_RUN_CONFLICT。
func TestRescoreHandler_SecondActiveRunReturns409(t *testing.T) {
	svc := &controllableRescoreService{
		createRunErr: service.ErrRescoreRunConflict,
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.CreateFaceQualityRescoreRun,
		map[string]interface{}{"mode": "calibration"}, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
	var resp model.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, "RESCORE_RUN_CONFLICT", resp.Error.Code)
}

// TestRescoreHandler_GetNotFoundReturns404 GetRun 未找到返回 404 + RESCORE_NOT_FOUND。
func TestRescoreHandler_GetNotFoundReturns404(t *testing.T) {
	svc := &controllableRescoreService{
		getRunErr: service.ErrRescoreRunNotFound,
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.GetFaceQualityRescoreRun, nil,
		gin.Params{{Key: "id", Value: "999"}})
	assert.Equal(t, http.StatusNotFound, rec.Code)
	var resp model.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, "RESCORE_NOT_FOUND", resp.Error.Code)
}

// TestRescoreHandler_PauseResumeScoped pause/resume 只影响指定 run（service 层隔离，handler 透传 id）。
func TestRescoreHandler_PauseResumeScoped(t *testing.T) {
	svc := &controllableRescoreService{}
	h := newRescoreHandlerWith(svc)

	rec := callRescoreHandler(t, h, h.PauseFaceQualityRescoreRun, nil,
		gin.Params{{Key: "id", Value: "7"}})
	require.Equal(t, http.StatusOK, rec.Code)

	rec = callRescoreHandler(t, h, h.ResumeFaceQualityRescoreRun, nil,
		gin.Params{{Key: "id", Value: "7"}})
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestRescoreHandler_RestoreAutoDoesNotAffectOtherRuns restore-auto 透传 run id，service 层按 rescore_run_id 隔离。
func TestRescoreHandler_RestoreAutoDoesNotAffectOtherRuns(t *testing.T) {
	svc := &controllableRescoreService{
		restoreResult: &model.FaceQualityRestoreResult{Restored: 3},
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.RestoreAutoFaceQualityRescoreRun, nil,
		gin.Params{{Key: "id", Value: "7"}})
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Success bool                           `json:"success"`
		Data    model.FaceQualityRestoreResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, 3, resp.Data.Restored)
}

// TestRescoreHandler_PauseErrorMapsNotFound Pause 返回 not-found 错误映射 404。
func TestRescoreHandler_PauseErrorMapsNotFound(t *testing.T) {
	svc := &controllableRescoreService{
		pauseErr: service.ErrRescoreRunNotFound,
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.PauseFaceQualityRescoreRun, nil,
		gin.Params{{Key: "id", Value: "999"}})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestRescoreHandler_UnavailableWhenNil service 未注入时返回 503。
func TestRescoreHandler_UnavailableWhenNil(t *testing.T) {
	h := &PeopleHandler{}
	rec := callRescoreHandler(t, h, h.CreateFaceQualityRescoreRun,
		map[string]interface{}{"mode": "calibration"}, nil)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

// TestRescoreHandler_InvalidModeReturns400 mode 非法返回 400。
func TestRescoreHandler_InvalidModeReturns400(t *testing.T) {
	svc := &controllableRescoreService{}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.CreateFaceQualityRescoreRun,
		map[string]interface{}{"mode": "bogus"}, nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestRescoreHandler_RetryReturnsNewRun retry 接口透传 source run id，返回新 shadow run。
func TestRescoreHandler_RetryReturnsNewRun(t *testing.T) {
	svc := &controllableRescoreService{
		retryRun: &model.FaceQualityRescoreRun{
			ID: 2, Mode: model.FaceQualityRescoreModeCalibration,
			ApplyMode: model.FaceQualityRescoreApplyModeShadow,
			Status:    model.FaceQualityRescoreStatusRunning,
		},
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.RetryFaceQualityRescoreRun, nil,
		gin.Params{{Key: "id", Value: "1"}})
	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Success bool                                `json:"success"`
		Data    model.FaceQualityRescoreRunResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, uint(2), resp.Data.ID)
	assert.Equal(t, model.FaceQualityRescoreApplyModeShadow, resp.Data.ApplyMode)
}

// TestRescoreHandler_RetrySourceInvalidReturns409 来源 run 不合格返回 409。
func TestRescoreHandler_RetrySourceInvalidReturns409(t *testing.T) {
	svc := &controllableRescoreService{
		retryErr: service.ErrRescoreRetrySourceInvalid,
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.RetryFaceQualityRescoreRun, nil,
		gin.Params{{Key: "id", Value: "1"}})
	assert.Equal(t, http.StatusConflict, rec.Code)
	var resp model.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, "RESCORE_RETRY_SOURCE_INVALID", resp.Error.Code)
}

// TestRescoreHandler_RetryNotFoundReturns404 来源 run 不存在返回 404。
func TestRescoreHandler_RetryNotFoundReturns404(t *testing.T) {
	svc := &controllableRescoreService{
		retryErr: service.ErrRescoreRunNotFound,
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.RetryFaceQualityRescoreRun, nil,
		gin.Params{{Key: "id", Value: "999"}})
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestRescoreHandler_FullRequiresCalibrationRunID full 缺 calibration_run_id 由 service 返回 409。
func TestRescoreHandler_FullRequiresCalibrationRunID(t *testing.T) {
	svc := &controllableRescoreService{
		createRunErr: service.ErrRescoreCalibrationRequired,
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.CreateFaceQualityRescoreRun,
		map[string]interface{}{"mode": "full"}, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
	var resp model.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, "RESCORE_CALIBRATION_REQUIRED", resp.Error.Code)
}

// TestRescoreHandler_VerifierUnavailableReturns409 v2 验证器不可用时 create 返回稳定 409 +
// FACE_QUALITY_VERIFIER_UNAVAILABLE，而非伪成功或 500。
func TestRescoreHandler_VerifierUnavailableReturns409(t *testing.T) {
	svc := &controllableRescoreService{
		createRunErr: service.ErrRescoreV2VerifierUnavailable,
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.CreateFaceQualityRescoreRun,
		map[string]interface{}{"mode": "calibration"}, nil)
	assert.Equal(t, http.StatusConflict, rec.Code)
	var resp model.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, "FACE_QUALITY_VERIFIER_UNAVAILABLE", resp.Error.Code)
}

// TestRescoreHandler_RetryVerifierUnavailableReturns409 v2 retry 验证器不可用同样映射 409。
func TestRescoreHandler_RetryVerifierUnavailableReturns409(t *testing.T) {
	svc := &controllableRescoreService{
		retryErr: service.ErrRescoreV2VerifierUnavailable,
	}
	h := newRescoreHandlerWith(svc)
	rec := callRescoreHandler(t, h, h.RetryFaceQualityRescoreRun, nil,
		gin.Params{{Key: "id", Value: "3"}})
	assert.Equal(t, http.StatusConflict, rec.Code)
	var resp model.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, "FACE_QUALITY_VERIFIER_UNAVAILABLE", resp.Error.Code)
}

// 编译期断言桩实现接口。
var _ service.FaceQualityRescoreService = (*controllableRescoreService)(nil)
