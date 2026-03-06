package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/davidhoo/relive/internal/api/v1/router"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/database"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/davidhoo/relive/pkg/version"
	"github.com/gin-gonic/gin"
)

func main() {
	// 命令行参数
	configPath := flag.String("config", "config.yaml", "配置文件路径")
	showVersion := flag.Bool("version", false, "显示版本信息")
	flag.Parse()

	// 显示版本
	if *showVersion {
		fmt.Printf("Relive Backend\n")
		fmt.Printf("Version: %s\n", version.Version)
		fmt.Printf("Build Time: %s\n", version.BuildTime)
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
	logger.Infof("Version: %s, Build Time: %s", version.Version, version.BuildTime)

	// 初始化数据库
	db, err := database.Init(cfg.Database)
	if err != nil {
		logger.Fatalf("Failed to initialize database: %v", err)
	}
	logger.Info("Database initialized successfully")

	// 设置 Gin 模式
	gin.SetMode(cfg.Server.Mode)

	// 初始化路由（同时获取服务集合）
	r, services := router.Setup(db, cfg)

	// 启动定时任务调度器
	services.Scheduler.Start()
	defer services.Scheduler.Stop()

	// 启动结果队列
	if services.ResultQueue != nil {
		if err := services.ResultQueue.Start(); err != nil {
			logger.Errorf("Failed to start result queue: %v", err)
		} else {
			logger.Info("Result queue started")
			defer services.ResultQueue.Stop()
		}
	}

	// 启动服务器
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	logger.Infof("Server listening on %s", addr)

	// 使用 http.Server 支持优雅关闭
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// 在 goroutine 中启动服务器
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server...")

	// 停止调度器
	services.Scheduler.Stop()

	// 优雅关闭服务器
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Errorf("Server forced to shutdown: %v", err)
	}

	logger.Info("Server exited")
}
