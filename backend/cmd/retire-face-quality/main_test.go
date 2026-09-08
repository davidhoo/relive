package main

import (
	"bytes"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgs_ApplyRequiresMirror(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"-db", "/tmp/x.db", "-apply"}, &stderr)
	if err == nil {
		t.Fatal("expected error when -apply without -mirror")
	}
	if !strings.Contains(stderr.String(), "-mirror") {
		t.Fatalf("stderr should mention -mirror, got: %s", stderr.String())
	}
}

func TestParseArgs_ApplyWithMirrorOK(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseArgs([]string{"-db", "/tmp/x.db", "-apply", "-mirror", "/tmp/m.json", "-plan", "/tmp/approved.json"}, &stderr)
	if err != nil {
		t.Fatalf("unexpected err: %v stderr=%s", err, stderr.String())
	}
	if !opts.apply || opts.mirror != "/tmp/m.json" {
		t.Fatalf("opts=%+v", opts)
	}
}

func TestApplyRequiresApprovedPlan(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseArgs([]string{"-db", "/tmp/x.db", "-apply", "-mirror", "/tmp/m.json"}, &stderr)
	if err == nil || !strings.Contains(stderr.String(), "-plan") {
		t.Fatalf("expected missing approved plan: %v %s", err, stderr.String())
	}
}

func TestCLIRejectsDatabaseChangeSinceReviewedPlan(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "data.db")
	// Create an isolated fixture; never use application data.
	f, err := os.Create(dbPath)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	db, err := openDB(dbPath, true)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()
	require.NoError(t, db.AutoMigrate(&model.Face{}, &model.FaceExclusion{}, &model.FaceQualityEvent{}, &model.FaceQualityRescoreRun{}, &model.AppConfig{}))
	planPath := filepath.Join(dir, "approved.json")
	var stdout, stderr bytes.Buffer
	require.Zero(t, run([]string{"-db", dbPath, "-out", planPath}, &stdout, &stderr), stderr.String())
	require.NoError(t, db.Create(&model.Face{PhotoID: 1, ClusterStatus: model.FaceClusterStatusReviewRequired}).Error)
	stdout.Reset()
	stderr.Reset()
	require.Equal(t, 1, run([]string{"-db", dbPath, "-apply", "-plan", planPath, "-mirror", filepath.Join(dir, "before.json")}, &stdout, &stderr))
	require.Contains(t, stderr.String(), "re-plan required")
	var count int64
	require.NoError(t, db.Model(&model.AppConfig{}).Where("key = ?", service.FaceQualityRetirementMigrationKey).Count(&count).Error)
	require.Zero(t, count)
}
