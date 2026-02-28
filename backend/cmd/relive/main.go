package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/davidhoo/relive/internal/api/v1/router"
	"github.com/gin-gonic/gin"
)

var (
	// 版本信息（编译时注入）
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	// 命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本信息")
	flag.Parse()

	// 显示版本
	if *showVersion {
		fmt.Printf("Relive Backend\n")
		fmt.Printf("Version: %s\n", Version)
		fmt.Printf("Build Time: %s\n", BuildTime)
		os.Exit(0)
	}

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 初始化日志
	if err := logger.Init(cfg.Logging); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("Starting Relive Backend...")
	logger.Infof("Version: %s, Build Time: %s", Version, BuildTime)

	// 初始化数据库
	db, err := database.Init(cfg.Database)
	if err != nil {
		logger.Fatalf("Failed to initialize database: %v", err)
	}
	logger.Info("Database initialized successfully")

	// 设置 Gin 模式
	gin.SetMode(cfg.Server.Mode)

	// 初始化路由
	r := router.Setup(db, cfg)

	// 启动服务器
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.Infof("Server listening on %s", addr)

	if err := r.Run(addr); err != nil {
		logger.Fatalf("Failed to start server: %v", err)
	}
}
