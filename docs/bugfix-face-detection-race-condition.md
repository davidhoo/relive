# Bug 分析：人脸检测结果被 db.Save() 竞态覆写

## 问题现象

1. 人物管理页面扫描照片，后台日志显示检测到人脸（如 "照片 #19880 检测到 11 张人脸"）
2. 但人物列表不出现新的人物
3. 照片详情页显示 "未检测到人脸"（face_count=0, face_process_status 被回退）

## 根因分析

### 核心问题：`db.Save(photo)` 覆写全部字段

Go GORM 的 `db.Save(struct)` 会写入结构体的**所有字段**（包括零值），而非仅修改过的字段。当多个后台任务并发处理同一张照片时，先读取再写入的模式导致后写入的任务覆盖了先完成任务的更新。

### 竞态时序

```
时间线  │  AI 分析服务                         │  人脸检测服务
────────┼──────────────────────────────────────┼──────────────────────────────────
T1      │  加载 photo (face_count=0,           │
        │    face_process_status="pending")     │
T2      │  开始 AI 分析（耗时 5-30 秒）         │  开始处理同一张 photo
T3      │  ...分析中...                         │  检测到 11 张人脸
T4      │  ...分析中...                         │  事务提交：创建 11 条 face 记录,
        │                                      │    更新 photo: face_count=11,
        │                                      │    face_process_status="ready"
T5      │  AI 分析完成                          │
T6      │  db.Save(photo)  ← 用 T1 时刻的      │
        │  旧数据覆写: face_count=0,            │
        │  face_process_status="pending"        │
────────┴──────────────────────────────────────┴──────────────────────────────────
```

结果：数据库中 face 记录存在（11 条），但 photo 表的 face_count=0、face_process_status="pending"。前端根据 `face_count === 0` 判定为 "未检测到人脸"。

### 受影响的代码位置

| 文件 | 行号 | 函数 | 说明 |
|------|------|------|------|
| `backend/internal/service/ai_service.go` | 624 | `analyzePhotoInternal()` | 单张照片 AI 分析完成后 Save |
| `backend/internal/service/ai_service.go` | 1225 | `runAnalysisTask()` | 批量分析中每张照片结果写入 |
| `backend/internal/service/photo_scan_service.go` | 568 | `processScanFile()` | rebuild 模式更新照片 |
| `backend/internal/service/photo_scan_service.go` | 584 | `processScanFile()` | hash 变更更新照片 |

### 前端触发条件

```typescript
// frontend/src/views/Photos/photoPeopleHelpers.ts:43
if (payload.face_process_status === 'no_face' || payload.face_count === 0) return '未检测到人脸'
```

当 face_count 被覆写为 0 时，无论 face_process_status 是什么值，都会显示 "未检测到人脸"。

## 修复方案

### 原则：每个写操作只更新自己负责的字段

将 `db.Save(photo)`（全量覆写）改为 `UpdateFields(photo.ID, map)`（精确字段更新），消除竞态窗口。

### 修改 1：`ai_service.go` — `analyzePhotoInternal()` (行 624)

**Before:**
```go
photo.AIAnalyzed = true
photo.AIProvider = s.provider.Name()
photo.Description = result.Description
photo.Caption = caption
photo.MainCategory = result.MainCategory
photo.Tags = result.Tags
photo.MemoryScore = int(result.MemoryScore)
photo.BeautyScore = int(result.BeautyScore)
photo.ScoreReason = result.Reason
photo.AnalyzedAt = &now
photo.OverallScore = model.CalcOverallScore(photo.MemoryScore, photo.BeautyScore)

if err := s.photoRepo.Update(photo); err != nil {
```

**After:**
```go
overallScore := model.CalcOverallScore(int(result.MemoryScore), int(result.BeautyScore))

if err := s.photoRepo.UpdateFields(photo.ID, map[string]interface{}{
    "ai_analyzed":   true,
    "ai_provider":   s.provider.Name(),
    "description":   result.Description,
    "caption":       caption,
    "main_category": result.MainCategory,
    "tags":          result.Tags,
    "memory_score":  int(result.MemoryScore),
    "beauty_score":  int(result.BeautyScore),
    "overall_score": overallScore,
    "score_reason":  result.Reason,
    "analyzed_at":   &now,
}); err != nil {
```

### 修改 2：`ai_service.go` — `runAnalysisTask()` (行 1225)

同修改 1 的模式，将批量分析中的 `s.photoRepo.Update(photo)` 改为 `s.photoRepo.UpdateFields(photo.ID, map)`，只写入 AI 相关字段。

### 修改 3：`photo_scan_service.go` — `processScanFile()` rebuild (行 568)

**Before:**
```go
photo.ID = existing.ID
s.preserveAnalysisFields(existing, photo)
if err := s.repo.Update(photo); err != nil {
```

**After:**
用 `UpdateFields` 只更新扫描相关字段（EXIF 元数据、文件信息），不触碰 face_count/face_process_status/top_person_category 等人脸检测字段，也不触碰 AI 分析字段（已由 `preserveAnalysisFields` 逻辑保证，但 `db.Save` 绕过了这个保护）。

需要构建一个 map，包含以下字段：
- 文件信息：file_name, file_path, file_size, file_hash, file_mod_time, file_type
- EXIF：width, height, orientation, taken_at, gps_latitude, gps_longitude, camera_make, camera_model, lens_model, focal_length, aperture, iso, shutter_speed, exposure_compensation
- 地理信息：location, country, province, city, district
- 保留的分析字段（从 existing 复制）：status, thumbnail_path, thumbnail_status, thumbnail_generated_at, geocode_status, geocode_provider, geocoded_at, face_process_status, face_count, top_person_category, description, main_category, tags, ai_analyzed, analyzed_at, ai_provider, caption, memory_score, beauty_score, overall_score, score_reason

### 修改 4：`photo_scan_service.go` — `processScanFile()` hash change (行 584)

同修改 3。

## 验证方案

### 自动化测试
```bash
cd backend
go test -v ./internal/service/ -run TestAI        # AI 服务测试
go test -v ./internal/service/ -run TestPhoto      # 扫描服务测试
go test -v ./internal/service/ -run TestPeople     # 人物服务测试
go test -v ./...                                   # 全量测试
```

### 手动验证（NAS 环境）
1. 启动服务，确保有未分析且未检测人脸的照片
2. 同时触发 AI 分析和人脸检测（或确保两个后台任务并行运行）
3. 等待两个任务都完成
4. 检查照片详情页：face_count 应 > 0，face_process_status 应为 "ready"
5. 检查人物列表：应出现新检测到的人物

### 数据库验证
```sql
-- 检查是否存在 face 记录存在但 photo.face_count=0 的不一致数据
SELECT p.id, p.face_count, p.face_process_status, COUNT(f.id) as actual_faces
FROM photos p
LEFT JOIN faces f ON f.photo_id = p.id
GROUP BY p.id
HAVING p.face_count != COUNT(f.id);
```

## 影响范围

- 修改 2 个文件，4 处调用
- 不涉及数据库 schema 变更
- 不涉及 API 接口变更
- 不涉及前端变更
- 向后兼容，无需数据迁移
