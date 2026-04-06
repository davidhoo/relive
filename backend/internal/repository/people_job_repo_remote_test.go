package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPeopleJobRepository_ClaimNextRemote(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()

	// 创建待处理任务
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:  1,
		FilePath: "/photos/1.jpg",
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceScan,
		Priority: 10,
		QueuedAt: now,
	}))
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:  2,
		FilePath: "/photos/2.jpg",
		Status:   model.PeopleJobStatusPending,
		Source:   model.PeopleJobSourceManual,
		Priority: 5,
		QueuedAt: now.Add(time.Minute),
	}))

	// 远程获取任务
	lockUntil := now.Add(5 * time.Minute)
	jobs, err := repo.ClaimNextRemote("worker-1", 10, lockUntil)

	require.NoError(t, err)
	require.Len(t, jobs, 2)
	// 应该按优先级排序，高优先级在前
	assert.Equal(t, uint(1), jobs[0].PhotoID)
	assert.Equal(t, model.PeopleJobStatusProcessing, jobs[0].Status)
	assert.Equal(t, "worker-1", jobs[0].WorkerID)
	assert.NotNil(t, jobs[0].LockExpiresAt)
}

func TestPeopleJobRepository_ClaimNextRemoteSkipsLocalProcessing(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()

	// 创建本地处理中的任务（worker_id 为空）
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:       1,
		FilePath:      "/photos/1.jpg",
		Status:        model.PeopleJobStatusProcessing,
		Source:        model.PeopleJobSourceScan,
		Priority:      10,
		QueuedAt:      now,
		WorkerID:      "", // 本地处理
		LockExpiresAt: &now,
	}))

	// 创建待处理任务
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:  2,
		FilePath: "/photos/2.jpg",
		Status:   model.PeopleJobStatusQueued,
		Source:   model.PeopleJobSourceManual,
		Priority: 5,
		QueuedAt: now,
	}))

	// 远程获取任务
	lockUntil := now.Add(5 * time.Minute)
	jobs, err := repo.ClaimNextRemote("worker-1", 10, lockUntil)

	require.NoError(t, err)
	require.Len(t, jobs, 1)
	// 应该只获取到待处理任务，跳过本地处理中的任务
	assert.Equal(t, uint(2), jobs[0].PhotoID)
}

func TestPeopleJobRepository_ReclaimExpiredRemoteJob(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()
	expiredTime := now.Add(-5 * time.Minute) // 已过期

	// 创建过期远程处理中的任务
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:       1,
		FilePath:      "/photos/1.jpg",
		Status:        model.PeopleJobStatusProcessing,
		Source:        model.PeopleJobSourceScan,
		Priority:      10,
		QueuedAt:      now,
		WorkerID:      "old-worker",
		LockExpiresAt: &expiredTime,
	}))

	// 新 worker 应该能 reclaim 这个任务
	lockUntil := now.Add(5 * time.Minute)
	jobs, err := repo.ClaimNextRemote("new-worker", 10, lockUntil)

	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, uint(1), jobs[0].PhotoID)
	assert.Equal(t, "new-worker", jobs[0].WorkerID)
}

func TestPeopleJobRepository_HeartbeatRemoteRejectsOtherWorker(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()
	lockUntil := now.Add(5 * time.Minute)

	// 创建一个远程处理中的任务
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:       1,
		FilePath:      "/photos/1.jpg",
		Status:        model.PeopleJobStatusProcessing,
		Source:        model.PeopleJobSourceScan,
		Priority:      10,
		QueuedAt:      now,
		WorkerID:      "worker-1",
		LockExpiresAt: &lockUntil,
	}))

	// 获取任务 ID
	var job model.PeopleJob
	require.NoError(t, db.First(&job).Error)

	// 正确的 worker 可以发送心跳
	newLockUntil := now.Add(10 * time.Minute)
	err := repo.HeartbeatRemote(job.ID, "worker-1", 50, "processing", newLockUntil)
	require.NoError(t, err)

	// 错误的 worker 不能发送心跳
	err = repo.HeartbeatRemote(job.ID, "worker-2", 50, "processing", newLockUntil)
	assert.Error(t, err)
}

func TestPeopleJobRepository_CompleteRemoteJob(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()
	lockUntil := now.Add(5 * time.Minute)

	// 创建一个远程处理中的任务
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:       1,
		FilePath:      "/photos/1.jpg",
		Status:        model.PeopleJobStatusProcessing,
		Source:        model.PeopleJobSourceScan,
		Priority:      10,
		QueuedAt:      now,
		WorkerID:      "worker-1",
		LockExpiresAt: &lockUntil,
	}))

	// 获取任务 ID
	var job model.PeopleJob
	require.NoError(t, db.First(&job).Error)

	// 完成远程任务
	err := repo.CompleteRemote(job.ID, "worker-1")
	require.NoError(t, err)

	// 验证任务状态
	var completedJob model.PeopleJob
	require.NoError(t, db.First(&completedJob, job.ID).Error)
	assert.Equal(t, model.PeopleJobStatusCompleted, completedJob.Status)
	assert.NotNil(t, completedJob.CompletedAt)
}

func TestPeopleJobRepository_ExpiredRemoteLockBecomesClaimable(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()

	// 创建一个即将过期的任务
	shortLock := now.Add(1 * time.Second)
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:       1,
		FilePath:      "/photos/1.jpg",
		Status:        model.PeopleJobStatusProcessing,
		Source:        model.PeopleJobSourceScan,
		Priority:      10,
		QueuedAt:      now,
		WorkerID:      "worker-1",
		LockExpiresAt: &shortLock,
	}))

	// 等待锁过期
	time.Sleep(2 * time.Second)

	// 新 worker 应该能 reclaim 这个任务
	lockUntil := now.Add(5 * time.Minute)
	jobs, err := repo.ClaimNextRemote("worker-2", 10, lockUntil)

	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "worker-2", jobs[0].WorkerID)
}

func TestPeopleJobRepository_NASRestartPreservesActiveRemoteJobs(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()
	lockUntil := now.Add(5 * time.Minute)

	// 创建活跃的远程任务
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:       1,
		FilePath:      "/photos/1.jpg",
		Status:        model.PeopleJobStatusProcessing,
		Source:        model.PeopleJobSourceScan,
		Priority:      10,
		QueuedAt:      now,
		WorkerID:      "worker-1",
		LockExpiresAt: &lockUntil,
	}))

	// 模拟 NAS 重启：调用 InterruptNonTerminal
	err := repo.InterruptNonTerminal("NAS restarted")
	require.NoError(t, err)

	// 验证活跃远程任务未被取消
	var job model.PeopleJob
	require.NoError(t, db.First(&job).Error)
	assert.Equal(t, model.PeopleJobStatusProcessing, job.Status)
	assert.Equal(t, "worker-1", job.WorkerID)
	assert.NotNil(t, job.LockExpiresAt)
}

func TestPeopleJobRepository_ReleaseRemoteWithRetryLater(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()
	lockUntil := now.Add(5 * time.Minute)

	// 创建远程处理中的任务
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:       1,
		FilePath:      "/photos/1.jpg",
		Status:        model.PeopleJobStatusProcessing,
		Source:        model.PeopleJobSourceScan,
		Priority:      10,
		QueuedAt:      now,
		WorkerID:      "worker-1",
		LockExpiresAt: &lockUntil,
	}))

	var job model.PeopleJob
	require.NoError(t, db.First(&job).Error)

	// 释放任务，retryLater = true
	err := repo.ReleaseRemote(job.ID, "worker-1", "temporary error", true)
	require.NoError(t, err)

	// 验证任务回到 queued 状态
	var releasedJob model.PeopleJob
	require.NoError(t, db.First(&releasedJob, job.ID).Error)
	assert.Equal(t, model.PeopleJobStatusQueued, releasedJob.Status)
	assert.Equal(t, "", releasedJob.WorkerID)
	assert.Nil(t, releasedJob.LockExpiresAt)
	assert.Equal(t, "temporary error", releasedJob.StatusMessage)
}

func TestPeopleJobRepository_ReleaseRemoteWithoutRetryLater(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	repo := NewPeopleJobRepository(db)
	now := time.Now()
	lockUntil := now.Add(5 * time.Minute)

	// 创建远程处理中的任务
	require.NoError(t, repo.Create(&model.PeopleJob{
		PhotoID:       1,
		FilePath:      "/photos/1.jpg",
		Status:        model.PeopleJobStatusProcessing,
		Source:        model.PeopleJobSourceScan,
		Priority:      10,
		QueuedAt:      now,
		WorkerID:      "worker-1",
		LockExpiresAt: &lockUntil,
		AttemptCount:  2,
	}))

	var job model.PeopleJob
	require.NoError(t, db.First(&job).Error)

	// 释放任务，retryLater = false
	err := repo.ReleaseRemote(job.ID, "worker-1", "permanent error", false)
	require.NoError(t, err)

	// 验证任务失败，错误记录在 last_error
	var failedJob model.PeopleJob
	require.NoError(t, db.First(&failedJob, job.ID).Error)
	assert.Equal(t, model.PeopleJobStatusFailed, failedJob.Status)
	assert.Equal(t, "permanent error", failedJob.LastError)
}
