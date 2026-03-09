package service

import (
	"encoding/json"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupConfigServiceForTests(t *testing.T) ConfigService {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := db.AutoMigrate(&model.AppConfig{}); err != nil {
		t.Fatalf("migrate app_config: %v", err)
	}
	return NewConfigService(repository.NewConfigRepository(db))
}

func TestAIServiceLoadAIConfig_PrefersDatabaseOverYAML(t *testing.T) {
	configService := setupConfigServiceForTests(t)

	dbValue := AIConfigFromDB{
		Provider:       "qwen",
		QwenModel:      "db-model",
		QwenEndpoint:   "https://db-endpoint.example.com",
		QwenTimeout:    45,
		Temperature:    0.3,
		Timeout:        45,
		OllamaModel:    "should-not-win",
		HybridPrimary:  "qwen",
		HybridFallback: "ollama",
	}
	data, _ := json.Marshal(dbValue)
	if err := configService.Set("ai", string(data)); err != nil {
		t.Fatalf("set ai config: %v", err)
	}

	svc := &aiService{
		config: &config.Config{
			AI: config.AIConfig{
				Provider: "ollama",
				Ollama: config.OllamaConfig{Model: "yaml-model", Endpoint: "http://yaml-endpoint"},
				Qwen: config.QwenConfig{Model: "yaml-qwen", Endpoint: "https://yaml-qwen.example.com"},
			},
		},
		configService: configService,
	}

	got := svc.loadAIConfig()

	if got.Provider != "qwen" {
		t.Fatalf("expected db provider qwen, got %q", got.Provider)
	}
	if got.QwenModel != "db-model" {
		t.Fatalf("expected db qwen model, got %q", got.QwenModel)
	}
	if got.QwenEndpoint != "https://db-endpoint.example.com" {
		t.Fatalf("expected db qwen endpoint, got %q", got.QwenEndpoint)
	}
}

func TestGeocodeServiceLoadGeocodeConfig_PrefersDatabaseOverYAML(t *testing.T) {
	configService := setupConfigServiceForTests(t)

	dbValue := config.GeocodeConfig{
		Provider:          "nominatim",
		NominatimEndpoint: "https://db-nominatim.example.com/reverse",
		NominatimTimeout:  22,
		OfflineMaxDistance: 12,
	}
	data, _ := json.Marshal(dbValue)
	if err := configService.Set("geocode", string(data)); err != nil {
		t.Fatalf("set geocode config: %v", err)
	}

	svc := &geocodeService{
		configService: configService,
		cfg: &config.Config{Geocode: config.GeocodeConfig{
			Provider:          "offline",
			NominatimEndpoint: "https://yaml-nominatim.example.com/reverse",
			NominatimTimeout:  10,
			OfflineMaxDistance: 100,
		}},
	}

	got := svc.loadGeocodeConfig()

	if got.Provider != "nominatim" {
		t.Fatalf("expected db provider nominatim, got %q", got.Provider)
	}
	if got.GetNominatimEndpoint() != "https://db-nominatim.example.com/reverse" {
		t.Fatalf("expected db nominatim endpoint, got %q", got.GetNominatimEndpoint())
	}
	if got.GetNominatimTimeout() != 22 {
		t.Fatalf("expected db nominatim timeout 22, got %d", got.GetNominatimTimeout())
	}
}

func TestDisplayServiceGetDisplayStrategyConfig_PrefersDatabaseOverDefaults(t *testing.T) {
	configService := setupConfigServiceForTests(t)

	dbValue := model.DisplayStrategyConfig{
		Algorithm:      "random",
		MinBeautyScore: 88,
		MinMemoryScore: 77,
		DailyCount:     5,
	}
	data, _ := json.Marshal(dbValue)
	if err := configService.Set("display.strategy", string(data)); err != nil {
		t.Fatalf("set display strategy: %v", err)
	}

	svc := &displayService{configService: configService, config: &config.Config{}}
	got := svc.getDisplayStrategyConfig()

	if got.Algorithm != "random" {
		t.Fatalf("expected db algorithm random, got %q", got.Algorithm)
	}
	if got.MinBeautyScore != 88 {
		t.Fatalf("expected db beauty score 88, got %d", got.MinBeautyScore)
	}
	if got.DailyCount != 5 {
		t.Fatalf("expected db daily count 5, got %d", got.DailyCount)
	}
}
