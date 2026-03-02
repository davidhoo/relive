package geocode

import (
	"fmt"
	"math"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// OfflineProvider 离线地理编码提供商（基于城市数据库）
type OfflineProvider struct {
	db          *gorm.DB
	maxDistance float64 // 最大搜索距离（km）
}

// NewOfflineProvider 创建离线提供商
func NewOfflineProvider(db *gorm.DB, maxDistance float64) *OfflineProvider {
	if maxDistance <= 0 {
		maxDistance = 100 // 默认 100km
	}
	return &OfflineProvider{
		db:          db,
		maxDistance: maxDistance,
	}
}

func (p *OfflineProvider) Name() string {
	return "offline"
}

func (p *OfflineProvider) Priority() int {
	return 5 // 最高优先级（快、离线）
}

func (p *OfflineProvider) IsAvailable() bool {
	if p.db == nil {
		return false
	}

	// 检查城市表是否有数据
	var count int64
	p.db.Model(&model.City{}).Count(&count)
	return count > 0
}

func (p *OfflineProvider) ReverseGeocode(lat, lon float64) (*Location, error) {
	startTime := time.Now()

	if p.db == nil {
		return nil, fmt.Errorf("database not configured")
	}

	// 粗筛选：在 GPS 坐标附近的矩形范围内查找
	// 大约 ±2 度 约等于 220km
	searchRange := p.maxDistance / 111.0 // 1度 ≈ 111km

	var cities []model.City
	err := p.db.Where("latitude BETWEEN ? AND ? AND longitude BETWEEN ? AND ?",
		lat-searchRange, lat+searchRange,
		lon-searchRange, lon+searchRange,
	).Find(&cities).Error

	if err != nil {
		return nil, fmt.Errorf("query cities failed: %w", err)
	}

	if len(cities) == 0 {
		return nil, fmt.Errorf("no nearby city found within %.0f km", p.maxDistance)
	}

	// 精确计算距离，找到最近的城市
	minDist := math.MaxFloat64
	var nearestCity *model.City

	for i := range cities {
		dist := haversineDistance(lat, lon, cities[i].Latitude, cities[i].Longitude)
		if dist < minDist {
			minDist = dist
			nearestCity = &cities[i]
		}
	}

	if nearestCity == nil || minDist > p.maxDistance {
		return nil, fmt.Errorf("nearest city is %.2f km away (max: %.0f km)", minDist, p.maxDistance)
	}

	location := &Location{
		City:      nearestCity.Name,
		Country:   nearestCity.Country,
		Province:  nearestCity.AdminName, // 如果数据库有这个字段
		FullName:  fmt.Sprintf("%s, %s", nearestCity.Name, nearestCity.Country),
		Latitude:  lat,
		Longitude: lon,
		Provider:  p.Name(),
		Duration:  time.Since(startTime),
	}

	logger.Debugf("Offline geocode: (%.6f,%.6f) -> %s (%.2f km, took %v)",
		lat, lon, location.FormatShort(), minDist, location.Duration)

	return location, nil
}

// haversineDistance 计算两点间的球面距离（km）
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371 // 地球半径（km）

	// 转换为弧度
	dLat := (lat2 - lat1) * math.Pi / 180
	dLon := (lon2 - lon1) * math.Pi / 180
	lat1Rad := lat1 * math.Pi / 180
	lat2Rad := lat2 * math.Pi / 180

	// Haversine 公式
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*
			math.Sin(dLon/2)*math.Sin(dLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return R * c
}
