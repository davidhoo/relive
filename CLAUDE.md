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

### relive-analyzer (Offline Analysis Tool)
```bash
# Build
cd backend
go build -o relive-analyzer ./cmd/relive-analyzer

# Usage
./relive-analyzer check -db export.db                                    # Check database status
./relive-analyzer estimate -config configs/analyzer.yaml -db export.db    # Estimate cost/time
./relive-analyzer analyze -config configs/analyzer.yaml -db export.db     # Run batch analysis
./relive-analyzer analyze -config configs/analyzer.yaml -db export.db -workers 10  # Custom concurrency
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
- Key tables: photos, devices, display_records, app_config, users, cities

### AI Provider System

Providers implement the `provider.AIProvider` interface:
```go
type AIProvider interface {
    Analyze(request *AnalyzeRequest) (*AnalyzeResult, error)
    Name() string
    IsAvailable() bool
}
```

Supported providers: `ollama`, `qwen`, `openai`, `vllm`, `hybrid`
Configured in `config.dev.yaml` under `ai:` section.

### Device Model (Simplified)

The `Device` model has been simplified to use a single `device_type` field:

**Device Types:**
- `embedded` - 嵌入式设备（电子相框等）
- `mobile` - 移动端（手机、平板）
- `web` - Web 浏览器
- `offline` - 离线分析程序
- `service` - 后台服务

**Removed fields:** hardware_model, platform, screen_width, screen_height, firmware_version, mac_address

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

### Unified Version Management (2026-03-05)
- 单一 VERSION 文件管理所有组件版本号
- Go 使用 `//go:embed` 读取，前端使用 Vite 注入
- Health API 返回正确版本号，不再硬编码
- analyzer 版本与主程序一致

### Simplified Device Management (2026-03-05)
- Device type and platform merged into single `device_type` field
- Values: `embedded`, `mobile`, `web`, `offline`, `service`
- Removed fields: `hardware_model`, `screen_width`, `screen_height`, `firmware_version`, `mac_address`
- Simplified device creation form with just name, type, and description

### VLLM Concurrent Analysis (2025-03-03)
- VLLM provider supports concurrent batch analysis
- Configurable concurrency (default: 5, configurable via `vllm_concurrency`)
- Improves batch analysis speed by 3-5x

### AI Service Hot Reload (2025-03-03)
- AI configuration changes apply immediately without restart
- ConfigHandler reinitializes AI service dynamically
- AIHandler updates its service reference automatically

### Offline Geocoding with Auto Import (2025-03-03)
- Docker container auto-imports city data on first startup
- Place `cities500.txt` in `./data/backend/` directory
- Configure via `config.prod.yaml` geocode section
- Supports offline, amap, nominatim, and hybrid modes

### Async Photo Scanning (2025-03-03)
- Photo scanning uses async task system to prevent timeouts
- Supports both scan and rebuild operations
- Frontend polls for progress updates

## Docker Geocoding Setup

### Automatic City Data Import
1. Download GeoNames data:
   ```bash
   cd data/backend
   wget https://download.geonames.org/export/dump/cities500.zip
   unzip cities500.zip
   ```

2. Start container - data imports automatically on first run

3. Configure geocode provider in `config.prod.yaml`:
   ```yaml
   geocode:
     provider: "offline"  # or "hybrid" with fallback
     offline:
       max_distance: 100
   ```

See `docs/docker-geocode.md` for detailed documentation.
