# WriteQueue 写入序列化方案 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 解决 SQLite 并发写入导致的 "database is locked" 问题，通过单写者模式序列化所有写操作，同时保留读连接池的并发能力。

**Architecture:** 读写分离——API 查询走 GORM 读连接池（4 connections），所有写操作通过 WriteQueue 序列化执行（1 dedicated connection）。WriteQueue 用 sync.Mutex 保证同一时刻只有一个写操作，Execute() 方法支持关键路径同步写入，Enqueue() 方法支持后台异步写入（channel + 单 goroutine 消费）。

**Tech Stack:** Go, GORM, SQLite (WAL mode), sync.Mutex, channel

---

## 设计概览

```
API Handler SELECT ──→ Read Pool (4 conns, GORM) ──→ SQLite WAL
                                                       ↕ (concurrent reads)
Background services ──→ WriteQueue.Enqueue() ──→ channel ──→ Writer goroutine
                                                              ↓
                                                       Write Conn (1) ──→ SQLite WAL
                                                              ↑
Critical path writes ──→ WriteQueue.Execute() ──→ writeMu.Lock ──┘
```

核心原则：
- **读并发不减**：API 查询走独立读池，不被写入阻塞
- **写完全串行**：所有写操作通过 WriteQueue，同一时刻只有一个写入者
- **关键路径无延迟**：Execute() 直接执行，不经过 channel
- **后台写入可批量**：Enqueue() 攒批后统一事务提交（减少 fsync）

---

## Task 1: 实现 WriteQueue 核心

**Files:**
- Create: `backend/pkg/database/write_queue.go`
- Test: `backend/pkg/database/write_queue_test.go`

**Step 1: 编写 WriteQueue 测试**

```go
// write_queue_test.go
package database

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteQueue_Execute_SerializesWrites(t *testing.T) {
	wq := NewWriteQueue(nil) // 测试模式，不需要真实 DB
	defer wq.Stop()

	var maxConcurrent int32
	var current int32

	// 模拟 10 个并发写入
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			wq.Execute(func() error {
				n := atomic.AddInt32(&current, 1)
				if n > atomic.LoadInt32(&maxConcurrent) {
					atomic.StoreInt32(&maxConcurrent, n)
				}
				time.Sleep(10 * time.Millisecond)
				atomic.AddInt32(&current, -1)
				return nil
			})
			done <- struct{}{}
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	if maxConcurrent != 1 {
		t.Errorf("expected max concurrent = 1, got %d", maxConcurrent)
	}
}

func TestWriteQueue_Enqueue_BatchFlush(t *testing.T) {
	wq := NewWriteQueue(&WriteQueueConfig{
		BatchSize:     3,
		FlushInterval: 100 * time.Millisecond,
	})
	defer wq.Stop()

	var flushCount int32
	wq.SetBatchFlushFn(func(ops []WriteOp) error {
		atomic.AddInt32(&flushCount, 1)
		return nil
	})

	// 入队 3 个，应触发一次批量 flush
	for i := 0; i < 3; i++ {
		wq.Enqueue(func() error { return nil })
	}

	time.Sleep(200 * time.Millisecond)
	if atomic.LoadInt32(&flushCount) != 1 {
		t.Errorf("expected 1 batch flush, got %d", atomic.LoadInt32(&flushCount))
	}
}

func TestWriteQueue_Enqueue_TimeFlush(t *testing.T) {
	wq := NewWriteQueue(&WriteQueueConfig{
		BatchSize:     100,
		FlushInterval: 50 * time.Millisecond,
	})
	defer wq.Stop()

	var flushCount int32
	wq.SetBatchFlushFn(func(ops []WriteOp) error {
		atomic.AddInt32(&flushCount, 1)
		return nil
	})

	// 入队 1 个（不够 batchSize），应等超时后 flush
	wq.Enqueue(func() error { return nil })

	time.Sleep(150 * time.Millisecond)
	if atomic.LoadInt32(&flushCount) < 1 {
		t.Errorf("expected at least 1 time-based flush, got %d", atomic.LoadInt32(&flushCount))
	}
}

func TestWriteQueue_Stats(t *testing.T) {
	wq := NewWriteQueue(nil)
	defer wq.Stop()

	wq.Execute(func() error { return nil })
	wq.Execute(func() error { return nil })

	stats := wq.Stats()
	if stats["executed"] != uint64(2) {
		t.Errorf("expected executed=2, got %v", stats["executed"])
	}
}
```

**Step 2: 运行测试确认失败**

Run: `cd backend && go test -run TestWriteQueue -v ./pkg/database/`
Expected: FAIL (WriteQueue 未定义)

**Step 3: 实现 WriteQueue**

```go
// write_queue.go
package database

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// WriteOp 表示一个可批量执行的写操作
type WriteOp struct {
	Fn func() error
}

// BatchFlushFn 批量 flush 函数类型
type BatchFlushFn func(ops []WriteOp) error

// WriteQueueConfig 配置
type WriteQueueConfig struct {
	BatchSize     int           // 批量 flush 阈值
	FlushInterval time.Duration // 定时 flush 间隔
}

// WriteQueue 序列化所有数据库写操作
// - Execute(): 关键路径写入，同步执行，调用方等待结果
// - Enqueue(): 后台写入，异步入队，攒批后统一提交
type WriteQueue struct {
	writeMu sync.Mutex // 保证同一时刻只有一个写操作

	// 批量写入
	queue        chan WriteOp
	batchSize    int
	flushInterval time.Duration
	batchFlushFn BatchFlushFn // 注入的批量执行函数
	stopCh       chan struct{}
	wg           sync.WaitGroup

	// 统计
	executedCount uint64
	enqueuedCount uint64
	batchCount    uint64
}

// 全局单例
var globalWriteQueue *WriteQueue

// InitWriteQueue 初始化全局 WriteQueue
func InitWriteQueue() *WriteQueue {
	globalWriteQueue = NewWriteQueue(&WriteQueueConfig{
		BatchSize:     50,
		FlushInterval: 5 * time.Second,
	})
	return globalWriteQueue
}

// GetWriteQueue 返回全局 WriteQueue
func GetWriteQueue() *WriteQueue {
	return globalWriteQueue
}

// NewWriteQueue 创建 WriteQueue
func NewWriteQueue(cfg *WriteQueueConfig) *WriteQueue {
	if cfg == nil {
		cfg = &WriteQueueConfig{
			BatchSize:     50,
			FlushInterval: 5 * time.Second,
		}
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = 5 * time.Second
	}

	wq := &WriteQueue{
		queue:         make(chan WriteOp, cfg.BatchSize*4),
		batchSize:     cfg.BatchSize,
		flushInterval: cfg.FlushInterval,
		stopCh:        make(chan struct{}),
	}

	// 启动批量消费 goroutine
	wq.wg.Add(1)
	go wq.runBatchWriter()

	return wq
}

// SetBatchFlushFn 注入批量执行函数（用于测试和初始化）
func (wq *WriteQueue) SetBatchFlushFn(fn BatchFlushFn) {
	wq.batchFlushFn = fn
}

// Execute 同步执行写操作（关键路径）
// 调用方阻塞直到操作完成，保证同一时刻只有一个写操作
func (wq *WriteQueue) Execute(fn func() error) error {
	wq.writeMu.Lock()
	defer wq.writeMu.Unlock()

	atomic.AddUint64(&wq.executedCount, 1)
	return fn()
}

// Enqueue 异步入队写操作（后台路径）
// 操作通过 channel 传递给批量写入 goroutine
func (wq *WriteQueue) Enqueue(fn func() error) {
	atomic.AddUint64(&wq.enqueuedCount, 1)
	wq.queue <- WriteOp{Fn: fn}
}

// runBatchWriter 批量消费 goroutine
func (wq *WriteQueue) runBatchWriter() {
	defer wq.wg.Done()

	ticker := time.NewTicker(wq.flushInterval)
	defer ticker.Stop()

	batch := make([]WriteOp, 0, wq.batchSize)

	for {
		select {
		case op := <-wq.queue:
			batch = append(batch, op)
			// 攒够一批或 channel 已空时 flush
			if len(batch) >= wq.batchSize {
				wq.flushBatch(batch)
				batch = batch[:0]
				ticker.Reset(wq.flushInterval)
			} else {
				// 尝试继续取，直到 channel 为空
				draining := true
				for draining && len(batch) < wq.batchSize {
					select {
					case op := <-wq.queue:
						batch = append(batch, op)
					default:
						draining = false
					}
				}
				if len(batch) >= wq.batchSize {
					wq.flushBatch(batch)
					batch = batch[:0]
					ticker.Reset(wq.flushInterval)
				}
			}

		case <-ticker.C:
			if len(batch) > 0 {
				wq.flushBatch(batch)
				batch = batch[:0]
			}

		case <-wq.stopCh:
			// drain remaining
			for {
				select {
				case op := <-wq.queue:
					batch = append(batch, op)
				default:
					if len(batch) > 0 {
						wq.flushBatch(batch)
					}
					return
				}
			}
		}
	}
}

// flushBatch 执行批量写入
func (wq *WriteQueue) flushBatch(ops []WriteOp) {
	if len(ops) == 0 {
		return
	}

	wq.writeMu.Lock()
	defer wq.writeMu.Unlock()

	atomic.AddUint64(&wq.batchCount, 1)

	if wq.batchFlushFn != nil {
		if err := wq.batchFlushFn(ops); err != nil {
			logger.Errorf("[WriteQueue] batch flush failed: %v", err)
		}
	} else {
		// 默认：逐个执行
		for _, op := range ops {
			if err := op.Fn(); err != nil {
				logger.Errorf("[WriteQueue] batched write failed: %v", err)
			}
		}
	}
}

// Stop 停止 WriteQueue
func (wq *WriteQueue) Stop() {
	close(wq.stopCh)
	wq.wg.Wait()
}

// Stats 返回统计信息
func (wq *WriteQueue) Stats() map[string]interface{} {
	return map[string]interface{}{
		"executed":    atomic.LoadUint64(&wq.executedCount),
		"enqueued":    atomic.LoadUint64(&wq.enqueuedCount),
		"batches":     atomic.LoadUint64(&wq.batchCount),
		"queue_depth": len(wq.queue),
	}
}
```

**Step 4: 运行测试确认通过**

Run: `cd backend && go test -run TestWriteQueue -v ./pkg/database/`
Expected: PASS

**Step 5: Commit**

```bash
git add backend/pkg/database/write_queue.go backend/pkg/database/write_queue_test.go
git commit -m "feat(database): add WriteQueue for write serialization"
```

---

## Task 2: Init 中创建独立写连接 + 注入 WriteQueue

**Files:**
- Modify: `backend/pkg/database/database.go:24-97` (Init 函数)

**Step 1: 修改 Init 函数，创建独立写连接**

在 `Init()` 中，读连接池创建之后、AutoMigrate 之前，创建写连接并初始化 WriteQueue：

```go
// 在 database.go 的 Init 函数中，sqlDB.SetConnMaxLifetime 之后添加：

		// 创建独立写连接（单连接，串行写入，避免 SQLite 写锁竞争）
		writePath := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=60000&_synchronous=NORMAL&_cache_size=-64000&_temp_store=memory",
			cfg.Path)
		writeDB, wErr := gorm.Open(sqlite.Open(writePath), gormConfig)
		if wErr != nil {
			return nil, fmt.Errorf("open write connection: %w", wErr)
		}
		writeSQL, wErr := writeDB.DB()
		if wErr != nil {
			return nil, wErr
		}
		writeDB.Exec("PRAGMA foreign_keys=ON")
		writeSQL.SetMaxOpenConns(1)
		writeSQL.SetMaxIdleConns(1)
		writeSQL.SetConnMaxLifetime(time.Hour)

		// 初始化 WriteQueue
		wq := InitWriteQueue()
		// 批量写入函数：在单连接事务中执行所有操作
		wq.SetBatchFlushFn(func(ops []WriteOp) error {
			return writeDB.Transaction(func(tx *gorm.DB) error {
				for _, op := range ops {
					if err := op.Fn(); err != nil {
						return err
					}
				}
				return nil
			})
		})
```

**Step 2: 在合适位置暴露 writeDB（供后续 Task 使用）**

在 database.go 中添加全局变量和 getter：

```go
var globalWriteDB *gorm.DB

// GetWriteDB returns the dedicated write connection (single connection, serialized access)
func GetWriteDB() *gorm.DB {
	return globalWriteDB
}
```

在 Init 中赋值：`globalWriteDB = writeDB`

**Step 3: 运行现有测试确认不破坏任何东西**

Run: `cd backend && go test -v ./... -count=1 -short`
Expected: 所有现有测试通过（新增代码不影响现有行为）

**Step 4: Commit**

```bash
git add backend/pkg/database/database.go
git commit -m "feat(database): initialize WriteQueue with dedicated write connection in Init"
```

---

## Task 3: 迁移 ThumbnailService 写入到 WriteQueue

**Files:**
- Modify: `backend/internal/service/thumbnail_service.go`
  - `processJob()` (line 407-450)
  - `updatePhotoWithRetry()` (line 452-469) → 改为用 WriteQueue.Execute
  - `updateJobWithRetry()` (line 471-487) → 改为用 WriteQueue.Execute
  - 删除 `isSQLiteLockError()` (line 489-498)

**Step 1: 在 thumbnailService 结构体中注入 WriteQueue**

在 `thumbnailService` 结构体中添加字段：
```go
writeQueue *database.WriteQueue
```

在构造函数 `NewThumbnailService` 中初始化：
```go
writeQueue: database.GetWriteQueue(),
```

**Step 2: 改写 updatePhotoWithRetry 和 updateJobWithRetry**

将两个 retry 函数改为通过 WriteQueue.Execute 执行，不再需要手动重试（WriteQueue 保证串行，不会有锁竞争）：

```go
// updatePhotoWithRetry 通过 WriteQueue 序列化写入
func (s *thumbnailService) updatePhotoWithRetry(photoID uint, updates map[string]interface{}) error {
	if s.writeQueue == nil {
		return s.photoRepo.UpdateFields(photoID, updates)
	}
	return s.writeQueue.Execute(func() error {
		return s.photoRepo.UpdateFields(photoID, updates)
	})
}

// updateJobWithRetry 通过 WriteQueue 序列化写入
func (s *thumbnailService) updateJobWithRetry(jobID uint, updates map[string]interface{}) error {
	if s.writeQueue == nil {
		return s.jobRepo.UpdateFields(jobID, updates)
	}
	return s.writeQueue.Execute(func() error {
		return s.jobRepo.UpdateFields(jobID, updates)
	})
}
```

**Step 3: 删除 isSQLiteLockError**

不再需要，WriteQueue 串行化保证无锁竞争。

**Step 4: 移除 processJob 中不再需要的 sleep**

在 `processJob` 中，原来在 line 359 的 `time.Sleep(50 * time.Millisecond)` 和 line 400 的 `time.Sleep(100 * time.Millisecond)` 是为减少锁竞争加的，现在可以移除（或保留为节流，视需求）。

**Step 5: 运行缩略图相关测试**

Run: `cd backend && go test -v ./internal/service/ -run TestThumbnail`
Expected: PASS

**Step 6: Commit**

```bash
git add backend/internal/service/thumbnail_service.go
git commit -m "refactor(thumbnail): use WriteQueue for DB writes, remove retry logic"
```

---

## Task 4: 迁移 GeocodeTaskService 写入到 WriteQueue

**Files:**
- Modify: `backend/internal/service/geocode_task_service.go`
  - `updatePhotoWithRetry()` → 改为用 WriteQueue.Execute
  - `updateJobWithRetry()` → 改为用 WriteQueue.Execute

**Step 1: 在 geocodeTaskService 中注入 WriteQueue**

同 Task 3 的模式。

**Step 2: 改写两个 retry 函数**

```go
func (s *geocodeTaskService) updatePhotoWithRetry(photoID uint, updates map[string]interface{}) error {
	if s.writeQueue == nil {
		return s.photoRepo.UpdateFields(photoID, updates)
	}
	return s.writeQueue.Execute(func() error {
		return s.photoRepo.UpdateFields(photoID, updates)
	})
}

func (s *geocodeTaskService) updateJobWithRetry(jobID uint, updates map[string]interface{}) error {
	if s.writeQueue == nil {
		return s.jobRepo.UpdateFields(jobID, updates)
	}
	return s.writeQueue.Execute(func() error {
		return s.jobRepo.UpdateFields(jobID, updates)
	})
}
```

**Step 3: 运行测试**

Run: `cd backend && go test -v ./internal/service/ -run TestGeocode`
Expected: PASS

**Step 4: Commit**

```bash
git add backend/internal/service/geocode_task_service.go
git commit -m "refactor(geocode): use WriteQueue for DB writes, remove retry logic"
```

---

## Task 5: 迁移 PeopleService 写入到 WriteQueue

**Files:**
- Modify: `backend/internal/service/people_service.go`
  - `ApplyDetectionResult` 中的事务写入 (line ~1179-1200)
  - `runIncrementalClustering` 中的批量更新 (line ~1115-1179)
  - `MergePeople` / `SplitPerson` / `MoveFaces` / `DissolvePerson` 中的事务写入

**Step 1: 在 peopleService 中注入 WriteQueue**

**Step 2: 将 ApplyDetectionResult 中的事务改为通过 WriteQueue.Execute**

```go
err := s.writeQueue.Execute(func() error {
    return s.db.Transaction(func(tx *gorm.DB) error {
        // 原有事务内容
    })
})
```

**Step 3: 将聚类相关的写入改为通过 WriteQueue.Execute**

**Step 4: 保留 writeGate（RWMutex）用于保护人脸/人物数据的一致性**

writeGate 和 WriteQueue 职责不同：
- writeGate: 保护业务逻辑互斥（前台 merge/split vs 后台聚类）
- WriteQueue: 保护 SQLite 写锁串行化

两者需要同时保留：先获取 writeGate，再通过 WriteQueue.Execute 写入。

**Step 5: 运行测试**

Run: `cd backend && go test -v ./internal/service/ -run TestPeople`
Expected: PASS

**Step 6: Commit**

```bash
git add backend/internal/service/people_service.go
git commit -m "refactor(people): use WriteQueue for DB writes"
```

---

## Task 6: 迁移 Scheduler 任务写入到 WriteQueue

**Files:**
- Modify: `backend/internal/service/scheduler.go`
  - `cleanExpiredLocksTask`
  - `ensureDailyBatchTask`
  - `cleanTerminalJobsTask`

**Step 1: 在 TaskScheduler 中注入 WriteQueue**

**Step 2: 将 scheduler 中的写操作改为通过 WriteQueue.Execute**

Scheduler 任务频率低、写入量小，但也是并发写入的来源之一。

**Step 3: 运行测试**

**Step 4: Commit**

```bash
git add backend/internal/service/scheduler.go
git commit -m "refactor(scheduler): use WriteQueue for background task writes"
```

---

## Task 7: 迁移其他零散写入到 WriteQueue

**Files:**
- Modify: `backend/internal/service/analysis_service.go` (SubmitResultsDirectly)
- Modify: `backend/internal/service/display_daily_service.go` (GenerateDailyBatch)
- Modify: `backend/internal/service/analysis_runtime_service.go` (Acquire, Heartbeat)
- Modify: `backend/internal/service/result_queue.go` (batchWrite 中的写入)
- Modify: `backend/internal/service/photo_service.go` (async goroutine 写入)

**Step 1: 逐个服务注入 WriteQueue 并改写写入路径**

**Step 2: 特别注意 ResultQueue 的 batchWrite**

ResultQueue 的 batchWrite 调用 AnalysisService.SubmitResultsDirectly，这个函数本身就是一个事务。改为通过 WriteQueue.Execute 包装：

```go
func (p *BatchProcessor) executeBatchUpdate(...) error {
	return p.writeQueue.Execute(func() error {
		// 原有 SubmitResultsDirectly 逻辑
	})
}
```

**Step 3: 运行全量测试**

Run: `cd backend && go test -v ./... -count=1`
Expected: PASS

**Step 4: Commit**

```bash
git add backend/internal/service/
git commit -m "refactor: migrate remaining services to WriteQueue for DB writes"
```

---

## Task 8: 清理 NewBackgroundDB 和连接池调整

**Files:**
- Modify: `backend/pkg/database/database.go` — 考虑是否保留 NewBackgroundDB
- Modify: `backend/internal/service/service.go` — 移除 mergeSuggestionService 的独立 bgDB

**Step 1: 评估 NewBackgroundDB 是否还需要**

personMergeSuggestionService 使用 bgDB 做 HNSW 索引构建等 CPU 密集操作。这些操作主要是读取 + 计算，写入量不大。如果所有写入都走 WriteQueue，bgDB 可以简化为只提供读连接。

**Step 2: 调整读连接池大小**

当前 API 池 4 连接 + background 池 2 连接 = 6 连接。WriteQueue 写连接 1 个。
总计 7 连接。可以考虑：
- API 池保持 4（足够 API 并发读）
- background 池可以去掉或减到 1
- WriteQueue 写连接 1（固定）

**Step 3: 运行全量测试**

**Step 4: Commit**

```bash
git add backend/pkg/database/database.go backend/internal/service/service.go
git commit -m "refactor(database): simplify connection pools after WriteQueue migration"
```

---

## Task 9: 集成测试和压测验证

**Files:**
- Create: `backend/pkg/database/write_queue_integration_test.go`

**Step 1: 编写集成测试模拟并发写入场景**

```go
func TestWriteQueue_ConcurrentWrites_NoLockErrors(t *testing.T) {
	// 初始化测试数据库
	// 启动 N 个 goroutine 并发写入
	// 验证零 "database is locked" 错误
}
```

**Step 2: 手动测试：启动服务，同时触发缩略图 + 地理编码 + AI 分析**

**Step 3: 观察日志确认无锁错误**

**Step 4: Commit**

```bash
git add backend/pkg/database/write_queue_integration_test.go
git commit -m "test: add integration test for WriteQueue concurrent write safety"
```

---

## 注意事项

1. **渐进式迁移**：每个 Task 独立可测，可逐个提交验证
2. **向后兼容**：WriteQueue 为 nil 时 fallback 到直接写入，不影响现有行为
3. **writeGate 保留**：people_service 的 writeGate 是业务层互斥，与 WriteQueue 的存储层串行化职责不同
4. **FTS5 触发器**：SQLite 触发器在写连接中自动执行，无需特殊处理
5. **事务嵌套**：WriteQueue.Execute 内部可以调用 db.Transaction()，GORM 会正确处理
