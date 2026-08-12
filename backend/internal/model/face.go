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
	// FaceClusterStatusReviewRequired 待人工质检：不参与聚类，与 excluded 一样不进入人物聚合，
	// 但语义不同——它是灰区样本，等待人工确认，照片详情页须提示“待质检”。
	FaceClusterStatusReviewRequired = "review_required"
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

	PersonID   *uint   `gorm:"index:idx_face_person;index:idx_face_person_photo,priority:1" json:"person_id,omitempty"`
	PhotoID    uint    `gorm:"not null;index:idx_face_photo;index:idx_face_person_photo,priority:2" json:"photo_id"`
	// idx_face_person_photo is a composite (person_id, photo_id) index for cursor pagination
	// queries that deduplicate photos by person association without scanning all faces.
	// Column order is person_id ASC, photo_id ASC (priority:1 < priority:2) so a
	// WHERE faces.person_id = ? predicate can seek directly without a full faces scan.
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

	// 质检证据快照（face_quality_events 的冗余字段，便于审核页直接读取，避免 JOIN）。
	// 由 ApplyDetectionResult 写入；人工改判不覆盖此处，历史由 face_quality_events 追加保留。
	FaceValidityScore      float64 `gorm:"not null;default:0" json:"face_validity_score"`
	QualityReasonsCSV      string  `gorm:"type:varchar(255);default:''" json:"quality_reasons,omitempty"`
	QualityRuleVersion     string  `gorm:"type:varchar(20);default:''" json:"quality_rule_version,omitempty"`
	QualityModelVersion    string  `gorm:"type:varchar(40);default:''" json:"quality_model_version,omitempty"`
}

func (Face) TableName() string {
	return "faces"
}

// 质检最终动作枚举
const (
	FaceQualityActionAccept          = "accept"
	FaceQualityActionExclude         = "exclude"
	FaceQualityActionReviewRequired  = "review_required"
)

// 质检判定枚举（face_quality_events.decision）
const (
	FaceQualityDecisionAccepted       = "accepted"
	FaceQualityDecisionNonFace        = "non_face"
	FaceQualityDecisionLowQuality     = "low_quality"
	FaceQualityDecisionReviewRequired = "review_required"
)

// 质检来源枚举
const (
	FaceQualitySourceAuto   = "auto"
	FaceQualitySourceManual = "manual"
)

// 质检审核动作枚举（face_quality_events.review_action）
const (
	FaceQualityReviewActionConfirmExclude = "confirm_exclude"
	FaceQualityReviewActionMarkNonFace    = "mark_non_face"
	FaceQualityReviewActionMarkLowQuality = "mark_low_quality"
	FaceQualityReviewActionAccept         = "accept"
	FaceQualityReviewActionRestore        = "restore"
)

// FaceQualityEvent 追加式人脸质检审计表。
// 每次判定/审核/恢复都写一行，不删除历史，保证可追溯与按规则版本回滚。
type FaceQualityEvent struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	PhotoID      uint    `gorm:"not null;index:idx_fqe_photo" json:"photo_id"`
	FaceID       *uint   `gorm:"index:idx_fqe_face" json:"face_id,omitempty"` // 最近匹配 Face ID，重检后回填
	ExclusionID  *uint   `gorm:"index:idx_fqe_exclusion" json:"exclusion_id,omitempty"`
	// 归一化人脸框：重检后按 photo_id + bbox IoU 匹配回填当前结论。
	// gorm 默认 snake_case 会把 BBoxX 转成 b_box_x，这里用 column 显式指定为 bbox_x，
	// 与 face_exclusions 表保持一致，便于跨表 IoU 查询复用同一列名。
	BBoxX      float64 `gorm:"column:bbox_x;not null" json:"bbox_x"`
	BBoxY      float64 `gorm:"column:bbox_y;not null" json:"bbox_y"`
	BBoxWidth  float64 `gorm:"column:bbox_width;not null" json:"bbox_width"`
	BBoxHeight float64 `gorm:"column:bbox_height;not null" json:"bbox_height"`

	// 判定结果。
	Decision string `gorm:"type:varchar(20);not null;index:idx_fqe_decision" json:"decision"`
	Reason   string `gorm:"type:varchar(20);not null" json:"reason"` // non_face/low_quality/''（accepted/review_required 为空）
	Source   string `gorm:"type:varchar(10);not null;index:idx_fqe_source" json:"source"` // auto/manual

	// 版本与证据。
	RuleVersion  string `gorm:"type:varchar(20);not null;index:idx_fqe_rule_version" json:"rule_version"`
	ModelVersion string `gorm:"type:varchar(40);not null" json:"model_version"`
	EvidenceJSON string `gorm:"type:text" json:"evidence_json,omitempty"`
	ReasonCodes  string `gorm:"type:varchar(255);default:''" json:"reason_codes,omitempty"`

	// 审核与恢复。
	ReviewAction string     `gorm:"type:varchar(30);default:'';index:idx_fqe_review_action" json:"review_action,omitempty"`
	ReviewedAt   *time.Time `json:"reviewed_at,omitempty"`
	RestoredAt   *time.Time `json:"restored_at,omitempty"`

	// 当前是否为该照片+人脸框的最新结论（重检后旧事件置 false，新事件置 true）。
	// 用于审核页只取每个框的最新一条。
	IsCurrent bool `gorm:"not null;default:false;index:idx_fqe_current" json:"is_current"`
}

func (FaceQualityEvent) TableName() string {
	return "face_quality_events"
}

// IsValidQualityDecision 校验质检判定是否合法
func IsValidQualityDecision(d string) bool {
	return d == FaceQualityDecisionAccepted ||
		d == FaceQualityDecisionNonFace ||
		d == FaceQualityDecisionLowQuality ||
		d == FaceQualityDecisionReviewRequired
}

// IsValidQualityReviewAction 校验审核动作是否合法
func IsValidQualityReviewAction(a string) bool {
	switch a {
	case FaceQualityReviewActionConfirmExclude,
		FaceQualityReviewActionMarkNonFace,
		FaceQualityReviewActionMarkLowQuality,
		FaceQualityReviewActionAccept,
		FaceQualityReviewActionRestore:
		return true
	}
	return false
}

// FaceQualityEvidence 质检证据的 Go 镜像（对应 ml-service FaceQualityEvidence JSON）
type FaceQualityEvidence struct {
	FaceValidityScore      float64   `json:"face_validity_score"`
	PixelWidth             int       `json:"pixel_width"`
	PixelHeight            int       `json:"pixel_height"`
	Sharpness              float64   `json:"sharpness"`
	Brightness             float64   `json:"brightness"`
	Contrast               float64   `json:"contrast"`
	LandmarkCompleteness   float64   `json:"landmark_completeness"`
	LandmarkGeometryScore  float64   `json:"landmark_geometry_score"`
	Yaw                    *float64  `json:"yaw,omitempty"`
	Pitch                  *float64  `json:"pitch,omitempty"`
	Roll                   *float64  `json:"roll,omitempty"`
	PoseEstimable          bool      `json:"pose_estimable"`
	Occluded               bool      `json:"occluded"`
	QualityReasons         []string  `json:"quality_reasons"`
	RuleVersion            string    `json:"rule_version"`
	ModelVersion           string    `json:"model_version"`
}
