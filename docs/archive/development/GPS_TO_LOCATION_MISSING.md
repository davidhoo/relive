# GPS 坐标转位置信息功能缺失

## 问题现状

### 已有的数据
✅ GPS 坐标 (gps_latitude, gps_longitude)
❌ 位置名称 (location) - 全部为空

### 数据库字段
```go
type Photo struct {
    // ... 其他字段
    GPSLatitude  *float64  `json:"gps_latitude"`   // ✅ 有数据
    GPSLongitude *float64  `json:"gps_longitude"`  // ✅ 有数据
    Location     string    `json:"location"`       // ❌ 空的
}
```

### 现状检查
```bash
sqlite3 ./data/relive.db "SELECT COUNT(*) FROM photos WHERE gps_latitude IS NOT NULL;"
# 结果：X 张照片有 GPS 坐标

sqlite3 ./data/relive.db "SELECT COUNT(*) FROM photos WHERE location IS NOT NULL AND location != '';"
# 结果：0 张照片有位置信息
```

## 原因分析

### 代码检查

**扫描照片时 (photo_service.go:182-194):**
```go
photo := &model.Photo{
    // ... 其他字段
    GPSLatitude:  exifData.GPSLatitude,   // ✅ 保存 GPS
    GPSLongitude: exifData.GPSLongitude,  // ✅ 保存 GPS
    // Location 字段没有赋值 ❌
}
```

**缺失的功能：**
1. ❌ 没有 GPS 反向地理编码 (Reverse Geocoding)
2. ❌ 没有城市数据库（cities 表是空的）
3. ❌ 没有 GPS 转城市名称的函数

## 解决方案

### 方案 1: 在线 API 反向地理编码 (推荐)

使用地图服务 API 将 GPS 坐标转换为位置名称。

#### 可选的 API 服务

**A. 高德地图 (中国推荐)**
- API: https://restapi.amap.com/v3/geocode/regeo
- 优点：中文地名准确，国内速度快
- 限额：个人认证 30 万次/天（免费）
- 文档：https://lbs.amap.com/api/webservice/guide/api/georegeo

**B. OpenStreetMap Nominatim (开源)**
- API: https://nominatim.openstreetmap.org/reverse
- 优点：免费，无需 API Key
- 限制：每秒 1 次请求
- 文档：https://nominatim.org/release-docs/latest/api/Reverse/

**C. Google Maps API**
- API: https://maps.googleapis.com/maps/api/geocode/json
- 优点：全球准确
- 费用：200 美元免费额度/月
- 文档：https://developers.google.com/maps/documentation/geocoding

#### 实现代码

**1. 创建 GPS 工具文件**

`backend/internal/util/gps.go`:
```go
package util

import (
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "time"
)

// GPSToLocation 将 GPS 坐标转换为位置名称
func GPSToLocation(lat, lon float64, apiKey string) (string, error) {
    // 使用高德地图 API（中国）
    return gpsToLocationAmap(lat, lon, apiKey)
}

// gpsToLocationAmap 使用高德地图 API
func gpsToLocationAmap(lat, lon float64, apiKey string) (string, error) {
    baseURL := "https://restapi.amap.com/v3/geocode/regeo"

    params := url.Values{}
    params.Add("key", apiKey)
    params.Add("location", fmt.Sprintf("%.6f,%.6f", lon, lat)) // 高德: 经度,纬度
    params.Add("extensions", "base")

    apiURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

    client := &http.Client{Timeout: 5 * time.Second}
    resp, err := client.Get(apiURL)
    if err != nil {
        return "", fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("read response failed: %w", err)
    }

    var result struct {
        Status    string `json:"status"`
        Info      string `json:"info"`
        Regeocode struct {
            AddressComponent struct {
                City     string `json:"city"`
                District string `json:"district"`
                Province string `json:"province"`
            } `json:"addressComponent"`
        } `json:"regeocode"`
    }

    if err := json.Unmarshal(body, &result); err != nil {
        return "", fmt.Errorf("parse response failed: %w", err)
    }

    if result.Status != "1" {
        return "", fmt.Errorf("API error: %s", result.Info)
    }

    // 构建位置字符串
    addr := result.Regeocode.AddressComponent
    location := ""

    // 优先使用：城市 > 省份
    if addr.City != "" && addr.City != "[]" {
        location = addr.City
        if addr.District != "" {
            location = addr.City + addr.District
        }
    } else if addr.Province != "" {
        location = addr.Province
    }

    return location, nil
}

// gpsToLocationNominatim 使用 OpenStreetMap (开源方案)
func gpsToLocationNominatim(lat, lon float64) (string, error) {
    baseURL := "https://nominatim.openstreetmap.org/reverse"

    params := url.Values{}
    params.Add("lat", fmt.Sprintf("%.6f", lat))
    params.Add("lon", fmt.Sprintf("%.6f", lon))
    params.Add("format", "json")
    params.Add("accept-language", "zh-CN")

    apiURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

    client := &http.Client{Timeout: 5 * time.Second}
    req, _ := http.NewRequest("GET", apiURL, nil)
    req.Header.Set("User-Agent", "Relive-Photo-App/1.0")

    resp, err := client.Do(req)
    if err != nil {
        return "", fmt.Errorf("request failed: %w", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return "", fmt.Errorf("read response failed: %w", err)
    }

    var result struct {
        Address struct {
            City       string `json:"city"`
            County     string `json:"county"`
            State      string `json:"state"`
            Country    string `json:"country"`
        } `json:"address"`
    }

    if err := json.Unmarshal(body, &result); err != nil {
        return "", fmt.Errorf("parse response failed: %w", err)
    }

    // 构建位置字符串
    location := ""
    if result.Address.City != "" {
        location = result.Address.City
    } else if result.Address.County != "" {
        location = result.Address.County
    } else if result.Address.State != "" {
        location = result.Address.State
    }

    return location, nil
}
```

**2. 在扫描时调用**

修改 `photo_service.go`:
```go
func (s *photoService) processPhoto(filePath string, info os.FileInfo) (*model.Photo, error) {
    // ... 现有的 EXIF 提取代码 ...

    // 构建 Photo 对象
    photo := &model.Photo{
        FilePath:     filePath,
        // ... 其他字段 ...
        GPSLatitude:  exifData.GPSLatitude,
        GPSLongitude: exifData.GPSLongitude,
    }

    // 新增：GPS 转位置 ⭐
    if photo.GPSLatitude != nil && photo.GPSLongitude != nil {
        // 从配置获取高德地图 API Key
        apiKey := s.config.Map.AmapAPIKey // 需要在配置中添加

        location, err := util.GPSToLocation(*photo.GPSLatitude, *photo.GPSLongitude, apiKey)
        if err != nil {
            logger.Warnf("GPS to location failed: %v", err)
            // 不算错误，继续处理
        } else {
            photo.Location = location
        }

        // 添加延迟，避免 API 限流
        time.Sleep(50 * time.Millisecond)
    }

    return photo, nil
}
```

**3. 添加配置**

`config.dev.yaml`:
```yaml
# 地图服务配置
map:
  provider: "amap"  # amap / nominatim / google
  amap_api_key: "your-api-key-here"  # 高德地图 API Key
```

---

### 方案 2: 离线城市数据库

下载城市数据，通过距离计算找到最近的城市。

#### 数据源

**GeoNames 城市数据库**
- 下载：http://download.geonames.org/export/dump/cities15000.zip
- 包含全球人口 > 15000 的城市（~27000 个城市）
- 免费、可商用

#### 实现步骤

**1. 导入城市数据**

```bash
# 下载并解压
wget http://download.geonames.org/export/dump/cities15000.zip
unzip cities15000.zip

# 格式：geonameid, name, lat, lng, country, ...
# 需要编写导入脚本
```

**2. 创建导入命令**

`cmd/import-cities/main.go`:
```go
package main

import (
    "encoding/csv"
    "os"
    "strconv"
    // ... imports
)

func main() {
    file, _ := os.Open("cities15000.txt")
    defer file.Close()

    reader := csv.NewReader(file)
    reader.Comma = '\t'

    for {
        record, err := reader.Read()
        if err != nil {
            break
        }

        geonameID, _ := strconv.Atoi(record[0])
        name := record[1]
        lat, _ := strconv.ParseFloat(record[4], 64)
        lon, _ := strconv.ParseFloat(record[5], 64)
        country := record[8]

        city := &model.City{
            GeonameID: geonameID,
            Name:      name,
            Latitude:  lat,
            Longitude: lon,
            Country:   country,
        }

        db.Create(city)
    }
}
```

**3. 实现最近城市查找**

`internal/util/gps.go`:
```go
import "math"

// FindNearestCity 找到最近的城市
func FindNearestCity(lat, lon float64, db *gorm.DB) (string, error) {
    var cities []model.City

    // 粗筛选：范围 ±2 度（约 220km）
    err := db.Where("latitude BETWEEN ? AND ? AND longitude BETWEEN ? AND ?",
        lat-2, lat+2, lon-2, lon+2).Find(&cities).Error
    if err != nil {
        return "", err
    }

    if len(cities) == 0 {
        return "", fmt.Errorf("no nearby city found")
    }

    // 精确计算距离
    minDist := math.MaxFloat64
    nearestCity := ""

    for _, city := range cities {
        dist := haversineDistance(lat, lon, city.Latitude, city.Longitude)
        if dist < minDist {
            minDist = dist
            nearestCity = city.Name
        }
    }

    return nearestCity, nil
}

// haversineDistance 计算两点间距离（km）
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
    const R = 6371 // 地球半径（km）

    dLat := (lat2 - lat1) * math.Pi / 180
    dLon := (lon2 - lon1) * math.Pi / 180

    a := math.Sin(dLat/2)*math.Sin(dLat/2) +
        math.Cos(lat1*math.Pi/180)*math.Cos(lat2*math.Pi/180)*
        math.Sin(dLon/2)*math.Sin(dLon/2)

    c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

    return R * c
}
```

**优点:**
- ✅ 完全离线
- ✅ 无 API 限制
- ✅ 速度快

**缺点:**
- ❌ 不够精确（只能到最近的大城市）
- ❌ 需要维护数据库（占用空间）
- ❌ 初始导入复杂

---

### 方案 3: 混合方案 (推荐)

结合在线 API 和离线数据库：

1. **首次扫描**: 使用在线 API 获取准确位置
2. **缓存结果**: 保存到数据库
3. **相近位置**: 如果新照片的 GPS 与已知位置接近，复用位置名

```go
func (s *photoService) gpsToLocationWithCache(lat, lon float64) (string, error) {
    // 1. 查找缓存（距离 < 1km 的已知位置）
    cachedLocation := s.findCachedLocation(lat, lon, 1.0)
    if cachedLocation != "" {
        return cachedLocation, nil
    }

    // 2. 调用在线 API
    location, err := util.GPSToLocation(lat, lon, s.config.Map.AmapAPIKey)
    if err != nil {
        return "", err
    }

    // 3. 保存到缓存（可以用 Location + GPS 坐标）
    // 或者保存到单独的 location_cache 表

    return location, nil
}
```

---

## 实现 GPS 转位置功能的 API

### 新增 API 端点

**批量更新位置信息:**

```go
// POST /api/v1/photos/update-locations
func (h *PhotoHandler) UpdateLocations(c *gin.Context) {
    photos, err := h.photoService.GetPhotosWithGPS()

    updated := 0
    for _, photo := range photos {
        if photo.Location == "" {
            location, err := util.GPSToLocation(*photo.GPSLatitude, *photo.GPSLongitude, apiKey)
            if err == nil {
                photo.Location = location
                h.photoService.Update(photo)
                updated++
            }
            time.Sleep(100 * time.Millisecond) // 避免 API 限流
        }
    }

    c.JSON(200, gin.H{"updated": updated})
}
```

---

## 快速测试

### 测试高德地图 API

```bash
# 获取 API Key: https://console.amap.com/dev/key/app
# 免费个人认证：30 万次/天

# 测试请求
curl "https://restapi.amap.com/v3/geocode/regeo?key=YOUR_KEY&location=116.481488,39.990464"

# 返回示例：
{
  "status": "1",
  "regeocode": {
    "addressComponent": {
      "province": "北京市",
      "city": "北京市",
      "district": "朝阳区"
    }
  }
}
```

---

## 推荐实施步骤

### 第一步：获取高德地图 API Key (5 分钟)

1. 访问：https://console.amap.com/dev/key/app
2. 注册/登录
3. 创建应用
4. 获取 API Key

### 第二步：添加配置 (1 分钟)

`config.dev.yaml`:
```yaml
# 地图服务
map:
  amap_api_key: "your-api-key-here"
```

### 第三步：实现代码 (30 分钟)

1. 创建 `internal/util/gps.go`
2. 修改 `photo_service.go` 在扫描时转换
3. 添加批量更新 API（可选）

### 第四步：测试 (10 分钟)

1. 重新扫描几张有 GPS 的照片
2. 检查数据库 `location` 字段
3. 验证位置信息正确

---

## 总结

**问题:** GPS 坐标有了，但没有转换为位置名称

**原因:** GPS 反向地理编码功能未实现

**推荐方案:**
✅ 使用高德地图 API（在线）- 简单、准确、免费额度足够
📦 配合缓存机制 - 减少 API 调用
🔄 提供批量更新接口 - 处理现有照片

**实施难度:** 低（约 1 小时）

**是否需要我帮你实现这个功能？**
