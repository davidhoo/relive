package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupPersonPhotosTestDB 建库 + 安装 person_photos 表/索引/trigger。
func setupPersonPhotosTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupTestDB(t)
	// setupTestDB 未迁移 PersonPhoto，补上。
	require.NoError(t, db.AutoMigrate(&model.PersonPhoto{}))
	require.NoError(t, database.AutoMigrate(db))
	return db
}

func seedPersonPhotoFixture(t *testing.T, db *gorm.DB) (personID uint, photoIDs []uint) {
	t.Helper()
	person := &model.Person{Name: "PP", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	personID = person.ID

	// 3 张照片，taken_at 递减
	for i := 0; i < 3; i++ {
		tt := time.Date(2025, 1, 1+i, 0, 0, 0, 0, time.UTC)
		// i=0 → 2025-01-01, i=1 → 01-02, i=2 → 01-03；用倒序便于断言 DESC
		_ = tt
	}
	// 用明确的 taken_at，确保排序可断言
	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	p1 := &model.Photo{FilePath: "/p1.jpg", FileName: "p1.jpg", FileSize: 1, FileHash: "h1", TakenAt: &t1, Status: model.PhotoStatusActive}
	p2 := &model.Photo{FilePath: "/p2.jpg", FileName: "p2.jpg", FileSize: 1, FileHash: "h2", TakenAt: &t2, Status: model.PhotoStatusActive}
	p3 := &model.Photo{FilePath: "/p3.jpg", FileName: "p3.jpg", FileSize: 1, FileHash: "h3", TakenAt: &t3, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(p1).Error)
	require.NoError(t, db.Create(p2).Error)
	require.NoError(t, db.Create(p3).Error)
	photoIDs = []uint{p1.ID, p2.ID, p3.ID}

	// 给每张照片各关联一张有效 face
	for _, pid := range photoIDs {
		require.NoError(t, db.Create(&model.Face{PhotoID: pid, PersonID: &personID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	}
	return personID, photoIDs
}

// TestPersonPhotos_TriggerInsertExcludeDelete 验证 trigger 在 face insert/exclude/delete 时维护派生表。
func TestPersonPhotos_TriggerInsertExcludeDelete(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, photoIDs := seedPersonPhotoFixture(t, db)
	ppRepo := NewPersonPhotoRepository(db)

	var cnt int64
	require.NoError(t, db.Table("person_photos").Where("person_id = ?", personID).Count(&cnt).Error)
	assert.Equal(t, int64(3), cnt, "3 faces → 3 person_photos rows via trigger")

	// 排除其中一张 face → 该 (person,photo) 应被删除
	var faceOnP1 model.Face
	require.NoError(t, db.Where("photo_id = ?", photoIDs[0]).First(&faceOnP1).Error)
	require.NoError(t, db.Model(&model.Face{}).Where("id = ?", faceOnP1.ID).Update("cluster_status", model.FaceClusterStatusExcluded).Error)
	require.NoError(t, db.Table("person_photos").Where("person_id = ?", personID).Count(&cnt).Error)
	assert.Equal(t, int64(2), cnt, "excluding the only face on p1 removes its person_photos row")

	// 恢复 → 应重新插入
	require.NoError(t, db.Model(&model.Face{}).Where("id = ?", faceOnP1.ID).Update("cluster_status", model.FaceClusterStatusAssigned).Error)
	require.NoError(t, db.Table("person_photos").Where("person_id = ?", personID).Count(&cnt).Error)
	assert.Equal(t, int64(3), cnt, "restoring face re-inserts person_photos row")
	_ = ppRepo
}

// TestPersonPhotos_MultiFacesSamePhotoDedup 验证同人物同照片多张脸只产生一条派生记录，
// 且删除其中一张不会误删仍有效的关联。
func TestPersonPhotos_MultiFacesSamePhotoDedup(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "M", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)
	tt := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	photo := &model.Photo{FilePath: "/m.jpg", FileName: "m.jpg", FileSize: 1, FileHash: "hm", TakenAt: &tt, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(photo).Error)

	// 同照片 2 张脸，同一人物
	f1 := &model.Face{PhotoID: photo.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}
	f2 := &model.Face{PhotoID: photo.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.8}
	require.NoError(t, db.Create(f1).Error)
	require.NoError(t, db.Create(f2).Error)

	var cnt int64
	require.NoError(t, db.Table("person_photos").Where("person_id = ? AND photo_id = ?", person.ID, photo.ID).Count(&cnt).Error)
	assert.Equal(t, int64(1), cnt, "two faces on same (person,photo) → one deduped row")

	// 删除 f1：f2 仍有效，关联不应被误删
	require.NoError(t, db.Delete(f1).Error)
	require.NoError(t, db.Table("person_photos").Where("person_id = ? AND photo_id = ?", person.ID, photo.ID).Count(&cnt).Error)
	assert.Equal(t, int64(1), cnt, "deleting one of two faces keeps the association")

	// 删除 f2：无有效 face，关联应被删除
	require.NoError(t, db.Delete(f2).Error)
	require.NoError(t, db.Table("person_photos").Where("person_id = ? AND photo_id = ?", person.ID, photo.ID).Count(&cnt).Error)
	assert.Equal(t, int64(0), cnt, "deleting last face removes the association")
}

// TestPersonPhotos_PhotoTakenAtUpdateSyncsDerived 验证 photo.taken_at 更新同步到派生表。
func TestPersonPhotos_PhotoTakenAtUpdateSyncsDerived(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, photoIDs := seedPersonPhotoFixture(t, db)

	var pp model.PersonPhoto
	require.NoError(t, db.Where("person_id = ? AND photo_id = ?", personID, photoIDs[0]).First(&pp).Error)
	require.NotNil(t, pp.TakenAt)

	newT := time.Date(2030, 12, 31, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.Model(&model.Photo{}).Where("id = ?", photoIDs[0]).Update("taken_at", newT).Error)

	var pp2 model.PersonPhoto
	require.NoError(t, db.Where("person_id = ? AND photo_id = ?", personID, photoIDs[0]).First(&pp2).Error)
	require.NotNil(t, pp2.TakenAt)
	assert.True(t, pp2.TakenAt.Equal(newT), "taken_at update propagates to person_photos")
}

// TestPersonPhotos_PhotoDeleteCleansDerived 验证删除照片清理派生记录。
func TestPersonPhotos_PhotoDeleteCleansDerived(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, photoIDs := seedPersonPhotoFixture(t, db)

	require.NoError(t, db.Unscoped().Delete(&model.Photo{}, photoIDs[0]).Error)
	var cnt int64
	require.NoError(t, db.Table("person_photos").Where("person_id = ? AND photo_id = ?", personID, photoIDs[0]).Count(&cnt).Error)
	assert.Equal(t, int64(0), cnt, "deleting photo cleans its person_photos row")
}

// TestPersonPhotos_BackfillAndConsistency 验证回填 + 一致性校验 + ready 切换。
func TestPersonPhotos_BackfillAndConsistency(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, photoIDs := seedPersonPhotoFixture(t, db)

	// 清空 trigger 已写入的数据，模拟历史库未回填
	require.NoError(t, db.Exec("DELETE FROM person_photos").Error)
	// 删除触发器临时禁用? 不需要——重新插入由回填走 ON CONFLICT，trigger 对回填 INSERT 也会触发但 ON CONFLICT 安全。

	ppRepo := NewPersonPhotoRepository(db)
	status, _, err := ppRepo.GetMigrationStatus(db)
	require.NoError(t, err)
	assert.Equal(t, "", status)

	// 标记 backfilling
	require.NoError(t, ppRepo.SetMigrationStatus(db, "backfilling", 0))

	// 回填一批（batchSize 足够大覆盖全部）
	last, inserted, err := ppRepo.BackfillBatch(db, 0, 500)
	require.NoError(t, err)
	assert.Greater(t, inserted, 0)
	// 再回填一次应无新增
	last2, inserted2, err := ppRepo.BackfillBatch(db, last, 500)
	require.NoError(t, err)
	assert.Equal(t, 0, inserted2)

	var cnt int64
	require.NoError(t, db.Table("person_photos").Where("person_id = ?", personID).Count(&cnt).Error)
	assert.Equal(t, int64(len(photoIDs)), cnt, "backfill restored all 3 associations")

	// 一致性校验通过（结构化报告全部为 0）
	rep, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.True(t, rep.IsClean(), "consistency check passes after backfill: %+v", rep)

	// 标记 ready
	require.NoError(t, ppRepo.SetMigrationStatus(db, "ready", last2))
	ready, err := ppRepo.MigrationReady(db)
	require.NoError(t, err)
	assert.True(t, ready)
}

// TestPersonPhotos_FaceMovePersonIDChange 验证 face 的 person_id 变更（人物移动）时，
// 旧人物的 person_photos 减少一条、新人物增加一条。Face UPDATE trigger 处理 person_id 变更。
func TestPersonPhotos_FaceMovePersonIDChange(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personA := &model.Person{Name: "A", Category: model.PersonCategoryFamily}
	personB := &model.Person{Name: "B", Category: model.PersonCategoryFriend}
	require.NoError(t, db.Create(personA).Error)
	require.NoError(t, db.Create(personB).Error)

	t1 := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	photo := &model.Photo{FilePath: "/move.jpg", FileName: "move.jpg", FileSize: 1, FileHash: "hm", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(photo).Error)

	// face 属于 A
	face := &model.Face{PhotoID: photo.ID, PersonID: &personA.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}
	require.NoError(t, db.Create(face).Error)

	var cntA, cntB int64
	require.NoError(t, db.Table("person_photos").Where("person_id = ?", personA.ID).Count(&cntA).Error)
	require.NoError(t, db.Table("person_photos").Where("person_id = ?", personB.ID).Count(&cntB).Error)
	assert.Equal(t, int64(1), cntA, "A has the association")
	assert.Equal(t, int64(0), cntB, "B has no association yet")

	// 移动 face 到 B（更新 person_id）
	require.NoError(t, db.Model(&model.Face{}).Where("id = ?", face.ID).Update("person_id", personB.ID).Error)

	require.NoError(t, db.Table("person_photos").Where("person_id = ?", personA.ID).Count(&cntA).Error)
	require.NoError(t, db.Table("person_photos").Where("person_id = ?", personB.ID).Count(&cntB).Error)
	assert.Equal(t, int64(0), cntA, "A loses the association after move")
	assert.Equal(t, int64(1), cntB, "B gains the association after move")
}

// TestPersonPhotos_BackfillInterruptResume 验证回填中断后从 lastFaceID 继续。
// 模拟：回填一批记录进度 → 再回填一批从进度继续 → 最终完成。
func TestPersonPhotos_BackfillInterruptResume(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "R", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	// 6 张照片 + 6 张有效 face，batchSize=2 → 需要 3 批
	photos := make([]*model.Photo, 0, 6)
	for i := 0; i < 6; i++ {
		tt := time.Date(2025, 1, 1+i, 0, 0, 0, 0, time.UTC)
		p := &model.Photo{FilePath: "/r" + string(rune('a'+i)) + ".jpg", FileName: "r.jpg", FileSize: 1, FileHash: "hr" + string(rune('a'+i)), TakenAt: &tt, Status: model.PhotoStatusActive}
		require.NoError(t, db.Create(p).Error)
		photos = append(photos, p)
		require.NoError(t, db.Create(&model.Face{PhotoID: p.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	}

	// 清空 trigger 写入，模拟历史库
	require.NoError(t, db.Exec("DELETE FROM person_photos").Error)

	ppRepo := NewPersonPhotoRepository(db)
	require.NoError(t, ppRepo.SetMigrationStatus(db, "backfilling", 0))

	// 第 1 批（batchSize=2）
	last1, ins1, err := ppRepo.BackfillBatch(db, 0, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, ins1)
	require.NoError(t, ppRepo.SetMigrationStatus(db, "backfilling", last1))

	// 模拟"重启"：重新读取进度
	status, lastResume, err := ppRepo.GetMigrationStatus(db)
	require.NoError(t, err)
	assert.Equal(t, "backfilling", status)
	assert.Equal(t, last1, lastResume, "resumed lastFaceID matches saved progress")

	// 第 2 批从 lastResume 继续
	last2, ins2, err := ppRepo.BackfillBatch(db, lastResume, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, ins2)
	require.NoError(t, ppRepo.SetMigrationStatus(db, "backfilling", last2))

	// 第 3 批（剩余 2 条）
	last3, ins3, err := ppRepo.BackfillBatch(db, last2, 2)
	require.NoError(t, err)
	assert.Equal(t, 2, ins3)

	// 第 4 批应无新增 → done
	_, ins4, err := ppRepo.BackfillBatch(db, last3, 2)
	require.NoError(t, err)
	assert.Equal(t, 0, ins4)

	var cnt int64
	require.NoError(t, db.Table("person_photos").Where("person_id = ?", person.ID).Count(&cnt).Error)
	assert.Equal(t, int64(6), cnt, "all 6 associations restored after resume")

	// 一致性校验通过
	rep, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.True(t, rep.IsClean(), "consistency clean after resume: %+v", rep)
}

// TestPersonPhotos_CursorQueryOrderBy 验证 cursor 查询按 taken_at DESC, photo_id DESC，无重复无遗漏。
func TestPersonPhotos_CursorQueryOrderBy(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, _ := seedPersonPhotoFixture(t, db)
	ppRepo := NewPersonPhotoRepository(db)

	// 首页取 2 条
	ids, hasMore, next, err := ppRepo.ListPhotoIDsByPersonCursor(personID, nil, 2)
	require.NoError(t, err)
	assert.Len(t, ids, 2)
	assert.True(t, hasMore)
	require.NotNil(t, next)

	// 验证 DESC：第一页第一条 taken_at 应最大。通过回表比对。
	var first, second model.Photo
	require.NoError(t, db.First(&first, ids[0]).Error)
	require.NoError(t, db.First(&second, ids[1]).Error)
	assert.True(t, first.TakenAt.After(*second.TakenAt) || first.TakenAt.Equal(*second.TakenAt), "first page ordered DESC")

	// 第二页
	ids2, hasMore2, _, err := ppRepo.ListPhotoIDsByPersonCursor(personID, next, 2)
	require.NoError(t, err)
	assert.Len(t, ids2, 1)
	assert.False(t, hasMore2)

	// 无重复
	all := append([]uint{}, ids...)
	all = append(all, ids2...)
	seen := map[uint]bool{}
	for _, id := range all {
		assert.False(t, seen[id], "no duplicate across pages")
		seen[id] = true
	}
	assert.Len(t, all, 3, "all 3 photos covered, no gap")
}

// TestPersonPhotos_CursorNullTakenAt 验证 NULL taken_at 排在非空之后。
func TestPersonPhotos_CursorNullTakenAt(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "N", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	// 1 张有 taken_at，1 张 NULL
	t1 := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	p1 := &model.Photo{FilePath: "/n1.jpg", FileName: "n1.jpg", FileSize: 1, FileHash: "hn1", TakenAt: &t1, Status: model.PhotoStatusActive}
	p2 := &model.Photo{FilePath: "/n2.jpg", FileName: "n2.jpg", FileSize: 1, FileHash: "hn2", TakenAt: nil, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(p1).Error)
	require.NoError(t, db.Create(p2).Error)
	require.NoError(t, db.Create(&model.Face{PhotoID: p1.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	require.NoError(t, db.Create(&model.Face{PhotoID: p2.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)

	ppRepo := NewPersonPhotoRepository(db)
	ids, _, _, err := ppRepo.ListPhotoIDsByPersonCursor(person.ID, nil, 10)
	require.NoError(t, err)
	require.Len(t, ids, 2)
	// p1 (有 taken_at) 应在前
	assert.Equal(t, p1.ID, ids[0], "non-NULL taken_at sorts before NULL in DESC")
	assert.Equal(t, p2.ID, ids[1])
}

// TestPersonPhotos_CursorEmptyResult 验证空人物返回空且 hasMore=false。
func TestPersonPhotos_CursorEmptyResult(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	ppRepo := NewPersonPhotoRepository(db)
	ids, hasMore, next, err := ppRepo.ListPhotoIDsByPersonCursor(999999, nil, 10)
	require.NoError(t, err)
	assert.Empty(t, ids)
	assert.False(t, hasMore)
	assert.Nil(t, next)
}

// TestPersonPhotos_ExplainUsesIndex 验证 cursor 查询走 idx_person_photos_cursor，
// 不出现 USE TEMP B-TREE FOR DISTINCT / ORDER BY。
func TestPersonPhotos_ExplainUsesIndex(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, _ := seedPersonPhotoFixture(t, db)

	var plans []struct {
		Detail string `gorm:"column:detail"`
	}
	// 首页查询（无 cursor）
	require.NoError(t, db.Raw(`EXPLAIN QUERY PLAN
		SELECT photo_id, taken_at FROM person_photos
		WHERE person_id = ? ORDER BY taken_at DESC, photo_id DESC LIMIT 31`, personID).Scan(&plans).Error)
	joined := ""
	for _, p := range plans {
		joined += p.Detail + " | "
	}
	assert.Contains(t, joined, "idx_person_photos_cursor", "must use idx_person_photos_cursor")
	assert.NotContains(t, joined, "USE TEMP B-TREE", "no temp B-Tree for DISTINCT/ORDER BY")
}

// TestPersonPhotos_CursorThreePagesNoRepeatNoGap 用真实 SQLite（UTC taken_at）连续读取三页，
// 断言每页 ID 无重复、每页 next_cursor 不同、且三页并集覆盖全部关联无遗漏。
// 这是线上“cursor 不推进、重复请求同一页”的回归保护。
func TestPersonPhotos_CursorThreePagesNoRepeatNoGap(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "ThreePages", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	// 8 张照片，taken_at 严格递减（便于断言 DESC 无歧义），各关联一张有效 face。
	const n = 8
	ids := make([]uint, 0, n)
	for i := 0; i < n; i++ {
		tt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -i) // 每天 -1 天
		p := &model.Photo{
			FilePath: fmt.Sprintf("/tp_%d.jpg", i), FileName: fmt.Sprintf("tp_%d.jpg", i),
			FileSize: 1, FileHash: fmt.Sprintf("htp_%d", i), TakenAt: &tt, Status: model.PhotoStatusActive,
		}
		require.NoError(t, db.Create(p).Error)
		ids = append(ids, p.ID)
		require.NoError(t, db.Create(&model.Face{
			PhotoID: p.ID, PersonID: &person.ID,
			ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9,
		}).Error)
	}

	ppRepo := NewPersonPhotoRepository(db)
	seen := map[uint]bool{}
	var cursor *PersonPhotoCursor
	pageNo := 0
	for {
		pageNo++
		got, hasMore, next, err := ppRepo.ListPhotoIDsByPersonCursor(person.ID, cursor, 3)
		require.NoError(t, err)
		require.NotEmpty(t, got, "page %d should not be empty", pageNo)
		for _, id := range got {
			assert.False(t, seen[id], "page %d: id %d already seen — cursor repeated a row", pageNo, id)
			seen[id] = true
		}
		if hasMore {
			require.NotNil(t, next, "hasMore=true but nil nextCursor")
			assert.NotEqual(t, cursor, next, "nextCursor must differ from input cursor (no stall)")
			cursor = next
		} else {
			break
		}
		require.LessOrEqual(t, pageNo, 5, "too many pages — cursor not progressing")
	}

	// 三页（实际 ceil(8/3)=3）覆盖全部 8 张，无遗漏。
	assert.Len(t, seen, n, "all %d photos covered, no gap", n)
	for _, id := range ids {
		assert.True(t, seen[id], "photo %d missing across pages", id)
	}
}

// TestPersonPhotos_IndexMatchesFallbackJOIN 验证 person_photos 派生表 cursor 查询结果
// 与原 JOIN + DISTINCT + ORDER BY 查询（fallback 路径）一致。
// 两者必须返回相同的 photo_id 序列（含 NULL taken_at 区），否则迁移切换会改变可见结果。
func TestPersonPhotos_IndexMatchesFallbackJOIN(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "Cmp", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	// 构造含同 taken_at、NULL taken_at、多 face 同照片（去重）的混合场景。
	t1 := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC) // 与 t1 相同，触发 id DESC tiebreaker
	photos := []*model.Photo{
		{FilePath: "/c1.jpg", FileName: "c1.jpg", FileSize: 1, FileHash: "hc1", TakenAt: &t1, Status: model.PhotoStatusActive},
		{FilePath: "/c2.jpg", FileName: "c2.jpg", FileSize: 1, FileHash: "hc2", TakenAt: &t2, Status: model.PhotoStatusActive},
		{FilePath: "/c3.jpg", FileName: "c3.jpg", FileSize: 1, FileHash: "hc3", TakenAt: nil, Status: model.PhotoStatusActive},
	}
	for _, p := range photos {
		require.NoError(t, db.Create(p).Error)
		// c1 加两张 face 验证去重
		require.NoError(t, db.Create(&model.Face{PhotoID: p.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	}
	require.NoError(t, db.Create(&model.Face{PhotoID: photos[0].ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.8}).Error)

	ppRepo := NewPersonPhotoRepository(db)
	photoRepo := NewPhotoRepository(db)

	// 索引路径逐页收集 id 序列。
	var indexIDs []uint
	var cur *PersonPhotoCursor
	for {
		got, hasMore, next, err := ppRepo.ListPhotoIDsByPersonCursor(person.ID, cur, 2)
		require.NoError(t, err)
		indexIDs = append(indexIDs, got...)
		if !hasMore {
			break
		}
		cur = next
	}

	// fallback 路径逐页收集 id 序列。
	var fallbackIDs []uint
	var fcur *PersonPhotoCursor
	for {
		got, hasMore, next, err := photoRepo.ListPhotoSummariesByPersonIDCursor(person.ID, fcur, 2)
		require.NoError(t, err)
		for _, p := range got {
			fallbackIDs = append(fallbackIDs, p.ID)
		}
		if !hasMore {
			break
		}
		fcur = next
	}

	assert.Equal(t, fallbackIDs, indexIDs, "index and fallback cursor paths must return identical id sequences")
}

// TestPersonPhotos_NullZoneNoRepeatNoGap 验证 NULL taken_at 区间翻页无重复无遗漏。
func TestPersonPhotos_NullZoneNoRepeatNoGap(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "NullZone", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	// 5 张全 NULL taken_at，靠 id DESC 排序。
	const n = 5
	ids := make([]uint, 0, n)
	for i := 0; i < n; i++ {
		p := &model.Photo{
			FilePath: fmt.Sprintf("/nz_%d.jpg", i), FileName: fmt.Sprintf("nz_%d.jpg", i),
			FileSize: 1, FileHash: fmt.Sprintf("hnz_%d", i), TakenAt: nil, Status: model.PhotoStatusActive,
		}
		require.NoError(t, db.Create(p).Error)
		ids = append(ids, p.ID)
		require.NoError(t, db.Create(&model.Face{PhotoID: p.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	}

	ppRepo := NewPersonPhotoRepository(db)
	seen := map[uint]bool{}
	var cur *PersonPhotoCursor
	for {
		got, hasMore, next, err := ppRepo.ListPhotoIDsByPersonCursor(person.ID, cur, 2)
		require.NoError(t, err)
		for _, id := range got {
			assert.False(t, seen[id], "NULL zone: id %d repeated", id)
			seen[id] = true
		}
		if !hasMore {
			break
		}
		require.NotNil(t, next)
		cur = next
	}
	assert.Len(t, seen, n)
}

// TestPersonPhotos_ConsistencyReport_Structured 验证结构化报告分别计数各类不一致。
// 构造 missing / extra / orphan / taken_at 四类不一致，断言报告逐项非零、其余为零。
func TestPersonPhotos_ConsistencyReport_Structured(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "Report", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	t1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	// p1 有效，trigger 自动写入 person_photos。
	p1 := &model.Photo{FilePath: "/r1.jpg", FileName: "r1.jpg", FileSize: 1, FileHash: "hr1", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(p1).Error)
	require.NoError(t, db.Create(&model.Face{PhotoID: p1.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)

	// p2 有效但手动删除 person_photos → missing。
	p2 := &model.Photo{FilePath: "/r2.jpg", FileName: "r2.jpg", FileSize: 1, FileHash: "hr2", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(p2).Error)
	require.NoError(t, db.Create(&model.Face{PhotoID: p2.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	require.NoError(t, db.Exec("DELETE FROM person_photos WHERE person_id = ? AND photo_id = ?", person.ID, p2.ID).Error)

	// extra：插入一条无有效 face 的 person_photos（虚构 photo_id）。
	require.NoError(t, db.Exec("INSERT INTO person_photos(person_id, photo_id, taken_at) VALUES(?, ?, ?)", person.ID, 999999, &t1).Error)

	// orphan：插入一条 photo 已删的关联。先建 photo 再删，留 person_photos。
	pOrphan := &model.Photo{FilePath: "/ro.jpg", FileName: "ro.jpg", FileSize: 1, FileHash: "hro", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(pOrphan).Error)
	require.NoError(t, db.Exec("INSERT INTO person_photos(person_id, photo_id, taken_at) VALUES(?, ?, ?)", person.ID, pOrphan.ID, &t1).Error)
	require.NoError(t, db.Unscoped().Delete(&model.Photo{}, pOrphan.ID).Error)

	// taken_at 不一致：p1 的 person_photos.taken_at 改成不同值。
	t2 := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.Exec("UPDATE person_photos SET taken_at = ? WHERE person_id = ? AND photo_id = ?", t2, person.ID, p1.ID).Error)

	ppRepo := NewPersonPhotoRepository(db)
	rep, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)

	assert.Greater(t, rep.MissingAssociations, int64(0), "should detect missing (p2)")
	assert.Greater(t, rep.ExtraAssociations, int64(0), "should detect extra (999999)")
	assert.Greater(t, rep.OrphanPhotos, int64(0), "should detect orphan (deleted pOrphan)")
	assert.Greater(t, rep.TakenAtMismatches, int64(0), "should detect taken_at mismatch (p1)")
}

// TestPersonPhotos_RepairBatch_FixesInconsistencies 验证 RepairBatch 能修复各类不一致并最终 clean。
// 这是线上“校验失败后只重试不修复、永远卡在 backfilling”的回归保护。
func TestPersonPhotos_RepairBatch_FixesInconsistencies(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "Repair", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	t1 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	// 有效 face 但删除 person_photos → missing。
	p1 := &model.Photo{FilePath: "/rp1.jpg", FileName: "rp1.jpg", FileSize: 1, FileHash: "hrp1", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(p1).Error)
	require.NoError(t, db.Create(&model.Face{PhotoID: p1.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	require.NoError(t, db.Exec("DELETE FROM person_photos WHERE person_id = ? AND photo_id = ?", person.ID, p1.ID).Error)

	// 第二张有效照片 + face，保持 person_photos 记录，作为 taken_at 不一致的修复目标。
	p2 := &model.Photo{FilePath: "/rp2.jpg", FileName: "rp2.jpg", FileSize: 1, FileHash: "hrp2", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(p2).Error)
	require.NoError(t, db.Create(&model.Face{PhotoID: p2.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	// p2 的 person_photos 已由 trigger 写入，篡改 taken_at 制造不一致。
	require.NoError(t, db.Exec("UPDATE person_photos SET taken_at = ? WHERE person_id = ? AND photo_id = ?", time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), person.ID, p2.ID).Error)

	// extra：虚构关联。
	require.NoError(t, db.Exec("INSERT INTO person_photos(person_id, photo_id, taken_at) VALUES(?, ?, ?)", person.ID, 999999, &t1).Error)

	ppRepo := NewPersonPhotoRepository(db)

	// 修复前不 clean。
	rep0, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	require.False(t, rep0.IsClean(), "should be dirty before repair")

	// 跑修复（missing 已被删除，需重新插入；extra 删除；taken_at 同步）。
	delta, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)
	assert.Greater(t, delta.ExtraDeleted, int64(0), "should delete extra")
	assert.Greater(t, delta.MissingInserted, int64(0), "should insert missing")
	assert.Greater(t, delta.TakenAtFixed, int64(0), "should fix taken_at")

	// 修复后 clean。
	rep1, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.True(t, rep1.IsClean(), "should be clean after repair: %+v", rep1)

	// p1 关联恢复（missing 补齐）。
	var cnt int64
	require.NoError(t, db.Table("person_photos").Where("person_id = ? AND photo_id = ?", person.ID, p1.ID).Count(&cnt).Error)
	assert.Equal(t, int64(1), cnt, "p1 association restored")
	// p2 的 taken_at 已同步回正确值。
	var pp model.PersonPhoto
	require.NoError(t, db.Where("person_id = ? AND photo_id = ?", person.ID, p2.ID).First(&pp).Error)
	require.NotNil(t, pp.TakenAt)
	assert.True(t, pp.TakenAt.Equal(t1), "p2 taken_at synced to photo value")
}

