> **⚠️ 历史设计文档** — 本文档写于项目设计阶段 (2026-02-28)，内容与当前实现存在大量差异，仅作为开发历史参考。
> 当前文档请参考：[BACKEND_API.md](../BACKEND_API.md)、[ANALYZER_API_MODE.md](../ANALYZER_API_MODE.md)、[CONFIGURATION.md](../CONFIGURATION.md)、[QUICKSTART.md](../../QUICKSTART.md)


# Relive 开发指南

> **阶段性开发文档说明**
>
> 本文档形成于早期开发阶段，部分目录结构、模块数量和 analyzer 工作流示例已不再与当前仓库完全一致。
>
> 当前真值请优先参考：`backend/internal/api/v1/router/router.go`、`frontend/src/router/index.ts`、`docs/BACKEND_API.md`、`docs/ANALYZER_API_MODE.md`。
>
> analyzer 当前唯一推荐模板为仓库根目录 `analyzer.yaml.example`。

> 开发环境搭建、编码规范、工作流程
> 最后更新：2026-02-28
> 版本：v1.0

---

## 目录

- [一、开发环境搭建](#一开发环境搭建)
- [二、项目结构](#二项目结构)
- [三、编码规范](#三编码规范)
- [四、开发工作流](#四开发工作流)
- [五、测试指南](#五测试指南)
- [六、调试技巧](#六调试技巧)
- [七、性能优化](#七性能优化)
- [八、常见问题](#八常见问题)

---

## 一、开发环境搭建

### 1.1 环境要求

**必需工具**：
- **Golang**：1.21+ ([下载](https://go.dev/dl/))
- **Git**：2.30+ ([下载](https://git-scm.com/))
- **Make**：4.0+（macOS/Linux 自带，Windows 需安装）
- **Docker**：20.10+（用于本地测试）([下载](https://www.docker.com/))

**推荐工具**：
- **IDE**：VS Code、GoLand、Vim
- **API 测试**：Postman、curl、httpie
- **数据库工具**：SQLite Browser、DBeaver
- **Git GUI**：SourceTree、GitKraken（可选）

**操作系统**：
- macOS 10.15+
- Linux (Ubuntu 20.04+, Debian 11+)
- Windows 10+ (WSL2 推荐)

### 1.2 克隆仓库

```bash
# 克隆项目
git clone https://github.com/davidhoo/relive.git
cd relive

# 查看分支
git branch -a

# 切换到开发分支
git checkout develop
```

### 1.3 安装依赖

**Golang 依赖**：
```bash
# 下载依赖
go mod download

# 验证依赖
go mod verify

# 整理依赖
go mod tidy
```

**前端依赖**（如果开发前端）：
```bash
cd frontend
npm install
# 或
yarn install
```

### 1.4 配置开发环境

**从示例创建配置文件** `backend/config.dev.yaml`：
```bash
cp backend/config.dev.yaml.example backend/config.dev.yaml
```

然后按需编辑：
```yaml
# 开发环境配置
server:
  host: "0.0.0.0"
  port: 8080
  mode: "debug"  # 启用 debug 模式

database:
  type: "sqlite"
  path: "./dev-data/relive.db"
  auto_migrate: true
  log_mode: true  # 打印 SQL

photos:
  root_path: "./dev-data/photos"  # 使用测试照片目录
  exclude_dirs:
    - ".sync"
    - "@eaDir"

ai:
  provider: "ollama"  # 本地开发使用 Ollama
  ollama:
    endpoint: "http://localhost:11434"
    model: "llava:13b"
    timeout: 60

logging:
  level: "debug"
  file: "./dev-data/logs/relive.log"
  console: true  # 同时输出到控制台
```

**创建目录结构**：
```bash
mkdir -p dev-data/{photos,logs}
mkdir -p dev-data/photos/{2023,2024,2025,2026}
```

### 1.5 VS Code 配置

**安装扩展**：
- Go (golang.go)
- GitLens (eamodio.gitlens)
- REST Client (humao.rest-client)
- SQLite Viewer (alexcvzz.vscode-sqlite)

**工作区配置** `.vscode/settings.json`：
```json
{
  "go.useLanguageServer": true,
  "go.lintTool": "golangci-lint",
  "go.lintOnSave": "package",
  "go.formatTool": "goimports",
  "editor.formatOnSave": true,
  "editor.codeActionsOnSave": {
    "source.organizeImports": true
  },
  "go.testFlags": ["-v"],
  "go.coverOnSave": true
}
```

**调试配置** `.vscode/launch.json`：
```json
{
  "version": "0.2.0",
  "configurations": [
    {
      "name": "Launch Relive Backend",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/backend/cmd/relive",
      "args": [
        "--config", "${workspaceFolder}/backend/config.dev.yaml"
      ],
      "env": {
        "GIN_MODE": "debug"
      }
    },
    {
      "name": "Launch Relive Analyzer",
      "type": "go",
      "request": "launch",
      "mode": "debug",
      "program": "${workspaceFolder}/backend/cmd/relive-analyzer",
      "args": [
        "check",
        "-config", "${workspaceFolder}/analyzer.yaml"
      ]
    }
  ]
}
```

### 1.6 启动开发服务

**方法 1：使用 Make**（推荐）
```bash
# 启动后端服务
make run

# 启动前端服务
make run-frontend

# 同时启动后端和前端
make dev
```

**方法 2：手动启动**
```bash
# 启动后端
go run backend/cmd/relive/main.go --config backend/config.dev.yaml

# 启动前端（新终端）
cd frontend && npm run dev
```

**验证启动**：
```bash
# 测试后端 API
curl http://localhost:8080/api/v1/system/health

# 访问前端
open http://localhost:3000
```

---

## 二、项目结构

### 2.1 后端结构

```
backend/
├── cmd/
│   └── relive/
│       └── main.go                 # 程序入口
├── internal/
│   ├── api/
│   │   └── v1/
│   │       ├── handler/            # HTTP 处理器
│   │       │   ├── photo.go
│   │       │   ├── ai.go
│   │       │   ├── display.go
│   │       │   ├── esp32.go
│   │       │   ├── export.go
│   │       │   └── config.go
│   │       ├── middleware/         # 中间件
│   │       │   ├── auth.go
│   │       │   ├── cors.go
│   │       │   └── logger.go
│   │       └── router.go           # 路由配置
│   ├── service/                    # 业务逻辑层
│   │   ├── photo.go
│   │   ├── ai.go
│   │   ├── display.go
│   │   ├── esp32.go
│   │   ├── export.go
│   │   ├── scanner.go
│   │   └── scheduler.go
│   ├── repository/                 # 数据访问层
│   │   ├── photo.go
│   │   ├── display_record.go
│   │   ├── esp32_device.go
│   │   └── config.go
│   ├── model/                      # 数据模型
│   │   ├── photo.go
│   │   ├── display_record.go
│   │   ├── esp32_device.go
│   │   ├── config.go
│   │   └── dto.go                  # 数据传输对象
│   ├── provider/                   # AI 提供者
│   │   ├── provider.go             # 接口定义
│   │   ├── ollama.go
│   │   ├── qwen.go
│   │   ├── openai.go
│   │   ├── vllm.go
│   │   └── hybrid.go
│   ├── worker/                     # 后台任务
│   │   ├── scanner.go              # 照片扫描
│   │   └── analyzer.go             # AI 分析
│   ├── scheduler/                  # 定时任务
│   │   └── scheduler.go
│   └── util/                       # 工具函数
│       ├── image.go                # 图片处理
│       ├── exif.go                 # EXIF 处理
│       ├── hash.go                 # 哈希计算
│       └── geo.go                  # 地理位置
└── pkg/                            # 公共库（可被外部引用）
    ├── config/                     # 配置管理
    ├── logger/                     # 日志
    ├── database/                   # 数据库
    └── errors/                     # 错误处理
```

### 2.2 前端结构

```
frontend/
├── src/
│   ├── views/                      # 页面
│   │   ├── Home.vue
│   │   ├── Photos.vue
│   │   ├── AIAnalysis.vue
│   │   ├── Display.vue
│   │   ├── ESP32.vue
│   │   └── Settings.vue
│   ├── components/                 # 组件
│   │   ├── PhotoCard.vue
│   │   ├── AIProgress.vue
│   │   ├── DisplayStrategy.vue
│   │   └── DeviceList.vue
│   ├── api/                        # API 调用
│   │   ├── photo.js
│   │   ├── ai.js
│   │   ├── display.js
│   │   └── esp32.js
│   ├── store/                      # 状态管理
│   │   ├── index.js
│   │   └── modules/
│   ├── router/                     # 路由
│   │   └── index.js
│   ├── assets/                     # 静态资源
│   └── App.vue
├── public/
└── package.json
```

### 2.3 relive-analyzer 结构

```
relive-analyzer/
├── cmd/
│   └── analyzer/
│       └── main.go                 # 入口
├── internal/
│   ├── analyzer/                   # 分析服务
│   │   ├── analyzer.go
│   │   └── batch.go
│   ├── provider/                   # 复用 backend 的 provider
│   └── database/                   # 导出/导入数据库
│       ├── export.go
│       └── import.go
├── pkg/
└── config.yaml
```

---

## 三、编码规范

### 3.1 Golang 规范

#### 命名规范

**包名**：小写，简短，单数
```go
package photo    // ✅
package photos   // ❌
package PhotoService  // ❌
```

**文件名**：小写，下划线分隔
```go
photo_service.go      // ✅
ai_provider.go        // ✅
PhotoService.go       // ❌
```

**接口名**：名词或形容词，er 结尾
```go
type Reader interface { ... }       // ✅
type AIProvider interface { ... }   // ✅
type Read interface { ... }         // ❌
```

**结构体**：大驼峰（导出）或小驼峰（私有）
```go
type PhotoService struct { ... }    // ✅ 导出
type photoRepo struct { ... }       // ✅ 私有
type photo_service struct { ... }   // ❌
```

**函数/方法**：大驼峰（导出）或小驼峰（私有）
```go
func GetPhotoByID() { ... }         // ✅ 导出
func calculateScore() { ... }       // ✅ 私有
func get_photo_by_id() { ... }      // ❌
```

#### 注释规范

**包注释**：
```go
// Package photo provides photo management functionality.
// It handles photo scanning, EXIF extraction, and AI analysis.
package photo
```

**函数注释**：
```go
// GetPhotoByID retrieves a photo by its ID from the database.
// It returns an error if the photo is not found.
func GetPhotoByID(id int) (*model.Photo, error) {
    // ...
}
```

**复杂逻辑注释**：
```go
// Calculate composite score (70% memory + 30% beauty)
// This algorithm is based on user preference research
compositeScore := memoryScore*0.7 + beautyScore*0.3
```

#### 错误处理

**使用 errors.New 或 fmt.Errorf**：
```go
import "errors"

// ✅ 简单错误
if photo == nil {
    return nil, errors.New("photo not found")
}

// ✅ 格式化错误
if err != nil {
    return nil, fmt.Errorf("failed to scan photo: %w", err)
}
```

**自定义错误类型**：
```go
// pkg/errors/errors.go
type AppError struct {
    Code    string
    Message string
    Err     error
}

func (e *AppError) Error() string {
    if e.Err != nil {
        return fmt.Sprintf("%s: %s (%v)", e.Code, e.Message, e.Err)
    }
    return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// 使用
return nil, &errors.AppError{
    Code:    "PHOTO_NOT_FOUND",
    Message: "The requested photo does not exist",
}
```

#### 日志规范

```go
import "github.com/sirupsen/logrus"

// 使用结构化日志
log.WithFields(logrus.Fields{
    "photo_id": photoID,
    "file_path": filePath,
}).Info("Scanning photo")

// 错误日志
log.WithError(err).Error("Failed to analyze photo")

// 调试日志
log.Debug("Starting AI analysis")
```

### 3.2 代码格式化

**使用 gofmt 和 goimports**：
```bash
# 格式化所有代码
gofmt -w .

# 整理 import
goimports -w .

# 使用 goimports（推荐，包含 gofmt）
go install golang.org/x/tools/cmd/goimports@latest
```

**配置 golangci-lint** `.golangci.yml`：
```yaml
linters:
  enable:
    - gofmt
    - goimports
    - govet
    - errcheck
    - staticcheck
    - gosimple
    - ineffassign
    - misspell
    - unconvert
    - unparam

linters-settings:
  gofmt:
    simplify: true
  goimports:
    local-prefixes: github.com/davidhoo/relive

run:
  timeout: 5m
  tests: true
```

**运行 lint**：
```bash
# 安装 golangci-lint
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# 运行
golangci-lint run
```

### 3.3 SQL 规范

**使用 GORM**：
```go
// ✅ 使用 Where
db.Where("file_path = ?", filePath).First(&photo)

// ❌ 避免 SQL 注入
db.Raw("SELECT * FROM photos WHERE file_path = '" + filePath + "'")

// ✅ 使用事务
tx := db.Begin()
defer func() {
    if r := recover(); r != nil {
        tx.Rollback()
    }
}()

if err := tx.Create(&photo).Error; err != nil {
    tx.Rollback()
    return err
}

tx.Commit()
```

---

## 四、开发工作流

### 4.1 Git 工作流

**分支策略**：
```
main          # 主分支（生产环境）
  ├── develop     # 开发分支
  │   ├── feature/photo-scanner
  │   ├── feature/ai-provider
  │   └── feature/esp32-api
  └── hotfix/xxx  # 紧急修复
```

**创建特性分支**：
```bash
# 从 develop 创建特性分支
git checkout develop
git pull origin develop
git checkout -b feature/photo-scanner

# 开发...

# 提交代码
git add .
git commit -m "feat: implement photo scanner"

# 推送分支
git push origin feature/photo-scanner
```

**合并到 develop**：
```bash
# 切换到 develop
git checkout develop
git pull origin develop

# 合并特性分支
git merge --no-ff feature/photo-scanner

# 推送
git push origin develop

# 删除特性分支
git branch -d feature/photo-scanner
git push origin --delete feature/photo-scanner
```

### 4.2 Commit 规范

**格式**：
```
<type>(<scope>): <subject>

<body>

<footer>
```

**Type 类型**：
- `feat`: 新功能
- `fix`: 修复 bug
- `docs`: 文档更新
- `style`: 代码格式（不影响功能）
- `refactor`: 重构
- `perf`: 性能优化
- `test`: 测试
- `chore`: 构建/工具/依赖

**示例**：
```bash
# 新功能
git commit -m "feat(scanner): add photo scanner with EXIF extraction"

# 修复 bug
git commit -m "fix(ai): handle timeout error in Ollama provider"

# 文档
git commit -m "docs: update DEPLOYMENT.md with Redis config"

# 重构
git commit -m "refactor(service): extract display logic to separate service"
```

### 4.3 Pull Request 流程

**创建 PR**：
1. 推送特性分支到 GitHub
2. 在 GitHub 上创建 Pull Request
3. 选择 base: `develop` ← compare: `feature/xxx`
4. 填写 PR 描述：
   ```markdown
   ## 功能描述
   实现照片扫描器，支持 EXIF 提取

   ## 改动内容
   - 添加 PhotoScanner 服务
   - 实现 EXIF 解析
   - 添加单元测试

   ## 测试
   - [x] 单元测试通过
   - [x] 本地测试通过
   - [ ] 集成测试通过（待补充）

   ## 相关 Issue
   Closes #123
   ```

**Code Review 检查清单**：
- [ ] 代码符合规范
- [ ] 有足够的测试覆盖
- [ ] 文档已更新
- [ ] 无明显性能问题
- [ ] 无安全漏洞
- [ ] Commit 信息清晰

**合并 PR**：
- 使用 **Squash and merge**（合并为单个 commit）
- 或 **Merge commit**（保留完整历史）

---

## 五、测试指南

### 5.1 单元测试

**测试文件命名**：
```
photo_service.go       # 源文件
photo_service_test.go  # 测试文件
```

**编写测试**：
```go
package service

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestPhotoService_GetPhotoByID(t *testing.T) {
    // 准备
    service := NewPhotoService()
    photoID := 123

    // 执行
    photo, err := service.GetPhotoByID(photoID)

    // 断言
    assert.NoError(t, err)
    assert.NotNil(t, photo)
    assert.Equal(t, photoID, photo.ID)
}

func TestPhotoService_GetPhotoByID_NotFound(t *testing.T) {
    service := NewPhotoService()
    photoID := 999

    photo, err := service.GetPhotoByID(photoID)

    assert.Error(t, err)
    assert.Nil(t, photo)
}
```

**运行测试**：
```bash
# 运行所有测试
go test ./...

# 运行特定包
go test ./internal/service

# 显示详细输出
go test -v ./...

# 生成覆盖率报告
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```

### 5.2 表驱动测试

```go
func TestCalculateCompositeScore(t *testing.T) {
    tests := []struct {
        name         string
        memoryScore  int
        beautyScore  int
        expectedScore int
    }{
        {"High memory, high beauty", 90, 90, 90},
        {"High memory, low beauty", 90, 50, 78},
        {"Low memory, high beauty", 50, 90, 62},
        {"Low memory, low beauty", 50, 50, 50},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            score := calculateCompositeScore(tt.memoryScore, tt.beautyScore)
            assert.Equal(t, tt.expectedScore, score)
        })
    }
}
```

### 5.3 Mock 测试

**使用 testify/mock**：
```go
// internal/repository/mock/photo_repo_mock.go
type MockPhotoRepo struct {
    mock.Mock
}

func (m *MockPhotoRepo) GetByID(id int) (*model.Photo, error) {
    args := m.Called(id)
    if args.Get(0) == nil {
        return nil, args.Error(1)
    }
    return args.Get(0).(*model.Photo), args.Error(1)
}

// 测试中使用
func TestPhotoService_WithMock(t *testing.T) {
    mockRepo := new(MockPhotoRepo)
    service := NewPhotoService(mockRepo)

    // 设置 mock 行为
    photo := &model.Photo{ID: 123, FilePath: "/test.jpg"}
    mockRepo.On("GetByID", 123).Return(photo, nil)

    // 执行测试
    result, err := service.GetPhotoByID(123)

    // 验证
    assert.NoError(t, err)
    assert.Equal(t, photo, result)
    mockRepo.AssertExpectations(t)
}
```

### 5.4 集成测试

```go
// tests/integration/photo_test.go
func TestPhotoAPI_Integration(t *testing.T) {
    // 设置测试数据库
    db := setupTestDB(t)
    defer db.Close()

    // 初始化服务
    router := setupRouter(db)

    // 发送 HTTP 请求
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", "/api/v1/photos/123", nil)
    router.ServeHTTP(w, req)

    // 验证响应
    assert.Equal(t, 200, w.Code)

    var response map[string]interface{}
    json.Unmarshal(w.Body.Bytes(), &response)
    assert.True(t, response["success"].(bool))
}
```

---

## 六、调试技巧

### 6.1 日志调试

```go
import "github.com/sirupsen/logrus"

// 临时添加调试日志
log.WithFields(logrus.Fields{
    "variable": value,
    "state": state,
}).Debug("Debug point")
```

### 6.2 Delve 调试器

```bash
# 安装 Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# 启动调试
dlv debug backend/cmd/relive/main.go

# 设置断点
(dlv) break main.main
(dlv) break service.GetPhotoByID

# 运行
(dlv) continue

# 查看变量
(dlv) print photo
(dlv) locals

# 单步执行
(dlv) next
(dlv) step
```

### 6.3 pprof 性能分析

```go
import _ "net/http/pprof"

func main() {
    // 启用 pprof
    go func() {
        log.Println(http.ListenAndServe("localhost:6060", nil))
    }()

    // ...
}
```

**分析性能**：
```bash
# CPU 分析
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# 内存分析
go tool pprof http://localhost:6060/debug/pprof/heap

# 生成可视化图表
go tool pprof -http=:8081 http://localhost:6060/debug/pprof/profile
```

---

## 七、性能优化

### 7.1 数据库优化

```go
// 使用索引
db.Where("file_path = ?", path).First(&photo)

// 批量插入
db.CreateInBatches(photos, 1000)

// 预加载关联
db.Preload("DisplayRecords").Find(&photos)

// 选择字段
db.Select("id", "file_path", "taken_at").Find(&photos)
```

### 7.2 并发处理

```go
// 使用 worker pool
func processPhotos(photos []*model.Photo) {
    const numWorkers = 10
    jobs := make(chan *model.Photo, len(photos))
    results := make(chan error, len(photos))

    // 启动 workers
    for i := 0; i < numWorkers; i++ {
        go worker(jobs, results)
    }

    // 分发任务
    for _, photo := range photos {
        jobs <- photo
    }
    close(jobs)

    // 收集结果
    for i := 0; i < len(photos); i++ {
        <-results
    }
}

func worker(jobs <-chan *model.Photo, results chan<- error) {
    for photo := range jobs {
        err := processPhoto(photo)
        results <- err
    }
}
```

### 7.3 缓存策略

```go
import "github.com/go-redis/redis/v8"

// Redis 缓存
func (s *PhotoService) GetPhotoByID(id int) (*model.Photo, error) {
    // 先查缓存
    cacheKey := fmt.Sprintf("photo:%d", id)
    cached, err := s.redis.Get(ctx, cacheKey).Result()
    if err == nil {
        var photo model.Photo
        json.Unmarshal([]byte(cached), &photo)
        return &photo, nil
    }

    // 查数据库
    photo, err := s.repo.GetByID(id)
    if err != nil {
        return nil, err
    }

    // 写入缓存
    data, _ := json.Marshal(photo)
    s.redis.Set(ctx, cacheKey, data, 1*time.Hour)

    return photo, nil
}
```

---

## 八、常见问题

### 8.1 依赖问题

**问题**：`go mod download` 很慢

**解决**：使用国内镜像
```bash
go env -w GOPROXY=https://goproxy.cn,direct
go env -w GOSUMDB=sum.golang.google.cn
```

### 8.2 编译问题

**问题**：CGO 编译错误

**解决**：
```bash
# macOS
xcode-select --install

# Linux
sudo apt-get install build-essential

# Windows (使用 WSL)
sudo apt-get install gcc
```

### 8.3 数据库问题

**问题**：SQLite locked

**解决**：
```go
// 设置 busy_timeout
db.Exec("PRAGMA busy_timeout = 5000")

// 或使用 WAL 模式
db.Exec("PRAGMA journal_mode=WAL")
```

---

## 九、Makefile

**创建 Makefile**：
```makefile
.PHONY: all build run test clean

# 变量
APP_NAME=relive
VERSION=$(shell git describe --tags --always --dirty)
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
LDFLAGS=-ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}"

# 默认目标
all: build

# 构建
build:
	go build ${LDFLAGS} -o bin/$(APP_NAME) backend/cmd/relive/main.go

# 运行
run:
	go run backend/cmd/relive/main.go --config backend/config.dev.yaml

# 测试
test:
	go test -v ./...

# 测试覆盖率
test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# 代码检查
lint:
	golangci-lint run

# 格式化
fmt:
	gofmt -w .
	goimports -w .

# 清理
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# 安装依赖
deps:
	go mod download
	go mod tidy

# Docker 构建
docker-build:
	docker build -t $(APP_NAME):$(VERSION) .

# Docker 运行
docker-run:
	docker-compose up -d

# 帮助
help:
	@echo "Relive Development Commands:"
	@echo "  make build          - Build the application"
	@echo "  make run            - Run the application"
	@echo "  make test           - Run tests"
	@echo "  make test-coverage  - Generate coverage report"
	@echo "  make lint           - Run linter"
	@echo "  make fmt            - Format code"
	@echo "  make clean          - Clean build artifacts"
	@echo "  make deps           - Install dependencies"
	@echo "  make docker-build   - Build Docker image"
	@echo "  make docker-run     - Run with Docker Compose"
```

---

**开发指南完成** ✅
**准备开始开发** 🚀
