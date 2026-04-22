package service

import (
	"math"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// --- haversineDistance ---

func TestHaversineDistance_SamePoint(t *testing.T) {
	assert.InDelta(t, 0.0, haversineDistance(31.2, 121.5, 31.2, 121.5), 0.001)
}

func TestHaversineDistance_BeijingToShanghai(t *testing.T) {
	// 北京 (39.9, 116.4) → 上海 (31.2, 121.5)，直线约 1065 km
	dist := haversineDistance(39.9, 116.4, 31.2, 121.5)
	assert.InDelta(t, 1065.0, dist, 30.0)
}

func TestHaversineDistance_Symmetry(t *testing.T) {
	d1 := haversineDistance(39.9, 116.4, 31.2, 121.5)
	d2 := haversineDistance(31.2, 121.5, 39.9, 116.4)
	assert.InDelta(t, d1, d2, 0.001)
}

func TestHaversineDistance_Short(t *testing.T) {
	// 两点间约 1 km
	dist := haversineDistance(31.2000, 121.5000, 31.2090, 121.5000)
	assert.InDelta(t, 1.0, dist, 0.2)
}

// --- mostFrequent ---

func TestMostFrequent_Empty(t *testing.T) {
	assert.Equal(t, "", mostFrequent(map[string]int{}))
}

func TestMostFrequent_Single(t *testing.T) {
	assert.Equal(t, "travel", mostFrequent(map[string]int{"travel": 3}))
}

func TestMostFrequent_ClearWinner(t *testing.T) {
	counts := map[string]int{"travel": 5, "family": 2, "food": 1}
	assert.Equal(t, "travel", mostFrequent(counts))
}

func TestMostFrequent_TieDeterministic(t *testing.T) {
	// 相同次数时按字母序排，靠前的 key 胜出
	counts := map[string]int{"zzz": 3, "aaa": 3}
	got := mostFrequent(counts)
	// 无论调用多少次，结果相同（确定性）
	assert.Equal(t, got, mostFrequent(counts))
	assert.Equal(t, "aaa", got)
}

// --- absDuration ---

func TestAbsDuration_Positive(t *testing.T) {
	assert.Equal(t, 5*time.Hour, absDuration(5*time.Hour))
}

func TestAbsDuration_Negative(t *testing.T) {
	assert.Equal(t, 3*time.Minute, absDuration(-3*time.Minute))
}

func TestAbsDuration_Zero(t *testing.T) {
	assert.Equal(t, time.Duration(0), absDuration(0))
}

// --- clusterPhotos ---

func makePhoto(id uint, takenAt time.Time) *model.Photo {
	return &model.Photo{
		ID:      id,
		TakenAt: &takenAt,
		Status:  model.PhotoStatusActive,
	}
}

func makePhotoGPS(id uint, takenAt time.Time, lat, lon float64) *model.Photo {
	p := makePhoto(id, takenAt)
	p.GPSLatitude = &lat
	p.GPSLongitude = &lon
	return p
}

func newClusteringSvc() *eventClusteringService {
	return &eventClusteringService{
		config: model.DefaultEventClusteringConfig(),
	}
}

func newClusteringSvcWithDB(t *testing.T) *eventClusteringService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.PhotoTag{}))
	return &eventClusteringService{
		db:     db,
		config: model.DefaultEventClusteringConfig(),
	}
}

func TestClusterPhotos_Empty(t *testing.T) {
	svc := newClusteringSvc()
	assert.Nil(t, svc.clusterPhotos(nil))
	assert.Nil(t, svc.clusterPhotos([]*model.Photo{}))
}

func TestClusterPhotos_SinglePhoto(t *testing.T) {
	svc := newClusteringSvc()
	now := time.Now()
	clusters := svc.clusterPhotos([]*model.Photo{makePhoto(1, now)})
	assert.Len(t, clusters, 1)
	assert.Len(t, clusters[0].photos, 1)
}

func TestClusterPhotos_SameEventWithin6Hours(t *testing.T) {
	svc := newClusteringSvc()
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	photos := []*model.Photo{
		makePhoto(1, base),
		makePhoto(2, base.Add(2*time.Hour)),
		makePhoto(3, base.Add(4*time.Hour)),
	}
	clusters := svc.clusterPhotos(photos)
	assert.Len(t, clusters, 1)
	assert.Len(t, clusters[0].photos, 3)
}

func TestClusterPhotos_SplitsAt24Hours(t *testing.T) {
	svc := newClusteringSvc()
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	photos := []*model.Photo{
		makePhoto(1, base),
		makePhoto(2, base.Add(25*time.Hour)), // > 24h 强制切分
	}
	clusters := svc.clusterPhotos(photos)
	assert.Len(t, clusters, 2)
}

func TestClusterPhotos_GPSSplitsInGrayZone(t *testing.T) {
	// 6h~24h 灰色地带，GPS 距离 > 50km 切分
	svc := newClusteringSvc()
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	photos := []*model.Photo{
		makePhotoGPS(1, base, 31.2, 121.5),                // 上海
		makePhotoGPS(2, base.Add(8*time.Hour), 39.9, 116.4), // 北京，~1065km 远
	}
	clusters := svc.clusterPhotos(photos)
	assert.Len(t, clusters, 2, "GPS > 50km 在灰色地带应切分")
}

func TestClusterPhotos_GPSSameCity_NoSplit(t *testing.T) {
	// 6h~24h 灰色地带，GPS 距离 < 50km 不切分
	svc := newClusteringSvc()
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	photos := []*model.Photo{
		makePhotoGPS(1, base, 31.200, 121.500),
		makePhotoGPS(2, base.Add(8*time.Hour), 31.210, 121.510), // ~1.5km
	}
	clusters := svc.clusterPhotos(photos)
	assert.Len(t, clusters, 1)
}

func TestClusterPhotos_GPSSplitsUnder6HoursIfFar(t *testing.T) {
	// < 6h 且 GPS > 50km 也切分
	svc := newClusteringSvc()
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	photos := []*model.Photo{
		makePhotoGPS(1, base, 31.2, 121.5),
		makePhotoGPS(2, base.Add(3*time.Hour), 39.9, 116.4), // 北京
	}
	clusters := svc.clusterPhotos(photos)
	assert.Len(t, clusters, 2)
}

func TestClusterPhotos_MultipleClusters(t *testing.T) {
	svc := newClusteringSvc()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	photos := []*model.Photo{
		makePhoto(1, base),
		makePhoto(2, base.Add(2*time.Hour)),
		makePhoto(3, base.Add(30*time.Hour)), // 新簇
		makePhoto(4, base.Add(31*time.Hour)),
		makePhoto(5, base.Add(60*time.Hour)), // 又一簇
	}
	clusters := svc.clusterPhotos(photos)
	assert.Len(t, clusters, 3)
	assert.Len(t, clusters[0].photos, 2)
	assert.Len(t, clusters[1].photos, 2)
	assert.Len(t, clusters[2].photos, 1)
}

// --- profileEvent ---

func TestProfileEvent_Empty(t *testing.T) {
	svc := newClusteringSvcWithDB(t)
	event := &model.Event{}
	svc.profileEvent(event, nil)
	// Should not panic and leave event unchanged.
	assert.Nil(t, event.CoverPhotoID)
	assert.Equal(t, "", event.PrimaryCategory)
}

func TestProfileEvent_CoverIsHighestBeauty(t *testing.T) {
	svc := newClusteringSvcWithDB(t)
	p1 := &model.Photo{ID: 1, BeautyScore: 70}
	p2 := &model.Photo{ID: 2, BeautyScore: 90}
	p3 := &model.Photo{ID: 3, BeautyScore: 50}
	event := &model.Event{}
	svc.profileEvent(event, []*model.Photo{p1, p2, p3})
	assert.Equal(t, uint(2), *event.CoverPhotoID)
}

func TestProfileEvent_PrimaryCategoryMostFrequent(t *testing.T) {
	svc := newClusteringSvcWithDB(t)
	photos := []*model.Photo{
		{ID: 1, MainCategory: "travel"},
		{ID: 2, MainCategory: "travel"},
		{ID: 3, MainCategory: "family"},
	}
	event := &model.Event{}
	svc.profileEvent(event, photos)
	assert.Equal(t, "travel", event.PrimaryCategory)
}

func TestProfileEvent_GPSAverageCoordinate(t *testing.T) {
	svc := newClusteringSvcWithDB(t)
	lat1, lon1 := 30.0, 120.0
	lat2, lon2 := 32.0, 122.0
	photos := []*model.Photo{
		{ID: 1, GPSLatitude: &lat1, GPSLongitude: &lon1},
		{ID: 2, GPSLatitude: &lat2, GPSLongitude: &lon2},
	}
	event := &model.Event{}
	svc.profileEvent(event, photos)
	assert.NotNil(t, event.GPSLatitude)
	assert.InDelta(t, 31.0, *event.GPSLatitude, 0.001)
	assert.InDelta(t, 121.0, *event.GPSLongitude, 0.001)
}

func TestProfileEvent_EventScoreFormula(t *testing.T) {
	// EventScore = avg(overall_score) * log2(count+1)
	svc := newClusteringSvcWithDB(t)
	photos := []*model.Photo{
		{ID: 1, OverallScore: 80},
		{ID: 2, OverallScore: 60},
	}
	event := &model.Event{}
	svc.profileEvent(event, photos)
	expected := 70.0 * math.Log2(3)
	assert.InDelta(t, expected, event.EventScore, 0.001)
}

// --- findBestMatchingEvent ---

func TestFindBestMatchingEvent_NilEvents(t *testing.T) {
	svc := newClusteringSvc()
	now := time.Now()
	cluster := photoCluster{photos: []*model.Photo{makePhoto(1, now)}}
	assert.Nil(t, svc.findBestMatchingEvent(cluster, nil))
}

func TestFindBestMatchingEvent_ReturnsClosestByMidpoint(t *testing.T) {
	svc := newClusteringSvc()
	base := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

	cluster := photoCluster{photos: []*model.Photo{
		makePhoto(1, base),
		makePhoto(2, base.Add(2*time.Hour)),
	}}
	// clusterMid = base + 1h

	near := &model.Event{
		StartTime: base.Add(-30 * time.Minute),
		EndTime:   base.Add(30 * time.Minute),
	} // eventMid = base, dist = 1h
	far := &model.Event{
		StartTime: base.Add(10 * time.Hour),
		EndTime:   base.Add(12 * time.Hour),
	} // eventMid = base+11h, dist = 10h

	best := svc.findBestMatchingEvent(cluster, []*model.Event{far, near})
	assert.Equal(t, near, best)
}
