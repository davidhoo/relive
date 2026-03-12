package service

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupGeocodeServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	require.NoError(t, db.AutoMigrate(&model.City{}, &model.AppConfig{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	return db
}

func TestGeocodeService_NewWithOfflineProvider(t *testing.T) {
	db := setupGeocodeServiceTestDB(t)

	// Insert a test city so offline provider is available
	db.Create(&model.City{GeonameID: 1, Name: "Beijing", AdminName: "01", Country: "CN", Latitude: 39.9, Longitude: 116.4})

	cfg := &config.Config{
		Geocode: config.GeocodeConfig{
			Provider:           "offline",
			OfflineMaxDistance: 100,
			CacheEnabled:       true,
			CacheTTL:           3600,
		},
	}

	svc, err := NewGeocodeService(db, cfg)
	require.NoError(t, err)

	providers := svc.GetAvailableProviders()
	assert.Contains(t, providers, "offline")
}

func TestGeocodeService_ReverseGeocode_Offline(t *testing.T) {
	db := setupGeocodeServiceTestDB(t)

	db.Create(&model.City{GeonameID: 1, Name: "Beijing", AdminName: "01", Country: "CN", Latitude: 39.9042, Longitude: 116.4074})

	cfg := &config.Config{
		Geocode: config.GeocodeConfig{
			Provider:           "offline",
			OfflineMaxDistance: 100,
			CacheEnabled:       false,
		},
	}

	svc, err := NewGeocodeService(db, cfg)
	require.NoError(t, err)

	loc, err := svc.ReverseGeocode(39.9, 116.4)
	require.NoError(t, err)
	assert.Equal(t, "Beijing", loc.City)
}

func TestGeocodeService_EmptyProvider_Error(t *testing.T) {
	db := setupGeocodeServiceTestDB(t)

	cfg := &config.Config{
		Geocode: config.GeocodeConfig{Provider: ""},
	}

	_, err := NewGeocodeService(db, cfg)
	require.Error(t, err)
}

func TestGeocodeService_Reload(t *testing.T) {
	db := setupGeocodeServiceTestDB(t)
	db.Create(&model.City{GeonameID: 1, Name: "Tokyo", AdminName: "13", Country: "JP", Latitude: 35.6762, Longitude: 139.6503})

	cfg := &config.Config{
		Geocode: config.GeocodeConfig{
			Provider:           "offline",
			OfflineMaxDistance: 100,
			CacheEnabled:       false,
		},
	}

	svc, err := NewGeocodeService(db, cfg)
	require.NoError(t, err)

	// Reload with same config should work
	err = svc.Reload(db, cfg)
	require.NoError(t, err)
}
