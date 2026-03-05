package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	analyzerConfig "github.com/davidhoo/relive/cmd/relive-analyzer/internal/config"
	pkgConfig "github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/davidhoo/relive/pkg/version"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "analyze":
		runAnalyze()
	case "check":
		runCheck()
	case "version":
		runVersion()
	case "gen-config":
		runGenConfig()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `relive-analyzer v%s - AI Photo Analysis Tool (API Mode)

Usage:
  relive-analyzer <command> [options]

Commands:
  analyze      Start analysis process
  check        Check server connection and task stats
  version      Show version information
  gen-config   Generate sample configuration file

Analyze Options:
  -config string
        Configuration file path (default "analyzer.yaml")
  -workers int
        Number of concurrent workers (0 = auto based on provider)
  -verbose
        Enable verbose logging

Check Options:
  -config string
        Configuration file path (default "analyzer.yaml")

Examples:
  # Generate sample config
  relive-analyzer gen-config > analyzer.yaml

  # Check server connection
  relive-analyzer check -config analyzer.yaml

  # Run analysis
  relive-analyzer analyze -config analyzer.yaml

  # Run with custom worker count
  relive-analyzer analyze -config analyzer.yaml -workers 8

Environment Variables:
  RELIVE_API_KEY    API Key for authentication

`, version.Version)
}

func runVersion() {
	fmt.Printf("relive-analyzer version %s\n", version.Version)
	fmt.Printf("Build time: %s\n", version.BuildTime)
	fmt.Printf("Git commit: %s\n", version.GitCommit)
	fmt.Println("\nAPI Mode - Compatible with Relive Server v1.5.0+")
}

func runGenConfig() {
	fmt.Print(analyzerConfig.GenerateSampleConfig())
}

func runCheck() {
	checkCmd := flag.NewFlagSet("check", flag.ExitOnError)
	configPath := checkCmd.String("config", "analyzer.yaml", "Configuration file path")

	checkCmd.Parse(os.Args[2:])

	// Load configuration
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	initLogger(cfg)

	// Create analyzer
	analyzer, err := createAnalyzer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating analyzer: %v\n", err)
		os.Exit(1)
	}
	defer analyzer.Stop()

	// Run check
	if err := analyzer.Check(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runAnalyze() {
	analyzeCmd := flag.NewFlagSet("analyze", flag.ExitOnError)
	configPath := analyzeCmd.String("config", "analyzer.yaml", "Configuration file path")
	workers := analyzeCmd.Int("workers", 0, "Number of concurrent workers (0 = auto)")
	verbose := analyzeCmd.Bool("verbose", false, "Enable verbose logging")

	analyzeCmd.Parse(os.Args[2:])

	// Load configuration
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Override with command line flags
	if *workers > 0 {
		cfg.Analyzer.Workers = *workers
	}
	if *verbose {
		cfg.Logging.Level = "debug"
	}

	// Initialize logger
	initLogger(cfg)

	logger.Infof("Starting relive-analyzer v%s", version.Version)
	logger.Infof("Server: %s", cfg.Server.Endpoint)
	logger.Infof("Analyzer ID: %s", getAnalyzerID(cfg))
	// 打印 API Key 前 10 位用于调试（不打印完整 Key）
	if cfg.Server.APIKey != "" {
		maskKey := cfg.Server.APIKey
		if len(maskKey) > 10 {
			maskKey = maskKey[:10] + "..."
		}
		logger.Infof("API Key: %s", maskKey)
	} else {
		logger.Warn("API Key is empty!")
	}

	// Create analyzer
	analyzer, err := createAnalyzer(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating analyzer: %v\n", err)
		os.Exit(1)
	}

	// Run analysis
	if err := analyzer.Run(); err != nil {
		logger.Errorf("Analysis failed: %v", err)
		analyzer.Stop()
		os.Exit(1)
	}

	logger.Info("Analysis completed successfully")
}

func loadConfig(configPath string) (*analyzerConfig.Config, error) {
	// Get absolute path
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("get absolute path: %w", err)
	}

	// Check if config file exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file not found: %s", configPath)
	}

	// Load config
	cfg, err := analyzerConfig.Load(absPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Validate config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func initLogger(cfg *analyzerConfig.Config) {
	logCfg := pkgConfig.LoggingConfig{
		Level:   cfg.Logging.Level,
		Console: cfg.Logging.Console,
		File:    cfg.Logging.File,
	}

	if err := logger.Init(logCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to initialize logger: %v\n", err)
	}
}

func getAnalyzerID(cfg *analyzerConfig.Config) string {
	if cfg.Analyzer.AnalyzerID != "" {
		return cfg.Analyzer.AnalyzerID
	}
	return "auto-generated"
}
