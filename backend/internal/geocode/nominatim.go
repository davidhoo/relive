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

// NominatimProvider OpenStreetMap Nominatim 提供商
type NominatimProvider struct {
	endpoint string
	timeout  time.Duration
}

// NewNominatimProvider 创建 Nominatim 提供商
func NewNominatimProvider(endpoint string, timeout int) *NominatimProvider {
	if endpoint == "" {
		endpoint = "https://nominatim.openstreetmap.org/reverse"
	}
	if timeout <= 0 {
		timeout = 10
	}
	return &NominatimProvider{
		endpoint: endpoint,
		timeout:  time.Duration(timeout) * time.Second,
	}
}

func (p *NominatimProvider) Name() string {
	return "nominatim"
}

func (p *NominatimProvider) Priority() int {
	return 20 // 较低优先级（慢）
}

func (p *NominatimProvider) IsAvailable() bool {
	return true // 公开服务，始终可用
}

func (p *NominatimProvider) ReverseGeocode(lat, lon float64) (*Location, error) {
	startTime := time.Now()

	params := url.Values{}
	params.Add("lat", fmt.Sprintf("%.6f", lat))
	params.Add("lon", fmt.Sprintf("%.6f", lon))
	params.Add("format", "json")
	params.Add("accept-language", "zh-CN,zh,en") // 优先中文
	params.Add("addressdetails", "1")
	params.Add("zoom", "18") // 详细级别

	apiURL := fmt.Sprintf("%s?%s", p.endpoint, params.Encode())

	client := &http.Client{Timeout: p.timeout}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}

	// Nominatim 要求设置 User-Agent
	req.Header.Set("User-Agent", "Relive-Photo-App/1.0 (https://github.com/yourusername/relive)")

	// 添加延迟，遵守 Nominatim 使用政策（最多 1 请求/秒）
	time.Sleep(1 * time.Second)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response failed: %w", err)
	}

	var result struct {
		PlaceID     int    `json:"place_id"`
		Licence     string `json:"licence"`
		DisplayName string `json:"display_name"`
		Address     struct {
			Road         string `json:"road"`
			Suburb       string `json:"suburb"`
			City         string `json:"city"`
			County       string `json:"county"`
			State        string `json:"state"`
			Postcode     string `json:"postcode"`
			Country      string `json:"country"`
			CountryCode  string `json:"country_code"`
			Village      string `json:"village"`
			Town         string `json:"town"`
			Municipality string `json:"municipality"`
		} `json:"address"`
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response failed: %w", err)
	}

	// 提取城市信息（可能在不同字段）
	city := result.Address.City
	if city == "" {
		city = result.Address.Town
	}
	if city == "" {
		city = result.Address.Municipality
	}
	if city == "" {
		city = result.Address.County
	}

	// 提取区/县
	district := result.Address.Suburb
	if district == "" {
		district = result.Address.Village
	}

	location := &Location{
		Country:   result.Address.Country,
		Province:  result.Address.State,
		City:      city,
		District:  district,
		FullName:  result.DisplayName,
		Latitude:  lat,
		Longitude: lon,
		Provider:  p.Name(),
		Duration:  time.Since(startTime),
	}

	logger.Debugf("Nominatim geocode: (%.6f,%.6f) -> %s (took %v)",
		lat, lon, location.FormatShort(), location.Duration)

	return location, nil
}
