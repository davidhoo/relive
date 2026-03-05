package analyzer

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/provider"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/internal/analyzer"
	analyzerCache "github.com/davidhoo/relive/cmd/relive-analyzer/internal/cache"
	analyzerClient "github.com/davidhoo/relive/cmd/relive-analyzer/internal/client"
	analyzerConfig "github.com/davidhoo/relive/cmd/relive-analyzer/internal/config"
	"github.com/davidhoo/relive/cmd/relive-analyzer/internal/download"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/google/uuid"
)

// APIAnalyzer API 模式分析器
type APIAnalyzer struct {
	config         *analyzerConfig.Config
	client         *analyzerClient.APIClient
	taskManager    *analyzerClient.TaskManager
	downloader     *download.Downloader
	resultBuffer   *analyzerCache.ResultBuffer
	checkpoint     *analyzerCache.Checkpoint
	aiProvider     provider.AIProvider
	imageProcessor *util.ImageProcessor
	analyzerID     string

	// 工作控制
	workerPool   *analyzer.WorkerPool
	ctx          context.Context
	cancel       context.CancelFunc
	stopCh       chan struct{}
	wg           sync.WaitGroup

	// 统计
	stats        *analyzer.Stats
}

// NewAPIAnalyzer 创建 API 模式分析器
func NewAPIAnalyzer(cfg *analyzerConfig.Config) (*APIAnalyzer, error) {
	// 生成分析器实例ID
	analyzerID := cfg.Analyzer.AnalyzerID
	if analyzerID == "" {
		analyzerID = uuid.New().String()
	}

	// 创建 API 客户端
	client := analyzerClient.NewAPIClient(
		cfg.Server.Endpoint,
		cfg.Server.APIKey,
		analyzerClient.WithTimeout(cfg.GetServerTimeout()),
		analyzerClient.WithRetry(cfg.Analyzer.RetryCount, cfg.GetRetryDelay()),
	)

	// 创建任务管理器
	taskManager := analyzerClient.NewTaskManager(client, analyzerID, cfg.Analyzer.FetchLimit)

	// 创建下载器
	downloader, err := download.NewDownloader(
		client,
		download.WithTempDir(cfg.Download.TempDir),
		download.WithTimeout(cfg.GetDownloadTimeout()),
		download.WithRetryCount(cfg.Download.RetryCount),
		download.WithKeepTempFiles(cfg.Download.KeepTemp),
	)
	if err != nil {
		return nil, fmt.Errorf("create downloader: %w", err)
	}

	// 创建结果缓冲区
	resultBuffer := analyzerCache.NewResultBuffer(
		submitResultsFunc(client),
		analyzerCache.WithBatchSize(cfg.Batch.Size),
		analyzerCache.WithFlushInterval(cfg.GetFlushInterval()),
	)

	// 创建检查点管理器
	checkpoint, err := analyzerCache.NewCheckpoint(cfg.Analyzer.CheckpointFile)
	if err != nil {
		return nil, fmt.Errorf("create checkpoint: %w", err)
	}

	// 清理卡住的处理中记录
	if _, err := checkpoint.ResetStuckPending(1 * time.Hour); err != nil {
		logger.Warnf("Failed to reset stuck pending records: %v", err)
	}

	// 创建 AI Provider
	aiProvider, err := createAIProvider(cfg)
	if err != nil {
		return nil, fmt.Errorf("create AI provider: %w", err)
	}

	// 创建图像处理器
	imageProcessor := util.NewImageProcessor(1024, 85)

	// 创建工作池
	workerPool := analyzer.NewWorkerPool(cfg.Analyzer.Workers)

	// 创建上下文
	ctx, cancel := context.WithCancel(context.Background())

	return &APIAnalyzer{
		config:         cfg,
		client:         client,
		taskManager:    taskManager,
		downloader:     downloader,
		resultBuffer:   resultBuffer,
		checkpoint:     checkpoint,
		aiProvider:     aiProvider,
		imageProcessor: imageProcessor,
		analyzerID:     analyzerID,
		workerPool:     workerPool,
		ctx:            ctx,
		cancel:         cancel,
		stopCh:         make(chan struct{}),
		stats:          analyzer.NewStats(0),
	}, nil
}

// Check 检查服务端连接和任务统计
func (a *APIAnalyzer) Check() error {
	logger.Info("Checking server connection...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 检查服务健康
	if err := a.taskManager.CheckHealth(ctx); err != nil {
		return fmt.Errorf("server health check failed: %w", err)
	}

	logger.Info("Server connection OK")

	// 获取统计信息
	stats, err := a.taskManager.GetStats(ctx)
	if err != nil {
		return fmt.Errorf("get stats: %w", err)
	}

	fmt.Println("\n========================================")
	fmt.Println("Server Status")
	fmt.Println("========================================")
	fmt.Printf("Total photos:      %d\n", stats.TotalPhotos)
	fmt.Printf("Analyzed:          %d (%.1f%%)\n", stats.Analyzed, float64(stats.Analyzed)/float64(stats.TotalPhotos)*100)
	fmt.Printf("Pending:           %d\n", stats.Pending)
	fmt.Printf("Locked:            %d\n", stats.Locked)
	fmt.Printf("Failed:            %d\n", stats.Failed)
	fmt.Println("========================================")
	fmt.Printf("Queue pressure:    %s\n", stats.QueuePressure)

	// 本地检查点统计
	cpStats, err := a.checkpoint.GetStats()
	if err == nil && cpStats.Total > 0 {
		fmt.Println("\n========================================")
		fmt.Println("Local Checkpoint")
		fmt.Println("========================================")
		fmt.Printf("Total processed:   %d\n", cpStats.Total)
		fmt.Printf("Success:           %d\n", cpStats.Success)
		fmt.Printf("Failed:            %d\n", cpStats.Failed)
		fmt.Println("========================================")
	}

	return nil
}

// Run 运行分析器
func (a *APIAnalyzer) Run() error {
	logger.Info("Starting API analyzer...")
	logger.Infof("Analyzer ID: %s", a.analyzerID)
	logger.Infof("Workers: %d", a.config.Analyzer.Workers)
	logger.Infof("AI Provider: %s", a.aiProvider.Name())

	// 检查 AI Provider
	if !a.aiProvider.IsAvailable() {
		return fmt.Errorf("AI provider %s is not available", a.aiProvider.Name())
	}
	logger.Info("AI provider is available")

	// 恢复结果缓冲区
	if err := a.resultBuffer.Restore(); err != nil {
		logger.Warnf("Failed to restore result buffer: %v", err)
	}

	// 启动结果缓冲区
	a.resultBuffer.Start()
	defer a.resultBuffer.Stop()

	// 启动工作池
	a.workerPool.Start()

	// 设置信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 启动任务获取循环
	a.wg.Add(1)
	go a.fetchLoop()

	// 启动任务处理循环
	a.wg.Add(1)
	go a.processLoop()

	logger.Info("Analyzer is running, press Ctrl+C to stop")

	// 等待停止信号
	select {
	case <-sigCh:
		logger.Info("Received stop signal, shutting down...")
	case <-a.stopCh:
		logger.Info("Analyzer stopped")
	}

	// 停止所有组件
	a.Stop()

	// 打印统计
	a.stats.Print()

	return nil
}

// Stop 停止分析器
func (a *APIAnalyzer) Stop() {
	a.cancel()
	a.taskManager.StopAllHeartbeats()
	a.workerPool.Cancel()
	a.wg.Wait()

	// 保存检查点
	if a.checkpoint != nil {
		a.checkpoint.Close()
	}

	// 清理临时文件
	if a.downloader != nil {
		a.downloader.Cleanup()
	}
}

// fetchLoop 任务获取循环
func (a *APIAnalyzer) fetchLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// 检查任务队列
			if a.taskManager.TaskCount() < a.config.Analyzer.FetchLimit {
				ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
				_, err := a.taskManager.FetchTasks(ctx)
				cancel()

				if err != nil {
					if err.Error() != "no tasks available" {
						logger.Warnf("Failed to fetch tasks: %v", err)
					}
				}
			}

		case <-a.ctx.Done():
			return
		}
	}
}

// processLoop 任务处理循环
func (a *APIAnalyzer) processLoop() {
	defer a.wg.Done()

	for {
		select {
		case <-a.ctx.Done():
			return
		default:
		}

		task, ok := a.taskManager.GetNextTask()
		if !ok {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// 检查是否已经处理过
		processed, err := a.checkpoint.IsProcessed(task.PhotoID)
		if err != nil {
			logger.Errorf("Failed to check checkpoint: %v", err)
		}
		if processed {
			logger.Debugf("Photo %d already processed, skipping", task.PhotoID)
			a.taskManager.StopHeartbeat(task.ID)
			continue
		}

		// 提交到工作池
		t := task // 捕获循环变量
		if err := a.workerPool.Submit(func(ctx context.Context) error {
			return a.processTask(ctx, t)
		}); err != nil {
			logger.Errorf("Failed to submit task: %v", err)
		}
	}
}

// processTask 处理单个任务
func (a *APIAnalyzer) processTask(ctx context.Context, task *model.AnalysisTask) error {
	startTime := time.Now()

	// 标记为处理中
	if err := a.checkpoint.MarkPending(task.PhotoID); err != nil {
		logger.Warnf("Failed to mark pending: %v", err)
	}

	// 下载照片
	a.taskManager.UpdateHeartbeatProgress(task.ID, 10, "downloading")
	tempFile, err := a.downloader.Download(ctx, task.PhotoID, task.DownloadURL)
	if err != nil {
		a.handleTaskError(task, err, "download_failed")
		return err
	}
	defer a.downloader.Delete(tempFile)

	// 处理图像
	a.taskManager.UpdateHeartbeatProgress(task.ID, 30, "processing")
	imageData, err := a.imageProcessor.ProcessForAI(tempFile)
	if err != nil {
		a.handleTaskError(task, err, "processing_failed")
		return err
	}

	// AI 分析
	a.taskManager.UpdateHeartbeatProgress(task.ID, 50, "analyzing")
	request := &provider.AnalyzeRequest{
		ImageData: imageData,
		ImagePath: task.FilePath,
		ExifInfo: &provider.ExifInfo{
			DateTime: "",
			City:     task.Location,
			Model:    task.CameraModel,
		},
	}

	if task.TakenAt != nil {
		request.ExifInfo.DateTime = task.TakenAt.Format("2006-01-02 15:04:05")
	}

	result, err := a.aiProvider.Analyze(request)
	if err != nil {
		a.handleTaskError(task, err, "analysis_failed")
		return err
	}

	// 构建分析结果
	analysisResult := model.AnalysisResult{
		PhotoID:      task.PhotoID,
		TaskID:       task.ID,
		Description:  result.Description,
		Caption:      result.Caption,
		MemoryScore:  int(result.MemoryScore),
		BeautyScore:  int(result.BeautyScore),
		OverallScore: int(result.MemoryScore*0.7 + result.BeautyScore*0.3),
		ScoreReason:  result.Reason,
		MainCategory: result.MainCategory,
		Tags:         result.Tags,
		AnalyzedAt:   time.Now(),
		AIProvider:   a.aiProvider.Name(),
	}

	// 添加到结果缓冲区
	a.resultBuffer.Add(analysisResult)

	// 更新检查点
	if err := a.checkpoint.MarkSuccess(task.PhotoID); err != nil {
		logger.Warnf("Failed to mark success: %v", err)
	}

	// 停止心跳
	a.taskManager.StopHeartbeat(task.ID)

	// 更新统计
	duration := time.Since(startTime)
	a.stats.RecordSuccess(duration, result.Cost)

	logger.Debugf("Analyzed photo %d: %s (%.2fs)", task.PhotoID, task.FilePath, duration.Seconds())

	return nil
}

// handleTaskError 处理任务错误
func (a *APIAnalyzer) handleTaskError(task *model.AnalysisTask, err error, reason string) {
	logger.Errorf("Task %s failed: %v", task.ID, err)

	// 更新检查点
	if cpErr := a.checkpoint.MarkFailed(task.PhotoID, err.Error()); cpErr != nil {
		logger.Warnf("Failed to mark failed: %v", cpErr)
	}

	// 释放任务
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if releaseErr := a.taskManager.ReleaseTask(ctx, task.ID, reason, err.Error(), true); releaseErr != nil {
		logger.Warnf("Failed to release task: %v", releaseErr)
	}

	// 停止心跳
	a.taskManager.StopHeartbeat(task.ID)

	// 更新统计
	a.stats.RecordFailure(reason)
}

// createAIProvider 创建 AI Provider
func createAIProvider(cfg *analyzerConfig.Config) (provider.AIProvider, error) {
	switch cfg.AI.Provider {
	case "ollama":
		return provider.NewOllamaProvider(&provider.OllamaConfig{
			Endpoint:    cfg.AI.Ollama.Endpoint,
			Model:       cfg.AI.Ollama.Model,
			Temperature: cfg.AI.Ollama.Temperature,
			Timeout:     cfg.AI.Ollama.Timeout,
		})
	case "vllm":
		return provider.NewVLLMProvider(&provider.VLLMConfig{
			Endpoint:    cfg.AI.VLLM.Endpoint,
			Model:       cfg.AI.VLLM.Model,
			Temperature: cfg.AI.VLLM.Temperature,
			Timeout:     cfg.AI.VLLM.Timeout,
		})
	default:
		return nil, fmt.Errorf("unsupported provider: %s", cfg.AI.Provider)
	}
}

// submitResultsFunc 创建结果提交函数
func submitResultsFunc(client *analyzerClient.APIClient) func(ctx context.Context, results []model.AnalysisResult) error {
	return func(ctx context.Context, results []model.AnalysisResult) error {
		resp, err := client.SubmitResults(ctx, results)
		if err != nil {
			return err
		}

		// 记录被拒绝的项
		if resp.Rejected > 0 {
			for _, item := range resp.RejectedItems {
				logger.Warnf("Result rejected: photo_id=%d, reason=%s", item.PhotoID, item.Reason)
			}
		}

		logger.Infof("Submitted %d results (accepted: %d, rejected: %d, failed: %d)",
			len(results), resp.Accepted, resp.Rejected, len(resp.FailedPhotos))

		// 如果有失败的照片，返回错误让缓冲区恢复数据以便重试
		if len(resp.FailedPhotos) > 0 {
			logger.Errorf("Failed to submit %d results, will retry", len(resp.FailedPhotos))
			return fmt.Errorf("server failed to process %d results", len(resp.FailedPhotos))
		}

		return nil
	}
}
