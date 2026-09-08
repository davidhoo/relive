package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 质检/rescore 路由已退役：一律 410，不能成功创建后台工作。
func TestRescoreHandlers_AllReturnGone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &PeopleHandler{}

	calls := []struct {
		name string
		fn   func(*gin.Context)
	}{
		{"create", h.CreateFaceQualityRescoreRun},
		{"list", h.ListFaceQualityRescoreRuns},
		{"get", h.GetFaceQualityRescoreRun},
		{"pause", h.PauseFaceQualityRescoreRun},
		{"resume", h.ResumeFaceQualityRescoreRun},
		{"cancel", h.CancelFaceQualityRescoreRun},
		{"restore", h.RestoreAutoFaceQualityRescoreRun},
		{"retry", h.RetryFaceQualityRescoreRun},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/people/face-quality/rescore-runs", nil)
			c.Params = gin.Params{{Key: "id", Value: "1"}}
			tc.fn(c)
			assert.Equal(t, http.StatusGone, w.Code)
			var resp model.Response
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			require.NotNil(t, resp.Error)
			assert.Equal(t, "FACE_QUALITY_RETIRED", resp.Error.Code)
		})
	}
}
