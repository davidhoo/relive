package model

import "time"

// PeopleWorkerTask 人物 worker 任务响应
type PeopleWorkerTask struct {
	ID            uint       `json:"id"`
	JobID         uint       `json:"job_id"`
	PhotoID       uint       `json:"photo_id"`
	FilePath      string     `json:"file_path"`
	DownloadURL   string     `json:"download_url"`
	Width         int        `json:"width"`
	Height        int        `json:"height"`
	LockExpiresAt *time.Time `json:"lock_expires_at,omitempty"`
}

// PeopleWorkerTasksResponse 任务列表响应
type PeopleWorkerTasksResponse struct {
	Tasks []PeopleWorkerTask `json:"tasks"`
}

// PeopleWorkerHeartbeatRequest 任务心跳请求
type PeopleWorkerHeartbeatRequest struct {
	Progress      int    `json:"progress"`
	StatusMessage string `json:"status_message"`
}

// PeopleWorkerHeartbeatResponse 任务心跳响应
type PeopleWorkerHeartbeatResponse struct {
	LockExpiresAt time.Time `json:"lock_expires_at"`
}

// PeopleWorkerReleaseTaskRequest 释放任务请求
type PeopleWorkerReleaseTaskRequest struct {
	Reason     string `json:"reason"`
	RetryLater bool   `json:"retry_later"`
}

// PeopleDetectionFace 人脸检测结果（对齐 mlclient.DetectedFace）
type PeopleDetectionFace struct {
	BBox         BoundingBox `json:"bbox"`
	Confidence   float64     `json:"confidence"`
	QualityScore float64     `json:"quality_score"`
	Embedding    []float32   `json:"embedding"`
}

// BoundingBox 人脸边界框
type BoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// PeopleDetectionResult 单张照片的检测结果
type PeopleDetectionResult struct {
	PhotoID          uint                  `json:"photo_id"`
	TaskID           uint                  `json:"task_id"`
	Faces            []PeopleDetectionFace `json:"faces"`
	ProcessingTimeMS int                   `json:"processing_time_ms"`
}

// PeopleWorkerSubmitResultsRequest 提交结果请求
type PeopleWorkerSubmitResultsRequest struct {
	Results []PeopleDetectionResult `json:"results"`
}

// PeopleWorkerSubmitResultsResponse 提交结果响应
type PeopleWorkerSubmitResultsResponse struct {
	Processed int      `json:"processed"`
	Errors    []string `json:"errors,omitempty"`
}

// PeopleWorkerRuntimeLeaseRequest 运行时租约请求
type PeopleWorkerRuntimeLeaseRequest struct {
	WorkerID string `json:"worker_id"`
}

// PeopleWorkerRuntimeLeaseResponse 运行时租约响应
type PeopleWorkerRuntimeLeaseResponse struct {
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
}
