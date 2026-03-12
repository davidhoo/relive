package geocode

import (
	"errors"
	"testing"

	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	_ = logger.Init(config.LoggingConfig{Level: "error", Console: true})
}

// stubProvider implements Provider for testing.
type stubProvider struct {
	name      string
	available bool
	priority  int
	fn        func(lat, lon float64) (*Location, error)
}

func (s *stubProvider) Name() string                                 { return s.name }
func (s *stubProvider) IsAvailable() bool                            { return s.available }
func (s *stubProvider) Priority() int                                { return s.priority }
func (s *stubProvider) ReverseGeocode(lat, lon float64) (*Location, error) {
	if s.fn != nil {
		return s.fn(lat, lon)
	}
	return nil, errors.New("not implemented")
}

// ===== Service.ReverseGeocode =====

func TestService_ReverseGeocode_FirstProviderSuccess(t *testing.T) {
	p := &stubProvider{
		name: "stub1", available: true,
		fn: func(lat, lon float64) (*Location, error) {
			return &Location{City: "Beijing", Country: "中国", Provider: "stub1"}, nil
		},
	}
	svc := NewService(&Config{CacheEnabled: false}, p)

	loc, err := svc.ReverseGeocode(39.9, 116.4)

	require.NoError(t, err)
	assert.Equal(t, "Beijing", loc.City)
	assert.Equal(t, "stub1", loc.Provider)
}

func TestService_ReverseGeocode_Fallback(t *testing.T) {
	p1 := &stubProvider{
		name: "failing", available: true,
		fn: func(lat, lon float64) (*Location, error) {
			return nil, errors.New("timeout")
		},
	}
	p2 := &stubProvider{
		name: "backup", available: true,
		fn: func(lat, lon float64) (*Location, error) {
			return &Location{City: "Tokyo", Country: "日本", Provider: "backup"}, nil
		},
	}
	svc := NewService(&Config{CacheEnabled: false}, p1, p2)

	loc, err := svc.ReverseGeocode(35.6, 139.7)

	require.NoError(t, err)
	assert.Equal(t, "Tokyo", loc.City)
	assert.Equal(t, "backup", loc.Provider)
}

func TestService_ReverseGeocode_AllFail(t *testing.T) {
	p := &stubProvider{
		name: "failing", available: true,
		fn: func(lat, lon float64) (*Location, error) {
			return nil, errors.New("fail")
		},
	}
	svc := NewService(&Config{CacheEnabled: false}, p)

	_, err := svc.ReverseGeocode(0, 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "all providers failed")
}

func TestService_ReverseGeocode_SkipsUnavailable(t *testing.T) {
	unavailable := &stubProvider{
		name: "down", available: false,
		fn: func(lat, lon float64) (*Location, error) {
			t.Fatal("should not be called")
			return nil, nil
		},
	}
	working := &stubProvider{
		name: "up", available: true,
		fn: func(lat, lon float64) (*Location, error) {
			return &Location{City: "Seoul", Provider: "up"}, nil
		},
	}
	svc := NewService(&Config{CacheEnabled: false}, unavailable, working)

	loc, err := svc.ReverseGeocode(37.5, 126.9)

	require.NoError(t, err)
	assert.Equal(t, "Seoul", loc.City)
}

func TestService_ReverseGeocode_NoProviders(t *testing.T) {
	svc := NewService(&Config{CacheEnabled: false})

	_, err := svc.ReverseGeocode(0, 0)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no available")
}

func TestService_ReverseGeocode_CacheHit(t *testing.T) {
	callCount := 0
	p := &stubProvider{
		name: "counter", available: true,
		fn: func(lat, lon float64) (*Location, error) {
			callCount++
			return &Location{City: "Cached", Provider: "counter"}, nil
		},
	}
	svc := NewService(&Config{CacheEnabled: true, CacheTTL: 3600}, p)

	loc1, err := svc.ReverseGeocode(39.9042, 116.4074)
	require.NoError(t, err)
	assert.Equal(t, "Cached", loc1.City)
	assert.Equal(t, 1, callCount)

	// Second call should hit cache
	loc2, err := svc.ReverseGeocode(39.9042, 116.4074)
	require.NoError(t, err)
	assert.Equal(t, "Cached", loc2.City)
	assert.Equal(t, 1, callCount) // Not incremented
}

func TestService_ReverseGeocode_CacheDisabled(t *testing.T) {
	callCount := 0
	p := &stubProvider{
		name: "counter", available: true,
		fn: func(lat, lon float64) (*Location, error) {
			callCount++
			return &Location{City: "NoCache", Provider: "counter"}, nil
		},
	}
	svc := NewService(&Config{CacheEnabled: false}, p)

	svc.ReverseGeocode(39.9, 116.4)
	svc.ReverseGeocode(39.9, 116.4)

	assert.Equal(t, 2, callCount)
}

func TestService_GetAvailableProviders(t *testing.T) {
	svc := NewService(&Config{},
		&stubProvider{name: "a", available: true},
		&stubProvider{name: "b", available: false},
		&stubProvider{name: "c", available: true},
	)

	names := svc.GetAvailableProviders()

	assert.Equal(t, []string{"a", "c"}, names)
}

// ===== Location format methods =====

func TestLocation_FormatShort(t *testing.T) {
	tests := []struct {
		name     string
		loc      Location
		expected string
	}{
		{"city+district", Location{City: "北京市", District: "朝阳区"}, "北京市朝阳区"},
		{"city_only", Location{City: "上海市"}, "上海市"},
		{"province_only", Location{Province: "四川省"}, "四川省"},
		{"country_only", Location{Country: "日本"}, "日本"},
		{"empty", Location{}, ""},
		{"city_brackets_fallback", Location{City: "[]", Province: "浙江省"}, "浙江省"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.loc.FormatShort())
		})
	}
}

func TestLocation_FormatFull(t *testing.T) {
	tests := []struct {
		name     string
		loc      Location
		expected string
	}{
		{
			"full_name_override",
			Location{FullName: "Custom Address"},
			"Custom Address",
		},
		{
			"all_parts",
			Location{Country: "中国", Province: "北京市", City: "北京市", District: "海淀区"},
			"中国，北京市，北京市，海淀区",
		},
		{
			"country_province",
			Location{Country: "中国", Province: "四川省"},
			"中国，四川省",
		},
		{
			"empty",
			Location{},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.loc.FormatFull())
		})
	}
}

func TestLocation_FormatDisplay(t *testing.T) {
	tests := []struct {
		name     string
		loc      Location
		expected string
	}{
		{
			"offline_provider_uses_fullname",
			Location{FullName: "四川省Chengdu", Provider: "offline", City: "Chengdu", Province: "四川省"},
			"四川省Chengdu",
		},
		{
			"city_district_poi_street",
			Location{City: "北京市", District: "朝阳区", POI: "东大桥", Street: "三里屯街区"},
			"北京市朝阳区东大桥（三里屯街区）",
		},
		{
			"city_only",
			Location{City: "上海市"},
			"上海市",
		},
		{
			"province_with_district",
			Location{Province: "四川省", District: "武侯区"},
			"四川省武侯区",
		},
		{
			"district_only",
			Location{District: "海淀区"},
			"海淀区",
		},
		{
			"empty_falls_to_short",
			Location{Country: "日本"},
			"日本",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.loc.FormatDisplay())
		})
	}
}
