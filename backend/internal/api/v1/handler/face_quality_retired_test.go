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

func TestFaceQualityRoutes_ReturnGoneWhenRetired(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &PeopleHandler{} // 不注入任何质检服务

	cases := []struct {
		name string
		call func(*gin.Context)
	}{
		{"stats", h.GetFaceQualityStats},
		{"reviews", h.ListFaceQualityReviews},
		{"decision", h.ApplyFaceQualityDecision},
		{"restore-auto", h.RestoreAutoFaceQuality},
		{"backfill-status", h.GetFaceQualityBackfillStatus},
		{"backfill-pause", h.PauseFaceQualityBackfill},
		{"backfill-resume", h.ResumeFaceQualityBackfill},
		{"rescore-create", h.CreateFaceQualityRescoreRun},
		{"rescore-list", h.ListFaceQualityRescoreRuns},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/people/face-quality/"+tc.name, nil)
			tc.call(c)

			assert.Equal(t, http.StatusGone, w.Code)
			var resp model.Response
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.False(t, resp.Success)
		})
	}
}
