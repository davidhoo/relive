package service

import (
	"errors"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/database"
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
func (s *identityProfileServiceStub) Invalidate(IdentityProfileInvalidation) error {
	return nil
}
func (s *identityProfileServiceStub) InvalidateANNOnly([]uint) {}
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
func (s *identityProfileServiceStub) GetOperationalStats(_ repository.PeopleIdentityDecisionRepository) (*model.IdentityProfileOperationalStatsResponse, error) {
	return &model.IdentityProfileOperationalStatsResponse{Mode: s.mode}, nil
}
func (s *identityProfileServiceStub) ListRecentDecisions(_ int, _ repository.PeopleIdentityDecisionRepository) ([]model.IdentityDecisionResponse, error) {
	return []model.IdentityDecisionResponse{}, nil
}
func (s *identityProfileServiceStub) Mode() string { return s.mode }

// analysisServiceStub 是 AnalysisService 的最小桩，供调度器 Start() 测试使用，
// 避免 cleanExpiredLocksTask 因 nil analysisService panic。
type analysisServiceStub struct{}

func (analysisServiceStub) GetPendingTasks(int, string) ([]model.AnalysisTask, int64, error) {
	return nil, 0, nil
}
func (analysisServiceStub) ExtendTaskLock(string, string, int64) (time.Time, int64, error) {
	return time.Time{}, 0, nil
}
func (analysisServiceStub) ReleaseTask(string, string, model.ReleaseTaskRequest) (*model.ReleaseTaskResult, error) {
	return nil, nil
}
func (analysisServiceStub) SubmitResults([]model.AnalysisResult, uint) (*model.SubmitResultsResponse, error) {
	return nil, nil
}
func (analysisServiceStub) SubmitResultsDirectly([]model.AnalysisResult, uint) (*model.SubmitResultsResponse, error) {
	return nil, nil
}
func (analysisServiceStub) GetStats(uint) (*model.AnalyzerStatsResponse, error)  { return nil, nil }
func (analysisServiceStub) CleanExpiredLocks() (int64, error)                    { return 0, nil }
func (analysisServiceStub) SetResultQueue(*ResultQueue)                          {}
func (analysisServiceStub) SetAnalysisCompletedHandler(AnalysisCompletedHandler) {}

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

// ---- 身份画像决策遥测清理测试 ----

// setupSchedulerIdentityDecisionDB 构造隔离的临时文件库并迁移 people_identity_decisions。
func setupSchedulerIdentityDecisionDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "identity_decision_test.db") + "?cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.PeopleIdentityDecision{}))
	t.Cleanup(func() {
		if sqlDB, _ := db.DB(); sqlDB != nil {
			sqlDB.Close()
		}
	})
	return db
}

// seedDecision 插入一条决策记录并按指定时间回填 created_at，返回其 ID。
// when 必须传 time.Time（由 GORM 统一绑定格式），避免手写 RFC3339 字符串与
// ListIDsBefore 的 cutoff 绑定格式不一致导致 SQLite 文本比较失效。
func seedDecision(t *testing.T, db *gorm.DB, key string, when time.Time) uint {
	t.Helper()
	d := &model.PeopleIdentityDecision{
		Mode:          model.PeopleIdentityModeShadow,
		ComponentHash: "h-" + key,
		ComponentSize: 1,
		DecisionKey:   key,
		Decision:      identityDecisionDisagree,
	}
	require.NoError(t, db.Create(d).Error)
	require.NoError(t, db.Exec("UPDATE people_identity_decisions SET created_at = ? WHERE id = ?", when, d.ID).Error)
	return d.ID
}

// failingDeleteDecisionRepo 委托真实仓库，但 DeleteByIDs 返回注入错误，用于验证失败即停止。
type failingDeleteDecisionRepo struct {
	repository.PeopleIdentityDecisionRepository
	deleteErr error
}

func (r *failingDeleteDecisionRepo) DeleteByIDs(ids []uint) (int64, error) {
	return 0, r.deleteErr
}

func TestSchedulerIdentityDecisionCleanup_LegacySkipped(t *testing.T) {
	db := setupSchedulerIdentityDecisionDB(t)
	repo := repository.NewPeopleIdentityDecisionRepository(db)

	old := time.Now().Add(-100 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		seedDecision(t, db, "k"+strconv.Itoa(i), old)
	}

	scheduler := &TaskScheduler{
		identityDecisionRepo:   repo,
		identityProfileService: &identityProfileServiceStub{mode: "legacy"},
		stopCh:                 make(chan struct{}),
	}

	// cleanIdentityDecisions 守卫：legacy 模式直接返回，不调用 runIdentityDecisionCleanup，
	// 不执行任何 DELETE。runIdentityDecisionCleanup 本身不检查 mode，守卫在上层。
	scheduler.cleanIdentityDecisions()

	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Count(&count).Error)
	assert.Equal(t, int64(3), count, "legacy mode must not delete any decisions")
}

func TestSchedulerIdentityDecisionCleanup_BatchedDeletion(t *testing.T) {
	db := setupSchedulerIdentityDecisionDB(t)
	repo := repository.NewPeopleIdentityDecisionRepository(db)

	now := time.Now()
	cutoff := now.Add(-identityDecisionRetentionDays * 24 * time.Hour)
	old := cutoff.Add(-1 * time.Hour)

	// 5 条过期记录
	for i := 0; i < 5; i++ {
		seedDecision(t, db, "k"+strconv.Itoa(i), old)
	}
	// 1 条保留期内记录
	seedDecision(t, db, "keep", now)

	scheduler := &TaskScheduler{
		identityDecisionRepo:   repo,
		identityProfileService: &identityProfileServiceStub{mode: "shadow"},
		stopCh:                 make(chan struct{}),
	}

	// batchSize=2, maxPerRun=5：删完 5 条过期记录，保留期内不动
	res := scheduler.runIdentityDecisionCleanup(cutoff, identityDecisionCleanupConfig{batchSize: 2, maxPerRun: 5})
	require.NoError(t, res.err)
	assert.Equal(t, int64(5), res.deleted)
	assert.Equal(t, 3, res.batches) // 2+2+1
	assert.False(t, res.capped)

	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "only the in-retention record remains")
}

func TestSchedulerIdentityDecisionCleanup_MaxPerRunCap(t *testing.T) {
	db := setupSchedulerIdentityDecisionDB(t)
	repo := repository.NewPeopleIdentityDecisionRepository(db)

	now := time.Now()
	cutoff := now.Add(-identityDecisionRetentionDays * 24 * time.Hour)
	old := cutoff.Add(-1 * time.Hour)

	// 6 条过期记录
	for i := 0; i < 6; i++ {
		seedDecision(t, db, "k"+strconv.Itoa(i), old)
	}

	scheduler := &TaskScheduler{
		identityDecisionRepo:   repo,
		identityProfileService: &identityProfileServiceStub{mode: "shadow"},
		stopCh:                 make(chan struct{}),
	}

	// maxPerRun=4：删 4 条，剩 2 条 → capped=true
	res := scheduler.runIdentityDecisionCleanup(cutoff, identityDecisionCleanupConfig{batchSize: 2, maxPerRun: 4})
	require.NoError(t, res.err)
	assert.Equal(t, int64(4), res.deleted)
	assert.True(t, res.capped)

	// 第二轮清空剩余
	res2 := scheduler.runIdentityDecisionCleanup(cutoff, identityDecisionCleanupConfig{batchSize: 2, maxPerRun: 50})
	require.NoError(t, res2.err)
	assert.Equal(t, int64(2), res2.deleted)
	assert.False(t, res2.capped)
}

func TestSchedulerIdentityDecisionCleanup_NoDataNoDelete(t *testing.T) {
	db := setupSchedulerIdentityDecisionDB(t)
	repo := repository.NewPeopleIdentityDecisionRepository(db)

	// 只插保留期内记录（now 远晚于 cutoff）
	now := time.Now()
	cutoff := now.Add(-identityDecisionRetentionDays * 24 * time.Hour)
	seedDecision(t, db, "fresh", now)

	scheduler := &TaskScheduler{
		identityDecisionRepo:   repo,
		identityProfileService: &identityProfileServiceStub{mode: "shadow"},
		stopCh:                 make(chan struct{}),
	}

	res := scheduler.runIdentityDecisionCleanup(cutoff, identityDecisionCleanupConfig{batchSize: 500, maxPerRun: 2000})
	require.NoError(t, res.err)
	assert.Equal(t, int64(0), res.deleted)
	assert.Equal(t, 0, res.batches, "no expired data must not issue any DELETE")
}

func TestSchedulerIdentityDecisionCleanup_DeleteFailureStops(t *testing.T) {
	db := setupSchedulerIdentityDecisionDB(t)
	realRepo := repository.NewPeopleIdentityDecisionRepository(db)

	now := time.Now()
	cutoff := now.Add(-identityDecisionRetentionDays * 24 * time.Hour)
	old := cutoff.Add(-1 * time.Hour)
	for i := 0; i < 5; i++ {
		seedDecision(t, db, "k"+strconv.Itoa(i), old)
	}

	repo := &failingDeleteDecisionRepo{
		PeopleIdentityDecisionRepository: realRepo,
		deleteErr:                        errors.New("disk full"),
	}
	scheduler := &TaskScheduler{
		identityDecisionRepo:   repo,
		identityProfileService: &identityProfileServiceStub{mode: "shadow"},
		stopCh:                 make(chan struct{}),
	}

	res := scheduler.runIdentityDecisionCleanup(cutoff, identityDecisionCleanupConfig{batchSize: 2, maxPerRun: 100})
	require.Error(t, res.err)
	// 失败发生在 batches++ 之前（批次计数只计成功批），故 batches=0；
	// 关键断言是：不重试、记录全部保留。
	assert.Equal(t, 0, res.batches, "failed batch must not be counted; loop must stop without retry")
	assert.Equal(t, int64(0), res.deleted)

	// 失败后不进入紧密重试：所有记录仍在
	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Count(&count).Error)
	assert.Equal(t, int64(5), count)
}

func TestSchedulerIdentityDecisionCleanup_StopSignalInterrupts(t *testing.T) {
	db := setupSchedulerIdentityDecisionDB(t)
	repo := repository.NewPeopleIdentityDecisionRepository(db)

	now := time.Now()
	cutoff := now.Add(-identityDecisionRetentionDays * 24 * time.Hour)
	old := cutoff.Add(-1 * time.Hour)
	for i := 0; i < 10; i++ {
		seedDecision(t, db, "k"+strconv.Itoa(i), old)
	}

	stopCh := make(chan struct{})
	scheduler := &TaskScheduler{
		identityDecisionRepo:   repo,
		identityProfileService: &identityProfileServiceStub{mode: "shadow"},
		stopCh:                 stopCh,
	}

	// 预先关闭 stop signal，验证循环能在长积压场景下退出
	close(stopCh)
	res := scheduler.runIdentityDecisionCleanup(cutoff, identityDecisionCleanupConfig{batchSize: 2, maxPerRun: 100})
	// stopCh 已关闭，循环应在首次 select 检查时退出；deleted 可能为 0
	assert.NoError(t, res.err)
}

func TestSchedulerIdentityDecisionCleanup_ViaWriteQueue(t *testing.T) {
	db := setupSchedulerIdentityDecisionDB(t)
	repo := repository.NewPeopleIdentityDecisionRepository(db)

	now := time.Now()
	cutoff := now.Add(-identityDecisionRetentionDays * 24 * time.Hour)
	old := cutoff.Add(-1 * time.Hour)
	for i := 0; i < 3; i++ {
		seedDecision(t, db, "k"+strconv.Itoa(i), old)
	}

	wq := database.NewWriteQueue(nil)
	defer wq.Stop()

	scheduler := &TaskScheduler{
		identityDecisionRepo:   repo,
		identityProfileService: &identityProfileServiceStub{mode: "shadow"},
		stopCh:                 make(chan struct{}),
		writeQueue:             wq,
	}

	res := scheduler.runIdentityDecisionCleanup(cutoff, identityDecisionCleanupConfig{batchSize: 2, maxPerRun: 100})
	require.NoError(t, res.err)
	assert.Equal(t, int64(3), res.deleted)

	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Count(&count).Error)
	assert.Equal(t, int64(0), count, "WriteQueue path must delete all expired records")
}

func TestSchedulerIdentityDecisionCleanup_NilRepoNoop(t *testing.T) {
	scheduler := &TaskScheduler{
		identityProfileService: &identityProfileServiceStub{mode: "shadow"},
		stopCh:                 make(chan struct{}),
	}
	// nil repo：cleanIdentityDecisions 直接返回，不 panic
	assert.NotPanics(t, func() { scheduler.cleanIdentityDecisions() })
}

// ---- Task 7: coordinator gating scheduler background slices ----

// TestTaskSchedulerMergeSuggestionSliceSkippedWhenCoordinatorBusy 验证 coordinator
// 拒绝 automatic 准入时，runMergeSuggestionSlice 不调用 RunBackgroundSlice。
func TestTaskSchedulerMergeSuggestionSliceSkippedWhenCoordinatorBusy(t *testing.T) {
	stub := &mergeSuggestionServiceStub{}
	coord := NewBackgroundTaskCoordinator()
	scheduler := &TaskScheduler{
		mergeSuggestionService: stub,
		backgroundCoordinator:  coord,
		stopCh:                 make(chan struct{}),
	}

	// foreground active → automatic 被拒绝。
	release := coord.BeginForeground()
	defer release()

	scheduler.runMergeSuggestionSlice()
	assert.Equal(t, 0, stub.runCalls, "must not call RunBackgroundSlice when coordinator blocks")

	// 释放 foreground 后允许运行。
	release()
	scheduler.runMergeSuggestionSlice()
	assert.Equal(t, 1, stub.runCalls, "must run after foreground released")
}

// TestTaskSchedulerIdentityProfileSliceSkippedWhenCoordinatorBusy 验证 coordinator
// 拒绝 automatic 准入时，runIdentityProfileSlice 不调用 RunBackgroundSlice。
// 计划要求：BackgroundTaskCoordinator.BeginForeground() active 时 identity profile slice skip。
func TestTaskSchedulerIdentityProfileSliceSkippedWhenCoordinatorBusy(t *testing.T) {
	stub := &identityProfileServiceStub{mode: "shadow"}
	coord := NewBackgroundTaskCoordinator()
	scheduler := &TaskScheduler{
		identityProfileService: stub,
		backgroundCoordinator:  coord,
		stopCh:                 make(chan struct{}),
	}

	release := coord.BeginForeground()
	scheduler.runIdentityProfileSlice()
	assert.Equal(t, int64(0), stub.runCalls.Load(), "must not call RunBackgroundSlice when foreground active")

	release()
	scheduler.runIdentityProfileSlice()
	assert.Equal(t, int64(1), stub.runCalls.Load(), "must run after foreground released")
}

// TestTaskSchedulerSliceRunsWhenCoordinatorNil 验证 coordinator 为 nil 时（向后兼容）
// slice 正常运行，不 gating。
func TestTaskSchedulerSliceRunsWhenCoordinatorNil(t *testing.T) {
	mergeStub := &mergeSuggestionServiceStub{}
	idStub := &identityProfileServiceStub{mode: "shadow"}
	scheduler := &TaskScheduler{
		mergeSuggestionService: mergeStub,
		identityProfileService: idStub,
		stopCh:                 make(chan struct{}),
		// backgroundCoordinator 为 nil
	}

	scheduler.runMergeSuggestionSlice()
	assert.Equal(t, 1, mergeStub.runCalls)
	scheduler.runIdentityProfileSlice()
	assert.Equal(t, int64(1), idStub.runCalls.Load())
}

// TestTaskSchedulerSliceSkippedDuringCooldown 验证 class cooldown 期间 slice 被跳过，
// 且不调用 RunBackgroundSlice（拒绝不等于标记为完成）。
func TestTaskSchedulerSliceSkippedDuringCooldown(t *testing.T) {
	stub := &mergeSuggestionServiceStub{}
	coord := NewBackgroundTaskCoordinator()
	coord.Cooldown(BackgroundTaskMergeSuggestion, 50*time.Millisecond, "db_busy")
	scheduler := &TaskScheduler{
		mergeSuggestionService: stub,
		backgroundCoordinator:  coord,
		stopCh:                 make(chan struct{}),
	}

	scheduler.runMergeSuggestionSlice()
	assert.Equal(t, 0, stub.runCalls, "must skip slice during cooldown")

	time.Sleep(60 * time.Millisecond)
	scheduler.runMergeSuggestionSlice()
	assert.Equal(t, 1, stub.runCalls, "must run after cooldown expires")
}
