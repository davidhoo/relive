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

// 证据来源枚举（evidence_origin）：区分“谁作出最终结论”之外的“证据从何而来”。
// 保留 source=auto/manual 的“谁作出最终结论”语义，不滥用 source 表达证据来源。
const (
	// FaceQualityEvidenceOriginRealtime 实时检测/实时自动质检产出的证据。
	FaceQualityEvidenceOriginRealtime = "realtime"
	// FaceQualityEvidenceOriginHistoricalBackfill 旧 Face 快照回放写入的审计事件（无模型证据）。
	FaceQualityEvidenceOriginHistoricalBackfill = "historical_backfill"
	// FaceQualityEvidenceOriginHistoricalRescore 历史重评分运行补齐的模型证据。
	FaceQualityEvidenceOriginHistoricalRescore = "historical_rescore"
)

// 证据管线枚举（evidence_pipeline）：区分证据由哪条验证链路产生。
// v1 的 ScoreKnownFaces 在已旋转展示缩略图上复用同一套 InsightFace 检测，属同源启发式证据；
// v2 使用独立验证器（YuNet）+ 原图裁剪，是独立复核证据。两者不可混用为同一结论依据。
const (
	// FaceQualityEvidencePipelineLegacyV1 v1 同源启发式证据（score-known-faces）。
	// 仅保留供历史追溯，不得作为 v2 自动隔离或人工判断依据。
	FaceQualityEvidencePipelineLegacyV1 = "legacy_v1"
	// FaceQualityEvidencePipelineIndependentV2 v2 独立验证器 + 原图裁剪证据。
	// v2 历史自动隔离唯一允许使用的校准来源。
	FaceQualityEvidencePipelineIndependentV2 = "independent_v2"
)

// IsValidEvidencePipeline 校验证据管线是否合法
func IsValidEvidencePipeline(s string) bool {
	return s == FaceQualityEvidencePipelineLegacyV1 ||
		s == FaceQualityEvidencePipelineIndependentV2
}

// 证据状态枚举（evidence_state）：审核队列分流的权威字段，不能由分数推断。
const (
	// FaceQualityEvidenceStateAvailable 已有可解析的模型证据（含真实 0 分）。
	FaceQualityEvidenceStateAvailable = "available"
	// FaceQualityEvidenceStateMissing 历史回填无模型证据，待补证据，不需人工逐张确认。
	FaceQualityEvidenceStateMissing = "missing"
	// FaceQualityEvidenceStateRetryableError 重评分遇到可重试技术问题（超时/读图失败/JSON 异常）。
	FaceQualityEvidenceStateRetryableError = "retryable_error"
	// FaceQualityEvidenceStateUnmatched 重评分未在图中找到与旧框匹配的人脸。
	FaceQualityEvidenceStateUnmatched = "unmatched"
)

// IsValidEvidenceOrigin 校验证据来源是否合法
func IsValidEvidenceOrigin(s string) bool {
	return s == FaceQualityEvidenceOriginRealtime ||
		s == FaceQualityEvidenceOriginHistoricalBackfill ||
		s == FaceQualityEvidenceOriginHistoricalRescore
}

// IsValidEvidenceState 校验证据状态是否合法
func IsValidEvidenceState(s string) bool {
	return s == FaceQualityEvidenceStateAvailable ||
		s == FaceQualityEvidenceStateMissing ||
		s == FaceQualityEvidenceStateRetryableError ||
		s == FaceQualityEvidenceStateUnmatched
}

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

	// 证据来源/状态：队列分流的权威字段（详见枚举注释）。新写入路径必须显式填写有效枚举，
	// 旧数据空字符串仅为向后兼容，由一次性迁移标记为 historical_backfill/missing。
	// (is_current, evidence_origin, evidence_state, id DESC) 复合索引由
	// migrateFaceQualityEvidenceOrigin 以原生 SQL 创建，便于带 id DESC 方向。
	EvidenceOrigin string `gorm:"type:varchar(32);default:''" json:"evidence_origin,omitempty"`
	EvidenceState  string `gorm:"type:varchar(24);default:''" json:"evidence_state,omitempty"`
	// EvidencePipeline 证据管线：legacy_v1 / independent_v2。
	// v2 审核接口不得把 legacy_v1 字段映射成 v2 结论。新实时检测与历史复核必须显式填写，
	// 不允许留空（旧行由迁移标记为 legacy_v1）。
	EvidencePipeline string `gorm:"type:varchar(20);not null;default:'legacy_v1'" json:"evidence_pipeline,omitempty"`
	// RescoreRunID 产生该次历史重评分结论的运行 ID；实时与旧回填为空。
	RescoreRunID *uint `gorm:"index:idx_fqe_rescore_run" json:"rescore_run_id,omitempty"`

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

// FaceQualityEvidenceV2 独立复核证据（evidence_pipeline=independent_v2）。
// 所有尺寸/质量指标都在「EXIF 方向校正 + manual_rotation 叠加」的原图上计算，
// 不得用缩略图或 ProcessForAI(1024,85) 压缩图的尺寸覆盖。缩略图尺寸不得替代这些字段。
type FaceQualityEvidenceV2 struct {
	EvidenceSchemaVersion string `json:"evidence_schema_version"` // 固定 "independent_v2"

	// 主检测（InsightFace buffalo_sc）置信度。
	PrimaryDetectorScore float64 `json:"primary_detector_score"`

	// 独立验证器（YuNet）结果。
	VerificationStatus string  `json:"verification_status"` // face / no_face / uncertain / error
	VerifierScore      float64 `json:"verifier_score"`
	VerifierName       string  `json:"verifier_name"`
	VerifierVersion    string  `json:"verifier_version"`

	// 原图（方向校正后）尺寸。
	OriginalWidth  int `json:"original_width"`
	OriginalHeight int `json:"original_height"`
	// 人脸框在原图中的实际宽/高（像素）。
	FaceBoxWidthPx  int `json:"face_box_width_px"`
	FaceBoxHeightPx int `json:"face_box_height_px"`
	// 上下文裁剪（人脸框四周各扩展 100%，超出边界裁切）宽/高（像素）。
	ContextCropWidthPx  int     `json:"context_crop_width_px"`
	ContextCropHeightPx int     `json:"context_crop_height_px"`
	ContextExpandRatio  float64 `json:"context_expand_ratio"` // 扩展比例，固定 1.0（四周各 100%）

	// 原图人脸框质量特征（统一到固定短边归一化后计算，标明计算域与版本）。
	SharpnessNorm   float64 `json:"sharpness_norm"`   // Laplacian 清晰度（归一化后）
	BrightnessNorm  float64 `json:"brightness_norm"`  // 亮度
	ContrastNorm    float64 `json:"contrast_norm"`    // 对比度
	Occluded        bool    `json:"occluded"`         // 遮挡/几何可用性
	QualityDomain   string  `json:"quality_domain"`   // 计算域说明，如 "original_face_box_norm_to_96"
	QualityVersion  string  `json:"quality_version"`  // 质量特征计算版本

	// 原因码（auto_decision_conflict / too_small_unconfirmed / verifier_uncertain / ...）。
	ReasonCodes []string `json:"reason_codes,omitempty"`

	// SuggestedDecision shadow 模式下对可自动隔离样本保存的建议决策（non_face/low_quality）。
	// shadow 一律写 review_required，但 evidence 保存系统「想做什么」供人工校准抽样判断阈值。
	// enforce 模式留空（决策即最终决策，无需建议）。
	SuggestedDecision string `json:"suggested_decision,omitempty"`

	RuleVersion  string `json:"rule_version"`
	ModelVersion string `json:"model_version"`
}
