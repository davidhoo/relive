package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupBackfillTestDB 建库 + 安装 person_photos 表/索引/trigger。
func setupBackfillTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Photo{}, &model.Person{}, &model.Face{}, &model.AppConfig{}))
	require.NoError(t, database.AutoMigrate(db))
	return db
}

// newBackfill 构造一个不依赖 coordinator 的 PersonPhotosBackfill（coordinator=nil 放行），
// 并把退避时间压到极短，避免测试真实等待 10 秒。
func newBackfill(db *gorm.DB) *PersonPhotosBackfill {
	ppRepo := repository.NewPersonPhotoRepository(db)
	b := NewPersonPhotosBackfill(db, ppRepo, nil)
	// 测试专用：极短退避，覆盖生产默认 10s。
	b.backoff = 1 * time.Millisecond
	// 缩短 pause 让测试快。
	b.pauseBetween = 0
	return b
}

// makeFacePhoto 建一张 active 照片 + 一张已聚类到 person 的人脸。
func makeFacePhoto(t *testing.T, db *gorm.DB, person *model.Person, name string) *model.Photo {
	t.Helper()
	t1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	p := &model.Photo{FilePath: "/" + name, FileName: name, FileSize: 1, FileHash: "h_" + name, TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(p).Error)
	require.NoError(t, db.Create(&model.Face{PhotoID: p.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	return p
}

// TestPersonPhotosBackfill_DefaultBackoffIs10s 验证生产默认退避仍为 10 秒，
// 且 backoff<=0 时回退默认值，避免 tight loop。
func TestPersonPhotosBackfill_DefaultBackoffIs10s(t *testing.T) {
	db := setupBackfillTestDB(t)
	b := NewPersonPhotosBackfill(db, repository.NewPersonPhotoRepository(db), nil)
	assert.Equal(t, 10*time.Second, b.backoff, "生产默认退避应为 10 秒")
	assert.Equal(t, 10*time.Second, b.effectiveBackoff())

	// <=0 回退默认。
	b.backoff = 0
	assert.Equal(t, 10*time.Second, b.effectiveBackoff())
	b.backoff = -5 * time.Second
	assert.Equal(t, 10*time.Second, b.effectiveBackoff())

	// 正向覆盖生效。
	b.backoff = 1 * time.Millisecond
	assert.Equal(t, 1*time.Millisecond, b.effectiveBackoff())
}

// TestPersonPhotosBackfill_RepairOnConsistencyFailure 验证核心修复：
// 回填到末尾后一致性失败时，回填流程会触发 RepairBatch 修复并最终 ready，
// 而不是像旧版那样只 sleep 重试、永远卡在 backfilling。
func TestPersonPhotosBackfill_RepairOnConsistencyFailure(t *testing.T) {
	db := setupBackfillTestDB(t)
	person := &model.Person{Name: "BF", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	p := makeFacePhoto(t, db, person, "bf1.jpg")

	// 制造不一致：删除 person_photos（missing），插入 extra。
	require.NoError(t, db.Exec("DELETE FROM person_photos WHERE person_id = ? AND photo_id = ?", person.ID, p.ID).Error)
	require.NoError(t, db.Exec("INSERT INTO person_photos(person_id, photo_id, taken_at) VALUES(?, ?, ?)", person.ID, 999999, &time.Time{}).Error)

	b := newBackfill(db)

	// 直接调用 finalizeAfterBackfill：模拟回填已到末尾。
	// 此时一致性失败（missing + extra），应进入修复 → 校验 → ready。
	done, err := b.finalizeAfterBackfill(0)
	require.NoError(t, err)
	assert.True(t, done, "finalize should complete (ready) after repair")

	ready, err := b.ppRepo.MigrationReady(b.db)
	require.NoError(t, err)
	assert.True(t, ready, "status should be ready after repair")

	// 校验报告 clean。
	rep, err := b.ppRepo.RunConsistencyCheck(b.db)
	require.NoError(t, err)
	assert.True(t, rep.IsClean(), "consistency clean after finalize: %+v", rep)
}

// TestPersonPhotosBackfill_StaysRepairingWhenStillDirty 验证 maxRounds 用尽时不会误标 ready：
// 25 条 missing、batchSize=1，单回合最多 20 轮只能修 20 条，剩余 5 条。
// 期望 done=false、v2 保持 repairing、v1 不被标 ready、一致性报告仍存在 missing。
func TestPersonPhotosBackfill_StaysRepairingWhenStillDirty(t *testing.T) {
	db := setupBackfillTestDB(t)
	person := &model.Person{Name: "Dirty", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	// 25 张照片 + 人脸，全部清空 person_photos 制造 25 条 missing。
	for i := 0; i < 25; i++ {
		makeFacePhoto(t, db, person, "d"+itoa(i)+".jpg")
	}
	require.NoError(t, db.Exec("DELETE FROM person_photos WHERE person_id = ?", person.ID).Error)

	b := newBackfill(db)
	// batchSize=1：每轮 RepairBatch 只补 1 条 missing；maxRounds=20 → 单回合最多修 20 条。
	b.batchSize = 1

	// 模拟回填扫描到末尾，进入修复收尾。
	done, err := b.finalizeAfterBackfill(0)
	require.NoError(t, err)
	assert.False(t, done, "maxRounds 用尽仍有剩余，应返回 done=false")

	// v2 状态保持 repairing。
	statusV2, _, err := b.ppRepo.GetMigrationStatusV2(b.db)
	require.NoError(t, err)
	assert.Equal(t, "repairing", statusV2, "v2 应保持 repairing，不得误标 ready")

	// v1 不得被标记 ready。
	ready, err := b.ppRepo.MigrationReady(b.db)
	require.NoError(t, err)
	assert.False(t, ready, "v1 不得在仍不一致时被标 ready")

	// 一致性报告仍存在 missing。
	rep, err := b.ppRepo.RunConsistencyCheck(b.db)
	require.NoError(t, err)
	assert.Greater(t, rep.MissingAssociations, int64(0), "仍应存在未修复的 missing")
}

// TestPersonPhotosBackfill_MaxRoundsExhaustedThenResumes 验证后续回合能从 repairing 继续：
// 第一回合耗尽 maxRounds 后仍有剩余，第二回合（再次收尾）能修复剩余数据并最终 ready。
func TestPersonPhotosBackfill_MaxRoundsExhaustedThenResumes(t *testing.T) {
	db := setupBackfillTestDB(t)
	person := &model.Person{Name: "Resume", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	for i := 0; i < 25; i++ {
		makeFacePhoto(t, db, person, "r"+itoa(i)+".jpg")
	}
	require.NoError(t, db.Exec("DELETE FROM person_photos WHERE person_id = ?", person.ID).Error)

	b := newBackfill(db)
	b.batchSize = 1 // 单回合最多 20 轮，修 20 条，剩 5 条。

	// 第一回合：耗尽 maxRounds，done=false，保持 repairing。
	done1, err := b.finalizeAfterBackfill(0)
	require.NoError(t, err)
	assert.False(t, done1, "第一回合应未完成")

	// 第二回合：从 repairing 继续，修复剩余 5 条，最终 ready。
	done2, err := b.finalizeAfterBackfill(0)
	require.NoError(t, err)
	assert.True(t, done2, "第二回合应完成修复并 ready")

	ready, err := b.ppRepo.MigrationReady(b.db)
	require.NoError(t, err)
	assert.True(t, ready, "最终应 ready")

	rep, err := b.ppRepo.RunConsistencyCheck(b.db)
	require.NoError(t, err)
	assert.True(t, rep.IsClean(), "最终一致性应 clean: %+v", rep)
}

// TestPersonPhotosBackfill_PersonCountMismatchBoundary 验证人物聚合数量不一致的检测与修复：
// 构造 person_photos 记录数与有效 face DISTINCT photo 数不匹配，RunConsistencyCheck 能检出
// PersonCountMismatch，RepairBatch 后重新校验为 clean。
func TestPersonPhotosBackfill_PersonCountMismatchBoundary(t *testing.T) {
	db := setupBackfillTestDB(t)
	person := &model.Person{Name: "Count", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	// 两张照片都有有效 face 指向 person（trigger 自动写 2 条 person_photos）。
	p1 := makeFacePhoto(t, db, person, "c1.jpg")
	makeFacePhoto(t, db, person, "c2.jpg")

	// 删除 p1 对应的 person_photos 行：face 侧 DISTINCT photo=2，person_photos 记录=1，
	// 按人物聚合数量不等 → 触发 PersonCountMismatch（同时 MissingAssociations=1）。
	require.NoError(t, db.Exec("DELETE FROM person_photos WHERE person_id = ? AND photo_id = ?", person.ID, p1.ID).Error)

	// 检测：应能检出 PersonCountMismatch（聚合数量不等）。
	rep, err := bRepo(db).RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.False(t, rep.IsClean(), "应检测到不一致: %+v", rep)
	assert.Greater(t, rep.PersonCountMismatch, int64(0), "应检出 PersonCountMismatch: %+v", rep)

	// 修复后重新校验为 clean。
	delta, err := bRepo(db).RepairBatch(db, 500)
	require.NoError(t, err)
	assert.Greater(t, delta.MissingInserted, int64(0), "修复应补齐 missing")

	rep2, err := bRepo(db).RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.True(t, rep2.IsClean(), "修复后应 clean: %+v", rep2)
}

// bRepo 构造裸仓库实例，用于直接调用一致性校验/修复。
func bRepo(db *gorm.DB) repository.PersonPhotoRepository {
	return repository.NewPersonPhotoRepository(db)
}

// itoa 轻量整数转字符串，避免引入 strconv 仅此一处。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
