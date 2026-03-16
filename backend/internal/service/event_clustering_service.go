package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// EventClusteringService 事件聚类服务接口
type EventClusteringService interface {
	StartClustering() (*model.EventClusteringTask, error)
	StartRebuild() (*model.EventClusteringTask, error)
	StopTask() error
	GetTask() *model.EventClusteringTask
	RunIncremental()
}

type eventClusteringService struct {
	db           *gorm.DB
	photoRepo    repository.PhotoRepository
	eventRepo    repository.EventRepository
	photoTagRepo repository.PhotoTagRepository
	config       model.EventClusteringConfig

	activeTask *clusteringRuntime
	taskMutex  sync.RWMutex
}

// clusteringRuntime 运行中的聚类任务
type clusteringRuntime struct {
	id              string
	taskType        string
	ctx             context.Context
	cancel          context.CancelFunc
	startedAt       time.Time
	completedAt     *time.Time
	status          string
	stopRequestedAt *time.Time
	errorMessage    string
	progress        model.EventClusteringProgress
	mu              sync.Mutex
}

// photoCluster 聚类中间结果
type photoCluster struct {
	photos []*model.Photo
}

// NewEventClusteringService 创建事件聚类服务
func NewEventClusteringService(db *gorm.DB, photoRepo repository.PhotoRepository, eventRepo repository.EventRepository, photoTagRepo repository.PhotoTagRepository) EventClusteringService {
	return &eventClusteringService{
		db:           db,
		photoRepo:    photoRepo,
		eventRepo:    eventRepo,
		photoTagRepo: photoTagRepo,
		config:       model.DefaultEventClusteringConfig(),
	}
}

// StartClustering 启动增量聚类任务
func (s *eventClusteringService) StartClustering() (*model.EventClusteringTask, error) {
	return s.startTask(model.ClusteringTaskTypeIncremental)
}

// StartRebuild 启动全量重建任务
func (s *eventClusteringService) StartRebuild() (*model.EventClusteringTask, error) {
	return s.startTask(model.ClusteringTaskTypeRebuild)
}

func (s *eventClusteringService) startTask(taskType string) (*model.EventClusteringTask, error) {
	s.taskMutex.Lock()
	defer s.taskMutex.Unlock()

	if s.activeTask != nil && (s.activeTask.status == model.ScanJobStatusRunning || s.activeTask.status == model.ScanJobStatusStopping) {
		return nil, fmt.Errorf("聚类任务正在运行中")
	}

	ctx, cancel := context.WithCancel(context.Background())
	runtime := &clusteringRuntime{
		id:        uuid.New().String()[:8],
		taskType:  taskType,
		ctx:       ctx,
		cancel:    cancel,
		startedAt: time.Now(),
		status:    model.ScanJobStatusRunning,
	}
	s.activeTask = runtime

	go s.runTask(runtime)

	return s.buildTaskDTO(runtime), nil
}

// StopTask 停止当前聚类任务
func (s *eventClusteringService) StopTask() error {
	s.taskMutex.RLock()
	task := s.activeTask
	s.taskMutex.RUnlock()

	if task == nil || task.status != model.ScanJobStatusRunning {
		return fmt.Errorf("没有正在运行的聚类任务")
	}

	task.mu.Lock()
	now := time.Now()
	task.status = model.ScanJobStatusStopping
	task.stopRequestedAt = &now
	task.mu.Unlock()
	task.cancel()

	return nil
}

// GetTask 获取当前任务状态
func (s *eventClusteringService) GetTask() *model.EventClusteringTask {
	s.taskMutex.RLock()
	defer s.taskMutex.RUnlock()

	if s.activeTask == nil {
		return nil
	}
	return s.buildTaskDTO(s.activeTask)
}

// RunIncremental 执行增量聚类（同步，扫描完成后调用）
func (s *eventClusteringService) RunIncremental() {
	s.taskMutex.RLock()
	active := s.activeTask
	s.taskMutex.RUnlock()

	// 如果已有任务在跑，跳过
	if active != nil && (active.status == model.ScanJobStatusRunning || active.status == model.ScanJobStatusStopping) {
		logger.Infof("[EventClustering] Skipping incremental: task already running")
		return
	}

	logger.Infof("[EventClustering] Running incremental clustering after scan")
	if err := s.runIncremental(context.Background(), nil); err != nil {
		logger.Warnf("[EventClustering] Incremental clustering failed: %v", err)
	}
}

func (s *eventClusteringService) runTask(runtime *clusteringRuntime) {
	var err error
	switch runtime.taskType {
	case model.ClusteringTaskTypeRebuild:
		err = s.runRebuild(runtime.ctx, runtime)
	case model.ClusteringTaskTypeIncremental:
		err = s.runIncremental(runtime.ctx, runtime)
	}

	runtime.mu.Lock()
	now := time.Now()
	runtime.completedAt = &now
	if err != nil {
		if runtime.status == model.ScanJobStatusStopping {
			runtime.status = model.ScanJobStatusStopped
		} else {
			runtime.status = model.ScanJobStatusFailed
			runtime.errorMessage = err.Error()
		}
	} else {
		if runtime.status == model.ScanJobStatusStopping {
			runtime.status = model.ScanJobStatusStopped
		} else {
			runtime.status = model.ScanJobStatusCompleted
			runtime.progress.Phase = "completed"
		}
	}
	runtime.mu.Unlock()

	logger.Infof("[EventClustering] Task %s (%s) finished: status=%s, created=%d, updated=%d, skipped_photos=%d",
		runtime.id, runtime.taskType, runtime.status,
		runtime.progress.EventsCreated, runtime.progress.EventsUpdated, runtime.progress.PhotosSkipped)
}

// runRebuild 全量重建
func (s *eventClusteringService) runRebuild(ctx context.Context, runtime *clusteringRuntime) error {
	setPhase := func(phase string) {
		if runtime != nil {
			runtime.mu.Lock()
			runtime.progress.Phase = phase
			runtime.mu.Unlock()
		}
	}

	// 1. 清空所有事件和照片的 event_id
	setPhase("discovering")
	logger.Infof("[EventClustering] Rebuild: clearing all events and photo event_ids")

	if err := s.eventRepo.DeleteAll(); err != nil {
		return fmt.Errorf("delete all events: %w", err)
	}
	if err := s.db.Model(&model.Photo{}).Where("event_id IS NOT NULL").Update("event_id", nil).Error; err != nil {
		return fmt.Errorf("clear photo event_ids: %w", err)
	}

	// 2. 查询所有有 taken_at 且 active 的照片
	var photos []*model.Photo
	if err := s.db.Where("taken_at IS NOT NULL AND status = ?", model.PhotoStatusActive).
		Order("taken_at ASC").Find(&photos).Error; err != nil {
		return fmt.Errorf("query photos: %w", err)
	}

	if len(photos) == 0 {
		logger.Infof("[EventClustering] Rebuild: no photos with taken_at found")
		return nil
	}

	if runtime != nil {
		runtime.mu.Lock()
		runtime.progress.TotalPhotos = len(photos)
		runtime.mu.Unlock()
	}

	// 检查取消
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// 3. 聚类
	setPhase("clustering")
	clusters := s.clusterPhotos(photos)

	// 4. 创建事件 + 画像
	setPhase("profiling")
	for _, cluster := range clusters {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 簇照片数不足，跳过（照片保持 event_id=NULL，由 hidden_gem 兜底）
		if s.config.MinPhotosPerEvent > 0 && len(cluster.photos) < s.config.MinPhotosPerEvent {
			if runtime != nil {
				runtime.mu.Lock()
				runtime.progress.PhotosSkipped += len(cluster.photos)
				runtime.progress.ProcessedPhotos += len(cluster.photos)
				runtime.mu.Unlock()
			}
			continue
		}

		event, err := s.createEventFromCluster(cluster)
		if err != nil {
			logger.Warnf("[EventClustering] Failed to create event: %v", err)
			continue
		}

		// 更新照片的 event_id
		photoIDs := make([]uint, len(cluster.photos))
		for i, p := range cluster.photos {
			photoIDs[i] = p.ID
		}
		if err := s.db.Model(&model.Photo{}).Where("id IN ?", photoIDs).Update("event_id", event.ID).Error; err != nil {
			logger.Warnf("[EventClustering] Failed to update photo event_ids: %v", err)
		}

		if runtime != nil {
			runtime.mu.Lock()
			runtime.progress.EventsCreated++
			runtime.progress.ProcessedPhotos += len(cluster.photos)
			runtime.mu.Unlock()
		}
	}

	return nil
}

// runIncremental 增量聚类
func (s *eventClusteringService) runIncremental(ctx context.Context, runtime *clusteringRuntime) error {
	setPhase := func(phase string) {
		if runtime != nil {
			runtime.mu.Lock()
			runtime.progress.Phase = phase
			runtime.mu.Unlock()
		}
	}

	// 1. 查询未聚类的照片
	setPhase("discovering")
	var photos []*model.Photo
	if err := s.db.Where("event_id IS NULL AND taken_at IS NOT NULL AND status = ?", model.PhotoStatusActive).
		Order("taken_at ASC").Find(&photos).Error; err != nil {
		return fmt.Errorf("query unclustered photos: %w", err)
	}

	if len(photos) == 0 {
		logger.Infof("[EventClustering] Incremental: no unclustered photos found")
		return nil
	}

	logger.Infof("[EventClustering] Incremental: found %d unclustered photos", len(photos))

	if runtime != nil {
		runtime.mu.Lock()
		runtime.progress.TotalPhotos = len(photos)
		runtime.mu.Unlock()
	}

	// 2. 聚类
	setPhase("clustering")
	clusters := s.clusterPhotos(photos)

	// 3. 尝试归入已有事件或创建新事件
	setPhase("profiling")
	for _, cluster := range clusters {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		clusterStart := *cluster.photos[0].TakenAt
		clusterEnd := *cluster.photos[len(cluster.photos)-1].TakenAt
		windowPadding := time.Duration(s.config.TimeGapSameEvent * float64(time.Hour))

		// 查找时间窗口内的已有事件
		existingEvents, err := s.eventRepo.GetByTimeRange(
			clusterStart.Add(-windowPadding),
			clusterEnd.Add(windowPadding),
		)
		if err != nil {
			logger.Warnf("[EventClustering] Failed to query existing events: %v", err)
			existingEvents = nil
		}

		photoIDs := make([]uint, len(cluster.photos))
		for i, p := range cluster.photos {
			photoIDs[i] = p.ID
		}

		if len(existingEvents) > 0 {
			// 归入最近的已有事件
			bestEvent := s.findBestMatchingEvent(cluster, existingEvents)
			if bestEvent != nil {
				// 更新照片的 event_id
				if err := s.db.Model(&model.Photo{}).Where("id IN ?", photoIDs).Update("event_id", bestEvent.ID).Error; err != nil {
					logger.Warnf("[EventClustering] Failed to update photo event_ids: %v", err)
					continue
				}
				// 重新画像
				if err := s.reprofileEvent(bestEvent.ID); err != nil {
					logger.Warnf("[EventClustering] Failed to reprofile event %d: %v", bestEvent.ID, err)
				}
				if runtime != nil {
					runtime.mu.Lock()
					runtime.progress.EventsUpdated++
					runtime.progress.ProcessedPhotos += len(cluster.photos)
					runtime.mu.Unlock()
				}
				continue
			}
		}

		// 创建新事件（需满足最小照片数）
		if s.config.MinPhotosPerEvent > 0 && len(cluster.photos) < s.config.MinPhotosPerEvent {
			// 簇太小，跳过（照片保持 event_id=NULL，由 hidden_gem 兜底）
			if runtime != nil {
				runtime.mu.Lock()
				runtime.progress.PhotosSkipped += len(cluster.photos)
				runtime.progress.ProcessedPhotos += len(cluster.photos)
				runtime.mu.Unlock()
			}
			continue
		}

		event, err := s.createEventFromCluster(cluster)
		if err != nil {
			logger.Warnf("[EventClustering] Failed to create event: %v", err)
			continue
		}

		if err := s.db.Model(&model.Photo{}).Where("id IN ?", photoIDs).Update("event_id", event.ID).Error; err != nil {
			logger.Warnf("[EventClustering] Failed to update photo event_ids: %v", err)
		}

		if runtime != nil {
			runtime.mu.Lock()
			runtime.progress.EventsCreated++
			runtime.progress.ProcessedPhotos += len(cluster.photos)
			runtime.mu.Unlock()
		}
	}

	return nil
}

// clusterPhotos 按时空连续性聚类照片（照片必须已按 taken_at ASC 排序）
func (s *eventClusteringService) clusterPhotos(photos []*model.Photo) []photoCluster {
	if len(photos) == 0 {
		return nil
	}

	var clusters []photoCluster
	current := photoCluster{photos: []*model.Photo{photos[0]}}

	for i := 1; i < len(photos); i++ {
		prev := photos[i-1]
		curr := photos[i]
		timeDelta := curr.TakenAt.Sub(*prev.TakenAt).Hours()

		shouldSplit := false

		if timeDelta >= s.config.TimeGapNewEvent {
			// > 24h: 必切分
			shouldSplit = true
		} else if timeDelta >= s.config.TimeGapSameEvent {
			// 6h~24h 灰色地带：看 GPS 距离
			if prev.HasGPS() && curr.HasGPS() {
				dist := haversineDistance(*prev.GPSLatitude, *prev.GPSLongitude, *curr.GPSLatitude, *curr.GPSLongitude)
				if dist > s.config.DistanceForceSplit {
					shouldSplit = true
				}
			}
			// 没有 GPS 数据时，灰色地带默认不切分（保守策略）
		} else if timeDelta < s.config.TimeGapSameEvent {
			// < 6h: GPS > 50km 也切分
			if prev.HasGPS() && curr.HasGPS() {
				dist := haversineDistance(*prev.GPSLatitude, *prev.GPSLongitude, *curr.GPSLatitude, *curr.GPSLongitude)
				if dist > s.config.DistanceForceSplit {
					shouldSplit = true
				}
			}
		}

		if shouldSplit {
			clusters = append(clusters, current)
			current = photoCluster{photos: []*model.Photo{curr}}
		} else {
			current.photos = append(current.photos, curr)
		}
	}
	clusters = append(clusters, current)

	return clusters
}

// createEventFromCluster 从聚类创建事件并画像
func (s *eventClusteringService) createEventFromCluster(cluster photoCluster) (*model.Event, error) {
	photos := cluster.photos
	startTime := *photos[0].TakenAt
	endTime := *photos[len(photos)-1].TakenAt
	durationHours := endTime.Sub(startTime).Hours()

	event := &model.Event{
		StartTime:     startTime,
		EndTime:       endTime,
		DurationHours: durationHours,
		PhotoCount:    len(photos),
	}

	// 画像
	s.profileEvent(event, photos)

	if err := s.eventRepo.Create(event); err != nil {
		return nil, err
	}
	return event, nil
}

// profileEvent 计算事件画像
func (s *eventClusteringService) profileEvent(event *model.Event, photos []*model.Photo) {
	if len(photos) == 0 {
		return
	}

	// cover_photo_id = beauty_score 最高
	var bestPhoto *model.Photo
	for _, p := range photos {
		if bestPhoto == nil || p.BeautyScore > bestPhoto.BeautyScore {
			bestPhoto = p
		}
	}
	if bestPhoto != nil {
		event.CoverPhotoID = &bestPhoto.ID
	}

	// primary_category = 最频繁 main_category
	categoryCounts := make(map[string]int)
	for _, p := range photos {
		if p.MainCategory != "" {
			categoryCounts[p.MainCategory]++
		}
	}
	event.PrimaryCategory = mostFrequent(categoryCounts)

	// primary_tag = 最频繁标签（从 photo_tags 聚合）
	photoIDs := make([]uint, len(photos))
	for i, p := range photos {
		photoIDs[i] = p.ID
	}
	event.PrimaryTag = s.getMostFrequentTag(photoIDs)

	// location = 最频繁 photo.location
	locationCounts := make(map[string]int)
	for _, p := range photos {
		if p.Location != "" {
			locationCounts[p.Location]++
		}
	}
	event.Location = mostFrequent(locationCounts)

	// GPS = 簇内有效坐标均值
	var latSum, lngSum float64
	var gpsCount int
	for _, p := range photos {
		if p.HasGPS() {
			latSum += *p.GPSLatitude
			lngSum += *p.GPSLongitude
			gpsCount++
		}
	}
	if gpsCount > 0 {
		lat := latSum / float64(gpsCount)
		lng := lngSum / float64(gpsCount)
		event.GPSLatitude = &lat
		event.GPSLongitude = &lng
	}

	// event_score = avg(overall_score) * log2(photo_count + 1)
	var scoreSum float64
	for _, p := range photos {
		scoreSum += float64(p.OverallScore)
	}
	avgScore := scoreSum / float64(len(photos))
	event.EventScore = avgScore * math.Log2(float64(len(photos)+1))
}

// reprofileEvent 重新画像已有事件
func (s *eventClusteringService) reprofileEvent(eventID uint) error {
	event, err := s.eventRepo.GetByID(eventID)
	if err != nil {
		return err
	}

	// 查询事件内所有照片
	var photos []*model.Photo
	if err := s.db.Where("event_id = ? AND status = ?", eventID, model.PhotoStatusActive).
		Order("taken_at ASC").Find(&photos).Error; err != nil {
		return err
	}

	if len(photos) == 0 {
		return nil
	}

	// 更新时间范围
	event.StartTime = *photos[0].TakenAt
	event.EndTime = *photos[len(photos)-1].TakenAt
	event.DurationHours = event.EndTime.Sub(event.StartTime).Hours()
	event.PhotoCount = len(photos)

	// 重新画像
	s.profileEvent(event, photos)

	return s.eventRepo.Update(event)
}

// findBestMatchingEvent 在已有事件中找到最佳匹配（时间重叠最大的）
func (s *eventClusteringService) findBestMatchingEvent(cluster photoCluster, events []*model.Event) *model.Event {
	clusterStart := *cluster.photos[0].TakenAt
	clusterEnd := *cluster.photos[len(cluster.photos)-1].TakenAt
	clusterMid := clusterStart.Add(clusterEnd.Sub(clusterStart) / 2)

	var best *model.Event
	var bestDist time.Duration = math.MaxInt64

	for _, e := range events {
		eventMid := e.StartTime.Add(e.EndTime.Sub(e.StartTime) / 2)
		dist := absDuration(clusterMid.Sub(eventMid))
		if dist < bestDist {
			bestDist = dist
			best = e
		}
	}

	return best
}

// getMostFrequentTag 从 photo_tags 表获取照片集合的最频繁标签
func (s *eventClusteringService) getMostFrequentTag(photoIDs []uint) string {
	if len(photoIDs) == 0 {
		return ""
	}

	type tagCount struct {
		Tag   string
		Count int
	}
	var results []tagCount
	s.db.Table("photo_tags").
		Select("tag, COUNT(*) as count").
		Where("photo_id IN ?", photoIDs).
		Group("tag").
		Order("count DESC").
		Limit(1).
		Scan(&results)

	if len(results) > 0 {
		return results[0].Tag
	}
	return ""
}

// buildTaskDTO 构建任务 DTO
func (s *eventClusteringService) buildTaskDTO(runtime *clusteringRuntime) *model.EventClusteringTask {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()

	task := &model.EventClusteringTask{
		ID:              runtime.id,
		Type:            runtime.taskType,
		Status:          runtime.status,
		StartedAt:       runtime.startedAt,
		CompletedAt:     runtime.completedAt,
		StopRequestedAt: runtime.stopRequestedAt,
		ErrorMessage:    runtime.errorMessage,
		Progress:        &runtime.progress,
	}
	return task
}

// --- 纯函数工具 ---

// haversineDistance 计算两点间的 Haversine 距离（公里）
func haversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0

	dLat := degreesToRadians(lat2 - lat1)
	dLon := degreesToRadians(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(degreesToRadians(lat1))*math.Cos(degreesToRadians(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)

	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

func degreesToRadians(deg float64) float64 {
	return deg * math.Pi / 180
}

// mostFrequent 返回 map 中出现次数最多的 key
func mostFrequent(counts map[string]int) string {
	var best string
	var bestCount int
	// 按 key 排序保证确定性
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if counts[k] > bestCount {
			bestCount = counts[k]
			best = k
		}
	}
	return best
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
