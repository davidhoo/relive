# EXIF 元数据处理方案

> 详细说明照片 EXIF 信息的提取、处理和使用
> 参考项目：InkTime

---

## 一、EXIF 提取字段

### 1.1 基础信息

| 字段 | EXIF Tag | 说明 | 必需 |
|------|----------|------|------|
| **拍摄时间** | DateTime / DateTimeOriginal | 照片拍摄时间 | ✅ 必需 |
| **相机品牌** | Make | 相机制造商（如 Apple、Canon） | 可选 |
| **相机型号** | Model | 相机型号（如 iPhone 13 Pro） | 可选 |
| **图片宽度** | ImageWidth | 照片宽度（像素） | ✅ 必需 |
| **图片高度** | ImageLength | 照片高度（像素） | ✅ 必需 |
| **方向** | Orientation | 图片方向（1-8） | ✅ 重要 |

### 1.2 拍摄参数（摄影技术分析）

| 字段 | EXIF Tag | 说明 | 用途 |
|------|----------|------|------|
| **ISO 感光度** | ISOSpeedRatings | 感光度值 | 评估画质 |
| **光圈** | FNumber | 光圈值（如 f/2.8） | 评估景深 |
| **快门速度** | ExposureTime | 曝光时间（如 1/125s） | 评估运动模糊 |
| **焦距** | FocalLength | 焦距（如 50mm） | 识别镜头类型 |
| **曝光补偿** | ExposureBiasValue | 曝光补偿值 | 评估曝光 |
| **白平衡** | WhiteBalance | 白平衡模式 | 评估色温 |
| **闪光灯** | Flash | 是否使用闪光灯 | 评估光源 |

### 1.3 GPS 位置信息

| 字段 | EXIF Tag | 说明 | 用途 |
|------|----------|------|------|
| **GPS 纬度** | GPSLatitude + GPSLatitudeRef | 拍摄地点纬度 | ✅ 地点识别 |
| **GPS 经度** | GPSLongitude + GPSLongitudeRef | 拍摄地点经度 | ✅ 地点识别 |
| **GPS 高度** | GPSAltitude + GPSAltitudeRef | 海拔高度 | 地理分析 |
| **GPS 时间** | GPSTimeStamp | GPS 记录时间 | 验证时间 |

### 1.4 软件信息（用于识别截图）

| 字段 | EXIF Tag | 说明 | 用途 |
|------|----------|------|------|
| **软件** | Software | 处理软件（如 Screenshot） | ✅ 识别截图 |
| **艺术家** | Artist | 作者信息 | 可选 |
| **版权** | Copyright | 版权信息 | 可选 |

---

## 二、EXIF 提取策略

### 2.1 优先级顺序

**时间获取优先级**：
```
1. DateTimeOriginal（原始拍摄时间）✅ 最优先
2. DateTime（最后修改时间）
3. CreateDate
4. 文件修改时间（mtime）作为兜底
```

**原因**：
- DateTimeOriginal 是真正的拍摄时间
- DateTime 可能是编辑时间
- 文件时间可能因复制而改变

### 2.2 处理流程

```
1. 打开照片文件
   ↓
2. 读取 EXIF 数据
   ↓
3. 解析关键字段（按优先级）
   ↓
4. 数据验证和清洗
   ↓
5. 保存到数据库
   ↓
6. 保留完整 EXIF JSON（用于后续查询）
```

---

## 三、数据库存储设计

### 3.1 EXIF 字段（photos 表）

```sql
-- 基础信息
exif_datetime DATETIME,           -- 拍摄时间（最重要）
exif_make VARCHAR(100),            -- 相机品牌
exif_model VARCHAR(100),           -- 相机型号
width INTEGER,                     -- 图片宽度
height INTEGER,                    -- 图片高度
orientation INTEGER,               -- 方向（1-8）

-- 拍摄参数
exif_iso INTEGER,                  -- ISO 感光度
exif_exposure_time VARCHAR(50),   -- 快门速度（如 "1/125"）
exif_f_number REAL,                -- 光圈值（如 2.8）
exif_focal_length REAL,            -- 焦距（如 50.0）
exif_flash INTEGER,                -- 闪光灯（0/1）

-- GPS 信息
exif_gps_lat REAL,                 -- GPS 纬度
exif_gps_lon REAL,                 -- GPS 经度
exif_gps_alt REAL,                 -- GPS 高度
exif_city VARCHAR(100),            -- 反查的城市名称

-- 软件信息（用于识别截图）
exif_software VARCHAR(200),        -- 软件信息

-- 完整 EXIF（JSON）
exif_json TEXT,                    -- 完整 EXIF 的 JSON 格式
```

### 3.2 索引设计

```sql
-- 时间索引（最常用）
CREATE INDEX idx_exif_datetime ON photos(exif_datetime);

-- GPS 索引（地理查询）
CREATE INDEX idx_exif_gps ON photos(exif_gps_lat, exif_gps_lon);

-- 城市索引（按地点筛选）
CREATE INDEX idx_exif_city ON photos(exif_city);

-- 相机型号索引（按设备筛选）
CREATE INDEX idx_exif_model ON photos(exif_model);
```

---

## 四、特殊情况处理

### 4.1 EXIF 缺失

**场景**：某些照片可能没有 EXIF 信息

**处理策略**：
```
1. 时间缺失 → 使用文件修改时间（mtime）
2. GPS 缺失 → exif_gps_lat/lon 设为 NULL
3. 相机信息缺失 → exif_make/model 设为 NULL
4. 尺寸缺失 → 通过图片库读取实际尺寸
```

**标记字段**：
```sql
exif_available BOOLEAN,  -- EXIF 是否可用
```

### 4.2 EXIF 方向处理

**Orientation 值定义**：
```
1 = 正常（0度）
2 = 水平翻转
3 = 旋转180度
4 = 垂直翻转
5 = 水平翻转 + 逆时针90度
6 = 顺时针90度
7 = 水平翻转 + 顺时针90度
8 = 逆时针90度
```

**处理策略**：
- ✅ **存储原始 orientation 值**
- ✅ **渲染时自动旋转**（墨水屏显示时）
- ✅ **缩略图预处理**（Web 界面显示时）

### 4.3 时间格式处理

**EXIF 时间格式**：
```
标准格式："2024:02:28 14:30:45"
目标格式："2024-02-28 14:30:45"
```

**解析逻辑**：
```go
// 伪代码
func ParseExifDateTime(exifTime string) time.Time {
    // 1. 尝试标准 EXIF 格式 "YYYY:MM:DD HH:MM:SS"
    // 2. 替换 ":" 为 "-" 处理日期部分
    // 3. 如果解析失败，尝试其他格式
    // 4. 最终兜底使用文件时间
}
```

### 4.4 GPS 坐标处理

**坐标格式转换**：
```
EXIF 格式：度分秒（DMS）
例如：纬度 30° 15' 30" N

数据库格式：十进制度（Decimal Degrees）
例如：30.258333

转换公式：
Decimal = Degrees + (Minutes/60) + (Seconds/3600)
```

**坐标验证**：
```
纬度范围：-90 ~ +90
经度范围：-180 ~ +180

如果超出范围 → GPS 无效，设为 NULL
```

---

## 五、EXIF 在功能中的应用

### 5.1 拍摄时间 → 往年今日

```
用途：核心功能
字段：exif_datetime

逻辑：
WHERE MONTH(exif_datetime) = MONTH(今天)
  AND DAY(exif_datetime) = DAY(今天)
  AND YEAR(exif_datetime) < YEAR(今天)
```

### 5.2 GPS 信息 → 地点识别

```
用途：城市识别、旅行加分
字段：exif_gps_lat, exif_gps_lon

处理：
1. GPS 坐标 → 查询离线城市数据库 → 获取城市名
2. 判断是否在常驻地（HOME_RADIUS_KM）
3. 如果在外地 → memory_score +5
```

### 5.3 拍摄参数 → 美观度评分

```
用途：辅助美观度评分
字段：exif_iso, exif_f_number, exif_exposure_time

评估：
- 高 ISO（>3200）→ 可能有噪点 → 影响画质
- 大光圈（<f/2.8）→ 可能有浅景深 → 人像加分
- 慢快门（>1/30s）→ 可能运动模糊（除非三脚架）

注意：仅作为辅助参考，主要靠 AI 视觉评估
```

### 5.4 相机型号 → 特殊分类

```
用途：识别截图
字段：exif_software, exif_model

规则：
IF exif_software LIKE '%Screenshot%'
   OR exif_model IS NULL  -- 截图通常无相机信息
   OR (width=1170 AND height=2532)  -- iPhone 屏幕分辨率
THEN
   category = "截图"
   memory_score = 0-25
```

### 5.5 Orientation → 图片旋转

```
用途：正确显示照片
字段：orientation

应用：
1. 墨水屏渲染时根据 orientation 旋转
2. Web 缩略图预处理时旋转
3. 确保照片始终正向显示
```

---

## 六、Golang 实现示例

### 6.1 EXIF 提取（使用第三方库）

**推荐库**：
- `github.com/rwcarlsen/goexif/exif` - 标准 EXIF 解析
- `github.com/dsoprea/go-exif/v3` - 更全面的支持

**代码示例**：
```go
import (
    "github.com/rwcarlsen/goexif/exif"
)

type PhotoExif struct {
    DateTime     time.Time
    Make         string
    Model        string
    Width        int
    Height       int
    Orientation  int
    ISO          int
    FNumber      float64
    ExposureTime string
    FocalLength  float64
    GPSLatitude  float64
    GPSLongitude float64
    GPSAltitude  float64
    Software     string
    ExifJSON     string  // 完整 EXIF 的 JSON
}

func ExtractExif(filePath string) (*PhotoExif, error) {
    // 打开文件
    f, err := os.Open(filePath)
    if err != nil {
        return nil, err
    }
    defer f.Close()

    // 解码 EXIF
    x, err := exif.Decode(f)
    if err != nil {
        return nil, err
    }

    result := &PhotoExif{}

    // 提取时间
    dt, _ := x.DateTime()
    result.DateTime = dt

    // 提取相机信息
    make, _ := x.Get(exif.Make)
    result.Make = make.StringVal()

    // 提取 GPS
    lat, lon, _ := x.LatLong()
    result.GPSLatitude = lat
    result.GPSLongitude = lon

    // ... 其他字段

    return result, nil
}
```

---

## 七、注意事项和最佳实践

### 7.1 性能优化

- ✅ **缓存 EXIF**：提取后保存到数据库，避免重复读取
- ✅ **批量处理**：扫描时批量提取 EXIF，减少 IO
- ✅ **异步处理**：EXIF 提取可以异步进行，不阻塞主流程

### 7.2 容错处理

- ✅ **EXIF 损坏**：捕获异常，使用兜底值
- ✅ **格式不标准**：兼容多种时间格式
- ✅ **部分缺失**：缺失字段设为 NULL，不影响其他字段

### 7.3 隐私保护

- ✅ **GPS 可选**：允许用户配置是否提取 GPS
- ✅ **敏感信息**：不提取 UserComment、ImageDescription（可能包含敏感信息）
- ✅ **数据脱敏**：Web 界面显示时可选择隐藏精确 GPS

### 7.4 数据完整性

- ✅ **原始 JSON**：保存完整 EXIF JSON，便于后续扩展
- ✅ **版本兼容**：EXIF 标准更新时可以重新解析 JSON
- ✅ **审计日志**：记录 EXIF 提取时间和版本

---

## 八、待确认的问题

### 需要你确认：

**1. EXIF 提取深度**
- A. 只提取必需字段（时间、GPS、尺寸）- 快速
- B. 提取所有常用字段（包含拍摄参数）- 推荐 ✅
- C. 提取完整 EXIF（所有字段）- 详细但慢

**2. GPS 隐私**
- A. 始终提取 GPS（用于地点识别）
- B. 可配置（允许用户禁用 GPS 提取）- 推荐 ✅
- C. 不提取 GPS

**3. EXIF 缺失处理**
- A. 完全跳过没有 EXIF 的照片
- B. 尝试使用文件时间兜底 - 推荐 ✅
- C. 手动补充时间信息

**4. 截图识别策略**
- A. 仅通过 EXIF Software 字段识别
- B. EXIF + 分辨率 + 文件名 综合判断 - 推荐 ✅
- C. 完全依赖 AI 识别

**5. Orientation 处理**
- A. 存储时自动旋转图片（修改原文件）
- B. 仅记录 orientation，渲染时旋转 - 推荐 ✅
- C. 忽略 orientation

---

## 九、总结

### 已明确的内容
- ✅ 提取哪些 EXIF 字段
- ✅ 数据库存储结构
- ✅ 特殊情况处理策略
- ✅ EXIF 在各功能中的应用
- ✅ 实现技术方案（Golang 库）

### 待确认的内容
- [ ] EXIF 提取深度
- [ ] GPS 隐私配置
- [ ] EXIF 缺失兜底策略
- [ ] 截图识别策略
- [ ] Orientation 处理方式

**你对哪些方面有不同想法？或者我推荐的方案是否可以接受？**
