package service

import (
	"encoding/json"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
)

func defaultDisplayStrategyConfig() model.DisplayStrategyConfig {
	return model.DisplayStrategyConfig{
		Algorithm:            "on_this_day",
		MinBeautyScore:       70,
		MinMemoryScore:       60,
		DailyCount:           3,
		CandidatePoolFactor:  5,
		MinTimeGapHours:      24,
		MaxPhotosPerEvent:    1,
		MaxPhotosPerLocation: 1,
		LocationBucketKM:     3,
	}
}

func normalizeDisplayStrategyConfig(cfg *model.DisplayStrategyConfig) {
	if cfg.Algorithm == "" {
		cfg.Algorithm = "on_this_day"
	}
	if cfg.Algorithm == "smart" {
		cfg.Algorithm = "on_this_day"
	}
	if cfg.MinBeautyScore < 0 {
		cfg.MinBeautyScore = 0
	}
	if cfg.MinBeautyScore > 100 {
		cfg.MinBeautyScore = 100
	}
	if cfg.MinMemoryScore < 0 {
		cfg.MinMemoryScore = 0
	}
	if cfg.MinMemoryScore > 100 {
		cfg.MinMemoryScore = 100
	}
	if cfg.DailyCount <= 0 {
		cfg.DailyCount = 3
	}
	if cfg.DailyCount > 20 {
		cfg.DailyCount = 20
	}
	if cfg.CandidatePoolFactor <= 0 {
		cfg.CandidatePoolFactor = 5
	}
	if cfg.CandidatePoolFactor > 20 {
		cfg.CandidatePoolFactor = 20
	}
	if cfg.MinTimeGapHours < 0 {
		cfg.MinTimeGapHours = 0
	}
	if cfg.MinTimeGapHours == 0 {
		cfg.MinTimeGapHours = 24
	}
	if cfg.MaxPhotosPerEvent <= 0 {
		cfg.MaxPhotosPerEvent = 1
	}
	if cfg.MaxPhotosPerLocation <= 0 {
		cfg.MaxPhotosPerLocation = 1
	}
	if cfg.LocationBucketKM <= 0 {
		cfg.LocationBucketKM = 3
	}
}

func (s *displayService) getDisplayStrategyConfig() model.DisplayStrategyConfig {
	cfg := defaultDisplayStrategyConfig()
	if s.configService == nil {
		return cfg
	}

	value, err := s.configService.GetWithDefault("display.strategy", "")
	if err != nil {
		logger.Warnf("Load display strategy config failed: %v", err)
		return cfg
	}
	if value == "" {
		return cfg
	}

	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		logger.Warnf("Parse display strategy config failed: %v", err)
		return defaultDisplayStrategyConfig()
	}

	normalizeDisplayStrategyConfig(&cfg)
	return cfg
}
