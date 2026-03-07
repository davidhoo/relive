package service

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

// DeviceService 设备服务接口
type DeviceService interface {
	// 设备管理（管理员操作）
	Create(req *model.CreateDeviceRequest) (*model.CreateDeviceResponse, error)
	Delete(id uint) error
	Update(device *model.Device) error
	UpdateEnabled(id uint, enabled bool) error // 更新设备可用状态
	UpdateRenderProfile(id uint, renderProfile string) error

	// 设备激活（设备使用预分配的 API Key 激活）
	Activate(req *model.DeviceActivateRequest) (*model.DeviceActivateResponse, error)

	// 心跳
	Heartbeat(req *model.DeviceHeartbeatRequest) (*model.DeviceHeartbeatResponse, error)

	// 查询
	GetByID(id uint) (*model.Device, error)
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

	// 更新最后请求时间（异步）
	UpdateLastSeen(deviceID uint, ip string)
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

// Create 创建设备（管理员操作）
func (s *deviceService) Create(req *model.CreateDeviceRequest) (*model.CreateDeviceResponse, error) {
	// 生成 API Key
	apiKey, err := s.generateAPIKey()
	if err != nil {
		return nil, fmt.Errorf("generate api key: %w", err)
	}

	// 生成设备 ID（短格式，便于显示和输入）
	deviceID, err := s.generateDeviceID()
	if err != nil {
		return nil, fmt.Errorf("generate device id: %w", err)
	}

	// 设置默认值
	deviceType := req.DeviceType
	if deviceType == "" {
		deviceType = "embedded"
	}

	// 创建设备记录
	renderProfile := ""
	if deviceType == "embedded" {
		renderProfile = req.RenderProfile
		if renderProfile == "" {
			renderProfile = util.DefaultRenderProfile()
		}
	}
	device := &model.Device{
		DeviceID:      deviceID,
		Name:          req.Name,
		APIKey:        apiKey,
		DeviceType:    deviceType,
		RenderProfile: renderProfile,
		IsEnabled:     true,  // 新设备默认可用
		Online:        false, // 新设备默认离线，等待激活
	}

	if err := s.repo.Create(device); err != nil {
		return nil, fmt.Errorf("create device: %w", err)
	}

	logger.Infof("Device created by admin: %s (name: %s, type: %s)",
		deviceID, req.Name, deviceType)

	return &model.CreateDeviceResponse{
		ID:            device.ID,
		CreatedAt:     device.CreatedAt,
		DeviceID:      device.DeviceID,
		Name:          device.Name,
		APIKey:        apiKey, // ⚠️ 仅创建时返回
		DeviceType:    device.DeviceType,
		RenderProfile: device.RenderProfile,
	}, nil
}

// Delete 删除设备
func (s *deviceService) Delete(id uint) error {
	return s.repo.Delete(id)
}

// Update 更新设备信息
func (s *deviceService) Update(device *model.Device) error {
	return s.repo.Update(device)
}

// UpdateEnabled 更新设备可用状态
func (s *deviceService) UpdateEnabled(id uint, enabled bool) error {
	device, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("device not found: %w", err)
	}

	device.IsEnabled = enabled
	if err := s.repo.Update(device); err != nil {
		return fmt.Errorf("update device enabled status: %w", err)
	}

	status := "disabled"
	if enabled {
		status = "enabled"
	}
	logger.Infof("Device %s %s by admin", device.DeviceID, status)
	return nil
}

func (s *deviceService) UpdateRenderProfile(id uint, renderProfile string) error {
	device, err := s.repo.GetByID(id)
	if err != nil {
		return fmt.Errorf("device not found: %w", err)
	}
	if device.DeviceType != "embedded" {
		device.RenderProfile = ""
	} else {
		if renderProfile == "" {
			renderProfile = util.DefaultRenderProfile()
		}
		device.RenderProfile = renderProfile
	}
	if err := s.repo.Update(device); err != nil {
		return fmt.Errorf("update render profile: %w", err)
	}
	logger.Infof("Device %s render profile updated to %s", device.DeviceID, device.RenderProfile)
	return nil
}

// GetByID 根据 ID 获取设备
func (s *deviceService) GetByID(id uint) (*model.Device, error) {
	return s.repo.GetByID(id)
}

// Activate 设备激活（设备使用预分配的 API Key 激活）
func (s *deviceService) Activate(req *model.DeviceActivateRequest) (*model.DeviceActivateResponse, error) {
	// 查找设备
	device, err := s.repo.GetByDeviceID(req.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("device not found: %w", err)
	}

	// 更新设备信息（设备上报的信息）
	if req.Name != "" {
		device.Name = req.Name
	}
	if req.DeviceType != "" {
		device.DeviceType = req.DeviceType
	}

	// 激活时更新状态
	device.Online = true
	now := time.Now()
	device.LastHeartbeat = &now
	if req.IPAddress != "" {
		device.IPAddress = req.IPAddress
	}

	if err := s.repo.Update(device); err != nil {
		return nil, fmt.Errorf("update device: %w", err)
	}

	logger.Infof("Device activated: %s (IP: %s)", req.DeviceID, req.IPAddress)

	return &model.DeviceActivateResponse{
		DeviceID: device.DeviceID,
		Name:     device.Name,
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

// UpdateLastSeen 更新设备最后请求时间和 IP。
func (s *deviceService) UpdateLastSeen(deviceID uint, ip string) {
	device, err := s.repo.GetByID(deviceID)
	if err != nil {
		return
	}

	now := time.Now()
	device.LastHeartbeat = &now
	device.Online = true
	if ip != "" {
		device.IPAddress = ip
	}

	_ = s.repo.Update(device)
}

// generateDeviceID 生成设备 ID（8位随机字符串，便于显示和输入）
func (s *deviceService) generateDeviceID() (string, error) {
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 8

	for {
		result := make([]byte, length)
		if _, err := rand.Read(result); err != nil {
			return "", err
		}

		for i := range result {
			result[i] = charset[int(result[i])%len(charset)]
		}

		deviceID := string(result)

		// 检查是否已存在（极低概率）
		exists, err := s.repo.ExistsByDeviceID(deviceID)
		if err != nil {
			return "", err
		}
		if !exists {
			return deviceID, nil
		}
		// 已存在则重新生成
	}
}

// generateAPIKey 生成 API Key（格式: sk-relive- + 32位十六进制）
func (s *deviceService) generateAPIKey() (string, error) {
	// 生成 16 字节随机数 → 32 位十六进制字符串
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}

	// 转换为十六进制字符串（32位）
	randomStr := hex.EncodeToString(bytes)

	// 添加前缀（使用配置中的前缀，如 sk-relive-）
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
