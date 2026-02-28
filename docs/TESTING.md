# Relive 测试策略

> 完整的测试方案和测试用例
> 最后更新：2026-02-28
> 版本：v1.0

---

## 目录

- [一、测试策略](#一测试策略)
- [二、单元测试](#二单元测试)
- [三、集成测试](#三集成测试)
- [四、端到端测试](#四端到端测试)
- [五、性能测试](#五性能测试)
- [六、安全测试](#六安全测试)
- [七、测试数据](#七测试数据)
- [八、CI/CD 集成](#八cicd-集成)

---

## 一、测试策略

### 1.1 测试金字塔

```
        /\
       /  \        E2E 测试 (5%)
      /    \       - 完整业务流程
     /------\      - 用户场景测试
    /        \
   /          \    集成测试 (15%)
  /            \   - API 接口测试
 /--------------\  - 数据库交互测试
/                \
/                 \ 单元测试 (80%)
/                  \ - 函数级别测试
--------------------  - Mock 依赖
```

### 1.2 测试覆盖率目标

| 层级 | 目标覆盖率 | 说明 |
|------|-----------|------|
| **核心业务逻辑** | ≥ 90% | Service 层、Provider 层 |
| **API Handler** | ≥ 80% | HTTP 处理器 |
| **Repository** | ≥ 85% | 数据访问层 |
| **Util** | ≥ 90% | 工具函数 |
| **总体** | ≥ 80% | 整个项目 |

### 1.3 测试分类

**按照测试类型**：
- **Unit Test**（单元测试）- 最基础，快速执行
- **Integration Test**（集成测试）- 测试模块间交互
- **E2E Test**（端到端测试）- 模拟真实用户场景
- **Performance Test**（性能测试）- 压力测试、基准测试
- **Security Test**（安全测试）- 漏洞扫描、渗透测试

**按照测试阶段**：
- **开发阶段**：单元测试、集成测试
- **提交阶段**：自动化测试（CI）
- **发布前**：完整回归测试、性能测试
- **生产后**：监控、日志分析

---

## 二、单元测试

### 2.1 测试结构

```
backend/
├── internal/
│   ├── service/
│   │   ├── photo_service.go
│   │   ├── photo_service_test.go      # 单元测试
│   │   ├── ai_service.go
│   │   └── ai_service_test.go
│   ├── repository/
│   │   ├── photo_repo.go
│   │   ├── photo_repo_test.go
│   │   └── mock/                       # Mock 实现
│   │       └── photo_repo_mock.go
│   └── util/
│       ├── image.go
│       └── image_test.go
└── tests/
    ├── fixtures/                        # 测试数据
    │   ├── photos/
    │   └── databases/
    └── testutil/                        # 测试工具
        ├── setup.go
        └── assertions.go
```

### 2.2 Service 层测试

**测试示例** `internal/service/photo_service_test.go`：
```go
package service

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/mock"
    "github.com/davidhoo/relive/internal/model"
    mockRepo "github.com/davidhoo/relive/internal/repository/mock"
)

func TestPhotoService_GetPhotoByID(t *testing.T) {
    // 准备 mock
    mockRepo := new(mockRepo.MockPhotoRepo)
    service := NewPhotoService(mockRepo)

    // 测试数据
    expectedPhoto := &model.Photo{
        ID:       123,
        FilePath: "/photos/2023/01/IMG_0001.jpg",
        TakenAt:  time.Now(),
    }

    // 设置 mock 行为
    mockRepo.On("GetByID", 123).Return(expectedPhoto, nil)

    // 执行
    photo, err := service.GetPhotoByID(123)

    // 断言
    assert.NoError(t, err)
    assert.NotNil(t, photo)
    assert.Equal(t, expectedPhoto.ID, photo.ID)
    assert.Equal(t, expectedPhoto.FilePath, photo.FilePath)

    // 验证 mock 调用
    mockRepo.AssertExpectations(t)
}

func TestPhotoService_GetPhotoByID_NotFound(t *testing.T) {
    mockRepo := new(mockRepo.MockPhotoRepo)
    service := NewPhotoService(mockRepo)

    // 模拟未找到
    mockRepo.On("GetByID", 999).Return(nil, errors.New("not found"))

    photo, err := service.GetPhotoByID(999)

    assert.Error(t, err)
    assert.Nil(t, photo)
    mockRepo.AssertExpectations(t)
}
```

### 2.3 表驱动测试

**测试评分算法**：
```go
func TestCalculateCompositeScore(t *testing.T) {
    tests := []struct {
        name          string
        memoryScore   int
        beautyScore   int
        expectedScore int
    }{
        {
            name:          "Both high",
            memoryScore:   90,
            beautyScore:   90,
            expectedScore: 90,
        },
        {
            name:          "High memory, low beauty",
            memoryScore:   90,
            beautyScore:   50,
            expectedScore: 78, // 90*0.7 + 50*0.3 = 78
        },
        {
            name:          "Low memory, high beauty",
            memoryScore:   50,
            beautyScore:   90,
            expectedScore: 62, // 50*0.7 + 90*0.3 = 62
        },
        {
            name:          "Both low",
            memoryScore:   50,
            beautyScore:   50,
            expectedScore: 50,
        },
        {
            name:          "Edge case: zero",
            memoryScore:   0,
            beautyScore:   0,
            expectedScore: 0,
        },
        {
            name:          "Edge case: max",
            memoryScore:   100,
            beautyScore:   100,
            expectedScore: 100,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            score := calculateCompositeScore(tt.memoryScore, tt.beautyScore)
            assert.Equal(t, tt.expectedScore, score)
        })
    }
}
```

### 2.4 AI Provider 测试

**测试 Ollama Provider**：
```go
func TestOllamaProvider_Analyze(t *testing.T) {
    // 使用 httptest 模拟 Ollama API
    server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // 验证请求
        assert.Equal(t, "POST", r.Method)
        assert.Equal(t, "/api/generate", r.URL.Path)

        // 模拟响应
        response := map[string]interface{}{
            "response": "这是一张美丽的风景照片，拍摄于日落时分。",
        }
        json.NewEncoder(w).Encode(response)
    }))
    defer server.Close()

    // 创建 provider
    provider := &OllamaProvider{
        endpoint: server.URL,
        model:    "llava:13b",
    }

    // 执行测试
    result, err := provider.Analyze(&AnalyzeRequest{
        ImageData: base64Image,
    })

    // 断言
    assert.NoError(t, err)
    assert.NotNil(t, result)
    assert.NotEmpty(t, result.Description)
}
```

### 2.5 工具函数测试

**测试图片处理**：
```go
func TestImagePreprocessor_ProcessForAI(t *testing.T) {
    preprocessor := &ImagePreprocessor{
        MaxLongSide: 1024,
        JPEGQuality: 85,
    }

    // 读取测试图片
    testImagePath := "../../tests/fixtures/photos/test_image.jpg"

    // 执行处理
    compressed, err := preprocessor.ProcessForAI(testImagePath)

    // 断言
    assert.NoError(t, err)
    assert.NotNil(t, compressed)
    assert.Less(t, len(compressed), 500*1024) // < 500KB

    // 验证图片尺寸
    img, err := jpeg.Decode(bytes.NewReader(compressed))
    assert.NoError(t, err)
    bounds := img.Bounds()
    maxSide := max(bounds.Dx(), bounds.Dy())
    assert.LessOrEqual(t, maxSide, 1024)
}
```

---

## 三、集成测试

### 3.1 API 测试

**测试框架设置** `tests/integration/setup_test.go`：
```go
package integration

import (
    "testing"
    "github.com/gin-gonic/gin"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
)

var (
    testDB     *gorm.DB
    testRouter *gin.Engine
)

func setupTestDB(t *testing.T) *gorm.DB {
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
    if err != nil {
        t.Fatalf("Failed to connect to test database: %v", err)
    }

    // 自动迁移
    db.AutoMigrate(&model.Photo{}, &model.DisplayRecord{}, ...)

    return db
}

func setupTestRouter(db *gorm.DB) *gin.Engine {
    gin.SetMode(gin.TestMode)
    router := gin.New()

    // 初始化服务
    photoRepo := repository.NewPhotoRepository(db)
    photoService := service.NewPhotoService(photoRepo)
    photoHandler := handler.NewPhotoHandler(photoService)

    // 注册路由
    v1 := router.Group("/api/v1")
    {
        v1.GET("/photos/:id", photoHandler.GetPhoto)
        v1.POST("/photos/scan", photoHandler.ScanPhotos)
        // ...
    }

    return router
}

func TestMain(m *testing.M) {
    // 测试前准备
    testDB = setupTestDB(nil)
    testRouter = setupTestRouter(testDB)

    // 运行测试
    code := m.Run()

    // 清理
    sqlDB, _ := testDB.DB()
    sqlDB.Close()

    os.Exit(code)
}
```

**API 测试示例** `tests/integration/photo_api_test.go`：
```go
func TestPhotoAPI_GetPhoto(t *testing.T) {
    // 准备测试数据
    photo := &model.Photo{
        FilePath: "/photos/test.jpg",
        TakenAt:  time.Now(),
    }
    testDB.Create(photo)

    // 发送请求
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/photos/%d", photo.ID), nil)
    testRouter.ServeHTTP(w, req)

    // 验证响应
    assert.Equal(t, 200, w.Code)

    var response map[string]interface{}
    json.Unmarshal(w.Body.Bytes(), &response)

    assert.True(t, response["success"].(bool))
    assert.NotNil(t, response["data"])
}

func TestPhotoAPI_ScanPhotos(t *testing.T) {
    // 准备测试照片目录
    testPhotosDir := "../../tests/fixtures/photos"

    // 构造请求
    requestBody := map[string]interface{}{
        "path": testPhotosDir,
    }
    jsonBody, _ := json.Marshal(requestBody)

    // 发送请求
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("POST", "/api/v1/photos/scan", bytes.NewReader(jsonBody))
    req.Header.Set("Content-Type", "application/json")
    testRouter.ServeHTTP(w, req)

    // 验证响应
    assert.Equal(t, 200, w.Code)

    var response map[string]interface{}
    json.Unmarshal(w.Body.Bytes(), &response)

    assert.True(t, response["success"].(bool))
    data := response["data"].(map[string]interface{})
    assert.Greater(t, data["scanned_count"].(float64), 0.0)
}
```

### 3.2 数据库测试

**测试 Repository**：
```go
func TestPhotoRepository_Create(t *testing.T) {
    db := setupTestDB(t)
    repo := repository.NewPhotoRepository(db)

    photo := &model.Photo{
        FilePath:    "/photos/test.jpg",
        FileHash:    "abc123",
        TakenAt:     time.Now(),
        MemoryScore: 85,
        BeautyScore: 90,
    }

    // 创建
    err := repo.Create(photo)
    assert.NoError(t, err)
    assert.NotZero(t, photo.ID)

    // 验证插入
    var found model.Photo
    db.First(&found, photo.ID)
    assert.Equal(t, photo.FilePath, found.FilePath)
    assert.Equal(t, photo.FileHash, found.FileHash)
}

func TestPhotoRepository_GetByFileHash(t *testing.T) {
    db := setupTestDB(t)
    repo := repository.NewPhotoRepository(db)

    // 插入测试数据
    photo := &model.Photo{
        FilePath: "/photos/test.jpg",
        FileHash: "unique-hash-123",
    }
    db.Create(photo)

    // 查询
    found, err := repo.GetByFileHash("unique-hash-123")

    assert.NoError(t, err)
    assert.NotNil(t, found)
    assert.Equal(t, photo.ID, found.ID)
}
```

---

## 四、端到端测试

### 4.1 完整流程测试

**测试场景：扫描 → 分析 → 展示**：
```go
func TestE2E_PhotoWorkflow(t *testing.T) {
    // 1. 扫描照片
    t.Run("Scan photos", func(t *testing.T) {
        w := httptest.NewRecorder()
        body := `{"path": "/test-photos"}`
        req, _ := http.NewRequest("POST", "/api/v1/photos/scan", strings.NewReader(body))
        req.Header.Set("Content-Type", "application/json")
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 200, w.Code)
    })

    // 2. 等待扫描完成
    time.Sleep(2 * time.Second)

    // 3. 获取照片列表
    var photos []model.Photo
    t.Run("Get photo list", func(t *testing.T) {
        w := httptest.NewRecorder()
        req, _ := http.NewRequest("GET", "/api/v1/photos?limit=10", nil)
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 200, w.Code)

        var response map[string]interface{}
        json.Unmarshal(w.Body.Bytes(), &response)
        data := response["data"].(map[string]interface{})
        items := data["items"].([]interface{})
        assert.Greater(t, len(items), 0)

        // 解析 photos
        jsonData, _ := json.Marshal(items)
        json.Unmarshal(jsonData, &photos)
    })

    // 4. 分析一张照片
    photoID := photos[0].ID
    t.Run("Analyze photo", func(t *testing.T) {
        w := httptest.NewRecorder()
        body := fmt.Sprintf(`{"photo_id": %d}`, photoID)
        req, _ := http.NewRequest("POST", "/api/v1/ai/analyze", strings.NewReader(body))
        req.Header.Set("Content-Type", "application/json")
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 200, w.Code)
    })

    // 5. 等待分析完成
    time.Sleep(5 * time.Second)

    // 6. 获取展示照片
    t.Run("Get display photo", func(t *testing.T) {
        w := httptest.NewRecorder()
        req, _ := http.NewRequest("GET", "/api/v1/display/photo", nil)
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 200, w.Code)

        var response map[string]interface{}
        json.Unmarshal(w.Body.Bytes(), &response)
        data := response["data"].(map[string]interface{})
        assert.NotNil(t, data["photo_id"])
    })
}
```

### 4.2 ESP32 设备测试

**模拟 ESP32 设备**：
```go
func TestE2E_ESP32Device(t *testing.T) {
    // 1. 设备注册
    var deviceID string
    var apiKey string

    t.Run("Register device", func(t *testing.T) {
        body := `{
            "device_id": "ESP32-TEST01",
            "name": "测试相框",
            "screen_width": 800,
            "screen_height": 480
        }`

        w := httptest.NewRecorder()
        req, _ := http.NewRequest("POST", "/api/v1/esp32/register", strings.NewReader(body))
        req.Header.Set("Content-Type", "application/json")
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 200, w.Code)

        var response map[string]interface{}
        json.Unmarshal(w.Body.Bytes(), &response)
        data := response["data"].(map[string]interface{})

        deviceID = data["device_id"].(string)
        apiKey = data["api_key"].(string)
        assert.NotEmpty(t, apiKey)
    })

    // 2. 发送心跳
    t.Run("Send heartbeat", func(t *testing.T) {
        body := fmt.Sprintf(`{
            "device_id": "%s",
            "battery_level": 85,
            "wifi_rssi": -45
        }`, deviceID)

        w := httptest.NewRecorder()
        req, _ := http.NewRequest("POST", "/api/v1/esp32/heartbeat", strings.NewReader(body))
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", "Bearer "+apiKey)
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 200, w.Code)
    })

    // 3. 获取展示照片
    t.Run("Get display photo", func(t *testing.T) {
        w := httptest.NewRecorder()
        req, _ := http.NewRequest("GET", "/api/v1/esp32/display/photo?device_id="+deviceID, nil)
        req.Header.Set("Authorization", "Bearer "+apiKey)
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 200, w.Code)
    })

    // 4. 下载图片
    t.Run("Download image", func(t *testing.T) {
        w := httptest.NewRecorder()
        req, _ := http.NewRequest("GET", "/api/v1/esp32/image/1", nil)
        req.Header.Set("Authorization", "Bearer "+apiKey)
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 200, w.Code)
        assert.Equal(t, "image/jpeg", w.Header().Get("Content-Type"))
    })
}
```

---

## 五、性能测试

### 5.1 基准测试

**测试图片处理性能**：
```go
func BenchmarkImagePreprocessor_ProcessForAI(b *testing.B) {
    preprocessor := &ImagePreprocessor{
        MaxLongSide: 1024,
        JPEGQuality: 85,
    }

    testImage := "../../tests/fixtures/photos/test_5mb.jpg"

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := preprocessor.ProcessForAI(testImage)
        if err != nil {
            b.Fatal(err)
        }
    }
}

// 预期结果：~50-100ms/op
```

**测试数据库查询性能**：
```go
func BenchmarkPhotoRepository_GetByID(b *testing.B) {
    db := setupTestDB(nil)
    repo := repository.NewPhotoRepository(db)

    // 插入测试数据
    for i := 0; i < 1000; i++ {
        db.Create(&model.Photo{FilePath: fmt.Sprintf("/test/%d.jpg", i)})
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, _ = repo.GetByID(i % 1000)
    }
}

// 预期结果：~0.1-1ms/op
```

### 5.2 压力测试

**使用 vegeta**：
```bash
# 安装 vegeta
go install github.com/tsenart/vegeta@latest

# 创建测试目标
echo "GET http://localhost:8080/api/v1/photos/123" | vegeta attack -duration=30s -rate=100 | vegeta report

# 结果示例：
# Requests      [total, rate, throughput]  3000, 100.03, 99.98
# Duration      [total, attack, wait]      30s, 29.99s, 12.3ms
# Latencies     [mean, 50, 95, 99, max]    15ms, 12ms, 28ms, 45ms, 120ms
# Success       [ratio]                    100.00%
```

**批量导入性能测试**：
```go
func TestBatchImport_Performance(t *testing.T) {
    db := setupTestDB(t)
    service := service.NewExportService(db)

    // 准备测试数据
    const photoCount = 10000
    records := make([]*model.Photo, photoCount)
    for i := 0; i < photoCount; i++ {
        records[i] = &model.Photo{
            FilePath: fmt.Sprintf("/test/%d.jpg", i),
            FileHash: fmt.Sprintf("hash-%d", i),
        }
    }

    // 测试批量导入
    start := time.Now()
    err := service.BatchImport(records)
    elapsed := time.Since(start)

    assert.NoError(t, err)
    assert.Less(t, elapsed, 5*time.Second) // 应在 5 秒内完成

    t.Logf("Imported %d records in %v (%.0f records/s)",
        photoCount, elapsed, float64(photoCount)/elapsed.Seconds())
}
```

---

## 六、安全测试

### 6.1 SQL 注入测试

```go
func TestSecurity_SQLInjection(t *testing.T) {
    // 尝试 SQL 注入
    maliciousInput := "1' OR '1'='1"

    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", "/api/v1/photos/"+maliciousInput, nil)
    testRouter.ServeHTTP(w, req)

    // 应该返回错误，而不是执行恶意 SQL
    assert.NotEqual(t, 200, w.Code)
}
```

### 6.2 认证测试

```go
func TestSecurity_Authentication(t *testing.T) {
    // 测试未认证访问
    t.Run("Without token", func(t *testing.T) {
        w := httptest.NewRecorder()
        req, _ := http.NewRequest("GET", "/api/v1/esp32/display/photo", nil)
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 401, w.Code)
    })

    // 测试无效 token
    t.Run("Invalid token", func(t *testing.T) {
        w := httptest.NewRecorder()
        req, _ := http.NewRequest("GET", "/api/v1/esp32/display/photo", nil)
        req.Header.Set("Authorization", "Bearer invalid-token")
        testRouter.ServeHTTP(w, req)

        assert.Equal(t, 401, w.Code)
    })
}
```

### 6.3 XSS 测试

```go
func TestSecurity_XSS(t *testing.T) {
    // 尝试注入 XSS 脚本
    maliciousCaption := "<script>alert('XSS')</script>"

    photo := &model.Photo{
        FilePath: "/test.jpg",
        Caption:  maliciousCaption,
    }
    testDB.Create(photo)

    // 获取照片
    w := httptest.NewRecorder()
    req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/photos/%d", photo.ID), nil)
    testRouter.ServeHTTP(w, req)

    // 验证响应已转义
    body := w.Body.String()
    assert.NotContains(t, body, "<script>")
    assert.Contains(t, body, "&lt;script&gt;") // 应该被转义
}
```

---

## 七、测试数据

### 7.1 Fixtures

**创建测试数据** `tests/fixtures/photos.sql`：
```sql
INSERT INTO photos (file_path, file_hash, taken_at, memory_score, beauty_score)
VALUES
  ('/photos/2023/01/IMG_0001.jpg', 'hash1', '2023-01-15 10:30:00', 85, 90),
  ('/photos/2023/03/IMG_0002.jpg', 'hash2', '2023-03-20 14:20:00', 90, 85),
  ('/photos/2023/06/IMG_0003.jpg', 'hash3', '2023-06-10 16:45:00', 75, 80);
```

**加载测试数据**：
```go
func loadFixtures(db *gorm.DB, file string) error {
    sqlBytes, err := os.ReadFile(file)
    if err != nil {
        return err
    }

    return db.Exec(string(sqlBytes)).Error
}

func setupTestData(t *testing.T, db *gorm.DB) {
    err := loadFixtures(db, "../../tests/fixtures/photos.sql")
    assert.NoError(t, err)
}
```

### 7.2 测试图片

**准备测试图片**：
```
tests/fixtures/photos/
├── small_1mb.jpg       # 小图片
├── normal_5mb.jpg      # 正常大小
├── large_20mb.jpg      # 大图片
├── portrait.jpg        # 竖向照片
├── landscape.jpg       # 横向照片
├── screenshot.png      # 截图
└── corrupted.jpg       # 损坏的图片
```

---

## 八、CI/CD 集成

### 8.1 GitHub Actions

**配置文件** `.github/workflows/test.yml`：
```yaml
name: Tests

on:
  push:
    branches: [main, develop]
  pull_request:
    branches: [main, develop]

jobs:
  test:
    runs-on: ubuntu-latest

    steps:
      - name: Checkout code
        uses: actions/checkout@v3

      - name: Set up Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'

      - name: Cache dependencies
        uses: actions/cache@v3
        with:
          path: ~/go/pkg/mod
          key: ${{ runner.os }}-go-${{ hashFiles('**/go.sum') }}
          restore-keys: |
            ${{ runner.os }}-go-

      - name: Install dependencies
        run: go mod download

      - name: Run linter
        run: |
          go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
          golangci-lint run

      - name: Run tests
        run: go test -v -race -coverprofile=coverage.out ./...

      - name: Upload coverage
        uses: codecov/codecov-action@v3
        with:
          file: ./coverage.out

      - name: Run integration tests
        run: go test -v -tags=integration ./tests/integration/...

  e2e:
    runs-on: ubuntu-latest
    needs: test

    steps:
      - name: Checkout code
        uses: actions/checkout@v3

      - name: Build Docker image
        run: docker build -t relive:test .

      - name: Run E2E tests
        run: |
          docker-compose -f docker-compose.test.yml up -d
          sleep 10
          go test -v -tags=e2e ./tests/e2e/...
          docker-compose -f docker-compose.test.yml down
```

### 8.2 测试报告

**生成 HTML 报告**：
```bash
# 运行测试并生成报告
go test -v ./... -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html

# 使用 gotestsum（更好的输出）
go install gotest.tools/gotestsum@latest
gotestsum --junitfile report.xml --format testname
```

### 8.3 测试徽章

**在 README.md 中添加**：
```markdown
[![Tests](https://github.com/davidhoo/relive/actions/workflows/test.yml/badge.svg)](https://github.com/davidhoo/relive/actions/workflows/test.yml)
[![Coverage](https://codecov.io/gh/davidhoo/relive/branch/main/graph/badge.svg)](https://codecov.io/gh/davidhoo/relive)
```

---

## 九、测试最佳实践

### 9.1 测试原则

**FIRST 原则**：
- **Fast**（快速）- 测试应该快速执行
- **Independent**（独立）- 测试间不应有依赖
- **Repeatable**（可重复）- 测试结果应该一致
- **Self-Validating**（自验证）- 测试应该自动验证结果
- **Timely**（及时）- 测试应该及时编写

### 9.2 测试命名

**使用描述性名称**：
```go
// ✅ 好的命名
func TestPhotoService_GetPhotoByID_ReturnsPhotoWhenFound(t *testing.T) { ... }
func TestPhotoService_GetPhotoByID_ReturnsErrorWhenNotFound(t *testing.T) { ... }

// ❌ 不好的命名
func TestGetPhoto(t *testing.T) { ... }
func TestGetPhoto2(t *testing.T) { ... }
```

### 9.3 测试组织

**使用子测试**：
```go
func TestPhotoService(t *testing.T) {
    t.Run("GetByID", func(t *testing.T) {
        t.Run("success", func(t *testing.T) { ... })
        t.Run("not found", func(t *testing.T) { ... })
    })

    t.Run("Create", func(t *testing.T) {
        t.Run("success", func(t *testing.T) { ... })
        t.Run("duplicate", func(t *testing.T) { ... })
    })
}
```

---

## 十、总结

### 10.1 测试检查清单

开发时：
- [ ] 为新功能编写单元测试
- [ ] 运行测试确保通过
- [ ] 检查测试覆盖率 ≥ 80%
- [ ] 运行 linter 检查代码质量

提交前：
- [ ] 运行完整测试套件
- [ ] 修复所有失败的测试
- [ ] 更新相关文档

发布前：
- [ ] 运行集成测试
- [ ] 运行 E2E 测试
- [ ] 运行性能测试
- [ ] 运行安全测试

### 10.2 常用命令

```bash
# 运行所有测试
go test ./...

# 运行特定测试
go test -run TestPhotoService ./internal/service

# 生成覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# 运行基准测试
go test -bench=. -benchmem ./...

# 运行竞态检测
go test -race ./...

# 使用 make（如果配置了 Makefile）
make test
make test-coverage
make test-integration
make test-e2e
```

---

**测试策略完成** ✅
**准备编写测试** 🚀
