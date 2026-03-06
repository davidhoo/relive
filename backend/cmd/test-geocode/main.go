package main

import (
	"fmt"
	"os"

	"github.com/davidhoo/relive/internal/geocode"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

// TestCase 测试用例
type TestCase struct {
	name        string
	lat, lon    float64
	wantFormats map[string]string // provider -> expected FormatDisplay
}

func main() {
	// 初始化 logger
	logger.Init(config.LoggingConfig{Level: "warn", Console: false})

	// 定义测试用例
	testCases := []TestCase{
		{
			name: "北京朝阳区三里屯",
			lat:  39.930492,
			lon:  116.447411,
			wantFormats: map[string]string{
				"weibo":     "北京市朝阳区东大桥（三里屯街区）",
				"nominatim": "北京市朝阳区（南三里屯中街）",
				"offline":   "北京市Beijing",
			},
		},
		{
			name: "北京海淀区软件园",
			lat:  40.040611,
			lon:  116.271356,
			wantFormats: map[string]string{
				"weibo":     "北京市海淀区西北旺（马连洼街区）",
				"nominatim": "北京市海淀区中关村软件园二期（软件园南街）",
				"offline":   "北京市Beijing",
			},
		},
		{
			name: "杭州西湖",
			lat:  30.2741,
			lon:  120.1551,
			wantFormats: map[string]string{
				"weibo":     "杭州市拱墅区浙江（米市巷街区）",
				"nominatim": "拱墅区天水街道", // Nominatim 缺少省市级
				"offline":   "浙江省Hangzhou",
			},
		},
		{
			name: "上海外滩",
			lat:  31.2397,
			lon:  121.4900,
			wantFormats: map[string]string{
				"weibo":     "上海市黄浦区（外滩街区）",
				"nominatim": "上海市浦东新区（东岸绿道）",
				"offline":   "上海市Shanghai",
			},
		},
		{
			name: "成都",
			lat:  30.549894,
			lon:  104.071014,
			wantFormats: map[string]string{
				"weibo":     "成都市武侯区世纪城（桂溪街区）", // 微博返回详细地址
				"nominatim": "武侯区桂溪街道",              // Nominatim 缺少省市级
				"offline":   "四川省Chengdu",
			},
		},
		{
			name: "纽约时代广场",
			lat:  40.7580,
			lon:  -73.9855,
			wantFormats: map[string]string{
				"weibo":     "Hudson",
				"nominatim": "纽约;紐約ManhattanTimes Square（7th Avenue）",
				"offline":   "美国New York",
			},
		},
		{
			name: "巴黎埃菲尔铁塔",
			lat:  48.8584,
			lon:  2.2945,
			wantFormats: map[string]string{
				"weibo":     "",
				"nominatim": "法国巴黎",
				"offline":   "法国Paris",
			},
		},
		{
			name: "东京",
			lat:  35.6762,
			lon:  139.6503,
			wantFormats: map[string]string{
				"weibo":     "",
				"nominatim": "日本東京都",
				"offline":   "日本Tokyo",
			},
		},
	}

	fmt.Println("========================================")
	fmt.Println("地理编码服务商格式测试")
	fmt.Println("========================================")
	fmt.Println()

	// 测试微博 RGC
	fmt.Println("【微博 RGC (Weibo)】")
	fmt.Println("期望格式: 城市 + 区县 + 商圈（街道）")
	fmt.Println("----------------------------------------")
	testProvider("weibo", testCases)
	fmt.Println()

	// 测试 Nominatim
	fmt.Println("【OpenStreetMap (Nominatim)】")
	fmt.Println("期望格式: 城市 + 区县 + 商圈（街道）")
	fmt.Println("----------------------------------------")
	testProvider("nominatim", testCases)
	fmt.Println()

	// 测试离线数据库
	fmt.Println("【离线数据库 (Offline)】")
	fmt.Println("期望格式: 省份 + 城市（英文）")
	fmt.Println("----------------------------------------")
	testProvider("offline", testCases)

	fmt.Println()
	fmt.Println("========================================")
	fmt.Println("测试完成")
	fmt.Println("========================================")
}

func testProvider(providerName string, testCases []TestCase) {
	var provider geocode.Provider
	var available bool

	switch providerName {
	case "weibo":
		apiKey := os.Getenv("WEIBO_API_KEY")
		if apiKey == "" {
			apiKey = "weibo_lbs2023" // 默认测试 key
		}
		p := geocode.NewWeiboProvider(apiKey, 10)
		provider = p
		available = p.IsAvailable()
	case "nominatim":
		p := geocode.NewNominatimProvider("https://nominatim.openstreetmap.org/reverse", 10)
		provider = p
		available = p.IsAvailable()
	case "offline":
		fmt.Println("⚠️  离线数据库需要数据库连接，跳过")
		return
	}

	if !available {
		fmt.Printf("❌ Provider 不可用\n")
		return
	}

	for _, tc := range testCases {
		location, err := provider.ReverseGeocode(tc.lat, tc.lon)
		if err != nil {
			// 某些坐标在某些 provider 可能失败
			fmt.Printf("\n📍 %s (%.6f, %.6f)\n", tc.name, tc.lat, tc.lon)
			fmt.Printf("   ⚠️  Error: %v\n", err)
			continue
		}

		want := tc.wantFormats[providerName]
		got := location.FormatDisplay()

		status := "✅"
		if want != "" && got != want {
			status = "⚠️ "
		}

		fmt.Printf("\n%s %s (%.6f, %.6f)\n", status, tc.name, tc.lat, tc.lon)
		fmt.Printf("   📍 Display: %s\n", got)
		fmt.Printf("   🏙️  City:    %s\n", location.City)
		fmt.Printf("   🏘️  Dist:    %s\n", location.District)
		if location.POI != "" {
			fmt.Printf("   🏪 POI:     %s\n", location.POI)
		}
		if location.Street != "" {
			fmt.Printf("   🛣️  Street:  %s\n", location.Street)
		}
		if want != "" && got != want {
			fmt.Printf("   期望: %s\n", want)
		}
	}
}
