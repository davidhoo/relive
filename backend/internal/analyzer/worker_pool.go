package analyzer

import (
	"context"
	"sync"
)

// Task represents a work task
type Task func(ctx context.Context) error

// WorkerPool manages a pool of worker goroutines
type WorkerPool struct {
	workers   int                // Number of worker goroutines
	taskQueue chan Task          // Task queue
	wg        sync.WaitGroup     // Wait group for workers
	ctx       context.Context    // Context for cancellation
	cancel    context.CancelFunc // Cancel function
	errors    chan error         // Error channel
}

// NewWorkerPool creates a new worker pool
func NewWorkerPool(workers int) *WorkerPool {
	if workers <= 0 {
		workers = 1
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &WorkerPool{
		workers:   workers,
		taskQueue: make(chan Task, workers*2), // Buffer for smoother operation
		ctx:       ctx,
		cancel:    cancel,
		errors:    make(chan error, workers),
	}
}

// Start starts the worker pool
func (wp *WorkerPool) Start() {
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go wp.worker()
	}
}

// worker is the worker goroutine
func (wp *WorkerPool) worker() {
	defer wp.wg.Done()

	for {
		select {
		case <-wp.ctx.Done():
			// Context cancelled, exit
			return

		case task, ok := <-wp.taskQueue:
			if !ok {
				// Channel closed, exit
				return
			}

			// Execute task
			if err := task(wp.ctx); err != nil {
				// Send error (non-blocking)
				select {
				case wp.errors <- err:
				default:
					// Error channel full, skip
				}
			}
		}
	}
}

// Submit submits a task to the worker pool
// Returns error if pool is stopped or context is cancelled
func (wp *WorkerPool) Submit(task Task) error {
	select {
	case <-wp.ctx.Done():
		return wp.ctx.Err()
	case wp.taskQueue <- task:
		return nil
	}
}

// Stop stops the worker pool and waits for all workers to finish
func (wp *WorkerPool) Stop() {
	// Close task queue (no more tasks accepted)
	close(wp.taskQueue)

	// Wait for all workers to finish
	wp.wg.Wait()

	// Close error channel
	close(wp.errors)
}

// Cancel cancels the context, causing all workers to stop
func (wp *WorkerPool) Cancel() {
	wp.cancel()
}

// Errors returns the error channel
func (wp *WorkerPool) Errors() <-chan error {
	return wp.errors
}

// Wait waits for all workers to finish and closes the pool
func (wp *WorkerPool) Wait() {
	wp.Stop()
}
