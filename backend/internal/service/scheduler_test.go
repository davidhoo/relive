package service

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type mergeSuggestionServiceStub struct {
	runCalls int
}

func (s *mergeSuggestionServiceStub) GetTask() *model.PersonMergeSuggestionTask {
	return nil
}

func (s *mergeSuggestionServiceStub) GetStats() (*model.PersonMergeSuggestionStatsResponse, error) {
	return nil, nil
}

func (s *mergeSuggestionServiceStub) GetBackgroundLogs() []string {
	return nil
}

func (s *mergeSuggestionServiceStub) Pause() error {
	return nil
}

func (s *mergeSuggestionServiceStub) Resume() error {
	return nil
}

func (s *mergeSuggestionServiceStub) Rebuild() error {
	return nil
}

func (s *mergeSuggestionServiceStub) MarkDirty(reason string) error {
	return nil
}

func (s *mergeSuggestionServiceStub) RunBackgroundSlice() error {
	s.runCalls++
	return nil
}

func (s *mergeSuggestionServiceStub) ExcludeCandidates(suggestionID uint, candidateIDs []uint) error {
	return nil
}

func (s *mergeSuggestionServiceStub) ApplySuggestion(suggestionID uint, candidateIDs []uint) error {
	return nil
}

func (s *mergeSuggestionServiceStub) ListPending(page, pageSize int) ([]model.PersonMergeSuggestionResponse, int64, error) {
	return nil, 0, nil
}

func (s *mergeSuggestionServiceStub) GetPendingByID(id uint) (*model.PersonMergeSuggestionResponse, error) {
	return nil, nil
}

func (s *mergeSuggestionServiceStub) AttachThreshold() float64 {
	return 0.65
}

func (s *mergeSuggestionServiceStub) CalculateSimilarity(personID1, personID2 uint) (float64, error) {
	return 0, nil
}

func (s *mergeSuggestionServiceStub) MergeSuggestionThreshold() float64 {
	return 0.55
}

func TestTaskSchedulerRunMergeSuggestionSlice(t *testing.T) {
	stub := &mergeSuggestionServiceStub{}
	scheduler := &TaskScheduler{
		mergeSuggestionService: stub,
		stopCh:                 make(chan struct{}),
	}

	scheduler.runMergeSuggestionSlice()

	if stub.runCalls != 1 {
		t.Fatalf("expected merge suggestion slice to run once, got %d", stub.runCalls)
	}
}

// identityProfileServiceStub 计数 RunBackgroundSlice 调用，用于调度器测试。
type identityProfileServiceStub struct {
	mode     string
	runCalls atomic.Int64
}

func (s *identityProfileServiceStub) MarkDirty([]uint, string) error { return nil }
func (s *identityProfileServiceStub) RunBackgroundSlice() error {
	s.runCalls.Add(1)
	return nil
}
func (s *identityProfileServiceStub) GetActive(uint) (*model.PersonIdentityProfileBuild, error) {
	return nil, nil
}
func (s *identityProfileServiceStub) GetStats() (*model.PersonIdentityProfileStats, error) {
	return &model.PersonIdentityProfileStats{}, nil
}
func (s *identityProfileServiceStub) Mode() string { return s.mode }

// analysisServiceStub 是 AnalysisService 的最小桩，供调度器 Start() 测试使用，
// 避免 cleanExpiredLocksTask 因 nil analysisService panic。
type analysisServiceStub struct{}

func (analysisServiceStub) GetPendingTasks(int, string) ([]model.AnalysisTask, int64, error) {
	return nil, 0, nil
}
func (analysisServiceStub) ExtendTaskLock(string, string) (time.Time, error) {
	return time.Time{}, nil
}
func (analysisServiceStub) ReleaseTask(string, string, string, string, bool) error { return nil }
func (analysisServiceStub) SubmitResults([]model.AnalysisResult, uint) (*model.SubmitResultsResponse, error) {
	return nil, nil
}
func (analysisServiceStub) SubmitResultsDirectly([]model.AnalysisResult, uint) (*model.SubmitResultsResponse, error) {
	return nil, nil
}
func (analysisServiceStub) GetStats(uint) (*model.AnalyzerStatsResponse, error) { return nil, nil }
func (analysisServiceStub) CleanExpiredLocks() (int64, error)                   { return 0, nil }
func (analysisServiceStub) SetResultQueue(*ResultQueue)                         {}

func TestTaskSchedulerIdentityProfileSliceRunsOncePerTick(t *testing.T) {
	stub := &identityProfileServiceStub{mode: "shadow"}
	scheduler := &TaskScheduler{
		identityProfileService: stub,
		stopCh:                 make(chan struct{}),
	}

	scheduler.runIdentityProfileSlice()
	assert.Equal(t, int64(1), stub.runCalls.Load(), "each tick must call RunBackgroundSlice exactly once")
}

func TestTaskSchedulerIdentityProfileRunOnceSingleSlice(t *testing.T) {
	stub := &identityProfileServiceStub{mode: "shadow"}
	scheduler := &TaskScheduler{
		analysisService:        analysisServiceStub{},
		identityProfileService: stub,
		stopCh:                 make(chan struct{}),
	}
	scheduler.RunOnce()
	assert.Equal(t, int64(1), stub.runCalls.Load(), "RunOnce must execute exactly one identity profile slice")
}

func TestTaskSchedulerIdentityProfileSliceTaskStops(t *testing.T) {
	stub := &identityProfileServiceStub{mode: "shadow"}
	scheduler := &TaskScheduler{
		identityProfileService: stub,
		stopCh:                 make(chan struct{}),
	}

	scheduler.wg.Add(1)
	done := make(chan struct{})
	go func() {
		scheduler.identityProfileSliceTask(5 * time.Millisecond)
		close(done)
	}()

	time.Sleep(12 * time.Millisecond)
	close(scheduler.stopCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected identity profile task to stop")
	}
	if stub.runCalls.Load() == 0 {
		t.Fatal("expected identity profile slice to run at least once")
	}
}

// TestTaskSchedulerStartSkipsIdentityProfileInLegacyMode 验证 legacy 模式不启动画像 goroutine。
func TestTaskSchedulerStartSkipsIdentityProfileInLegacyMode(t *testing.T) {
	stub := &identityProfileServiceStub{mode: "legacy"}
	scheduler := &TaskScheduler{
		analysisService:        analysisServiceStub{},
		identityProfileService: stub,
		stopCh:                 make(chan struct{}),
	}
	scheduler.Start()
	defer scheduler.Stop()

	// legacy 模式不应启动画像 goroutine；identityProfileSliceTask 启动时会立即执行一次 slice，
	// 若 goroutine 未启动则 runCalls 保持 0。
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int64(0), stub.runCalls.Load(), "legacy mode must not run identity profile slices")
}

// TestTaskSchedulerStartRunsIdentityProfileInShadowMode 验证非 legacy 模式启动画像 goroutine。
func TestTaskSchedulerStartRunsIdentityProfileInShadowMode(t *testing.T) {
	stub := &identityProfileServiceStub{mode: "shadow"}
	scheduler := &TaskScheduler{
		analysisService:        analysisServiceStub{},
		identityProfileService: stub,
		stopCh:                 make(chan struct{}),
	}
	scheduler.Start()
	defer scheduler.Stop()

	// identityProfileSliceTask 启动时立即执行一次 slice。
	time.Sleep(50 * time.Millisecond)
	assert.GreaterOrEqual(t, stub.runCalls.Load(), int64(1), "shadow mode must start identity profile goroutine")
}

func TestTaskSchedulerMergeSuggestionSliceTaskStops(t *testing.T) {
	stub := &mergeSuggestionServiceStub{}
	scheduler := &TaskScheduler{
		mergeSuggestionService: stub,
		stopCh:                 make(chan struct{}),
	}

	scheduler.wg.Add(1)
	done := make(chan struct{})
	go func() {
		scheduler.mergeSuggestionSliceTask(5 * time.Millisecond)
		close(done)
	}()

	time.Sleep(12 * time.Millisecond)
	close(scheduler.stopCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected merge suggestion task to stop")
	}

	if stub.runCalls == 0 {
		t.Fatal("expected merge suggestion slice to run at least once")
	}
}

// setupSchedulerPeopleJobsDB 构造隔离的临时文件库并迁移 people_jobs 相关表，
// 避免与其它使用 file::memory:?cache=shared 的测试共享内存库造成数据污染。
func setupSchedulerPeopleJobsDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "people_jobs_test.db") + "?cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.PeopleJob{}, &model.AppConfig{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	return db
}

func schedulerCreateJob(t *testing.T, repo repository.PeopleJobRepository, photoID uint, status string) *model.PeopleJob {
	t.Helper()
	job := &model.PeopleJob{
		PhotoID:  photoID,
		FilePath: "/p.jpg",
		Status:   status,
		Source:   model.PeopleJobSourceScan,
		QueuedAt: time.Now(),
	}
	require.NoError(t, repo.Create(job))
	return job
}

// TestSchedulerRunPeopleJobsCleanup 验证分批删除：终态历史记录被删，非终态与保留期内保留，capped 正确。
func TestSchedulerRunPeopleJobsCleanup(t *testing.T) {
	db := setupSchedulerPeopleJobsDB(t)
	repo := repository.NewPeopleJobRepository(db)

	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)
	cutoff := now.Add(-7 * 24 * time.Hour)

	// 5 条历史终态 + 1 条保留期内终态 + 1 条非终态
	for i := 0; i < 5; i++ {
		j := schedulerCreateJob(t, repo, uint(i+1), model.PeopleJobStatusCompleted)
		require.NoError(t, db.Exec("UPDATE people_jobs SET updated_at = ? WHERE id = ?", old, j.ID).Error)
	}
	recent := schedulerCreateJob(t, repo, 100, model.PeopleJobStatusCompleted)
	require.NoError(t, db.Exec("UPDATE people_jobs SET updated_at = ? WHERE id = ?", now, recent.ID).Error)
	queued := schedulerCreateJob(t, repo, 101, model.PeopleJobStatusQueued)
	require.NoError(t, db.Exec("UPDATE people_jobs SET updated_at = ? WHERE id = ?", old, queued.ID).Error)

	scheduler := &TaskScheduler{
		peopleJobRepo: repo,
		stopCh:        make(chan struct{}),
		// writeQueue 为 nil：直接调用 DeleteByIDs
	}

	// batchSize=2, maxPerRun=5：恰好删完 5 条历史终态，无积压 → capped=false
	res := scheduler.runPeopleJobsCleanup(cutoff, peopleJobsCleanupConfig{batchSize: 2, maxPerRun: 5})
	require.NoError(t, res.err)
	assert.Equal(t, int64(5), res.deleted)
	assert.Equal(t, 3, res.batches) // 2+2+1
	assert.False(t, res.capped)

	// 保留期内终态与非终态仍在
	exists, err := repo.GetByID(recent.ID)
	require.NoError(t, err)
	assert.NotNil(t, exists)
	keptQueued, err := repo.GetByID(queued.ID)
	require.NoError(t, err)
	assert.NotNil(t, keptQueued)
}

// TestSchedulerRunPeopleJobsCleanup_Capped 验证单次上限：积压超过 maxPerRun 时 capped=true。
func TestSchedulerRunPeopleJobsCleanup_Capped(t *testing.T) {
	db := setupSchedulerPeopleJobsDB(t)
	repo := repository.NewPeopleJobRepository(db)

	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)
	cutoff := now.Add(-7 * 24 * time.Hour)

	for i := 0; i < 6; i++ {
		j := schedulerCreateJob(t, repo, uint(i+1), model.PeopleJobStatusCompleted)
		require.NoError(t, db.Exec("UPDATE people_jobs SET updated_at = ? WHERE id = ?", old, j.ID).Error)
	}

	scheduler := &TaskScheduler{
		peopleJobRepo: repo,
		stopCh:        make(chan struct{}),
	}

	// maxPerRun=4：删 4 条，剩 2 条 → capped=true
	res := scheduler.runPeopleJobsCleanup(cutoff, peopleJobsCleanupConfig{batchSize: 2, maxPerRun: 4})
	require.NoError(t, res.err)
	assert.Equal(t, int64(4), res.deleted)
	assert.True(t, res.capped)

	// 第二轮清空剩余
	res2 := scheduler.runPeopleJobsCleanup(cutoff, peopleJobsCleanupConfig{batchSize: 2, maxPerRun: 50})
	require.NoError(t, res2.err)
	assert.Equal(t, int64(2), res2.deleted)
	assert.False(t, res2.capped)
}

// TestSchedulerRunPeopleJobsCleanup_NonTerminalPreserved 非终态任务绝不被删。
func TestSchedulerRunPeopleJobsCleanup_NonTerminalPreserved(t *testing.T) {
	db := setupSchedulerPeopleJobsDB(t)
	repo := repository.NewPeopleJobRepository(db)

	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)
	cutoff := now.Add(-7 * 24 * time.Hour)

	pending := schedulerCreateJob(t, repo, 1, model.PeopleJobStatusPending)
	processing := schedulerCreateJob(t, repo, 2, model.PeopleJobStatusProcessing)
	for _, j := range []*model.PeopleJob{pending, processing} {
		require.NoError(t, db.Exec("UPDATE people_jobs SET updated_at = ? WHERE id = ?", old, j.ID).Error)
	}

	scheduler := &TaskScheduler{
		peopleJobRepo: repo,
		stopCh:        make(chan struct{}),
	}
	res := scheduler.runPeopleJobsCleanup(cutoff, peopleJobsCleanupConfig{batchSize: 10, maxPerRun: 100})
	require.NoError(t, res.err)
	assert.Equal(t, int64(0), res.deleted)

	for _, j := range []*model.PeopleJob{pending, processing} {
		got, err := repo.GetByID(j.ID)
		require.NoError(t, err)
		assert.NotNil(t, got, "non-terminal job %d must not be deleted", j.ID)
	}
}
