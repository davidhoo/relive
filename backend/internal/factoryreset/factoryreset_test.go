package factoryreset

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_ = logger.Init(config.LoggingConfig{Level: "error", Console: false})
}

func TestApplyPending_RemovesDatabaseFilesAndManagedDirectories(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "relive.db")
	thumbPath := filepath.Join(dir, "thumbs")
	displayPath := util.DisplayBatchRoot(thumbPath)
	cachePath := filepath.Join(dir, "cache")

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Type: "sqlite",
			Path: dbPath,
		},
		Photos: config.PhotosConfig{
			ThumbnailPath: thumbPath,
		},
	}

	require.NoError(t, os.WriteFile(dbPath, []byte("db"), 0o644))
	require.NoError(t, os.WriteFile(dbPath+"-wal", []byte("wal"), 0o644))
	require.NoError(t, os.WriteFile(dbPath+"-shm", []byte("shm"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(thumbPath, "nested"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(displayPath, "nested"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(cachePath, "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(thumbPath, "nested", "a.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(displayPath, "nested", "b.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(cachePath, "nested", "c.txt"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(MarkerPath(cfg), []byte("pending"), 0o644))

	applied, err := ApplyPending(cfg)
	require.NoError(t, err)
	assert.True(t, applied)

	assert.NoFileExists(t, MarkerPath(cfg))
	assert.NoFileExists(t, dbPath)
	assert.NoFileExists(t, dbPath+"-wal")
	assert.NoFileExists(t, dbPath+"-shm")
	assertDirEmpty(t, thumbPath)
	assertDirEmpty(t, displayPath)
	assertDirEmpty(t, cachePath)
}

func TestApplyPending_WithoutMarkerDoesNothing(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "relive.db")
	thumbPath := filepath.Join(dir, "thumbs")

	cfg := &config.Config{
		Database: config.DatabaseConfig{
			Type: "sqlite",
			Path: dbPath,
		},
		Photos: config.PhotosConfig{
			ThumbnailPath: thumbPath,
		},
	}

	require.NoError(t, os.WriteFile(dbPath, []byte("db"), 0o644))

	applied, err := ApplyPending(cfg)
	require.NoError(t, err)
	assert.False(t, applied)
	assert.FileExists(t, dbPath)
}

func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 0)
}
