package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPhoto_CalculateOverallScore(t *testing.T) {
	tests := []struct {
		name     string
		memory   int
		beauty   int
		expected int
	}{
		{"both zero", 0, 0, 0},
		{"memory only", 100, 0, 70},
		{"beauty only", 0, 100, 30},
		{"both max", 100, 100, 100},
		{"typical", 80, 60, 74},     // int(80*0.7 + 60*0.3) = int(56+18) = 74
		{"low scores", 10, 20, 13},  // int(7+6) = 13
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Photo{MemoryScore: tt.memory, BeautyScore: tt.beauty}
			p.CalculateOverallScore()
			assert.Equal(t, tt.expected, p.OverallScore)
		})
	}
}

func TestPhoto_IsAnalyzed(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name       string
		aiAnalyzed bool
		analyzedAt *time.Time
		expected   bool
	}{
		{"both set", true, &now, true},
		{"flag false", false, &now, false},
		{"time nil", true, nil, false},
		{"both unset", false, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Photo{AIAnalyzed: tt.aiAnalyzed, AnalyzedAt: tt.analyzedAt}
			assert.Equal(t, tt.expected, p.IsAnalyzed())
		})
	}
}

func TestPhoto_HasGPS(t *testing.T) {
	lat := 39.9
	lon := 116.4

	tests := []struct {
		name string
		lat  *float64
		lon  *float64
		want bool
	}{
		{"both set", &lat, &lon, true},
		{"lat nil", nil, &lon, false},
		{"lon nil", &lat, nil, false},
		{"both nil", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Photo{GPSLatitude: tt.lat, GPSLongitude: tt.lon}
			assert.Equal(t, tt.want, p.HasGPS())
		})
	}
}

func TestPhoto_TableName(t *testing.T) {
	assert.Equal(t, "photos", Photo{}.TableName())
}
