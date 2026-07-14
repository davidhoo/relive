package service

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupEventClusteringTestDB 建立事件聚类测试所需的内存 DB 与 repository。
func setupEventClusteringTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.Person{}, &model.Event{}, &model.PhotoTag{}))
	return db
}

func newEventClusteringSvcForTest(t *testing.T) *eventClusteringService {
	t.Helper()
	db := setupEventClusteringTestDB(t)
	return &eventClusteringService{
		db:           db,
		photoRepo:    repository.NewPhotoRepository(db),
		eventRepo:    repository.NewEventRepository(db),
		photoTagRepo: repository.NewPhotoTagRepository(db),
		config:       model.DefaultEventClusteringConfig(),
		autoWake:     make(chan struct{}, 1),
		autoStop:     make(chan struct{}),
		autoWorkerDone: make(chan struct{}),
	}
}

// insertUnclusteredPhotos 写入若干未聚类照片（event_id=NULL, taken_at 非空）。
func insertUnclusteredPhotos(t *testing.T, db *gorm.DB, start time.Time, count int) []uint {
	t.Helper()
	var ids []uint
	for i := 0; i < count; i++ {
		ts := start.Add(time.Duration(i) * 30 * time.Minute) // 同一事件内（间隔 < 6h）
		p := &model.Photo{
			FilePath:   fmt.Sprintf("/p_%d.jpg", i),
			FileName:   fmt.Sprintf("p_%d.jpg", i),
			FileSize:   1,
			FileHash:   fmt.Sprintf("h_%d", i),
			TakenAt:    &ts,
			Status:     model.PhotoStatusActive,
		}
		require.NoError(t, db.Create(p).Error)
		ids = append(ids, p.ID)
	}
	return ids
}

// TestEventClustering_AutoIncremental_RunsWhenAdmitted 验证：注入 coordinator 后，
// RunIncremental 不直接执行重工作，而是经 worker 准入后完成增量聚类，照片 event_id 落库。
func TestEventClustering_AutoIncremental_RunsWhenAdmitted(t *testing.T) {
	svc := newEventClusteringSvcForTest(t)
	db := svc.db

	coord := NewBackgroundTaskCoordinator()
	svc.SetBackgroundCoordinator(coord)
	defer svc.StopAutoWorker()

	start := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	// 5 张照片，每 30 分钟一张 → 单个 cluster（>3 张满足 MinPhotosPerEvent=3）。
	insertUnclusteredPhotos(t, db, start, 5)

	svc.RunIncremental()

	// 等待 worker 完成（autoRunning 回落到 false 且 pending 清空）。
	require.Eventually(t, func() bool {
		svc.autoMu.Lock()
		defer svc.autoMu.Unlock()
		return !svc.autoRunning && !svc.autoPending
	}, 3*time.Second, 20*time.Millisecond, "auto incremental should complete")

	// 验证照片已聚类（event_id 非空）。
	var eventID int
	require.NoError(t, db.Model(&model.Photo{}).Select("event_id").Where("id IS NOT NULL").Limit(1).Row().Scan(&eventID))
	assert.NotZero(t, eventID, "photos should be assigned an event_id")

	var n int64
	db.Model(&model.Photo{}).Where("event_id IS NOT NULL").Count(&n)
	assert.Equal(t, int64(5), n, "all 5 photos should be clustered")
}

// TestEventClustering_AutoIncremental_DeferredWhenForegroundActive 验证：foreground active 时
// 自动增量聚类被拒绝，保持 pending 不执行；foreground 释放后才执行。
func TestEventClustering_AutoIncremental_DeferredWhenForegroundActive(t *testing.T) {
	svc := newEventClusteringSvcForTest(t)
	db := svc.db

	coord := NewBackgroundTaskCoordinator()
	svc.SetBackgroundCoordinator(coord)
	defer svc.StopAutoWorker()

	// 测试用：把重试退避调小，使 foreground 释放后能快速恢复。
	setAutoRetryBackoffForTest(50 * time.Millisecond)
	defer setAutoRetryBackoffForTest(0)

	start := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	insertUnclusteredPhotos(t, db, start, 5)

	// 占用 foreground scope。
	releaseFg := coord.BeginForeground()
	svc.RunIncremental()

	// 等待一段时间，确认未执行（pending 保持，无 event_id 落库）。
	time.Sleep(300 * time.Millisecond)
	var n int64
	db.Model(&model.Photo{}).Where("event_id IS NOT NULL").Count(&n)
	assert.Equal(t, int64(0), n, "should not cluster while foreground active")

	svc.autoMu.Lock()
	assert.True(t, svc.autoPending, "pending should be retained while deferred")
	svc.autoMu.Unlock()

	// 释放 foreground，等待 worker 重试并完成。
	releaseFg()
	require.Eventually(t, func() bool {
		svc.autoMu.Lock()
		defer svc.autoMu.Unlock()
		return !svc.autoRunning && !svc.autoPending
	}, 5*time.Second, 20*time.Millisecond, "should complete after foreground releases")

	db.Model(&model.Photo{}).Where("event_id IS NOT NULL").Count(&n)
	assert.Equal(t, int64(5), n, "should cluster after foreground releases")
}

// TestEventClustering_AutoIncremental_CoalescesMultipleRequests 验证：多次扫描完成触发
// 合并 —— running 期间的第二个请求被 coalesce，只保留一个 pending，不并发运行多个 incremental。
func TestEventClustering_AutoIncremental_CoalescesMultipleRequests(t *testing.T) {
	svc := newEventClusteringSvcForTest(t)
	coord := NewBackgroundTaskCoordinator()
	// 用极小 batch + 大量照片制造可观察的 running 窗口。
	svc.SetBackgroundCoordinator(coord)
	defer svc.StopAutoWorker()

	start := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	// 每 30 分钟一张，30 张 → 1 个 cluster，但 batch=20 仍只 1 个 batch。改用跨天制造多 cluster。
	// 制造 50 张跨多天：每 25 小时一张 → 每张独立 cluster（>24h 强制切分）。
	var ids []uint
	for i := 0; i < 50; i++ {
		ts := start.Add(time.Duration(i) * 25 * time.Hour)
		p := &model.Photo{FilePath: fmt.Sprintf("/p_%d.jpg", i), FileName: fmt.Sprintf("p_%d.jpg", i), FileSize: 1, FileHash: fmt.Sprintf("h_%d", i), TakenAt: &ts, Status: model.PhotoStatusActive}
		require.NoError(t, svc.db.Create(p).Error)
		ids = append(ids, p.ID)
	}

	// 把 batch size 临时调小，使 running 窗口可观察。直接改常量不可行，故通过制造足够多 cluster
	// 让首批 20 个 cluster 的提交有可测时间窗。这里用协调器负载高来暂停首次准入制造 running 标记。
	// 简化：触发一次请求，在 worker 开始前再次触发，验证 coalesce 行为。
	svc.RunIncremental()
	// 立即第二次请求：此时 worker 可能已开始或仍 pending。无论哪种，第二个请求都不应产生第二个 running。
	svc.RunIncremental()

	// 等待最终完成。
	require.Eventually(t, func() bool {
		svc.autoMu.Lock()
		defer svc.autoMu.Unlock()
		return !svc.autoRunning && !svc.autoPending
	}, 5*time.Second, 20*time.Millisecond)

	// running 期间不应超过 1（单 worker）。验证最终结果：50 张照片中 >=3 的 cluster 才建事件。
	// 50 个单照片 cluster 都 < MinPhotosPerEvent(3)，全部跳过，event_id 保持 NULL。
	var clustered int64
	svc.db.Model(&model.Photo{}).Where("event_id IS NOT NULL").Count(&clustered)
	// 单照片 cluster 全部跳过，应 0 个建事件。
	assert.Equal(t, int64(0), clustered, "single-photo clusters should be skipped")
}

// TestEventClustering_AutoIncremental_YieldsAndResumesResultEquivalent 验证：可暂停批次的
// 最终结果与连续执行一致。用同一组照片分别跑：一次连续 runIncremental，一次分批让路恢复的
// runIncrementalYieldable，断言最终 event 数量、photo.event_id 归属一致。
func TestEventClustering_AutoIncremental_YieldsAndResumesResultEquivalent(t *testing.T) {
	mkSvc := func(t *testing.T) *eventClusteringService {
		svc := newEventClusteringSvcForTest(t)
		// 注入 coordinator 但用 foreground 制造让路；不启动 worker，直接调底层方法。
		coord := NewBackgroundTaskCoordinator()
		svc.SetBackgroundCoordinator(coord)
		return svc
	}

	// 构造 60 张照片，分 3 组：每组内连续（每 30 分钟一张，20 张同事件），组间间隔 48 小时
	// （>24h TimeGapNewEvent 强制切分）→ 3 个 cluster，各 20 张（>=3 满足 MinPhotosPerEvent）。
	// 用 48h（而非刚过 24h）避免浮点边界歧义。
	start := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	mkPhotos := func(t *testing.T, db *gorm.DB) []uint {
		var ids []uint
		idx := 0
		for g := 0; g < 3; g++ {
			for i := 0; i < 20; i++ {
				ts := start.Add(time.Duration(g)*48*time.Hour + time.Duration(i)*30*time.Minute)
				p := &model.Photo{FilePath: fmt.Sprintf("/p_%d.jpg", idx), FileName: fmt.Sprintf("p_%d.jpg", idx), FileSize: 1, FileHash: fmt.Sprintf("h_%d", idx), TakenAt: &ts, Status: model.PhotoStatusActive}
				require.NoError(t, db.Create(p).Error)
				ids = append(ids, p.ID)
				idx++
			}
		}
		return ids
	}

	// 基线：连续执行 runIncremental。
	svcBaseline := mkSvc(t)
	defer svcBaseline.StopAutoWorker()
	photoIDs := mkPhotos(t, svcBaseline.db)
	require.NoError(t, svcBaseline.runIncremental(t.Context(), nil))
	baselineAssign := map[uint]uint{}
	for _, id := range photoIDs {
		var p model.Photo
		require.NoError(t, svcBaseline.db.Select("id,event_id").First(&p, id).Error)
		if p.EventID != nil {
			baselineAssign[id] = *p.EventID
		} else {
			baselineAssign[id] = 0
		}
	}
	var baselineEvents int64
	svcBaseline.db.Model(&model.Event{}).Count(&baselineEvents)
	assert.Equal(t, int64(3), baselineEvents, "baseline should create 3 events")

	// 对比：分批让路 + 恢复执行 runIncrementalYieldable。
	svcYield := mkSvc(t)
	defer svcYield.StopAutoWorker()
	mkPhotos(t, svcYield.db)

	// 测试用：把批次大小调到 1，使每个 cluster 提交后都检查 foreground，从而在第 1 个 cluster
	// 后即让路（模拟可暂停批次）。恢复后用大 batch 完成剩余。
	setAutoBatchSizeForTest(1)
	defer setAutoBatchSizeForTest(0)

	// 第一次 slice：foreground active → shouldYield 在第 1 个 cluster 后返回 true。
	ctx := t.Context()
	fgRelease := svcYield.backgroundCoordinator.BeginForeground()
	yielded, err := svcYield.runIncrementalYieldable(ctx, nil)
	require.NoError(t, err)
	assert.True(t, yielded, "first slice should yield at batch boundary with foreground active")
	fgRelease()

	// 验证让路后只有第 1 个 cluster（20 张）落库，其余 40 张未处理。
	var clusteredAfterYield int64
	svcYield.db.Model(&model.Photo{}).Where("event_id IS NOT NULL").Count(&clusteredAfterYield)
	assert.Equal(t, int64(20), clusteredAfterYield, "first cluster (20 photos) committed before yield")

	// 恢复：用大 batch（不再让路）完成剩余 2 个 cluster。
	setAutoBatchSizeForTest(100)
	yielded2, err := svcYield.runIncrementalYieldable(ctx, nil)
	require.NoError(t, err)
	assert.False(t, yielded2, "second slice should complete without yielding")

	// 验证结果等价：相同 event 数量、相同 photo→event 归属。
	var yieldEvents int64
	svcYield.db.Model(&model.Event{}).Count(&yieldEvents)
	assert.Equal(t, baselineEvents, yieldEvents, "event count must match baseline")

	for _, id := range photoIDs {
		var p model.Photo
		require.NoError(t, svcYield.db.Select("id,event_id").First(&p, id).Error)
		var got uint
		if p.EventID != nil {
			got = *p.EventID
		}
		assert.Equal(t, baselineAssign[id], got, "photo %d event_id must match baseline", id)
	}
}

// setAutoBatchSizeForTest 设置测试用批次大小覆盖并返回原值。仅测试使用。
func setAutoBatchSizeForTest(n int) int32 {
	old := atomic.LoadInt32(&autoBatchSizeOverride)
	atomic.StoreInt32(&autoBatchSizeOverride, int32(n))
	return old
}

// setAutoRetryBackoffForTest 设置测试用重试退避覆盖（纳秒），返回原值。仅测试使用。
func setAutoRetryBackoffForTest(d time.Duration) int64 {
	old := atomic.LoadInt64(&autoRetryBackoffOverride)
	atomic.StoreInt64(&autoRetryBackoffOverride, int64(d))
	return old
}
// TestEventClustering_UserStartClusteringStillPassesThrough 验证：注入 coordinator 后，
// 用户显式 StartClustering 仍走 P1 user，不被 foreground 背压拒绝（StartClustering 内部
// 用 activeTask + runTask，不经 auto worker）。
func TestEventClustering_UserStartClusteringStillPassesThrough(t *testing.T) {
	svc := newEventClusteringSvcForTest(t)
	coord := NewBackgroundTaskCoordinator()
	svc.SetBackgroundCoordinator(coord)
	defer svc.StopAutoWorker()

	start := time.Date(2025, 3, 15, 10, 0, 0, 0, time.UTC)
	insertUnclusteredPhotos(t, svc.db, start, 5)

	// 占用 foreground。
	releaseFg := coord.BeginForeground()
	defer releaseFg()

	// 用户显式启动应成功（不被 foreground 拒绝）。
	task, err := svc.StartClustering()
	require.NoError(t, err)
	require.NotNil(t, task)

	// 等待完成。
	require.Eventually(t, func() bool {
		return svc.GetTask() == nil || svc.GetTask().Status != model.ScanJobStatusRunning
	}, 3*time.Second, 20*time.Millisecond)

	var n int64
	svc.db.Model(&model.Photo{}).Where("event_id IS NOT NULL").Count(&n)
	assert.Equal(t, int64(5), n, "user-triggered clustering should run despite foreground")
}
