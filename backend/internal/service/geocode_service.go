package service

import (
	"fmt"

	"github.com/davidhoo/relive/internal/geocode"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"gorm.io/gorm"
)

// GeocodeService 地理编码服务接口
type GeocodeService interface {
	// ReverseGeocode 根据 GPS 坐标获取位置信息
	ReverseGeocode(lat, lon float64) (*geocode.Location, error)
	// GetAvailableProviders 获取可用的提供商列表
	GetAvailableProviders() []string
}

// geocodeService 地理编码服务实现
type geocodeService struct {
	service *geocode.Service
}

// NewGeocodeService 创建地理编码服务
func NewGeocodeService(db *gorm.DB, cfg *config.Config) (GeocodeService, error) {
	if cfg.Geocode.Provider == "" {
		return nil, fmt.Errorf("geocode provider not configured")
	}

	// 准备提供商列表
	var providers []geocode.Provider

	// 根据配置初始化提供商
	switch cfg.Geocode.Provider {
	case "amap":
		if cfg.Geocode.AMapAPIKey == "" {
			logger.Warn("AMap API key not configured, skipping AMap provider")
		} else {
			providers = append(providers, geocode.NewAmapProvider(
				cfg.Geocode.AMapAPIKey,
				cfg.Geocode.AMapTimeout,
			))
		}
	case "nominatim":
		providers = append(providers, geocode.NewNominatimProvider(
			cfg.Geocode.NominatimEndpoint,
			cfg.Geocode.NominatimTimeout,
		))
	case "offline":
		providers = append(providers, geocode.NewOfflineProvider(
			db,
			cfg.Geocode.OfflineMaxDistance,
		))
	}

	// 添加 fallback 提供商
	if cfg.Geocode.Fallback != "" && cfg.Geocode.Fallback != cfg.Geocode.Provider {
		switch cfg.Geocode.Fallback {
		case "amap":
			if cfg.Geocode.AMapAPIKey != "" {
				providers = append(providers, geocode.NewAmapProvider(
					cfg.Geocode.AMapAPIKey,
					cfg.Geocode.AMapTimeout,
				))
			}
		case "nominatim":
			providers = append(providers, geocode.NewNominatimProvider(
				cfg.Geocode.NominatimEndpoint,
				cfg.Geocode.NominatimTimeout,
			))
		case "offline":
			providers = append(providers, geocode.NewOfflineProvider(
				db,
				cfg.Geocode.OfflineMaxDistance,
			))
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no geocode providers available")
	}

	// 创建 geocode 配置
	geocodeConfig := &geocode.Config{
		Provider:           cfg.Geocode.Provider,
		Fallback:           cfg.Geocode.Fallback,
		AMapAPIKey:         cfg.Geocode.AMapAPIKey,
		AMapTimeout:        cfg.Geocode.AMapTimeout,
		NominatimEndpoint:  cfg.Geocode.NominatimEndpoint,
		NominatimTimeout:   cfg.Geocode.NominatimTimeout,
		OfflineMaxDistance: cfg.Geocode.OfflineMaxDistance,
		CacheEnabled:       cfg.Geocode.CacheEnabled,
		CacheTTL:           cfg.Geocode.CacheTTL,
	}

	// 创建服务
	service := geocode.NewService(geocodeConfig, providers...)

	logger.Infof("Geocode service initialized with providers: %v", service.GetAvailableProviders())

	return &geocodeService{
		service: service,
	}, nil
}

// ReverseGeocode 根据 GPS 坐标获取位置信息
func (s *geocodeService) ReverseGeocode(lat, lon float64) (*geocode.Location, error) {
	return s.service.ReverseGeocode(lat, lon)
}

// GetAvailableProviders 获取可用的提供商列表
func (s *geocodeService) GetAvailableProviders() []string {
	return s.service.GetAvailableProviders()
}
