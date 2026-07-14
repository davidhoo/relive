package model

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"time"
)

// EncodeEmbedding serializes a float32 slice as raw little-endian bytes.
// This is ~10x faster to decode than JSON and uses half the storage.
func EncodeEmbedding(emb []float32) []byte {
	b := make([]byte, len(emb)*4)
	for i, f := range emb {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

// DecodeEmbedding parses a face embedding from either the legacy JSON format
// (starts with '[') or the current raw little-endian float32 binary format.
//
// 格式识别说明：raw binary embedding 的首字节可能碰巧为 0x5B（等同 ASCII '['），
// 仅凭 payload[0]=='[' 判定 JSON 会把合法 binary 误判为 JSON 并解析失败，导致
// identity profile ANN rebuild 持续 fail-closed。这里改用「先尝试 JSON，失败则
// fallback 到 binary」的策略，确保两种格式都正确解析，且不做 NaN/Inf/zero-norm
// 校验（这些由 ANN 层 validVector 负责）。
func DecodeEmbedding(payload []byte) []float32 {
	if len(payload) == 0 {
		return nil
	}

	if payload[0] == '[' {
		// 优先按 JSON 解析（兼容旧格式）。
		var emb []float32
		if err := json.Unmarshal(payload, &emb); err == nil {
			return emb
		}

		// JSON 解析失败但长度符合 raw float32 binary，则按 binary fallback。
		// 这覆盖 raw binary 首字节碰巧为 0x5B 的情况。
		if len(payload)%4 == 0 {
			return decodeBinaryEmbedding(payload)
		}

		return nil
	}

	if len(payload)%4 != 0 {
		return nil
	}

	return decodeBinaryEmbedding(payload)
}

// decodeBinaryEmbedding 将 raw little-endian float32 字节切片还原为 []float32。
// 调用方需保证 len(payload)%4 == 0。
func decodeBinaryEmbedding(payload []byte) []float32 {
	emb := make([]float32, len(payload)/4)
	for i := range emb {
		emb[i] = math.Float32frombits(binary.LittleEndian.Uint32(payload[i*4:]))
	}
	return emb
}

const (
	FaceClusterStatusPending  = "pending"
	FaceClusterStatusAssigned = "assigned"
	FaceClusterStatusOutlier  = "outlier"
	FaceClusterStatusManual   = "manual"
	FaceClusterStatusExcluded = "excluded"
)

// 排除原因枚举
const (
	ExclusionReasonNonFace    = "non_face"
	ExclusionReasonLowQuality = "low_quality"
)

// IsValidExclusionReason 校验排除原因是否合法
func IsValidExclusionReason(reason string) bool {
	return reason == ExclusionReasonNonFace || reason == ExclusionReasonLowQuality
}

// FaceExclusion 持久化的人脸排除记录，跨重新检测保持排除结论
type FaceExclusion struct {
	ID           uint      `gorm:"primarykey" json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	PhotoID      uint      `gorm:"not null;index:idx_face_exclusion_photo" json:"photo_id"`
	SourceFaceID uint      `gorm:"not null" json:"source_face_id"`
	Reason       string    `gorm:"type:varchar(20);not null" json:"reason"`
	BBoxX        float64   `gorm:"not null" json:"bbox_x"`
	BBoxY        float64   `gorm:"not null" json:"bbox_y"`
	BBoxWidth    float64   `gorm:"not null" json:"bbox_width"`
	BBoxHeight   float64   `gorm:"not null" json:"bbox_height"`
}

func (FaceExclusion) TableName() string {
	return "face_exclusions"
}

// Face 单张照片中的人脸检测结果
type Face struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	PhotoID    uint    `gorm:"not null;index:idx_face_photo;index:idx_face_person_photo,priority:1" json:"photo_id"`
	PersonID   *uint   `gorm:"index:idx_face_person;index:idx_face_person_photo,priority:2" json:"person_id,omitempty"`
	// idx_face_person_photo is a composite (person_id, photo_id) index for cursor pagination
	// queries that deduplicate photos by person association without scanning all faces.
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

	// 排除相关字段（cluster_status = excluded 时使用）
	ExclusionReason string     `gorm:"type:varchar(20);default:''" json:"exclusion_reason,omitempty"`
	ExcludedAt      *time.Time `json:"excluded_at,omitempty"`
}

func (Face) TableName() string {
	return "faces"
}
