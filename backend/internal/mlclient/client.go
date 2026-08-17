package mlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type DetectFacesRequest struct {
	ImagePath     string  `json:"image_path,omitempty"`
	ImageBase64   string  `json:"image_base64,omitempty"`
	MinConfidence float64 `json:"min_confidence,omitempty"`
	MaxFaces      int     `json:"max_faces,omitempty"`
}

type BoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// FaceQualityEvidence 质检证据（对应 ml-service FaceQualityEvidence）。
// 指针字段为零值时省略，向后兼容旧版 ml-service 不返回 evidence 的响应。
type FaceQualityEvidence struct {
	FaceValidityScore     float64  `json:"face_validity_score"`
	PixelWidth            int      `json:"pixel_width"`
	PixelHeight           int      `json:"pixel_height"`
	Sharpness             float64  `json:"sharpness"`
	Brightness            float64  `json:"brightness"`
	Contrast              float64  `json:"contrast"`
	LandmarkCompleteness  float64  `json:"landmark_completeness"`
	LandmarkGeometryScore float64  `json:"landmark_geometry_score"`
	Yaw                   *float64 `json:"yaw,omitempty"`
	Pitch                 *float64 `json:"pitch,omitempty"`
	Roll                  *float64 `json:"roll,omitempty"`
	PoseEstimable         bool     `json:"pose_estimable"`
	Occluded              bool     `json:"occluded"`
	QualityReasons        []string `json:"quality_reasons"`
	RuleVersion           string   `json:"rule_version"`
	ModelVersion          string   `json:"model_version"`
}

type DetectedFace struct {
	BBox         BoundingBox          `json:"bbox"`
	Confidence   float64              `json:"confidence"`
	QualityScore float64              `json:"quality_score"`
	Embedding    []float32            `json:"embedding"`
	Evidence     *FaceQualityEvidence `json:"evidence,omitempty"`
}

type DetectFacesResponse struct {
	Faces            []DetectedFace `json:"faces"`
	ProcessingTimeMS int            `json:"processing_time_ms"`
	RuleVersion      string         `json:"rule_version,omitempty"`
	ModelVersion     string         `json:"model_version,omitempty"`
}

// ---- 已知框重评分（score-known-faces）----
// 仅供后端历史重评分 worker 内部调用，不暴露给浏览器。

// ScoreKnownFaceTarget 单个重评分目标：一个已知人脸框（归一化）。
type ScoreKnownFaceTarget struct {
	FaceID uint        `json:"face_id"`
	BBox   BoundingBox `json:"bbox"`
}

// ScoreKnownFacesRequest 历史重评分请求：展示图 base64 + 一组目标框。
type ScoreKnownFacesRequest struct {
	ImageBase64 string                 `json:"image_base64"`
	Targets     []ScoreKnownFaceTarget `json:"targets"`
}

// ScoreKnownFaceResult 单个目标的重评分结果，按请求 target 顺序返回。
type ScoreKnownFaceResult struct {
	FaceID       uint                 `json:"face_id"`
	Status       string               `json:"status"` // matched / unmatched / error
	MatchedIoU   *float64             `json:"matched_iou,omitempty"`
	Evidence     *FaceQualityEvidence `json:"evidence,omitempty"`
	QualityScore *float64             `json:"quality_score,omitempty"`
}

// ScoreKnownFacesResponse 历史重评分响应。
type ScoreKnownFacesResponse struct {
	Results      []ScoreKnownFaceResult `json:"results"`
	RuleVersion  string                 `json:"rule_version,omitempty"`
	ModelVersion string                 `json:"model_version,omitempty"`
}

// ---- v2 独立复核（verify-known-face-crops）----
// 仅供后端历史复核 worker 内部调用，不暴露给浏览器。
// 请求按 Face 传输「以人脸框为中心、四周各扩展 100%」的上下文裁剪 Base64、face_id、
// 原图人脸框宽高与主检测分。ML 端用独立验证器（YuNet）判定，并在原图人脸框上计算质量特征。

// VerifyKnownFaceCropTarget 单个 v2 复核目标。
type VerifyKnownFaceCropTarget struct {
	FaceID            uint   `json:"face_id"`
	ContextCropBase64 string `json:"context_crop_base64"` // 上下文裁剪 JPEG Base64
	FaceBoxWidthPx    int    `json:"face_box_width_px"`   // 原图人脸框实际宽
	FaceBoxHeightPx   int    `json:"face_box_height_px"`  // 原图人脸框实际高
	// FaceBoxOffsetX/Y 人脸框左上角在上下文裁剪中的像素偏移。
	// ML 端用此精确定位人脸框区域计算原图质量指标，不得用裁剪中心近似。
	FaceBoxOffsetX       int     `json:"face_box_offset_x"`
	FaceBoxOffsetY       int     `json:"face_box_offset_y"`
	PrimaryDetectorScore float64 `json:"primary_detector_score"` // 主检测置信度
}

// VerifyKnownFaceCropsRequest v2 独立复核请求：一组目标裁剪。
type VerifyKnownFaceCropsRequest struct {
	Targets []VerifyKnownFaceCropTarget `json:"targets"`
}

// V2QualityFeatures 原图人脸框质量特征（统一到固定短边归一化后计算）。
type V2QualityFeatures struct {
	SharpnessNorm  float64 `json:"sharpness_norm"`
	BrightnessNorm float64 `json:"brightness_norm"`
	ContrastNorm   float64 `json:"contrast_norm"`
	Occluded       bool    `json:"occluded"`
	QualityDomain  string  `json:"quality_domain"`
	QualityVersion string  `json:"quality_version"`
}

// CandidateBox 诊断用候选框（上下文裁剪副本坐标系，像素）。仅供审计/排障。
type CandidateBox struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// VerifyKnownFaceCropResult 单个目标的 v2 复核结果，按请求 target 顺序返回。
// VerificationStatus: face / no_face / uncertain / error。
// face/no_face 语义为「是否匹配到目标脸框」：face 表示某检测框与请求目标框 IoU>=阈值；
// no_face 表示未匹配目标框（裁剪内可能仍有其他人脸，见 MaxContextScore）。
type VerifyKnownFaceCropResult struct {
	FaceID             uint     `json:"face_id"`
	VerificationStatus string   `json:"verification_status"`
	VerifierScore      float64  `json:"verifier_score"`             // 目标匹配分；未匹配为 0
	MaxContextScore    float64  `json:"max_context_score"`          // 裁剪内所有检测最高分（诊断用，非确认分）
	TargetMatchIoU     *float64 `json:"target_match_iou,omitempty"` // 匹配目标框的 IoU；未匹配为 nil
	// 诊断：与目标框几何最接近（最大 IoU）的候选，即便低于阈值也记录。无候选为 nil。
	// 仅供审计/排障，不作为自动隔离、质量分或 UI 的确认分。
	BestTargetIoU            *float64           `json:"best_target_iou,omitempty"`
	BestTargetCandidateScore float64            `json:"best_target_candidate_score"`         // best_target_iou 对应候选置信度；无候选为 0
	BestTargetCandidateBox   *CandidateBox      `json:"best_target_candidate_box,omitempty"` // best_target_iou 对应候选框
	// 尺度归一化审计：送入 YuNet 的检测副本相对未缩放上下文的缩放比例与实际输入尺寸。
	// scale=1 表示未缩放；<1 表示等比缩小。仅 v4 schema（independent_v2_target_match_v3）填充。
	VerifierInputScale    float64 `json:"verifier_input_scale,omitempty"`
	VerifierInputWidthPx  int     `json:"verifier_input_width_px,omitempty"`
	VerifierInputHeightPx int     `json:"verifier_input_height_px,omitempty"`
	VerifierName             string             `json:"verifier_name"`
	VerifierVersion          string             `json:"verifier_version"`
	OriginalWidth            int                `json:"original_width"`
	OriginalHeight           int                `json:"original_height"`
	FaceBoxWidthPx           int                `json:"face_box_width_px"`
	FaceBoxHeightPx          int                `json:"face_box_height_px"`
	ContextCropWidthPx       int                `json:"context_crop_width_px"`
	ContextCropHeightPx      int                `json:"context_crop_height_px"`
	ContextExpandRatio       float64            `json:"context_expand_ratio"`
	PrimaryDetectorScore     float64            `json:"primary_detector_score"`
	Quality                  *V2QualityFeatures `json:"quality,omitempty"`
	ReasonCodes              []string           `json:"reason_codes,omitempty"`
	EvidenceSchemaVersion    string             `json:"evidence_schema_version"`
}

// VerifyKnownFaceCropsResponse v2 独立复核响应，按 face_id 一一对应。
type VerifyKnownFaceCropsResponse struct {
	Results      []VerifyKnownFaceCropResult `json:"results"`
	RuleVersion  string                      `json:"rule_version,omitempty"`
	ModelVersion string                      `json:"model_version,omitempty"`
}

// ---- ML 服务健康（v2 readiness 门禁）----
// 后端在创建/恢复/重试 independent_v2 run 前调用 Health，仅当验证器可用才放行。
// 任何 503/非预期 identity/解析错误/timeout 都映射为 Ready=false，由 service 层吞成
// errV2VerifierUnavailable，不向浏览器暴露底层路径或原始异常。

// MLHealthResponse 镜像 ml-service 的 HealthResponse。
type MLHealthResponse struct {
	Status            string `json:"status"`
	VerifierAvailable bool   `json:"verifier_available"`
	VerifierName      string `json:"verifier_name"`
	VerifierVersion   string `json:"verifier_version"`
}

// 预期 v2 验证器 identity。与 ml-service schemas.YUNET_VERIFIER_NAME/VERSION 对齐。
const (
	MLHealthVerifierNameExpected    = "yunet"
	MLHealthVerifierVersionExpected = "opencv-yunet-2023mar"
)

// MLHealthResult 是从 ML 健康响应推导的就绪判定。Ready=true 仅当 HTTP 200、status=ok、
// verifier_available=true 且 identity 匹配预期契约。
type MLHealthResult struct {
	Ready             bool
	Status            string
	VerifierAvailable bool
	VerifierName      string
	VerifierVersion   string
}

// Health 调用 ML 服务 /api/v1/health，返回就绪判定。
// 仅当 HTTP 200 + status=ok + verifier_available=true + identity=yunet/opencv-yunet-2023mar
// 时 Ready=true。任何其他情况（含 503 degraded、identity 不符、解析/网络错误）返回 Ready=false，
// 调用方据此阻断 v2 run。transport 错误以 error 返回，调用方统一吞为「未就绪」。
func (c *Client) Health(ctx context.Context) (*MLHealthResult, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/health", nil)
	if err != nil {
		return nil, fmt.Errorf("build health request: %w", err)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call health: %w", err)
	}
	defer resp.Body.Close()

	var hr MLHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&hr); err != nil {
		return nil, fmt.Errorf("decode health response: %w", err)
	}

	ready := resp.StatusCode == http.StatusOK &&
		hr.Status == "ok" &&
		hr.VerifierAvailable &&
		hr.VerifierName == MLHealthVerifierNameExpected &&
		hr.VerifierVersion == MLHealthVerifierVersionExpected

	return &MLHealthResult{
		Ready:             ready,
		Status:            hr.Status,
		VerifierAvailable: hr.VerifierAvailable,
		VerifierName:      hr.VerifierName,
		VerifierVersion:   hr.VerifierVersion,
	}, nil
}

func New(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) DetectFaces(ctx context.Context, request DetectFacesRequest) (*DetectFacesResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal detect faces request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/detect-faces", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build detect faces request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call detect faces: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("detect faces returned status %d", resp.StatusCode)
	}

	var result DetectFacesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode detect faces response: %w", err)
	}

	return &result, nil
}

// ScoreKnownFaces 调用 ML 服务的“已知框重评分”接口。
// 仅用于历史重评分 worker：传入已旋转校正的展示图 base64 和一组目标归一化 BBox，
// ML 端在同一张图上检测并按最大 IoU 一对一匹配，返回每个目标的 matched/unmatched/error 与证据。
// 不得把 unmatched/error 当作 non_face——这些只能进入可重试技术状态。
func (c *Client) ScoreKnownFaces(ctx context.Context, request ScoreKnownFacesRequest) (*ScoreKnownFacesResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal score known faces request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/score-known-faces", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build score known faces request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call score known faces: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("score known faces returned status %d", resp.StatusCode)
	}

	var result ScoreKnownFacesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode score known faces response: %w", err)
	}

	return &result, nil
}

// VerifyKnownFaceCrops 调用 ML 服务的 v2 独立复核接口。
// 仅用于历史复核 worker：传入每个 Face 的上下文裁剪 Base64、原图人脸框宽高与主检测分，
// ML 端用独立验证器（YuNet）判定 face/no_face/uncertain/error，并在原图人脸框上计算质量特征。
// 任何单条 target 的错误只影响对应 result，不使整批失败（HTTP 层仍校验 2xx）。
// 不得把 error/uncertain 当作 non_face——这些只能进入可重试/待人工审核状态。
func (c *Client) VerifyKnownFaceCrops(ctx context.Context, request VerifyKnownFaceCropsRequest) (*VerifyKnownFaceCropsResponse, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("marshal verify known face crops request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/verify-known-face-crops", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build verify known face crops request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call verify known face crops: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("verify known face crops returned status %d", resp.StatusCode)
	}

	var result VerifyKnownFaceCropsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode verify known face crops response: %w", err)
	}

	return &result, nil
}
