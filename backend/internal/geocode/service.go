package geocode

import (
	"fmt"
	"sync"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// Service 地理编码服务
type Service struct {
	providers []Provider
	cache     *locationCache
	config    *Config
}

// NewService 创建地理编码服务
func NewService(config *Config, providers ...Provider) *Service {
	s := &Service{
		providers: providers,
		config:    config,
	}

	// 初始化缓存
	if config.CacheEnabled {
		if config.CacheTTL <= 0 {
			config.CacheTTL = 3600 // 默认 1 小时
		}
		s.cache = newLocationCache(time.Duration(config.CacheTTL) * time.Second)
	}

	return s
}

// ReverseGeocode 反向地理编码（支持多提供商 fallback）
func (s *Service) ReverseGeocode(lat, lon float64) (*Location, error) {
	// 检查缓存
	if s.cache != nil {
		if cached := s.cache.get(lat, lon); cached != nil {
			logger.Debugf("Geocode cache hit for (%.6f,%.6f)", lat, lon)
			return cached, nil
		}
	}

	// 尝试所有可用的提供商
	var lastErr error
	for _, provider := range s.providers {
		if !provider.IsAvailable() {
			logger.Debugf("Provider %s is not available, skipping", provider.Name())
			continue
		}

		logger.Debugf("Trying provider: %s", provider.Name())
		location, err := provider.ReverseGeocode(lat, lon)
		if err != nil {
			logger.Warnf("Provider %s failed: %v", provider.Name(), err)
			lastErr = err
			continue
		}

		// 成功获取位置
		if location != nil && location.FormatShort() != "" {
			logger.Infof("Geocode success with %s: (%.6f,%.6f) -> %s",
				provider.Name(), lat, lon, location.FormatShort())

			// 保存到缓存
			if s.cache != nil {
				s.cache.set(lat, lon, location)
			}

			return location, nil
		}
	}

	// 所有提供商都失败了
	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed, last error: %w", lastErr)
	}
	return nil, fmt.Errorf("no available geocoding provider")
}

// GetAvailableProviders 获取可用的提供商列表
func (s *Service) GetAvailableProviders() []string {
	var names []string
	for _, p := range s.providers {
		if p.IsAvailable() {
			names = append(names, p.Name())
		}
	}
	return names
}

// locationCache 位置缓存
type locationCache struct {
	cache map[string]*cacheEntry
	ttl   time.Duration
	mu    sync.RWMutex
}

type cacheEntry struct {
	location  *Location
	expiresAt time.Time
}

func newLocationCache(ttl time.Duration) *locationCache {
	return &locationCache{
		cache: make(map[string]*cacheEntry),
		ttl:   ttl,
	}
}

func (c *locationCache) key(lat, lon float64) string {
	// 精度到小数点后 4 位（约 11 米）
	return fmt.Sprintf("%.4f,%.4f", lat, lon)
}

func (c *locationCache) get(lat, lon float64) *Location {
	c.mu.RLock()
	defer c.mu.RUnlock()

	key := c.key(lat, lon)
	entry, exists := c.cache[key]
	if !exists {
		return nil
	}

	// 检查是否过期
	if time.Now().After(entry.expiresAt) {
		return nil
	}

	return entry.location
}

func (c *locationCache) set(lat, lon float64, location *Location) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := c.key(lat, lon)
	c.cache[key] = &cacheEntry{
		location:  location,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// CleanExpired 清理过期缓存
func (c *locationCache) CleanExpired() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, entry := range c.cache {
		if now.After(entry.expiresAt) {
			delete(c.cache, key)
		}
	}
}
