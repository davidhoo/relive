# 照片旋转建议功能

> 日期：2026-04-23
> 状态：计划中

## 背景

很多照片的 EXIF 方向信息不准确，导致显示时方向错误。用户需要手动逐张旋转调整，效率低下。通过 AI 自动检测照片正确方向，给出批量旋转建议，提升用户体验。

## 目标

1. 自动检测照片正确方向（0°/90°/180°/270°）
2. 按旋转角度聚合，方便批量审核
3. 应用旋转时级联更新人脸缩略图

## 架构

```
┌─────────────────┐     ┌─────────────────┐
│   relive 后端    │────▶│   relive-ml     │
│ OrientationSvc  │     │ /detect-orient  │
└─────────────────┘     └─────────────────┘
        │
        ▼
┌─────────────────────────────┐
│ photo_orientation_suggestions│
└─────────────────────────────┘
        │
        ▼
┌─────────────────┐
│  前端批量审核    │
└─────────────────┘
```

## 实现阶段

### S1: relive-ml 扩展

**目标**：新增方向检测端点

**API 设计**：
```
POST /api/v1/detect-orientation

Request:
{
  "image_path": "/photos/2024/01/IMG_001.jpg"
}

Response:
{
  "rotation": 90,           // 建议额外旋转角度 (0/90/180/270)
  "confidence": 0.96,
  "processing_time_ms": 35
}
```

**模型选择**：
- MobileNetV3-Small（~2.5MB，~20ms 推理）
- 或预训练的方向分类模型

**文件**：
- `ml-service/routers/orientation.py`
- `ml-service/models/orientation.py`

---

### S2: 后端数据模型

**目标**：新建数据库表

**表结构**：
```sql
CREATE TABLE photo_orientation_suggestions (
    id INTEGER PRIMARY KEY,
    photo_id INTEGER NOT NULL UNIQUE,
    suggested_rotation INTEGER NOT NULL CHECK(suggested_rotation IN (0, 90, 180, 270)),
    confidence REAL NOT NULL,
    low_confidence INTEGER DEFAULT 0,
    status TEXT DEFAULT 'pending' CHECK(status IN ('pending', 'applied', 'dismissed')),
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (photo_id) REFERENCES photos(id) ON DELETE CASCADE
);

CREATE INDEX idx_orientation_suggestion_status ON photo_orientation_suggestions(status);
CREATE INDEX idx_orientation_suggestion_rotation ON photo_orientation_suggestions(suggested_rotation);
```

**文件**：
- `backend/internal/model/photo_orientation_suggestion.go`（新建）
- `backend/pkg/database/database.go`（AutoMigrate）

---

### S3: 后端服务层

**目标**：后台任务切片处理

**服务接口**：
```go
type OrientationSuggestionService interface {
    GetTask() *OrientationSuggestionTask
    GetStats() (*OrientationSuggestionStats, error)
    GetBackgroundLogs() []string
    Pause() error
    Resume() error
    Rebuild() error
    RunBackgroundSlice() error

    GetGroups() ([]OrientationSuggestionGroup, error)
    GetDetail(rotation int, page, pageSize int) (*OrientationSuggestionDetail, int64, error)
    Apply(photoIDs []uint) error
    Dismiss(photoIDs []uint) error
}
```

**配置项**：
```yaml
orientation:
  enabled: true
  confidence_threshold: 0.85
  cooldown_seconds: 300
  batch_size: 50
```

**文件**：
- `backend/internal/service/orientation_suggestion_service.go`（新建）
- `backend/pkg/config/config.go`（加配置）
- `backend/internal/repository/photo_orientation_suggestion_repo.go`（新建）

---

### S4: 后端 API 层

**目标**：Handler + 路由

**API 列表**：
| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/photos/orientation-suggestions/groups` | 获取按旋转角度分组的概览 |
| GET | `/photos/orientation-suggestions/detail` | 获取指定旋转角度的照片列表 |
| POST | `/photos/orientation-suggestions/apply` | 批量应用旋转 |
| POST | `/photos/orientation-suggestions/dismiss` | 批量忽略 |
| GET | `/photos/orientation-suggestions/task` | 任务状态 |
| GET | `/photos/orientation-suggestions/stats` | 统计 |
| GET | `/photos/orientation-suggestions/logs` | 后台日志 |
| POST | `/photos/orientation-suggestions/pause` | 暂停 |
| POST | `/photos/orientation-suggestions/resume` | 恢复 |
| POST | `/photos/orientation-suggestions/rebuild` | 重建 |

**文件**：
- `backend/internal/api/v1/handler/orientation_suggestion_handler.go`（新建）
- `backend/internal/api/v1/router/router.go`（加路由）

---

### S5: 人脸缩略图修复

**目标**：旋转时级联更新人脸缩略图

**修改内容**：
1. `GenerateFaceThumbnail` 增加 `manualRotation` 参数
2. `UpdateManualRotation` / `BatchRotate` 级联重建人脸缩略图

**文件**：
- `backend/internal/util/face_thumbnail.go`（修改）
- `backend/internal/service/photo_service.go`（修改）
- `backend/internal/service/people_service.go`（可能需要修改）

---

### S6: 前端类型和 API

**目标**：定义 TypeScript 类型和 API 调用

**类型定义**：
```typescript
// types/photo.ts
export interface OrientationSuggestionGroup {
  suggested_rotation: 0 | 90 | 180 | 270
  count: number
  avg_confidence: number
  low_confidence_count: number
  photos: PhotoPreview[]
}

export interface OrientationSuggestionDetail {
  suggested_rotation: 0 | 90 | 180 | 270
  photos: OrientationSuggestionPhoto[]
  total: number
}

export interface OrientationSuggestionPhoto {
  id: number
  file_name: string
  thumbnail_path: string
  current_rotation: number
  suggested_rotation: number
  confidence: number
  low_confidence: boolean
}

export interface OrientationSuggestionTask {
  status: string
  current_message: string
  processed_count: number
  started_at?: string
  stopped_at?: string
}

export interface OrientationSuggestionStats {
  total: number
  pending: number
  applied: number
  dismissed: number
  low_confidence: number
}
```

**文件**：
- `frontend/src/types/photo.ts`（修改）
- `frontend/src/api/photos.ts`（修改）

---

### S7: 前端组件

**目标**：旋转建议卡片和审核弹窗

**组件**：
1. `OrientationSuggestionCard.vue` - 单个旋转角度的卡片
2. `OrientationSuggestionReviewDialog.vue` - 审核弹窗（含旋转预览）

**功能**：
- 卡片显示旋转角度、照片数量、低置信度标记
- 弹窗显示照片缩略图，CSS 实时预览旋转效果
- 支持批量选择、应用、忽略

**文件**：
- `frontend/src/views/Photos/OrientationSuggestionCard.vue`（新建）
- `frontend/src/views/Photos/OrientationSuggestionReviewDialog.vue`（新建）

---

### S8: 前端页面集成

**目标**：照片管理页面集成旋转建议区域

**修改内容**：
- 照片列表下方新增"照片旋转建议"区域
- 按旋转角度分组显示卡片
- 后台任务控制（暂停/恢复/重建）

**文件**：
- `frontend/src/views/Photos/index.vue`（修改）

---

## 检查清单

| 项目 | 状态 |
|------|------|
| S1: relive-ml 扩展 | ⬜ |
| S2: 后端数据模型 | ⬜ |
| S3: 后端服务层 | ⬜ |
| S4: 后端 API 层 | ⬜ |
| S5: 人脸缩略图修复 | ⬜ |
| S6: 前端类型和 API | ⬜ |
| S7: 前端组件 | ⬜ |
| S8: 前端页面集成 | ⬜ |

## 注意事项

1. **检测目标过滤**：跳过已有 `manual_rotation != 0` 的照片
2. **低置信度处理**：置信度 < 0.85 的标记为 `low_confidence`
3. **异步重建**：批量应用时异步重建缩略图，避免阻塞
4. **预览实现**：前端用 CSS `transform: rotate()` 实时预览
