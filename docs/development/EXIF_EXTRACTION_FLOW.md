# EXIF 信息提取时机说明

## 流程概览

### 📸 扫描照片阶段（ScanPhotos）
**时机：** 用户点击"扫描照片"按钮时

**提取的信息：**
```
✅ 文件信息
   ├─ 文件路径（FilePath）
   ├─ 文件名（FileName）
   ├─ 文件大小（FileSize）
   └─ 文件哈希（FileHash）

✅ EXIF 元数据（通过 util.ExtractEXIF）
   ├─ 拍摄时间（TakenAt）
   ├─ 相机型号（CameraModel）
   ├─ 图片尺寸（Width, Height）
   ├─ 旋转方向（Orientation）
   ├─ GPS 经度（GPSLatitude）
   └─ GPS 纬度（GPSLongitude）

❌ 不提取
   ├─ AI 描述（Description）
   ├─ 标题（Caption）
   ├─ 标签（Tags）
   ├─ 评分（Scores）
   └─ 分类（Category）
```

**代码位置：** `backend/internal/service/photo_service.go:154-197`

```go
func (s *photoService) processPhoto(filePath string, info os.FileInfo) (*model.Photo, error) {
    // 计算文件哈希
    fileHash, err := util.HashFile(filePath)

    // 提取 EXIF 信息 ← 在这里！
    exifData, err := util.ExtractEXIF(filePath)

    // 构建 Photo 对象（保存到数据库）
    photo := &model.Photo{
        FilePath:     filePath,
        TakenAt:      exifData.TakenAt,      // EXIF: 拍摄时间
        CameraModel:  exifData.CameraModel,  // EXIF: 相机型号
        Width:        width,                  // EXIF: 宽度
        Height:       height,                 // EXIF: 高度
        Orientation:  exifData.Orientation,  // EXIF: 方向
        GPSLatitude:  exifData.GPSLatitude,  // EXIF: GPS 纬度
        GPSLongitude: exifData.GPSLongitude, // EXIF: GPS 经度
        // AI 相关字段在这时都是空的
    }

    return photo, nil
}
```

---

### 🤖 AI 分析阶段（AnalyzePhoto）
**时机：** 用户点击"AI 分析"按钮时（单张或批量）

**使用的信息：**
```
✅ 从数据库读取的 EXIF 信息（已在扫描时保存）
   ├─ TakenAt     → 作为上下文提供给 AI
   ├─ Location    → 作为上下文提供给 AI
   └─ CameraModel → 作为上下文提供给 AI

✅ 生成的 AI 分析结果
   ├─ 描述（Description）
   ├─ 标题（Caption）
   ├─ 主分类（MainCategory）
   ├─ 标签（Tags）
   ├─ 记忆评分（MemoryScore）
   ├─ 美学评分（BeautyScore）
   └─ 综合评分（OverallScore）
```

**代码位置：** `backend/internal/service/ai_service.go:188-267`

```go
func (s *aiService) AnalyzePhoto(photoID uint) error {
    // 获取照片信息（包含之前扫描时提取的 EXIF）
    photo, err := s.photoRepo.GetByID(photoID)

    // 构建分析请求（使用已有的 EXIF 信息）
    req := &provider.AnalyzeRequest{
        ImageData: processedData,
        ExifInfo: &provider.ExifInfo{
            DateTime: photo.TakenAt,      // 使用扫描时提取的
            City:     photo.Location,      // 使用扫描时提取的
            Model:    photo.CameraModel,   // 使用扫描时提取的
        },
    }

    // 调用 AI 分析
    result, err := s.provider.Analyze(req)

    // 更新照片记录（添加 AI 分析结果）
    photo.AIAnalyzed = true
    photo.Description = result.Description    // AI 生成
    photo.Caption = result.Caption            // AI 生成
    photo.MainCategory = result.MainCategory  // AI 生成
    photo.Tags = result.Tags                  // AI 生成
    photo.MemoryScore = result.MemoryScore    // AI 生成
    photo.BeautyScore = result.BeautyScore    // AI 生成
    photo.OverallScore = calculated           // 计算得出

    return nil
}
```

---

## 数据流图

```
用户操作：扫描照片
    ↓
┌─────────────────────────────────────┐
│ 1. 扫描照片 (ScanPhotos)           │
│    - 遍历目录                       │
│    - 读取文件信息                   │
│    - 提取 EXIF 元数据 ✓             │
│    - 保存到数据库                   │
└─────────────────────────────────────┘
    ↓
┌─────────────────────────────────────┐
│ 数据库 - Photo 表                   │
│    ✅ FilePath, FileHash            │
│    ✅ TakenAt, CameraModel          │
│    ✅ Width, Height, Orientation    │
│    ✅ GPS Latitude, Longitude       │
│    ❌ Description (空)              │
│    ❌ Tags (空)                     │
│    ❌ Scores (0)                    │
└─────────────────────────────────────┘
    ↓
用户操作：AI 分析
    ↓
┌─────────────────────────────────────┐
│ 2. AI 分析 (AnalyzePhoto)          │
│    - 读取照片 EXIF（已保存）       │
│    - 将 EXIF 作为上下文提供给 AI   │
│    - AI 生成描述、标签、评分       │
│    - 更新数据库                     │
└─────────────────────────────────────┘
    ↓
┌─────────────────────────────────────┐
│ 数据库 - Photo 表（更新后）         │
│    ✅ 所有 EXIF 信息（不变）        │
│    ✅ Description (AI 生成)         │
│    ✅ Tags (AI 生成)                │
│    ✅ Scores (AI 生成)              │
└─────────────────────────────────────┘
```

---

## 关键点总结

### ✅ EXIF 在扫描时提取
1. **原因：** EXIF 是照片文件的固有属性，不需要 AI
2. **优势：**
   - 快速：不需要等待 AI 处理
   - 可靠：直接从文件读取
   - 独立：即使不使用 AI 也能获取
3. **包含信息：**
   - 拍摄时间、地点（GPS）
   - 相机型号、参数
   - 图片尺寸、方向

### 🤖 AI 在分析时使用 EXIF
1. **作用：** 将 EXIF 信息作为上下文提供给 AI
2. **帮助：** AI 可以基于时间、地点生成更准确的描述
3. **示例：**
   ```
   EXIF: 2025-11-15, Beijing, iPhone 15 Pro
   AI 输出: "北京秋天的红叶，用 iPhone 15 Pro 拍摄于颐和园"
   ```

---

## 实际使用示例

### 场景 1: 只扫描不分析
```bash
# 用户扫描了 1000 张照片
POST /api/v1/photos/scan/async

# 结果：数据库中有 1000 条记录
# ✅ 所有 EXIF 信息都已保存
# ❌ AI 相关字段为空
# 💡 用户可以浏览照片、按时间排序、按地点筛选
```

### 场景 2: 先扫描后分析
```bash
# 第一步：扫描
POST /api/v1/photos/scan/async
# → 1000 张照片，EXIF 已提取

# 第二步：AI 分析（批量）
POST /api/v1/ai/analyze/batch
# → AI 读取已保存的 EXIF
# → 生成描述、标签、评分
# → 更新数据库
```

---

## 性能考虑

### 扫描阶段（快）
- EXIF 提取：~10ms/张
- 1000 张照片：~10 秒
- **可以频繁扫描**

### AI 分析阶段（慢）
- AI 处理：~2-5 秒/张
- 1000 张照片：~1-2 小时
- **按需分析，不阻塞扫描**

---

## 代码文件位置

### EXIF 提取工具
```
backend/internal/util/exif.go
├─ ExtractEXIF()     // 提取所有 EXIF 信息
├─ GetImageSize()    // 获取图片尺寸
└─ EXIFData struct   // EXIF 数据结构
```

### 照片扫描服务
```
backend/internal/service/photo_service.go
├─ StartScan()       // 启动异步扫描任务
├─ ScanDirectory()   // 遍历目录
└─ processPhoto()    // 处理单张照片（提取 EXIF）
```

### AI 分析服务
```
backend/internal/service/ai_service.go
├─ AnalyzePhoto()    // 分析单张照片
└─ AnalyzeBatch()    // 批量分析
```

---

**结论：** EXIF 信息在 **扫描照片时** 就已经提取并保存到数据库，AI 分析时只是 **使用** 这些信息作为上下文，并生成额外的语义分析结果。
