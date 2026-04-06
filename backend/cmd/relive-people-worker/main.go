package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	workerConfig "github.com/davidhoo/relive/cmd/relive-people-worker/internal/config"
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
	case "run":
		runWorker()
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
	fmt.Fprintf(os.Stderr, `relive-people-worker v%s - Face Detection Worker for Mac M4 (API Mode)

Usage:
  relive-people-worker <command> [options]

Commands:
  run          Start people detection worker
  check        Check server and ML service connection
  version      Show version information
  gen-config   Generate sample configuration file

Run Options:
  -config string
        Configuration file path (default "people-worker.yaml")
  -workers int
        Number of concurrent workers (0 = use config value)
  -verbose
        Enable verbose logging

Check Options:
  -config string
        Configuration file path (default "people-worker.yaml")

Examples:
  # Generate sample config
  relive-people-worker gen-config > people-worker.yaml

  # Check connections
  relive-people-worker check -config people-worker.yaml

  # Run worker
  relive-people-worker run -config people-worker.yaml

  # Run with custom worker count
  relive-people-worker run -config people-worker.yaml -workers 8

Environment Variables:
  RELIVE_API_KEY    API Key for authentication

`, version.Version)
}

func runVersion() {
	fmt.Printf("relive-people-worker version %s\n", version.Version)
	fmt.Printf("Build time: %s\n", version.BuildTime)
	fmt.Printf("Git commit: %s\n", version.GitCommit)
	fmt.Println("\nAPI Mode - Compatible with Relive Server v1.3.0+")
	fmt.Println("Optimized for Apple Silicon (M4 Mac recommended)")
}

func runGenConfig() {
	fmt.Print(workerConfig.GenerateSampleConfig())
}

func runCheck() {
	checkCmd := flag.NewFlagSet("check", flag.ExitOnError)
	configPath := checkCmd.String("config", "people-worker.yaml", "Configuration file path")

	checkCmd.Parse(os.Args[2:])

	// Load configuration
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	initLogger(cfg)

	// Create worker
	worker, err := createWorker(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating worker: %v\n", err)
		os.Exit(1)
	}
	defer worker.Stop()

	// Run check
	if err := worker.Check(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runWorker() {
	runCmd := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := runCmd.String("config", "people-worker.yaml", "Configuration file path")
	workers := runCmd.Int("workers", 0, "Number of concurrent workers (0 = use config)")
	verbose := runCmd.Bool("verbose", false, "Enable verbose logging")

	runCmd.Parse(os.Args[2:])

	// Load configuration
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Override with command line flags
	if *workers > 0 {
		cfg.PeopleWorker.Workers = *workers
	}
	if *verbose {
		cfg.Logging.Level = "debug"
	}

	// Initialize logger
	initLogger(cfg)

	logger.Infof("Starting relive-people-worker v%s", version.Version)
	logger.Infof("Server: %s", cfg.Server.Endpoint)
	logger.Infof("ML Endpoint: %s", cfg.ML.Endpoint)
	logger.Infof("Worker ID: %s", getWorkerID(cfg))
	logger.Infof("Workers: %d", cfg.PeopleWorker.Workers)

	// Print API Key preview
	if cfg.Server.APIKey != "" {
		maskKey := cfg.Server.APIKey
		if len(maskKey) > 10 {
			maskKey = maskKey[:10] + "..."
		}
		logger.Infof("API Key: %s", maskKey)
	} else {
		logger.Warn("API Key is empty!")
	}

	// Create worker
	worker, err := createWorker(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating worker: %v\n", err)
		os.Exit(1)
	}

	// Run worker
	if err := worker.Run(); err != nil {
		logger.Errorf("Worker failed: %v", err)
		worker.Stop()
		os.Exit(1)
	}

	logger.Info("Worker completed successfully")
}

func loadConfig(configPath string) (*workerConfig.Config, error) {
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
	cfg, err := workerConfig.Load(absPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Validate config
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func initLogger(cfg *workerConfig.Config) {
	logCfg := pkgConfig.LoggingConfig{
		Level:   cfg.Logging.Level,
		Console: cfg.Logging.Console,
		File:    cfg.Logging.File,
	}

	if err := logger.Init(logCfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to initialize logger: %v\n", err)
	}
}

func getWorkerID(cfg *workerConfig.Config) string {
	if cfg.PeopleWorker.WorkerID != "" {
		return cfg.PeopleWorker.WorkerID
	}
	return "auto-generated"
}
