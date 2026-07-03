package service

import (
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// ---- 测试桩 ----

type fakeIdentityDecisionRepo struct {
	mu        sync.Mutex
	calls     []*model.PeopleIdentityDecision
	createErr error
}

func (f *fakeIdentityDecisionRepo) CreateIgnore(d *model.PeopleIdentityDecision) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, d)
	if f.createErr != nil {
		return false, f.createErr
	}
	return true, nil
}

func (f *fakeIdentityDecisionRepo) ListRecent(int) ([]*model.PeopleIdentityDecision, error) {
	return nil, nil
}

func (f *fakeIdentityDecisionRepo) ListIDsBefore(time.Time, int) ([]uint, error) { return nil, nil }

func (f *fakeIdentityDecisionRepo) DeleteByIDs([]uint) (int64, error) { return 0, nil }

func (f *fakeIdentityDecisionRepo) GetSummarySince(time.Time) (*model.IdentityDecisionSummary, error) {
	return &model.IdentityDecisionSummary{}, nil
}

func (f *fakeIdentityDecisionRepo) lastCall() *model.PeopleIdentityDecision {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakeIdentityDecisionRepo) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// ---- DB 辅助 ----

func setupTelemetryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.PeopleIdentityDecision{}))
	return db
}

// findSampleInAndOut 返回两个单 Face ID：第一个采样命中、第一个采样未命中。
func findSampleInAndOut(t *testing.T) (uint, uint) {
	t.Helper()
	var inID, outID uint
	for n := uint(1); n < 100000; n++ {
		h := computeComponentHashBytes([]uint{n})
		if sampleIdentityDecision(h) {
			if inID == 0 {
				inID = n
			}
		} else {
			if outID == 0 {
				outID = n
			}
		}
		if inID != 0 && outID != 0 {
			return inID, outID
		}
	}
	t.Fatal("could not find in/out sample face IDs")
	return 0, 0
}

// ---- 测试用例 ----

func TestIdentityTelemetry_LegacyModeNoop(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeLegacy,
		ComponentFaceIDs: []uint{1, 2, 3},
		LegacyMatched:    true,
		Profile:          IdentityProfileMatch{Available: true, PersonID: 5},
	})

	assert.Equal(t, 0, repo.callCount(), "legacy mode must not access repository")
}

func TestIdentityTelemetry_LegacyMissProfileHitRecorded(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeShadow,
		ComponentFaceIDs: []uint{1, 2},
		LegacyMatched:    false,
		Profile:          IdentityProfileMatch{Available: true, PersonID: 7, Score: 0.9, AutoEligible: true},
		AlgorithmVersion: "identity-profile-v1",
	})

	require.Equal(t, 1, repo.callCount())
	d := repo.lastCall()
	assert.Equal(t, identityDecisionLegacyMissProfileHit, d.Decision)
	assert.Equal(t, 2, d.ComponentSize)
}

func TestIdentityTelemetry_LegacyMissProfileMissRecorded(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeShadow,
		ComponentFaceIDs: []uint{1},
		LegacyMatched:    false,
		Profile:          IdentityProfileMatch{Available: true, PersonID: 0},
	})

	require.Equal(t, 1, repo.callCount())
	assert.Equal(t, identityDecisionLegacyMissProfileMiss, repo.lastCall().Decision)
}

func TestIdentityTelemetry_AgreeSampledOut(t *testing.T) {
	_, outID := findSampleInAndOut(t)
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:                 model.PeopleIdentityModeShadow,
		ComponentFaceIDs:     []uint{outID},
		LegacyMatched:        true,
		LegacyTargetPersonID: 5,
		Profile:              IdentityProfileMatch{Available: true, PersonID: 5, AutoEligible: true},
	})

	assert.Equal(t, 0, repo.callCount(), "agree sampled out must not be recorded")
}

func TestIdentityTelemetry_AgreeSampledIn(t *testing.T) {
	inID, _ := findSampleInAndOut(t)
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:                 model.PeopleIdentityModeShadow,
		ComponentFaceIDs:     []uint{inID},
		LegacyMatched:        true,
		LegacyTargetPersonID: 5,
		Profile:              IdentityProfileMatch{Available: true, PersonID: 5, AutoEligible: true},
	})

	require.Equal(t, 1, repo.callCount())
	assert.Equal(t, identityDecisionAgree, repo.lastCall().Decision)
}

func TestIdentityTelemetry_DisagreeAlwaysRecorded(t *testing.T) {
	// disagree 不受采样影响，即使 face ID 落在采样外也必须记录
	_, outID := findSampleInAndOut(t)
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:                 model.PeopleIdentityModeShadow,
		ComponentFaceIDs:     []uint{outID},
		LegacyMatched:        true,
		LegacyTargetPersonID: 1,
		Profile:              IdentityProfileMatch{Available: true, PersonID: 2, AutoEligible: true},
	})

	require.Equal(t, 1, repo.callCount())
	d := repo.lastCall()
	assert.Equal(t, identityDecisionDisagree, d.Decision)
}

func TestIdentityTelemetry_DisagreeWithBlockReasonRecorded(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:                 model.PeopleIdentityModeShadow,
		ComponentFaceIDs:     []uint{1},
		LegacyMatched:        true,
		LegacyTargetPersonID: 1,
		Profile: IdentityProfileMatch{
			Available:    true,
			PersonID:     2,
			AutoEligible: false,
			BlockReason:  blockMarginTooSmall,
		},
	})

	d := repo.lastCall()
	assert.Equal(t, identityDecisionDisagree, d.Decision)
	assert.Equal(t, blockMarginTooSmall, d.Reason)
}

func TestIdentityTelemetry_ProfileUnavailableAlwaysRecorded(t *testing.T) {
	_, outID := findSampleInAndOut(t)
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeShadow,
		ComponentFaceIDs: []uint{outID},
		LegacyMatched:    true,
		Profile:          IdentityProfileMatch{Available: false, BlockReason: blockIndexUnavailable},
	})

	require.Equal(t, 1, repo.callCount())
	d := repo.lastCall()
	assert.Equal(t, identityDecisionProfileUnavailable, d.Decision)
	assert.Equal(t, blockIndexUnavailable, d.Reason)
}

func TestIdentityTelemetry_ProfileBlockedRecorded(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:                 model.PeopleIdentityModeShadow,
		ComponentFaceIDs:     []uint{1},
		LegacyMatched:        true,
		LegacyTargetPersonID: 5,
		Profile: IdentityProfileMatch{
			Available:    true,
			PersonID:     5,
			AutoEligible: false,
			BlockReason:  blockUnstableCenter,
		},
	})

	d := repo.lastCall()
	assert.Equal(t, identityDecisionProfileBlocked, d.Decision)
	assert.Equal(t, blockUnstableCenter, d.Reason)
}

func TestIdentityTelemetry_ProfileMissRecorded(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:                 model.PeopleIdentityModeShadow,
		ComponentFaceIDs:     []uint{1},
		LegacyMatched:        true,
		LegacyTargetPersonID: 5,
		Profile:              IdentityProfileMatch{Available: true, PersonID: 0},
	})

	d := repo.lastCall()
	assert.Equal(t, identityDecisionProfileMiss, d.Decision)
}

func TestIdentityTelemetry_OrderIndependentHashAndKey(t *testing.T) {
	db := setupTelemetryDB(t)
	repo := repository.NewPeopleIdentityDecisionRepository(db)
	tel := NewIdentityProfileTelemetry(repo)

	// disagree 不受采样影响，适合验证幂等
	mkInput := func(order []uint) IdentityTelemetryInput {
		return IdentityTelemetryInput{
			Mode:                 model.PeopleIdentityModeShadow,
			ComponentFaceIDs:     order,
			LegacyMatched:        true,
			LegacyTargetPersonID: 1,
			Profile:              IdentityProfileMatch{Available: true, PersonID: 2, AutoEligible: true},
			AlgorithmVersion:     "identity-profile-v1",
		}
	}

	tel.Record(mkInput([]uint{3, 1, 2}))
	tel.Record(mkInput([]uint{2, 1, 3}))

	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "same face ID set in different order must produce same DecisionKey and dedupe")

	var got model.PeopleIdentityDecision
	require.NoError(t, db.First(&got).Error)
	assert.Equal(t, "1,2,3", got.ComponentFaceIDs)
	assert.Equal(t, 3, got.ComponentSize)
}

func TestIdentityTelemetry_FaceIDSortDedupFilterZero(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeShadow,
		ComponentFaceIDs: []uint{3, 0, 1, 2, 1, 3},
		LegacyMatched:    false,
		Profile:          IdentityProfileMatch{Available: true, PersonID: 9},
	})

	d := repo.lastCall()
	assert.Equal(t, "1,2,3", d.ComponentFaceIDs)
	assert.Equal(t, 3, d.ComponentSize)
	assert.False(t, d.ComponentFaceIDsTruncated)
}

func TestIdentityTelemetry_TruncatesDisplayButHashCoversAll(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	ids := make([]uint, 600)
	for i := range ids {
		ids[i] = uint(i + 1)
	}
	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeShadow,
		ComponentFaceIDs: ids,
		LegacyMatched:    false,
		Profile:          IdentityProfileMatch{Available: true, PersonID: 9},
	})

	d := repo.lastCall()
	assert.Equal(t, 600, d.ComponentSize)
	assert.True(t, d.ComponentFaceIDsTruncated)
	// 展示字段只含 512 个
	parts := strings.Split(d.ComponentFaceIDs, ",")
	assert.Equal(t, 512, len(parts))
	// hash 覆盖全部 600 个 ID，应不同于仅前 512 的 hash
	hashAll := computeComponentHashBytes(ids)
	hashFirst512 := computeComponentHashBytes(ids[:512])
	assert.Equal(t, hashAll, computeComponentHashBytes(dedupSortUint(ids)))
	assert.NotEqual(t, hashAll, hashFirst512)
}

func TestIdentityTelemetry_CenterIDSortDedup(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeShadow,
		ComponentFaceIDs: []uint{1},
		LegacyMatched:    false,
		Profile:          IdentityProfileMatch{Available: true, PersonID: 9, CenterIDs: []uint{9, 3, 9, 3, 7}},
	})

	d := repo.lastCall()
	assert.Equal(t, "3,7,9", d.CenterIDs)
}

func TestIdentityTelemetry_NaNInfScoresNulled(t *testing.T) {
	db := setupTelemetryDB(t)
	repo := repository.NewPeopleIdentityDecisionRepository(db)
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:                 model.PeopleIdentityModeShadow,
		ComponentFaceIDs:     []uint{1},
		LegacyMatched:        true,
		LegacyTargetPersonID: 1,
		LegacyScore:          math.NaN(),
		Profile: IdentityProfileMatch{
			Available:    true,
			PersonID:     2,
			Score:        math.Inf(1),
			SecondScore:  math.Inf(-1),
			Margin:       math.NaN(),
			AutoEligible: true,
		},
	})

	var got model.PeopleIdentityDecision
	require.NoError(t, db.First(&got).Error)
	assert.Nil(t, got.LegacyScore)
	assert.Nil(t, got.ProfileBestScore)
	assert.Nil(t, got.ProfileSecondScore)
	assert.Equal(t, float64(0), got.Margin)
}

func TestIdentityTelemetry_PersonIDZeroNulled(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:                 model.PeopleIdentityModeShadow,
		ComponentFaceIDs:     []uint{1},
		LegacyMatched:        false,
		LegacyTargetPersonID: 0,
		Profile:              IdentityProfileMatch{Available: true, PersonID: 0, SecondPersonID: 0},
	})

	d := repo.lastCall()
	assert.Nil(t, d.LegacyTargetPersonID)
	assert.Nil(t, d.ProfileBestPersonID)
	assert.Nil(t, d.ProfileSecondPersonID)
}

func TestIdentityTelemetry_NegativeElapsedClamped(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeShadow,
		ComponentFaceIDs: []uint{1},
		LegacyMatched:    false,
		Profile:          IdentityProfileMatch{Available: true, PersonID: 9},
		Elapsed:          -5 * time.Second,
	})

	assert.Equal(t, 0, repo.lastCall().ElapsedMilliseconds)
}

func TestIdentityTelemetry_UnknownReasonDropped(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeShadow,
		ComponentFaceIDs: []uint{1},
		LegacyMatched:    true,
		Profile:          IdentityProfileMatch{Available: false, BlockReason: "some raw error with /path/to/img and 人名"},
	})

	d := repo.lastCall()
	assert.Equal(t, identityDecisionProfileUnavailable, d.Decision)
	assert.Empty(t, d.Reason, "unknown reason must be dropped to avoid leaking sensitive data")
}

func TestIdentityTelemetry_RepoFailureNoPanicNoReturn(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{createErr: assertNotUsedErr}
	tel := NewIdentityProfileTelemetry(repo)

	assert.NotPanics(t, func() {
		tel.Record(IdentityTelemetryInput{
			Mode:             model.PeopleIdentityModeShadow,
			ComponentFaceIDs: []uint{1},
			LegacyMatched:    false,
			Profile:          IdentityProfileMatch{Available: true, PersonID: 9},
		})
	})
	assert.Equal(t, 1, repo.callCount(), "telemetry must attempt one write even on failure")
}

var assertNotUsedErr = &simpleErr{msg: "db unavailable"}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

func TestIdentityTelemetry_DuplicateRetryNoDuplicate(t *testing.T) {
	db := setupTelemetryDB(t)
	repo := repository.NewPeopleIdentityDecisionRepository(db)
	tel := NewIdentityProfileTelemetry(repo)

	in := IdentityTelemetryInput{
		Mode:                 model.PeopleIdentityModeShadow,
		ComponentFaceIDs:     []uint{1, 2},
		LegacyMatched:        true,
		LegacyTargetPersonID: 5,
		Profile:              IdentityProfileMatch{Available: true, PersonID: 5, AutoEligible: true},
		AlgorithmVersion:     "identity-profile-v1",
	}
	// 采样可能命中也可能不命中；若采样不命中，第二次仍不写。为保证测试确定性，
	// 选用 disagree 决策（永不采样）验证幂等。
	in.Profile.PersonID = 6 // 使其成为 disagree

	tel.Record(in)
	tel.Record(in)
	tel.Record(in)

	var count int64
	require.NoError(t, db.Model(&model.PeopleIdentityDecision{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "identical retries must not duplicate the record")
}

func TestIdentityTelemetry_InputHasNoSensitiveFields(t *testing.T) {
	// IdentityTelemetryInput 结构上禁止 embedding/路径/人名字段。
	typ := reflect.TypeOf(IdentityTelemetryInput{})
	forbidden := map[string]string{
		"Embedding":  "embedding",
		"FilePath":   "file path",
		"Thumbnail":  "thumbnail",
		"Path":       "path",
		"PersonName": "person name",
		"Name":       "name",
		"ApiKey":     "api key",
		"RawError":   "raw error",
	}
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		_, ok := forbidden[name]
		assert.False(t, ok, "IdentityTelemetryInput must not have field %q", name)
	}
}

func TestIdentityTelemetry_NilReceiverSafe(t *testing.T) {
	var tel *identityProfileTelemetry
	assert.NotPanics(t, func() {
		tel.Record(IdentityTelemetryInput{Mode: model.PeopleIdentityModeShadow})
	})
}

func TestIdentityTelemetry_NilRepoSafe(t *testing.T) {
	tel := NewIdentityProfileTelemetry(nil)
	assert.NotPanics(t, func() {
		tel.Record(IdentityTelemetryInput{
			Mode:             model.PeopleIdentityModeShadow,
			ComponentFaceIDs: []uint{1},
			LegacyMatched:    false,
			Profile:          IdentityProfileMatch{Available: true, PersonID: 9},
		})
	})
}

// TestIdentityTelemetry_RescueAppliedAlwaysRecorded 验证 RescueApplied=true 时 Decision 固定为
// rescue_applied，全量记录不采样（即使 agree 采样会命中的 face ID）。
func TestIdentityTelemetry_RescueAppliedAlwaysRecorded(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	// 选一个会被 agree 采样剔除的 face ID，证明 rescue_applied 不采样。
	_, outID := findSampleInAndOut(t)
	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeRescue,
		ComponentFaceIDs: []uint{outID},
		LegacyMatched:    false,
		Profile:          IdentityProfileMatch{Available: true, PersonID: 9, Score: 0.92, AutoEligible: true},
		RescueApplied:    true,
	})

	require.Equal(t, 1, repo.callCount(), "rescue_applied must always be recorded, never sampled")
	d := repo.lastCall()
	assert.Equal(t, identityDecisionRescueApplied, d.Decision)
	assert.Equal(t, model.PeopleIdentityModeRescue, d.Mode)
	assert.Empty(t, d.Reason, "rescue_applied carries no block reason")
}

// TestIdentityTelemetry_RescueAppliedOverridesClassification 验证 RescueApplied 优先级高于
// 其他分类（即使 profile unavailable 也不掩盖 rescue_applied —— 实际 rescue 已应用即代表
// 画像可用且 eligible，此处仅验证字段优先级语义）。
func TestIdentityTelemetry_RescueAppliedOverridesClassification(t *testing.T) {
	repo := &fakeIdentityDecisionRepo{}
	tel := NewIdentityProfileTelemetry(repo)

	tel.Record(IdentityTelemetryInput{
		Mode:             model.PeopleIdentityModeRescue,
		ComponentFaceIDs: []uint{1},
		LegacyMatched:    false,
		Profile:          IdentityProfileMatch{Available: true, PersonID: 9, Score: 0.92, AutoEligible: true},
		RescueApplied:    true,
	})

	d := repo.lastCall()
	assert.Equal(t, identityDecisionRescueApplied, d.Decision)
}
