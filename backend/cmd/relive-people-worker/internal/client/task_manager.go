package client

import (
	"context"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
)

// Task 本地任务队列中的任务
type Task struct {
	model.PeopleWorkerTask
	HeartbeatStop chan struct{}
}

// TaskManager 本地任务队列管理器
type TaskManager struct {
	client     *APIClient
	tasks      []*Task
	mutex      sync.Mutex
	heartbeatInterval time.Duration
}

// NewTaskManager 创建任务管理器
func NewTaskManager(client *APIClient) *TaskManager {
	return &TaskManager{
		client:            client,
		tasks:             make([]*Task, 0),
		heartbeatInterval: 30 * time.Second,
	}
}

// FetchTasks 从服务器获取任务
func (tm *TaskManager) FetchTasks(ctx context.Context, limit int) ([]*Task, error) {
	resp, err := tm.client.GetTasks(ctx, limit)
	if err != nil {
		return nil, err
	}

	tasks := make([]*Task, 0, len(resp.Tasks))
	for _, t := range resp.Tasks {
		task := &Task{
			PeopleWorkerTask: t,
			HeartbeatStop:    make(chan struct{}),
		}
		tasks = append(tasks, task)
	}

	tm.mutex.Lock()
	tm.tasks = append(tm.tasks, tasks...)
	tm.mutex.Unlock()

	// 为每个任务启动心跳
	for _, task := range tasks {
		go tm.heartbeatLoop(task)
	}

	return tasks, nil
}

// GetTask 获取一个待处理任务
func (tm *TaskManager) GetTask() *Task {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	if len(tm.tasks) == 0 {
		return nil
	}

	task := tm.tasks[0]
	tm.tasks = tm.tasks[1:]
	return task
}

// ReleaseTask 释放任务
func (tm *TaskManager) ReleaseTask(ctx context.Context, task *Task, reason string, retryLater bool) error {
	// 停止心跳
	close(task.HeartbeatStop)

	return tm.client.ReleaseTask(ctx, task.ID, reason, retryLater)
}

// CompleteTask 完成任务
func (tm *TaskManager) CompleteTask(ctx context.Context, task *Task, result *model.PeopleDetectionResult) error {
	// 停止心跳
	close(task.HeartbeatStop)

	_, err := tm.client.SubmitResults(ctx, []model.PeopleDetectionResult{*result})
	return err
}

// heartbeatLoop 任务心跳循环
func (tm *TaskManager) heartbeatLoop(task *Task) {
	ticker := time.NewTicker(tm.heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-task.HeartbeatStop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := tm.client.HeartbeatTask(ctx, task.ID, 50, "processing")
			cancel()
			if err != nil {
				// 心跳失败，记录日志但继续尝试
				// 服务器会在租约过期后回收任务
			}
		}
	}
}

// QueueSize 获取队列大小
func (tm *TaskManager) QueueSize() int {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	return len(tm.tasks)
}

// Clear 清空队列并停止所有心跳
func (tm *TaskManager) Clear() {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	for _, task := range tm.tasks {
		close(task.HeartbeatStop)
	}
	tm.tasks = tm.tasks[:0]
}
