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

	// 熔断器与租约暂停
	circuit      *CircuitBreaker
	leasePaused  bool
	leasePauseMu sync.Mutex

	// 工作控制
	workerPool *analyzer.WorkerPool
	ctx        context.Context
	cancel     context.CancelFunc
	stopCh     chan struct{}
	wg         sync.WaitGroup

	// 统计
	stats *analyzer.Stats

	// 会话级永久失败记录：记录本次运行中遇到不可恢复错误的照片 ID，不再重试
	sessionPermanentFailures map[uint]string // photoID → error reason
	sessionFailMu            sync.Mutex
}

// circuitConfigFromCfg 把 config 包的镜像结构转换为 CircuitConfig。
func circuitConfigFromCfg(c analyzerConfig.AnalyzerConfig) CircuitConfig {
	threshold := c.CircuitFailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	initial := time.Duration(c.CircuitInitialBackoff) * time.Second
	if initial <= 0 {
		initial = 30 * time.Second
	}
	max := time.Duration(c.CircuitMaxBackoff) * time.Second
	if max <= 0 {
		max = 10 * time.Minute
	}
	return CircuitConfig{
		FailureThreshold: threshold,
		InitialBackoff:   initial,
		MaxBackoff:       max,
	}
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
		circuit:        NewCircuitBreaker(circuitConfigFromCfg(cfg.Analyzer)),
		workerPool:               workerPool,
		ctx:                     ctx,
		cancel:                  cancel,
		stopCh:                  make(chan struct{}),
		stats:                   analyzer.NewStats(0),
		sessionPermanentFailures: make(map[uint]string),
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

	// 打印统计与结束摘要
	a.printSummary()

	return nil
}

// printSummary 区分全部成功、部分最终失败、Provider 熔断暂停和用户取消。
func (a *APIAnalyzer) printSummary() {
	a.stats.Print()

	finalFailures := len(a.sessionPermanentFailures)
	circuitOpen := a.circuit.State() != CircuitClosed
	ctxErr := a.ctx.Err()

	switch {
	case ctxErr != nil && !circuitOpen && finalFailures == 0:
		// 用户取消或正常停止
		logger.Info("Analysis run stopped")
	case finalFailures > 0:
		logger.Warnf("Analysis run finished with %d permanently failed photo(s)", finalFailures)
	case circuitOpen:
		logger.Warn("Analysis run paused due to provider circuit open; will auto-resume on provider recovery")
	case ctxErr != nil:
		logger.Info("Analysis run cancelled")
	default:
		logger.Info("Analysis completed successfully")
	}
}

// restoreUnsubmitted 清理检查点中 analyzed 状态的过期记录
// 这些记录表示上次运行中分析完成但提交未成功（崩溃、网络失败等）
// 清除这些记录后，服务器会重新分配任务，analyzer 会重新分析
func (a *APIAnalyzer) restoreUnsubmitted() error {
	photoIDs, err := a.checkpoint.GetAnalyzed()
	if err != nil {
		return fmt.Errorf("get analyzed photo IDs: %w", err)
	}

	if len(photoIDs) == 0 {
		return nil
	}

	// 清除这些过期的 analyzed 记录，让服务器重新分配任务进行分析
	for _, photoID := range photoIDs {
		if err := a.checkpoint.ResetFailed(photoID); err != nil {
			logger.Warnf("Failed to reset stale checkpoint for photo %d: %v", photoID, err)
		}
	}

	logger.Infof("Cleared %d stale 'analyzed' checkpoint entries from previous run, will re-process when server assigns", len(photoIDs))
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

// acquireRuntimeLease 获取全局分析运行租约
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

	a.setLeasePaused(false)
	a.runtimeLeaseAcquired = true
	hbCtx, hbCancel := context.WithCancel(context.Background())
	a.runtimeHeartbeatCancel = hbCancel
	a.wg.Add(1)
	go a.runtimeHeartbeatLoop(hbCtx)
	return nil
}

// setLeasePaused 设置租约暂停状态。
func (a *APIAnalyzer) setLeasePaused(paused bool) {
	a.leasePauseMu.Lock()
	defer a.leasePauseMu.Unlock()
	a.leasePaused = paused
}

// isLeasePaused 返回租约是否暂停。
func (a *APIAnalyzer) isLeasePaused() bool {
	a.leasePauseMu.Lock()
	defer a.leasePauseMu.Unlock()
	return a.leasePaused
}

// tryReacquireLease 在租约丢失时立即尝试一次重新获取。
// 不在这里做退避：fetchLoop 每 5 秒 tick 一次，lease-paused 期间每个 tick 调用一次
// 本函数，本身就是有界的重试节奏（最坏 5s 一次）。真正恢复后由 setLeasePaused(false)
// 解除暂停，普通 fetch 恢复。
func (a *APIAnalyzer) tryReacquireLease() {
	if err := a.acquireRuntimeLease(); err != nil {
		logger.Warnf("Re-acquire runtime lease failed: %v", err)
		a.setLeasePaused(true)
		return
	}
	logger.Info("Runtime lease re-acquired, resuming fetch")
}

func (a *APIAnalyzer) runtimeHeartbeatLoop(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	consecutiveFailures := 0
	for {
		select {
		case <-ticker.C:
			hbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := a.client.HeartbeatAnalysisRuntime(hbCtx, model.AnalysisOwnerTypeAnalyzer, a.analyzerID)
			cancel()
			if err != nil {
				consecutiveFailures++
				logger.Warnf("Failed to heartbeat analysis runtime lease: %v", err)
				// 连续 3 次心跳失败视为租约丢失，进入 lease-paused。
				if consecutiveFailures >= 3 && !a.isLeasePaused() {
					logger.Warn("Runtime lease heartbeat failed repeatedly, entering lease-paused")
					a.setLeasePaused(true)
				}
			} else {
				if consecutiveFailures >= 3 {
					logger.Info("Runtime lease heartbeat recovered")
				}
				consecutiveFailures = 0
				a.setLeasePaused(false)
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
//
// 在 circuit open、probe 已在途、lease 未持有或 context 取消时不得领取。
// 使用 timer/select，不使用不可取消的长 sleep。
func (a *APIAnalyzer) fetchLoop() {
	defer a.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// lease 丢失时进入 lease-paused，不普通 fetch。
			if a.isLeasePaused() {
				logger.Warnf("Runtime lease paused, skipping fetch; attempting re-acquire")
				a.tryReacquireLease()
				continue
			}

			// circuit 禁止领取时跳过（half-open 探测由 processLoop 单独获取）。
			if !a.circuit.CanFetch() {
				continue
			}

			// 计算需要补充的数量：目标是保持本地队列 = workers 数量
			current := a.taskManager.TaskCount()
			target := a.config.Analyzer.Workers
			if target < a.config.Analyzer.FetchLimit {
				target = a.config.Analyzer.FetchLimit
			}
			need := target - current
			if need <= 0 {
				continue
			}

			ctx, cancel := context.WithTimeout(a.ctx, 60*time.Second)
			_, err := a.taskManager.FetchTasks(ctx, need)
			cancel()

			if err != nil {
				if err.Error() != "no tasks available" {
					logger.Warnf("Failed to fetch tasks: %v", err)
				}
				continue
			}

			// 注意：half-open 探测的成功/失败由 processLoop 通过 AcquireProbe + processTask
			// 的 AI 分析结果决定（fetch 成功只代表服务端可通信，不代表 Provider 健康）。
			// 这里不基于 fetch 结果调用 RecordSuccess——fetchLoop 在 half-open 时
			// CanFetch() 返回 false，本分支不会在 half-open 状态下执行。

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

		// half-open 探测任务：领取后立即占用 probe 许可，处理结果决定熔断器收敛。
		probe := false
		if a.circuit.State() == CircuitHalfOpen {
			if !a.circuit.AcquireProbe() {
				// 已有 probe 在途；把任务退还到本地队列稍后再处理。
				a.taskManager.RequeueTask(task)
				time.Sleep(500 * time.Millisecond)
				continue
			}
			probe = true
		}

		// 检查本次会话中是否已标记为永久失败（不可恢复错误，如损坏的 JPEG）
		a.sessionFailMu.Lock()
		if reason, failed := a.sessionPermanentFailures[task.PhotoID]; failed {
			a.sessionFailMu.Unlock()
			logger.Debugf("Photo %d permanently failed in this session (%s), skipping", task.PhotoID, reason)
			ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
			a.taskManager.ReleaseTask(ctx, task.ID, model.ReleaseTaskRequest{
				Reason:       "permanent_error",
				ErrorMsg:     reason,
				FailureClass: FailureClassInputPermanent,
				Provider:     a.aiProvider.Name(),
				LockVersion:  task.LockVersion,
			})
			cancel()
			if probe {
				a.circuit.ReleaseProbe()
			}
			continue
		}
		a.sessionFailMu.Unlock()

		// 服务器是 source of truth；本地 checkpoint 仅做诊断，不再用本地 3 次阈值覆盖服务端决定。
		processed, err := a.checkpoint.IsProcessed(task.PhotoID)
		if err != nil {
			logger.Errorf("Failed to check checkpoint: %v", err)
		}
		if processed {
			status, _ := a.checkpoint.GetStatus(task.PhotoID)
			switch status {
			case string(analyzerCache.StatusAnalyzed), string(analyzerCache.StatusSubmitted), "success":
				logger.Infof("Photo %d has stale checkpoint status '%s', server reassigned, will re-process", task.PhotoID, status)
				a.checkpoint.ResetFailed(task.PhotoID)
			case string(analyzerCache.StatusFailed):
				// 本地失败仅作诊断；是否重试由服务端 attempts/next_retry_at 决定。
				a.checkpoint.ResetFailed(task.PhotoID)
			default:
				logger.Warnf("Photo %d has unknown checkpoint status '%s', resetting", task.PhotoID, status)
				a.checkpoint.ResetFailed(task.PhotoID)
			}
		}

		// 通过 checkpoint 检查后才启动心跳（带 lock version）
		a.taskManager.StartHeartbeat(task.ID, task.LockExpiresAt, task.LockVersion)

		// 提交到工作池
		t := task // 捕获循环变量
		p := probe
		if err := a.workerPool.Submit(func(ctx context.Context) error {
			return a.processTask(ctx, t, p)
		}); err != nil {
			logger.Errorf("Failed to submit task: %v", err)
			if p {
				a.circuit.ReleaseProbe()
			}
		}
	}
}

// processTask 处理单个任务
func (a *APIAnalyzer) processTask(ctx context.Context, task *model.AnalysisTask, probe bool) error {
	startTime := time.Now()

	// probe 结束时统一释放许可。
	defer func() {
		if probe {
			a.circuit.ReleaseProbe()
		}
	}()

	// 标记为处理中
	if err := a.checkpoint.MarkPending(task.PhotoID); err != nil {
		logger.Warnf("Failed to mark pending: %v", err)
	}

	// 下载照片
	a.taskManager.UpdateHeartbeatProgress(task.ID, 10, "downloading")
	tempFile, err := a.downloader.Download(ctx, task.PhotoID, task.DownloadURL)
	if err != nil {
		// 下载失败包装为 download_failed 分类（计退避，不触发 Provider 熔断）。
		a.handleTaskError(task, NewDownloadFailedError(err), "download_failed")
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

	caption, captionErr := provider.EnsureCaption(a.aiProvider, request, result)
	if captionErr != nil {
		logger.Warnf("Caption generation failed for photo %d, using fallback: %v", task.PhotoID, captionErr)
	}
	result.Caption = caption

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

	// probe 成功 → 关闭熔断器。
	if probe {
		a.circuit.RecordSuccess()
	}

	logger.Debugf("Analyzed photo %d: %s (%.2fs), waiting for submission", task.PhotoID, task.FilePath, duration.Seconds())

	return nil
}

// handleTaskError 处理任务错误
//
// 对错误进行分类，构造带 failure_class/provider/lock_version 的 release 请求，
// 并按分类更新熔断器。input_permanent 标记会话级跳过；client_cancelled 不计业务失败。
func (a *APIAnalyzer) handleTaskError(task *model.AnalysisTask, err error, reason string) {
	logger.Errorf("Task %s failed: %v", task.ID, err)

	class := ClassifyFailure(err)

	// 不可恢复的永久错误，标记会话级跳过。
	if class == FailureClassInputPermanent {
		a.sessionFailMu.Lock()
		a.sessionPermanentFailures[task.PhotoID] = err.Error()
		a.sessionFailMu.Unlock()
		logger.Warnf("Photo %d marked as permanent failure for this session: %v", task.PhotoID, err)
	}

	// 更新熔断器（input_permanent / client_cancelled 不触发熔断）。
	if class != FailureClassInputPermanent && class != FailureClassClientCancelled {
		retryAfter := extractRetryAfter(err)
		a.circuit.RecordFailure(task.PhotoID, class, retryAfter)
	}

	// 更新检查点（诊断用）
	if cpErr := a.checkpoint.MarkFailed(task.PhotoID, err.Error()); cpErr != nil {
		logger.Warnf("Failed to mark failed: %v", cpErr)
	}

	// 构造 release 请求
	retryAfterSecs := 0
	if ra := extractRetryAfter(err); ra > 0 {
		retryAfterSecs = int(ra.Seconds())
	}
	req := model.ReleaseTaskRequest{
		Reason:            reason,
		ErrorMsg:          err.Error(),
		FailureClass:      class,
		Provider:          a.aiProvider.Name(),
		RetryAfterSeconds: retryAfterSecs,
		LockVersion:       task.LockVersion,
	}
	// 旧字段兼容：input_permanent 时 retry_later=false，其余按 transient。
	req.RetryLater = false

	// 释放任务
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, releaseErr := a.taskManager.ReleaseTask(ctx, task.ID, req); releaseErr != nil {
		logger.Warnf("Failed to release task: %v", releaseErr)
	}

	// 停止心跳
	a.taskManager.StopHeartbeat(task.ID)

	// 更新统计（client_cancelled 不计业务失败）
	if class != FailureClassClientCancelled {
		a.stats.RecordFailure(reason)
	}
}

// extractRetryAfter 从错误中提取 Retry-After 时长（仅 ProviderError 携带）。
func extractRetryAfter(err error) time.Duration {
	if pe, ok := provider.IsProviderError(err); ok {
		return pe.RetryAfter
	}
	return 0
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
