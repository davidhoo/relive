package model

import (
	"time"
)

// AnalysisTask 分析任务响应
type AnalysisTask struct {
	ID                    string     `json:"id"`                      // 任务唯一标识
	PhotoID               uint       `json:"photo_id"`                // 照片ID
	FilePath              string     `json:"file_path"`               // 文件路径
	DownloadURL           string     `json:"download_url"`            // 下载URL
	DownloadTokenExpiresAt *time.Time `json:"download_token_expires_at,omitempty"` // 下载token过期时间
	Width                 int        `json:"width"`                   // 宽度
	Height                int        `json:"height"`                  // 高度
	TakenAt               *time.Time `json:"taken_at,omitempty"`      // 拍摄时间
	Location              string     `json:"location,omitempty"`      // 位置
	CameraModel           string     `json:"camera_model,omitempty"`  // 相机型号
	LockExpiresAt         *time.Time `json:"lock_expires_at,omitempty"` // 锁过期时间
}

// AnalyzerTasksResponse 获取任务响应
type AnalyzerTasksResponse struct {
	Tasks          []AnalysisTask `json:"tasks"`           // 任务列表
	TotalRemaining int64          `json:"total_remaining"` // 剩余任务数
	LockDuration   int            `json:"lock_duration"`   // 锁定期（秒）
	AnalyzerID     string         `json:"analyzer_id"`     // 分析器实例ID
	APIKeyID       uint           `json:"api_key_id"`      // API Key ID
}

// HeartbeatRequest 心跳请求
type HeartbeatRequest struct {
	Progress int    `json:"progress,omitempty"` // 进度百分比（0-100）
	Status   string `json:"status,omitempty"`   // 当前状态：analyzing, downloading
}

// HeartbeatResponse 心跳响应
type HeartbeatResponse struct {
	LockExpiresAt time.Time `json:"lock_expires_at"` // 锁过期时间
	LockDuration  int       `json:"lock_duration"`   // 锁定期（秒）
}

// ReleaseTaskRequest 释放任务请求
type ReleaseTaskRequest struct {
	Reason    string `json:"reason" binding:"required"`    // 释放原因
	ErrorMsg  string `json:"error_msg,omitempty"`          // 详细错误信息
	RetryLater bool  `json:"retry_later"`                  // 是否允许稍后重试
}

// ReleaseReason 释放原因枚举
const (
	ReleaseReasonDownloadFailed     = "download_failed"
	ReleaseReasonFileCorrupted      = "file_corrupted"
	ReleaseReasonAIUnavailable      = "ai_unavailable"
	ReleaseReasonUnsupportedFormat  = "unsupported_format"
	ReleaseReasonTimeout            = "timeout"
	ReleaseReasonManual             = "manual"
)

// AnalysisResult 分析结果
type AnalysisResult struct {
	PhotoID       uint      `json:"photo_id" binding:"required"`      // 照片ID
	TaskID        string    `json:"task_id,omitempty"`                // 任务ID（可选）
	Description   string    `json:"description" binding:"required"`   // 详细描述
	Caption       string    `json:"caption,omitempty"`                // 短标题
	MemoryScore   int       `json:"memory_score" binding:"required,min=0,max=100"`  // 记忆分数
	BeautyScore   int       `json:"beauty_score" binding:"required,min=0,max=100"`  // 美观分数
	OverallScore  int       `json:"overall_score,omitempty"`          // 综合分数（后端计算）
	ScoreReason   string    `json:"score_reason,omitempty"`           // 评分理由
	MainCategory  string    `json:"main_category,omitempty"`          // 主分类
	Tags          string    `json:"tags,omitempty"`                   // 标签（逗号分隔）
	AnalyzedAt    time.Time `json:"analyzed_at,omitempty"`            // 分析时间
	AIProvider    string    `json:"ai_provider,omitempty"`            // AI提供商（如 qwen, ollama 等）
}

// SubmitResultsRequest 提交结果请求
type SubmitResultsRequest struct {
	Results []AnalysisResult `json:"results" binding:"required,min=1,max=50"` // 结果列表（1-50条）
}

// RejectedItem 被拒绝的项
type RejectedItem struct {
	PhotoID uint   `json:"photo_id"` // 照片ID
	Reason  string `json:"reason"`   // 拒绝原因
	Message string `json:"message"`  // 详细消息
}

// SubmitResultsResponse 提交结果响应
type SubmitResultsResponse struct {
	Accepted       int            `json:"accepted"`        // 接受数量
	Rejected       int            `json:"rejected"`        // 拒绝数量
	RejectedItems  []RejectedItem `json:"rejected_items"`  // 被拒绝的明细
	FailedPhotos   []uint         `json:"failed_photos"`   // 失败的照片ID列表
}

// AnalyzerStatsResponse 分析统计响应
type AnalyzerStatsResponse struct {
	TotalPhotos       int64   `json:"total_photos"`        // 照片总数
	Analyzed          int64   `json:"analyzed"`            // 已分析数
	Pending           int64   `json:"pending"`             // 待分析数
	Locked            int64   `json:"locked"`              // 当前被锁定数
	Failed            int64   `json:"failed"`              // 失败数
	MyTasks           *MyTasksStats `json:"my_tasks,omitempty"` // 当前API Key的任务统计
	AvgAnalysisTime   float64 `json:"avg_analysis_time"`   // 平均分析时间（秒）
	QueuePressure     string  `json:"queue_pressure"`      // 队列压力：low, normal, high
}

// MyTasksStats 当前API Key的任务统计
type MyTasksStats struct {
	Locked    int64 `json:"locked"`     // 当前持有的锁
	Completed int64 `json:"completed"`  // 已完成分析数
	Failed    int64 `json:"failed"`     // 分析失败数
}

// QueuePressure 队列压力级别
const (
	QueuePressureLow    = "low"
	QueuePressureNormal = "normal"
	QueuePressureHigh   = "high"
)

// GetQueuePressure 根据pending数量计算队列压力
func GetQueuePressure(pending int64) string {
	switch {
	case pending < 100:
		return QueuePressureLow
	case pending >= 1000:
		return QueuePressureHigh
	default:
		return QueuePressureNormal
	}
}
