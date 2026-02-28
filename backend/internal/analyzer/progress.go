package analyzer

import (
	"fmt"
	"sync"
	"time"
)

// ProgressTracker tracks and displays analysis progress
type ProgressTracker struct {
	mu sync.Mutex

	total     int       // Total photos to process
	current   int       // Current progress
	startTime time.Time // Start time

	// For terminal display
	lastPrint time.Time // Last print time
	printRate time.Duration // Minimum duration between prints
}

// NewProgressTracker creates a new progress tracker
func NewProgressTracker(total int) *ProgressTracker {
	return &ProgressTracker{
		total:     total,
		current:   0,
		startTime: time.Now(),
		lastPrint: time.Now(),
		printRate: 100 * time.Millisecond, // Update at most 10 times per second
	}
}

// Increment increments the progress counter and prints progress
func (p *ProgressTracker) Increment() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.current++

	// Rate-limit printing (unless it's the last one)
	now := time.Now()
	if p.current == p.total || now.Sub(p.lastPrint) >= p.printRate {
		p.printProgressLocked()
		p.lastPrint = now
	}
}

// Print forces a progress print (thread-safe)
func (p *ProgressTracker) Print() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.printProgressLocked()
}

// printProgressLocked prints progress (must be called with lock held)
func (p *ProgressTracker) printProgressLocked() {
	if p.total == 0 {
		return
	}

	percentage := float64(p.current) / float64(p.total) * 100
	elapsed := time.Since(p.startTime)

	// Calculate ETA
	var eta time.Duration
	if p.current > 0 {
		avgTime := elapsed / time.Duration(p.current)
		remaining := p.total - p.current
		eta = avgTime * time.Duration(remaining)
	}

	// Build progress bar
	barWidth := 30
	filled := int(float64(barWidth) * percentage / 100)
	bar := ""
	for i := 0; i < barWidth; i++ {
		if i < filled {
			bar += "="
		} else if i == filled {
			bar += ">"
		} else {
			bar += " "
		}
	}

	// Print progress (use \r to overwrite previous line)
	fmt.Printf("\r[%s] %d/%d (%.1f%%) | Elapsed: %s | ETA: %s",
		bar, p.current, p.total, percentage,
		formatDuration(elapsed), formatDuration(eta))

	// Add newline when complete
	if p.current == p.total {
		fmt.Println()
	}
}

// GetCurrent returns current progress
func (p *ProgressTracker) GetCurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

// GetTotal returns total count
func (p *ProgressTracker) GetTotal() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total
}

// GetPercentage returns completion percentage
func (p *ProgressTracker) GetPercentage() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.total == 0 {
		return 0
	}
	return float64(p.current) / float64(p.total) * 100
}
