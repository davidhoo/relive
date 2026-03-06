package handler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidhoo/relive/pkg/config"
)

func TestSystemHandlerGetDatabaseSize(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "relive.db")

	if err := os.WriteFile(dbPath, make([]byte, 128), 0o644); err != nil {
		t.Fatalf("write db file: %v", err)
	}
	if err := os.WriteFile(dbPath+"-wal", make([]byte, 64), 0o644); err != nil {
		t.Fatalf("write wal file: %v", err)
	}

	h := &SystemHandler{
		cfg: &config.Config{
			Database: config.DatabaseConfig{
				Type: "sqlite",
				Path: dbPath,
			},
		},
	}

	if got := h.getDatabaseSize(); got != 192 {
		t.Fatalf("expected database size 192, got %d", got)
	}
}

func TestSystemHandlerGetDatabaseSizeNonSQLite(t *testing.T) {
	h := &SystemHandler{
		cfg: &config.Config{
			Database: config.DatabaseConfig{
				Type: "postgres",
				Path: "/tmp/test.db",
			},
		},
	}

	if got := h.getDatabaseSize(); got != 0 {
		t.Fatalf("expected database size 0 for non-sqlite database, got %d", got)
	}
}
