# OFFLINE_WORKFLOW.md 设计审查报告

> 审查日期：2026-02-28
> 审查目标：技术可行性、逻辑完整性、实用性、与现有设计的兼容性

---

## 一、总体评价

### ✅ 优点

1. **需求理解准确**：完全覆盖了 NAS 与 GPU 物理分离的场景
2. **架构清晰**：四阶段工作流逻辑明确
3. **向后兼容**：支持在线、本地、离线三种模式并存
4. **文档详细**：接口、数据结构、命令行示例都很完整
5. **成本分析充分**：明确了离线模式的价值（节省 ¥2,200）

### ⚠️ 需要改进的问题

发现 **16 个关键问题**，分为：
- 🔴 高优先级（必须解决）：7 个
- 🟡 中优先级（建议改进）：6 个
- 🟢 低优先级（可选优化）：3 个

---

## 二、高优先级问题（🔴 必须解决）

### 问题 1：数据库结构不一致

**问题描述**：
- `export.db`：包含完整的 Photo 记录（40+ 字段）
- `import.db`：只包含 AI 结果（ai_results 表）
- 两个数据库结构差异大，可能导致混淆

**当前设计**（export.db）：
```go
// 导出完整 Photo 记录
exportPhoto := &model.Photo{
    ID:            photo.ID,
    FilePath:      photo.FilePath,
    // ... 所有字段
}
exportDB.Create(exportPhoto)
```

**当前设计**（import.db）：
```sql
CREATE TABLE ai_results (
    id INTEGER PRIMARY KEY,
    photo_id INTEGER,
    file_hash TEXT NOT NULL UNIQUE,  -- 匹配用
    caption TEXT,
    // ... 仅 AI 字段
);
```

**改进建议**：

**方案 A：统一使用 Photo 表**（推荐）
```go
// export.db: 完整 Photo 记录
type Photo struct { /* 40+ 字段 */ }

// import.db: 同样使用 Photo 表，但只更新 AI 字段
type Photo struct {
    FileHash     string  `gorm:"uniqueIndex;not null"`
    Caption      string
    SideCaption  string
    Category     string
    MemoryScore  float64
    BeautyScore  float64
    DisplayScore float64
    Analyzed     bool
    AnalyzedAt   *time.Time
    AIModel      string
}

// 导入时根据 file_hash 匹配后更新
```

**优点**：
- ✅ 结构统一，容易理解
- ✅ 可以直接使用 GORM 的 Updates()
- ✅ 未来可以支持更复杂的合并策略

**方案 B：明确分离**
- `export.db`：重命名为 `metadata.db`（元数据）
- `import.db`：重命名为 `results.db`（结果）
- 在文档中明确说明两者的用途

---

### 问题 2：file_hash 匹配的脆弱性

**问题描述**：
如果用户在导出后对原照片做了任何修改（旋转、压缩、添加水印），`file_hash` 会变化，导致匹配失败。

**风险场景**：
1. 导出后用户整理照片，用 Lightroom 批量旋转
2. NAS 自动优化存储，重新压缩照片
3. 误删照片后从备份恢复（文件时间戳变化）

**改进建议**：

**多重匹配策略**（推荐）：
```go
func (s *ImportService) findPhotoByMultipleStrategy(result *AIResult) (*model.Photo, error) {
    var photo model.Photo

    // 策略 1: file_hash（最可靠）
    if err := s.db.Where("file_hash = ?", result.FileHash).First(&photo).Error; err == nil {
        return &photo, nil
    }

    // 策略 2: photo_id（如果ID未变）
    if result.PhotoID > 0 {
        if err := s.db.First(&photo, result.PhotoID).Error; err == nil {
            // 验证是否是同一张照片（比对文件名）
            if photo.FileName == result.FileName {
                log.Warn("Matched by photo_id, but file_hash changed")
                return &photo, nil
            }
        }
    }

    // 策略 3: 复合键（文件名 + EXIF 时间 + 文件大小）
    if result.ExifDatetime != nil {
        query := s.db.Where("file_name = ?", result.FileName).
            Where("exif_datetime = ?", result.ExifDatetime).
            Where("ABS(file_size - ?) < ?", result.FileSize, 1024*10) // 允许 10KB 误差

        if err := query.First(&photo).Error; err == nil {
            log.Warn("Matched by composite key, file_hash mismatch")
            return &photo, nil
        }
    }

    // 策略 4: 文件路径（最后的兜底）
    if err := s.db.Where("file_path = ?", result.FilePath).First(&photo).Error; err == nil {
        log.Warn("Matched by file_path, file_hash mismatch")
        return &photo, nil
    }

    return nil, errors.New("photo not found by any strategy")
}
```

**在 import.db 中增加字段**：
```sql
CREATE TABLE ai_results (
    -- 主键
    id INTEGER PRIMARY KEY,

    -- 多重匹配字段
    photo_id INTEGER,              -- 原 photo ID
    file_hash TEXT NOT NULL,       -- SHA256 哈希
    file_path TEXT,                -- 原始路径
    file_name TEXT,                -- 文件名
    file_size INTEGER,             -- 文件大小
    exif_datetime DATETIME,        -- EXIF 时间

    -- AI 结果
    caption TEXT,
    // ...
);
```

---

### 问题 3：缩略图路径管理混乱

**问题描述**：
- NAS 数据库：`thumbnail_path = "/app/cache/thumbnails/abc123.jpg"`（绝对路径）
- export.db：`thumbnail_path = "abc123.jpg"`（相对路径）
- 导入后路径不一致，可能导致缩略图失效

**改进建议**：

**方案 A：导入时不更新 thumbnail_path**（推荐）
```go
func (s *ImportService) mergeResult(result *AIResult) error {
    // 只更新 AI 字段，不更新 thumbnail_path
    updates := map[string]interface{}{
        "caption":        result.Caption,
        "side_caption":   result.SideCaption,
        // ...
        // 不包含 thumbnail_path
    }

    return s.db.Model(&photo).Updates(updates).Error
}
```

**方案 B：重新生成缩略图**
```go
// 导入时检查缩略图是否存在，不存在则重新生成
if !fileExists(photo.ThumbnailPath) {
    newPath, _ := s.imageService.GenerateThumbnail(photo.FilePath, 1024, 85)
    photo.ThumbnailPath = newPath
}
```

---

### 问题 4：导出大量数据的性能问题

**问题描述**：
11 万张照片，复制 40GB 缩略图，当前设计没有：
- 导出进度显示
- 暂停/恢复功能
- 错误处理（磁盘空间不足、文件损坏）

**改进建议**：

**增加导出进度跟踪**：
```go
// 导出任务状态表
type ExportJob struct {
    ID              string
    Status          string    // "preparing", "exporting", "completed", "failed", "paused"
    TotalPhotos     int
    ExportedPhotos  int
    FailedPhotos    int
    TotalSize       int64
    ExportedSize    int64
    StartedAt       time.Time
    CompletedAt     *time.Time
    Error           string
}

// 导出服务
func (s *ExportService) CreateExport(request *ExportRequest) (*ExportJob, error) {
    // 1. 创建任务记录
    job := &ExportJob{
        ID:     uuid.New().String(),
        Status: "preparing",
    }
    s.saveJob(job)

    // 2. 异步执行导出
    go s.executeExport(job, request)

    return job, nil
}

// 异步导出
func (s *ExportService) executeExport(job *ExportJob, request *ExportRequest) {
    job.Status = "exporting"
    s.updateJob(job)

    for i, photo := range photos {
        // 复制缩略图
        if err := copyFile(photo.ThumbnailPath, dstPath); err != nil {
            job.FailedPhotos++
            continue
        }

        // 更新进度（每10张更新一次）
        if i%10 == 0 {
            job.ExportedPhotos = i
            job.ExportedSize = calculateSize()
            s.updateJob(job)
        }
    }

    job.Status = "completed"
    job.CompletedAt = timePtr(time.Now())
    s.updateJob(job)
}

// API: 查询导出进度
GET /api/v1/export/{id}/progress
Response:
{
  "status": "exporting",
  "progress": 65.5,
  "exported_photos": 7205,
  "total_photos": 11000,
  "exported_size": "26.8 GB",
  "total_size": "41 GB",
  "speed": "120 MB/s",
  "eta": "2m 15s"
}
```

**支持暂停/恢复**：
```go
// API: 暂停导出
POST /api/v1/export/{id}/pause

// API: 恢复导出
POST /api/v1/export/{id}/resume

// 实现：使用 context.Context 控制
func (s *ExportService) executeExport(ctx context.Context, job *ExportJob) {
    for _, photo := range photos {
        // 检查是否暂停
        select {
        case <-ctx.Done():
            job.Status = "paused"
            s.updateJob(job)
            return
        default:
            // 继续导出
        }

        copyFile(photo.ThumbnailPath, dstPath)
    }
}
```

---

### 问题 5：版本兼容性风险

**问题描述**：
- NAS 端 Relive：v0.1.0，数据库 schema v1
- GPU 端 relive-analyzer：v0.2.0，期望 schema v2
- 导致数据结构不匹配，导入失败

**改进建议**：

**增强 manifest.json**：
```json
{
  "export_id": "550e8400-e29b-41d4-a716-446655440000",
  "export_name": "2024年春节照片分析",
  "export_date": "2024-02-28T10:30:00Z",

  // 版本信息（新增）
  "relive_version": "0.1.0",
  "schema_version": 1,              // 数据库 schema 版本
  "export_format_version": "1.0",   // 导出格式版本
  "compatible_analyzer_versions": ["0.1.x", "0.2.x"],

  // 数据信息
  "photo_count": 5420,
  "total_size": 1876234567,

  // 校验和（新增）
  "checksums": {
    "export_db": "sha256:abc123...",
    "thumbnails": "sha256:def456..."
  }
}
```

**导入时严格验证**：
```go
func (s *ImportService) ValidateImport(importPath string) error {
    manifest := s.readManifest(importPath)

    // 1. 检查格式版本
    if manifest.ExportFormatVersion != "1.0" {
        return fmt.Errorf("unsupported export format: %s", manifest.ExportFormatVersion)
    }

    // 2. 检查 schema 版本
    currentSchema := s.getCurrentSchemaVersion()
    if manifest.SchemaVersion > currentSchema {
        return fmt.Errorf("export requires newer schema (has %d, need %d)",
            currentSchema, manifest.SchemaVersion)
    }

    // 3. 检查 Relive 版本兼容性
    if !s.isCompatibleVersion(manifest.ReliveVersion) {
        return fmt.Errorf("incompatible relive version: %s", manifest.ReliveVersion)
    }

    // 4. 校验文件完整性（可选）
    if manifest.Checksums != nil {
        if err := s.verifyChecksums(importPath, manifest.Checksums); err != nil {
            return fmt.Errorf("checksum mismatch: %w", err)
        }
    }

    return nil
}
```

---

### 问题 6：失败照片的处理不完善

**问题描述**：
- 5420 张照片中 2 张分析失败
- 当前设计只记录 `failed_count`，没有详细日志
- 无法单独重试失败的照片

**改进建议**：

**增加失败记录表**：
```sql
-- 在 import.db 中
CREATE TABLE analysis_failures (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    photo_id INTEGER,
    file_hash TEXT,
    file_name TEXT,

    -- 失败信息
    error_type TEXT,        -- "timeout", "model_error", "invalid_image"
    error_message TEXT,
    retry_count INTEGER DEFAULT 0,
    last_attempt DATETIME,

    -- 元数据（用于重试）
    thumbnail_path TEXT
);
```

**relive-analyzer 记录失败**：
```go
func (s *AnalyzerService) worker(jobs <-chan *model.Photo, results chan<- *AnalysisResult) {
    for photo := range jobs {
        result, err := s.analyzePhoto(photo)

        if err != nil {
            // 记录失败
            failure := &AnalysisFailure{
                PhotoID:      photo.ID,
                FileHash:     photo.FileHash,
                FileName:     photo.FileName,
                ErrorType:    classifyError(err),
                ErrorMessage: err.Error(),
                RetryCount:   0,
                LastAttempt:  time.Now(),
            }
            s.recordFailure(failure)

            results <- &AnalysisResult{Error: err}
            continue
        }

        results <- result
    }
}
```

**支持重试失败照片**：
```bash
# 查看失败列表
relive-analyzer failures --export-dir /path/to/export

# 输出
共 2 张照片分析失败:
1. IMG_1234.jpg - timeout (超时)
2. IMG_5678.jpg - model_error (模型错误)

# 重试失败照片
relive-analyzer retry-failures \
  --export-dir /path/to/export \
  --retry-all
```

---

### 问题 7：导入时的性能问题

**问题描述**：
11 万条记录，当前设计是单条更新：
```go
for _, result := range aiResults {
    s.mergeResult(result)  // 单次数据库操作
}
```

预估时间：假设每条 10ms，总计 1100 秒（18 分钟）

**改进建议**：

**批量更新**（推荐）：
```go
func (s *ImportService) ImportResults(importPath string) (*ImportReport, error) {
    // 读取所有结果
    var aiResults []*AIResult
    importDB.Find(&aiResults)

    // 分批处理（每批 1000 条）
    batchSize := 1000
    for i := 0; i < len(aiResults); i += batchSize {
        end := i + batchSize
        if end > len(aiResults) {
            end = len(aiResults)
        }

        batch := aiResults[i:end]

        // 批量更新（在事务中）
        err := s.db.Transaction(func(tx *gorm.DB) error {
            for _, result := range batch {
                // 查找照片
                var photo model.Photo
                if err := tx.Where("file_hash = ?", result.FileHash).First(&photo).Error; err != nil {
                    continue
                }

                // 批量更新（使用 GORM 的 Updates）
                updates := map[string]interface{}{
                    "caption":        result.Caption,
                    "side_caption":   result.SideCaption,
                    "category":       result.Category,
                    "memory_score":   result.MemoryScore,
                    "beauty_score":   result.BeautyScore,
                    "display_score":  result.DisplayScore,
                    "analyzed":       true,
                    "analyzed_at":    result.AnalyzedAt,
                }

                if err := tx.Model(&photo).Updates(updates).Error; err != nil {
                    return err
                }
            }
            return nil
        })

        if err != nil {
            return nil, err
        }

        // 更新进度
        report.SuccessCount += len(batch)
    }

    return report, nil
}
```

**使用 UPSERT（更优）**：
```go
// 如果数据库支持，使用 UPSERT
func (s *ImportService) batchUpsert(batch []*AIResult) error {
    // 构建批量更新 SQL
    sql := `
    INSERT INTO photos (file_hash, caption, side_caption, memory_score, analyzed)
    VALUES (?, ?, ?, ?, ?)
    ON CONFLICT(file_hash) DO UPDATE SET
        caption = excluded.caption,
        side_caption = excluded.side_caption,
        memory_score = excluded.memory_score,
        analyzed = excluded.analyzed
    `

    // 批量执行
    for _, result := range batch {
        s.db.Exec(sql, result.FileHash, result.Caption, result.SideCaption, result.MemoryScore, true)
    }

    return nil
}
```

**性能对比**：
| 方法 | 11万条耗时 | 说明 |
|------|----------|------|
| 单条更新 | ~18 分钟 | 当前设计 |
| 批量更新（1000条/批） | ~2 分钟 | 推荐 ✅ |
| UPSERT | ~1 分钟 | 最优（需要 SQLite 3.24+） |

---

## 三、中优先级问题（🟡 建议改进）

### 问题 8：安全性和隐私保护

**问题描述**：
导出包包含敏感信息：
- 完整文件路径（可能泄露目录结构）
- GPS 坐标（精确位置）
- 照片缩略图（包含人脸）

如果移动硬盘丢失，可能泄露隐私。

**改进建议**：

**方案 A：加密导出包**
```go
// 导出时加密
func (s *ExportService) CreateEncryptedExport(request *ExportRequest, password string) error {
    // 1. 创建普通导出包
    exportPath := s.createExport(request)

    // 2. 压缩并加密
    archivePath := exportPath + ".tar.gz.enc"

    // 使用 AES-256-GCM 加密
    encryptedArchive := encryptArchive(exportPath, password)
    os.WriteFile(archivePath, encryptedArchive, 0644)

    // 3. 删除明文导出包
    os.RemoveAll(exportPath)

    return nil
}

// 分析前解密
relive-analyzer analyze \
  --export-archive /path/to/export.tar.gz.enc \
  --password "your-password"
```

**方案 B：路径脱敏**（推荐）
```go
// 导出时脱敏路径
func (s *ExportService) sanitizePath(filePath string) string {
    // 只保留相对路径，移除敏感根目录
    // /volume1/photos/2024/02/IMG_1234.jpg
    // → 2024/02/IMG_1234.jpg

    base := s.config.PhotosRootDir
    relativePath := strings.TrimPrefix(filePath, base)
    return relativePath
}

// 导入时恢复路径
func (s *ImportService) restorePath(relativePath string) string {
    return filepath.Join(s.config.PhotosRootDir, relativePath)
}
```

---

### 问题 9：增量导入的追踪

**问题描述**：
如果多次导出、多次导入，可能重复导入同一批照片。

**改进建议**：

**增加导入历史表**：
```sql
CREATE TABLE import_history (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    export_id TEXT NOT NULL UNIQUE,  -- 防止重复导入
    export_name TEXT,
    imported_at DATETIME,
    photo_count INTEGER,
    success_count INTEGER,
    status TEXT
);
```

**导入前检查**：
```go
func (s *ImportService) ImportResults(importPath string) (*ImportReport, error) {
    manifest := s.readManifest(importPath)

    // 检查是否已导入过
    var existingImport ImportHistory
    err := s.db.Where("export_id = ?", manifest.ExportID).First(&existingImport).Error

    if err == nil {
        // 已导入过
        return nil, fmt.Errorf("export %s already imported on %s",
            manifest.ExportID, existingImport.ImportedAt)
    }

    // 执行导入
    // ...

    // 记录导入历史
    history := &ImportHistory{
        ExportID:     manifest.ExportID,
        ExportName:   manifest.ExportName,
        ImportedAt:   time.Now(),
        PhotoCount:   report.TotalRecords,
        SuccessCount: report.SuccessCount,
        Status:       "completed",
    }
    s.db.Create(history)

    return report, nil
}
```

---

### 问题 10：relive-analyzer 的依赖检查

**问题描述**：
工具启动时没有检查：
- Ollama 是否运行
- 模型是否已下载
- 磁盘空间是否足够

**改进建议**：

**启动前预检查**：
```go
func (s *AnalyzerService) PreflightCheck() error {
    checks := []struct {
        name  string
        check func() error
    }{
        {"Ollama 服务", s.checkOllamaRunning},
        {"模型可用性", s.checkModelAvailable},
        {"磁盘空间", s.checkDiskSpace},
        {"导出包完整性", s.checkExportPackage},
    }

    fmt.Println("🔍 执行预检查...")

    for _, c := range checks {
        fmt.Printf("  %-20s ... ", c.name)

        if err := c.check(); err != nil {
            fmt.Printf("❌ 失败: %v\n", err)
            return err
        }

        fmt.Println("✅ 通过")
    }

    return nil
}

func (s *AnalyzerService) checkOllamaRunning() error {
    resp, err := http.Get(s.config.OllamaEndpoint + "/api/version")
    if err != nil {
        return fmt.Errorf("Ollama 未运行或无法连接")
    }
    defer resp.Body.Close()

    if resp.StatusCode != 200 {
        return fmt.Errorf("Ollama 返回错误状态: %d", resp.StatusCode)
    }

    return nil
}

func (s *AnalyzerService) checkModelAvailable() error {
    // 调用 Ollama API 检查模型
    resp, err := http.Get(s.config.OllamaEndpoint + "/api/tags")
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    var result struct {
        Models []struct {
            Name string `json:"name"`
        } `json:"models"`
    }

    json.NewDecoder(resp.Body).Decode(&result)

    // 检查目标模型是否存在
    modelName := s.config.ModelName
    for _, model := range result.Models {
        if model.Name == modelName {
            return nil
        }
    }

    return fmt.Errorf("模型 %s 未找到，请先下载: ollama pull %s", modelName, modelName)
}

func (s *AnalyzerService) checkDiskSpace() error {
    // 检查磁盘空间（预留 5GB 用于结果）
    requiredSpace := int64(5 * 1024 * 1024 * 1024)

    // 使用 syscall 检查磁盘空间
    // ...

    return nil
}
```

---

### 问题 11：混合模式的实现不清晰

**问题描述**：
文档提到支持混合模式（优先本地，失败回退云端），但没有详细实现。

**改进建议**：

**配置混合模式**：
```yaml
ai:
  enabled: true
  mode: "hybrid"              # online/local/offline/hybrid

  # 混合模式配置
  hybrid:
    primary: "ollama"         # 主要提供者
    fallback: "qwen"          # 备用提供者
    fallback_conditions:
      - timeout               # 超时时回退
      - model_error           # 模型错误时回退
    fallback_threshold: 3     # 连续失败3次后切换
```

**实现混合 Provider**：
```go
type HybridProvider struct {
    primary   AIProvider
    fallback  AIProvider
    config    *HybridConfig
    failCount int
}

func (p *HybridProvider) Analyze(request *AnalyzeRequest) (*AnalyzeResult, error) {
    // 优先使用主提供者
    result, err := p.primary.Analyze(request)

    if err == nil {
        p.failCount = 0  // 成功，重置失败计数
        return result, nil
    }

    // 检查是否满足回退条件
    if p.shouldFallback(err) {
        p.failCount++

        log.Warnf("Primary provider failed (%d/%d), falling back to %s",
            p.failCount, p.config.FallbackThreshold, p.fallback.Name())

        // 使用备用提供者
        return p.fallback.Analyze(request)
    }

    return nil, err
}

func (p *HybridProvider) shouldFallback(err error) bool {
    // 判断是否满足回退条件
    for _, condition := range p.config.FallbackConditions {
        switch condition {
        case "timeout":
            if errors.Is(err, context.DeadlineExceeded) {
                return true
            }
        case "model_error":
            if isModelError(err) {
                return true
            }
        }
    }

    // 或者连续失败次数达到阈值
    return p.failCount >= p.config.FallbackThreshold
}
```

---

### 问题 12：API 幂等性设计不明确

**问题描述**：
重复调用导入接口会发生什么？文档没有明确说明。

**改进建议**：

**方案 A：基于 export_id 的幂等性**（推荐）
```go
func (s *ImportService) ImportResults(importPath string) (*ImportReport, error) {
    manifest := s.readManifest(importPath)

    // 检查是否已导入
    var existing ImportHistory
    err := s.db.Where("export_id = ?", manifest.ExportID).First(&existing).Error

    if err == nil {
        // 已导入，返回之前的结果（幂等）
        return &ImportReport{
            ExportID:     manifest.ExportID,
            SuccessCount: existing.SuccessCount,
            // ...从 history 构建报告
        }, nil
    }

    // 未导入，执行导入
    // ...
}
```

**方案 B：使用导入锁**
```go
type ImportLock struct {
    ExportID  string
    LockedAt  time.Time
    LockedBy  string  // 用户或进程ID
}

func (s *ImportService) ImportResults(importPath string) (*ImportReport, error) {
    manifest := s.readManifest(importPath)

    // 尝试获取锁
    lock := &ImportLock{
        ExportID: manifest.ExportID,
        LockedAt: time.Now(),
        LockedBy: s.getUserID(),
    }

    // 使用数据库唯一约束实现分布式锁
    if err := s.db.Create(lock).Error; err != nil {
        // 已有其他进程在导入
        return nil, fmt.Errorf("import already in progress")
    }

    // 执行导入
    // ...

    // 释放锁
    defer s.db.Delete(lock)

    return report, nil
}
```

---

### 问题 13：文档结构可以优化

**问题描述**：
文档 ~850 行，内容详细但缺少"快速开始"，新用户可能不知道从哪里入手。

**改进建议**：

**增加"快速开始"章节**（放在开头）：
```markdown
## 一、快速开始（5 分钟上手）

### 1.1 典型使用场景

你有 11 万张照片在 NAS，想用本地 GPU 跑 AI 分析，节省 ¥2,200 API 费用。

### 1.2 三步完成

**Step 1: NAS 扫描（8 小时）**
```bash
# Web 界面操作
1. 访问 http://nas:8080
2. 设置 → AI 分析 → 关闭
3. 照片管理 → 开始扫描
4. 等待完成（11万张 ~8小时）
```

**Step 2: 导出到移动硬盘（30 分钟）**
```bash
# Web 界面操作
1. 导出管理 → 创建导出
2. 选择"仅未分析照片"
3. 下载导出包 → 解压到移动硬盘
4. 带移动硬盘到 GPU 机器
```

**Step 3: GPU 分析 + 导入（15 小时）**
```bash
# GPU 机器命令行
relive-analyzer analyze \
  --export-dir /mnt/usb/export_xxx \
  --model llava:13b \
  --workers 4

# 分析完成后，带移动硬盘回 NAS

# Web 界面操作
1. 导入管理 → 上传导入包
2. 预览导入
3. 执行导入
4. 完成！
```

### 1.3 下一步

详细设计请看后续章节...
```

---

## 四、低优先级问题（🟢 可选优化）

### 问题 14：与 AI_PROVIDERS.md 的架构统一

**建议**：relive-analyzer 应该基于相同的 `AIProvider` 接口，避免重复设计。

**实现**：
```go
// relive-analyzer 复用主项目的 provider 包
import "github.com/davidhoo/relive/backend/provider"

type AnalyzerService struct {
    provider provider.AIProvider  // 复用接口
}

// 初始化
provider := &provider.OllamaProvider{
    Endpoint: config.OllamaEndpoint,
    Model:    config.ModelName,
}

analyzer := &AnalyzerService{
    provider: provider,
}
```

---

### 问题 15：测试用例可以更全面

**建议增加的测试场景**：
- 空导出（0 张照片）
- 全部失败（所有照片分析失败）
- 部分失败（50% 失败率）
- 导出中断（磁盘空间不足）
- 导入冲突（同一批次重复导入）
- 版本不兼容（旧版本导出 → 新版本导入）

---

### 问题 16：导入性能可以进一步优化

**建议**：使用 SQLite 的 WAL 模式 + PRAGMA 优化
```go
// 导入前优化数据库
func (s *ImportService) optimizeDatabase() {
    s.db.Exec("PRAGMA journal_mode = WAL")
    s.db.Exec("PRAGMA synchronous = NORMAL")
    s.db.Exec("PRAGMA cache_size = 100000")     // 100MB 缓存
    s.db.Exec("PRAGMA temp_store = MEMORY")     // 临时表放内存
}

// 导入后恢复设置
func (s *ImportService) restoreDatabase() {
    s.db.Exec("PRAGMA optimize")
}
```

---

## 五、改进优先级建议

### 立即修改（开发前必须解决）

1. ✅ **问题 1**：统一数据库结构（export.db vs import.db）
2. ✅ **问题 2**：增加多重匹配策略（file_hash + 备用）
3. ✅ **问题 5**：增强版本兼容性检查
4. ✅ **问题 7**：使用批量更新提升导入性能

### 开发中实现

5. ✅ **问题 3**：明确缩略图路径管理策略
6. ✅ **问题 4**：增加导出进度跟踪和暂停/恢复
7. ✅ **问题 6**：完善失败照片处理和重试机制
8. ✅ **问题 10**：relive-analyzer 预检查

### 后续优化

9. ⏸️ **问题 8**：安全性（可选加密）
10. ⏸️ **问题 9**：增量导入追踪
11. ⏸️ **问题 11**：混合模式实现
12. ⏸️ **问题 12**：API 幂等性
13. ⏸️ **问题 13**：文档结构优化

---

## 六、修改建议总结

### 必须修改的代码

**1. 统一数据库结构**
```go
// import.db 使用 Photo 表（与主库一致）
type Photo struct {
    FileHash     string  `gorm:"uniqueIndex;not null"`
    Caption      string
    // ... 仅 AI 字段
}
```

**2. 多重匹配策略**
```go
func findPhotoByMultipleStrategy(result *AIResult) (*model.Photo, error) {
    // 策略 1: file_hash
    // 策略 2: photo_id + 文件名验证
    // 策略 3: 文件名 + EXIF时间 + 文件大小
    // 策略 4: 文件路径
}
```

**3. 批量更新**
```go
// 每批 1000 条，使用事务
batchSize := 1000
for i := 0; i < len(aiResults); i += batchSize {
    s.db.Transaction(func(tx *gorm.DB) error {
        // 批量更新
    })
}
```

**4. 版本检查**
```json
// manifest.json 增加
{
  "schema_version": 1,
  "relive_version": "0.1.0",
  "compatible_analyzer_versions": ["0.1.x"]
}
```

### 建议修改的配置

**config.yaml 增强**：
```yaml
export:
  progress_interval: 10         # 每10张更新进度
  max_concurrent_files: 4       # 并发复制文件数
  verify_checksums: true        # 验证文件完整性

import:
  batch_size: 1000             # 批量更新大小
  match_strategy: "multi"      # file_hash/multi
  overwrite_existing: false    # 是否覆盖已分析照片
```

---

## 七、总体评价

### ✅ 设计质量：优秀

- 架构清晰，四阶段工作流逻辑正确
- 充分考虑了离线场景的实际需求
- 技术选型合理（SQLite、Golang、Ollama）

### ⚠️ 需要改进：16 处

- 7 个高优先级问题必须在开发前解决
- 6 个中优先级问题建议在开发中实现
- 3 个低优先级问题可后续优化

### 🎯 改进后效果

完成改进后，方案将更加：
- **健壮**：多重匹配策略、版本检查、错误处理
- **高效**：批量更新、导出进度、性能优化
- **易用**：预检查、失败重试、快速开始文档

---

## 八、下一步行动

### 建议流程

1. **修订文档**（1 小时）
   - 更新 OFFLINE_WORKFLOW.md
   - 增加"快速开始"章节
   - 修正高优先级问题

2. **评审确认**（与用户讨论）
   - 确认改进方案
   - 调整优先级

3. **开始开发**
   - 按修改后的设计实现

---

**审查完成** ✅
**建议：修订文档后再开始开发** 🚀
