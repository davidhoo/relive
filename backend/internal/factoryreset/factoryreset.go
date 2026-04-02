package factoryreset

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

const markerFileName = ".factory-reset-pending"

var ErrUnsupportedDatabase = errors.New("factory reset only supports sqlite")

func MarkerPath(cfg *config.Config) string {
	dbPath := databasePath(cfg)
	return filepath.Join(filepath.Dir(dbPath), markerFileName)
}

func Schedule(cfg *config.Config) error {
	if !isSQLite(cfg) {
		return ErrUnsupportedDatabase
	}

	markerPath := MarkerPath(cfg)
	if err := os.MkdirAll(filepath.Dir(markerPath), 0o755); err != nil {
		return fmt.Errorf("create reset marker dir: %w", err)
	}
	if err := os.WriteFile(markerPath, []byte("pending\n"), 0o644); err != nil {
		return fmt.Errorf("write reset marker: %w", err)
	}

	return nil
}

func ApplyPending(cfg *config.Config) (bool, error) {
	if !isSQLite(cfg) {
		return false, nil
	}

	markerPath := MarkerPath(cfg)
	if _, err := os.Stat(markerPath); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat reset marker: %w", err)
	}

	logger.Infof("Applying pending factory reset from marker %s", markerPath)

	for _, path := range databaseFiles(cfg) {
		if err := removeIfExists(path); err != nil {
			return true, err
		}
	}

	for _, dir := range managedDirectories(cfg) {
		if err := clearDirectoryContents(dir); err != nil {
			return true, err
		}
	}

	if err := os.Remove(markerPath); err != nil {
		return true, fmt.Errorf("remove reset marker: %w", err)
	}

	logger.Info("Pending factory reset applied successfully")
	return true, nil
}

func managedDirectories(cfg *config.Config) []string {
	thumbPath := ""
	if cfg != nil {
		thumbPath = strings.TrimSpace(cfg.Photos.ThumbnailPath)
	}

	return []string{
		thumbPath,
		util.DisplayBatchRoot(thumbPath),
		cachePath(cfg),
	}
}

func databaseFiles(cfg *config.Config) []string {
	dbPath := databasePath(cfg)
	return []string{
		dbPath,
		dbPath + "-wal",
		dbPath + "-shm",
	}
}

func databasePath(cfg *config.Config) string {
	if cfg == nil {
		return filepath.Clean("./data/relive.db")
	}

	dbPath := strings.TrimSpace(cfg.Database.Path)
	if dbPath == "" {
		dbPath = "./data/relive.db"
	}

	return filepath.Clean(dbPath)
}

func cachePath(cfg *config.Config) string {
	return filepath.Join(filepath.Dir(databasePath(cfg)), "cache")
}

func isSQLite(cfg *config.Config) bool {
	if cfg == nil {
		return true
	}
	dbType := strings.ToLower(strings.TrimSpace(cfg.Database.Type))
	return dbType == "" || dbType == "sqlite"
}

func clearDirectoryContents(dirPath string) error {
	if strings.TrimSpace(dirPath) == "" {
		return nil
	}
	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return fmt.Errorf("ensure directory %s: %w", dirPath, err)
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fmt.Errorf("read directory %s: %w", dirPath, err)
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dirPath, entry.Name())
		if err := os.RemoveAll(fullPath); err != nil {
			return fmt.Errorf("remove %s: %w", fullPath, err)
		}
	}

	return nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}
