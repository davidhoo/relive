package service

import (
	"encoding/json"
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
	// Reload 重新加载配置（热重载）
	Reload(db *gorm.DB, cfg *config.Config) error
}

// geocodeService 地理编码服务实现
type geocodeService struct {
	service       *geocode.Service
	configService ConfigService
	db            *gorm.DB
	cfg           *config.Config
}

// NewGeocodeService 创建地理编码服务
// configService 可为 nil（启动时尚未就绪时），之后通过 Reload 会使用数据库配置
func NewGeocodeService(db *gorm.DB, cfg *config.Config, configService ...ConfigService) (GeocodeService, error) {
	var cs ConfigService
	if len(configService) > 0 {
		cs = configService[0]
	}

	svc := &geocodeService{
		configService: cs,
		db:            db,
		cfg:           cfg,
	}

	geocodeCfg := svc.loadGeocodeConfig()
	service, err := buildGeocodeService(db, geocodeCfg)
	if err != nil {
		return nil, err
	}

	svc.service = service
	return svc, nil
}

// loadGeocodeConfig 加载 geocode 配置（优先从数据库，其次从 YAML）
func (s *geocodeService) loadGeocodeConfig() config.GeocodeConfig {
	// 以 YAML 配置为默认值
	geocodeCfg := s.cfg.Geocode

	// 尝试从数据库读取配置（覆盖 YAML）
	if s.configService != nil {
		dbConfig, err := s.configService.Get("geocode")
		if err == nil && dbConfig != nil && dbConfig.Value != "" {
			var dbGeocodeConfig config.GeocodeConfig
			if err := json.Unmarshal([]byte(dbConfig.Value), &dbGeocodeConfig); err == nil {
				logger.Info("Loading geocode config from database")
				// 覆盖扁平字段（保留 YAML 嵌套结构作为后备）
				if dbGeocodeConfig.Provider != "" {
					geocodeCfg.Provider = dbGeocodeConfig.Provider
				}
				geocodeCfg.Fallback = dbGeocodeConfig.Fallback
				geocodeCfg.CacheEnabled = dbGeocodeConfig.CacheEnabled
				geocodeCfg.CacheTTL = dbGeocodeConfig.CacheTTL
				if dbGeocodeConfig.AMapAPIKey != "" {
					geocodeCfg.AMapAPIKey = dbGeocodeConfig.AMapAPIKey
				}
				if dbGeocodeConfig.AMapTimeout > 0 {
					geocodeCfg.AMapTimeout = dbGeocodeConfig.AMapTimeout
				}
				if dbGeocodeConfig.NominatimEndpoint != "" {
					geocodeCfg.NominatimEndpoint = dbGeocodeConfig.NominatimEndpoint
				}
				if dbGeocodeConfig.NominatimTimeout > 0 {
					geocodeCfg.NominatimTimeout = dbGeocodeConfig.NominatimTimeout
				}
				if dbGeocodeConfig.OfflineMaxDistance > 0 {
					geocodeCfg.OfflineMaxDistance = dbGeocodeConfig.OfflineMaxDistance
				}
				if dbGeocodeConfig.WeiboAPIKey != "" {
					geocodeCfg.WeiboAPIKey = dbGeocodeConfig.WeiboAPIKey
				}
				if dbGeocodeConfig.WeiboTimeout > 0 {
					geocodeCfg.WeiboTimeout = dbGeocodeConfig.WeiboTimeout
				}
			} else {
				logger.Warnf("Failed to parse geocode config from database: %v", err)
			}
		}
	}

	return geocodeCfg
}

// buildGeocodeService 根据配置构建 geocode.Service
func buildGeocodeService(db *gorm.DB, geocodeCfg config.GeocodeConfig) (*geocode.Service, error) {
	if geocodeCfg.Provider == "" {
		return nil, fmt.Errorf("geocode provider not configured")
	}

	var providers []geocode.Provider

	// 根据配置初始化主提供商
	switch geocodeCfg.Provider {
	case "amap":
		if geocodeCfg.GetAMapAPIKey() == "" {
			logger.Warn("AMap API key not configured, skipping AMap provider")
		} else {
			providers = append(providers, geocode.NewAmapProvider(
				geocodeCfg.GetAMapAPIKey(),
				geocodeCfg.GetAMapTimeout(),
			))
		}
	case "nominatim":
		providers = append(providers, geocode.NewNominatimProvider(
			geocodeCfg.GetNominatimEndpoint(),
			geocodeCfg.GetNominatimTimeout(),
		))
	case "offline":
		providers = append(providers, geocode.NewOfflineProvider(
			db,
			geocodeCfg.GetOfflineMaxDistance(),
		))
	case "weibo":
		if geocodeCfg.GetWeiboAPIKey() == "" {
			logger.Warn("Weibo API key not configured, skipping Weibo provider")
		} else {
			providers = append(providers, geocode.NewWeiboProvider(
				geocodeCfg.GetWeiboAPIKey(),
				geocodeCfg.GetWeiboTimeout(),
			))
		}
	}

	// 添加 fallback 提供商
	if geocodeCfg.Fallback != "" && geocodeCfg.Fallback != geocodeCfg.Provider {
		switch geocodeCfg.Fallback {
		case "amap":
			if geocodeCfg.GetAMapAPIKey() != "" {
				providers = append(providers, geocode.NewAmapProvider(
					geocodeCfg.GetAMapAPIKey(),
					geocodeCfg.GetAMapTimeout(),
				))
			}
		case "nominatim":
			providers = append(providers, geocode.NewNominatimProvider(
				geocodeCfg.GetNominatimEndpoint(),
				geocodeCfg.GetNominatimTimeout(),
			))
		case "offline":
			providers = append(providers, geocode.NewOfflineProvider(
				db,
				geocodeCfg.GetOfflineMaxDistance(),
			))
		case "weibo":
			if geocodeCfg.GetWeiboAPIKey() != "" {
				providers = append(providers, geocode.NewWeiboProvider(
					geocodeCfg.GetWeiboAPIKey(),
					geocodeCfg.GetWeiboTimeout(),
				))
			}
		}
	}

	if len(providers) == 0 {
		return nil, fmt.Errorf("no geocode providers available")
	}

	// 创建 geocode 配置
	geocodeConfig := &geocode.Config{
		Provider:           geocodeCfg.Provider,
		Fallback:           geocodeCfg.Fallback,
		AMapAPIKey:         geocodeCfg.GetAMapAPIKey(),
		AMapTimeout:        geocodeCfg.GetAMapTimeout(),
		NominatimEndpoint:  geocodeCfg.GetNominatimEndpoint(),
		NominatimTimeout:   geocodeCfg.GetNominatimTimeout(),
		OfflineMaxDistance: geocodeCfg.GetOfflineMaxDistance(),
		WeiboAPIKey:        geocodeCfg.GetWeiboAPIKey(),
		WeiboTimeout:       geocodeCfg.GetWeiboTimeout(),
		CacheEnabled:       geocodeCfg.CacheEnabled,
		CacheTTL:           geocodeCfg.CacheTTL,
	}

	service := geocode.NewService(geocodeConfig, providers...)
	logger.Infof("Geocode service initialized with providers: %v", service.GetAvailableProviders())
	return service, nil
}

// ReverseGeocode 根据 GPS 坐标获取位置信息
func (s *geocodeService) ReverseGeocode(lat, lon float64) (*geocode.Location, error) {
	return s.service.ReverseGeocode(lat, lon)
}

// GetAvailableProviders 获取可用的提供商列表
func (s *geocodeService) GetAvailableProviders() []string {
	return s.service.GetAvailableProviders()
}

// Reload 重新加载配置（热重载）
func (s *geocodeService) Reload(db *gorm.DB, cfg *config.Config) error {
	// 更新存储的引用
	s.db = db
	s.cfg = cfg

	// 从数据库（或 YAML）加载最新配置
	geocodeCfg := s.loadGeocodeConfig()

	service, err := buildGeocodeService(db, geocodeCfg)
	if err != nil {
		return fmt.Errorf("failed to reload geocode service: %w", err)
	}

	s.service = service
	logger.Infof("Geocode service reloaded successfully with providers: %v", s.GetAvailableProviders())
	return nil
}
