package analyzer

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	analyzerCache "github.com/davidhoo/relive/cmd/relive-analyzer/internal/cache"
	analyzerClient "github.com/davidhoo/relive/cmd/relive-analyzer/internal/client"
	analyzerConfig "github.com/davidhoo/relive/cmd/relive-analyzer/internal/config"
	"github.com/davidhoo/relive/cmd/relive-analyzer/internal/download"
	"github.com/davidhoo/relive/internal/analyzer"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/provider"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/google/uuid"
)

// APIAnalyzer API 模式分析器
type APIAnalyzer struct {
	config                 *analyzerConfig.Config
	client                 *analyzerClient.APIClient
	taskManager            *analyzerClient.TaskManager
	downloader             *download.Downloader
	resultBuffer           *analyzerCache.ResultBuffer
	checkpoint             *analyzerCache.Checkpoint
	aiProvider             provider.AIProvider
	imageProcessor         *util.ImageProcessor
	analyzerID             string
	runtimeLeaseAcquired   bool
	runtimeHeartbeatCancel context.CancelFunc

	// 工作控制
	workerPool *analyzer.WorkerPool
	ctx        context.Context
	cancel     context.CancelFunc
	stopCh     chan struct{}
	wg         sync.WaitGroup

	// 统计
	stats *analyzer.Stats
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
		fmt.Printf("Analyzed:          %d (pending submission)\n", cpStats.Analyzed)
		fmt.Printf("Submitted:         %d\n", cpStats.Submitted)
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

	if err := a.acquireRuntimeLease(); err != nil {
		return err
	}

	// 检查 AI Provider
	if !a.aiProvider.IsAvailable() {
		return fmt.Errorf("AI provider %s is not available", a.aiProvider.Name())
	}
	logger.Info("AI provider is available")

	// 恢复结果缓冲区（文件中的未提交结果）
	if err := a.resultBuffer.Restore(); err != nil {
		logger.Warnf("Failed to restore result buffer: %v", err)
	}

	// 恢复检查点中 analyzed 状态的任务（上次崩溃未提交的）
	if err := a.restoreUnsubmitted(); err != nil {
		logger.Warnf("Failed to restore unsubmitted results: %v", err)
	}

	// 设置提交成功回调（用于更新 checkpoint 和停止心跳）
	a.resultBuffer.SetOnSubmitted(func(results []model.AnalysisResult) {
		for _, result := range results {
			// 更新检查点为已提交
			if err := a.checkpoint.MarkSubmitted(result.PhotoID); err != nil {
				logger.Warnf("Failed to mark submitted for photo %d: %v", result.PhotoID, err)
			}
			// 停止对应的心跳
			if result.TaskID != "" {
				a.taskManager.StopHeartbeat(result.TaskID)
			}
		}
		logger.Debugf("Marked %d photos as submitted", len(results))
	})

	// 启动结果缓冲区
	a.resultBuffer.Start()

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

// restoreUnsubmitted 恢复检查点中 analyzed 状态的结果到缓冲区
// 用于程序崩溃后重启时，重新提交未成功提交的结果
func (a *APIAnalyzer) restoreUnsubmitted() error {
	photoIDs, err := a.checkpoint.GetAnalyzed()
	if err != nil {
		return fmt.Errorf("get analyzed photo IDs: %w", err)
	}

	if len(photoIDs) == 0 {
		return nil
	}

	logger.Infof("Found %d unsubmitted results from previous run, will retry submission", len(photoIDs))

	// 注意：这里我们只记录了 photoID，没有记录完整的 AnalysisResult
	// 这是因为结果已经保存在 buffer.json 中了（通过 Persist）
	// 如果 buffer.json 丢失了，这些任务将永远处于 analyzed 状态但不会提交
	// 这种情况下，下次获取任务时会跳过（IsProcessed 返回 true），需要手动清理

	return nil
}

// Stop 停止分析器
func (a *APIAnalyzer) Stop() {
	a.cancel()
	a.stopRuntimeLease()
	a.taskManager.StopAllHeartbeats()
	a.workerPool.Cancel()
	a.wg.Wait()

	// 先停止结果缓冲区（触发 Flush 和回调）
	if a.resultBuffer != nil {
		a.resultBuffer.Stop()
	}

	// 再关闭检查点（确保回调可以访问数据库）
	if a.checkpoint != nil {
		a.checkpoint.Close()
	}

	// 清理临时文件
	if a.downloader != nil {
		a.downloader.Cleanup()
	}
}

func (a *APIAnalyzer) acquireRuntimeLease() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, status, err := a.client.AcquireAnalysisRuntime(ctx, model.AnalysisOwnerTypeAnalyzer, a.analyzerID, "离线 analyzer 运行中")
	if err != nil {
		if status != nil && status.IsActive {
			return fmt.Errorf("检测到其他分析器正在运行：%s(%s)，离线 analyzer 已退出", status.OwnerType, status.OwnerID)
		}
		return fmt.Errorf("acquire analysis runtime: %w", err)
	}

	a.runtimeLeaseAcquired = true
	hbCtx, hbCancel := context.WithCancel(context.Background())
	a.runtimeHeartbeatCancel = hbCancel
	a.wg.Add(1)
	go a.runtimeHeartbeatLoop(hbCtx)
	return nil
}

func (a *APIAnalyzer) runtimeHeartbeatLoop(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			hbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := a.client.HeartbeatAnalysisRuntime(hbCtx, model.AnalysisOwnerTypeAnalyzer, a.analyzerID)
			cancel()
			if err != nil {
				logger.Warnf("Failed to heartbeat analysis runtime lease: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (a *APIAnalyzer) stopRuntimeLease() {
	if a.runtimeHeartbeatCancel != nil {
		a.runtimeHeartbeatCancel()
		a.runtimeHeartbeatCancel = nil
	}
	if !a.runtimeLeaseAcquired {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.client.ReleaseAnalysisRuntime(ctx, model.AnalysisOwnerTypeAnalyzer, a.analyzerID); err != nil {
		logger.Warnf("Failed to release analysis runtime lease: %v", err)
	}
	a.runtimeLeaseAcquired = false
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

	// 添加到结果缓冲区（内部会触发 Persist 保存到文件）
	a.resultBuffer.Add(analysisResult)

	// 【关键变更】标记为已分析（等待提交），而不是已提交
	// 心跳继续保持，直到异步提交成功后通过回调停止
	if err := a.checkpoint.MarkAnalyzed(task.PhotoID); err != nil {
		logger.Warnf("Failed to mark analyzed: %v", err)
	}

	// 【移除】这里不再停止心跳，移到提交回调中
	// a.taskManager.StopHeartbeat(task.ID)

	// 更新统计（AI分析成功）
	duration := time.Since(startTime)
	a.stats.RecordSuccess(duration, result.Cost)

	logger.Debugf("Analyzed photo %d: %s (%.2fs), waiting for submission", task.PhotoID, task.FilePath, duration.Seconds())

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

		// 分析提交结果
		var actuallyFailed []uint
		var rejectedAsSuccess []uint // 因“已分析”等原因被拒绝，视为成功

		for _, item := range resp.RejectedItems {
			switch item.Reason {
			case "already_analyzed", "task_not_found", "photo_not_found":
				// 这些原因视为成功：
				// - already_analyzed: 照片已经被分析过了
				// - task_not_found: 任务已过期被释放，但结果可能已保存
				// - photo_not_found: 照片不存在（被删除）
				logger.Warnf("Result rejected but treated as success: photo_id=%d, reason=%s",
					item.PhotoID, item.Reason)
				rejectedAsSuccess = append(rejectedAsSuccess, item.PhotoID)
			default:
				// 其他原因需要重试
				logger.Errorf("Result rejected: photo_id=%d, reason=%s, message=%s",
					item.PhotoID, item.Reason, item.Message)
				actuallyFailed = append(actuallyFailed, item.PhotoID)
			}
		}

		// 合并 FailedPhotos
		actuallyFailed = append(actuallyFailed, resp.FailedPhotos...)

		logger.Infof("Submitted %d results (accepted: %d, rejected-as-success: %d, failed: %d)",
			len(results), resp.Accepted, len(rejectedAsSuccess), len(actuallyFailed))

		// 如果有真正失败的照片，返回错误让缓冲区恢复数据以便重试
		if len(actuallyFailed) > 0 {
			logger.Errorf("Failed to submit %d results, will retry", len(actuallyFailed))
			return fmt.Errorf("server failed to process %d results", len(actuallyFailed))
		}

		return nil
	}
}
