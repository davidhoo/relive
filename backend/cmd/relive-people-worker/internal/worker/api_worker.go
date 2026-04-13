package worker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/davidhoo/relive/cmd/relive-people-worker/internal/client"
	"github.com/davidhoo/relive/cmd/relive-people-worker/internal/config"
	"github.com/davidhoo/relive/cmd/relive-people-worker/internal/download"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/google/uuid"
)

// PeopleWorker People Worker 实现
type PeopleWorker struct {
	config         *config.Config
	apiClient      *client.APIClient
	taskManager    *client.TaskManager
	downloader     *download.Downloader
	imageProcessor *util.ImageProcessor
	workerID       string

	// 运行时租约
	runtimeLeaseAcquired bool
	runtimeHeartbeatStop chan struct{}
	runtimeLeaseLost     atomic.Bool

	// 工作控制
	ctx      context.Context
	cancel   context.CancelFunc
	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	// 统计
	tasksProcessed int64
	tasksFailed    int64
	facesDetected  int64
	inFlightTasks  atomic.Int64
}

// NewPeopleWorker 创建 People Worker
func NewPeopleWorker(cfg *config.Config) (*PeopleWorker, error) {
	// 生成 Worker ID
	workerID := cfg.PeopleWorker.WorkerID
	if workerID == "" {
		workerID = uuid.New().String()
	}

	// 创建 API 客户端
	apiClient := client.NewAPIClient(
		cfg.Server.Endpoint,
		cfg.Server.APIKey,
		client.WithTimeout(cfg.GetServerTimeout()),
		client.WithRetry(cfg.PeopleWorker.RetryCount, cfg.GetRetryDelay()),
		client.WithWorkerID(workerID),
	)

	// 创建任务管理器
	taskManager := client.NewTaskManager(apiClient)

	// 创建下载器
	downloader, err := download.NewDownloader(
		download.WithTempDir(cfg.Download.TempDir),
		download.WithTimeout(cfg.GetDownloadTimeout()),
		download.WithRetryCount(cfg.Download.RetryCount),
	)
	if err != nil {
		return nil, fmt.Errorf("create downloader: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &PeopleWorker{
		config:               cfg,
		apiClient:            apiClient,
		taskManager:          taskManager,
		downloader:           downloader,
		imageProcessor:       util.NewImageProcessor(1024, 85),
		workerID:             workerID,
		runtimeHeartbeatStop: make(chan struct{}),
		ctx:                  ctx,
		cancel:               cancel,
		stopCh:               make(chan struct{}),
	}, nil
}

// Check 检查服务器和 ML 服务连接
func (w *PeopleWorker) Check() error {
	logger.Info("Checking server connection...")

	// 检查服务器连接
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 尝试获取运行时租约（测试服务器连通性）
	lease, err := w.apiClient.AcquireRuntime(ctx)
	if err != nil {
		logger.Errorf("Server connection failed: %v", err)
		return fmt.Errorf("server connection failed: %w", err)
	}
	logger.Infof("Server connection OK (lease expires at: %v)", lease.LeaseExpiresAt)

	// 释放租约
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	if err := w.apiClient.ReleaseRuntime(ctx2); err != nil {
		logger.Warnf("Failed to release runtime lease: %v", err)
	}

	// 检查 ML 服务
	logger.Info("Checking ML service connection...")
	if err := w.checkMLService(); err != nil {
		logger.Errorf("ML service connection failed: %v", err)
		return fmt.Errorf("ML service connection failed: %w", err)
	}
	logger.Info("ML service connection OK")

	logger.Info("All checks passed!")
	return nil
}

// checkMLService 检查 ML 服务
func (w *PeopleWorker) checkMLService() error {
	ctx, cancel := context.WithTimeout(context.Background(), w.config.GetMLTimeout())
	defer cancel()

	// 尝试调用 ML 服务的健康检查端点
	req, err := http.NewRequestWithContext(ctx, "GET", w.config.ML.Endpoint+"/api/v1/health", nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ML service returned HTTP %d", resp.StatusCode)
	}

	return nil
}

// Run 运行 Worker
func (w *PeopleWorker) Run() error {
	// 设置信号处理
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 启动信号处理 goroutine
	go w.handleSignals(sigCh)

	// 获取运行时租约
	if err := w.acquireRuntimeLease(); err != nil {
		return fmt.Errorf("acquire runtime lease: %w", err)
	}
	defer w.releaseRuntimeLease()

	// 启动运行时心跳
	go w.runtimeHeartbeatLoop()

	// 启动任务获取循环
	go w.fetchLoop()

	// 启动处理循环
	for i := 0; i < w.config.PeopleWorker.Workers; i++ {
		w.wg.Add(1)
		go w.processLoop(i)
	}

	// 等待停止信号
	<-w.stopCh

	logger.Info("Stopping worker...")
	w.cancel()
	w.wg.Wait()

	logger.Infof("Worker stopped. Processed: %d, Failed: %d, Faces detected: %d",
		w.tasksProcessed, w.tasksFailed, w.facesDetected)

	return nil
}

// Stop 停止 Worker
func (w *PeopleWorker) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopCh)
	})
}

// handleSignals 处理系统信号
func (w *PeopleWorker) handleSignals(sigCh chan os.Signal) {
	sig := <-sigCh
	logger.Infof("Received signal: %v", sig)
	w.Stop()
}

// acquireRuntimeLease 获取运行时租约
func (w *PeopleWorker) acquireRuntimeLease() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lease, err := w.apiClient.AcquireRuntime(ctx)
	if err != nil {
		return err
	}

	w.runtimeLeaseAcquired = true
	logger.Infof("Acquired runtime lease (expires at: %v)", lease.LeaseExpiresAt)
	return nil
}

// releaseRuntimeLease 释放运行时租约
func (w *PeopleWorker) releaseRuntimeLease() {
	if !w.runtimeLeaseAcquired {
		return
	}

	close(w.runtimeHeartbeatStop)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := w.apiClient.ReleaseRuntime(ctx); err != nil {
		logger.Warnf("Failed to release runtime lease: %v", err)
	} else {
		logger.Info("Released runtime lease")
	}

	w.runtimeLeaseAcquired = false
}

// runtimeHeartbeatLoop 运行时心跳循环
func (w *PeopleWorker) runtimeHeartbeatLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.runtimeHeartbeatStop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := w.apiClient.HeartbeatRuntime(ctx)
			cancel()
			if err != nil {
				if client.IsPeopleRuntimeConflict(err) {
					w.handleRuntimeLeaseLost(err)
					return
				}
				logger.Warnf("Runtime heartbeat failed: %v", err)
			}
		}
	}
}

// fetchLoop 任务获取循环
func (w *PeopleWorker) fetchLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			if w.runtimeLeaseLost.Load() {
				return
			}
			// 当队列较小时获取新任务
			if w.taskManager.QueueSize() < w.config.PeopleWorker.FetchLimit {
				ctx, cancel := context.WithTimeout(w.ctx, 30*time.Second)
				_, err := w.taskManager.FetchTasks(ctx, w.config.PeopleWorker.FetchLimit)
				cancel()
				if err != nil {
					if client.IsPeopleRuntimeConflict(err) {
						w.handleRuntimeLeaseLost(err)
						return
					}
					logger.Warnf("Failed to fetch tasks: %v", err)
				}
			}
		}
	}
}

// processLoop 处理循环
func (w *PeopleWorker) processLoop(workerID int) {
	defer w.wg.Done()

	logger.Infof("Worker %d started", workerID)

	for {
		select {
		case <-w.ctx.Done():
			logger.Infof("Worker %d stopped", workerID)
			return
		default:
		}

		// 获取任务
		task := w.taskManager.GetTask()
		if task == nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		// 处理任务
		w.inFlightTasks.Add(1)
		if err := w.processTask(task); err != nil {
			logger.Errorf("Failed to process task %d: %v", task.ID, err)
			w.tasksFailed++
		} else {
			w.tasksProcessed++
		}
		w.inFlightTasks.Add(-1)
		w.maybeStopAfterDrain()
	}
}

func (w *PeopleWorker) handleRuntimeLeaseLost(err error) {
	if !w.runtimeLeaseLost.CompareAndSwap(false, true) {
		return
	}

	w.runtimeLeaseAcquired = false
	logger.Warnf("People runtime lease lost, entering drain mode: %v", err)
	w.maybeStopAfterDrain()
}

func (w *PeopleWorker) maybeStopAfterDrain() {
	if !w.runtimeLeaseLost.Load() {
		return
	}

	queueSize := 0
	if w.taskManager != nil {
		queueSize = w.taskManager.QueueSize()
	}
	if queueSize > 0 || w.inFlightTasks.Load() > 0 {
		return
	}

	logger.Info("People runtime lease lost and local queue drained, stopping worker")
	w.Stop()
}

// processTask 处理单个任务
func (w *PeopleWorker) processTask(task *client.Task) error {
	logger.Infof("Processing task %d (photo %d)", task.ID, task.PhotoID)

	// 下载照片
	localPath, err := w.downloader.Download(task.DownloadURL, w.config.Server.APIKey)
	if err != nil {
		w.taskManager.ReleaseTask(w.ctx, task, "download_failed", true)
		return fmt.Errorf("download photo: %w", err)
	}
	defer w.downloader.Cleanup(localPath)

	// 处理图片
	processedImage, err := w.imageProcessor.ProcessForAI(localPath)
	if err != nil {
		// 处理失败，尝试使用原图
		logger.Warnf("Failed to process image, using original: %v", err)
		processedImage, err = os.ReadFile(localPath)
		if err != nil {
			w.taskManager.ReleaseTask(w.ctx, task, "read_failed", true)
			return fmt.Errorf("read image: %w", err)
		}
	}

	// 调用 ML 服务进行人脸检测
	faces, processingTime, err := w.detectFaces(processedImage, localPath)
	if err != nil {
		w.taskManager.ReleaseTask(w.ctx, task, "detection_failed", true)
		return fmt.Errorf("detect faces: %w", err)
	}

	logger.Infof("Task %d: detected %d faces in %dms", task.ID, len(faces), processingTime)

	// 构建结果
	result := model.PeopleDetectionResult{
		PhotoID:          task.PhotoID,
		TaskID:           task.ID,
		Faces:            faces,
		ProcessingTimeMS: processingTime,
	}

	// 提交结果
	if err := w.taskManager.CompleteTask(w.ctx, task, &result); err != nil {
		return fmt.Errorf("submit result: %w", err)
	}

	w.facesDetected += int64(len(faces))
	return nil
}

// detectFaces 调用人脸检测服务
func (w *PeopleWorker) detectFaces(imageData []byte, imagePath string) ([]model.PeopleDetectionFace, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), w.config.GetMLTimeout())
	defer cancel()

	// 编码图片为 base64
	imageBase64 := base64.StdEncoding.EncodeToString(imageData)

	// 构建请求
	reqBody := map[string]interface{}{
		"image_base64":   imageBase64,
		"image_path":     imagePath,
		"min_confidence": 0.5,
		"max_faces":      20,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal request: %w", err)
	}

	// 发送请求到 ML 服务
	url := w.config.ML.Endpoint + "/api/v1/detect-faces"
	logger.Debugf("Calling ML service: %s", url)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	processingTime := int(time.Since(start).Milliseconds())

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("ML service returned HTTP %d", resp.StatusCode)
	}

	// 解析响应
	var result struct {
		Faces            []model.PeopleDetectionFace `json:"faces"`
		ProcessingTimeMS int                         `json:"processing_time_ms"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, 0, fmt.Errorf("decode response: %w", err)
	}

	// 如果 ML 服务返回了处理时间，使用它
	if result.ProcessingTimeMS > 0 {
		processingTime = result.ProcessingTimeMS
	}

	return result.Faces, processingTime, nil
}
