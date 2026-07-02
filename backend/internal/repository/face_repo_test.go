package repository

import (
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
