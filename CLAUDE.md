# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Relive is an intelligent photo memory frame system that analyzes photos using AI and displays them on various devices. It consists of:
- **Backend**: Go (Gin + GORM + SQLite) - REST API server
- **Frontend**: Vue 3 + TypeScript + Vite + Element Plus - Web management interface
- **relive-analyzer**: Standalone CLI tool for offline AI batch analysis
- **Devices**: Support for multiple hardware platforms (ESP32, Android, iOS, etc.)

## Development Commands

### Root Level (Makefile)
```bash
# Development
make dev              # Interactive dev environment launcher (choose backend/frontend/both)
make dev-backend      # Start backend only (cd backend && go run cmd/relive/main.go --config config.dev.yaml)
make dev-frontend     # Start frontend only (cd frontend && npm run dev)

# Production
make build            # Build Docker images
make deploy           # Local build and deploy
make prod             # Deploy using DockerHub images
make stop             # Stop all services (docker-compose down)
make restart          # Restart services
make logs             # View logs (docker-compose logs -f)

# Testing & Maintenance
make test             # Run backend tests (cd backend && go test -v ./...)
make clean            # Clean build artifacts
make deps             # Install all dependencies
```

### Backend Commands (backend/Makefile)
```bash
cd backend

make build            # Build binary to bin/relive
make run              # Run with dev config (config.dev.yaml)
make test             # Run all tests
make test-coverage    # Generate coverage report (coverage.html)
make lint             # Run golangci-lint
make fmt              # Format with gofmt and goimports
make clean            # Clean build artifacts
make deps             # Download Go modules
```

### Frontend Commands
```bash
cd frontend

npm run dev           # Start dev server (http://localhost:5173)
npm run build         # Type check and build for production
npm run preview       # Preview production build
```

### relive-analyzer (API Analysis Tool)
```bash
# Build
cd backend
go build -o relive-analyzer ./cmd/relive-analyzer

# Generate sample config
./relive-analyzer gen-config > analyzer.yaml

# Usage (API Mode - requires running server)
./relive-analyzer check -config analyzer.yaml                           # Check server connection
./relive-analyzer analyze -config analyzer.yaml                         # Run batch analysis
./relive-analyzer analyze -config analyzer.yaml -workers 10             # Custom concurrency
./relive-analyzer analyze -config analyzer.yaml -verbose                # Verbose logging
./relive-analyzer version                                               # Show version info

# Config file location: analyzer.yaml.example (repository root sample)
```

## Architecture

### Backend Architecture (Layered)

```
HTTP Request → Handler → Service → Repository → Database
                  ↓          ↓           ↓
            Validation   Business    Data Access
                         Logic       (GORM)
```

**Key Layers**:
- **Handler** (`internal/api/v1/handler/`): HTTP request handling, validation, response formatting
- **Service** (`internal/service/`): Business logic, orchestration
- **Repository** (`internal/repository/`): Database access layer (GORM)
- **Model** (`internal/model/`): Data models and DTOs
- **Provider** (`internal/provider/`): AI provider implementations (Ollama, Qwen, OpenAI, VLLM, Hybrid)

**Important Patterns**:
- Repository pattern with interface definitions for testability
- Service layer handles business logic, not repositories
- Handlers use `model.Response` for unified JSON responses
- Configuration via YAML (config.dev.yaml for development)

### Frontend Architecture

```
src/
├── api/           # Axios HTTP clients (photo.ts, config.ts, etc.)
├── views/         # Page components (Photos/, Dashboard/, etc.)
├── layouts/       # Layout components (MainLayout.vue)
├── router/        # Vue Router configuration
├── stores/        # Pinia state management
├── types/         # TypeScript interfaces
├── utils/         # Utility functions (request.ts for HTTP)
└── assets/        # CSS styles with CSS variables
```

**Key Patterns**:
- Composition API with `<script setup lang="ts">`
- API functions in `api/` modules, not inline in components
- Types defined in `types/` matching backend models
- HTTP client configured in `utils/request.ts` with interceptors

### Database (SQLite with GORM)

- Development: `backend/data/relive.db`
- Auto-migration enabled in dev mode (`auto_migrate: true`)
- Key tables: photos, photo_tags, devices, display_records, app_config, users, cities
- `photo_tags` table stores normalized tags (one row per photo-tag pair), dual-written with `photos.tags` for rollback safety

### AI Provider System

Providers implement the `provider.AIProvider` interface:
```go
type AIProvider interface {
    Analyze(request *AnalyzeRequest) (*AnalyzeResult, error)
    AnalyzeBatch(requests []*AnalyzeRequest) ([]*AnalyzeResult, error)
    GenerateCaption(request *AnalyzeRequest) (string, error)
    Name() string
    Cost() float64
    BatchCost() float64
    IsAvailable() bool
    MaxConcurrency() int
    SupportsBatch() bool
    MaxBatchSize() int
}
```

Supported providers: `ollama`, `qwen`, `openai`, `vllm`, `hybrid`
Configured in `config.dev.yaml` under `ai:` section.

### Device Model (Simplified)

The `Device` model and `CreateDeviceRequest` have been simplified to use a single `device_type` field:

**Device Types:**
- `embedded` - 嵌入式设备（电子相框等）
- `mobile` - 移动端（手机、平板）
- `web` - Web 浏览器
- `offline` - 离线分析程序
- `service` - 后台服务

**Admin Device Creation** (`CreateDeviceRequest`): Uses simplified fields - name, device_type, description only.

**Device Registration** (`DeviceRegisterRequest`): Still contains legacy fields (hardware_model, platform, screen_width, screen_height, firmware_version, mac_address) for backward compatibility with existing devices, but only device_id, name, device_type, and ip_address are actively used.

### Configuration System

- **Backend**: YAML config files (config.dev.yaml for dev)
- **Frontend**: Environment variables (.env.development, .env.production)
- **Docker**: .env file for deployment configuration

## Key Files

- `backend/cmd/relive/main.go` - Backend entry point
- `backend/config.dev.yaml` - Development configuration
- `frontend/src/main.ts` - Frontend entry point
- `docker-compose.yml` - Production deployment
- `dev.sh` - Interactive development launcher

## Testing

### Backend Tests
```bash
cd backend
go test -v ./...                    # Run all tests
go test -v ./internal/repository/   # Run specific package
go test -run TestPhotoRepository_Create -v  # Run single test
```

### API Testing (Manual)
```bash
# Health check
curl http://localhost:8080/api/v1/system/health

# List photos
curl "http://localhost:8080/api/v1/photos?page=1&page_size=20"
```

## Common Tasks

### Running Single Test
```bash
cd backend
go test -run TestFunctionName -v ./path/to/package/
```

### Building Backend Binary
```bash
cd backend
go build -o bin/relive cmd/relive/main.go
```

### Type Checking Frontend
```bash
cd frontend
npx vue-tsc --noEmit
```

### Database Inspection (Development)
```bash
sqlite3 backend/data/relive.db ".schema"
sqlite3 backend/data/relive.db "SELECT count(*) FROM photos;"
```

## Development Workflow

1. **Start services**: `make dev` (choose option 3 for both)
2. **Backend runs on**: http://localhost:8080
3. **Frontend runs on**: http://localhost:5173
4. **API prefix**: `/api/v1/`

## Deployment

Production uses Docker Compose with single image:
```bash
make deploy    # Local build and deploy
# or
make prod      # Use DockerHub images
```
- Web UI: http://localhost:8080
- API: http://localhost:8080/api/v1/

## Version Management

版本号使用单一来源管理（Single Source of Truth）：

### 版本文件
- **根目录 `VERSION` 文件**：唯一的版本号来源（格式：`1.0.0`）
- **Go 代码**：通过 `pkg/version` 包读取，使用 `//go:embed` 嵌入
- **前端**：`vite.config.ts` 构建时读取 VERSION 文件，通过 `__APP_VERSION__` 全局变量注入

### 使用方式

**Go 代码中获取版本：**
```go
import "github.com/davidhoo/relive/pkg/version"

// 获取版本号
fmt.Println(version.Version)        // 1.0.0
fmt.Println(version.Info())         // 1.0.0 (built: 2025-03-05T12:00:00Z)
fmt.Println(version.FullInfo())     // 1.0.0, commit: v1.0.0, built: 2025-03-05T12:00:00Z
```

**前端获取版本：**
```typescript
// vite.config.ts 中定义
const appVersion = __APP_VERSION__  // 1.0.0
```

### 发布新版本

1. **更新版本号**：
   ```bash
   echo "1.1.0" > VERSION
   ```

2. **构建前同步版本**（Makefile 会自动处理）：
   ```bash
   make sync-version  # 复制 VERSION 到 backend/pkg/version/VERSION
   ```

3. **Git 打标签**：
   ```bash
   git add VERSION
   git commit -m "chore: bump version to 1.1.0"
   git tag v1.1.0
   git push origin v1.1.0  # 触发 Docker 构建
   ```

### CI/CD 构建信息

Docker 构建时通过 build-args 注入额外信息：
- `VERSION`: Git tag（如 `v1.0.0`）
- `BUILD_TIME`: ISO 8601 格式构建时间

这些信息通过 `-ldflags` 注入到 `version.BuildTime` 和 `version.GitCommit`。

## Recent Features

### Photo Tags Optimization (2026-03-15)
- 标签存储从 `photos.tags` 逗号分隔文本迁移到独立 `photo_tags` 表（双写保留回滚安全）
- `GetTags()` 改为热门标签 + 搜索模式：`GET /photos/tags?q=&limit=15` 返回 `TagsResponse{items: TagWithCount[], total}`
- 标签按照片数量降序排列，支持关键词搜索（LIKE），默认返回 Top 15，上限 50
- 标签筛选从 `LIKE '%tag%'` 改为精确子查询匹配
- 前端 `tags` 类型从 `string` 改为 `string[]`，后端 `Photo.TagList` 字段批量填充
- 前端标签区域：热门标签带数量、"查看所有标签（N）"链接打开标签云弹窗（前 100 热门 + 搜索 300ms debounce）
- 标签云弹窗选中非热门标签 → 临时添加到热门区域末尾（选中态），刷新/导航后消失
- 启动自动迁移：从 `photos.tags` 批量拆分写入 `photo_tags`（手动分页，避免 GORM FindInBatches + SQLite 反引号不兼容）
- 注意：GORM 自定义结构体查询需用 `db.Table("photo_tags").Scan()` 而非 `db.Model(&PhotoTag{}).Find()`

### v1.1.0 (2026-03-14)
- 城市数据内嵌二进制：离线 geocoding 开箱即用，启动自动导入，无需手动下载外部数据
- 登录速率限制：防止暴力破解攻击
- Dashboard 加载优化：前端并行请求 + 后端合并 SQL 查询
- 照片管理页优化：新增 `/photos/counts` 轻量接口，减少 HTTP 请求与 SQL 查询
- 高频查询复合索引：DisplayRecord、Job 等表添加复合索引
- 代码质量优化：错误处理改进、Job 过期清理机制、死代码清除
- Dockerfile Go 编译镜像升级至 1.26-alpine

### Photo Management Enhancements (2026-03-13)
- 照片级永久排除功能：Photo 表新增 `status` 字段（active/excluded），排除后重扫不恢复
- 照片列表支持分类（main_category）和标签（tags）精确筛选
- 照片详情页支持修改分类
- 照片管理页面集成扫描路径配置与批量选择功能
- 缩略图和 GPS 解析后台任务自动过滤 excluded 照片

### Location & Geocoding (2026-03-13)
- 照片位置结构化存储：Photo 表新增 country/province/city/district 字段
- 城市中文名支持：City 表 name_zh 字段，离线 geocode 返回中文地名
- 全量重建 GPS 位置解析 API 及前端入口（复用后台任务基础设施）
- 离线 geocode 海外地址显示格式修正

### Performance & Fixes (2026-03-13)
- 展示策略查询性能优化：消除百年循环和 ListAll 全量加载
- BuildDisplayCanvas 缺少 EXIF 方向校正导致批次图片旋转修复

### ESP32 Firmware v1.0.0 (2026-03-12)
- 双配置源：Office 模式（编译时配置）与 NVS 模式（AP 配网）
- AP 配网门户（SSID: relive, Web 配置页面）
- 定时睡眠调度（HHMM 格式，深度睡眠到下一时间点）
- NTP + 服务器时间校准
- AP 超时退避深度睡眠（30min → 60min → 180min）

### Unified Version Management (2026-03-05)
- 单一 VERSION 文件管理所有组件版本号
- Go 使用 `//go:embed` 读取，前端使用 Vite 注入
- Health API 返回正确版本号，不再硬编码
- analyzer 版本与主程序一致

### Simplified Device Management (2026-03-05)
- Device type and platform merged into single `device_type` field
- Values: `embedded`, `mobile`, `web`, `offline`, `service`
- Admin device creation (`CreateDeviceRequest`) simplified to: name, device_type, description
- Legacy fields in `DeviceRegisterRequest` retained for backward compatibility with existing devices

### VLLM Concurrent Analysis (2025-03-03)
- VLLM provider supports concurrent batch analysis
- Configurable concurrency (default: 5, configurable via `vllm_concurrency`)
- Improves batch analysis speed by 3-5x

### AI Service Hot Reload (2025-03-03)
- AI configuration changes apply immediately without restart
- ConfigHandler reinitializes AI service dynamically
- AIHandler updates its service reference automatically

### Offline Geocoding (2025-03-03)
- Supports offline, amap, nominatim, weibo, and hybrid modes
- Configure via config or Web UI geocode settings

### Async Photo Scanning (2025-03-03)
- Photo scanning uses async task system to prevent timeouts
- Supports both scan and rebuild operations
- Frontend polls for progress updates

## Offline Geocoding

City data is embedded in the binary (via `pkg/geodata/cities_zh.csv.gz`), no external files needed.
On first startup, if the `cities` table has fewer than 1000 rows, data is auto-imported.

To regenerate the embedded data (when GeoNames updates):
```bash
cd backend
# Download source files
wget https://download.geonames.org/export/dump/cities500.zip && unzip cities500.zip
wget https://download.geonames.org/export/dump/alternateNamesV2.zip && unzip alternateNamesV2.zip
# Generate embedded data
go run cmd/gen-geodata/main.go -cities cities500.txt -alt alternateNamesV2.txt -out pkg/geodata/cities_zh.csv.gz
```

Configure geocode provider in Web UI or `config.prod.yaml`:
```yaml
geocode:
  provider: "offline"  # or "hybrid" with fallback
  offline:
    max_distance: 100
```
