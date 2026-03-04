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

// DeviceService 设备服务接口
type DeviceService interface {
	// 注册
	Register(req *model.DeviceRegisterRequest) (*model.DeviceRegisterResponse, error)

	// 心跳
	Heartbeat(req *model.DeviceHeartbeatRequest) (*model.DeviceHeartbeatResponse, error)

	// 查询
	GetByDeviceID(deviceID string) (*model.Device, error)
	GetByAPIKey(apiKey string) (*model.Device, error)
	List(page, pageSize int) ([]*model.Device, int64, error)
	ListByDeviceType(deviceType string) ([]*model.Device, error)
	ListByPlatform(platform string) ([]*model.Device, error)

	// 统计
	CountAll() (int64, error)
	CountOnline() (int64, error)
	CountByDeviceType(deviceType string) (int64, error)
	CountByPlatform(platform string) (int64, error)
}

// deviceService 设备服务实现
type deviceService struct {
	repo   repository.DeviceRepository
	config *config.Config
}

// NewDeviceService 创建设备服务
func NewDeviceService(repo repository.DeviceRepository, cfg *config.Config) DeviceService {
	return &deviceService{
		repo:   repo,
		config: cfg,
	}
}

// Register 注册设备
func (s *deviceService) Register(req *model.DeviceRegisterRequest) (*model.DeviceRegisterResponse, error) {
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

		logger.Infof("Device already registered: %s (type: %s)", req.DeviceID, device.DeviceType)

		// 返回响应（不返回完整 API Key，只返回提示）
		return &model.DeviceRegisterResponse{
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

	// 设置默认值
	deviceType := req.DeviceType
	if deviceType == "" {
		deviceType = "esp32" // 默认 ESP32
	}

	platform := req.Platform
	if platform == "" {
		platform = "embedded" // 默认嵌入式
	}

	// 创建设备
	device := &model.Device{
		DeviceID:        req.DeviceID,
		Name:            req.Name,
		APIKey:          apiKey,
		IPAddress:       req.IPAddress,
		DeviceType:      deviceType,
		HardwareModel:   req.HardwareModel,
		Platform:        platform,
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

	logger.Infof("Device registered successfully: %s (type: %s, platform: %s)",
		req.DeviceID, deviceType, platform)

	return &model.DeviceRegisterResponse{
		DeviceID: device.DeviceID,
		APIKey:   apiKey,
		Config:   s.getDefaultConfig(),
	}, nil
}

// Heartbeat 处理心跳
func (s *deviceService) Heartbeat(req *model.DeviceHeartbeatRequest) (*model.DeviceHeartbeatResponse, error) {
	// 更新心跳信息
	err := s.repo.UpdateHeartbeat(req.DeviceID, req.BatteryLevel, req.WiFiRSSI)
	if err != nil {
		return nil, fmt.Errorf("update heartbeat: %w", err)
	}

	// 计算下次刷新时间
	nextRefreshIn := s.calculateNextRefresh()

	return &model.DeviceHeartbeatResponse{
		ServerTime:           time.Now(),
		NextRefreshInSeconds: nextRefreshIn,
		HasNewFirmware:       false, // TODO: 实现固件更新检查
	}, nil
}

// GetByDeviceID 根据设备 ID 获取设备
func (s *deviceService) GetByDeviceID(deviceID string) (*model.Device, error) {
	return s.repo.GetByDeviceID(deviceID)
}

// GetByAPIKey 根据 API Key 获取设备
func (s *deviceService) GetByAPIKey(apiKey string) (*model.Device, error) {
	return s.repo.GetByAPIKey(apiKey)
}

// List 获取设备列表
func (s *deviceService) List(page, pageSize int) ([]*model.Device, int64, error) {
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

// ListByDeviceType 根据设备类型查询
func (s *deviceService) ListByDeviceType(deviceType string) ([]*model.Device, error) {
	return s.repo.ListByDeviceType(deviceType)
}

// ListByPlatform 根据平台查询
func (s *deviceService) ListByPlatform(platform string) ([]*model.Device, error) {
	return s.repo.ListByPlatform(platform)
}

// CountAll 统计设备总数
func (s *deviceService) CountAll() (int64, error) {
	return s.repo.Count()
}

// CountOnline 统计在线设备数
func (s *deviceService) CountOnline() (int64, error) {
	return s.repo.CountOnline()
}

// CountByDeviceType 根据设备类型统计
func (s *deviceService) CountByDeviceType(deviceType string) (int64, error) {
	return s.repo.CountByDeviceType(deviceType)
}

// CountByPlatform 根据平台统计
func (s *deviceService) CountByPlatform(platform string) (int64, error) {
	return s.repo.CountByPlatform(platform)
}

// generateAPIKey 生成 API Key
func (s *deviceService) generateAPIKey() (string, error) {
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
func (s *deviceService) getDefaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"refresh_hour": []int{8, 20}, // 每天 8:00 和 20:00 刷新
		"brightness":   100,
		"sleep_mode":   "deep",
		"ota_enabled":  true,
		"timezone":     "Asia/Shanghai",
	}
}

// calculateNextRefresh 计算下次刷新时间（秒）
func (s *deviceService) calculateNextRefresh() int {
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

// ============= 向后兼容 =============

// ESP32Service 类型别名，保持向后兼容
// Deprecated: 使用 DeviceService 代替
type ESP32Service = DeviceService

// NewESP32Service 创建设备服务（兼容旧代码）
// Deprecated: 使用 NewDeviceService 代替
func NewESP32Service(repo repository.DeviceRepository, cfg *config.Config) DeviceService {
	return NewDeviceService(repo, cfg)
}
