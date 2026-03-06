package geocode

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// WeiboProvider 微博地图RGC逆地理编码提供商
type WeiboProvider struct {
	apiKey   string
	timeout  time.Duration
	endpoint string
}

// NewWeiboProvider 创建微博地图提供商
func NewWeiboProvider(apiKey string, timeout int) *WeiboProvider {
	if timeout <= 0 {
		timeout = 10
	}
	return &WeiboProvider{
		apiKey:   apiKey,
		timeout:  time.Duration(timeout) * time.Second,
		endpoint: "https://place.weibo.cn/wandermap/rgc",
	}
}

func (p *WeiboProvider) Name() string {
	return "weibo"
}

func (p *WeiboProvider) Priority() int {
	return 5 // 高优先级（国内覆盖好）
}

func (p *WeiboProvider) IsAvailable() bool {
	return p.apiKey != ""
}

// WeiboRGCResponse 微博RGC响应结构
type WeiboRGCResponse struct {
	Status string                 `json:"status"`
	Result map[string]interface{} `json:"result"`
}

// LocationDetail 位置详情
type LocationDetail struct {
	District struct {
		ObjectID     string `json:"objectid"`
		Name         string `json:"name"`
		SimpleName   string `json:"simple_name"`
		Adcode       string `json:"adcode"`
		Type         int    `json:"type"`
	} `json:"disctrict"`
	City struct {
		ObjectID     string `json:"objectid"`
		Name         string `json:"name"`
		SimpleName   string `json:"simple_name"`
		Adcode       string `json:"adcode"`
		Type         int    `json:"type"`
	} `json:"city"`
	Province struct {
		ObjectID     string `json:"objectid"`
		Name         string `json:"name"`
		SimpleName   string `json:"simple_name"`
		Adcode       string `json:"adcode"`
		Type         int    `json:"type"`
	} `json:"province"`
	Country struct {
		ObjectID     string `json:"objectid"`
		Name         string `json:"name"`
		SimpleName   string `json:"simple_name"`
		Adcode       string `json:"adcode"`
		Type         int    `json:"type"`
	} `json:"country"`
}

func (p *WeiboProvider) ReverseGeocode(lat, lon float64) (*Location, error) {
	startTime := time.Now()

	if p.apiKey == "" {
		return nil, fmt.Errorf("weibo API key not configured")
	}

	params := url.Values{}
	params.Add("formpt", p.apiKey)
	params.Add("lng", fmt.Sprintf("%.6f", lon))
	params.Add("lat", fmt.Sprintf("%.6f", lat))

	apiURL := fmt.Sprintf("%s?%s", p.endpoint, params.Encode())

	client := &http.Client{Timeout: p.timeout}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}

	var result WeiboRGCResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response failed: %w", err)
	}

	if result.Status != "ok" {
		return nil, fmt.Errorf("API error: status=%s", result.Status)
	}

	// 解析结果（key是坐标组合）
	key := fmt.Sprintf("%.6f_%.6f", lon, lat)
	locationData, ok := result.Result[key].(map[string]interface{})
	if !ok {
		// 尝试任意key
		for _, v := range result.Result {
			locationData, ok = v.(map[string]interface{})
			if ok {
				break
			}
		}
	}

	if !ok {
		return nil, fmt.Errorf("no location data in response")
	}

	// 提取各级地址信息
	location := &Location{
		Latitude:  lat,
		Longitude: lon,
		Provider:  p.Name(),
		Duration:  time.Since(startTime),
	}

	// 解析国家
	if country, ok := locationData["country"].(map[string]interface{}); ok {
		if name, ok := country["name"].(string); ok {
			location.Country = name
		}
	}

	// 解析省份
	if province, ok := locationData["province"].(map[string]interface{}); ok {
		if name, ok := province["name"].(string); ok {
			location.Province = name
		}
	}

	// 解析城市
	if city, ok := locationData["city"].(map[string]interface{}); ok {
		if name, ok := city["name"].(string); ok {
			location.City = name
		}
	}

	// 解析区县
	if district, ok := locationData["disctrict"].(map[string]interface{}); ok {
		if name, ok := district["name"].(string); ok {
			location.District = name
		}
	}

	// 解析街道/街区 (town)
	if town, ok := locationData["town"].(map[string]interface{}); ok {
		if name, ok := town["name"].(string); ok {
			location.Street = name
		}
	}

	// 解析商圈/地标 (business)
	if business, ok := locationData["business"].(map[string]interface{}); ok {
		if name, ok := business["name"].(string); ok {
			location.POI = name
		}
	}

	// 构建完整地址（使用新的显示格式）
	location.FullName = location.FormatDisplay()

	logger.Debugf("Weibo geocode: (%.6f,%.6f) -> %s (took %v)",
		lat, lon, location.FormatShort(), location.Duration)

	return location, nil
}
