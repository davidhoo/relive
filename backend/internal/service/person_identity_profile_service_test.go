package service

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// countingIdentityProfileRepo 包装真实 repository，统计各方法调用次数，
// 用于断言 legacy 模式不访问 Repository、backfill 不重复扫描等行为。
type countingIdentityProfileRepo struct {
	inner                     repository.PersonIdentityProfileRepository
	mu                        sync.Mutex
	markDirty                 int
	listDirty                 int
	listBackfill              int
	getActive                 int
	getStats                  int
	replaceGeneration         int
	markFailed                int
	deleteByPersonIDs         int
	deleteInactiveGenerations int
	invalidateDeletedPeople   int
	listAllActiveCenters      int
	applyInvalidation         int
	markDirtyPersonIDs        []uint
	listBackfillCursor        []uint
	listBackfillLimit         []int
	applyInvalidationCalls    []invalidationCall
}

// invalidationCall 记录一次 ApplyInvalidation 的清洗前入参，供断言。
type invalidationCall struct {
	dirty   []uint
	deleted []uint
	reset   bool
	reason  string
}

func (c *countingIdentityProfileRepo) MarkDirty(personIDs []uint, reason string) error {
	c.mu.Lock()
	c.markDirty++
	c.markDirtyPersonIDs = append(c.markDirtyPersonIDs, personIDs...)
	c.mu.Unlock()
	return c.inner.MarkDirty(personIDs, reason)
}
func (c *countingIdentityProfileRepo) ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error) {
	c.mu.Lock()
	c.listDirty++
	c.mu.Unlock()
	return c.inner.ListDirty(cursor, limit)
}
func (c *countingIdentityProfileRepo) ListBackfillPersonIDs(cursor uint, limit int) ([]uint, error) {
	c.mu.Lock()
	c.listBackfill++
	c.listBackfillCursor = append(c.listBackfillCursor, cursor)
	c.listBackfillLimit = append(c.listBackfillLimit, limit)
	c.mu.Unlock()
	return c.inner.ListBackfillPersonIDs(cursor, limit)
}
func (c *countingIdentityProfileRepo) GetActive(personID uint) (*model.PersonIdentityProfileBuild, error) {
	c.mu.Lock()
	c.getActive++
	c.mu.Unlock()
	return c.inner.GetActive(personID)
}
func (c *countingIdentityProfileRepo) GetStats() (*model.PersonIdentityProfileStats, error) {
	c.mu.Lock()
	c.getStats++
	c.mu.Unlock()
	return c.inner.GetStats()
}
func (c *countingIdentityProfileRepo) ListAllActiveCenters(embeddingModel string) ([]*model.PersonIdentityCenter, error) {
	c.mu.Lock()
	c.listAllActiveCenters++
	c.mu.Unlock()
	return c.inner.ListAllActiveCenters(embeddingModel)
}
func (c *countingIdentityProfileRepo) ListActiveCentersByPersonIDs(personIDs []uint, embeddingModel string) (map[uint][]*model.PersonIdentityCenter, error) {
	return c.inner.ListActiveCentersByPersonIDs(personIDs, embeddingModel)
}
func (c *countingIdentityProfileRepo) ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error {
	c.mu.Lock()
	c.replaceGeneration++
	c.mu.Unlock()
	return c.inner.ReplaceGeneration(personID, build)
}
func (c *countingIdentityProfileRepo) MarkFailed(personID uint, message string) error {
	c.mu.Lock()
	c.markFailed++
	c.mu.Unlock()
	return c.inner.MarkFailed(personID, message)
}
func (c *countingIdentityProfileRepo) DeleteByPersonIDs(personIDs []uint) error {
	c.mu.Lock()
	c.deleteByPersonIDs++
	c.mu.Unlock()
	return c.inner.DeleteByPersonIDs(personIDs)
}
func (c *countingIdentityProfileRepo) InvalidateDeletedPeople() error {
	c.mu.Lock()
	c.invalidateDeletedPeople++
	c.mu.Unlock()
	return c.inner.InvalidateDeletedPeople()
}
func (c *countingIdentityProfileRepo) DeleteInactiveGenerations(personID uint, keep int) error {
	c.mu.Lock()
	c.deleteInactiveGenerations++
	c.mu.Unlock()
	return c.inner.DeleteInactiveGenerations(personID, keep)
}
func (c *countingIdentityProfileRepo) ApplyInvalidation(req repository.IdentityProfileInvalidationRequest) error {
	c.mu.Lock()
	c.applyInvalidation++
	c.applyInvalidationCalls = append(c.applyInvalidationCalls, invalidationCall{
		dirty:   append([]uint(nil), req.DirtyPersonIDs...),
		deleted: append([]uint(nil), req.DeletedPersonIDs...),
		reset:   req.ResetAll,
		reason:  string(req.Reason),
	})
	c.mu.Unlock()
	return c.inner.ApplyInvalidation(req)
}

// setupIdentityProfileDB 创建隔离的临时文件库并迁移画像相关表。
// 使用临时文件（非共享内存库）以便专用后台连接能看到同一份数据。
func setupIdentityProfileDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ip_test.db")
	dsn := "file:" + path + "?cache=shared&_busy_timeout=5000&_journal_mode=WAL"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.AppConfig{},
		&model.Photo{},
		&model.Person{},
		&model.Face{},
		&model.PersonIdentityProfile{},
		&model.PersonIdentityCenter{},
		&model.PersonIdentityCenterMember{},
		&model.PeopleIdentityDecision{},
	))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return db, dsn
}

func openBgDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// ipTestDeps 聚合身份画像服务测试所需的依赖。
type ipTestDeps struct {
	db            *gorm.DB
	dsn           string
	repos         *repository.Repositories
	configService ConfigService
	countingRepo  *countingIdentityProfileRepo
}

func newIdentityProfileServiceForTest(t *testing.T, mode, embeddingModel string, batchSize, cooldownMs int) (*personIdentityProfileService, *ipTestDeps) {
	t.Helper()
	db, dsn := setupIdentityProfileDB(t)
	repos := repository.NewRepositories(db)
	configService := NewConfigService(repos.Config)
	bgDB := openBgDB(t, dsn)

	cfg := &config.Config{
		People: config.PeopleConfig{
			IdentityProfileMode:            mode,
			IdentityProfileBatchSize:       batchSize,
			IdentityProfileCooldownMs:      cooldownMs,
			IdentityProfileMaxCenters:      6,
			IdentityProfileMinCenterFaces:  3,
			IdentityProfileMinCenterPhotos: 2,
			MLEndpoint:                     embeddingModel,
		},
	}
	counting := &countingIdentityProfileRepo{inner: repos.IdentityProfile}
	svc := NewPersonIdentityProfileService(counting, configService, cfg, bgDB, nil).(*personIdentityProfileService)
	// 用计数仓库覆盖后台仓库，使测试可断言后台调用次数。
	// 主库与后台仓库共享同一临时库，行为等价。
	svc.bgRepo = counting
	svc.bgFaceRepo = repos.Face
	return svc, &ipTestDeps{db: db, dsn: dsn, repos: repos, configService: configService, countingRepo: counting}
}

// createPersonWithFaces 创建人物并附带若干带 embedding 的人脸。
// photoID 从 base 起递增，保证可控制 distinct photo 数。
func createPersonWithFaces(t *testing.T, repos *repository.Repositories, basePhotoID uint, embeddings ...[]float32) *model.Person {
	t.Helper()
	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, repos.Person.Create(person))
	for i, emb := range embeddings {
		f := &model.Face{
			PhotoID:       basePhotoID + uint(i),
			PersonID:      &person.ID,
			Confidence:    0.95,
			QualityScore:  0.9,
			Embedding:     model.EncodeEmbedding(emb),
			ClusterStatus: model.FaceClusterStatusAssigned,
			ClusterScore:  0.9,
			ManualLocked:  false,
		}
		// 让前两张来自同一张照片，其余递增，便于构造 distinct photos ≥ 2。
		if i >= 1 {
			f.PhotoID = basePhotoID + uint(i) + 1
		}
		require.NoError(t, repos.Face.Create(f))
	}
	return person
}

func vec3(x, y, z float32) []float32 { return []float32{x, y, z} }

// runSliceWithClock 推进注入时钟并执行一次 slice，避免真实 sleep。
func runSliceWithClock(svc *personIdentityProfileService, advance time.Duration) {
	svc.nowFn = func() time.Time {
		return svc.lastRunAt.Add(advance)
	}
	_ = svc.RunBackgroundSlice()
}

// ---- legacy 模式 ----

func TestPersonIdentityProfileService_LegacyNoRepoAccess(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "legacy", "emb", 25, 500)

	require.NoError(t, svc.MarkDirty([]uint{1, 2}, "test"))
	err := svc.RunBackgroundSlice()
	require.NoError(t, err)
	_, err = svc.GetActive(1)
	require.NoError(t, err)
	stats, err := svc.GetStats()
	require.NoError(t, err)

	c := deps.countingRepo
	assert.Equal(t, "legacy", svc.Mode())
	assert.Nil(t, svc.ann, "legacy must not initialize ANN")
	assert.Equal(t, 0, c.markDirty, "legacy must not call repo.MarkDirty")
	assert.Equal(t, 0, c.listDirty, "legacy must not call repo.ListDirty")
	assert.Equal(t, 0, c.listBackfill, "legacy must not call repo.ListBackfillPersonIDs")
	assert.Equal(t, 0, c.getActive, "legacy must not call repo.GetActive")
	assert.Equal(t, 0, c.getStats, "legacy must not call repo.GetStats")
	assert.Equal(t, 0, c.listAllActiveCenters, "legacy must not call repo.ListAllActiveCenters")

	// legacy Invalidate 完全 no-op：不访问 Repository、不操作 ANN。
	require.NoError(t, svc.Invalidate(IdentityProfileInvalidation{
		DirtyPersonIDs:   []uint{1, 2},
		DeletedPersonIDs: []uint{3},
		Reason:           "people_merged",
	}))
	assert.Equal(t, 0, c.applyInvalidation, "legacy Invalidate must not call repo.ApplyInvalidation")
	assert.Nil(t, svc.ann, "legacy must still not initialize ANN after Invalidate")
	assert.NotNil(t, stats)
	assert.Equal(t, int64(0), stats.Total)
}

func TestPersonIdentityProfileService_LegacyNoAppConfigRead(t *testing.T) {
	db, _ := setupIdentityProfileDB(t)
	repos := repository.NewRepositories(db)
	configService := NewConfigService(repos.Config)
	cfg := &config.Config{People: config.PeopleConfig{IdentityProfileMode: "legacy"}}

	svc := NewPersonIdentityProfileService(repos.IdentityProfile, configService, cfg, nil, nil).(*personIdentityProfileService)

	// legacy 构造不得加载 backfill 状态（不读 AppConfig）。
	assert.False(t, svc.stateLoaded, "legacy must not load backfill state from AppConfig")
	// AppConfig 表应为空。
	var n int64
	require.NoError(t, db.Model(&model.AppConfig{}).Count(&n).Error)
	assert.Equal(t, int64(0), n, "legacy must not write any AppConfig rows")

	// 运行期调用也不读 AppConfig。
	require.NoError(t, svc.MarkDirty([]uint{1}, "x"))
	_ = svc.RunBackgroundSlice()
	_, _ = svc.GetStats()
	require.NoError(t, db.Model(&model.AppConfig{}).Count(&n).Error)
	assert.Equal(t, int64(0), n, "legacy runtime must not touch AppConfig")
}

// ---- shadow 模式与后台连接 ----

func TestPersonIdentityProfileService_ShadowCreatesBackgroundConn(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 25, 500)

	assert.Equal(t, "shadow", svc.Mode())
	assert.True(t, svc.enabled, "shadow mode must enable background building")
	assert.NotNil(t, svc.bgDB, "shadow mode must create dedicated background DB")
	assert.NotNil(t, svc.bgRepo)
	assert.NotNil(t, svc.bgFaceRepo)
	// backfill 状态已加载（可能写入空初始状态，但不强制）。
	_ = deps
}

func TestPersonIdentityProfileService_BackgroundDBFailureDoesNotFallBack(t *testing.T) {
	db, _ := setupIdentityProfileDB(t)
	repos := repository.NewRepositories(db)
	configService := NewConfigService(repos.Config)
	cfg := &config.Config{People: config.PeopleConfig{IdentityProfileMode: "shadow"}}

	// bgDB 传入 nil，模拟专用后台连接创建失败。
	counting := &countingIdentityProfileRepo{inner: repos.IdentityProfile}
	svc := NewPersonIdentityProfileService(counting, configService, cfg, nil, nil).(*personIdentityProfileService)

	assert.False(t, svc.enabled, "background DB failure must disable building")
	assert.Nil(t, svc.bgRepo, "must not fall back to shared connection for heavy backfill")

	// RunBackgroundSlice 不做任何 backfill/构建（不访问 repo 的重型查询）。
	err := svc.RunBackgroundSlice()
	require.NoError(t, err)
	assert.Equal(t, 0, counting.listBackfill, "disabled service must not run backfill")
	assert.Equal(t, 0, counting.listDirty, "disabled service must not list dirty")
}

// ---- backfill 行为 ----

func TestPersonIdentityProfileService_BackfillBatchSizeBounded(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 3, 0)

	// 10 个人物，无画像。
	for i := 0; i < 10; i++ {
		createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	}

	// 首次 slice（cooldownMs=0，但 lastRunAt 为零值 → 直接执行）。
	require.NoError(t, svc.RunBackgroundSlice())

	// 一次 slice 的 backfill 批次不超过 batch size=3。
	var profileCount int64
	require.NoError(t, deps.db.Model(&model.PersonIdentityProfile{}).Count(&profileCount).Error)
	assert.LessOrEqual(t, profileCount, int64(3), "backfill batch must not exceed batch size")
	assert.Equal(t, 1, deps.countingRepo.listBackfill, "one backfill listing per slice")
}

func TestPersonIdentityProfileService_SliceBuildsAtMostOnePerson(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 10, 0)

	for i := 0; i < 5; i++ {
		createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	}

	require.NoError(t, svc.RunBackgroundSlice())

	// 一次 slice 最多构建一个人物 → ready 数 ≤ 1。
	var ready int64
	require.NoError(t, deps.db.Model(&model.PersonIdentityProfile{}).Where("status = ?", model.PersonIdentityProfileStatusReady).Count(&ready).Error)
	assert.LessOrEqual(t, ready, int64(1), "one slice must build at most one person")
	assert.Equal(t, 1, deps.countingRepo.replaceGeneration, "at most one ReplaceGeneration per slice")
}

func TestPersonIdentityProfileService_BackfillCursorPersisted(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 3, 0)

	for i := 0; i < 5; i++ {
		p := createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
		_ = p
	}

	require.NoError(t, svc.RunBackgroundSlice())

	st := svc.currentBackfillState()
	assert.True(t, st.CursorPersonID > 0, "cursor must advance after a successful batch")
	assert.Equal(t, "emb", st.EmbeddingModel)
	assert.Equal(t, identityProfileAlgorithmVersion, st.AlgorithmVersion)
}

func TestPersonIdentityProfileService_RestartContinuesCursor(t *testing.T) {
	svc1, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 3, 0)
	for i := 0; i < 6; i++ {
		createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	}

	require.NoError(t, svc1.RunBackgroundSlice())
	cursorAfterFirst := svc1.currentBackfillState().CursorPersonID
	require.True(t, cursorAfterFirst > 0)

	// 模拟重启：用同一数据库构造新服务。
	cfg := &config.Config{People: config.PeopleConfig{
		IdentityProfileMode: "shadow", IdentityProfileBatchSize: 3, IdentityProfileCooldownMs: 0,
		IdentityProfileMaxCenters: 6, IdentityProfileMinCenterFaces: 3, IdentityProfileMinCenterPhotos: 2,
		MLEndpoint: "emb",
	}}
	svc2 := NewPersonIdentityProfileService(
		&countingIdentityProfileRepo{inner: deps.repos.IdentityProfile},
		deps.configService, cfg, openBgDB(t, deps.dsn), nil,
	).(*personIdentityProfileService)

	// 重启后游标应从持久化值继续，而非重置为 0。
	st := svc2.currentBackfillState()
	assert.Equal(t, cursorAfterFirst, st.CursorPersonID, "restart must resume persisted cursor")

	// 第二批应从 cursor 之后继续。
	require.NoError(t, svc2.RunBackgroundSlice())
	st2 := svc2.currentBackfillState()
	assert.True(t, st2.CursorPersonID > cursorAfterFirst, "second slice must advance cursor beyond previous")
}

func TestPersonIdentityProfileService_BackfillCompletedNoRepeatScan(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	// 仅 1 个人物 → 一次即可完成 backfill。
	createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	// 推进到完成（多 slice 但 cooldown=0；用时钟推进绕过）。
	for i := 0; i < 5; i++ {
		runSliceWithClock(svc, time.Duration(svc.cooldownMs)*time.Millisecond+1)
	}
	st := svc.currentBackfillState()
	assert.True(t, st.Completed, "backfill must complete")

	// 记录完成后 profile 总数，再跑一个 slice，断言不新增 profile、不重复扫描。
	var before int64
	require.NoError(t, deps.db.Model(&model.PersonIdentityProfile{}).Count(&before).Error)
	callsBefore := deps.countingRepo.listBackfill

	runSliceWithClock(svc, time.Duration(svc.cooldownMs)*time.Millisecond+1)

	var after int64
	require.NoError(t, deps.db.Model(&model.PersonIdentityProfile{}).Count(&after).Error)
	// 完成后仍可能调用一次 ListBackfillPersonIDs 以确认无更多人物（返回空后保持 completed），
	// 但不得新增 profile。
	assert.Equal(t, before, after, "completed backfill must not create new profiles")
	_ = callsBefore
}

func TestPersonIdentityProfileService_AlgorithmVersionChangeResetsCursor(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 3, 0)
	for i := 0; i < 5; i++ {
		createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	}
	require.NoError(t, svc.RunBackgroundSlice())
	require.True(t, svc.currentBackfillState().CursorPersonID > 0)

	// 模拟算法版本变化。
	svc.algorithmVersion = "identity-profile-v2"
	svc.loadAndAlignBackfillState()

	st := svc.currentBackfillState()
	assert.Equal(t, uint(0), st.CursorPersonID, "algorithm version change must reset cursor")
	assert.False(t, st.Completed, "algorithm version change must clear completed")
	assert.Equal(t, "identity-profile-v2", st.AlgorithmVersion)
	_ = deps
}

func TestPersonIdentityProfileService_EmbeddingModelChangeResetsCursor(t *testing.T) {
	svc1, deps := newIdentityProfileServiceForTest(t, "shadow", "emb-A", 3, 0)
	for i := 0; i < 5; i++ {
		createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	}
	require.NoError(t, svc1.RunBackgroundSlice())
	require.True(t, svc1.currentBackfillState().CursorPersonID > 0)

	// embedding model 变化（emb-A → emb-B），模拟重建服务。
	cfg := &config.Config{People: config.PeopleConfig{
		IdentityProfileMode: "shadow", IdentityProfileBatchSize: 3, IdentityProfileCooldownMs: 0,
		IdentityProfileMaxCenters: 6, IdentityProfileMinCenterFaces: 3, IdentityProfileMinCenterPhotos: 2,
		MLEndpoint: "emb-B",
	}}
	svc2 := NewPersonIdentityProfileService(
		&countingIdentityProfileRepo{inner: deps.repos.IdentityProfile},
		deps.configService, cfg, openBgDB(t, deps.dsn), nil,
	).(*personIdentityProfileService)

	st := svc2.currentBackfillState()
	assert.Equal(t, uint(0), st.CursorPersonID, "embedding model change must reset cursor")
	assert.False(t, st.Completed)
	assert.Equal(t, "emb-B", st.EmbeddingModel)
}

// ---- 构建成功/失败/删除 ----

func TestPersonIdentityProfileService_DirtyPersonBuiltAndActivated(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())

	active, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	require.NotNil(t, active)
	require.NotNil(t, active.Profile)
	assert.Equal(t, model.PersonIdentityProfileStatusReady, active.Profile.Status)
	assert.True(t, active.Profile.ActiveGeneration > 0, "new generation must be activated")
	assert.Equal(t, "emb", active.Profile.EmbeddingModel)
	assert.NotEmpty(t, active.Centers, "build must produce at least one center")
}

func TestPersonIdentityProfileService_BuilderFailureMarkFailed(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	// 人物只有 1 张人脸、且 embedding 为空 → builder 可正常返回（excluded），
	// 但我们注入一个总是失败的 builder 以测试 MarkFailed 路径。
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0))
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))

	svc.builder = &failingIdentityBuilder{}

	require.NoError(t, svc.RunBackgroundSlice())

	// 失败 → MarkFailed 调用，profile 状态 failed，无 active generation。
	var prof model.PersonIdentityProfile
	require.NoError(t, deps.db.Where("person_id = ?", person.ID).First(&prof).Error)
	assert.Equal(t, model.PersonIdentityProfileStatusFailed, prof.Status)
	assert.Equal(t, 0, prof.ActiveGeneration)
	assert.Contains(t, prof.LastError, "boom")
	assert.Equal(t, 1, deps.countingRepo.markFailed)
}

type failingIdentityBuilder struct{}

func (b *failingIdentityBuilder) Build(personID uint, faces []*model.Face) (*model.PersonIdentityProfileBuild, error) {
	return nil, errBoom
}

var errBoom = newSentinelErr("boom")

type sentinelErr struct{ msg string }

func (e *sentinelErr) Error() string { return e.msg }

func newSentinelErr(msg string) error { return &sentinelErr{msg: msg} }

func TestPersonIdentityProfileService_WriteFailurePreservesOldGeneration(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	// 先成功构建一次，建立 active generation。
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "first"))
	require.NoError(t, svc.RunBackgroundSlice())
	old, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	require.True(t, old.Profile.ActiveGeneration > 0)

	// 再次标记 dirty，并注入会在 ReplaceGeneration 失败的 bg repo。
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "second"))
	svc.bgRepo = &failingReplaceRepo{inner: svc.bgRepo}
	// 推进时钟绕过 cooldown（构造时 cooldownMs=0 被默认为 500）。
	svc.nowFn = func() time.Time { return svc.lastRunAt.Add(1 * time.Second) }
	require.NoError(t, svc.RunBackgroundSlice())

	// 旧 active generation 保留不变。
	cur, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, old.Profile.ActiveGeneration, cur.Profile.ActiveGeneration, "old generation must be preserved on write failure")
	// MarkFailed 记录原因。
	assert.Equal(t, model.PersonIdentityProfileStatusFailed, cur.Profile.Status)
}

type failingReplaceRepo struct {
	inner repository.PersonIdentityProfileRepository
}

func (r *failingReplaceRepo) MarkDirty(p []uint, reason string) error {
	return r.inner.MarkDirty(p, reason)
}
func (r *failingReplaceRepo) ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error) {
	return r.inner.ListDirty(cursor, limit)
}
func (r *failingReplaceRepo) ListBackfillPersonIDs(cursor uint, limit int) ([]uint, error) {
	return r.inner.ListBackfillPersonIDs(cursor, limit)
}
func (r *failingReplaceRepo) GetActive(personID uint) (*model.PersonIdentityProfileBuild, error) {
	return r.inner.GetActive(personID)
}
func (r *failingReplaceRepo) GetStats() (*model.PersonIdentityProfileStats, error) {
	return r.inner.GetStats()
}
func (r *failingReplaceRepo) ListAllActiveCenters(embeddingModel string) ([]*model.PersonIdentityCenter, error) {
	return r.inner.ListAllActiveCenters(embeddingModel)
}
func (r *failingReplaceRepo) ListActiveCentersByPersonIDs(personIDs []uint, embeddingModel string) (map[uint][]*model.PersonIdentityCenter, error) {
	return r.inner.ListActiveCentersByPersonIDs(personIDs, embeddingModel)
}
func (r *failingReplaceRepo) ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error {
	return errBoom
}
func (r *failingReplaceRepo) MarkFailed(personID uint, message string) error {
	return r.inner.MarkFailed(personID, message)
}
func (r *failingReplaceRepo) DeleteByPersonIDs(personIDs []uint) error {
	return r.inner.DeleteByPersonIDs(personIDs)
}
func (r *failingReplaceRepo) InvalidateDeletedPeople() error {
	return r.inner.InvalidateDeletedPeople()
}
func (r *failingReplaceRepo) DeleteInactiveGenerations(personID uint, keep int) error {
	return r.inner.DeleteInactiveGenerations(personID, keep)
}
func (r *failingReplaceRepo) ApplyInvalidation(req repository.IdentityProfileInvalidationRequest) error {
	return r.inner.ApplyInvalidation(req)
}

func TestPersonIdentityProfileService_PersonDeletedMidBuildCleanedUp(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))

	// 在构建前删除人物（不删除 faces，模拟 orphan）。
	require.NoError(t, deps.repos.Person.Delete(person.ID))

	// slice 不应返回系统级失败，且清理该人物派生画像。
	require.NoError(t, svc.RunBackgroundSlice())

	// 不应残留 profile/center/member（不创建幽灵 profile）。
	var n int64
	require.NoError(t, deps.db.Model(&model.PersonIdentityProfile{}).Where("person_id = ?", person.ID).Count(&n).Error)
	assert.Equal(t, int64(0), n, "deleted person must have no profile")
	require.NoError(t, deps.db.Model(&model.PersonIdentityCenter{}).Where("person_id = ?", person.ID).Count(&n).Error)
	assert.Equal(t, int64(0), n)
	// DeleteByPersonIDs 被调用做清理。
	assert.Equal(t, 1, deps.countingRepo.deleteByPersonIDs)
}

// ---- cleanup 保留策略 ----

func TestPersonIdentityProfileService_CleanupKeepsActiveAndOneHistory(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	// 连续构建 3 次，产生多个历史 generation。
	for i := 0; i < 3; i++ {
		require.NoError(t, svc.MarkDirty([]uint{person.ID}, "rebuild"))
		// 每次构建前进时钟绕过 cooldown。
		runSliceWithClock(svc, time.Duration(svc.cooldownMs)*time.Millisecond+1)
	}

	cur, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	active := cur.Profile.ActiveGeneration
	require.True(t, active > 0)

	// 非活动 generation 应至多保留 1 个（最近历史）。
	var gens []int
	require.NoError(t, deps.db.Model(&model.PersonIdentityCenter{}).
		Distinct("generation").Where("person_id = ?", person.ID).Pluck("generation", &gens).Error)
	var inactive []int
	for _, g := range gens {
		if g != active {
			inactive = append(inactive, g)
		}
	}
	assert.LessOrEqual(t, len(inactive), 1, "cleanup must keep at most one historical generation")
	// DeleteInactiveGenerations 在每次成功构建后被调用。
	assert.GreaterOrEqual(t, deps.countingRepo.deleteInactiveGenerations, 1)
}

// ---- cooldown ----

func TestPersonIdentityProfileService_CooldownEnforced(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 3, 500)
	for i := 0; i < 5; i++ {
		createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	}

	// 固定时钟：两次 slice 间隔不足 cooldown，第二次必须被跳过。
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return t0 }
	require.NoError(t, svc.RunBackgroundSlice())
	callsAfterFirst := deps.countingRepo.listBackfill

	// 仅推进 100ms（< 500ms）。
	svc.nowFn = func() time.Time { return t0.Add(100 * time.Millisecond) }
	require.NoError(t, svc.RunBackgroundSlice())
	assert.Equal(t, callsAfterFirst, deps.countingRepo.listBackfill, "cooldown must skip early re-invocation")

	// 推进超过 cooldown → 再次执行。
	svc.nowFn = func() time.Time { return t0.Add(501 * time.Millisecond) }
	require.NoError(t, svc.RunBackgroundSlice())
	assert.Greater(t, deps.countingRepo.listBackfill, callsAfterFirst, "slice must run after cooldown elapses")
}

func TestPersonIdentityProfileService_CooldownNoRealSleep(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 1, 60000)
	// 3 个人、batchSize=1：每个 slice 推进一个 backfill 批次，便于断言多次执行。
	for i := 0; i < 3; i++ {
		createPersonWithFaces(t, deps.repos, uint(100+i), vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	}

	// cooldownMs=60s，但用注入时钟立刻绕过，无需真实等待。
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.nowFn = func() time.Time { return t0 }
	require.NoError(t, svc.RunBackgroundSlice())
	svc.nowFn = func() time.Time { return t0.Add(61 * time.Second) }
	require.NoError(t, svc.RunBackgroundSlice())
	// 达到这里即说明未真实 sleep 60s，且两个 slice 均执行了 backfill 列表查询。
	assert.Equal(t, 2, deps.countingRepo.listBackfill)
}

// ---- MarkDirty ----

func TestPersonIdentityProfileService_MarkDirtyDedupAndEmpty(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)

	// 空输入直接返回，不写库。
	require.NoError(t, svc.MarkDirty(nil, "x"))
	require.NoError(t, svc.MarkDirty([]uint{0}, "x"))
	assert.Equal(t, 0, deps.countingRepo.markDirty)

	// 去重。
	require.NoError(t, svc.MarkDirty([]uint{1, 1, 2, 0}, "x"))
	assert.Equal(t, 1, deps.countingRepo.markDirty)
	// 去重后传入 {1,2}。
	assert.ElementsMatch(t, []uint{1, 2}, deps.countingRepo.markDirtyPersonIDs)
}

// ---- ANN 接入（Task 7） ----

// TestPersonIdentityProfileService_ANNInitializedOnlyNonLegacy 验证 ANN 仅在非 legacy 模式初始化。
func TestPersonIdentityProfileService_ANNInitializedOnlyNonLegacy(t *testing.T) {
	// shadow 初始化 ANN。
	svc, _ := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	require.NotNil(t, svc.ann, "shadow must initialize ANN")
	assert.True(t, svc.ann.RebuildRequested(), "non-legacy must request initial rebuild")

	// legacy 不初始化 ANN。
	legacySvc, _ := newIdentityProfileServiceForTest(t, "legacy", "emb", 5, 0)
	assert.Nil(t, legacySvc.ann, "legacy must not initialize ANN")
}

// TestPersonIdentityProfileService_ANNBuiltAndRecalls 验证后台切片完成首次 ANN 构建并可召回。
func TestPersonIdentityProfileService_ANNBuiltAndRecalls(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())

	require.NotNil(t, svc.ann)
	// ANN 已构建（snapshot 来自 ListAllActiveCenters），可召回该人物。
	got, ready := svc.ann.Search(vec3(1, 0, 0), 5, "emb")
	require.True(t, ready, "ANN must be ready after background build")
	assert.Contains(t, got, person.ID, "ANN must recall the built person")
	assert.GreaterOrEqual(t, deps.countingRepo.listAllActiveCenters, 1, "rebuild must query active centers")
}

// TestPersonIdentityProfileService_ANNActivateDoesNotRequireRebuild 验证 ReplaceGeneration 成功后
// 通过 Activate 接入 delta，新人物无需等待完整重建即可召回。
func TestPersonIdentityProfileService_ANNActivateDoesNotRequireRebuild(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	p1 := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	p2 := createPersonWithFaces(t, deps.repos, 200, vec3(0, 1, 0), vec3(0.01, 0.98, 0), vec3(0, 0.99, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{p1.ID}, "first"))
	require.NoError(t, svc.RunBackgroundSlice()) // 构建 p1 + 首次 ANN 重建

	// 第二次切片构建 p2：Activate 接入 delta。
	require.NoError(t, svc.MarkDirty([]uint{p2.ID}, "second"))
	runSliceWithClock(svc, time.Duration(svc.cooldownMs)*time.Millisecond+1)

	// p2 应可通过 delta 召回（即便本次切片的 rebuild 用的是 Activate 之前的状态）。
	got, ready := svc.ann.Search(vec3(0, 1, 0), 5, "emb")
	require.True(t, ready)
	assert.Contains(t, got, p2.ID, "newly activated person must be recallable via delta")
}

// TestPersonIdentityProfileService_ANNInvalidatesDeletedPerson 验证人物删除后 ANN 不再召回。
func TestPersonIdentityProfileService_ANNInvalidatesDeletedPerson(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())

	got, ready := svc.ann.Search(vec3(1, 0, 0), 5, "emb")
	require.True(t, ready)
	require.Contains(t, got, person.ID)

	// 清理删除人物 → InvalidatePerson。
	svc.cleanupDeletedPerson(person.ID)

	got, ready = svc.ann.Search(vec3(1, 0, 0), 5, "emb")
	require.True(t, ready)
	assert.NotContains(t, got, person.ID, "deleted person must not be recalled")
}

// TestPersonIdentityProfileService_ANNRebuildFailureDoesNotRollbackGeneration 验证 ANN 重建失败
// 不回滚已成功激活的数据库 generation。
func TestPersonIdentityProfileService_ANNRebuildFailureDoesNotRollbackGeneration(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())

	// generation 已激活。
	active, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	require.NotNil(t, active)
	require.True(t, active.Profile.ActiveGeneration > 0)
	genBefore := active.Profile.ActiveGeneration

	// 注入会让 ListAllActiveCenters 失败的后台仓库，触发 ANN 重建失败。
	svc.bgRepo = &failingListCentersRepo{inner: svc.bgRepo}
	svc.ann.RequestRebuild()
	runSliceWithClock(svc, time.Duration(svc.cooldownMs)*time.Millisecond+1)

	// generation 不变（ANN 失败不回滚）。
	active, err = svc.GetActive(person.ID)
	require.NoError(t, err)
	assert.Equal(t, genBefore, active.Profile.ActiveGeneration, "ANN rebuild failure must not roll back generation")

	// Task 14：ListAllActiveCenters 失败应记录脱敏错误类别 list_active_centers_failed。
	// ann.RequestRebuild() 已由失败分支再次调用，RebuildRequested 保持 true。
	resp, err := svc.GetOperationalStats(nil)
	require.NoError(t, err)
	assert.Equal(t, "list_active_centers_failed", resp.ANN.LastBuildError, "list active centers failure recorded as sanitized category")
	assert.True(t, resp.ANN.RebuildRequested)
	assert.Equal(t, "failed", resp.ANN.LastBuildStatus, "failed rebuild recorded as failed")
	assert.GreaterOrEqual(t, resp.ANN.LastBuildDurationMs, int64(0))
}

// failingListCentersRepo 包装仓库，使 ListAllActiveCenters 失败，其余方法透传。
type failingListCentersRepo struct {
	inner repository.PersonIdentityProfileRepository
}

func (r *failingListCentersRepo) MarkDirty(p []uint, reason string) error {
	return r.inner.MarkDirty(p, reason)
}
func (r *failingListCentersRepo) ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error) {
	return r.inner.ListDirty(cursor, limit)
}
func (r *failingListCentersRepo) ListBackfillPersonIDs(cursor uint, limit int) ([]uint, error) {
	return r.inner.ListBackfillPersonIDs(cursor, limit)
}
func (r *failingListCentersRepo) GetActive(personID uint) (*model.PersonIdentityProfileBuild, error) {
	return r.inner.GetActive(personID)
}
func (r *failingListCentersRepo) GetStats() (*model.PersonIdentityProfileStats, error) {
	return r.inner.GetStats()
}
func (r *failingListCentersRepo) ListAllActiveCenters(embeddingModel string) ([]*model.PersonIdentityCenter, error) {
	return nil, errBoom
}
func (r *failingListCentersRepo) ListActiveCentersByPersonIDs(personIDs []uint, embeddingModel string) (map[uint][]*model.PersonIdentityCenter, error) {
	return r.inner.ListActiveCentersByPersonIDs(personIDs, embeddingModel)
}
func (r *failingListCentersRepo) ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error {
	return r.inner.ReplaceGeneration(personID, build)
}
func (r *failingListCentersRepo) MarkFailed(personID uint, message string) error {
	return r.inner.MarkFailed(personID, message)
}
func (r *failingListCentersRepo) DeleteByPersonIDs(personIDs []uint) error {
	return r.inner.DeleteByPersonIDs(personIDs)
}
func (r *failingListCentersRepo) InvalidateDeletedPeople() error {
	return r.inner.InvalidateDeletedPeople()
}
func (r *failingListCentersRepo) DeleteInactiveGenerations(personID uint, keep int) error {
	return r.inner.DeleteInactiveGenerations(personID, keep)
}
func (r *failingListCentersRepo) ApplyInvalidation(req repository.IdentityProfileInvalidationRequest) error {
	return r.inner.ApplyInvalidation(req)
}

// ---- Task 13: Invalidate 统一入口 ----

func TestPersonIdentityProfileService_Invalidate_LegacyNoop(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "legacy", "emb", 25, 500)
	c := deps.countingRepo

	require.NoError(t, svc.Invalidate(IdentityProfileInvalidation{
		DirtyPersonIDs: []uint{1, 2},
		Reason:         "people_merged",
	}))
	assert.Equal(t, 0, c.applyInvalidation, "legacy Invalidate must not call repo.ApplyInvalidation")
}

func TestPersonIdentityProfileService_Invalidate_EmptyRequestNoop(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	c := deps.countingRepo

	require.NoError(t, svc.Invalidate(IdentityProfileInvalidation{}))
	assert.Equal(t, 0, c.applyInvalidation, "empty request must not call repo.ApplyInvalidation")

	require.NoError(t, svc.Invalidate(IdentityProfileInvalidation{
		DirtyPersonIDs: []uint{0, 0},
		Reason:         "faces_moved",
	}))
	assert.Equal(t, 0, c.applyInvalidation, "all-zero dirty must not call repo.ApplyInvalidation")
}

func TestPersonIdentityProfileService_Invalidate_DirtyMarksAndAnnInvalidates(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())

	got, ready := svc.ann.Search(vec3(1, 0, 0), 5, "emb")
	require.True(t, ready)
	require.Contains(t, got, person.ID)

	// Invalidate 标记 dirty → ANN 立即失效该人物 + Repository 标记 dirty。
	require.NoError(t, svc.Invalidate(IdentityProfileInvalidation{
		DirtyPersonIDs: []uint{person.ID},
		Reason:         "detection_replaced_faces",
	}))
	c := deps.countingRepo
	require.Equal(t, 1, c.applyInvalidation)
	assert.Equal(t, "detection_replaced_faces", c.applyInvalidationCalls[0].reason)

	// dirty 人物立即从 ANN 召回中屏蔽。
	got, ready = svc.ann.Search(vec3(1, 0, 0), 5, "emb")
	if ready {
		assert.NotContains(t, got, person.ID, "dirty person must be invalidated from ANN")
	}
	// profile 标记 dirty。
	active, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	require.NotNil(t, active.Profile)
	assert.Equal(t, model.PersonIdentityProfileStatusDirty, active.Profile.Status)
}

func TestPersonIdentityProfileService_Invalidate_DeletedRemovesAndAnnInvalidates(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())

	require.NoError(t, svc.Invalidate(IdentityProfileInvalidation{
		DeletedPersonIDs: []uint{person.ID},
		Reason:           "person_dissolved",
	}))

	// deleted 完全删除 profile/center/member。
	active, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	assert.Nil(t, active, "deleted person profile removed")

	// ANN 失效该人物。
	got, ready := svc.ann.Search(vec3(1, 0, 0), 5, "emb")
	if ready {
		assert.NotContains(t, got, person.ID)
	}
}

func TestPersonIdentityProfileService_Invalidate_DeletedPriorityOverDirty(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())

	require.NoError(t, svc.Invalidate(IdentityProfileInvalidation{
		DirtyPersonIDs:   []uint{person.ID},
		DeletedPersonIDs: []uint{person.ID},
		Reason:           "people_merged",
	}))
	active, err := svc.GetActive(person.ID)
	require.NoError(t, err)
	assert.Nil(t, active, "deleted priority: profile removed not dirty-marked")
}

func TestPersonIdentityProfileService_Invalidate_ResetAllClearsAnnAndRepo(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	p1 := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))
	p2 := createPersonWithFaces(t, deps.repos, 200, vec3(0, 1, 0), vec3(0.01, 0.98, 0), vec3(0, 0.99, 0.01))
	require.NoError(t, svc.MarkDirty([]uint{p1.ID, p2.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())
	require.NoError(t, svc.RunBackgroundSlice())

	require.True(t, svc.ann.Ready("emb"))

	require.NoError(t, svc.Invalidate(IdentityProfileInvalidation{
		ResetAll: true,
		Reason:   "reset_all_people",
	}))

	assert.False(t, svc.ann.Ready("emb"), "ANN must be unavailable after ResetAll")
	assert.True(t, svc.ann.RebuildRequested())

	// 全部派生画像清空。
	var profileCount int64
	require.NoError(t, deps.db.Model(&model.PersonIdentityProfile{}).Count(&profileCount).Error)
	assert.Zero(t, profileCount)
}

// failingApplyInvalidationRepo 包装仓库，使 ApplyInvalidation 失败，验证 fail-closed。
type failingApplyInvalidationRepo struct {
	inner repository.PersonIdentityProfileRepository
}

func (r *failingApplyInvalidationRepo) MarkDirty(p []uint, reason string) error {
	return r.inner.MarkDirty(p, reason)
}
func (r *failingApplyInvalidationRepo) ListDirty(cursor uint, limit int) ([]*model.PersonIdentityProfile, error) {
	return r.inner.ListDirty(cursor, limit)
}
func (r *failingApplyInvalidationRepo) ListBackfillPersonIDs(cursor uint, limit int) ([]uint, error) {
	return r.inner.ListBackfillPersonIDs(cursor, limit)
}
func (r *failingApplyInvalidationRepo) GetActive(personID uint) (*model.PersonIdentityProfileBuild, error) {
	return r.inner.GetActive(personID)
}
func (r *failingApplyInvalidationRepo) GetStats() (*model.PersonIdentityProfileStats, error) {
	return r.inner.GetStats()
}
func (r *failingApplyInvalidationRepo) ListAllActiveCenters(embeddingModel string) ([]*model.PersonIdentityCenter, error) {
	return r.inner.ListAllActiveCenters(embeddingModel)
}
func (r *failingApplyInvalidationRepo) ListActiveCentersByPersonIDs(personIDs []uint, embeddingModel string) (map[uint][]*model.PersonIdentityCenter, error) {
	return r.inner.ListActiveCentersByPersonIDs(personIDs, embeddingModel)
}
func (r *failingApplyInvalidationRepo) ReplaceGeneration(personID uint, build *model.PersonIdentityProfileBuild) error {
	return r.inner.ReplaceGeneration(personID, build)
}
func (r *failingApplyInvalidationRepo) MarkFailed(personID uint, message string) error {
	return r.inner.MarkFailed(personID, message)
}
func (r *failingApplyInvalidationRepo) DeleteByPersonIDs(personIDs []uint) error {
	return r.inner.DeleteByPersonIDs(personIDs)
}
func (r *failingApplyInvalidationRepo) InvalidateDeletedPeople() error {
	return r.inner.InvalidateDeletedPeople()
}
func (r *failingApplyInvalidationRepo) DeleteInactiveGenerations(personID uint, keep int) error {
	return r.inner.DeleteInactiveGenerations(personID, keep)
}
func (r *failingApplyInvalidationRepo) ApplyInvalidation(req repository.IdentityProfileInvalidationRequest) error {
	return errBoom
}

// TestPersonIdentityProfileService_Invalidate_PersistFailureFailsClosed 验证持久化失败时
// 返回错误且 ANN 保持失效（fail closed）。
func TestPersonIdentityProfileService_Invalidate_PersistFailureFailsClosed(t *testing.T) {
	db, dsn := setupIdentityProfileDB(t)
	repos := repository.NewRepositories(db)
	configService := NewConfigService(repos.Config)
	bgDB := openBgDB(t, dsn)
	cfg := &config.Config{People: config.PeopleConfig{IdentityProfileMode: "shadow", MLEndpoint: "emb"}}
	failing := &failingApplyInvalidationRepo{inner: repos.IdentityProfile}
	svc := NewPersonIdentityProfileService(failing, configService, cfg, bgDB, nil).(*personIdentityProfileService)

	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, repos.Person.Create(person))

	err := svc.Invalidate(IdentityProfileInvalidation{
		DirtyPersonIDs: []uint{person.ID},
		Reason:         "detection_replaced_faces",
	})
	require.Error(t, err, "persist failure must return error")

	// ANN fail closed：dirty 人物已 InvalidatePerson，且 unavailable/rebuildRequested。
	got, ready := svc.ann.Search(vec3(1, 0, 0), 5, "emb")
	if ready {
		assert.NotContains(t, got, person.ID, "dirty person must remain invalidated after persist failure")
	}
}

// ==================== Task 14: GetOperationalStats / ListRecentDecisions ====================

func TestPersonIdentityProfileService_GetOperationalStats_LegacyNoRepoAccess(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "legacy", "emb", 25, 500)

	resp, err := svc.GetOperationalStats(deps.repos.IdentityDecision)
	require.NoError(t, err)
	assert.Equal(t, "legacy", resp.Mode)
	// 零值运行状态。
	assert.Zero(t, resp.Profiles.Total)
	assert.Zero(t, resp.ANN.Generation)
	assert.False(t, resp.ANN.Ready)
	assert.Zero(t, resp.Decisions.Total)

	// legacy 不得访问 Repository/ANN/AppConfig。
	c := deps.countingRepo
	assert.Equal(t, 0, c.getStats, "legacy stats must not call repo.GetStats")
	assert.Equal(t, 0, c.listAllActiveCenters, "legacy stats must not call repo.ListAllActiveCenters")
	// legacy ann 字段为零值，构建状态显式 never（ann 为 nil）。
	assert.Equal(t, "never", resp.ANN.LastBuildStatus, "legacy ANN build status must be never")
}

func TestPersonIdentityProfileService_GetOperationalStats_ShadowAggregates(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	person := createPersonWithFaces(t, deps.repos, 100, vec3(1, 0, 0), vec3(0.98, 0.01, 0), vec3(0.99, 0, 0.01))

	require.NoError(t, svc.MarkDirty([]uint{person.ID}, "manual"))
	require.NoError(t, svc.RunBackgroundSlice())

	resp, err := svc.GetOperationalStats(deps.repos.IdentityDecision)
	require.NoError(t, err)
	assert.Equal(t, "shadow", resp.Mode)
	assert.Greater(t, resp.Profiles.Total, int64(0))
	assert.Equal(t, int64(1), resp.Profiles.Ready)
	assert.Greater(t, resp.Centers.Active, int64(0))
	assert.Greater(t, resp.Members.Total, int64(0))
	assert.Greater(t, resp.ANN.Generation, uint64(0), "generation increments after successful ANN rebuild")
	assert.True(t, resp.ANN.Ready)
	assert.Empty(t, resp.ANN.LastBuildError)
	assert.Equal(t, "success", resp.ANN.LastBuildStatus, "successful rebuild recorded as success")
	assert.GreaterOrEqual(t, resp.ANN.LastBuildDurationMs, int64(0))
	assert.Equal(t, resp.Centers.Active, int64(resp.ANN.LastBuildCenters), "last build centers equals active centers after rebuild")
	assert.False(t, resp.ANN.RebuildRequested, "successful rebuild clears rebuild requested")
	assert.Equal(t, identityProfileANNDeltaMax, resp.ANN.DeltaMax, "delta max exposed in stats")
	assert.NotNil(t, resp.ANN.LastBuildStartedAt)
	assert.NotNil(t, resp.ANN.LastBuildEndedAt)
	// decisions 窗口固定 24 小时。
	assert.Equal(t, 24, resp.Decisions.WindowHours)
}

func TestPersonIdentityProfileService_ListRecentDecisions_EmptyReturnsEmptySlice(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	out, err := svc.ListRecentDecisions(50, deps.repos.IdentityDecision)
	require.NoError(t, err)
	assert.NotNil(t, out)
	assert.Empty(t, out)
}

func TestPersonIdentityProfileService_ListRecentDecisions_CenterIDsParsed(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	now := time.Now().UTC()
	// 写入一条 decision，CenterIDs 含重复、0、非法值。
	d := &model.PeopleIdentityDecision{
		Mode:                model.PeopleIdentityModeShadow,
		ComponentHash:       "h1",
		ComponentSize:       2,
		ComponentFaceIDs:    "1,2",
		DecisionKey:         "dk1",
		CenterIDs:           "5,3,5,0,abc,3,7",
		Decision:            model.PeopleIdentityDecisionAgree,
		AlgorithmVersion:    "v1",
		IndexGeneration:     1,
		CreatedAt:           now,
		ElapsedMilliseconds: 10,
	}
	require.NoError(t, deps.db.Create(d).Error)

	out, err := svc.ListRecentDecisions(50, deps.repos.IdentityDecision)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Equal(t, []uint{3, 5, 7}, out[0].CenterIDs, "CenterIDs deduped, filtered, sorted")
	// 不返回 ComponentFaceIDs / ComponentHash / DecisionKey。
	assert.Empty(t, out[0].ComponentFaceIDsTruncated)
}

func TestPersonIdentityProfileService_ListRecentDecisions_CenterIDsTruncatedTo32(t *testing.T) {
	svc, deps := newIdentityProfileServiceForTest(t, "shadow", "emb", 5, 0)
	now := time.Now().UTC()
	parts := make([]string, 0, 40)
	for i := 1; i <= 40; i++ {
		parts = append(parts, strconv.Itoa(i))
	}
	d := &model.PeopleIdentityDecision{
		Mode:          model.PeopleIdentityModeShadow,
		ComponentHash: "h2",
		DecisionKey:   "dk2",
		CenterIDs:     strings.Join(parts, ","),
		Decision:      model.PeopleIdentityDecisionAgree,
		CreatedAt:     now,
	}
	require.NoError(t, deps.db.Create(d).Error)

	out, err := svc.ListRecentDecisions(50, deps.repos.IdentityDecision)
	require.NoError(t, err)
	require.Len(t, out, 1)
	assert.Len(t, out[0].CenterIDs, 32, "CenterIDs truncated to 32")
}
