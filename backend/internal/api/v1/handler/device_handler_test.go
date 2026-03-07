package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newDeviceHandlerForTest(t *testing.T) (*DeviceHandler, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}

	if err := db.AutoMigrate(&model.Device{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	repo := repository.NewDeviceRepository(db)
	svc := service.NewDeviceService(repo, &config.Config{
		Security: config.SecurityConfig{APIKeyPrefix: "sk-relive-"},
	})

	return NewDeviceHandler(svc), db
}

func performJSONRequest(t *testing.T, method, path string, body []byte, params gin.Params, fn func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Params = params
	ctx.Request = httptest.NewRequest(method, path, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	fn(ctx)
	return recorder
}

func decodeAPIResponse(t *testing.T, recorder *httptest.ResponseRecorder) model.Response {
	t.Helper()

	var resp model.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func decodeResponseData[T any](t *testing.T, response model.Response) T {
	t.Helper()

	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}

	var data T
	if err := json.Unmarshal(dataJSON, &data); err != nil {
		t.Fatalf("unmarshal response data: %v", err)
	}
	return data
}

func TestDeviceHandlerCreateDevice(t *testing.T) {
	handler, db := newDeviceHandlerForTest(t)

	body := []byte(`{"name":"Living Room Frame","device_type":"embedded"}`)
	recorder := performJSONRequest(t, http.MethodPost, "/api/v1/devices", body, nil, handler.CreateDevice)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", recorder.Code, recorder.Body.String())
	}

	resp := decodeAPIResponse(t, recorder)
	if !resp.Success {
		t.Fatalf("expected success response, got: %+v", resp)
	}

	created := decodeResponseData[model.CreateDeviceResponse](t, resp)
	if created.ID == 0 {
		t.Fatal("expected created device id")
	}
	if created.DeviceID == "" {
		t.Fatal("expected generated device id")
	}
	if !strings.HasPrefix(created.APIKey, "sk-relive-") {
		t.Fatalf("expected api key prefix sk-relive-, got %s", created.APIKey)
	}
	if created.RenderProfile != util.DefaultRenderProfile() {
		t.Fatalf("expected default render profile %s, got %s", util.DefaultRenderProfile(), created.RenderProfile)
	}

	var stored model.Device
	if err := db.First(&stored, created.ID).Error; err != nil {
		t.Fatalf("load created device: %v", err)
	}
	if stored.Name != "Living Room Frame" {
		t.Fatalf("expected stored name Living Room Frame, got %s", stored.Name)
	}
}

func TestDeviceHandlerUpdateRenderProfile(t *testing.T) {
	handler, db := newDeviceHandlerForTest(t)

	device := &model.Device{
		DeviceID:      "FRAME001",
		Name:          "Frame",
		APIKey:        "sk-relive-test",
		DeviceType:    "embedded",
		IsEnabled:     true,
		RenderProfile: util.DefaultRenderProfile(),
	}
	if err := db.Create(device).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}

	body := []byte(`{"render_profile":"waveshare_7in3e"}`)
	recorder := performJSONRequest(
		t,
		http.MethodPut,
		"/api/v1/devices/1/render-profile",
		body,
		gin.Params{{Key: "id", Value: strconv.FormatUint(uint64(device.ID), 10)}},
		handler.UpdateDeviceRenderProfile,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", recorder.Code, recorder.Body.String())
	}

	var updated model.Device
	if err := db.First(&updated, device.ID).Error; err != nil {
		t.Fatalf("load updated device: %v", err)
	}
	if updated.RenderProfile != "waveshare_7in3e" {
		t.Fatalf("expected render profile waveshare_7in3e, got %s", updated.RenderProfile)
	}
}

func TestDeviceHandlerGetDeviceStats(t *testing.T) {
	handler, db := newDeviceHandlerForTest(t)

	recent := time.Now().Add(-2 * time.Minute)
	old := time.Now().Add(-10 * time.Minute)
	devices := []*model.Device{
		{DeviceID: "EMBED001", Name: "Embedded", APIKey: "sk-relive-1", DeviceType: "embedded", IsEnabled: true, LastHeartbeat: &recent},
		{DeviceID: "OFF001", Name: "Analyzer", APIKey: "sk-relive-2", DeviceType: "offline", IsEnabled: true, LastHeartbeat: &old},
	}
	for _, device := range devices {
		if err := db.Create(device).Error; err != nil {
			t.Fatalf("create device %s: %v", device.DeviceID, err)
		}
	}

	recorder := performJSONRequest(t, http.MethodGet, "/api/v1/devices/stats", nil, nil, handler.GetDeviceStats)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body=%s", recorder.Code, recorder.Body.String())
	}

	resp := decodeAPIResponse(t, recorder)
	stats := decodeResponseData[model.DeviceStatsResponse](t, resp)
	if stats.Total != 2 {
		t.Fatalf("expected total 2, got %d", stats.Total)
	}
	if stats.Online != 1 {
		t.Fatalf("expected online 1, got %d", stats.Online)
	}
	if stats.ByType["embedded"] != 1 {
		t.Fatalf("expected embedded count 1, got %d", stats.ByType["embedded"])
	}
	if stats.ByType["offline"] != 1 {
		t.Fatalf("expected offline count 1, got %d", stats.ByType["offline"])
	}
}
