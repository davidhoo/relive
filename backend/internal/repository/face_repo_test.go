package repository

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFaceRepository_CreateAndList(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	face1 := &model.Face{
		PhotoID:       1,
		PersonID:      &person.ID,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.90,
		ThumbnailPath: "faces/1.jpg",
	}
	face2 := &model.Face{
		PhotoID:      1,
		BBoxX:        0.5,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.88,
		QualityScore: 0.80,
	}
	require.NoError(t, faceRepo.Create(face1))
	require.NoError(t, faceRepo.Create(face2))

	byPhoto, err := faceRepo.ListByPhotoID(1)
	require.NoError(t, err)
	require.Len(t, byPhoto, 2)

	byPerson, err := faceRepo.ListByPersonID(person.ID)
	require.NoError(t, err)
	require.Len(t, byPerson, 1)
	assert.Equal(t, face1.ID, byPerson[0].ID)
}

func TestFaceRepository_ListPending(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type pendingLister interface {
		ListPending(limit int) ([]*model.Face, error)
	}

	faceRepo, ok := NewFaceRepository(db).(pendingLister)
	require.True(t, ok)

	pendingOne := &model.Face{
		PhotoID:       1,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.90,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.77,
		ClusteredAt:   ptrTime(time.Now().Add(-2 * time.Hour)),
		ThumbnailPath: "faces/pending-1.jpg",
	}
	pendingTwo := &model.Face{
		PhotoID:       2,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.94,
		QualityScore:  0.89,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.78,
		ClusteredAt:   ptrTime(time.Now().Add(-1 * time.Hour)),
	}
	assignedPersonID := uint(9)
	assigned := &model.Face{
		PhotoID:       3,
		PersonID:      &assignedPersonID,
		BBoxX:         0.3,
		BBoxY:         0.3,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.93,
		QualityScore:  0.88,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}

	require.NoError(t, db.Create(pendingOne).Error)
	require.NoError(t, db.Create(pendingTwo).Error)
	require.NoError(t, db.Create(assigned).Error)

	faces, err := faceRepo.ListPending(1)
	require.NoError(t, err)
	require.Len(t, faces, 1)
	assert.Equal(t, pendingOne.ID, faces[0].ID)
	assert.Equal(t, model.FaceClusterStatusPending, faces[0].ClusterStatus)
}

func TestFaceRepository_ListPending_PrioritizesNeverClusteredFaces(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type pendingLister interface {
		ListPending(limit int) ([]*model.Face, error)
	}

	faceRepo, ok := NewFaceRepository(db).(pendingLister)
	require.True(t, ok)

	retriedOld := &model.Face{
		PhotoID:       1,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.90,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.10,
		ClusteredAt:   ptrTime(time.Now().Add(-3 * time.Hour)),
	}
	neverClustered := &model.Face{
		PhotoID:       2,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.94,
		QualityScore:  0.89,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.0,
		ClusteredAt:   nil,
	}
	retriedRecent := &model.Face{
		PhotoID:       3,
		BBoxX:         0.3,
		BBoxY:         0.3,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.93,
		QualityScore:  0.88,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusterScore:  0.20,
		ClusteredAt:   ptrTime(time.Now().Add(-1 * time.Hour)),
	}

	require.NoError(t, db.Create(retriedOld).Error)
	require.NoError(t, db.Create(neverClustered).Error)
	require.NoError(t, db.Create(retriedRecent).Error)

	faces, err := faceRepo.ListPending(2)
	require.NoError(t, err)
	require.Len(t, faces, 2)
	assert.Equal(t, neverClustered.ID, faces[0].ID)
	assert.Equal(t, retriedOld.ID, faces[1].ID)
}

func TestFaceRepository_GetPendingStats(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type pendingStatsGetter interface {
		GetPendingStats() (*PendingFaceStats, error)
	}

	faceRepo, ok := NewFaceRepository(db).(pendingStatsGetter)
	require.True(t, ok)

	require.NoError(t, db.Create(&model.Face{
		PhotoID:       1,
		BBoxX:         0.1,
		BBoxY:         0.1,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.95,
		QualityScore:  0.9,
		ClusterStatus: model.FaceClusterStatusPending,
	}).Error)
	require.NoError(t, db.Create(&model.Face{
		PhotoID:       2,
		BBoxX:         0.2,
		BBoxY:         0.2,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.94,
		QualityScore:  0.89,
		ClusterStatus: model.FaceClusterStatusPending,
		ClusteredAt:   ptrTime(time.Now().Add(-time.Hour)),
	}).Error)
	require.NoError(t, db.Create(&model.Face{
		PhotoID:       3,
		BBoxX:         0.3,
		BBoxY:         0.3,
		BBoxWidth:     0.2,
		BBoxHeight:    0.2,
		Confidence:    0.93,
		QualityScore:  0.88,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}).Error)

	stats, err := faceRepo.GetPendingStats()
	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.Equal(t, int64(2), stats.Total)
	assert.Equal(t, int64(1), stats.NeverClustered)
	assert.Equal(t, int64(1), stats.Retried)
}

func TestFaceRepository_UpdateClusterFields(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	type clusterUpdater interface {
		UpdateClusterFields(ids []uint, fields map[string]interface{}) error
	}

	faceRepo, ok := NewFaceRepository(db).(clusterUpdater)
	require.True(t, ok)

	faceOne := &model.Face{
		PhotoID:      1,
		BBoxX:        0.1,
		BBoxY:        0.1,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.95,
		QualityScore: 0.90,
	}
	faceTwo := &model.Face{
		PhotoID:      2,
		BBoxX:        0.2,
		BBoxY:        0.2,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.94,
		QualityScore: 0.89,
	}
	faceThree := &model.Face{
		PhotoID:      3,
		BBoxX:        0.3,
		BBoxY:        0.3,
		BBoxWidth:    0.2,
		BBoxHeight:   0.2,
		Confidence:   0.93,
		QualityScore: 0.88,
	}
	require.NoError(t, db.Create(faceOne).Error)
	require.NoError(t, db.Create(faceTwo).Error)
	require.NoError(t, db.Create(faceThree).Error)

	clusteredAt := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, faceRepo.UpdateClusterFields([]uint{faceOne.ID, faceTwo.ID}, map[string]interface{}{
		"cluster_status": model.FaceClusterStatusAssigned,
		"cluster_score":  0.96,
		"clustered_at":   clusteredAt,
	}))

	var updated []*model.Face
	require.NoError(t, db.Order("id ASC").Find(&updated).Error)
	require.Len(t, updated, 3)

	assert.Equal(t, model.FaceClusterStatusAssigned, updated[0].ClusterStatus)
	assert.Equal(t, model.FaceClusterStatusAssigned, updated[1].ClusterStatus)
	assert.InDelta(t, 0.96, updated[0].ClusterScore, 0.0001)
	assert.InDelta(t, 0.96, updated[1].ClusterScore, 0.0001)
	require.NotNil(t, updated[0].ClusteredAt)
	require.NotNil(t, updated[1].ClusteredAt)
	assert.WithinDuration(t, clusteredAt, *updated[0].ClusteredAt, time.Second)
	assert.WithinDuration(t, clusteredAt, *updated[1].ClusteredAt, time.Second)
	assert.Empty(t, updated[2].ClusterStatus)
	assert.Zero(t, updated[2].ClusterScore)
	assert.Nil(t, updated[2].ClusteredAt)
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func TestFaceRepository_ListProfileFaces(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	person := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(person))

	emb := model.EncodeEmbedding([]float32{1, 0, 0})
	// face1: manual-locked -> must sort first regardless of other fields.
	face1 := &model.Face{
		PhotoID: 1, PersonID: &person.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.5, QualityScore: 0.5, Embedding: emb,
		ClusterStatus: model.FaceClusterStatusManual, ClusterScore: 0.0,
		ManualLocked: true, ManualLockReason: "pin",
	}
	// face2: higher cluster_score than face3.
	face2 := &model.Face{
		PhotoID: 1, PersonID: &person.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.9, QualityScore: 0.9, Embedding: emb,
		ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.9,
	}
	// face3: lower cluster_score despite higher quality -> after face2.
	face3 := &model.Face{
		PhotoID: 1, PersonID: &person.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.7, QualityScore: 0.99, Embedding: emb,
		ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 0.5,
	}
	// face4: belongs to another person -> excluded.
	other := &model.Person{Category: model.PersonCategoryStranger}
	require.NoError(t, personRepo.Create(other))
	face4 := &model.Face{
		PhotoID: 1, PersonID: &other.ID,
		BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		Confidence: 0.99, QualityScore: 0.99, Embedding: emb,
		ClusterStatus: model.FaceClusterStatusAssigned, ClusterScore: 1.0,
	}
	require.NoError(t, faceRepo.Create(face1))
	require.NoError(t, faceRepo.Create(face2))
	require.NoError(t, faceRepo.Create(face3))
	require.NoError(t, faceRepo.Create(face4))

	faces, err := faceRepo.ListProfileFaces(person.ID)
	require.NoError(t, err)
	require.Len(t, faces, 3, "only the target person's faces are returned")

	// Deterministic ordering: manual lock, cluster_score, quality, id.
	assert.Equal(t, face1.ID, faces[0].ID)
	assert.Equal(t, face2.ID, faces[1].ID)
	assert.Equal(t, face3.ID, faces[2].ID)

	// Only the lightweight fields plus embedding are selected; thumbnail_path is absent.
	assert.NotEmpty(t, faces[0].Embedding, "embedding loaded for profile builds")
	assert.Equal(t, "pin", faces[0].ManualLockReason)
	assert.Equal(t, person.ID, *faces[0].PersonID)
	assert.Empty(t, faces[0].ThumbnailPath, "thumbnail_path not selected")
	assert.Zero(t, faces[0].BBoxX, "bbox fields not selected")
}

func TestFaceRepository_ListPersonIDsCooccurringWithPhotos(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)
	photoRepo := NewPhotoRepository(db)

	// 三个人物、三张照片。
	pa := &model.Person{Category: model.PersonCategoryFriend}
	pb := &model.Person{Category: model.PersonCategoryFriend}
	pc := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(pa))
	require.NoError(t, personRepo.Create(pb))
	require.NoError(t, personRepo.Create(pc))

	ph1 := &model.Photo{FileName: "1.jpg", FilePath: "/1.jpg"}
	ph2 := &model.Photo{FileName: "2.jpg", FilePath: "/2.jpg"}
	ph3 := &model.Photo{FileName: "3.jpg", FilePath: "/3.jpg"}
	require.NoError(t, photoRepo.Create(ph1))
	require.NoError(t, photoRepo.Create(ph2))
	require.NoError(t, photoRepo.Create(ph3))

	// photo1 同时出现 pa、pb 的人脸（共现）。
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph1.ID, PersonID: &pa.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph1.ID, PersonID: &pb.ID, BBoxX: 0.5, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	// photo2 只有 pc 的人脸。
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph2.ID, PersonID: &pc.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	// photo3 有 pa 的人脸。
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph3.ID, PersonID: &pa.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	// 未指派人物的人脸（应被忽略）。
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph1.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))

	// 查询 photo1 中、候选为 {pa, pb, pc} 的人物 → 应返回 pb（pa 是查询来源候选之一，也共现，应返回）。
	// 实际 photo1 中 pa 与 pb 都有人脸，故两人都应返回。
	got, err := faceRepo.ListPersonIDsCooccurringWithPhotos([]uint{ph1.ID}, []uint{pa.ID, pb.ID, pc.ID})
	require.NoError(t, err)
	assert.Equal(t, []uint{pa.ID, pb.ID}, got, "only persons co-occurring in photo1 returned, ascending")

	// 限定候选为 {pc}，photo1 中无 pc → 空。
	got, err = faceRepo.ListPersonIDsCooccurringWithPhotos([]uint{ph1.ID}, []uint{pc.ID})
	require.NoError(t, err)
	assert.Empty(t, got)

	// 多张照片去重：photo1+photo3 中候选 {pa,pb} → pa 出现两次只返回一次，pb 一次。
	got, err = faceRepo.ListPersonIDsCooccurringWithPhotos([]uint{ph1.ID, ph3.ID}, []uint{pa.ID, pb.ID})
	require.NoError(t, err)
	assert.Equal(t, []uint{pa.ID, pb.ID}, got, "dedup across photos")
}

func TestFaceRepository_ListPersonIDsCooccurringWithPhotos_EmptyAndZero(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	faceRepo := NewFaceRepository(db)

	got, err := faceRepo.ListPersonIDsCooccurringWithPhotos(nil, []uint{1, 2})
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = faceRepo.ListPersonIDsCooccurringWithPhotos([]uint{1, 2}, nil)
	require.NoError(t, err)
	assert.Empty(t, got)

	// 0 ID 被过滤。
	got, err = faceRepo.ListPersonIDsCooccurringWithPhotos([]uint{0, 0}, []uint{0, 0})
	require.NoError(t, err)
	assert.Empty(t, got)
}

// newCountingFaceRepo 用于验证同照片共现查询不产生逐候选 N+1。
func newCountingFaceRepo(t *testing.T) (FaceRepository, *gorm.DB, *int32) {
	t.Helper()
	db := setupTestDB(t)
	var count int32
	db.Callback().Query().Before("gorm:query").Register("test_count_face_queries", func(tx *gorm.DB) {
		atomic.AddInt32(&count, 1)
	})
	return NewFaceRepository(db), db, &count
}

func TestFaceRepository_ListPersonIDsCooccurringWithPhotos_NoNPlusOne(t *testing.T) {
	faceRepo, db, qcount := newCountingFaceRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	photoRepo := NewPhotoRepository(db)
	ph := &model.Photo{FileName: "x.jpg", FilePath: "/x.jpg"}
	require.NoError(t, photoRepo.Create(ph))

	persons := make([]*model.Person, 0, 10)
	for i := 0; i < 10; i++ {
		p := &model.Person{Category: model.PersonCategoryFriend}
		require.NoError(t, personRepo.Create(p))
		require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph.ID, PersonID: &p.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
		persons = append(persons, p)
	}
	photoIDs := []uint{ph.ID}
	candIDs := make([]uint, 0, 10)
	for _, p := range persons {
		candIDs = append(candIDs, p.ID)
	}
	got, err := faceRepo.ListPersonIDsCooccurringWithPhotos(photoIDs, candIDs)
	require.NoError(t, err)
	assert.Len(t, got, 10)
	// 单照片 + 10 候选低于参数上限，应仅 1 次批量查询。
	assert.Equal(t, int32(1), atomic.LoadInt32(qcount), "single batched query, no per-candidate N+1")
}

func TestFaceRepository_ListPersonIDsCooccurringWithPhotos_ChunkingBothDims(t *testing.T) {
	faceRepo, db, qcount := newCountingFaceRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	photoRepo := NewPhotoRepository(db)
	ph := &model.Photo{FileName: "x.jpg", FilePath: "/x.jpg"}
	require.NoError(t, photoRepo.Create(ph))
	p := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(p))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph.ID, PersonID: &p.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))

	// 600 张照片 + 600 个候选 → photo 维度 2 块 × person 维度 2 块 = 4 次查询。
	photoIDs := make([]uint, 0, 600)
	photoIDs = append(photoIDs, ph.ID)
	for i := 1; i < 600; i++ {
		photoIDs = append(photoIDs, uint(1000000+i))
	}
	candIDs := make([]uint, 0, 600)
	candIDs = append(candIDs, p.ID)
	for i := 1; i < 600; i++ {
		candIDs = append(candIDs, uint(2000000+i))
	}
	got, err := faceRepo.ListPersonIDsCooccurringWithPhotos(photoIDs, candIDs)
	require.NoError(t, err)
	assert.Equal(t, []uint{p.ID}, got)
	assert.Equal(t, int32(4), atomic.LoadInt32(qcount), "both dimensions chunked (2x2)")
}

func TestFaceRepository_ListPersonIDsSharingPhotos(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)
	photoRepo := NewPhotoRepository(db)

	// target=pa, 候选 pb/pc/pd。
	pa := &model.Person{Category: model.PersonCategoryFriend}
	pb := &model.Person{Category: model.PersonCategoryFriend}
	pc := &model.Person{Category: model.PersonCategoryFriend}
	pd := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(pa))
	require.NoError(t, personRepo.Create(pb))
	require.NoError(t, personRepo.Create(pc))
	require.NoError(t, personRepo.Create(pd))

	ph1 := &model.Photo{FileName: "1.jpg", FilePath: "/1.jpg"}
	ph2 := &model.Photo{FileName: "2.jpg", FilePath: "/2.jpg"}
	require.NoError(t, photoRepo.Create(ph1))
	require.NoError(t, photoRepo.Create(ph2))

	// ph1 中 pa 与 pb 共现（pb 应命中）。
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph1.ID, PersonID: &pa.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph1.ID, PersonID: &pb.ID, BBoxX: 0.5, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	// ph2 中 pa 与 pc 共现（pc 应命中）。
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph2.ID, PersonID: &pa.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph2.ID, PersonID: &pc.ID, BBoxX: 0.5, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	// pd 从不与 pa 同照片（不应命中）。
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph2.ID, PersonID: &pd.ID, BBoxX: 0.5, BBoxY: 0.5, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	// 注意：pd 在 ph2 与 pc 同照片，但与 pa 不同照片（pa 在 ph2），故 pd 与 pa 实际同照片共现。
	// 重新设计：让 pd 独占 ph3，确保不与 pa 共现。
	ph3 := &model.Photo{FileName: "3.jpg", FilePath: "/3.jpg"}
	require.NoError(t, photoRepo.Create(ph3))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph3.ID, PersonID: &pd.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))
	// 移除 ph2 上的 pd 人脸以隔离。
	require.NoError(t, db.Where("photo_id = ? AND person_id = ?", ph2.ID, pd.ID).Delete(&model.Face{}).Error)

	got, err := faceRepo.ListPersonIDsSharingPhotos(pa.ID, []uint{pb.ID, pc.ID, pd.ID})
	require.NoError(t, err)
	assert.Equal(t, []uint{pb.ID, pc.ID}, got, "pb 与 pc 与 pa 同照片共现，pd 否则，结果升序")

	// 候选包含 target 自身时应被排除。
	got, err = faceRepo.ListPersonIDsSharingPhotos(pa.ID, []uint{pa.ID, pb.ID})
	require.NoError(t, err)
	assert.Equal(t, []uint{pb.ID}, got, "target 自身不计入共现")

	// 空候选 / target=0 → nil。
	got, err = faceRepo.ListPersonIDsSharingPhotos(pa.ID, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	got, err = faceRepo.ListPersonIDsSharingPhotos(0, []uint{pb.ID})
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestFaceRepository_ListPersonIDsSharingPhotos_NoNPlusOne(t *testing.T) {
	faceRepo, db, qcount := newCountingFaceRepo(t)
	defer teardownTestDB(db)

	personRepo := NewPersonRepository(db)
	photoRepo := NewPhotoRepository(db)
	ph := &model.Photo{FileName: "x.jpg", FilePath: "/x.jpg"}
	require.NoError(t, photoRepo.Create(ph))
	target := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(target))
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: ph.ID, PersonID: &target.ID, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))

	// 600 个候选 → 仅 1 次查询（不逐候选 N+1）。
	candIDs := make([]uint, 0, 600)
	candIDs = append(candIDs, target.ID)
	for i := 1; i < 600; i++ {
		candIDs = append(candIDs, uint(2000000+i))
	}
	got, err := faceRepo.ListPersonIDsSharingPhotos(target.ID, candIDs)
	require.NoError(t, err)
	assert.Empty(t, got, "无候选与 target 同照片")
	// 600 候选按 sqliteVarLimit(500) 分块为 2 块；子查询与外层各触发一次查询回调，
	// 总数远小于 600（非逐候选 N+1）。
	assert.Less(t, atomic.LoadInt32(qcount), int32(600), "no per-candidate N+1; chunked and bounded")
	assert.Greater(t, atomic.LoadInt32(qcount), int32(0))
}

func TestFaceRepository_ListAssignedPersonIDsPaged(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	// Create 5 persons with faces assigned.
	personIDs := make([]uint, 5)
	for i := 0; i < 5; i++ {
		p := &model.Person{Category: model.PersonCategoryFriend}
		require.NoError(t, personRepo.Create(p))
		personIDs[i] = p.ID
		require.NoError(t, faceRepo.Create(&model.Face{
			PhotoID:      uint(i + 1),
			PersonID:     &p.ID,
			BBoxX:        0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
			Confidence:   0.9, QualityScore: 0.8,
		}))
	}
	// Also create an unassigned face — should not appear.
	require.NoError(t, faceRepo.Create(&model.Face{PhotoID: 99, BBoxX: 0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2}))

	// Page 1: offset=0, limit=3 → first 3 person IDs (ascending).
	page1, err := faceRepo.ListAssignedPersonIDsPaged(0, 3)
	require.NoError(t, err)
	assert.Len(t, page1, 3)
	assert.Equal(t, personIDs[0], page1[0])
	assert.Equal(t, personIDs[1], page1[1])
	assert.Equal(t, personIDs[2], page1[2])

	// Page 2: offset=3, limit=3 → last 2 person IDs.
	page2, err := faceRepo.ListAssignedPersonIDsPaged(3, 3)
	require.NoError(t, err)
	assert.Len(t, page2, 2)
	assert.Equal(t, personIDs[3], page2[0])
	assert.Equal(t, personIDs[4], page2[1])

	// Page 3: offset=5, limit=3 → empty.
	page3, err := faceRepo.ListAssignedPersonIDsPaged(5, 3)
	require.NoError(t, err)
	assert.Empty(t, page3)

	// limit=0 → empty (no error).
	empty, err := faceRepo.ListAssignedPersonIDsPaged(0, 0)
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestFaceRepository_ListPrototypeEmbeddings_Batched(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	// Create 2 persons, each with 3 faces (different quality).
	emb := encodeFloat32(t, []float32{1.0, 0.0, 0.0})
	for pid := 1; pid <= 2; pid++ {
		p := &model.Person{Category: model.PersonCategoryFriend}
		require.NoError(t, personRepo.Create(p))
		for f := 0; f < 3; f++ {
			require.NoError(t, faceRepo.Create(&model.Face{
				PhotoID:      uint(pid*10 + f),
				PersonID:     &p.ID,
				BBoxX:        0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
				Confidence:   0.9,
				QualityScore: float64(3 - f), // 3, 2, 1
				Embedding:    emb,
			}))
		}
	}

	personIDs, err := faceRepo.ListAssignedPersonIDsPaged(0, 10)
	require.NoError(t, err)
	assert.Len(t, personIDs, 2)

	// Request top 2 per person → 4 faces total.
	faces, err := faceRepo.ListPrototypeEmbeddings(personIDs, 2)
	require.NoError(t, err)
	assert.Len(t, faces, 4) // 2 persons × 2 per person

	// Verify each person got 2 faces (highest quality first).
	byPerson := make(map[uint]int)
	for _, f := range faces {
		byPerson[*f.PersonID]++
	}
	for _, count := range byPerson {
		assert.Equal(t, 2, count)
	}
}

func encodeFloat32(t *testing.T, vals []float32) []byte {
	t.Helper()
	payload, err := json.Marshal(vals)
	require.NoError(t, err)
	return payload
}

// --- Hidden person filtering tests ---

func TestFaceRepo_HiddenPersonFilteredFromAssignedPersonIDs(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	visible := &model.Person{Category: model.PersonCategoryFriend}
	hidden := &model.Person{Category: model.PersonCategoryFriend, Hidden: true}
	require.NoError(t, personRepo.Create(visible))
	require.NoError(t, personRepo.Create(hidden))

	for _, p := range []*model.Person{visible, hidden} {
		face := &model.Face{
			PhotoID:      1,
			PersonID:     &p.ID,
			ClusterStatus: model.FaceClusterStatusAssigned,
		}
		require.NoError(t, faceRepo.Create(face))
	}

	ids, err := faceRepo.ListAssignedPersonIDs()
	require.NoError(t, err)
	assert.Contains(t, ids, visible.ID)
	assert.NotContains(t, ids, hidden.ID)

	paged, err := faceRepo.ListAssignedPersonIDsPaged(0, 100)
	require.NoError(t, err)
	assert.Contains(t, paged, visible.ID)
	assert.NotContains(t, paged, hidden.ID)
}

func TestFaceRepo_HiddenPersonFilteredFromPrototypeEmbeddings(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	visible := &model.Person{Category: model.PersonCategoryFriend}
	hidden := &model.Person{Category: model.PersonCategoryFriend, Hidden: true}
	require.NoError(t, personRepo.Create(visible))
	require.NoError(t, personRepo.Create(hidden))

	emb := []byte(`[0.1,0.2,0.3]`)
	for _, p := range []*model.Person{visible, hidden} {
		face := &model.Face{
			PhotoID:       1,
			PersonID:      &p.ID,
			ClusterStatus: model.FaceClusterStatusAssigned,
			Embedding:     emb,
		}
		require.NoError(t, faceRepo.Create(face))
	}

	faces, err := faceRepo.ListPrototypeEmbeddings([]uint{visible.ID, hidden.ID}, 5)
	require.NoError(t, err)
	personIDs := make(map[uint]bool)
	for _, f := range faces {
		if f.PersonID != nil {
			personIDs[*f.PersonID] = true
		}
	}
	assert.True(t, personIDs[visible.ID], "visible person prototype should be returned")
	assert.False(t, personIDs[hidden.ID], "hidden person prototype should be filtered out")
}

func TestFaceRepo_HiddenPersonFilteredFromLowConfidence(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	visible := &model.Person{Category: model.PersonCategoryFriend}
	hidden := &model.Person{Category: model.PersonCategoryFriend, Hidden: true}
	require.NoError(t, personRepo.Create(visible))
	require.NoError(t, personRepo.Create(hidden))

	for _, p := range []*model.Person{visible, hidden} {
		face := &model.Face{
			PhotoID:       1,
			PersonID:      &p.ID,
			ClusterStatus: model.FaceClusterStatusAssigned,
			ClusterScore:  0.3,
		}
		require.NoError(t, faceRepo.Create(face))
	}

	faces, err := faceRepo.ListLowConfidence(0.5, 10)
	require.NoError(t, err)
	personIDs := make(map[uint]bool)
	for _, f := range faces {
		if f.PersonID != nil {
			personIDs[*f.PersonID] = true
		}
	}
	assert.True(t, personIDs[visible.ID], "visible person low-confidence face should be returned")
	assert.False(t, personIDs[hidden.ID], "hidden person low-confidence face should be filtered out")
}

func TestFaceRepo_PendingNotAffectedByHidden(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	// Pending face with no person_id — should always be returned.
	pendingFace := &model.Face{
		PhotoID:       1,
		ClusterStatus: model.FaceClusterStatusPending,
	}
	require.NoError(t, faceRepo.Create(pendingFace))

	faces, err := faceRepo.ListPending(10)
	require.NoError(t, err)
	assert.Len(t, faces, 1)
	assert.Equal(t, pendingFace.ID, faces[0].ID)
}

func TestFaceRepo_RestorePersonReappearsInQueries(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	person := &model.Person{Category: model.PersonCategoryFriend, Hidden: true}
	require.NoError(t, personRepo.Create(person))

	face := &model.Face{
		PhotoID:       1,
		PersonID:      &person.ID,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}
	require.NoError(t, faceRepo.Create(face))

	// Hidden: should not appear.
	ids, err := faceRepo.ListAssignedPersonIDs()
	require.NoError(t, err)
	assert.NotContains(t, ids, person.ID)

	// Restore.
	_, err = personRepo.UpdateVisibility([]uint{person.ID}, false)
	require.NoError(t, err)

	// Should reappear.
	ids, err = faceRepo.ListAssignedPersonIDs()
	require.NoError(t, err)
	assert.Contains(t, ids, person.ID)
}
