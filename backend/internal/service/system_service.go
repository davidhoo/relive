package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// SystemService 系统服务接口
type SystemService interface {
	Ping() error
	GetStats() (*model.SystemStatsResponse, time.Time, error)
	ResetSystem() error
}

type systemService struct {
	db        *gorm.DB
	startTime time.Time
}

// NewSystemService 创建系统服务
func NewSystemService(db *gorm.DB) SystemService {
	return &systemService{
		db:        db,
		startTime: time.Now(),
	}
}

// Ping 检查数据库连接
func (s *systemService) Ping() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return fmt.Errorf("get database connection: %w", err)
	}
	return sqlDB.Ping()
}

// GetStats 获取系统统计信息
func (s *systemService) GetStats() (*model.SystemStatsResponse, time.Time, error) {
	var stats model.SystemStatsResponse

	// 照片统计：一条 SQL 同时获取总数、已分析数、存储空间
	var photoStats struct {
		Total    int64 `gorm:"column:total"`
		Analyzed int64 `gorm:"column:analyzed"`
		Size     int64 `gorm:"column:size"`
	}
	if err := s.db.Model(&model.Photo{}).
		Where("status = ?", model.PhotoStatusActive).
		Select("COUNT(*) as total, SUM(CASE WHEN ai_analyzed = 1 THEN 1 ELSE 0 END) as analyzed, COALESCE(SUM(file_size), 0) as size").
		Scan(&photoStats).Error; err != nil {
		return nil, s.startTime, fmt.Errorf("query photo stats: %w", err)
	}
	stats.TotalPhotos = photoStats.Total
	stats.AnalyzedPhotos = photoStats.Analyzed
	stats.UnanalyzedPhotos = photoStats.Total - photoStats.Analyzed
	stats.StorageSize = photoStats.Size

	// 设备统计：一条 SQL 同时获取总数和在线数
	fiveMinutesAgo := time.Now().Add(-5 * time.Minute)
	var deviceStats struct {
		Total  int64 `gorm:"column:total"`
		Online int64 `gorm:"column:online"`
	}
	if err := s.db.Model(&model.Device{}).
		Select("COUNT(*) as total, SUM(CASE WHEN last_seen > ? THEN 1 ELSE 0 END) as online", fiveMinutesAgo).
		Scan(&deviceStats).Error; err != nil {
		return nil, s.startTime, fmt.Errorf("query device stats: %w", err)
	}
	stats.TotalDevices = deviceStats.Total
	stats.OnlineDevices = deviceStats.Online

	// 展示记录总数
	if err := s.db.Model(&model.DisplayRecord{}).Count(&stats.TotalDisplays).Error; err != nil {
		return nil, s.startTime, fmt.Errorf("query display stats: %w", err)
	}

	return &stats, s.startTime, nil
}

// ResetSystem 重置系统数据库状态
func (s *systemService) ResetSystem() error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, table := range resettableNames() {
			exists, err := tableExists(tx, table)
			if err != nil {
				return fmt.Errorf("check table %s exists: %w", table, err)
			}
			if !exists {
				continue
			}

			if err := tx.Exec(fmt.Sprintf("DELETE FROM %s", table)).Error; err != nil {
				return fmt.Errorf("clear table %s: %w", table, err)
			}
			logger.Infof("Table %s cleared", table)
		}

		if err := resetSQLiteSequences(tx); err != nil {
			return err
		}

		if err := resetUserPasswordTx(tx); err != nil {
			return err
		}

		return nil
	})
}

func resettableNames() []string {
	return []string{
		"result_queue",
		"display_records",
		"daily_display_assets",
		"daily_display_items",
		"device_playback_states",
		"daily_display_batches",
		"analysis_runtime_leases",
		"devices",
		"photos",
		"app_config",
		"api_keys",
	}
}

func tableExists(tx *gorm.DB, table string) (bool, error) {
	var count int64
	if err := tx.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func resetSQLiteSequences(tx *gorm.DB) error {
	exists, err := tableExists(tx, "sqlite_sequence")
	if err != nil || !exists {
		return err
	}

	tableNames := append(resettableNames(), "users")
	placeholders := make([]string, 0, len(tableNames))
	args := make([]interface{}, 0, len(tableNames))
	for _, tableName := range tableNames {
		placeholders = append(placeholders, "?")
		args = append(args, tableName)
	}

	query := fmt.Sprintf("DELETE FROM sqlite_sequence WHERE name IN (%s)", strings.Join(placeholders, ","))
	if err := tx.Exec(query, args...).Error; err != nil {
		return fmt.Errorf("reset sqlite sequences: %w", err)
	}

	return nil
}

// resetUserPasswordTx 重置用户密码为 admin/admin
func resetUserPasswordTx(tx *gorm.DB) error {
	PasswordHash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash default Password: %w", err)
	}

	if err := tx.Exec("DELETE FROM users").Error; err != nil {
		return fmt.Errorf("failed to clear users table: %w", err)
	}

	defaultUser := &model.User{
		Username:     "admin",
		PasswordHash: string(PasswordHash),
		IsFirstLogin: true,
	}

	if err := tx.Create(defaultUser).Error; err != nil {
		return fmt.Errorf("failed to create default user: %w", err)
	}

	logger.Info("User password reset to admin/admin")

	return nil
}
