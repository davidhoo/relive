package repository

import (
	"fmt"
	"strings"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFaceExclusionRepo_CreateAndList(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewFaceExclusionRepository(db)

	rec := &model.FaceExclusion{
		PhotoID:      1,
		SourceFaceID: 100,
		Reason:       model.ExclusionReasonNonFace,
		BBoxX:        0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
	}
	require.NoError(t, repo.Create(rec))
	assert.NotZero(t, rec.ID)

	records, err := repo.ListByPhotoID(1)
	require.NoError(t, err)
	assert.Len(t, records, 1)
	assert.Equal(t, model.ExclusionReasonNonFace, records[0].Reason)
}

func TestFaceExclusionRepo_DeleteByID(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	repo := NewFaceExclusionRepository(db)

	rec := &model.FaceExclusion{
		PhotoID:      1,
		SourceFaceID: 100,
		Reason:       model.ExclusionReasonLowQuality,
		BBoxX:        0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
	}
	require.NoError(t, repo.Create(rec))

	require.NoError(t, repo.DeleteByID(rec.ID))

	records, err := repo.ListByPhotoID(1)
	require.NoError(t, err)
	assert.Len(t, records, 0)
}

func TestFaceRepository_ExcludedFaceNotInListPending(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	// Create a pending face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusPending,
	}))

	// Create an excluded face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	pending, err := faceRepo.ListPending(10)
	require.NoError(t, err)
	assert.Len(t, pending, 1, "excluded face should not appear in ListPending")
	assert.NotEqual(t, model.FaceClusterStatusExcluded, pending[0].ClusterStatus)
}

func TestFaceRepository_ExcludedFaceNotInListAssigned(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	pid := uint(1)
	// Normal assigned face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}))

	// Excluded face (still has person_id temporarily, but cluster_status=excluded)
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	assigned, err := faceRepo.ListAssigned()
	require.NoError(t, err)
	assert.Len(t, assigned, 1, "excluded face should not appear in ListAssigned")
}

func TestFaceRepository_ExcludedFaceNotInListAssignedPersonIDs(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	pid := uint(1)
	// Excluded face with person_id
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	ids, err := faceRepo.ListAssignedPersonIDs()
	require.NoError(t, err)
	assert.Len(t, ids, 0, "excluded face should not contribute to assigned person IDs")
}

func TestFaceRepository_ExcludedFaceNotInListProfileFaces(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	pid := uint(1)
	// Normal face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}))

	// Excluded face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       2,
		PersonID:      &pid,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	profileFaces, err := faceRepo.ListProfileFaces(pid)
	require.NoError(t, err)
	assert.Len(t, profileFaces, 1, "excluded face should not appear in ListProfileFaces")
	assert.NotEqual(t, model.FaceClusterStatusExcluded, profileFaces[0].ClusterStatus)
}

func TestFaceRepository_ExcludedFaceNotInListByPersonIDPaginated(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)

	pid := uint(1)
	// Normal face
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &pid,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
	}))

	// Excluded face with same person_id
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       2,
		PersonID:      &pid,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
	}))

	faces, total, err := faceRepo.ListByPersonIDPaginated(pid, 1, 10)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "excluded face should not be counted")
	assert.Len(t, faces, 1)
}

// TestFaceRepository_ListByPersonIDCursor covers keyset pagination for person faces:
// quality_score DESC, id ASC ordering, stable tiebreaker, excluded filtering, has_more.
func TestFaceRepository_ListByPersonIDCursor(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	faceRepo := NewFaceRepository(db)

	person := &model.Person{Name: "Cursor", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	// Create photos (needed for face FK)
	var photoIDs []uint
	for i := 0; i < 5; i++ {
		p := &model.Photo{
			FilePath: fmt.Sprintf("/fc_%d.jpg", i),
			FileName: fmt.Sprintf("fc_%d.jpg", i),
			FileSize: 1,
			FileHash: fmt.Sprintf("fh_%d", i),
		}
		require.NoError(t, db.Create(p).Error)
		photoIDs = append(photoIDs, p.ID)
	}

	// Create faces with varying quality scores
	// Sort: quality_score DESC, id ASC
	// f1: 0.95, f2: 0.90, f3: 0.90, f4: 0.80, f5: excluded
	faces := []*model.Face{
		{PhotoID: photoIDs[0], PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.95, ClusterStatus: model.FaceClusterStatusAssigned},
		{PhotoID: photoIDs[1], PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.90, ClusterStatus: model.FaceClusterStatusAssigned},
		{PhotoID: photoIDs[2], PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.90, ClusterStatus: model.FaceClusterStatusAssigned},
		{PhotoID: photoIDs[3], PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.80, ClusterStatus: model.FaceClusterStatusAssigned},
		{PhotoID: photoIDs[4], PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.99, ClusterStatus: model.FaceClusterStatusExcluded},
	}
	for _, f := range faces {
		require.NoError(t, faceRepo.Create(f))
	}

	// Page 1: limit=2 → f1 (0.95), f2 (0.90, lower id)
	items, hasMore, nextCursor, err := faceRepo.ListByPersonIDCursor(person.ID, nil, 2)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.True(t, hasMore)
	require.NotNil(t, nextCursor)
	assert.Equal(t, faces[0].ID, items[0].ID)
	assert.Equal(t, faces[1].ID, items[1].ID)
	assert.Equal(t, faces[1].ID, nextCursor.ID)
	assert.Equal(t, 0.90, nextCursor.QualityScore)

	// Page 2: cursor from f2 → f3 (0.90, higher id), f4 (0.80)
	items, hasMore, nextCursor, err = faceRepo.ListByPersonIDCursor(person.ID, nextCursor, 2)
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.False(t, hasMore) // only 4 non-excluded faces total, 2 per page = 2 pages
	assert.Nil(t, nextCursor)
	assert.Equal(t, faces[2].ID, items[0].ID)
	assert.Equal(t, faces[3].ID, items[1].ID)
}

// TestFaceRepository_ListByPersonIDCursor_Empty verifies empty result set.
func TestFaceRepository_ListByPersonIDCursor_Empty(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	faceRepo := NewFaceRepository(db)
	person := &model.Person{Name: "Empty", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	items, hasMore, nextCursor, err := faceRepo.ListByPersonIDCursor(person.ID, nil, 50)
	require.NoError(t, err)
	assert.Empty(t, items)
	assert.False(t, hasMore)
	assert.Nil(t, nextCursor)
}

// TestFaceRepository_ListByPersonIDCursor_ExcludedFiltered verifies that excluded faces
// are not returned in cursor mode, same as paginated mode.
func TestFaceRepository_ListByPersonIDCursor_ExcludedFiltered(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	faceRepo := NewFaceRepository(db)
	person := &model.Person{Name: "Excl", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	p := &model.Photo{FilePath: "/ex.jpg", FileName: "ex.jpg", FileSize: 1, FileHash: "hex"}
	require.NoError(t, db.Create(p).Error)

	assignedFace := &model.Face{PhotoID: p.ID, PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.8, ClusterStatus: model.FaceClusterStatusAssigned}
	require.NoError(t, faceRepo.Create(assignedFace))

	excludedFace := &model.Face{PhotoID: p.ID, PersonID: &person.ID, Confidence: 0.9, QualityScore: 0.99, ClusterStatus: model.FaceClusterStatusExcluded}
	require.NoError(t, faceRepo.Create(excludedFace))

	items, hasMore, _, err := faceRepo.ListByPersonIDCursor(person.ID, nil, 50)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.False(t, hasMore)
	assert.Equal(t, assignedFace.ID, items[0].ID)
}

func TestFaceRepository_ExcludedFaceNotInListPrototypeEmbeddings(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)
	faceRepo := NewFaceRepository(db)
	personRepo := NewPersonRepository(db)

	p := &model.Person{Category: model.PersonCategoryFriend}
	require.NoError(t, personRepo.Create(p))

	// Normal face with embedding
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       1,
		PersonID:      &p.ID,
		BBoxX:         0.1, BBoxY: 0.1, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusAssigned,
		Embedding:     []byte{1, 2, 3, 4},
	}))

	// Excluded face with embedding
	require.NoError(t, faceRepo.Create(&model.Face{
		PhotoID:       2,
		PersonID:      &p.ID,
		BBoxX:         0.3, BBoxY: 0.3, BBoxWidth: 0.2, BBoxHeight: 0.2,
		ClusterStatus: model.FaceClusterStatusExcluded,
		Embedding:     []byte{5, 6, 7, 8},
	}))

	protos, err := faceRepo.ListPrototypeEmbeddings([]uint{p.ID}, 5)
	require.NoError(t, err)
	assert.Len(t, protos, 1, "excluded face should not appear in prototype embeddings")
}

// explainPersonFaceCursor runs EXPLAIN QUERY PLAN for the person-face cursor query
// (first page or a cursor-continued page) and returns the concatenated plan text.
// selectCols mirrors ListByPersonIDCursor's projection so the optimizer sees the same shape.
func explainPersonFaceCursor(t *testing.T, db *gorm.DB, personID uint, cursor *PersonFaceCursor, limit int) string {
	t.Helper()
	selectCols := "id, created_at, updated_at, photo_id, person_id, b_box_x, b_box_y, b_box_width, b_box_height, confidence, quality_score, thumbnail_path, cluster_status, cluster_score, clustered_at, manual_locked, manual_lock_reason, manual_locked_at, recluster_generation, retry_count"

	query := fmt.Sprintf(
		"EXPLAIN QUERY PLAN SELECT %s FROM faces WHERE person_id = ? AND cluster_status != ?",
		selectCols,
	)
	args := []interface{}{personID, model.FaceClusterStatusExcluded}
	if cursor != nil {
		query += " AND (quality_score < ? OR (quality_score = ? AND id > ?))"
		args = append(args, cursor.QualityScore, cursor.QualityScore, cursor.ID)
	}
	query += fmt.Sprintf(" ORDER BY quality_score DESC, id ASC LIMIT %d", limit+1)

	rows, err := db.Raw(query, args...).Rows()
	require.NoError(t, err)
	defer rows.Close()

	var plans []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		require.NoError(t, rows.Scan(&id, &parent, &notused, &detail))
		plans = append(plans, detail)
	}
	return strings.Join(plans, "\n")
}

// TestPersonFaceCursorIndex_HitFirstPageAndCursorPage 校验人物人脸 cursor 分页查询
// 在第一页和带 cursor 的后续页都命中专用 partial index idx_faces_person_quality_cursor，
// 且不产生临时排序树 (USE TEMP B-TREE)。这是首屏慢请求修复的硬门槛。
//
// 索引由 database.migratePersonFaceCursorIndex 在 AutoMigrate 阶段创建（其创建行为由
// database 包的迁移测试覆盖）。repository 测试用的 setupTestDB 只跑模型 AutoMigrate，
// 不执行自定义迁移函数，因此这里显式建出与线上等价的 partial index，再断言查询计划命中。
func TestPersonFaceCursorIndex_HitFirstPageAndCursorPage(t *testing.T) {
	db := setupTestDB(t)
	defer teardownTestDB(db)

	// 与 migratePersonFaceCursorIndex 创建的索引等价。
	require.NoError(t, db.Exec(`CREATE INDEX IF NOT EXISTS idx_faces_person_quality_cursor
		ON faces(person_id, quality_score DESC, id ASC)
		WHERE cluster_status != 'excluded'`).Error)

	faceRepo := NewFaceRepository(db)

	person := &model.Person{Name: "Explain", Category: model.PersonCategoryFamily}
	require.NoError(t, db.Create(person).Error)

	var photoIDs []uint
	for i := 0; i < 5; i++ {
		p := &model.Photo{
			FilePath: fmt.Sprintf("/ep_%d.jpg", i),
			FileName: fmt.Sprintf("ep_%d.jpg", i),
			FileSize: 1,
			FileHash: fmt.Sprintf("eh_%d", i),
		}
		require.NoError(t, db.Create(p).Error)
		photoIDs = append(photoIDs, p.ID)
	}

	// 写入足够多的人脸触发索引选择，并包含一个 excluded（不应进索引）。
	for i := 0; i < 5; i++ {
		status := model.FaceClusterStatusAssigned
		score := 0.5 + float64(i)*0.05
		if i == 4 {
			status = model.FaceClusterStatusExcluded
			score = 0.99
		}
		require.NoError(t, faceRepo.Create(&model.Face{
			PhotoID:       photoIDs[i],
			PersonID:      &person.ID,
			Confidence:    0.9,
			QualityScore:  score,
			ClusterStatus: status,
		}))
	}

	// 第一页：无 cursor。
	planFirst := explainPersonFaceCursor(t, db, person.ID, nil, 50)
	assert.Contains(t, planFirst, "idx_faces_person_quality_cursor",
		"first page should use idx_faces_person_quality_cursor, got: %s", planFirst)
	assert.NotContains(t, planFirst, "USE TEMP B-TREE",
		"first page must not create a temp b-tree for order by, got: %s", planFirst)

	// 先取真实 cursor，再用它跑后续页的 EXPLAIN。
	items, _, nextCursor, err := faceRepo.ListByPersonIDCursor(person.ID, nil, 2)
	require.NoError(t, err)
	require.NotNil(t, nextCursor)
	require.Len(t, items, 2)

	planCursor := explainPersonFaceCursor(t, db, person.ID, nextCursor, 50)
	assert.Contains(t, planCursor, "idx_faces_person_quality_cursor",
		"cursor page should use idx_faces_person_quality_cursor, got: %s", planCursor)
	assert.NotContains(t, planCursor, "USE TEMP B-TREE",
		"cursor page must not create a temp b-tree for order by, got: %s", planCursor)
}
