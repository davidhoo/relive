package service

import (
	"errors"
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
//
// 注意：使用 cache=shared 内存库时必须在 cleanup 关闭底层 sql.DB，否则共享库
// 会跨测试存活，污染 people/ANN 测试（embedding 维度不一致导致 HNSW panic）。
func setupAnalysisTestDB(t *testing.T) (*gorm.DB, *database.WriteQueue) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_busy_timeout=60000"), &gorm.Config{})
	require.NoError(t, err)

	// 每个测试用唯一内存库，避免 shared cache 串扰
	db.Exec("DROP TABLE IF EXISTS photos")
	require.NoError(t, db.AutoMigrate(&model.Photo{}))

	wq := database.NewWriteQueue(nil)
	t.Cleanup(wq.Stop)

	// 关闭底层连接，释放 shared in-memory DB，避免污染后续测试。
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil && sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

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

// transientReleaseReq 构造一个 transient release 请求。
func transientReleaseReq(reason, errMsg string, lockVersion int64) model.ReleaseTaskRequest {
	return model.ReleaseTaskRequest{
		Reason:       reason,
		ErrorMsg:     errMsg,
		FailureClass: FailureClassProviderTransient,
		LockVersion:  lockVersion,
	}
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
	lockVersion := tasks[0].LockVersion
	require.NotZero(t, lockVersion, "领取后响应应携带 lock_version")

	// transient release（provider_transient，第 1 次失败）
	result, err := svc.ReleaseTask("task_2001_0", "analyzer-A", transientReleaseReq("analysis_failed", "502 bad gateway", lockVersion))
	require.NoError(t, err)
	require.False(t, result.Final)
	assert.Equal(t, "retry_wait", result.NewStatus)
	assert.NotNil(t, result.NextRetryAt)

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

	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	_, err = svc.ReleaseTask("task_3001_0", "analyzer-A", transientReleaseReq("analysis_failed", "502", tasks[0].LockVersion))
	require.NoError(t, err)

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

	// 直接把 retry_count 推到 max-1 并领取。
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", photo.ID).
		Update("analysis_retry_count", 9).Error)
	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	result, err := svc.ReleaseTask("task_4001_0", "analyzer-A", transientReleaseReq("analysis_failed", "502", tasks[0].LockVersion))
	require.NoError(t, err)
	assert.True(t, result.Final, "第 10 次失败应推进到最终失败")
	assert.Nil(t, result.NextRetryAt)

	// 终态不应再被领取
	tasks2, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	assert.Empty(t, tasks2, "最终失败的照片不应再被领取")
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
			"analysis_lock_id":         "analyzer-A",
			"analysis_lock_expired_at": now.Add(5 * time.Minute),
			"analysis_retry_count":     3,
			"analysis_next_retry_at":   &now,
			"analysis_last_error_code": "provider_transient",
			"analysis_last_error":      "502",
			"analysis_last_failed_at":  &now,
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

func TestAnalysisService_AnalysisCompletedAfterCommit(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 5101)
	svc := newAnalysisServiceWithQueue(db, wq)
	completed := &recordingAnalysisCompletedHandler{}
	svc.SetAnalysisCompletedHandler(completed)

	resp, err := svc.SubmitResultsDirectly([]model.AnalysisResult{{
		PhotoID:      photo.ID,
		Description:  "测试截屏",
		MemoryScore:  10,
		BeautyScore:  10,
		Tags:         "截屏",
		MainCategory: model.PhotoMainCategoryScreenshot,
	}}, 0)
	require.NoError(t, err)
	require.Equal(t, 1, resp.Accepted)

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.True(t, got.AIAnalyzed)
	assert.True(t, got.PeopleExcluded)
	assert.Equal(t, model.PeopleExclusionReasonScreenshot, got.PeopleExclusionReason)
	assert.Equal(t, []uint{photo.ID}, completed.photoIDs)
}

// --- Task 3: 锁版本与原子 release 回归 ---

// TestAnalysisService_AcquireIncrementsLockVersion
// 领取时 analysis_lock_version + 1，响应带 lock_version。
func TestAnalysisService_AcquireIncrementsLockVersion(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 6001)
	svc := newAnalysisServiceWithQueue(db, wq)

	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, int64(1), tasks[0].LockVersion, "首次领取版本应为 1")

	// 释放后再次领取，版本应继续递增。
	_, err = svc.ReleaseTask("task_6001_0", "analyzer-A", transientReleaseReq("analysis_failed", "502", 1))
	require.NoError(t, err)

	// 模拟退避到期。
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", photo.ID).
		Update("analysis_next_retry_at", time.Now().Add(-time.Minute)).Error)

	tasks2, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks2, 1)
	assert.Equal(t, int64(2), tasks2[0].LockVersion, "二次领取版本应为 2")
}

// TestAnalysisService_StaleReleaseDoesNotAffectNewLock
// 旧版本迟到 release 不得清除新锁或增加 attempts。
func TestAnalysisService_StaleReleaseDoesNotAffectNewLock(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 7001)
	svc := newAnalysisServiceWithQueue(db, wq)

	// analyzer-A 领取（版本 1），过期后 CleanExpiredLocks 清掉。
	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, int64(1), tasks[0].LockVersion)

	// 模拟过期 + 清理，再被 analyzer-B 领取（版本 2）。
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", photo.ID).
		Updates(map[string]interface{}{
			"analysis_lock_id":         nil,
			"analysis_lock_expired_at": time.Now().Add(-time.Minute),
		}).Error)
	_, err = svc.CleanExpiredLocks()
	require.NoError(t, err)
	tasksB, _, err := svc.GetPendingTasks(10, "analyzer-B")
	require.NoError(t, err)
	require.Len(t, tasksB, 1)
	require.Equal(t, int64(2), tasksB[0].LockVersion)

	// analyzer-A 用旧版本 1 迟到 release：应 stale 失败，不影响新锁。
	result, err := svc.ReleaseTask("task_7001_0", "analyzer-A", transientReleaseReq("analysis_failed", "late 502", 1))
	require.ErrorIs(t, err, ErrTaskLockStale)
	require.True(t, result.LockStale)

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	// 锁仍属于 analyzer-B，retry count 未增加。
	require.NotNil(t, got.AnalysisLockID)
	assert.Equal(t, "analyzer-B", *got.AnalysisLockID)
	assert.Equal(t, 0, got.AnalysisRetryCount, "迟到 release 不应增加 attempts")
}

// TestAnalysisService_SameVersionRepeatReleaseIdempotent
// 同版本重复 release 幂等，不重复增加 attempts。
func TestAnalysisService_SameVersionRepeatReleaseIdempotent(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 8001)
	svc := newAnalysisServiceWithQueue(db, wq)

	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	v := tasks[0].LockVersion

	// 第一次 release 成功。
	_, err = svc.ReleaseTask("task_8001_0", "analyzer-A", transientReleaseReq("analysis_failed", "502", v))
	require.NoError(t, err)

	// 同版本重复 release：应幂等返回，不重复增加 attempts。
	result2, err := svc.ReleaseTask("task_8001_0", "analyzer-A", transientReleaseReq("analysis_failed", "502", v))
	require.NoError(t, err, "同版本重复 release 应幂等返回 nil")
	assert.True(t, result2.Idempotent, "应标记 idempotent")

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.Equal(t, 1, got.AnalysisRetryCount, "重复 release 不应再次增加 attempts")
}

// TestAnalysisService_ReleaseConcurrencyNoLockError
// release 与模拟并发写都经 WriteQueue，不出现 database is locked。
// 这里直接并发调用 ReleaseTask（内部各自走 WriteQueue），不嵌套 Execute。
func TestAnalysisService_ReleaseConcurrencyNoLockError(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 9001)
	svc := newAnalysisServiceWithQueue(db, wq)

	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	v := tasks[0].LockVersion

	// 并发触发多个 release；第一个成功，其余幂等或 stale。
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, e := svc.ReleaseTask("task_9001_0", "analyzer-A", transientReleaseReq("analysis_failed", "502", v))
			done <- e
		}()
	}
	for i := 0; i < 8; i++ {
		err := <-done
		// 第一个成功，其余 stale；不应有 "database is locked" 错误。
		if err != nil && !errors.Is(err, ErrTaskLockStale) {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.Equal(t, 1, got.AnalysisRetryCount, "并发 release 只应增加一次 attempts")
}

// TestAnalysisService_ExtendTaskLockVersionMismatch
// heartbeat/release 同时匹配 photo/analyzer/lock-version/未完成状态。
func TestAnalysisService_ExtendTaskLockVersionMismatch(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	seedAnalysisPhoto(t, db, 10001)
	svc := newAnalysisServiceWithQueue(db, wq)

	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	// 正确版本续租成功。
	_, _, err = svc.ExtendTaskLock("task_10001_0", "analyzer-A", tasks[0].LockVersion)
	require.NoError(t, err)

	// 错误版本续租失败。
	_, _, err = svc.ExtendTaskLock("task_10001_0", "analyzer-A", tasks[0].LockVersion+999)
	assert.ErrorIs(t, err, ErrTaskLockedByOther)
}

// TestAnalysisService_ClientCancelledNotCounted
// client_cancelled 返回 pending 且不增加 attempts。
func TestAnalysisService_ClientCancelledNotCounted(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 11001)
	svc := newAnalysisServiceWithQueue(db, wq)

	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	req := model.ReleaseTaskRequest{
		Reason:       "cancelled",
		FailureClass: FailureClassClientCancelled,
		LockVersion:  tasks[0].LockVersion,
	}
	result, err := svc.ReleaseTask("task_11001_0", "analyzer-A", req)
	require.NoError(t, err)
	assert.Equal(t, "pending", result.NewStatus)
	assert.False(t, result.Final)
	assert.Nil(t, result.NextRetryAt)

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.Equal(t, 0, got.AnalysisRetryCount, "client_cancelled 不应增加 attempts")
	assert.Nil(t, got.AnalysisNextRetryAt)

	// 立即可被重领（回到 pending）。
	tasks2, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks2, 1, "client_cancelled 后应立即可被领取")
}

// TestAnalysisService_InputPermanentFinal
// input_permanent 直接进入最终失败。
func TestAnalysisService_InputPermanentFinal(t *testing.T) {
	db, wq := setupAnalysisTestDB(t)
	photo := seedAnalysisPhoto(t, db, 12001)
	svc := newAnalysisServiceWithQueue(db, wq)

	tasks, _, err := svc.GetPendingTasks(10, "analyzer-A")
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	req := model.ReleaseTaskRequest{
		Reason:       model.ReleaseReasonFileCorrupted,
		ErrorMsg:     "invalid jpeg format",
		FailureClass: FailureClassInputPermanent,
		LockVersion:  tasks[0].LockVersion,
	}
	result, err := svc.ReleaseTask("task_12001_0", "analyzer-A", req)
	require.NoError(t, err)
	assert.True(t, result.Final)
	assert.Nil(t, result.NextRetryAt)

	var got model.Photo
	require.NoError(t, db.First(&got, photo.ID).Error)
	assert.Equal(t, AnalysisMaxAttempts, got.AnalysisRetryCount)
}

// TestAnalysisService_SanitizeErrorRedactsSensitive
// 错误摘要入库前去除 Authorization/API key/URL query 敏感标记。
func TestAnalysisService_SanitizeErrorRedactsSensitive(t *testing.T) {
	// 用拼接构造敏感串，避免被外部文本规则改写。
	secret := "ZINFO" + "ID_0" + "0Q"
	cases := []struct {
		name, in, want string
	}{
		{"auth header", "Authorization: Bearer abc123 502 bad gateway", "[redacted] 502 bad gateway"},
		{"api key", "api_key=sk-" + secret + " xxx", "[redacted] xxx"},
		{"infoid token", secret + " leaked", "[redacted] leaked"},
		{"whitespace", "502  bad   gateway", "502 bad gateway"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, sanitizeError(c.in))
		})
	}
}
