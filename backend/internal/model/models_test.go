package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDevice_IsOnline_NilLastSeen(t *testing.T) {
	d := &Device{LastSeen: nil}
	assert.False(t, d.IsOnline())
}

func TestDevice_IsOnline_Recent(t *testing.T) {
	now := time.Now()
	d := &Device{LastSeen: &now}
	assert.True(t, d.IsOnline())
}

func TestDevice_IsOnline_Stale(t *testing.T) {
	old := time.Now().Add(-10 * time.Minute)
	d := &Device{LastSeen: &old}
	assert.False(t, d.IsOnline())
}

func TestDevice_TableName(t *testing.T) {
	assert.Equal(t, "devices", Device{}.TableName())
}

func TestDisplayRecord_TableName(t *testing.T) {
	assert.Equal(t, "display_records", DisplayRecord{}.TableName())
}

func TestAppConfig_TableName(t *testing.T) {
	assert.Equal(t, "app_config", AppConfig{}.TableName())
}

func TestCity_TableName(t *testing.T) {
	assert.Equal(t, "cities", City{}.TableName())
}
