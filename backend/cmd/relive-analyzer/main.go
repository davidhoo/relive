package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/davidhoo/relive/internal/analyzer"
	"github.com/davidhoo/relive/internal/provider"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

var (
	Version   = "1.0.0"
	BuildTime = "unknown"
)

func main() {
	// Parse command line
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
	case "estimate":
		runEstimate()
	case "version":
		runVersion()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `relive-analyzer - Offline Photo Analysis Tool

Usage:
  relive-analyzer <command> [options]

Commands:
  analyze   Analyze unprocessed photos in the database
  check     Check database status
  estimate  Estimate analysis cost and time
  version   Show version information

Analyze Options:
  -config string
        Configuration file path (default "analyzer.yaml")
  -db string
        Database file path (required)
  -workers int
        Number of concurrent workers (0 = auto based on provider)
  -retry int
        Number of retries on failure (default 3)
  -retry-delay int
        Delay between retries in seconds (default 5)
  -verbose
        Enable verbose logging

Check Options:
  -db string
        Database file path (required)

Estimate Options:
  -config string
        Configuration file path (default "analyzer.yaml")
  -db string
        Database file path (required)

Examples:
  # Check database status
  relive-analyzer check -db export.db

  # Estimate cost and time
  relive-analyzer estimate -config analyzer.yaml -db export.db

  # Run analysis
  relive-analyzer analyze -config analyzer.yaml -db export.db

  # Run with custom worker count
  relive-analyzer analyze -config analyzer.yaml -db export.db -workers 10

  # Run with verbose logging
  relive-analyzer analyze -config analyzer.yaml -db export.db -verbose

`)
}

func runVersion() {
	fmt.Printf("relive-analyzer version %s\n", Version)
	fmt.Printf("Build time: %s\n", BuildTime)
}

func runCheck() {
	// Parse flags
	checkCmd := flag.NewFlagSet("check", flag.ExitOnError)
	dbPath := checkCmd.String("db", "", "Database file path (required)")

	checkCmd.Parse(os.Args[2:])

	// Validate required flags
	if *dbPath == "" {
		fmt.Fprintf(os.Stderr, "Error: -db flag is required\n")
		checkCmd.Usage()
		os.Exit(1)
	}

	// Check if database exists
	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: Database file not found: %s\n", *dbPath)
		os.Exit(1)
	}

	// Create minimal analyzer (no provider needed for check)
	a, err := analyzer.NewAnalyzer(&analyzer.AnalyzerConfig{
		DBPath: *dbPath,
	}, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	// Run check
	if err := a.CheckStatus(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runEstimate() {
	// Parse flags
	estimateCmd := flag.NewFlagSet("estimate", flag.ExitOnError)
	configPath := estimateCmd.String("config", "analyzer.yaml", "Configuration file path")
	dbPath := estimateCmd.String("db", "", "Database file path (required)")

	estimateCmd.Parse(os.Args[2:])

	// Validate required flags
	if *dbPath == "" {
		fmt.Fprintf(os.Stderr, "Error: -db flag is required\n")
		estimateCmd.Usage()
		os.Exit(1)
	}

	// Check if database exists
	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: Database file not found: %s\n", *dbPath)
		os.Exit(1)
	}

	// Load configuration
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	initLogger(cfg)

	// Create AI provider
	aiProvider, err := createProvider(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating provider: %v\n", err)
		os.Exit(1)
	}

	// Create analyzer
	a, err := analyzer.NewAnalyzer(&analyzer.AnalyzerConfig{
		DBPath:  *dbPath,
		Workers: 0, // Use default
	}, aiProvider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	// Run estimation
	if err := a.EstimateCost(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runAnalyze() {
	// Parse flags
	analyzeCmd := flag.NewFlagSet("analyze", flag.ExitOnError)
	configPath := analyzeCmd.String("config", "analyzer.yaml", "Configuration file path")
	dbPath := analyzeCmd.String("db", "", "Database file path (required)")
	workers := analyzeCmd.Int("workers", 0, "Number of concurrent workers (0 = auto)")
	retryCount := analyzeCmd.Int("retry", 3, "Number of retries on failure")
	retryDelay := analyzeCmd.Int("retry-delay", 5, "Delay between retries in seconds")
	verbose := analyzeCmd.Bool("verbose", false, "Enable verbose logging")

	analyzeCmd.Parse(os.Args[2:])

	// Validate required flags
	if *dbPath == "" {
		fmt.Fprintf(os.Stderr, "Error: -db flag is required\n")
		analyzeCmd.Usage()
		os.Exit(1)
	}

	// Check if database exists
	if _, err := os.Stat(*dbPath); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: Database file not found: %s\n", *dbPath)
		os.Exit(1)
	}

	// Load configuration
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	initLogger(cfg)

	logger.Info(fmt.Sprintf("Starting relive-analyzer v%s", Version))
	logger.Info(fmt.Sprintf("Database: %s", *dbPath))
	logger.Info(fmt.Sprintf("Config: %s", *configPath))

	// Create AI provider
	aiProvider, err := createProvider(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating provider: %v\n", err)
		os.Exit(1)
	}

	logger.Info(fmt.Sprintf("Using provider: %s", aiProvider.Name()))

	// Check provider availability
	if !aiProvider.IsAvailable() {
		fmt.Fprintf(os.Stderr, "Error: Provider %s is not available\n", aiProvider.Name())
		fmt.Fprintf(os.Stderr, "Please check your configuration and ensure the service is running.\n")
		os.Exit(1)
	}

	logger.Info("Provider is available")

	// Create analyzer
	analyzerConfig := &analyzer.AnalyzerConfig{
		DBPath:     *dbPath,
		Workers:    *workers,
		RetryCount: *retryCount,
		RetryDelay: time.Duration(*retryDelay) * time.Second,
		Resume:     true,
		Verbose:    *verbose,
	}

	a, err := analyzer.NewAnalyzer(analyzerConfig, aiProvider)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating analyzer: %v\n", err)
		os.Exit(1)
	}
	defer a.Close()

	// Setup signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info(fmt.Sprintf("Received signal: %v", sig))
		logger.Info("Shutting down gracefully...")
		cancel()
	}()

	// Run analysis
	if err := a.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error during analysis: %v\n", err)
		os.Exit(1)
	}

	logger.Info("Analysis completed successfully")
}

// loadConfig loads configuration from file
func loadConfig(configPath string) (*config.Config, error) {
	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration file not found: %s", configPath)
	}

	// Get absolute path
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("get absolute path: %w", err)
	}

	// Load config
	cfg, err := config.Load(absPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	// Validate AI config
	if cfg.AI.Provider == "" {
		return nil, fmt.Errorf("AI provider not specified in config")
	}

	return cfg, nil
}

// initLogger initializes the logger
func initLogger(cfg *config.Config) {
	if err := logger.Init(cfg.Logging); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: Failed to initialize logger: %v\n", err)
	}
}

// createProvider creates AI provider based on configuration
func createProvider(cfg *config.Config) (provider.AIProvider, error) {
	switch cfg.AI.Provider {
	case "ollama":
		return provider.NewOllamaProvider(&provider.OllamaConfig{
			Endpoint:    cfg.AI.Ollama.Endpoint,
			Model:       cfg.AI.Ollama.Model,
			Temperature: cfg.AI.Ollama.Temperature,
			Timeout:     cfg.AI.Ollama.Timeout,
		})

	case "qwen":
		apiKey := cfg.AI.Qwen.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("QWEN_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("Qwen API key not configured")
		}

		return provider.NewQwenProvider(&provider.QwenConfig{
			APIKey:      apiKey,
			Endpoint:    cfg.AI.Qwen.Endpoint,
			Model:       cfg.AI.Qwen.Model,
			Temperature: cfg.AI.Qwen.Temperature,
			Timeout:     cfg.AI.Qwen.Timeout,
		})

	case "openai":
		apiKey := cfg.AI.OpenAI.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("OpenAI API key not configured")
		}

		return provider.NewOpenAIProvider(&provider.OpenAIConfig{
			APIKey:      apiKey,
			Endpoint:    cfg.AI.OpenAI.Endpoint,
			Model:       cfg.AI.OpenAI.Model,
			Temperature: cfg.AI.OpenAI.Temperature,
			MaxTokens:   cfg.AI.OpenAI.MaxTokens,
			Timeout:     cfg.AI.OpenAI.Timeout,
		})

	case "vllm":
		return provider.NewVLLMProvider(&provider.VLLMConfig{
			Endpoint:    cfg.AI.VLLM.Endpoint,
			Model:       cfg.AI.VLLM.Model,
			Temperature: cfg.AI.VLLM.Temperature,
			MaxTokens:   cfg.AI.VLLM.MaxTokens,
			Timeout:     cfg.AI.VLLM.Timeout,
		})

	case "hybrid":
		// For hybrid, we need to configure the providers as interfaces
		primaryName := cfg.AI.Hybrid.Primary
		fallbackName := cfg.AI.Hybrid.Fallback

		// Get the config for each provider
		primaryCfg, err := getProviderConfig(primaryName, cfg)
		if err != nil {
			return nil, fmt.Errorf("get primary provider config: %w", err)
		}

		fallbackCfg, err := getProviderConfig(fallbackName, cfg)
		if err != nil {
			return nil, fmt.Errorf("get fallback provider config: %w", err)
		}

		return provider.NewHybridProvider(&provider.HybridConfig{
			Primary:        primaryName,
			Fallback:       fallbackName,
			PrimaryConfig:  primaryCfg,
			FallbackConfig: fallbackCfg,
		})

	default:
		return nil, fmt.Errorf("unknown provider: %s", cfg.AI.Provider)
	}
}

// getProviderConfig returns the config for a specific provider
func getProviderConfig(name string, cfg *config.Config) (interface{}, error) {
	switch name {
	case "ollama":
		return &provider.OllamaConfig{
			Endpoint:    cfg.AI.Ollama.Endpoint,
			Model:       cfg.AI.Ollama.Model,
			Temperature: cfg.AI.Ollama.Temperature,
			Timeout:     cfg.AI.Ollama.Timeout,
		}, nil

	case "qwen":
		apiKey := cfg.AI.Qwen.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("QWEN_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("Qwen API key not configured")
		}
		return &provider.QwenConfig{
			APIKey:      apiKey,
			Endpoint:    cfg.AI.Qwen.Endpoint,
			Model:       cfg.AI.Qwen.Model,
			Temperature: cfg.AI.Qwen.Temperature,
			Timeout:     cfg.AI.Qwen.Timeout,
		}, nil

	case "openai":
		apiKey := cfg.AI.OpenAI.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
		if apiKey == "" {
			return nil, fmt.Errorf("OpenAI API key not configured")
		}
		return &provider.OpenAIConfig{
			APIKey:      apiKey,
			Endpoint:    cfg.AI.OpenAI.Endpoint,
			Model:       cfg.AI.OpenAI.Model,
			Temperature: cfg.AI.OpenAI.Temperature,
			MaxTokens:   cfg.AI.OpenAI.MaxTokens,
			Timeout:     cfg.AI.OpenAI.Timeout,
		}, nil

	case "vllm":
		return &provider.VLLMConfig{
			Endpoint:    cfg.AI.VLLM.Endpoint,
			Model:       cfg.AI.VLLM.Model,
			Temperature: cfg.AI.VLLM.Temperature,
			MaxTokens:   cfg.AI.VLLM.MaxTokens,
			Timeout:     cfg.AI.VLLM.Timeout,
		}, nil

	default:
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
}
