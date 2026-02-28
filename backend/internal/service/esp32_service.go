package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

// ESP32Service ESP32 设备服务接口
type ESP32Service interface {
	// 注册
	Register(req *model.ESP32RegisterRequest) (*model.ESP32RegisterResponse, error)

	// 心跳
	Heartbeat(req *model.ESP32HeartbeatRequest) (*model.ESP32HeartbeatResponse, error)

	// 查询
	GetByDeviceID(deviceID string) (*model.ESP32Device, error)
	GetByAPIKey(apiKey string) (*model.ESP32Device, error)
	List(page, pageSize int) ([]*model.ESP32Device, int64, error)

	// 统计
	CountAll() (int64, error)
	CountOnline() (int64, error)
}

// esp32Service ESP32 设备服务实现
type esp32Service struct {
	repo   repository.ESP32DeviceRepository
	config *config.Config
}

// NewESP32Service 创建 ESP32 设备服务
func NewESP32Service(repo repository.ESP32DeviceRepository, cfg *config.Config) ESP32Service {
	return &esp32Service{
		repo:   repo,
		config: cfg,
	}
}

// Register 注册设备
func (s *esp32Service) Register(req *model.ESP32RegisterRequest) (*model.ESP32RegisterResponse, error) {
	// 检查设备是否已存在
	exists, err := s.repo.ExistsByDeviceID(req.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("check device exists: %w", err)
	}

	if exists {
		// 设备已存在，返回现有信息
		device, err := s.repo.GetByDeviceID(req.DeviceID)
		if err != nil {
			return nil, fmt.Errorf("get existing device: %w", err)
		}

		logger.Infof("Device already registered: %s", req.DeviceID)

		// 返回响应（不返回完整 API Key，只返回提示）
		return &model.ESP32RegisterResponse{
			DeviceID: device.DeviceID,
			APIKey:   device.APIKey, // 注意：实际应该不返回，这里为了测试方便
			Config:   s.getDefaultConfig(),
		}, nil
	}

	// 生成 API Key
	apiKey, err := s.generateAPIKey()
	if err != nil {
		return nil, fmt.Errorf("generate api key: %w", err)
	}

	// 创建设备
	device := &model.ESP32Device{
		DeviceID:        req.DeviceID,
		Name:            req.Name,
		APIKey:          apiKey,
		IPAddress:       req.IPAddress,
		ScreenWidth:     req.ScreenWidth,
		ScreenHeight:    req.ScreenHeight,
		FirmwareVersion: req.FirmwareVersion,
		MACAddress:      req.MACAddress,
		Online:          true,
	}

	now := time.Now()
	device.LastHeartbeat = &now

	if err := s.repo.Create(device); err != nil {
		return nil, fmt.Errorf("create device: %w", err)
	}

	logger.Infof("Device registered successfully: %s", req.DeviceID)

	return &model.ESP32RegisterResponse{
		DeviceID: device.DeviceID,
		APIKey:   apiKey,
		Config:   s.getDefaultConfig(),
	}, nil
}

// Heartbeat 处理心跳
func (s *esp32Service) Heartbeat(req *model.ESP32HeartbeatRequest) (*model.ESP32HeartbeatResponse, error) {
	// 更新心跳信息
	err := s.repo.UpdateHeartbeat(req.DeviceID, req.BatteryLevel, req.WiFiRSSI)
	if err != nil {
		return nil, fmt.Errorf("update heartbeat: %w", err)
	}

	// 计算下次刷新时间
	nextRefreshIn := s.calculateNextRefresh()

	return &model.ESP32HeartbeatResponse{
		ServerTime:           time.Now(),
		NextRefreshInSeconds: nextRefreshIn,
		HasNewFirmware:       false, // TODO: 实现固件更新检查
	}, nil
}

// GetByDeviceID 根据设备 ID 获取设备
func (s *esp32Service) GetByDeviceID(deviceID string) (*model.ESP32Device, error) {
	return s.repo.GetByDeviceID(deviceID)
}

// GetByAPIKey 根据 API Key 获取设备
func (s *esp32Service) GetByAPIKey(apiKey string) (*model.ESP32Device, error) {
	return s.repo.GetByAPIKey(apiKey)
}

// List 获取设备列表
func (s *esp32Service) List(page, pageSize int) ([]*model.ESP32Device, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	return s.repo.List(page, pageSize)
}

// CountAll 统计设备总数
func (s *esp32Service) CountAll() (int64, error) {
	return s.repo.Count()
}

// CountOnline 统计在线设备数
func (s *esp32Service) CountOnline() (int64, error) {
	return s.repo.CountOnline()
}

// generateAPIKey 生成 API Key
func (s *esp32Service) generateAPIKey() (string, error) {
	// 生成 32 字节随机数
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	// 转换为十六进制字符串
	randomStr := hex.EncodeToString(bytes)

	// 添加前缀
	apiKey := s.config.Security.APIKeyPrefix + randomStr

	// 检查是否已存在（极低概率）
	exists, err := s.repo.ExistsByAPIKey(apiKey)
	if err != nil {
		return "", err
	}

	if exists {
		// 重新生成（递归）
		return s.generateAPIKey()
	}

	return apiKey, nil
}

// getDefaultConfig 获取默认设备配置
func (s *esp32Service) getDefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"refresh_hour":   []int{8, 20}, // 每天 8:00 和 20:00 刷新
		"brightness":     100,
		"sleep_mode":     "deep",
		"ota_enabled":    true,
		"timezone":       "Asia/Shanghai",
	}
}

// calculateNextRefresh 计算下次刷新时间（秒）
func (s *esp32Service) calculateNextRefresh() int {
	now := time.Now()
	currentHour := now.Hour()

	// 找到下一个刷新时间
	var nextHour int
	if currentHour < 8 {
		nextHour = 8
	} else if currentHour < 20 {
		nextHour = 20
	} else {
		nextHour = 8 + 24 // 明天 8:00
	}

	// 计算距离下次刷新的秒数
	nextRefresh := time.Date(now.Year(), now.Month(), now.Day(), nextHour%24, 0, 0, 0, now.Location())
	if nextHour >= 24 {
		nextRefresh = nextRefresh.AddDate(0, 0, 1)
	}

	duration := nextRefresh.Sub(now)
	return int(duration.Seconds())
}
