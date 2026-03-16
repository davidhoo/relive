package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

type autoScanConfig struct {
	Enabled         bool `json:"enabled"`
	IntervalMinutes int  `json:"interval_minutes"`
}

type scanPathConfig struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Path            string     `json:"path"`
	IsDefault       bool       `json:"is_default"`
	Enabled         bool       `json:"enabled"`
	AutoScanEnabled *bool      `json:"auto_scan_enabled,omitempty"`
	CreatedAt       *time.Time `json:"created_at,omitempty"`
	LastScannedAt   *time.Time `json:"last_scanned_at,omitempty"`
}

type scanPathsConfig struct {
	Paths []scanPathConfig `json:"paths"`
}

type scanTreeNode struct {
	Path    string `json:"path"`
	ModTime int64  `json:"mod_time"`
}

type scanTreeSnapshot struct {
	RootPath    string         `json:"root_path"`
	GeneratedAt time.Time      `json:"generated_at"`
	Nodes       []scanTreeNode `json:"nodes"`
}

func defaultAutoScanConfig() autoScanConfig {
	return autoScanConfig{Enabled: false, IntervalMinutes: 60}
}

func (s *photoService) loadAutoScanConfig() (autoScanConfig, error) {
	if s.configService == nil {
		return defaultAutoScanConfig(), nil
	}
	value, err := s.configService.GetWithDefault("photos.auto_scan", "")
	if err != nil || value == "" {
		return defaultAutoScanConfig(), nil
	}
	cfg := defaultAutoScanConfig()
	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		return defaultAutoScanConfig(), err
	}
	if cfg.IntervalMinutes <= 0 {
		cfg.IntervalMinutes = 60
	}
	return cfg, nil
}

func (s *photoService) loadScanPathsConfig() (scanPathsConfig, error) {
	var cfg scanPathsConfig
	if s.configService == nil {
		return cfg, nil
	}
	value, err := s.configService.GetWithDefault("photos.scan_paths", "")
	if err != nil || value == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		return scanPathsConfig{}, err
	}
	for i := range cfg.Paths {
		if cfg.Paths[i].AutoScanEnabled == nil {
			enabled := true
			cfg.Paths[i].AutoScanEnabled = &enabled
		}
	}
	return cfg, nil
}

func (s *photoService) saveScanPathsConfig(cfg scanPathsConfig) error {
	if s.configService == nil {
		return nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.configService.Set("photos.scan_paths", string(data))
}

func (s *photoService) scanTreeConfigKey(pathID string) string {
	return "photos.scan_tree." + pathID
}

func (s *photoService) loadScanTreeSnapshot(pathID string) (*scanTreeSnapshot, error) {
	if s.configService == nil {
		return nil, nil
	}
	value, err := s.configService.GetWithDefault(s.scanTreeConfigKey(pathID), "")
	if err != nil || value == "" {
		return nil, nil
	}
	var snapshot scanTreeSnapshot
	if err := json.Unmarshal([]byte(value), &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *photoService) saveScanTreeSnapshot(pathID string, snapshot *scanTreeSnapshot) error {
	if s.configService == nil || snapshot == nil {
		return nil
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return s.configService.Set(s.scanTreeConfigKey(pathID), string(data))
}

func (s *photoService) buildScanTreeSnapshot(rootPath string) (*scanTreeSnapshot, error) {
	nodes := make([]scanTreeNode, 0, 32)
	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if path != rootPath && s.shouldExcludeDir(info.Name()) {
			return filepath.SkipDir
		}
		nodes = append(nodes, scanTreeNode{Path: path, ModTime: info.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &scanTreeSnapshot{RootPath: rootPath, GeneratedAt: time.Now(), Nodes: nodes}, nil
}

func (s *photoService) scanTreeChangedDirs(snapshot *scanTreeSnapshot) ([]string, error) {
	if snapshot == nil {
		return nil, nil
	}

	changedDirs := make([]string, 0)
	for _, node := range snapshot.Nodes {
		info, err := os.Stat(node.Path)
		if os.IsNotExist(err) {
			changedDirs = append(changedDirs, nearestExistingAncestor(node.Path, snapshot.RootPath))
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		if info.ModTime().UnixNano() != node.ModTime {
			changedDirs = append(changedDirs, node.Path)
		}
	}

	return compressChangedDirs(changedDirs, snapshot.RootPath), nil
}

func nearestExistingAncestor(path string, rootPath string) string {
	current := path
	for {
		if _, err := os.Stat(current); err == nil {
			return current
		}
		if current == rootPath {
			return rootPath
		}
		parent := filepath.Dir(current)
		if parent == current {
			return rootPath
		}
		current = parent
	}
}

func compressChangedDirs(changedDirs []string, rootPath string) []string {
	if len(changedDirs) == 0 {
		return nil
	}

	unique := make(map[string]struct{}, len(changedDirs))
	for _, dir := range changedDirs {
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		unique[clean] = struct{}{}
	}

	dirs := make([]string, 0, len(unique))
	for dir := range unique {
		dirs = append(dirs, dir)
	}
	sort.Slice(dirs, func(i, j int) bool {
		return len(dirs[i]) < len(dirs[j])
	})

	result := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		covered := false
		for _, existing := range result {
			if dir == existing || strings.HasPrefix(dir, existing+string(os.PathSeparator)) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, dir)
		}
	}

	if len(result) == 0 {
		return []string{rootPath}
	}
	return result
}

func (s *photoService) shouldRunAutoScan(intervalMinutes int) bool {
	s.autoScanMutex.Lock()
	defer s.autoScanMutex.Unlock()
	if intervalMinutes <= 0 {
		intervalMinutes = 60
	}
	now := time.Now()
	if s.lastAutoScanCheck.IsZero() || now.Sub(s.lastAutoScanCheck) >= time.Duration(intervalMinutes)*time.Minute {
		s.lastAutoScanCheck = now
		return true
	}
	return false
}

func (s *photoService) RunAutoScanCheck() error {
	cfg, err := s.loadAutoScanConfig()
	if err != nil {
		return err
	}
	if !cfg.Enabled || !s.shouldRunAutoScan(cfg.IntervalMinutes) {
		return nil
	}

	task := s.GetScanTask()
	if task != nil && task.IsRunning() {
		logger.Infof("Skipping auto scan check because a scan task is already running")
		return nil
	}

	pathsCfg, err := s.loadScanPathsConfig()
	if err != nil {
		return err
	}

	for _, path := range pathsCfg.Paths {
		if !path.Enabled || path.AutoScanEnabled == nil || !*path.AutoScanEnabled || path.LastScannedAt == nil {
			continue
		}
		if _, err := os.Stat(path.Path); os.IsNotExist(err) {
			logger.Warnf("Auto scan skipped for missing path: %s", path.Path)
			continue
		}

		snapshot, err := s.loadScanTreeSnapshot(path.ID)
		if err != nil {
			logger.Warnf("Load scan tree snapshot failed for %s: %v", path.Path, err)
			continue
		}
		if snapshot == nil {
			snapshot, err = s.buildScanTreeSnapshot(path.Path)
			if err != nil {
				logger.Warnf("Build initial scan tree snapshot failed for %s: %v", path.Path, err)
				continue
			}
			if err := s.saveScanTreeSnapshot(path.ID, snapshot); err != nil {
				logger.Warnf("Save initial scan tree snapshot failed for %s: %v", path.Path, err)
			}
			continue
		}

		changedDirs, err := s.scanTreeChangedDirs(snapshot)
		if err != nil {
			logger.Warnf("Check scan tree changes failed for %s: %v", path.Path, err)
			continue
		}
		if len(changedDirs) == 0 {
			continue
		}

		scanRoot := path.Path
		if len(changedDirs) == 1 {
			scanRoot = changedDirs[0]
			logger.Infof("Auto scan detected single changed subtree for %s: %s", path.Path, scanRoot)
		} else {
			logger.Infof("Auto scan detected multiple changed subtrees for %s, falling back to full path scan: %v", path.Path, changedDirs)
		}

		if _, err := s.StartScan(scanRoot); err != nil {
			logger.Warnf("Auto scan start failed for %s: %v", scanRoot, err)
		}
		return nil
	}

	return nil
}

// updateScanPathTimestamp 更新扫描路径的 last_scanned_at 时间戳
func (s *photoService) updateScanPathTimestamp(scanPath string) error {
	// 获取当前扫描路径配置
	configValue, err := s.configService.GetWithDefault("photos.scan_paths", "")
	if err != nil {
		return fmt.Errorf("get scan paths config: %w", err)
	}

	if configValue == "" {
		// 没有配置扫描路径，直接返回
		return nil
	}

	var pathsConfig scanPathsConfig

	if err := json.Unmarshal([]byte(configValue), &pathsConfig); err != nil {
		return fmt.Errorf("parse scan paths config: %w", err)
	}

	// 找到匹配的扫描路径并更新时间戳
	now := time.Now()
	updated := false
	for i := range pathsConfig.Paths {
		if pathsConfig.Paths[i].Path == scanPath {
			pathsConfig.Paths[i].LastScannedAt = &now
			updated = true
			break
		}
	}

	if !updated {
		// 没有找到匹配的路径，可能是通过直接路径扫描而非配置的路径
		return nil
	}

	// 保存更新后的配置
	newConfigValue, err := json.Marshal(pathsConfig)
	if err != nil {
		return fmt.Errorf("marshal scan paths config: %w", err)
	}

	if err := s.configService.Set("photos.scan_paths", string(newConfigValue)); err != nil {
		return fmt.Errorf("save scan paths config: %w", err)
	}

	logger.Infof("Updated last_scanned_at for scan path: %s", scanPath)
	return nil
}

func (s *photoService) updateScanTreeSnapshotWithSnapshot(scanPath string, snapshot *scanTreeSnapshot) error {
	pathsCfg, err := s.loadScanPathsConfig()
	if err != nil {
		return fmt.Errorf("load scan paths config: %w", err)
	}
	for _, path := range pathsCfg.Paths {
		if path.Path != scanPath {
			continue
		}
		if snapshot == nil {
			snapshot, err = s.buildScanTreeSnapshot(scanPath)
			if err != nil {
				return err
			}
		}
		return s.saveScanTreeSnapshot(path.ID, snapshot)
	}
	return nil
}
