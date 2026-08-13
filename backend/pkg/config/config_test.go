package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestLoadDefaultsPeopleMergeSuggestionConfig(t *testing.T) {
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
people:
  ml_endpoint: "http://localhost:5050"
  timeout: 15
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.People.MergeSuggestionThreshold != 0.55 {
		t.Fatalf("expected default merge_suggestion_threshold 0.55, got %v", cfg.People.MergeSuggestionThreshold)
	}
	if cfg.People.MergeSuggestionMaxPairsPerRun != 200 {
		t.Fatalf("expected default merge_suggestion_max_pairs_per_run 200, got %d", cfg.People.MergeSuggestionMaxPairsPerRun)
	}
	if cfg.People.MergeSuggestionBatchSize != 100 {
		t.Fatalf("expected default merge_suggestion_batch_size 100, got %d", cfg.People.MergeSuggestionBatchSize)
	}
	if cfg.People.MergeSuggestionCooldownSeconds != 300 {
		t.Fatalf("expected default merge_suggestion_cooldown_seconds 300, got %d", cfg.People.MergeSuggestionCooldownSeconds)
	}
}

func TestLoadRejectsInvalidPeopleMergeSuggestionConfig(t *testing.T) {
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
people:
  merge_suggestion_batch_size: -1
`)

	if _, err := Load(configPath); err == nil {
		t.Fatal("expected Load to reject invalid people.merge_suggestion_batch_size")
	}
}

func TestPeopleIdentityProfileDefaults(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	writeTestFile(t, configPath, `server:
  port: 8080
database:
  type: "sqlite"
photos:
  root_path: "/tmp/photos"
security:
  jwt_Secret: "base-secret"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	assertPeopleIdentityProfileDefaults(t, cfg.People)
}

func assertPeopleIdentityProfileDefaults(t *testing.T, got PeopleConfig) {
	t.Helper()

	if got.IdentityProfileMode != "legacy" {
		t.Errorf("identity_profile_mode = %q, want legacy", got.IdentityProfileMode)
	}
	if got.IdentityProfileMaxCenters != 6 {
		t.Errorf("identity_profile_max_centers = %d, want 6", got.IdentityProfileMaxCenters)
	}
	if got.IdentityProfileMinCenterFaces != 3 {
		t.Errorf("identity_profile_min_center_faces = %d, want 3", got.IdentityProfileMinCenterFaces)
	}
	if got.IdentityProfileMinCenterPhotos != 2 {
		t.Errorf("identity_profile_min_center_photos = %d, want 2", got.IdentityProfileMinCenterPhotos)
	}
	if got.IdentityProfileMargin != 0.05 {
		t.Errorf("identity_profile_margin = %v, want 0.05", got.IdentityProfileMargin)
	}
	if got.IdentityProfileRescueThreshold != 0.65 {
		t.Errorf("identity_profile_rescue_threshold = %v, want 0.65", got.IdentityProfileRescueThreshold)
	}
	if got.IdentityProfileBatchSize != 25 {
		t.Errorf("identity_profile_batch_size = %d, want 25", got.IdentityProfileBatchSize)
	}
	if got.IdentityProfileCooldownMs != 500 {
		t.Errorf("identity_profile_cooldown_ms = %d, want 500", got.IdentityProfileCooldownMs)
	}
	if got.IdentityProfileBuildWorkers != 2 {
		t.Errorf("identity_profile_build_workers = %d, want 2", got.IdentityProfileBuildWorkers)
	}
	if got.IdentityProfileDirtyBatchSize != 10 {
		t.Errorf("identity_profile_dirty_batch_size = %d, want 10", got.IdentityProfileDirtyBatchSize)
	}
	if got.IdentityProfileSliceBudgetMs != 5000 {
		t.Errorf("identity_profile_slice_budget_ms = %d, want 5000", got.IdentityProfileSliceBudgetMs)
	}
	if got.IdentityProfileAnnRebuildDeltaThreshold != 0.75 {
		t.Errorf("identity_profile_ann_rebuild_delta_threshold = %v, want 0.75", got.IdentityProfileAnnRebuildDeltaThreshold)
	}
}

func TestPeopleIdentityProfileExampleConfigs(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate config_test.go")
	}
	backendDir := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", ".."))

	for _, name := range []string{"config.dev.yaml.example", "config.prod.yaml.example"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(filepath.Join(backendDir, name))
			if err != nil {
				t.Fatalf("Load(%s) returned error: %v", name, err)
			}
			assertPeopleIdentityProfileDefaults(t, cfg.People)
		})
	}
}

func TestPeopleIdentityProfileRejectsInvalidConfig(t *testing.T) {
	valid := PeopleConfig{
		IdentityProfileMode:            "legacy",
		IdentityProfileMaxCenters:      6,
		IdentityProfileMinCenterFaces:  3,
		IdentityProfileMinCenterPhotos: 2,
		IdentityProfileMargin:          0.05,
		IdentityProfileRescueThreshold: 0.65,
		IdentityProfileBatchSize:       25,
		IdentityProfileCooldownMs:      500,
		IdentityProfileBuildWorkers:    2,
		IdentityProfileDirtyBatchSize:  10,
		IdentityProfileSliceBudgetMs:   5000,
		IdentityProfileAnnRebuildDeltaThreshold: 0.75,
		MergeSuggestionThreshold:       0.55,
		MergeSuggestionMaxPairsPerRun:  200,
		MergeSuggestionBatchSize:       100,
		MergeSuggestionCooldownSeconds: 300,
		ClusteringIntervalMs:           300,
		ANNBuildBatchSize:              100,
		ANNBuildCPUDuty:                0.5,
	}

	tests := []struct {
		name  string
		field string
		apply func(*PeopleConfig)
	}{
		{name: "unknown mode", field: "identity_profile_mode", apply: func(c *PeopleConfig) { c.IdentityProfileMode = "unknown" }},
		{name: "zero max centers", field: "identity_profile_max_centers", apply: func(c *PeopleConfig) { c.IdentityProfileMaxCenters = 0 }},
		{name: "negative max centers", field: "identity_profile_max_centers", apply: func(c *PeopleConfig) { c.IdentityProfileMaxCenters = -1 }},
		{name: "too many centers", field: "identity_profile_max_centers", apply: func(c *PeopleConfig) { c.IdentityProfileMaxCenters = 9 }},
		{name: "zero minimum center faces", field: "identity_profile_min_center_faces", apply: func(c *PeopleConfig) { c.IdentityProfileMinCenterFaces = 0 }},
		{name: "negative minimum center faces", field: "identity_profile_min_center_faces", apply: func(c *PeopleConfig) { c.IdentityProfileMinCenterFaces = -1 }},
		{name: "zero minimum center photos", field: "identity_profile_min_center_photos", apply: func(c *PeopleConfig) { c.IdentityProfileMinCenterPhotos = 0 }},
		{name: "negative minimum center photos", field: "identity_profile_min_center_photos", apply: func(c *PeopleConfig) { c.IdentityProfileMinCenterPhotos = -1 }},
		{name: "zero margin", field: "identity_profile_margin", apply: func(c *PeopleConfig) { c.IdentityProfileMargin = 0 }},
		{name: "negative margin", field: "identity_profile_margin", apply: func(c *PeopleConfig) { c.IdentityProfileMargin = -0.1 }},
		{name: "margin at upper bound", field: "identity_profile_margin", apply: func(c *PeopleConfig) { c.IdentityProfileMargin = 1 }},
		{name: "margin above upper bound", field: "identity_profile_margin", apply: func(c *PeopleConfig) { c.IdentityProfileMargin = 1.1 }},
		{name: "zero rescue threshold", field: "identity_profile_rescue_threshold", apply: func(c *PeopleConfig) { c.IdentityProfileRescueThreshold = 0 }},
		{name: "negative rescue threshold", field: "identity_profile_rescue_threshold", apply: func(c *PeopleConfig) { c.IdentityProfileRescueThreshold = -0.1 }},
		{name: "rescue threshold at upper bound", field: "identity_profile_rescue_threshold", apply: func(c *PeopleConfig) { c.IdentityProfileRescueThreshold = 1 }},
		{name: "rescue threshold above upper bound", field: "identity_profile_rescue_threshold", apply: func(c *PeopleConfig) { c.IdentityProfileRescueThreshold = 1.1 }},
		{name: "zero batch size", field: "identity_profile_batch_size", apply: func(c *PeopleConfig) { c.IdentityProfileBatchSize = 0 }},
		{name: "negative batch size", field: "identity_profile_batch_size", apply: func(c *PeopleConfig) { c.IdentityProfileBatchSize = -1 }},
		{name: "zero cooldown", field: "identity_profile_cooldown_ms", apply: func(c *PeopleConfig) { c.IdentityProfileCooldownMs = 0 }},
		{name: "negative cooldown", field: "identity_profile_cooldown_ms", apply: func(c *PeopleConfig) { c.IdentityProfileCooldownMs = -1 }},
		{name: "zero build workers", field: "identity_profile_build_workers", apply: func(c *PeopleConfig) { c.IdentityProfileBuildWorkers = 0 }},
		{name: "too many build workers", field: "identity_profile_build_workers", apply: func(c *PeopleConfig) { c.IdentityProfileBuildWorkers = 5 }},
		{name: "zero dirty batch size", field: "identity_profile_dirty_batch_size", apply: func(c *PeopleConfig) { c.IdentityProfileDirtyBatchSize = 0 }},
		{name: "zero slice budget", field: "identity_profile_slice_budget_ms", apply: func(c *PeopleConfig) { c.IdentityProfileSliceBudgetMs = 0 }},
		{name: "zero ann delta threshold", field: "identity_profile_ann_rebuild_delta_threshold", apply: func(c *PeopleConfig) { c.IdentityProfileAnnRebuildDeltaThreshold = 0 }},
		{name: "ann delta threshold above one", field: "identity_profile_ann_rebuild_delta_threshold", apply: func(c *PeopleConfig) { c.IdentityProfileAnnRebuildDeltaThreshold = 1.1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			people := valid
			tt.apply(&people)
			cfg := Config{
				Server:   ServerConfig{Port: 8080},
				Database: DatabaseConfig{Type: "sqlite"},
				Photos:   PhotosConfig{RootPath: "/tmp/photos"},
				Security: SecurityConfig{JWTSecret: "base-secret"},
				People:   people,
			}

			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate accepted invalid %s", tt.field)
			}
			if !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("Validate error %q does not contain YAML field %q", err, tt.field)
			}
		})
	}
}

func TestPeopleIdentityProfileLoadRejectsExplicitInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "zero max centers", field: "identity_profile_max_centers", value: "0"},
		{name: "zero minimum center faces", field: "identity_profile_min_center_faces", value: "0"},
		{name: "zero minimum center photos", field: "identity_profile_min_center_photos", value: "0"},
		{name: "zero margin", field: "identity_profile_margin", value: "0"},
		{name: "NaN margin", field: "identity_profile_margin", value: ".nan"},
		{name: "zero rescue threshold", field: "identity_profile_rescue_threshold", value: "0"},
		{name: "NaN rescue threshold", field: "identity_profile_rescue_threshold", value: ".nan"},
		{name: "zero batch size", field: "identity_profile_batch_size", value: "0"},
		{name: "zero cooldown", field: "identity_profile_cooldown_ms", value: "0"},
		{name: "zero build workers", field: "identity_profile_build_workers", value: "0"},
		{name: "zero dirty batch size", field: "identity_profile_dirty_batch_size", value: "0"},
		{name: "zero slice budget", field: "identity_profile_slice_budget_ms", value: "0"},
		{name: "zero ann delta threshold", field: "identity_profile_ann_rebuild_delta_threshold", value: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.yaml")
			writeTestFile(t, configPath, identityProfileTestYAML(tt.field+": "+tt.value))

			_, err := Load(configPath)
			if err == nil {
				t.Fatalf("Load accepted %s: %s", tt.field, tt.value)
			}
			if !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("Load error %q does not contain YAML field %q", err, tt.field)
			}
		})
	}
}

func TestPeopleIdentityProfileOverrideExplicitZeroIsNotDefaulted(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "config.base.yaml")
	overridePath := filepath.Join(dir, "config.prod.yaml")
	writeTestFile(t, basePath, identityProfileTestYAML("identity_profile_batch_size: 25"))
	writeTestFile(t, overridePath, "people:\n  identity_profile_batch_size: 0\n")

	_, err := Load(overridePath)
	if err == nil {
		t.Fatal("Load accepted override people.identity_profile_batch_size: 0")
	}
	if !strings.Contains(err.Error(), "identity_profile_batch_size") {
		t.Fatalf("Load error %q does not contain YAML field identity_profile_batch_size", err)
	}
}

func identityProfileTestYAML(peopleEntry string) string {
	return `server:
  port: 8080
database:
  type: "sqlite"
photos:
  root_path: "/tmp/photos"
security:
  jwt_Secret: "base-secret"
people:
  ` + peopleEntry + "\n"
}

// TestBackgroundDefaults 验证后台任务治理默认阈值：缺省时填默认 70/15/85/120。
func TestBackgroundDefaults(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "config.base.yaml")
	overridePath := filepath.Join(dir, "config.prod.yaml")
	writeTestFile(t, basePath, `server:
  port: 8080
database:
  type: "sqlite"
photos:
  root_path: "/tmp/photos"
security:
  jwt_Secret: "x"
`)
	writeTestFile(t, overridePath, `logging:
  level: "info"
`)

	cfg, err := Load(overridePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Background.CPUPauseThreshold != 70 {
		t.Fatalf("cpu_pause_threshold default = %v, want 70", cfg.Background.CPUPauseThreshold)
	}
	if cfg.Background.IOWaitPauseThreshold != 15 {
		t.Fatalf("iowait_pause_threshold default = %v, want 15", cfg.Background.IOWaitPauseThreshold)
	}
	if cfg.Background.MemoryPauseThreshold != 85 {
		t.Fatalf("memory_pause_threshold default = %v, want 85", cfg.Background.MemoryPauseThreshold)
	}
	if cfg.Background.DBLockedCooldownSeconds != 120 {
		t.Fatalf("db_locked_cooldown_seconds default = %v, want 120", cfg.Background.DBLockedCooldownSeconds)
	}
}

// TestBackgroundOverride 验证显式配置覆盖默认值。
func TestBackgroundOverride(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "config.base.yaml")
	overridePath := filepath.Join(dir, "config.prod.yaml")
	writeTestFile(t, basePath, `server:
  port: 8080
database:
  type: "sqlite"
photos:
  root_path: "/tmp/photos"
security:
  jwt_Secret: "x"
`)
	writeTestFile(t, overridePath, `background:
  auto_tasks_enabled: false
  cpu_pause_threshold: 50
  iowait_pause_threshold: 10
  memory_pause_threshold: 80
  db_locked_cooldown_seconds: 60
`)

	cfg, err := Load(overridePath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Background.AutoTasksEnabled != false {
		t.Fatalf("auto_tasks_enabled = %v, want false", cfg.Background.AutoTasksEnabled)
	}
	if cfg.Background.CPUPauseThreshold != 50 {
		t.Fatalf("cpu_pause_threshold = %v, want 50", cfg.Background.CPUPauseThreshold)
	}
	if cfg.Background.DBLockedCooldownSeconds != 60 {
		t.Fatalf("db_locked_cooldown_seconds = %v, want 60", cfg.Background.DBLockedCooldownSeconds)
	}
}

func TestPeopleV2ThresholdsDefaultsAndValidation(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	writeTestFile(t, configPath, `server:
  port: 8080
database:
  type: "sqlite"
photos:
  root_path: "/tmp/photos"
security:
  jwt_Secret: "base-secret"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	// 默认值。
	if cfg.People.FaceDetectionMinConfidence != 0.65 {
		t.Errorf("face_detection_min_confidence default = %v, want 0.65", cfg.People.FaceDetectionMinConfidence)
	}
	if cfg.People.FaceQualityV2MinOriginalShortEdge != 48 {
		t.Errorf("face_quality_v2_min_original_short_edge default = %d, want 48", cfg.People.FaceQualityV2MinOriginalShortEdge)
	}

	// 越界拒绝。
	bad := filepath.Join(dir, "bad.yaml")
	writeTestFile(t, bad, `server:
  port: 8080
database:
  type: "sqlite"
photos:
  root_path: "/tmp/photos"
security:
  jwt_Secret: "base-secret"
people:
  face_detection_min_confidence: 1.5
`)
	if _, err := Load(bad); err == nil {
		t.Fatal("expected error for face_detection_min_confidence out of range")
	}
}
