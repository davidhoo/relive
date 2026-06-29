package geocode

import (
	"math"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestHaversineDistance_SamePoint(t *testing.T) {
	dist := haversineDistance(39.9042, 116.4074, 39.9042, 116.4074)
	assert.Equal(t, 0.0, dist)
}

func TestHaversineDistance_KnownCityPair(t *testing.T) {
	// Beijing to Shanghai: ~1068 km
	dist := haversineDistance(39.9042, 116.4074, 31.2304, 121.4737)
	assert.InDelta(t, 1068, dist, 15) // within 15km tolerance
}

func TestHaversineDistance_Symmetry(t *testing.T) {
	d1 := haversineDistance(39.9042, 116.4074, 35.6762, 139.6503)
	d2 := haversineDistance(35.6762, 139.6503, 39.9042, 116.4074)
	assert.InDelta(t, d1, d2, 0.001)
}

func TestHaversineDistance_Antipodal(t *testing.T) {
	// Opposite sides of Earth: should be ~half circumference
	dist := haversineDistance(0, 0, 0, 180)
	halfCircumference := math.Pi * 6371.0
	assert.InDelta(t, halfCircumference, dist, 1)
}

func TestHaversineDistance_CrossEquator(t *testing.T) {
	// Singapore to Sydney: ~6300 km
	dist := haversineDistance(1.3521, 103.8198, -33.8688, 151.2093)
	assert.InDelta(t, 6300, dist, 50)
}

// ===== Helper functions =====

func TestGetCountryName_Known(t *testing.T) {
	assert.Equal(t, "中国", getCountryName("CN"))
	assert.Equal(t, "日本", getCountryName("JP"))
}

func TestGetCountryName_Unknown(t *testing.T) {
	assert.Equal(t, "XX", getCountryName("XX"))
}

func TestGetProvinceName_China(t *testing.T) {
	tests := map[string]string{
		"01": "安徽省",
		"02": "浙江省",
		"03": "江西省",
		"04": "江苏省",
		"05": "吉林省",
		"06": "青海省",
		"07": "福建省",
		"08": "黑龙江省",
		"09": "河南省",
		"10": "河北省",
		"11": "湖南省",
		"12": "湖北省",
		"13": "新疆维吾尔自治区",
		"14": "西藏自治区",
		"15": "甘肃省",
		"16": "广西壮族自治区",
		"18": "贵州省",
		"19": "辽宁省",
		"20": "内蒙古自治区",
		"21": "宁夏回族自治区",
		"22": "北京市",
		"23": "上海市",
		"24": "山西省",
		"25": "山东省",
		"26": "陕西省",
		"28": "天津市",
		"29": "云南省",
		"30": "广东省",
		"31": "海南省",
		"32": "四川省",
		"33": "重庆市",
	}

	for code, want := range tests {
		t.Run(code, func(t *testing.T) {
			assert.Equal(t, want, getProvinceName("CN", code))
		})
	}
}

func TestOfflineProvider_Issue11UsesGeoNamesAdmin1Code(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.City{}))
	require.NoError(t, db.Create(&model.City{
		GeonameID: 10001,
		Name:      "Xiasha",
		Country:   "CN",
		AdminName: "02",
		Latitude:  30.31023,
		Longitude: 120.31718,
	}).Error)

	location, err := NewOfflineProvider(db, 100).ReverseGeocode(30.292125, 120.378533)
	require.NoError(t, err)
	assert.Equal(t, "浙江省", location.Province)
	assert.Equal(t, "Xiasha", location.City)
	assert.Equal(t, "浙江省Xiasha", location.FormatDisplay())
}

func TestGetProvinceName_NonChina(t *testing.T) {
	assert.Equal(t, "California", getProvinceName("US", "California"))
}

func TestGetProvinceName_NonChinaNumericReturnsEmpty(t *testing.T) {
	assert.Equal(t, "", getProvinceName("US", "12"))
}

// --- isNumericCode ---

func TestIsNumericCode_Empty(t *testing.T) {
	assert.False(t, isNumericCode(""))
}

func TestIsNumericCode_AllDigits(t *testing.T) {
	assert.True(t, isNumericCode("01"))
	assert.True(t, isNumericCode("123"))
}

func TestIsNumericCode_HasLetters(t *testing.T) {
	assert.False(t, isNumericCode("CA"))
	assert.False(t, isNumericCode("12A"))
	assert.False(t, isNumericCode("California"))
}
