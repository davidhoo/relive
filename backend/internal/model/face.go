package model

import "time"

const (
	FaceClusterStatusPending  = "pending"
	FaceClusterStatusAssigned = "assigned"
	FaceClusterStatusOutlier  = "outlier"
	FaceClusterStatusManual   = "manual"
)

// Face 单张照片中的人脸检测结果
type Face struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	PhotoID    uint    `gorm:"not null;index:idx_face_photo" json:"photo_id"`
	PersonID   *uint   `gorm:"index:idx_face_person" json:"person_id,omitempty"`
	BBoxX      float64 `gorm:"not null" json:"bbox_x"`
	BBoxY      float64 `gorm:"not null" json:"bbox_y"`
	BBoxWidth  float64 `gorm:"not null" json:"bbox_width"`
	BBoxHeight float64 `gorm:"not null" json:"bbox_height"`

	Confidence    float64 `gorm:"not null;default:0" json:"confidence"`
	QualityScore  float64 `gorm:"not null;default:0" json:"quality_score"`
	Embedding     []byte  `gorm:"type:blob" json:"-"`
	ThumbnailPath string  `gorm:"type:varchar(500)" json:"thumbnail_path,omitempty"`

	ClusterStatus string     `gorm:"type:varchar(20);index:idx_face_cluster_status" json:"cluster_status,omitempty"`
	ClusterScore  float64    `gorm:"not null;default:0" json:"cluster_score"`
	ClusteredAt   *time.Time `json:"clustered_at,omitempty"`

	ManualLocked     bool       `gorm:"not null;default:false;index:idx_face_manual_locked" json:"manual_locked"`
	ManualLockReason string     `gorm:"type:varchar(50)" json:"manual_lock_reason,omitempty"`
	ManualLockedAt   *time.Time `json:"manual_locked_at,omitempty"`

	ReclusterGeneration int `gorm:"not null;default:0" json:"recluster_generation"`
	RetryCount          int `gorm:"not null;default:0" json:"retry_count"` // 聚类失败重试次数，用于退避策略
}

func (Face) TableName() string {
	return "faces"
}
