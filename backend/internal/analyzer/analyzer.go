package analyzer

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/provider"
	"github.com/davidhoo/relive/internal/util"
	"github.com/davidhoo/relive/pkg/logger"
	_ "github.com/mattn/go-sqlite3"
)

// AnalyzerConfig analyzer configuration
type AnalyzerConfig struct {
	// Database path
	DBPath string

	// Worker count (0 = auto based on provider)
	Workers int

	// Retry settings
	RetryCount int
	RetryDelay time.Duration

	// Resume from where we left off
	Resume bool

	// Verbose logging
	Verbose bool
}

// Analyzer offline photo analyzer
type Analyzer struct {
	config         *AnalyzerConfig
	db             *sql.DB
	provider       provider.AIProvider
	imageProcessor *util.ImageProcessor
	workerPool     *WorkerPool
	progress       *ProgressTracker
	stats          *Stats
}

// NewAnalyzer creates a new analyzer instance
func NewAnalyzer(config *AnalyzerConfig, aiProvider provider.AIProvider) (*Analyzer, error) {
	// Open database
	db, err := sql.Open("sqlite3", config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	// Create image processor (1024px, 85% quality)
	imageProcessor := util.NewImageProcessor(1024, 85)

	// Determine worker count
	workers := config.Workers
	if workers <= 0 {
		workers = aiProvider.MaxConcurrency()
	}

	logger.Info(fmt.Sprintf("Analyzer initialized with %d workers", workers))

	return &Analyzer{
		config:         config,
		db:             db,
		provider:       aiProvider,
		imageProcessor: imageProcessor,
	}, nil
}

// Close closes the analyzer
func (a *Analyzer) Close() error {
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

// CheckStatus checks the database status
func (a *Analyzer) CheckStatus() error {
	logger.Info("Checking database status...")

	// Count total photos
	var total int
	err := a.db.QueryRow("SELECT COUNT(*) FROM photos").Scan(&total)
	if err != nil {
		return fmt.Errorf("count total photos: %w", err)
	}

	// Count analyzed photos
	var analyzed int
	err = a.db.QueryRow("SELECT COUNT(*) FROM photos WHERE ai_analyzed = 1").Scan(&analyzed)
	if err != nil {
		return fmt.Errorf("count analyzed photos: %w", err)
	}

	// Count unanalyzed photos
	unanalyzed := total - analyzed

	fmt.Println("\n" + Repeat("=", 50))
	fmt.Println("Database Status")
	fmt.Println(Repeat("=", 50))
	fmt.Printf("Total photos:      %d\n", total)
	fmt.Printf("Analyzed:          %d (%.1f%%)\n", analyzed, float64(analyzed)/float64(total)*100)
	fmt.Printf("Unanalyzed:        %d (%.1f%%)\n", unanalyzed, float64(unanalyzed)/float64(total)*100)
	fmt.Println(Repeat("=", 50))

	return nil
}

// EstimateCost estimates the analysis cost
func (a *Analyzer) EstimateCost() error {
	logger.Info("Estimating analysis cost...")

	// Count unanalyzed photos
	var unanalyzed int
	err := a.db.QueryRow("SELECT COUNT(*) FROM photos WHERE ai_analyzed = 0").Scan(&unanalyzed)
	if err != nil {
		return fmt.Errorf("count unanalyzed photos: %w", err)
	}

	if unanalyzed == 0 {
		fmt.Println("No photos to analyze.")
		return nil
	}

	// Get provider info
	providerName := a.provider.Name()
	costPerPhoto := a.provider.Cost()
	totalCost := float64(unanalyzed) * costPerPhoto

	// Estimate time (based on typical analysis time)
	avgTimePerPhoto := 5 * time.Second // Conservative estimate
	if providerName == "ollama" || providerName == "vllm" {
		avgTimePerPhoto = 10 * time.Second // Local models are slower
	}

	workers := a.config.Workers
	if workers <= 0 {
		workers = a.provider.MaxConcurrency()
	}

	totalTime := time.Duration(unanalyzed) * avgTimePerPhoto / time.Duration(workers)

	fmt.Println("\n" + Repeat("=", 50))
	fmt.Println("Cost Estimation")
	fmt.Println(Repeat("=", 50))
	fmt.Printf("Provider:          %s\n", providerName)
	fmt.Printf("Unanalyzed photos: %d\n", unanalyzed)
	fmt.Printf("Workers:           %d\n", workers)
	fmt.Println(Repeat("-", 50))
	fmt.Printf("Cost per photo:    ¥%.4f\n", costPerPhoto)
	fmt.Printf("Estimated total:   ¥%.2f\n", totalCost)
	fmt.Println(Repeat("-", 50))
	fmt.Printf("Est. time:         %s\n", formatDuration(totalTime))
	fmt.Println(Repeat("=", 50))
	fmt.Println("\nNote: This is a rough estimate. Actual cost and time may vary.")

	return nil
}

// Run runs the analysis process
func (a *Analyzer) Run(ctx context.Context) error {
	logger.Info("Starting analysis...")

	// Check provider availability
	if !a.provider.IsAvailable() {
		return fmt.Errorf("provider %s is not available", a.provider.Name())
	}

	// Load unanalyzed photos
	photos, err := a.loadUnanalyzedPhotos()
	if err != nil {
		return fmt.Errorf("load unanalyzed photos: %w", err)
	}

	if len(photos) == 0 {
		logger.Info("No photos to analyze.")
		return nil
	}

	logger.Info(fmt.Sprintf("Found %d photos to analyze", len(photos)))

	// Initialize components
	workers := a.config.Workers
	if workers <= 0 {
		workers = a.provider.MaxConcurrency()
	}

	a.workerPool = NewWorkerPool(workers)
	a.progress = NewProgressTracker(len(photos))
	a.stats = NewStats(len(photos))

	// Start worker pool
	a.workerPool.Start()

	// Submit tasks
	for _, photo := range photos {
		photo := photo // Capture loop variable
		task := func(ctx context.Context) error {
			return a.analyzePhotoWithRetry(ctx, &photo)
		}

		if err := a.workerPool.Submit(task); err != nil {
			logger.Error(fmt.Sprintf("Failed to submit task: %v", err))
			break
		}
	}

	// Wait for completion or cancellation
	done := make(chan struct{})
	go func() {
		a.workerPool.Wait()
		close(done)
	}()

	// Handle errors from worker pool
	go func() {
		for err := range a.workerPool.Errors() {
			if err != nil {
				logger.Error(fmt.Sprintf("Worker error: %v", err))
			}
		}
	}()

	// Wait for completion or context cancellation
	select {
	case <-done:
		logger.Info("Analysis completed")
	case <-ctx.Done():
		logger.Info("Analysis cancelled, stopping workers...")
		a.workerPool.Cancel()
		<-done
		logger.Info("Workers stopped")
	}

	// Print statistics
	a.stats.Print()

	return nil
}

// loadUnanalyzedPhotos loads unanalyzed photos from database
func (a *Analyzer) loadUnanalyzedPhotos() ([]model.Photo, error) {
	query := `
		SELECT id, file_path, file_name, file_hash, width, height,
		       taken_at, camera_model, location, gps_latitude, gps_longitude
		FROM photos
		WHERE ai_analyzed = 0
		ORDER BY id ASC
	`

	rows, err := a.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var photos []model.Photo
	for rows.Next() {
		var p model.Photo
		var takenAt sql.NullTime
		var gpsLat, gpsLon sql.NullFloat64

		err := rows.Scan(
			&p.ID, &p.FilePath, &p.FileName, &p.FileHash,
			&p.Width, &p.Height, &takenAt, &p.CameraModel,
			&p.Location, &gpsLat, &gpsLon,
		)
		if err != nil {
			return nil, err
		}

		if takenAt.Valid {
			p.TakenAt = &takenAt.Time
		}
		if gpsLat.Valid {
			p.GPSLatitude = &gpsLat.Float64
		}
		if gpsLon.Valid {
			p.GPSLongitude = &gpsLon.Float64
		}

		photos = append(photos, p)
	}

	return photos, rows.Err()
}

// analyzePhotoWithRetry analyzes a photo with retry logic
func (a *Analyzer) analyzePhotoWithRetry(ctx context.Context, photo *model.Photo) error {
	var lastErr error

	for attempt := 0; attempt <= a.config.RetryCount; attempt++ {
		if attempt > 0 {
			logger.Info(fmt.Sprintf("Retrying photo %d (attempt %d/%d)", photo.ID, attempt, a.config.RetryCount))

			// Wait before retry
			select {
			case <-time.After(a.config.RetryDelay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		err := a.analyzePhoto(ctx, photo)
		if err == nil {
			return nil // Success
		}

		lastErr = err
		logger.Error(fmt.Sprintf("Failed to analyze photo %d: %v", photo.ID, err))
	}

	// All attempts failed
	a.stats.RecordFailure(lastErr.Error())
	return lastErr
}

// analyzePhoto analyzes a single photo
func (a *Analyzer) analyzePhoto(ctx context.Context, photo *model.Photo) error {
	startTime := time.Now()

	// Check if file exists
	if _, err := os.Stat(photo.FilePath); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", photo.FilePath)
	}

	// Process image
	imageData, err := a.imageProcessor.ProcessForAI(photo.FilePath)
	if err != nil {
		return fmt.Errorf("process image: %w", err)
	}

	// Build analyze request
	request := &provider.AnalyzeRequest{
		ImageData: imageData,
		ImagePath: photo.FilePath,
		ExifInfo: &provider.ExifInfo{
			DateTime: "",
			City:     photo.Location,
			Model:    photo.CameraModel,
		},
	}

	if photo.TakenAt != nil {
		request.ExifInfo.DateTime = photo.TakenAt.Format("2006-01-02 15:04:05")
	}

	// Call AI provider
	result, err := a.provider.Analyze(request)
	if err != nil {
		return fmt.Errorf("analyze: %w", err)
	}

	// Save result to database
	if err := a.saveResult(photo.ID, result); err != nil {
		return fmt.Errorf("save result: %w", err)
	}

	// Update statistics
	duration := time.Since(startTime)
	a.stats.RecordSuccess(duration, result.Cost)
	a.progress.Increment()

	if a.config.Verbose {
		logger.Info(fmt.Sprintf("Analyzed photo %d: %s (%.2fs, ¥%.4f)",
			photo.ID, photo.FileName, duration.Seconds(), result.Cost))
	}

	return nil
}

// saveResult saves the analysis result to database
func (a *Analyzer) saveResult(photoID uint, result *provider.AnalyzeResult) error {
	// Calculate overall score (70% memory + 30% beauty)
	memoryScore := int(result.MemoryScore)
	beautyScore := int(result.BeautyScore)
	overallScore := int(float64(memoryScore)*0.7 + float64(beautyScore)*0.3)

	now := time.Now()

	query := `
		UPDATE photos
		SET ai_analyzed = 1,
		    analyzed_at = ?,
		    description = ?,
		    caption = ?,
		    memory_score = ?,
		    beauty_score = ?,
		    overall_score = ?,
		    main_category = ?,
		    tags = ?
		WHERE id = ?
	`

	_, err := a.db.Exec(query,
		now,
		result.Description,
		result.Caption,
		memoryScore,
		beautyScore,
		overallScore,
		result.MainCategory,
		result.Tags,
		photoID,
	)

	return err
}
