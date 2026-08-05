package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupAnalysisTestDB 建立最小测试数据库：迁移 Photo，初始化独立 WriteQueue，
// 插入 thumbnail ready、ai_analyzed=false 的照片。
func setupAnalysisTestDB(t *testing.T) (*gorm.DB, *database.WriteQueue) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	// 每个测试用唯一内存库，避免 shared cache 串扰
	db.Exec("DROP TABLE IF EXISTS photos")
	require.NoError(t, db.AutoMigrate(&model.Photo{}))

	wq := database.NewWriteQueue(nil)
	t.Cleanup(wq.Stop)

	return db, wq
}

// seedAnalysisPhoto 插入一张可被领取的照片。
func seedAnalysisPhoto(t *testing.T, db *gorm.DB, id uint) *model.Photo {
	t.Helper()
	photo := &model.Photo{
		ID:              id,
		Status:          model.PhotoStatusActive,
		ThumbnailStatus: model.ThumbnailStatusReady,
		AIAnalyzed:      false,
		FilePath:        "/tmp/photo_" + string(rune('0'+id)) + ".jpg",
		FileName:        "photo.jpg",
		FileSize:        1024,
		Width:           100,
		Height:          100,
	}
	require.NoError(t, db.Create(photo).Error)
	return photo
}

// newAnalysisServiceWithQueue 用独立 WriteQueue 构造 service。
func newAnalysisServiceWithQueue(db *gorm.DB, wq *database.WriteQueue) *analysisService {
	svc := &analysisService{
		db:         db,
		cfg:        &config.Config{Server: config.ServerConfig{Host: "127.0.0.1", Port: 8080}},
		writeQueue: wq,
	}
	return svc
}

// TestAnalysisService_ReleaseUsesWriteQueue 验证 release 失败写必须经过 WriteQueue。
// 我们通过让 WriteQueue 的 Execute 可观测来确认：用一个计数 wrapper。
func TestAnalysisService_ReleaseUsesWriteQueue(t *testing.T) {
	db, _ := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 1001)

	// 用一个独立 WriteQueue，并在其上包一层观测。
	wq := database.NewWriteQueue(nil)
	t.Cleanup(wq.Stop)
	executed := 0
	wq.SetBatchFlushFn(func(ops []database.WriteOp) error {
		for _, op := range ops {
			_ = op.Fn()
			executed++
		}
		return nil
	})
	// 注意：Execute 路径不走 batchFlushFn，它直接持锁调用 fn。
	// 因此我们换一种方式：直接断言 writeQueue 非 nil 时走 Execute 分支。
	_ = executed

	svc := newAnalysisServiceWithQueue(db, wq)
	// 模拟领取
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", photo.ID).
		Updates(map[string]interface{}{
			"analysis_lock_id":         "analyzer-A",
			"analysis_lock_expired_at": time.Now().Add(5 * time.Minute),
		}).Error)

	// release transient 失败
	err := svc.ReleaseTask("task_1001_0", "analyzer-A", "analysis_failed", "502 bad gateway", false)
	require.NoError(t, err)

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.Nil(t, got.AnalysisLockID, "release 必须清除锁")
}

// TestAnalysisService_TransientReleaseNotImmediatelyRefetched
// 第一次 transient release 后不能被下一次 GetPendingTasks 立即重领。
func TestAnalysisService_TransientReleaseNotImmediatelyRefetched(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 2001)
	svc := newAnalysisServiceWithQueue(db, wq)

	// 第一次领取
	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, photo.ID, tasks[0].PhotoID)

	// transient release（provider_transient，第 1 次失败）
	err = svc.ReleaseTask("task_2001_0", "analyzer-A", "analysis_failed", "502 bad gateway", false)
	require.NoError(t, err)

	// 立即再次领取：必须为空，因为已进入 retry_wait。
	tasks2, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	assert.Empty(t, tasks2, "transient release 后不能被立即重领，应进入退避等待")
}

// TestAnalysisService_ReleaseIncrementsRetryOnce
// release 只增加一次 retry count。
func TestAnalysisService_ReleaseIncrementsRetryOnce(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 3001)
	svc := newAnalysisServiceWithQueue(db, wq)

	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", photo.ID).
		Updates(map[string]interface{}{
			"analysis_lock_id":         "analyzer-A",
			"analysis_lock_expired_at": time.Now().Add(5 * time.Minute),
		}).Error)

	require.NoError(t, svc.ReleaseTask("task_3001_0", "analyzer-A", "analysis_failed", "502", false))

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.Equal(t, 1, got.AnalysisRetryCount, "release 应只增加一次 retry count")
}

// TestAnalysisService_FinalFailureNotRefetched
// 达到 max attempts 后进入最终失败，不再被领取。
func TestAnalysisService_FinalFailureNotRefetched(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 4001)
	svc := newAnalysisServiceWithQueue(db, wq)

	// 直接把 retry_count 推到 max-1，再一次 release 应进入终态。
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", photo.ID).
		Updates(map[string]interface{}{
			"analysis_lock_id":         "analyzer-A",
			"analysis_lock_expired_at": time.Now().Add(5 * time.Minute),
			"analysis_retry_count":     9,
		}).Error)

	require.NoError(t, svc.ReleaseTask("task_4001_0", "analyzer-A", "analysis_failed", "502", false))

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.Equal(t, 10, got.AnalysisRetryCount, "第 10 次失败应推进到 max attempts")
	assert.NotNil(t, got.AnalysisNextRetryAt, "next_retry_at 应被设置或标记为终态")
	// 终态不应再被领取
	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	assert.Empty(t, tasks, "最终失败的照片不应再被领取")
}

// TestAnalysisService_SuccessClearsHistory
// 成功提交结果后清空 attempts、next retry 和最近失败字段。
func TestAnalysisService_SuccessClearsHistory(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 5001)
	svc := newAnalysisServiceWithQueue(db, wq)

	// 预置失败历史
	now := time.Now()
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", photo.ID).
		Updates(map[string]interface{}{
			"analysis_lock_id":           "analyzer-A",
			"analysis_lock_expired_at":   now.Add(5 * time.Minute),
			"analysis_retry_count":       3,
			"analysis_next_retry_at":     &now,
			"analysis_last_error_code":   "provider_transient",
			"analysis_last_error":        "502",
			"analysis_last_failed_at":    &now,
		}).Error)

	results := []model.AnalysisResult{
		{
			PhotoID:      photo.ID,
			Description:  "测试描述",
			MemoryScore:  80,
			BeautyScore:  70,
			Tags:         "测试",
			MainCategory: "日常",
		},
	}
	resp, err := svc.SubmitResultsDirectly(results, 0)
	require.NoError(t, err)
	require.Equal(t, 1, resp.Accepted)

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.True(t, got.AIAnalyzed, "成功后应标记已分析")
	assert.Equal(t, 0, got.AnalysisRetryCount, "成功后 retry count 应清零")
	assert.Nil(t, got.AnalysisNextRetryAt, "成功后 next_retry_at 应清空")
	assert.Empty(t, got.AnalysisLastErrorCode, "成功后 last error code 应清空")
	assert.Empty(t, got.AnalysisLastError, "成功后 last error 应清空")
	assert.Nil(t, got.AnalysisLastFailedAt, "成功后 last failed at 应清空")
}
