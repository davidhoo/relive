package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnalysisService_Stats_PendingExcludesFinalFailed
// Pending 不包含最终失败。
func TestAnalysisService_Stats_PendingExcludesFinalFailed(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	svc := newAnalysisServiceWithQueue(db, wq)
	seedAnalysisPhoto(t, db, 21001)

	// 推到最终失败。
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", 21001).
		Updates(map[string]interface{}{
			"analysis_retry_count": AnalysisMaxAttempts,
		}).Error)

	stats, err := svc.GetStats(0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.Pending, "Pending 不应包含最终失败照片")
	assert.Equal(t, int64(1), stats.Failed, "Failed 应统计最终失败")
	assert.Equal(t, int64(0), stats.RetryWaiting, "终态不在 retry_waiting")
}

// TestAnalysisService_Stats_RetryWaiting
// 新增 RetryWaiting：next_retry_at 未到。
func TestAnalysisService_Stats_RetryWaiting(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	svc := newAnalysisServiceWithQueue(db, wq)
	seedAnalysisPhoto(t, db, 22001)

	future := time.Now().Add(1 * time.Hour)
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", 22001).
		Updates(map[string]interface{}{
			"analysis_retry_count":   1,
			"analysis_next_retry_at": &future,
		}).Error)

	stats, err := svc.GetStats(0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.Pending, "退避未到不应在 Pending")
	assert.Equal(t, int64(1), stats.RetryWaiting, "应计入 RetryWaiting")
	assert.Equal(t, int64(0), stats.Failed)
}

// TestAnalysisService_Stats_FailedOnlyMaxAttempts
// Failed 只统计达到统一 max attempts 的任务。
func TestAnalysisService_Stats_FailedOnlyMaxAttempts(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	svc := newAnalysisServiceWithQueue(db, wq)
	seedAnalysisPhoto(t, db, 23001) // retry_count = 5，未到 max
	seedAnalysisPhoto(t, db, 23002) // retry_count = max

	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", 23001).
		Update("analysis_retry_count", 5).Error)
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", 23002).
		Update("analysis_retry_count", AnalysisMaxAttempts).Error)

	stats, err := svc.GetStats(0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.Failed, "只有达到 max attempts 才算 Failed")
	// 23001 退避未设但 retry_count<max 且 next_retry_at 为空 → pending
	assert.Equal(t, int64(1), stats.Pending)
}

// TestAnalysisService_Stats_LockedRequiresUnanalyzed
// Locked 同时要求 ai_analyzed=false。
func TestAnalysisService_Stats_LockedRequiresUnanalyzed(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	svc := newAnalysisServiceWithQueue(db, wq)
	seedAnalysisPhoto(t, db, 24001)

	// 持有未过期锁且未分析。
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", 24001).
		Updates(map[string]interface{}{
			"analysis_lock_id":         "analyzer-A",
			"analysis_lock_expired_at": time.Now().Add(3 * time.Minute),
		}).Error)

	stats, err := svc.GetStats(0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stats.Locked)

	// 标记已分析后，即使锁未清，也不应计入 Locked。
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", 24001).
		Update("ai_analyzed", true).Error)
	stats, err = svc.GetStats(0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), stats.Locked, "Locked 要求 ai_analyzed=false")
}

// TestAnalysisService_SubmitResultsClearsLockAndRetry
// SubmitResults 成功清空 lock、attempts、next-retry 和 last-error。
func TestAnalysisService_SubmitResultsClearsLockAndRetry(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 25001)
	svc := newAnalysisServiceWithQueue(db, wq)

	now := time.Now()
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", photo.ID).
		Updates(map[string]interface{}{
			"analysis_lock_id":         "analyzer-A",
			"analysis_lock_expired_at": now.Add(5 * time.Minute),
			"analysis_retry_count":     2,
			"analysis_next_retry_at":   &now,
			"analysis_last_error_code": "provider_transient",
			"analysis_last_error":      "502",
			"analysis_last_failed_at":  &now,
		}).Error)

	resp, err := svc.SubmitResultsDirectly([]model.AnalysisResult{
		{
			PhotoID:      photo.ID,
			Description:  "描述",
			MemoryScore:  70,
			BeautyScore:  60,
			MainCategory: "日常",
		},
	}, 0)
	require.NoError(t, err)
	require.Equal(t, 1, resp.Accepted)

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.Nil(t, got.AnalysisLockID)
	assert.Nil(t, got.AnalysisLockExpiredAt)
	assert.Equal(t, 0, got.AnalysisRetryCount)
	assert.Nil(t, got.AnalysisNextRetryAt)
	assert.Empty(t, got.AnalysisLastErrorCode)
	assert.Empty(t, got.AnalysisLastError)
	assert.Nil(t, got.AnalysisLastFailedAt)
}
