package client

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
)

// TaskManager 任务管理器
type TaskManager struct {
	client     *APIClient
	analyzerID string

	// 任务缓存
	tasks      []model.AnalysisTask
	taskMutex  sync.RWMutex

	// 心跳管理
	heartbeats map[string]*HeartbeatManager // taskID -> manager
	hbMutex    sync.RWMutex

	// 配置
	batchSize int
}

// HeartbeatManager 单个任务的心跳管理器
type HeartbeatManager struct {
	taskID       string
	analyzerID   string
	client       *APIClient
	lockExpiry   time.Time
	stopCh       chan struct{}
	wg           sync.WaitGroup
	lastProgress int
	status       string
	mu           sync.RWMutex
}

// NewTaskManager 创建任务管理器
func NewTaskManager(client *APIClient, analyzerID string, batchSize int) *TaskManager {
	if batchSize <= 0 {
		batchSize = 10
	}
	if batchSize > 50 {
		batchSize = 50
	}

	return &TaskManager{
		client:     client,
		analyzerID: analyzerID,
		tasks:      make([]model.AnalysisTask, 0),
		heartbeats: make(map[string]*HeartbeatManager),
		batchSize:  batchSize,
	}
}

// FetchTasks 获取新任务，limit 指定本次获取数量
func (tm *TaskManager) FetchTasks(ctx context.Context, limit int) ([]model.AnalysisTask, error) {
	if limit <= 0 {
		limit = tm.batchSize
	}
	resp, err := tm.client.GetTasks(ctx, limit, tm.analyzerID)
	if err != nil {
		return nil, fmt.Errorf("fetch tasks: %w", err)
	}

	if len(resp.Tasks) == 0 {
		return nil, fmt.Errorf("no tasks available")
	}

	tm.taskMutex.Lock()
	tm.tasks = append(tm.tasks, resp.Tasks...)
	tm.taskMutex.Unlock()

	logger.Infof("Fetched %d tasks (%d remaining in queue)", len(resp.Tasks), resp.TotalRemaining)

	// 心跳在 processLoop 中按需启动，避免对即将跳过的任务创建无用 goroutine

	return resp.Tasks, nil
}

// GetNextTask 获取下一个任务
func (tm *TaskManager) GetNextTask() (*model.AnalysisTask, bool) {
	tm.taskMutex.Lock()
	defer tm.taskMutex.Unlock()

	if len(tm.tasks) == 0 {
		return nil, false
	}

	task := tm.tasks[0]
	tm.tasks = tm.tasks[1:]

	// 返回副本的指针
	taskCopy := task
	return &taskCopy, true
}

// TaskCount 获取剩余任务数
func (tm *TaskManager) TaskCount() int {
	tm.taskMutex.RLock()
	defer tm.taskMutex.RUnlock()
	return len(tm.tasks)
}

// StartHeartbeat 启动任务心跳
func (tm *TaskManager) StartHeartbeat(taskID string, lockExpiry *time.Time) {
	if lockExpiry == nil {
		return
	}

	tm.hbMutex.Lock()
	defer tm.hbMutex.Unlock()

	// 如果已存在，先停止
	if oldManager, exists := tm.heartbeats[taskID]; exists {
		oldManager.Stop()
	}

	manager := &HeartbeatManager{
		taskID:     taskID,
		analyzerID: tm.analyzerID,
		client:     tm.client,
		lockExpiry: *lockExpiry,
		stopCh:     make(chan struct{}),
	}

	tm.heartbeats[taskID] = manager
	manager.Start()

	logger.Debugf("Started heartbeat for task %s (expires at %s)", taskID, lockExpiry.Format("15:04:05"))
}

// StopHeartbeat 停止任务心跳
func (tm *TaskManager) StopHeartbeat(taskID string) {
	tm.hbMutex.Lock()
	defer tm.hbMutex.Unlock()

	if manager, exists := tm.heartbeats[taskID]; exists {
		manager.Stop()
		delete(tm.heartbeats, taskID)
		logger.Debugf("Stopped heartbeat for task %s", taskID)
	}
}

// UpdateHeartbeatProgress 更新心跳进度
func (tm *TaskManager) UpdateHeartbeatProgress(taskID string, progress int, status string) {
	tm.hbMutex.RLock()
	defer tm.hbMutex.RUnlock()

	if manager, exists := tm.heartbeats[taskID]; exists {
		manager.UpdateProgress(progress, status)
	}
}

// StopAllHeartbeats 停止所有心跳
func (tm *TaskManager) StopAllHeartbeats() {
	tm.hbMutex.Lock()
	defer tm.hbMutex.Unlock()

	for taskID, manager := range tm.heartbeats {
		manager.Stop()
		delete(tm.heartbeats, taskID)
	}

	logger.Info("Stopped all heartbeats")
}

// ReleaseTask 释放任务
func (tm *TaskManager) ReleaseTask(ctx context.Context, taskID, reason, errorMsg string, retryLater bool) error {
	// 停止心跳
	tm.StopHeartbeat(taskID)

	// 调用 API 释放任务
	if err := tm.client.ReleaseTask(ctx, taskID, tm.analyzerID, reason, errorMsg, retryLater); err != nil {
		return fmt.Errorf("release task: %w", err)
	}

	logger.Infof("Released task %s (reason: %s)", taskID, reason)
	return nil
}

// Start 启动心跳管理器
func (hb *HeartbeatManager) Start() {
	hb.wg.Add(1)
	go hb.run()
}

// Stop 停止心跳管理器
func (hb *HeartbeatManager) Stop() {
	close(hb.stopCh)
	hb.wg.Wait()
}

// UpdateProgress 更新进度
func (hb *HeartbeatManager) UpdateProgress(progress int, status string) {
	hb.mu.Lock()
	defer hb.mu.Unlock()
	hb.lastProgress = progress
	if status != "" {
		hb.status = status
	}
}

// run 心跳循环
func (hb *HeartbeatManager) run() {
	defer hb.wg.Done()

	// 计算首次发送时间（锁过期前30秒）
	hb.mu.RLock()
	lockExpiry := hb.lockExpiry
	hb.mu.RUnlock()

	// 如果锁已经过期，立即发送一次
	now := time.Now()
	var nextBeat time.Time
	if lockExpiry.After(now) {
		nextBeat = lockExpiry.Add(-30 * time.Second)
		if nextBeat.Before(now) {
			nextBeat = now
		}
	} else {
		nextBeat = now
	}

	timer := time.NewTimer(nextBeat.Sub(now))
	defer timer.Stop()

	for {
		select {
		case <-timer.C:
			// 发送心跳
			hb.mu.RLock()
			progress := hb.lastProgress
			status := hb.status
			hb.mu.RUnlock()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			resp, err := hb.client.Heartbeat(ctx, hb.taskID, hb.analyzerID, progress, status)
			cancel()

			if err != nil {
				logger.Warnf("Heartbeat failed for task %s: %v", hb.taskID, err)
				// 心跳失败，稍后重试
				timer.Reset(30 * time.Second)
				continue
			}

			// 更新锁过期时间
			hb.mu.Lock()
			hb.lockExpiry = resp.LockExpiresAt
			hb.mu.Unlock()

			logger.Debugf("Heartbeat sent for task %s (new expiry: %s)", hb.taskID, resp.LockExpiresAt.Format("15:04:05"))

			// 设置下次心跳时间（新过期时间前30秒）
			nextBeat = resp.LockExpiresAt.Add(-30 * time.Second)
			if nextBeat.Before(time.Now()) {
				nextBeat = time.Now().Add(30 * time.Second)
			}
			timer.Reset(time.Until(nextBeat))

		case <-hb.stopCh:
			return
		}
	}
}

// GetStats 获取统计信息
func (tm *TaskManager) GetStats(ctx context.Context) (*model.AnalyzerStatsResponse, error) {
	return tm.client.GetStats(ctx)
}

// CheckHealth 检查服务健康
func (tm *TaskManager) CheckHealth(ctx context.Context) error {
	return tm.client.CheckHealth(ctx)
}
