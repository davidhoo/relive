package service

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
	_ "github.com/mattn/go-sqlite3"
)

// ExportService 导出服务接口
type ExportService interface {
	// Export 导出数据
	Export(outputPath string, analyzedOnly bool) (*model.ExportResponse, error)

	// Import 导入数据
	Import(inputPath string) (*model.ImportResponse, error)

	// CheckExport 检查导出数据的完整性
	CheckExport(exportPath string) error
}

// exportService 导出服务实现
type exportService struct {
	photoRepo repository.PhotoRepository
}

// NewExportService 创建导出服务
func NewExportService(photoRepo repository.PhotoRepository) ExportService {
	return &exportService{
		photoRepo: photoRepo,
	}
}

// Export 导出数据到指定路径
func (s *exportService) Export(outputPath string, analyzedOnly bool) (*model.ExportResponse, error) {
	startTime := time.Now()

	// 创建导出目录
	if err := os.MkdirAll(outputPath, 0755); err != nil {
		return nil, fmt.Errorf("create export directory: %w", err)
	}

	// 导出数据库文件
	exportDBPath := filepath.Join(outputPath, "export.db")

	// 获取要导出的照片
	var photos []*model.Photo
	var err error

	if analyzedOnly {
		// 只导出已分析的照片（用于离线分析完成后导入）
		photos, err = s.photoRepo.ListAll()
		if err != nil {
			return nil, fmt.Errorf("list photos: %w", err)
		}

		// 过滤已分析的照片
		analyzed := make([]*model.Photo, 0)
		for _, photo := range photos {
			if photo.AIAnalyzed {
				analyzed = append(analyzed, photo)
			}
		}
		photos = analyzed
	} else {
		// 导出所有照片（用于离线分析）
		photos, err = s.photoRepo.ListAll()
		if err != nil {
			return nil, fmt.Errorf("list photos: %w", err)
		}
	}

	if len(photos) == 0 {
		return nil, fmt.Errorf("no photos to export")
	}

	// 创建导出数据库
	db, err := sql.Open("sqlite3", exportDBPath)
	if err != nil {
		return nil, fmt.Errorf("create export database: %w", err)
	}
	defer db.Close()

	// 创建表结构
	if err := s.createExportSchema(db); err != nil {
		return nil, fmt.Errorf("create export schema: %w", err)
	}

	// 插入照片数据
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO photos (
			id, file_path, file_name, file_size, file_hash,
			width, height, taken_at, location, camera_model,
			ai_analyzed, description, caption, memory_score, beauty_score,
			overall_score, main_category, tags, analyzed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return nil, fmt.Errorf("prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, photo := range photos {
		var takenAtStr, analyzedAtStr string
		if photo.TakenAt != nil {
			takenAtStr = photo.TakenAt.Format(time.RFC3339)
		}
		if photo.AnalyzedAt != nil {
			analyzedAtStr = photo.AnalyzedAt.Format(time.RFC3339)
		}

		_, err = stmt.Exec(
			photo.ID, photo.FilePath, photo.FileName, photo.FileSize, photo.FileHash,
			photo.Width, photo.Height, takenAtStr, photo.Location, photo.CameraModel,
			photo.AIAnalyzed, photo.Description, photo.Caption, photo.MemoryScore, photo.BeautyScore,
			photo.OverallScore, photo.MainCategory, photo.Tags, analyzedAtStr,
		)
		if err != nil {
			return nil, fmt.Errorf("insert photo %d: %w", photo.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}

	// 获取数据库文件大小
	dbStat, err := os.Stat(exportDBPath)
	if err != nil {
		return nil, fmt.Errorf("stat export database: %w", err)
	}

	duration := time.Since(startTime)
	logger.Infof("Export completed: %d photos, size=%d bytes, duration=%v", len(photos), dbStat.Size(), duration)

	return &model.ExportResponse{
		OutputPath:   exportDBPath,
		PhotoCount:   len(photos),
		DatabaseSize: dbStat.Size(),
		ThumbnailDir: "", // 暂不支持缩略图导出
	}, nil
}

// Import 导入 AI 分析结果
func (s *exportService) Import(inputPath string) (*model.ImportResponse, error) {
	startTime := time.Now()

	// 检查导入文件是否存在
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("import file not found: %s", inputPath)
	}

	// 打开导入数据库
	db, err := sql.Open("sqlite3", inputPath)
	if err != nil {
		return nil, fmt.Errorf("open import database: %w", err)
	}
	defer db.Close()

	// 查询已分析的照片
	rows, err := db.Query(`
		SELECT
			id, file_hash, ai_analyzed, description, caption,
			memory_score, beauty_score, overall_score, main_category, tags
		FROM photos
		WHERE ai_analyzed = 1
	`)
	if err != nil {
		return nil, fmt.Errorf("query photos: %w", err)
	}
	defer rows.Close()

	updatedCount := 0
	failedCount := 0

	for rows.Next() {
		var (
			id                                                             uint
			fileHash, description, caption, mainCategory, tags             string
			memoryScore, beautyScore, overallScore                         int
			aiAnalyzed                                                     bool
		)

		if err := rows.Scan(&id, &fileHash, &aiAnalyzed, &description, &caption,
			&memoryScore, &beautyScore, &overallScore, &mainCategory, &tags); err != nil {
			logger.Errorf("Scan row error: %v", err)
			failedCount++
			continue
		}

		// 根据 file_hash 查找照片
		photo, err := s.photoRepo.GetByFileHash(fileHash)
		if err != nil {
			logger.Warnf("Photo not found for hash %s: %v", fileHash, err)
			failedCount++
			continue
		}

		// 更新分析结果
		err = s.photoRepo.MarkAsAnalyzed(photo.ID, description, caption, mainCategory, tags, memoryScore, beautyScore)
		if err != nil {
			logger.Errorf("Failed to update photo %d: %v", photo.ID, err)
			failedCount++
			continue
		}

		updatedCount++
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rows: %w", err)
	}

	duration := time.Since(startTime)
	logger.Infof("Import completed: updated=%d, failed=%d, duration=%v", updatedCount, failedCount, duration)

	return &model.ImportResponse{
		UpdatedCount: updatedCount,
		FailedCount:  failedCount,
	}, nil
}

// CheckExport 检查导出数据的完整性
func (s *exportService) CheckExport(exportPath string) error {
	// 检查导出目录是否存在
	if _, err := os.Stat(exportPath); os.IsNotExist(err) {
		return fmt.Errorf("export directory not found: %s", exportPath)
	}

	// 检查 export.db 是否存在
	dbPath := filepath.Join(exportPath, "export.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return fmt.Errorf("export.db not found in: %s", exportPath)
	}

	// 尝试打开数据库
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return fmt.Errorf("cannot open export database: %w", err)
	}
	defer db.Close()

	// 检查表结构
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM photos").Scan(&count)
	if err != nil {
		return fmt.Errorf("cannot query photos table: %w", err)
	}

	if count == 0 {
		return fmt.Errorf("export database is empty")
	}

	logger.Infof("Export check passed: %d photos found in %s", count, exportPath)
	return nil
}

// createExportSchema 创建导出数据库的表结构
func (s *exportService) createExportSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS photos (
		id INTEGER PRIMARY KEY,
		file_path TEXT NOT NULL,
		file_name TEXT NOT NULL,
		file_size INTEGER NOT NULL,
		file_hash TEXT NOT NULL,
		width INTEGER NOT NULL,
		height INTEGER NOT NULL,
		taken_at TEXT,
		location TEXT,
		camera_model TEXT,
		ai_analyzed BOOLEAN DEFAULT 0,
		description TEXT,
		caption TEXT,
		memory_score INTEGER DEFAULT 0,
		beauty_score INTEGER DEFAULT 0,
		overall_score INTEGER DEFAULT 0,
		main_category TEXT,
		tags TEXT,
		analyzed_at TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_file_hash ON photos(file_hash);
	CREATE INDEX IF NOT EXISTS idx_ai_analyzed ON photos(ai_analyzed);
	`

	_, err := db.Exec(schema)
	return err
}
