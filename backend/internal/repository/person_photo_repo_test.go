package repository

import (
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

	// 一致性校验通过
	inc, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.Equal(t, 0, inc, "consistency check passes after backfill")

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
	inc, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.Equal(t, 0, inc)
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
