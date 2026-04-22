package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
)

// --- seasonKeywords ---

func TestSeasonKeywords_Spring(t *testing.T) {
	for _, m := range []time.Month{time.March, time.April, time.May} {
		kws := seasonKeywords(m)
		assert.Contains(t, kws, "春")
		assert.Contains(t, kws, "spring")
	}
}

func TestSeasonKeywords_Summer(t *testing.T) {
	for _, m := range []time.Month{time.June, time.July, time.August} {
		kws := seasonKeywords(m)
		assert.Contains(t, kws, "夏")
		assert.Contains(t, kws, "summer")
	}
}

func TestSeasonKeywords_Autumn(t *testing.T) {
	for _, m := range []time.Month{time.September, time.October, time.November} {
		kws := seasonKeywords(m)
		assert.Contains(t, kws, "秋")
		assert.Contains(t, kws, "autumn")
	}
}

func TestSeasonKeywords_Winter(t *testing.T) {
	for _, m := range []time.Month{time.December, time.January, time.February} {
		kws := seasonKeywords(m)
		assert.Contains(t, kws, "冬")
		assert.Contains(t, kws, "winter")
	}
}

// --- matchesCurrentSeason ---

func TestMatchesCurrentSeason_TagMatch(t *testing.T) {
	p := &model.Photo{Tags: "春游,花"}
	date := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC) // spring
	assert.True(t, matchesCurrentSeason(p, date))
}

func TestMatchesCurrentSeason_CaptionMatch(t *testing.T) {
	p := &model.Photo{Caption: "海边的夏日"}
	date := time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC) // summer
	assert.True(t, matchesCurrentSeason(p, date))
}

func TestMatchesCurrentSeason_NoMatch(t *testing.T) {
	p := &model.Photo{Tags: "birthday,cake"}
	date := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) // winter
	// "birthday,cake" contains no winter keywords
	assert.False(t, matchesCurrentSeason(p, date))
}

func TestMatchesCurrentSeason_CaseInsensitive(t *testing.T) {
	p := &model.Photo{Tags: "SUMMER beach"}
	date := time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)
	assert.True(t, matchesCurrentSeason(p, date))
}

// testCurationCfg returns a minimal DisplayStrategyConfig with the default people bonus.
func testCurationCfg() model.DisplayStrategyConfig {
	return model.DisplayStrategyConfig{CurationPeopleBonus: 20}
}

// --- personCategoryBonus ---

func TestPersonCategoryBonus_Family(t *testing.T) {
	cfg := testCurationCfg()
	p := &model.Photo{TopPersonCategory: model.PersonCategoryFamily}
	assert.Equal(t, cfg.CurationPeopleBonus*3, personCategoryBonus(p, cfg))
}

func TestPersonCategoryBonus_Friend(t *testing.T) {
	cfg := testCurationCfg()
	p := &model.Photo{TopPersonCategory: model.PersonCategoryFriend}
	assert.Equal(t, cfg.CurationPeopleBonus*2, personCategoryBonus(p, cfg))
}

func TestPersonCategoryBonus_Acquaintance(t *testing.T) {
	cfg := testCurationCfg()
	p := &model.Photo{TopPersonCategory: model.PersonCategoryAcquaintance}
	assert.Equal(t, cfg.CurationPeopleBonus*0.5, personCategoryBonus(p, cfg))
}

func TestPersonCategoryBonus_Stranger(t *testing.T) {
	cfg := testCurationCfg()
	p := &model.Photo{TopPersonCategory: "stranger"}
	assert.Equal(t, 0.0, personCategoryBonus(p, cfg))
}

func TestPersonCategoryBonus_NilPhoto(t *testing.T) {
	cfg := testCurationCfg()
	assert.Equal(t, 0.0, personCategoryBonus(nil, cfg))
}

func TestPersonCategoryBonus_EmptyCategory(t *testing.T) {
	cfg := testCurationCfg()
	p := &model.Photo{}
	assert.Equal(t, 0.0, personCategoryBonus(p, cfg))
}

// --- peopleSpotlightChannelBonus ---

func TestPeopleSpotlightChannelBonus_Family(t *testing.T) {
	cfg := testCurationCfg()
	p := &model.Photo{TopPersonCategory: model.PersonCategoryFamily}
	assert.Equal(t, cfg.CurationPeopleBonus*3, peopleSpotlightChannelBonus(p, cfg))
}

func TestPeopleSpotlightChannelBonus_Acquaintance(t *testing.T) {
	cfg := testCurationCfg()
	p := &model.Photo{TopPersonCategory: model.PersonCategoryAcquaintance}
	// Spotlight gives 1x for acquaintance (vs 0.5x in generic)
	assert.Equal(t, cfg.CurationPeopleBonus*1, peopleSpotlightChannelBonus(p, cfg))
}

// --- hasTimeTunnelConflict ---

func TestHasTimeTunnelConflict_NoSelected(t *testing.T) {
	now := time.Now()
	candidate := &curationCandidate{photo: &model.Photo{TakenAt: &now}}
	assert.False(t, hasTimeTunnelConflict(candidate, nil))
}

func TestHasTimeTunnelConflict_FarApart(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	t1 := base
	t2 := base.Add(48 * time.Hour)
	candidate := &curationCandidate{photo: &model.Photo{TakenAt: &t1}}
	existing := &curationCandidate{photo: &model.Photo{TakenAt: &t2}}
	assert.False(t, hasTimeTunnelConflict(candidate, []*curationCandidate{existing}))
}

func TestHasTimeTunnelConflict_Within24h(t *testing.T) {
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	t1 := base
	t2 := base.Add(12 * time.Hour) // 12h apart — conflict
	candidate := &curationCandidate{photo: &model.Photo{TakenAt: &t1}}
	existing := &curationCandidate{photo: &model.Photo{TakenAt: &t2}}
	assert.True(t, hasTimeTunnelConflict(candidate, []*curationCandidate{existing}))
}

func TestHasTimeTunnelConflict_NilPhoto(t *testing.T) {
	candidate := &curationCandidate{photo: nil}
	assert.False(t, hasTimeTunnelConflict(candidate, nil))
}

// --- arrangeCuratedSequence ---

func TestArrangeCuratedSequence_LessThanThree(t *testing.T) {
	p1 := &model.Photo{ID: 1, BeautyScore: 90}
	result := arrangeCuratedSequence([]*model.Photo{p1})
	assert.Equal(t, []*model.Photo{p1}, result)
}

func TestArrangeCuratedSequence_FirstIsHighestBeauty(t *testing.T) {
	p1 := &model.Photo{ID: 1, BeautyScore: 50, MemoryScore: 60}
	p2 := &model.Photo{ID: 2, BeautyScore: 90, MemoryScore: 40}
	p3 := &model.Photo{ID: 3, BeautyScore: 30, MemoryScore: 80}
	result := arrangeCuratedSequence([]*model.Photo{p1, p2, p3})
	assert.Equal(t, uint(2), result[0].ID, "first should be highest beauty")
	assert.Equal(t, uint(3), result[len(result)-1].ID, "last should be highest memory")
}

// --- selectCuratedPhotos ---

func TestSelectCuratedPhotos_Empty(t *testing.T) {
	assert.Nil(t, selectCuratedPhotos(nil, 5))
	assert.Nil(t, selectCuratedPhotos([]curationCandidate{}, 5))
}

func TestSelectCuratedPhotos_ZeroLimit(t *testing.T) {
	now := time.Now()
	p := &model.Photo{ID: 1, TakenAt: &now}
	candidates := []curationCandidate{{photo: p, channel: "peak_memory", adjScore: 80}}
	assert.Nil(t, selectCuratedPhotos(candidates, 0))
}

func TestSelectCuratedPhotos_RespectsLimit(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	var candidates []curationCandidate
	for i := 0; i < 10; i++ {
		t := base.Add(time.Duration(i*48) * time.Hour)
		p := &model.Photo{ID: uint(i + 1), TakenAt: &t}
		candidates = append(candidates, curationCandidate{
			photo:    p,
			channel:  "peak_memory",
			adjScore: float64(100 - i),
		})
	}
	result := selectCuratedPhotos(candidates, 5)
	assert.Len(t, result, 5)
}

func TestSelectCuratedPhotos_OnePerChannel_Round1(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	makePh := func(id uint, day int) *model.Photo {
		tt := base.Add(time.Duration(day*48) * time.Hour)
		return &model.Photo{ID: id, TakenAt: &tt}
	}
	candidates := []curationCandidate{
		{photo: makePh(1, 0), channel: "time_tunnel", adjScore: 90},
		{photo: makePh(2, 2), channel: "peak_memory", adjScore: 80},
		{photo: makePh(3, 4), channel: "hidden_gem", adjScore: 70},
	}
	// Limit=3 → one per channel
	result := selectCuratedPhotos(candidates, 3)
	assert.Len(t, result, 3)
}
