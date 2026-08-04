package repository

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// seedOrphanFaceFixture 创建照片+人物+Face，再物理删除照片但保留 Face，模拟生产悬空 Face。
// 返回 (personID, deletedPhotoID, orphanFaceID, survivingPhotoID, survivingFaceID)。
func seedOrphanFaceFixture(t *testing.T, db *gorm.DB) (uint, uint, uint, uint, uint) {
	t.Helper()
	person := &model.Person{Name: "Orphan", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	// 将被删除的照片
	pDel := &model.Photo{FilePath: "/del.jpg", FileName: "del.jpg", FileSize: 1, FileHash: "hdel", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(pDel).Error)
	// 存活的照片
	pSurv := &model.Photo{FilePath: "/surv.jpg", FileName: "surv.jpg", FileSize: 1, FileHash: "hsurv", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(pSurv).Error)

	// 两张 face 都分配给同一人物
	fDel := &model.Face{PhotoID: pDel.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9, Confidence: 0.9}
	fSurv := &model.Face{PhotoID: pSurv.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.8, Confidence: 0.8}
	require.NoError(t, db.Create(fDel).Error)
	require.NoError(t, db.Create(fSurv).Error)

	// 设置 representative_face_id 指向将被删除的 face
	require.NoError(t, db.Model(&model.Person{}).Where("id = ?", person.ID).Update("representative_face_id", fDel.ID).Error)

	// 物理删除照片（模拟生产数据：只删 photos 行，保留 faces）
	require.NoError(t, db.Unscoped().Delete(&model.Photo{}, pDel.ID).Error)

	return person.ID, pDel.ID, fDel.ID, pSurv.ID, fSurv.ID
}

// TestPersonPhotos_OrphanFaceRepair_RepairBatchHandlesOrphanFaces 验证 RepairBatch 能处理
// Face 引用不存在照片的场景：删除悬空 Face，一致性校验通过。
func TestPersonPhotos_OrphanFaceRepair_RepairBatchHandlesOrphanFaces(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, _, orphanFaceID, _, _ := seedOrphanFaceFixture(t, db)
	ppRepo := NewPersonPhotoRepository(db)

	// 修复前：存在悬空 Face，一致性校验非 clean。
	rep0, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.False(t, rep0.IsClean(), "should be dirty with orphan face")
	assert.Greater(t, rep0.OrphanFaces, int64(0), "should detect orphan faces")

	// 确认旧实现无法补齐 missing（悬空 Face 被误计为 missing 但 JOIN photos 无法插入）
	assert.Equal(t, int64(0), rep0.MissingAssociations, "orphan face should NOT count as missing (photo does not exist)")

	// 跑修复
	delta, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)
	assert.Greater(t, delta.OrphanFacesDeleted, int64(0), "should delete orphan faces")
	assert.Contains(t, delta.AffectedPersonIDs, personID, "should report affected person")

	// 修复后：一致性校验通过
	rep1, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.True(t, rep1.IsClean(), "should be clean after repair: %+v", rep1)

	// 悬空 Face 已删除
	var faceCount int64
	require.NoError(t, db.Model(&model.Face{}).Where("id = ?", orphanFaceID).Count(&faceCount).Error)
	assert.Equal(t, int64(0), faceCount, "orphan face should be deleted")
}

// TestPersonPhotos_OrphanFaceRepair_RepresentativeFaceFixed 验证删除悬空 Face 后
// representative_face_id 被修正为仍存在的 face。
func TestPersonPhotos_OrphanFaceRepair_RepresentativeFaceFixed(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, _, _, _, survivingFaceID := seedOrphanFaceFixture(t, db)
	ppRepo := NewPersonPhotoRepository(db)

	// 修复前：representative_face_id 指向已删除的 face
	var person model.Person
	require.NoError(t, db.First(&person, personID).Error)
	// representative_face_id 此时可能已被 trigger 修正或仍指向旧值
	// （照片删除时 face 仍在，所以 representative_face_id 仍指向它）

	// 跑修复
	_, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)

	// 修复后：representative_face_id 应指向存活 face
	require.NoError(t, db.First(&person, personID).Error)
	require.NotNil(t, person.RepresentativeFaceID)
	assert.Equal(t, survivingFaceID, *person.RepresentativeFaceID, "representative_face_id should point to surviving face")
}

// TestPersonPhotos_OrphanFaceRepair_PersonStatsUpdated 验证删除悬空 Face 后
// 人物的 face_count 和 photo_count 正确更新（同一人物还有其他照片）。
func TestPersonPhotos_OrphanFaceRepair_PersonStatsUpdated(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, _, _, _, _ := seedOrphanFaceFixture(t, db)
	ppRepo := NewPersonPhotoRepository(db)

	// 修复前：face_count=2, photo_count=2（包含悬空 face）
	var person model.Person
	require.NoError(t, db.First(&person, personID).Error)
	// 模拟生产状态：删除前 stats 尚未同步（照片已被删除但 face 仍在）
	require.NoError(t, db.Model(&model.Person{}).Where("id = ?", personID).Updates(map[string]interface{}{"face_count": 2, "photo_count": 2}).Error)

	// 跑修复
	_, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)

	// 修复后：face_count=1, photo_count=1
	require.NoError(t, db.First(&person, personID).Error)
	assert.Equal(t, 1, person.FaceCount, "face_count should be 1 after orphan face cleanup")
	assert.Equal(t, 1, person.PhotoCount, "photo_count should be 1 after orphan face cleanup")
}

// TestPersonPhotos_OrphanFaceRepair_IdentityProfileDirty 验证删除悬空 Face 后
// 受影响人物的 identity profile 被标记为 dirty。
func TestPersonPhotos_OrphanFaceRepair_IdentityProfileDirty(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, _, _, _, _ := seedOrphanFaceFixture(t, db)
	ppRepo := NewPersonPhotoRepository(db)

	// 跑修复
	_, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)

	// identity profile 应被标记为 dirty
	var profile model.PersonIdentityProfile
	err = db.Where("person_id = ?", personID).First(&profile).Error
	require.NoError(t, err)
	assert.Equal(t, model.PersonIdentityProfileStatusDirty, profile.Status)
	assert.Equal(t, "orphan_face_cleanup", profile.DirtyReason)
}

// TestPersonPhotos_OrphanFaceRepair_LastPhotoPersonStats 验证人物最后一张照片（的 face）被删除时，
// 人物计数归零，representative_face_id 置 NULL。
func TestPersonPhotos_OrphanFaceRepair_LastPhotoPersonStats(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "Last", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	p := &model.Photo{FilePath: "/last.jpg", FileName: "last.jpg", FileSize: 1, FileHash: "hlast", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(p).Error)
	f := &model.Face{PhotoID: p.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}
	require.NoError(t, db.Create(f).Error)
	require.NoError(t, db.Model(&model.Person{}).Where("id = ?", person.ID).Update("representative_face_id", f.ID).Error)
	require.NoError(t, db.Model(&model.Person{}).Where("id = ?", person.ID).Updates(map[string]interface{}{"face_count": 1, "photo_count": 1}).Error)

	// 物理删除照片，保留 face
	require.NoError(t, db.Unscoped().Delete(&model.Photo{}, p.ID).Error)

	ppRepo := NewPersonPhotoRepository(db)
	_, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)

	// 人物计数应归零
	var person2 model.Person
	require.NoError(t, db.First(&person2, person.ID).Error)
	assert.Equal(t, 0, person2.FaceCount, "face_count should be 0")
	assert.Equal(t, 0, person2.PhotoCount, "photo_count should be 0")
	assert.Nil(t, person2.RepresentativeFaceID, "representative_face_id should be NULL")
}

// TestPersonPhotos_OrphanFaceRepair_MultipleFacesSamePhoto 验证多张 Face 指向同一被删照片时全部清理。
func TestPersonPhotos_OrphanFaceRepair_MultipleFacesSamePhoto(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	person := &model.Person{Name: "Multi", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	p := &model.Photo{FilePath: "/multi.jpg", FileName: "multi.jpg", FileSize: 1, FileHash: "hmulti", TakenAt: &t1, Status: model.PhotoStatusActive}
	require.NoError(t, db.Create(p).Error)

	// 同照片 3 张 face
	for i := 0; i < 3; i++ {
		require.NoError(t, db.Create(&model.Face{PhotoID: p.ID, PersonID: &person.ID, ClusterStatus: model.FaceClusterStatusAssigned, QualityScore: 0.9}).Error)
	}

	// 物理删除照片
	require.NoError(t, db.Unscoped().Delete(&model.Photo{}, p.ID).Error)

	ppRepo := NewPersonPhotoRepository(db)
	delta, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)
	assert.Equal(t, int64(3), delta.OrphanFacesDeleted, "all 3 orphan faces should be deleted")

	// 一致性通过
	rep, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.True(t, rep.IsClean())
}

// TestPersonPhotos_OrphanFaceRepair_Idempotent 验证修复重复运行保持幂等。
func TestPersonPhotos_OrphanFaceRepair_Idempotent(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	_, _, _, _, _ = seedOrphanFaceFixture(t, db)
	ppRepo := NewPersonPhotoRepository(db)

	// 第一次修复
	delta1, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)
	assert.Greater(t, delta1.OrphanFacesDeleted, int64(0))

	// 第二次修复：无悬空 Face 可删
	delta2, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)
	assert.Equal(t, int64(0), delta2.OrphanFacesDeleted, "second run should delete 0 orphan faces")

	// 一致性仍然 clean
	rep, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.True(t, rep.IsClean())
}

// TestPersonPhotos_OrphanFaceRepair_NormalMissingStillWorks 验证修复悬空 Face 的同时，
// 普通 missing 关联仍能正常补齐。
func TestPersonPhotos_OrphanFaceRepair_NormalMissingStillWorks(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, _, _, survivingPhotoID, _ := seedOrphanFaceFixture(t, db)
	ppRepo := NewPersonPhotoRepository(db)

	// 删除存活照片的 person_photos 记录，制造普通 missing
	require.NoError(t, db.Exec("DELETE FROM person_photos WHERE person_id = ? AND photo_id = ?", personID, survivingPhotoID).Error)

	// 跑修复：应同时处理 orphan face 和 normal missing
	delta, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)
	assert.Greater(t, delta.OrphanFacesDeleted, int64(0), "should delete orphan faces")
	assert.Greater(t, delta.MissingInserted, int64(0), "should insert normal missing")

	// 一致性通过
	rep, err := ppRepo.RunConsistencyCheck(db)
	require.NoError(t, err)
	assert.True(t, rep.IsClean())
}

// TestPersonPhotos_OrphanFaceRepair_PersonPhotosNoPseudoAssociation 验证修复不会为不存在的照片
// 创建伪 person_photos 关联。
func TestPersonPhotos_OrphanFaceRepair_PersonPhotosNoPseudoAssociation(t *testing.T) {
	db := setupPersonPhotosTestDB(t)
	personID, deletedPhotoID, _, _, _ := seedOrphanFaceFixture(t, db)
	ppRepo := NewPersonPhotoRepository(db)

	_, err := ppRepo.RepairBatch(db, 500)
	require.NoError(t, err)

	// 不应为已删除照片创建 person_photos
	var cnt int64
	require.NoError(t, db.Table("person_photos").Where("person_id = ? AND photo_id = ?", personID, deletedPhotoID).Count(&cnt).Error)
	assert.Equal(t, int64(0), cnt, "no pseudo association for deleted photo")
}
