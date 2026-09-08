package service

import (
	"path/filepath"
	"testing"

	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestNewServices_FaceQualityWorkersNotWired 钉死任务 3 启动入口：
// NewServices 不得创建/注入 FaceQuality / Backfill / Rescore。
// main.go 对这三项是 if != nil { Run() }，nil 即保证 worker 不会推进任何质检工作。
func TestNewServices_FaceQualityWorkersNotWired(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(db))

	database.InitWriteQueue()

	cfg := &config.Config{
		Server:   config.ServerConfig{Mode: "release"},
		Security: config.SecurityConfig{JWTSecret: "face-quality-retired-wiring"},
		Database: config.DatabaseConfig{Type: "sqlite", Path: filepath.Join(t.TempDir(), "wiring.db")},
		Photos:   config.PhotosConfig{ThumbnailPath: t.TempDir()},
		People: config.PeopleConfig{
			// 旧 enforce 配置必须无效：即便写上也不应接线质检服务。
			FaceQualityMode:     "enforce",
			IdentityProfileMode: "legacy",
		},
	}

	repos := repository.NewRepositories(db)
	services := NewServices(repos, cfg, db)

	require.Nil(t, services.FaceQuality, "FaceQuality must stay nil after retirement")
	require.Nil(t, services.FaceQualityBackfill, "FaceQualityBackfill must stay nil so Run() cannot start")
	require.Nil(t, services.FaceQualityRescore, "FaceQualityRescore must stay nil so Run() cannot start")

	// 模拟 main.go 启动分支：nil 时不得进入 Run。
	ranBackfill := false
	if services.FaceQualityBackfill != nil {
		ranBackfill = true
		services.FaceQualityBackfill.Run()
	}
	ranRescore := false
	if services.FaceQualityRescore != nil {
		ranRescore = true
		services.FaceQualityRescore.Run()
	}
	require.False(t, ranBackfill, "main-equivalent backfill Run branch must not execute")
	require.False(t, ranRescore, "main-equivalent rescore Run branch must not execute")
}
