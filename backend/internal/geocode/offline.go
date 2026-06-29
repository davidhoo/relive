package geocode

import (
	"fmt"
	"math"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// 国家代码到名称的映射
var countryNames = map[string]string{
	"CN": "中国",
	"US": "美国",
	"JP": "日本",
	"KR": "韩国",
	"GB": "英国",
	"FR": "法国",
	"DE": "德国",
	"IT": "意大利",
	"ES": "西班牙",
	"CA": "加拿大",
	"AU": "澳大利亚",
	"RU": "俄罗斯",
	"IN": "印度",
	"BR": "巴西",
	"MX": "墨西哥",
	"TH": "泰国",
	"SG": "新加坡",
	"MY": "马来西亚",
	"ID": "印度尼西亚",
	"VN": "越南",
	"PH": "菲律宾",
	"NZ": "新西兰",
	"CH": "瑞士",
	"AT": "奥地利",
	"BE": "比利时",
	"NL": "荷兰",
	"SE": "瑞典",
	"NO": "挪威",
	"DK": "丹麦",
	"FI": "芬兰",
	"PL": "波兰",
	"CZ": "捷克",
	"HU": "匈牙利",
	"GR": "希腊",
	"PT": "葡萄牙",
	"TR": "土耳其",
	"IL": "以色列",
	"AE": "阿联酋",
	"SA": "沙特阿拉伯",
	"EG": "埃及",
	"ZA": "南非",
	"AR": "阿根廷",
	"CL": "智利",
	"CO": "哥伦比亚",
	"PE": "秘鲁",
}

// 中国省份代码到名称的映射（基于 GeoNames admin1CodesASCII.txt 的 CN.* 代码）
var chinaProvinceNames = map[string]string{
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

// getCountryName 获取国家全称
func getCountryName(code string) string {
	if name, ok := countryNames[code]; ok {
		return name
	}
	return code // 返回代码作为后备
}

// getProvinceName 获取省份名称
func getProvinceName(country, adminCode string) string {
	if country == "CN" {
		if name, ok := chinaProvinceNames[adminCode]; ok {
			return name
		}
	}
	// 非中国国家：admin code 如果是纯数字（如 "12"），不是有意义的省份名，返回空
	// 如果是文字名称（如 "California"），保留
	if isNumericCode(adminCode) {
		return ""
	}
	return adminCode
}

// isNumericCode 检查字符串是否为纯数字代码
func isNumericCode(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

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
	return 50 // 保留接口，执行顺序由 buildGeocodeService 中的添加顺序决定
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

	// 转换省份代码为名称
	countryName := getCountryName(nearestCity.Country)
	provinceName := getProvinceName(nearestCity.Country, nearestCity.AdminName)

	// 优先使用中文城市名
	cityName := nearestCity.Name
	if nearestCity.NameZH != "" {
		cityName = nearestCity.NameZH
	}

	location := &Location{
		City:      cityName,
		Country:   countryName,
		Province:  provinceName,
		Latitude:  lat,
		Longitude: lon,
		Provider:  p.Name(),
		Duration:  time.Since(startTime),
	}

	// 构建显示格式
	// 中国：省份 + 城市（如"四川省成都"）
	// 海外：国家 + 省份 + 城市，省份为空时为 国家 + 城市（如"韩国仁川"）
	if nearestCity.Country == "CN" {
		if location.Province != "" && location.City != "" {
			location.FullName = location.Province + location.City
		} else if location.Province != "" {
			location.FullName = location.Province
		} else if location.City != "" {
			location.FullName = location.City
		}
	} else {
		parts := ""
		if location.Country != "" {
			parts = location.Country
		}
		if location.Province != "" {
			parts += location.Province
		}
		if location.City != "" {
			parts += location.City
		}
		location.FullName = parts
	}
	if location.FullName == "" {
		location.FullName = location.Country
	}

	logger.Debugf("Offline geocode result: City=%s, Province=%s, Country=%s, FullName=%s",
		location.City, location.Province, location.Country, location.FullName)

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
