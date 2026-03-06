package main

import (
	"fmt"

	"github.com/davidhoo/relive/internal/geocode"
)

func main() {
	// 模拟离线数据库返回的 Location
	location := &geocode.Location{
		City:      "Beijing",
		Country:   "中国",
		Province:  "北京市",
		FullName:  "北京市Beijing",
		Provider:  "offline",
		Latitude:  39.9042,
		Longitude: 116.4074,
	}

	fmt.Println("离线数据库 Location 测试")
	fmt.Println("========================")
	fmt.Printf("City:      %s\n", location.City)
	fmt.Printf("Province:  %s\n", location.Province)
	fmt.Printf("FullName:  %s\n", location.FullName)
	fmt.Printf("Provider:  %s\n", location.Provider)
	fmt.Println()
	fmt.Printf("FormatShort:   %s\n", location.FormatShort())
	fmt.Printf("FormatFull:    %s\n", location.FormatFull())
	fmt.Printf("FormatDisplay: %s\n", location.FormatDisplay())

	// 对比非离线 provider
	fmt.Println()
	fmt.Println("微博 RGC Location 测试")
	fmt.Println("========================")
	location2 := &geocode.Location{
		City:      "北京市",
		District:  "朝阳区",
		Country:   "中国",
		Province:  "北京市",
		FullName:  "北京市朝阳区东大桥（三里屯街区）",
		POI:       "东大桥",
		Street:    "三里屯街区",
		Provider:  "weibo",
		Latitude:  39.930492,
		Longitude: 116.447411,
	}
	fmt.Printf("City:      %s\n", location2.City)
	fmt.Printf("District:  %s\n", location2.District)
	fmt.Printf("FullName:  %s\n", location2.FullName)
	fmt.Printf("Provider:  %s\n", location2.Provider)
	fmt.Println()
	fmt.Printf("FormatShort:   %s\n", location2.FormatShort())
	fmt.Printf("FormatFull:    %s\n", location2.FormatFull())
	fmt.Printf("FormatDisplay: %s\n", location2.FormatDisplay())
}
