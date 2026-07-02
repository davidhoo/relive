package repository

import (
	"strconv"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newIdentityDecisionRepo(t *testing.T) (*peopleIdentityDecisionRepository, *gorm.DB) {
	t.Helper()
	db := setupTestDB(t)
	return NewPeopleIdentityDecisionRepository(db).(*peopleIdentityDecisionRepository), db
}

func sampleDecision(key, hash string) *model.PeopleIdentityDecision {
	return &model.PeopleIdentityDecision{
		Mode:                      model.PeopleIdentityModeShadow,
		ComponentHash:             hash,
		ComponentSize:             3,
		ComponentFaceIDs:          "1,2,3",
		ComponentFaceIDsTruncated: false,
		DecisionKey:               key,
		Decision:                  "agree",
		AlgorithmVersion:          "identity-profile-v1",
		IndexGeneration:           1,
	}
}

func TestPeopleIdentityDecisionRepository_CreateNormal(t *testing.T) {
	repo, db := newIdentityDecisionRepo(t)
	defer teardownTestDB(db)

	d := sampleDecision("key-1", "hash-1")
	created, err := repo.CreateIgnore(d)
	require.NoError(t, err)
	assert.True(t, created)
	assert.NotZero(t, d.ID)

	var got model.PeopleIdentityDecision
	require.NoError(t, db.First(&got, d.ID).Error)
	assert.Equal(t, "agree", got.Decision)
	assert.Equal(t, "hash-1", got.ComponentHash)
}

func TestPeopleIdentityDecisionRepository_DuplicateDecisionKeyIgnored(t *testing.T) {
	repo, db := newIdentityDecisionRepo(t)
	defer teardownTestDB(db)

	d1 := sampleDecision("dup-key", "hash-a")
	created, err := repo.CreateIgnore(d1)
	require.NoError(t, err)
	assert.True(t, created)

	// 相同 DecisionKey 重试：created=false, err=nil，不产生第二条
	d2 := sampleDecision("dup-key", "hash-a")
	d2.ComponentSize = 99
	created2, err2 := repo.CreateIgnore(d2)
	require.NoError(t, err2)
	assert.False(t, created2)

	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Where("decision_key = ?", "dup-key").Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestPeopleIdentityDecisionRepository_DifferentKeysCoexist(t *testing.T) {
	repo, db := newIdentityDecisionRepo(t)
	defer teardownTestDB(db)

	c1, err := repo.CreateIgnore(sampleDecision("key-a", "hash-a"))
	require.NoError(t, err)
	assert.True(t, c1)

	c2, err := repo.CreateIgnore(sampleDecision("key-b", "hash-b"))
	require.NoError(t, err)
	assert.True(t, c2)

	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Count(&count).Error)
	assert.Equal(t, int64(2), count)
}

func TestPeopleIdentityDecisionRepository_CreateIgnoreNil(t *testing.T) {
	repo, db := newIdentityDecisionRepo(t)
	defer teardownTestDB(db)

	created, err := repo.CreateIgnore(nil)
	require.NoError(t, err)
	assert.False(t, created)
}

func TestPeopleIdentityDecisionRepository_ListRecentOrderAndLimit(t *testing.T) {
	repo, db := newIdentityDecisionRepo(t)
	defer teardownTestDB(db)

	// 插入 3 条，控制 created_at 顺序
	base := time.Now().UTC().Add(-time.Hour)
	for i, key := range []string{"k1", "k2", "k3"} {
		d := sampleDecision(key, "h"+key)
		d.CreatedAt = base.Add(time.Duration(i) * time.Minute)
		d.ComponentSize = i + 1
		require.NoError(t, db.Create(d).Error)
	}

	// limit<=0 → 空
	empty, err := repo.ListRecent(0)
	require.NoError(t, err)
	assert.Empty(t, empty)

	// limit=2 → 最近两条按 created_at DESC
	recent, err := repo.ListRecent(2)
	require.NoError(t, err)
	assert.Len(t, recent, 2)
	assert.Equal(t, "k3", recent[0].DecisionKey)
	assert.Equal(t, "k2", recent[1].DecisionKey)

	// limit 超过 200 截断为 200（不报错）
	big, err := repo.ListRecent(9999)
	require.NoError(t, err)
	assert.Len(t, big, 3)
}

func TestPeopleIdentityDecisionRepository_ListIDsBefore(t *testing.T) {
	repo, db := newIdentityDecisionRepo(t)
	defer teardownTestDB(db)

	now := time.Now().UTC()
	old := now.Add(-2 * time.Hour)
	cutoff := now.Add(-1 * time.Hour)

	// 2 条过期 + 1 条保留
	for i, key := range []string{"old1", "old2", "new1"} {
		d := sampleDecision(key, "h"+key)
		if i < 2 {
			d.CreatedAt = old.Add(time.Duration(i) * time.Minute)
		} else {
			d.CreatedAt = now
		}
		require.NoError(t, db.Create(d).Error)
	}

	// limit<=0 → nil
	none, err := repo.ListIDsBefore(cutoff, 0)
	require.NoError(t, err)
	assert.Nil(t, none)

	// limit=10 → 返回 2 个过期 ID，按 created_at ASC
	ids, err := repo.ListIDsBefore(cutoff, 10)
	require.NoError(t, err)
	assert.Len(t, ids, 2)

	// limit=1 → 只返回最早的 1 个
	one, err := repo.ListIDsBefore(cutoff, 1)
	require.NoError(t, err)
	assert.Len(t, one, 1)
	assert.Equal(t, ids[0], one[0])
}

func TestPeopleIdentityDecisionRepository_DeleteByIDs(t *testing.T) {
	repo, db := newIdentityDecisionRepo(t)
	defer teardownTestDB(db)

	// 插入 5 条
	ids := make([]uint, 0, 5)
	for i := 0; i < 5; i++ {
		d := sampleDecision("k"+strconv.Itoa(i), "h"+strconv.Itoa(i))
		require.NoError(t, db.Create(d).Error)
		ids = append(ids, d.ID)
	}

	// 空输入 → 0
	n, err := repo.DeleteByIDs(nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	// 去重 + 过滤 0：传入重复与 0，仍只删 2 条
	dup := []uint{ids[0], ids[0], 0, ids[1], 0}
	n, err = repo.DeleteByIDs(dup)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Count(&count).Error)
	assert.Equal(t, int64(3), count)
}

func TestPeopleIdentityDecisionRepository_DeleteByIDsChunking(t *testing.T) {
	repo, db := newIdentityDecisionRepo(t)
	defer teardownTestDB(db)

	// 插入 sqliteVarLimit+10 条，验证分块删除不漏不重
	total := sqliteVarLimit + 10
	allIDs := make([]uint, 0, total)
	for i := 0; i < total; i++ {
		d := sampleDecision("ck"+strconv.Itoa(i), "ch"+strconv.Itoa(i))
		require.NoError(t, db.Create(d).Error)
		allIDs = append(allIDs, d.ID)
	}

	n, err := repo.DeleteByIDs(allIDs)
	require.NoError(t, err)
	assert.Equal(t, int64(total), n)

	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestPeopleIdentityDecisionRepository_NoSensitiveColumns(t *testing.T) {
	// people_identity_decisions 表结构不应包含 embedding/路径/人名字段。
	// 通过 PRAGMA table_info 检查列名集合。
	repo, db := newIdentityDecisionRepo(t)
	defer teardownTestDB(db)
	_ = repo

	type col struct {
		Name string `gorm:"column:name"`
	}
	var cols []col
	require.NoError(t, db.Raw("PRAGMA table_info(people_identity_decisions)").Scan(&cols).Error)

	forbidden := []string{"embedding", "file_path", "thumbnail", "path", "person_name", "name", "api_key"}
	names := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		names[c.Name] = struct{}{}
	}
	for _, f := range forbidden {
		_, ok := names[f]
		assert.False(t, ok, "people_identity_decisions must not have column %q", f)
	}
}
