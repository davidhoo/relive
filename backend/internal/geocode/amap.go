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

// AmapProvider 高德地图提供商
type AmapProvider struct {
	apiKey  string
	timeout time.Duration
}

// NewAmapProvider 创建高德地图提供商
func NewAmapProvider(apiKey string, timeout int) *AmapProvider {
	if timeout <= 0 {
		timeout = 5
	}
	return &AmapProvider{
		apiKey:  apiKey,
		timeout: time.Duration(timeout) * time.Second,
	}
}

func (p *AmapProvider) Name() string {
	return "amap"
}

func (p *AmapProvider) Priority() int {
	return 10 // 中等优先级
}

func (p *AmapProvider) IsAvailable() bool {
	return p.apiKey != ""
}

func (p *AmapProvider) ReverseGeocode(lat, lon float64) (*Location, error) {
	startTime := time.Now()

	if p.apiKey == "" {
		return nil, fmt.Errorf("amap API key not configured")
	}

	// 高德地图 API 端点
	baseURL := "https://restapi.amap.com/v3/geocode/regeo"

	params := url.Values{}
	params.Add("key", p.apiKey)
	params.Add("location", fmt.Sprintf("%.6f,%.6f", lon, lat)) // 注意：高德是 经度,纬度
	params.Add("extensions", "base")
	params.Add("output", "json")

	apiURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

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

	var result struct {
		Status    string `json:"status"`
		Info      string `json:"info"`
		InfoCode  string `json:"infocode"`
		Regeocode struct {
			FormattedAddress string `json:"formatted_address"`
			AddressComponent struct {
				Country      string `json:"country"`
				Province     string `json:"province"`
				City         interface{} `json:"city"` // 可能是字符串或数组
				District     string `json:"district"`
				Township     string `json:"township"`
				Streetnumber struct {
					Street    string `json:"street"`
					Number    string `json:"number"`
					Direction string `json:"direction"`
					Distance  string `json:"distance"`
				} `json:"streetNumber"`
			} `json:"addressComponent"`
		} `json:"regeocode"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response failed: %w", err)
	}

	if result.Status != "1" {
		return nil, fmt.Errorf("API error: %s (code: %s)", result.Info, result.InfoCode)
	}

	// 解析城市（可能是字符串或空数组）
	city := ""
	switch v := result.Regeocode.AddressComponent.City.(type) {
	case string:
		city = v
	case []interface{}:
		// 空数组，忽略
	}

	location := &Location{
		Country:   result.Regeocode.AddressComponent.Country,
		Province:  result.Regeocode.AddressComponent.Province,
		City:      city,
		District:  result.Regeocode.AddressComponent.District,
		Street:    result.Regeocode.AddressComponent.Township, // 乡镇/街道
		FullName:  "", // 使用 FormatDisplay 生成
		Latitude:  lat,
		Longitude: lon,
		Provider:  p.Name(),
		Duration:  time.Since(startTime),
	}

	// 构建显示格式地址
	location.FullName = location.FormatDisplay()

	logger.Debugf("AMap geocode: (%.6f,%.6f) -> %s (took %v)",
		lat, lon, location.FormatShort(), location.Duration)

	return location, nil
}
