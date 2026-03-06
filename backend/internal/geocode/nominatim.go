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

// 中国直辖市列表
var chinaMunicipalities = []string{"北京市", "天津市", "上海市", "重庆市"}

// inferMunicipalityFromContext 从 display_name 推断直辖市名称
// display_name 格式："软件园南街, 中关村软件园二期, 马连洼街道, 海淀区, 北京市, 100093, 中国"
func inferMunicipalityFromContext(displayName, district string) string {
	// 检查 display_name 中是否包含直辖市名称
	for _, m := range chinaMunicipalities {
		if contains(displayName, m) {
			return m
		}
	}
	// 如果找不到，返回区/县名作为后备
	return district
}

// contains 检查字符串是否包含子串
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

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
			Commercial   string `json:"commercial"` // 商圈/商业区
			Neighbourhood string `json:"neighbourhood"` // 社区
		} `json:"address"`
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response failed: %w", err)
	}

	// Nominatim 地址结构解析
	// 中国地址：
	// - 省份：state (浙江省)
	// - 直辖市：无 state，city = 区/县 (海淀区)，需要从 ISO3166-2-lvl4 识别
	// - 城市：对于直辖市，需要从 context 推断；对于省份，city = 区/县
	// - 区县：city = 区/县 (海淀区)
	// - 街道：suburb (马连洼街道)
	// - 商圈：commercial (中关村软件园二期)
	// - 道路：road (软件园南街)

	city := result.Address.City
	province := result.Address.State
	district := result.Address.Suburb
	street := result.Address.Road
	poi := result.Address.Commercial

	// 如果 poi 为空，尝试 neighbourhood
	if poi == "" {
		poi = result.Address.Neighbourhood
	}

	// 如果没有 province (state)，说明是直辖市
	// 从 display_name 或 context 推断
	if province == "" && result.Address.CountryCode == "cn" {
		// 对于中国直辖市，Nominatim 的 city 实际上是区/县，需要推断市级
		// 尝试从 display_name 推断
		// display_name 格式：..., 海淀区, 北京市, 100093, 中国
		municipality := inferMunicipalityFromContext(result.DisplayName, city)
		if municipality != city {
			// 推断成功，调整层级：
			// province = city = 直辖市名称 (北京市)
			// district = 原 city (海淀区)
			// suburb 保持不变 (马连洼街道)
			province = municipality
			district = city  // 区/县移到 district
			city = municipality  // 城市设为直辖市名称
		}
	}

	// 如果 district 为空，尝试其他字段
	if district == "" {
		district = result.Address.County
	}
	if district == "" {
		district = result.Address.Town
	}
	if district == "" {
		district = result.Address.Village
	}

	location := &Location{
		Country:   result.Address.Country,
		Province:  province,
		City:      city,
		District:  district,
		Street:    street,
		POI:       poi,
		Latitude:  lat,
		Longitude: lon,
		Provider:  p.Name(),
		Duration:  time.Since(startTime),
	}

	// 构建显示格式地址
	location.FullName = location.FormatDisplay()

	logger.Debugf("Nominatim geocode: (%.6f,%.6f) -> %s (took %v)",
		lat, lon, location.FormatShort(), location.Duration)

	return location, nil
}
