# 人像识别 + 向量数据库 + 智能裁切 方案

> 日期：2026-03-31
> **Status:** Candidate
> **Note:** This is a candidate design only. The current `main` branch does not implement this approach.

## 背景

Relive 当前的 AI 分析管线只做 VLM 图文分析（描述、标签、评分），无人脸结构化数据。策展引擎的 `people_spotlight` 通道靠标签关键词匹配，粗糙且无法区分"家人"与"路人"。智能裁切基于边缘+色彩显著性，无人脸感知，有可能裁掉人脸。

**目标**：增加人脸检测→聚类→家人标注→策展加分全链路，同时利用人脸位置信息改进裁切质量，并建立向量基础设施为后续语义搜索铺路。

## 技术选型

| 组件 | 方案 | 理由 |
|------|------|------|
| 人脸引擎 | Python/FastAPI + ONNX Runtime + InsightFace **buffalo_m** | buffalo_m 与 buffalo_l 识别精度相同(91.25%)，检测速度快 1.5-2x |
| 向量存储 | sqlite-vec（CGo 绑定 `sqlite-vec-go-bindings/cgo`） | 与现有 mattn/go-sqlite3 + GORM 兼容，静态链接无需分发 .so |
| 聚类 | Go 后端增量最近质心 | O(n*k) 复杂度，支持增量，无需 Python 依赖 |
| 部署 | Docker sidecar 容器 | ML 服务独立伸缩，HTTP 开销(~1ms)远小于推理时间(~5s/张 CPU) |

## 架构概览

```
┌──────────────┐  HTTP   ┌──────────────────┐
│  Go Backend  │◄───────►│  relive-ml       │
│  (Gin+GORM)  │         │  (FastAPI+ONNX)  │
│              │         │  - InsightFace   │
│  sqlite-vec  │         │  - CLIP (future) │
└──────────────┘         └──────────────────┘
```

---

## 一、数据模型

### 1.1 新增 `faces` 表

```go
// model/face.go
type Face struct {
    ID              uint    `gorm:"primaryKey"`
    CreatedAt       time.Time
    PhotoID         uint    `gorm:"index:idx_face_photo_id;not null"`
    PersonID        *uint   `gorm:"index:idx_face_person_id"`
    BboxX           float64 // 归一化 0-1（相对原图）
    BboxY           float64
    BboxWidth       float64
    BboxHeight      float64
    Confidence      float64 // RetinaFace 检测置信度
    ThumbnailPath   string  // 裁切的人脸缩略图
}
```

### 1.2 新增 `persons` 表

```go
// model/person.go
type Person struct {
    ID                   uint   `gorm:"primaryKey"`
    CreatedAt            time.Time
    UpdatedAt            time.Time
    Name                 string `gorm:"type:varchar(100)"`
    IsFamily             bool   `gorm:"default:false;index:idx_person_is_family"`
    RepresentativeFaceID *uint
    FaceCount            int    // 反范式，聚类变更时更新
    PhotoCount           int
}
```

### 1.3 `photos` 表新增字段

```go
FaceDetectStatus string // 'none'|'pending'|'ready'|'failed'，CHECK 约束
FaceCount        int    // 检测到的人脸数
HasFamily        bool   // 反范式，避免策展时 JOIN faces+persons
```

### 1.4 sqlite-vec 虚拟表

```sql
-- 人脸向量（512d ArcFace）
CREATE VIRTUAL TABLE face_embeddings USING vec0(
    face_id INTEGER PRIMARY KEY, embedding float[512]
);
-- 人物质心向量
CREATE VIRTUAL TABLE person_embeddings USING vec0(
    person_id INTEGER PRIMARY KEY, embedding float[512]
);
-- 图片向量（future CLIP，预建表零成本）
CREATE VIRTUAL TABLE image_embeddings USING vec0(
    photo_id INTEGER PRIMARY KEY, embedding float[512]
);
```

遵循 `FTS5Available` 模式：全局 `SqliteVecAvailable` 标志，扩展加载失败时优雅降级。

**关键文件**：
- `backend/internal/model/face.go`（新建）
- `backend/internal/model/photo.go`（加字段）
- `backend/pkg/database/database.go`（AutoMigrate + 迁移）
- `backend/pkg/database/sqlite_vec.go`（新建，扩展加载+虚拟表创建）

---

## 二、Python ML 服务 (`relive-ml`)

### 2.1 目录结构

```
ml-service/
  Dockerfile
  requirements.txt        # fastapi, uvicorn, onnxruntime, insightface, numpy, pillow
  app/
    main.py               # FastAPI + lifespan（启动时加载模型）
    config.py             # pydantic-settings
    schemas.py            # 请求/响应模型
    models/
      face.py             # InsightFace buffalo_m 封装
    routers/
      face.py             # /api/v1/detect-faces
      health.py           # /api/v1/health
```

### 2.2 API 接口

**POST `/api/v1/detect-faces`**
```json
// Request（同机部署用路径，零拷贝）
{ "image_path": "/photos/2024/01/IMG_001.jpg", "min_confidence": 0.5, "max_faces": 20 }
// Request（远程 GPU 部署用 base64，跨网络传输）
{ "image_base64": "...", "min_confidence": 0.5, "max_faces": 20 }

// Response
{
  "faces": [{
    "bbox": {"x": 0.12, "y": 0.08, "width": 0.15, "height": 0.20},
    "confidence": 0.98,
    "embedding": [0.023, -0.114, ...]
  }],
  "processing_time_ms": 3500
}
```

bbox 归一化 0-1，与分辨率无关。embedding 为 ArcFace 512d。

### 2.3 Docker Compose 集成

```yaml
relive-ml:
  build: ./ml-service
  volumes:
    - ${PHOTOS_PATH}:/photos:ro       # 共享照片目录（只读）
    - ./data/ml-models:/root/.insightface
  environment:
    - ONNX_DEVICE=cpu
    - MODEL_PACK=buffalo_m            # 默认 buffalo_m，可选 buffalo_l/buffalo_s
  healthcheck:
    test: ["CMD", "curl", "-f", "http://localhost:5050/api/v1/health"]
```

Go 后端通过 `ML_SERVICE_URL=http://relive-ml:5050` 环境变量/配置连接。

---

## 三、Go 后端扩展

### 3.1 ML 客户端

```go
// internal/mlclient/client.go（新建）
type MLClient struct { baseURL string; httpClient *http.Client }
func (c *MLClient) DetectFaces(ctx, imageBytes, minConf, maxFaces) (*DetectFacesResponse, error)
func (c *MLClient) IsAvailable(ctx) bool
```

### 3.2 Repository 层

```go
// repository/face_repo.go（新建）
type FaceRepository interface {
    Create(face *model.Face) error
    GetByPhotoID(photoID uint) ([]*model.Face, error)
    GetByPersonID(personID uint, page, pageSize int) ([]*model.Face, int64, error)
    UpdatePersonID(faceIDs []uint, personID *uint) error
    StoreEmbedding(faceID uint, embedding []float32) error
    FindNearestFaces(embedding []float32, k int, threshold float64) ([]FaceMatch, error)
}

// repository/person_repo.go（新建）
type PersonRepository interface {
    Create/Update/Delete/GetByID/List/ListFamily
    MergePersons(targetID uint, sourceIDs []uint) error
    StoreCentroid(personID uint, embedding []float32) error
    FindNearestPerson(embedding []float32, threshold float64) (*uint, float64, error)
}
```

### 3.3 Service 层

**FaceService** (`service/face_service.go` 新建)：
- `DetectFaces(photoID)` — 单张检测：读图 → ML 服务 → 存 Face 行 + 向量 → 增量聚类
- `DetectFacesBatch(limit)` / `StartBackgroundDetection()` — 批量/后台（复用现有异步任务模式）
- `ClusterFaces()` — 全量重聚类
- `IncrementalCluster(faceIDs)` — 增量聚类（贪心最近质心，阈值 0.4 余弦距离）

**PersonService** (`service/person_service.go` 新建)：
- CRUD + `SetFamily` / `MergePersons` / `SplitFaces`
- `SetFamily` 时级联更新所有关联照片的 `HasFamily`

### 3.4 Handler + 路由

`handler/face_handler.go`（新建），路由加到 `router.go` 的 `authorized` 组：

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/faces/detect/:photo_id` | 单张检测 |
| POST | `/faces/detect/batch` | 批量检测 |
| POST | `/faces/background/start` | 后台检测 |
| POST | `/faces/background/stop` | 停止检测 |
| GET | `/faces/progress` | 进度统计 |
| GET | `/faces/photo/:photo_id` | 照片的人脸 |
| POST | `/faces/cluster` | 全量聚类 |
| GET | `/persons` | 人物列表（分页，支持 family_only 筛选） |
| GET | `/persons/family` | 家人列表 |
| PUT | `/persons/:id/name` | 改名 |
| PUT | `/persons/:id/family` | 标记/取消家人 |
| POST | `/persons/merge` | 合并人物 |
| GET | `/persons/:id/photos` | 人物照片 |

### 3.5 配置

```yaml
ml:
  enabled: true                        # false = 完全禁用人脸功能
  service_url: "http://relive-ml:5050"
  timeout: 30
  min_confidence: 0.5
  cluster_threshold: 0.4               # 余弦距离阈值
  model_pack: "buffalo_m"              # buffalo_m(默认) | buffalo_l | buffalo_s
  use_file_path: true                  # Docker 共享 volume 模式（优先路径传输）
```

---

## 四、智能裁切增强

**现状**：`util/image.go` 的 `GenerateFramePreview` 用 saliency map（边缘+色彩）滑窗选最优裁切。

**增强**：新增 `GenerateFramePreviewWithFaces(img, w, h, faces []FaceBox)`，三个改进：

1. **人脸区域显著性加权**：在 `buildSaliencyMap` 基础上，将每个 face bbox 区域的显著性值提升 5x，使裁切窗口自然趋向人脸
2. **人脸裁切惩罚**：`scoreCropRect` 新增惩罚项——任何人脸被裁切 >5% 时施加强惩罚（`penalty += (1 - overlap) * 2.0`），确保人脸完整
3. **构图中心偏移**：有人脸时，构图偏好中心从图片几何中心移向人脸质心（70% 人脸 + 30% 默认）

**集成点**：`display_daily_service.go` 生成批次时查询照片人脸数据，传入裁切函数。

**关键文件**：
- `backend/internal/util/image.go`（修改 `calculateSmartCropRect` + `scoreCropRect`）
- `backend/internal/util/display_assets.go`（新增 `BuildDisplayCanvasWithFaces`）
- `backend/internal/service/display_daily_service.go`（传入人脸数据）

---

## 五、策展加分集成

### DisplayStrategyConfig 新增

```go
CurationFamilyBonus float64 `json:"curationFamilyBonus,omitempty"` // 默认 30
```

### 评分调整（`event_curation_service.go` ~line 370 后）

```go
if c.photo != nil && c.photo.HasFamily {
    c.adjScore += cfg.CurationFamilyBonus
}
```

两个 bonus 叠加设计：
- `CurationPeopleBonus`（+20）：标签关键词匹配，事件级，粗粒度
- `CurationFamilyBonus`（+30）：人脸识别，照片级，精确
- 家人出现在人物主题事件中 → 双重加分（+50），符合预期

**关键文件**：
- `backend/internal/model/dto.go`（DisplayStrategyConfig 加字段）
- `backend/internal/service/display_config.go`（默认值）
- `backend/internal/service/event_curation_service.go`（评分逻辑）

---

## 六、前端

### 新增页面

| 页面 | 路径 | 功能 |
|------|------|------|
| 人脸识别管理 | `/faces` | 检测进度、启停后台检测、统计 |
| 人物管理 | `/persons` | 人物卡片网格、改名、标记家人、合并 |
| 人物详情 | `/persons/:id` | 所有人脸、所有照片、拆分操作 |

### 照片详情增强

- 照片上叠加人脸框（CSS absolute positioning + 百分比坐标）
- 点击人脸框跳转对应人物

### 新增文件

- `frontend/src/api/face.ts`
- `frontend/src/views/Faces/index.vue`
- `frontend/src/views/Persons/index.vue`
- `frontend/src/views/Persons/Detail.vue`
- `frontend/src/router/index.ts`（加路由）

---

## 七、实施阶段

| 阶段 | 内容 | 依赖 | 备注 |
|------|------|------|------|
| **S1: 数据基础** | 新模型 + sqlite-vec + 迁移 | 无 | |
| **S2: ML 服务** | Python sidecar + Dockerfile + 接口 | 无 | 可并行 S1 |
| **S3: 检测管线** | MLClient + FaceService + Handler | S1+S2 | |
| **S4: 聚类+人物** | 增量聚类 + PersonService + 家人标注 | S3 | |
| **S5: 智能裁切** | 人脸感知裁切增强 | S1 | 可并行 S4 |
| **S6: 策展集成** | CurationFamilyBonus | S4 | |
| **S7: 前端** | 人脸管理+人物管理+照片增强 | S3+S4 | |

---

## 八、验证方式

1. **ML 服务**：`curl -X POST http://localhost:5050/api/v1/detect-faces` 传入测试图片
2. **检测管线**：`POST /api/v1/faces/detect/:id` 后查 `faces` 表和 `face_embeddings` 虚拟表
3. **聚类**：检测多张同一人照片后验证自动归入同一 person
4. **家人加分**：标记家人后用 `/api/v1/display/preview` 对比评分差异
5. **智能裁切**：对比含人脸照片在新旧裁切算法下的 480x640 输出
6. **后端测试**：`cd backend && go test -v ./...`
7. **前端类型检查**：`cd frontend && npx vue-tsc --noEmit`

---

## 参考

- [immich ML 架构](https://github.com/immich-app/immich)：Python/FastAPI + ONNX Runtime + InsightFace buffalo_l + OpenCLIP + PostgreSQL VectorChord
- [sqlite-vec](https://github.com/asg017/sqlite-vec)：SQLite 原生向量搜索扩展
- [InsightFace](https://github.com/deepinsight/insightface)：RetinaFace 检测 + ArcFace 识别

---

## 九、补充设计决策

### 9.1 模型选择：buffalo_m 而非 buffalo_l

| 指标 | buffalo_l | buffalo_m | buffalo_s |
|------|-----------|-----------|-----------|
| 检测 GFLOPS | 10 GF | 2.5 GF | 500 MF |
| 识别精度 (MR-ALL) | 91.25% | **91.25%** | 71.87% |
| 东亚人脸精度 | 74.96% | **74.96%** | 51.03% |
| 下载大小 | 326 MB | 313 MB | 159 MB |
| 内存占用 | ~1.2 GB | ~1.1 GB | ~0.6 GB |
| NAS CPU 耗时/张 | 5-10s | 3-6s | 1-3s |

**buffalo_m 与 buffalo_l 识别精度完全相同**，差异仅在检测模型。对于静态照片（非视频流），buffalo_m 是最优选择。

配置项 `ml.model_pack` 允许用户按硬件选择：
- `buffalo_m`（默认）：平衡精度与速度
- `buffalo_l`：GPU 用户或追求极致检测率
- `buffalo_s`：低功耗 ARM NAS（精度有损，尤其东亚人脸）

### 9.2 性能预估与资源规划

| 硬件 | 模型 | 单张耗时 | 1 万张估时 |
|------|------|----------|-----------|
| Xeon Gold 24C | buffalo_m | ~1.5s | ~4 小时 |
| NAS Celeron J4125 | buffalo_m | ~4s | ~11 小时 |
| NAS Celeron + OpenVINO | buffalo_m | ~1s | ~3 小时 |
| ARM NAS (Cortex-A53) | buffalo_s | ~5-15s | 不建议 buffalo_m |

**ML 服务内存预算**：~1.1 GB（buffalo_m），只加载 det + recognition 模型（跳过 landmarks/genderage 可降至 ~0.7 GB）。

### 9.3 sqlite-vec Go 集成方案

当前项目使用 `mattn/go-sqlite3`（CGo），集成方式：

```go
import (
    sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
    _ "github.com/mattn/go-sqlite3"
)

func init() {
    sqlite_vec.Auto() // 全局注册，静态链接，无需分发 .so
}
```

KNN 查询语法：
```sql
SELECT face_id, distance
FROM face_embeddings
WHERE embedding MATCH ?    -- 查询向量（[]byte, little-endian float32）
  AND k = 10               -- 返回最近 10 个
ORDER BY distance;
```

向量序列化：`sqlite_vec.SerializeFloat32([]float32{...})` → `[]byte`

**降级策略**：`SqliteVecAvailable` 全局标志。扩展加载失败时人脸数据仍可正常存储（Face/Person 表不依赖 vec0），仅 KNN 搜索和聚类不可用，前端提示"向量搜索不可用"。

### 9.4 图片传输优化

Docker 部署时 Go 后端和 ML 服务共享 photos volume：

```yaml
relive-ml:
  volumes:
    - ${PHOTOS_PATH}:/photos:ro    # 只读挂载照片目录
    - ./data/ml-models:/root/.insightface
```

API 支持双模式：
- `image_path`：Docker 内共享路径（同机部署，零拷贝）
- `image_base64`：网络传输模式（远程 GPU 服务器、开发环境等跨机器场景）

```json
// 同机部署：共享 volume，直接传路径
{ "image_path": "/photos/2024/01/IMG_001.jpg", "min_confidence": 0.5 }
// 远程部署：本地 GPU 机器跑 ML 服务，NAS 通过网络调用
{ "image_base64": "...", "min_confidence": 0.5 }
```

配置项 `ml.use_file_path`（默认 true）控制传输模式。设为 false 时自动走 base64。

### 9.5 降级与可选性

整个人脸识别功能可完全禁用：

```yaml
ml:
  enabled: true              # false = 完全禁用人脸功能
  service_url: "http://relive-ml:5050"
```

降级层次：
1. `ml.enabled = false` → 所有人脸相关 API 返回 404，前端隐藏人脸菜单
2. ML 服务不可达 → `FaceService.DetectFaces()` 返回错误，`FaceDetectStatus` 设为 `failed`，不影响其他功能
3. sqlite-vec 不可用 → Face/Person 表正常工作，聚类降级为全量暴力搜索（万级人脸可接受）

### 9.6 管线位置

人脸检测作为**独立管线阶段**，与 AI 分析并行而非串行：

```
照片扫描 → 缩略图生成 → ┬→ AI 分析（VLM）     → 事件聚类
                        └→ 人脸检测（ML 服务）  → 人脸聚类
                        └→ GPS 解析
```

前置条件：`thumbnail_status = 'ready'`（与 AI 分析相同）。
不依赖 AI 分析结果，不依赖 geocode 结果。

### 9.7 relive-analyzer 不集成人脸

离线分析器保持纯 VLM 分析职责，不增加人脸检测能力。原因：
- analyzer 设计为轻量 CLI，不应依赖 ML sidecar
- 人脸检测是服务端功能，与设备管理/策展紧密耦合
- analyzer 提交分析结果后，服务端后台自动触发人脸检测
