package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 4, cfg.Analyzer.Workers)
	assert.Equal(t, "ollama", cfg.AI.Provider)
	assert.NotEmpty(t, cfg.AI.Ollama.Model)
}

func TestConfig_Validate_MissingEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Endpoint = ""
	cfg.Server.APIKey = "key"
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint")
}

func TestConfig_Validate_MissingAPIKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Endpoint = "http://localhost:8080"
	cfg.Server.APIKey = ""
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "api_key")
}

func TestConfig_Validate_InvalidProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Endpoint = "http://localhost:8080"
	cfg.Server.APIKey = "key"
	cfg.AI.Provider = "invalid"
	err := cfg.Validate()
	require.Error(t, err)
}

func TestConfig_Validate_Success(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Endpoint = "http://localhost:8080"
	cfg.Server.APIKey = "key"
	cfg.AI.Provider = "ollama"
	err := cfg.Validate()
	require.NoError(t, err)
}

func TestConfig_Validate_ClampsWorkers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Server.Endpoint = "http://localhost:8080"
	cfg.Server.APIKey = "key"
	cfg.Analyzer.Workers = 0
	require.NoError(t, cfg.Validate())
	assert.Equal(t, 4, cfg.Analyzer.Workers, "workers=0 should be reset to default 4")
}

func TestConfig_Load_NonExistentFile(t *testing.T) {
	cfg, err := Load("/nonexistent/config.yaml")
	// Load falls back to defaults for non-existent file
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestConfig_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")

	cfg := DefaultConfig()
	cfg.Server.Endpoint = "http://test:8080"
	cfg.Server.APIKey = "test-key"
	require.NoError(t, cfg.Save(p))

	loaded, err := Load(p)
	require.NoError(t, err)
	assert.Equal(t, "http://test:8080", loaded.Server.Endpoint)
	assert.Equal(t, "test-key", loaded.Server.APIKey)
}

func TestGenerateSampleConfig(t *testing.T) {
	sample := GenerateSampleConfig()
	assert.NotEmpty(t, sample)
	assert.Contains(t, sample, "endpoint")
}

func TestConfig_Load_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(p, []byte("{{{{invalid yaml"), 0644))

	_, err := Load(p)
	require.Error(t, err)
}

func TestConfig_Validate_CircuitDefaultsAndBounds(t *testing.T) {
	// 默认值合法。
	cfg := DefaultConfig()
	cfg.Server.Endpoint = "http://localhost:8080"
	cfg.Server.APIKey = "key"
	require.NoError(t, cfg.Validate())
	assert.Equal(t, 10, cfg.Analyzer.MaxAttempts)
	assert.Equal(t, 3, cfg.Analyzer.CircuitFailureThreshold)
	assert.Equal(t, 30, cfg.Analyzer.CircuitInitialBackoff)
	assert.Equal(t, 600, cfg.Analyzer.CircuitMaxBackoff)

	// initial > max 非法。
	cfg2 := DefaultConfig()
	cfg2.Server.Endpoint = "http://localhost:8080"
	cfg2.Server.APIKey = "key"
	cfg2.Analyzer.CircuitInitialBackoff = 700
	cfg2.Analyzer.CircuitMaxBackoff = 600
	err := cfg2.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circuit_initial_backoff")

	// 负值非法。
	cfg3 := DefaultConfig()
	cfg3.Server.Endpoint = "http://localhost:8080"
	cfg3.Server.APIKey = "key"
	cfg3.Analyzer.CircuitFailureThreshold = -1
	err = cfg3.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circuit_failure_threshold")
}

func TestGenerateSampleConfig_ContainsCircuitKeys(t *testing.T) {
	sample := GenerateSampleConfig()
	assert.Contains(t, sample, "max_attempts")
	assert.Contains(t, sample, "circuit_failure_threshold")
	assert.Contains(t, sample, "circuit_initial_backoff")
	assert.Contains(t, sample, "circuit_max_backoff")
}
