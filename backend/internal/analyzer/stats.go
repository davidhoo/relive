package analyzer

import (
	"fmt"
	"sync"
	"time"
)

// Stats tracks analysis statistics
type Stats struct {
	mu sync.Mutex

	total       int           // Total photos to analyze
	success     int           // Successfully analyzed
	failed      int           // Failed to analyze
	skipped     int           // Skipped (already analyzed)
	totalTime   time.Duration // Total time spent
	totalCost   float64       // Total cost (for paid APIs)
	startTime   time.Time     // Analysis start time

	failureReasons map[string]int // Track failure reasons
}

// NewStats creates a new Stats instance
func NewStats(total int) *Stats {
	return &Stats{
		total:          total,
		success:        0,
		failed:         0,
		skipped:        0,
		totalTime:      0,
		totalCost:      0,
		startTime:      time.Now(),
		failureReasons: make(map[string]int),
	}
}

// RecordSuccess records a successful analysis
func (s *Stats) RecordSuccess(duration time.Duration, cost float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.success++
	s.totalTime += duration
	s.totalCost += cost
}

// RecordFailure records a failed analysis
func (s *Stats) RecordFailure(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failed++
	s.failureReasons[reason]++
}

// RecordSkipped records a skipped photo
func (s *Stats) RecordSkipped() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.skipped++
}

// GetSuccess returns the success count
func (s *Stats) GetSuccess() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.success
}

// GetFailed returns the failed count
func (s *Stats) GetFailed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failed
}

// GetSkipped returns the skipped count
func (s *Stats) GetSkipped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped
}

// GetProcessed returns the total processed count (success + failed + skipped)
func (s *Stats) GetProcessed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.success + s.failed + s.skipped
}

// GetAverageTime returns the average time per successful analysis
func (s *Stats) GetAverageTime() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.success == 0 {
		return 0
	}
	return s.totalTime / time.Duration(s.success)
}

// GetTotalCost returns the total cost
func (s *Stats) GetTotalCost() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.totalCost
}

// GetElapsedTime returns the elapsed time since start
func (s *Stats) GetElapsedTime() time.Duration {
	return time.Since(s.startTime)
}

// Print prints the statistics to stdout
func (s *Stats) Print() {
	s.mu.Lock()
	defer s.mu.Unlock()

	elapsed := time.Since(s.startTime)

	fmt.Println("\n" + Repeat("=", 50))
	fmt.Println("Analysis Statistics")
	fmt.Println(Repeat("=", 50))
	fmt.Printf("Total:     %d\n", s.total)
	fmt.Printf("Success:   %d (%.1f%%)\n", s.success, float64(s.success)/float64(s.total)*100)
	fmt.Printf("Failed:    %d\n", s.failed)
	fmt.Printf("Skipped:   %d\n", s.skipped)
	fmt.Println(Repeat("─", 50))
	fmt.Printf("Elapsed:   %s\n", formatDuration(elapsed))

	if s.success > 0 {
		avgTime := s.totalTime / time.Duration(s.success)
		fmt.Printf("Avg Time:  %s per photo\n", formatDuration(avgTime))
	}

	if s.totalCost > 0 {
		fmt.Printf("Total Cost: ¥%.4f\n", s.totalCost)
		if s.success > 0 {
			avgCost := s.totalCost / float64(s.success)
			fmt.Printf("Avg Cost:   ¥%.4f per photo\n", avgCost)
		}
	}

	// Print failure reasons if any
	if len(s.failureReasons) > 0 {
		fmt.Println(Repeat("─", 50))
		fmt.Println("Failure Reasons:")
		for reason, count := range s.failureReasons {
			fmt.Printf("  - %s: %d\n", reason, count)
		}
	}

	fmt.Println(Repeat("=", 50))
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}

	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

// Repeat repeats a string n times
func Repeat(s string, count int) string {
	result := ""
	for i := 0; i < count; i++ {
		result += s
	}
	return result
}
