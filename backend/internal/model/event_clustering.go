package model

import "time"

// 聚类任务状态常量（复用 ScanJob 状态）
const (
	ClusteringTaskTypeIncremental = "incremental"
	ClusteringTaskTypeRebuild     = "rebuild"
)

// EventClusteringConfig 聚类参数
type EventClusteringConfig struct {
	TimeGapSameEvent   float64 // 同一事件的最大时间间隔（小时），默认 6
	TimeGapNewEvent    float64 // 强制新事件的时间间隔（小时），默认 24
	DistanceForceSplit float64 // 强制切分的 GPS 距离（公里），默认 50
	MinPhotosPerEvent  int     // 最少照片数才创建事件，默认 3；不足的照片保持 event_id=NULL 由 hidden_gem 兜底
}

// DefaultEventClusteringConfig 返回默认聚类参数
func DefaultEventClusteringConfig() EventClusteringConfig {
	return EventClusteringConfig{
		TimeGapSameEvent:   6,
		TimeGapNewEvent:    24,
		DistanceForceSplit: 50,
		MinPhotosPerEvent:  3,
	}
}

// EventClusteringProgress 聚类进度
type EventClusteringProgress struct {
	Phase           string `json:"phase"`            // discovering / clustering / profiling / completed
	TotalPhotos     int    `json:"total_photos"`
	ProcessedPhotos int    `json:"processed_photos"`
	EventsCreated   int    `json:"events_created"`
	EventsUpdated   int    `json:"events_updated"`
	PhotosSkipped   int    `json:"photos_skipped"`   // 因簇太小而跳过的照片数
}

// EventClusteringTask 聚类任务 DTO（返回给前端）
type EventClusteringTask struct {
	ID              string                   `json:"id"`
	Type            string                   `json:"type"`   // incremental / rebuild
	Status          string                   `json:"status"` // running / stopping / stopped / completed / failed
	Progress        *EventClusteringProgress `json:"progress,omitempty"`
	ErrorMessage    string                   `json:"error_message,omitempty"`
	StartedAt       time.Time                `json:"started_at"`
	CompletedAt     *time.Time               `json:"completed_at,omitempty"`
	StopRequestedAt *time.Time               `json:"stop_requested_at,omitempty"`
}

// IsRunning 检查聚类任务是否运行中
func (t *EventClusteringTask) IsRunning() bool {
	return t.Status == ScanJobStatusRunning || t.Status == ScanJobStatusStopping
}
