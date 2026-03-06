package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupRuntimeTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AnalysisRuntimeLease{}))

	return db
}

func TestAnalysisRuntimeService_AcquireReleaseAndConflict(t *testing.T) {
	db := setupRuntimeTestDB(t)
	svc := NewAnalysisRuntimeService(db)

	lease, err := svc.AcquireGlobal(model.AnalysisOwnerTypeBatch, "task_1", "在线批量分析运行中")
	require.NoError(t, err)
	require.Equal(t, model.AnalysisOwnerTypeBatch, lease.OwnerType)
	require.Equal(t, "task_1", lease.OwnerID)

	status, err := svc.GetGlobalStatus()
	require.NoError(t, err)
	require.True(t, status.IsActive)
	require.Equal(t, model.AnalysisOwnerTypeBatch, status.OwnerType)

	_, err = svc.AcquireGlobal(model.AnalysisOwnerTypeAnalyzer, "analyzer_1", "离线 analyzer 运行中")
	require.ErrorIs(t, err, ErrAnalysisRuntimeBusy)

	require.NoError(t, svc.ReleaseGlobal(model.AnalysisOwnerTypeBatch, "task_1"))

	status, err = svc.GetGlobalStatus()
	require.NoError(t, err)
	require.False(t, status.IsActive)
	require.Equal(t, model.AnalysisRuntimeStatusIdle, status.Status)

	lease, err = svc.AcquireGlobal(model.AnalysisOwnerTypeAnalyzer, "analyzer_1", "离线 analyzer 运行中")
	require.NoError(t, err)
	require.Equal(t, model.AnalysisOwnerTypeAnalyzer, lease.OwnerType)
	require.Equal(t, "analyzer_1", lease.OwnerID)
}

func TestAnalysisRuntimeService_HeartbeatExtendsLease(t *testing.T) {
	db := setupRuntimeTestDB(t)
	svc := NewAnalysisRuntimeService(db)

	lease, err := svc.AcquireGlobal(model.AnalysisOwnerTypeAnalyzer, "analyzer_heartbeat", "离线 analyzer 运行中")
	require.NoError(t, err)
	require.NotNil(t, lease.LeaseExpiresAt)
	originalExpiresAt := *lease.LeaseExpiresAt

	time.Sleep(20 * time.Millisecond)

	lease, err = svc.HeartbeatGlobal(model.AnalysisOwnerTypeAnalyzer, "analyzer_heartbeat")
	require.NoError(t, err)
	require.NotNil(t, lease.LeaseExpiresAt)
	require.True(t, lease.LeaseExpiresAt.After(originalExpiresAt))

	err = svc.ReleaseGlobal(model.AnalysisOwnerTypeBatch, "other_owner")
	require.ErrorIs(t, err, ErrAnalysisRuntimeOwnedByOther)
}
