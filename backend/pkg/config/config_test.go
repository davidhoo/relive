package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func TestLoadMergesBaseConfigWithOverride(t *testing.T) {
	dir := t.TempDir()

	basePath := filepath.Join(dir, "config.base.yaml")
	overridePath := filepath.Join(dir, "config.prod.yaml")

	writeTestFile(t, basePath, `server:
  host: "0.0.0.0"
  port: 8080
  mode: "release"
  external_url: "https://example.com"
  static_path: "/app/frontend/dist"
database:
  type: "sqlite"
  path: "/app/data/relive.db"
  auto_migrate: true
photos:
  root_path: "/app/photos"
  thumbnail_path: "/app/data/thumbnails"
security:
  jwt_Secret: "base-secret"
  api_key_prefix: "sk-relive-"
performance:
  max_scan_workers: 10
  max_thumbnail_workers: 2
  max_geocode_workers: 1
`)

	writeTestFile(t, overridePath, `server:
  mode: "debug"
logging:
  level: "debug"
`)

	cfg, err := Load(overridePath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Fatalf("expected server.host from base config, got %q", cfg.Server.Host)
	}
	if cfg.Server.Mode != "debug" {
		t.Fatalf("expected override to win for server.mode, got %q", cfg.Server.Mode)
	}
	if cfg.Server.ExternalURL != "https://example.com" {
		t.Fatalf("expected external_url from base config, got %q", cfg.Server.ExternalURL)
	}
	if cfg.Photos.RootPath != "/app/photos" {
		t.Fatalf("expected photos.root_path from base config, got %q", cfg.Photos.RootPath)
	}
	if cfg.Security.JWTSecret != "base-secret" {
		t.Fatalf("expected jwt secret from base config, got %q", cfg.Security.JWTSecret)
	}
}

func TestLoadOverridesExternalURLFromEnv(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	writeTestFile(t, configPath, `server:
  host: "0.0.0.0"
  port: 8080
  mode: "release"
  external_url: "https://from-config.example.com"
database:
  type: "sqlite"
  path: "/app/data/relive.db"
  auto_migrate: true
photos:
  root_path: "/app/photos"
security:
  jwt_Secret: "base-secret"
`)

	t.Setenv("RELIVE_EXTERNAL_URL", "https://from-env.example.com")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Server.ExternalURL != "https://from-env.example.com" {
		t.Fatalf("expected RELIVE_EXTERNAL_URL to override config, got %q", cfg.Server.ExternalURL)
	}
}

func TestLoadLegacyMLConfigMapsToPeopleConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	writeTestFile(t, configPath, `server:
  host: "0.0.0.0"
  port: 8080
  mode: "debug"
database:
  type: "sqlite"
  path: "/tmp/relive.db"
  auto_migrate: true
photos:
  root_path: "/tmp/photos"
security:
  jwt_Secret: "base-secret"
ml:
  service_url: "http://localhost:5050"
  timeout: 30
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.People.MLEndpoint != "http://localhost:5050" {
		t.Fatalf("expected legacy ml.service_url to map to people.ml_endpoint, got %q", cfg.People.MLEndpoint)
	}
	if cfg.People.Timeout != 30 {
		t.Fatalf("expected legacy ml.timeout to map to people.timeout, got %d", cfg.People.Timeout)
	}
}
