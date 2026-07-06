package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/lifecycle"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/internal/testutil"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func init() {
	gin.SetMode(gin.TestMode)
	testutil.SuppressLogger()
}

// setupRouterTestDB 构造一个内存 SQLite 库并完成全部 AutoMigrate。
func setupRouterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))
	return db
}

// newAuthedRouterForTest 构造一个完整 router.Setup 引擎（legacy 模式），返回引擎与
// 一个有效 JWT token（admin 用户）。WriteQueue 需在 router.Setup 调用前初始化。
func newAuthedRouterForTest(t *testing.T) (*gin.Engine, *service.Services, string) {
	t.Helper()
	db := setupRouterTestDB(t)

	dbPath := filepath.Join(t.TempDir(), "router-service.db")
	cfg := &config.Config{
		Server:   config.ServerConfig{Mode: "release"},
		Security: config.SecurityConfig{JWTSecret: "router-test-secret"},
		Database: config.DatabaseConfig{Type: "sqlite", Path: dbPath},
		Photos:   config.PhotosConfig{ThumbnailPath: t.TempDir()},
		People:   config.PeopleConfig{IdentityProfileMode: "legacy"},
	}

	// WriteQueue 必须在 NewServices 前初始化（NewServices 内部 GetWriteQueue）。
	database.InitWriteQueue()

	repos := repository.NewRepositories(db)
	services := service.NewServices(repos, cfg, db)

	appState := lifecycle.NewState()
	engine, _ := Setup(db, cfg, appState)

	// 创建 admin 用户并签发 token。NewServices 内部 InitializeDefaultUser 已创建 admin
	// (IsFirstLogin=true)。ValidateToken 拒绝 user.UpdatedAt 晚于 token.IssuedAt 的 token，
	// 且 SQLite 时间精度为秒；FirstLoginCheck 在 IsFirstLogin=true 时返回 403。因此：
	// 1) UpdateFirstLoginStatus(false) 解除首次登录限制（会推进 UpdatedAt）；
	// 2) sleep 1.1s 确保后续 token.IssuedAt 严格晚于 UpdatedAt（沿用
	//    TestAuthService_GenerateAndValidateToken 的做法）。
	authSvc := service.NewAuthService(repos.User, cfg)
	var existing model.User
	require.NoError(t, db.First(&existing, 1).Error)
	require.NoError(t, repos.User.UpdateFirstLoginStatus(existing.ID, false))
	time.Sleep(1100 * time.Millisecond)
	token, _, err := authSvc.GenerateToken(existing.ID, existing.Username)
	require.NoError(t, err)

	return engine, services, token
}

// TestRouter_IdentityProfileStats_UnauthorizedReturns401 验证未认证请求返回 401。
func TestRouter_IdentityProfileStats_UnauthorizedReturns401(t *testing.T) {
	engine, _, _ := newAuthedRouterForTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/people/identity-profiles/stats", nil)
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestRouter_IdentityProfileDecisions_UnauthorizedReturns401 验证未认证请求返回 401。
func TestRouter_IdentityProfileDecisions_UnauthorizedReturns401(t *testing.T) {
	engine, _, _ := newAuthedRouterForTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/people/identity-profiles/decisions?limit=10", nil)
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestRouter_IdentityProfileStats_AuthedReturnsLegacy 验证已认证请求在 legacy 模式下
// 返回 mode=legacy 与零值运行状态。
func TestRouter_IdentityProfileStats_AuthedReturnsLegacy(t *testing.T) {
	engine, _, token := newAuthedRouterForTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/people/identity-profiles/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp model.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)

	stats, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	var op model.IdentityProfileOperationalStatsResponse
	require.NoError(t, json.Unmarshal(stats, &op))
	assert.Equal(t, "legacy", op.Mode)
	assert.False(t, op.ANN.Ready)
	assert.Zero(t, op.Profiles.Total)
}

// TestRouter_IdentityProfileDecisions_AuthedReturnsEmpty 验证已认证请求在空表时返回 items:[]。
func TestRouter_IdentityProfileDecisions_AuthedReturnsEmpty(t *testing.T) {
	engine, _, token := newAuthedRouterForTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/people/identity-profiles/decisions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	var resp model.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)

	data, err := json.Marshal(resp.Data)
	require.NoError(t, err)
	var list model.IdentityDecisionListResponse
	require.NoError(t, json.Unmarshal(data, &list))
	assert.NotNil(t, list.Items)
	assert.Empty(t, list.Items)
	assert.Equal(t, 50, list.Limit)
}

// TestRouter_IdentityProfilesNotCapturedByID 验证 identity-profiles 路径不会被 /:id 捕获。
// 如果路由顺序错误，GET /people/identity-profiles/stats 会被解析成 GetPerson(id="identity-profiles")
// 从而返回 400（invalid id）。这里断言返回 200（stats）而非 400。
func TestRouter_IdentityProfilesNotCapturedByID(t *testing.T) {
	engine, _, token := newAuthedRouterForTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/people/identity-profiles/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(rec, req)

	assert.NotEqual(t, http.StatusBadRequest, rec.Code, "identity-profiles must not fall into /:id")
	assert.Equal(t, http.StatusOK, rec.Code)
}

// TestRouter_IdentityProfileStats_GETOnly 验证非 GET 方法不被路由（404 NoRoute）。
func TestRouter_IdentityProfileStats_GETOnly(t *testing.T) {
	engine, _, token := newAuthedRouterForTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/people/identity-profiles/stats", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code, "POST not registered -> 404 (NoRoute)")
}
