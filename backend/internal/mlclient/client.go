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
	FaceValidityScore     float64   `json:"face_validity_score"`
	PixelWidth            int       `json:"pixel_width"`
	PixelHeight           int       `json:"pixel_height"`
	Sharpness             float64   `json:"sharpness"`
	Brightness            float64   `json:"brightness"`
	Contrast              float64   `json:"contrast"`
	LandmarkCompleteness  float64   `json:"landmark_completeness"`
	LandmarkGeometryScore float64   `json:"landmark_geometry_score"`
	Yaw                   *float64  `json:"yaw,omitempty"`
	Pitch                 *float64  `json:"pitch,omitempty"`
	Roll                  *float64  `json:"roll,omitempty"`
	PoseEstimable         bool      `json:"pose_estimable"`
	Occluded              bool      `json:"occluded"`
	QualityReasons        []string  `json:"quality_reasons"`
	RuleVersion           string    `json:"rule_version"`
	ModelVersion          string    `json:"model_version"`
}

type DetectedFace struct {
	BBox         BoundingBox           `json:"bbox"`
	Confidence   float64               `json:"confidence"`
	QualityScore float64               `json:"quality_score"`
	Embedding    []float32             `json:"embedding"`
	Evidence     *FaceQualityEvidence  `json:"evidence,omitempty"`
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
	FaceID uint       `json:"face_id"`
	BBox   BoundingBox `json:"bbox"`
}

// ScoreKnownFacesRequest 历史重评分请求：展示图 base64 + 一组目标框。
type ScoreKnownFacesRequest struct {
	ImageBase64 string                 `json:"image_base64"`
	Targets     []ScoreKnownFaceTarget `json:"targets"`
}

// ScoreKnownFaceResult 单个目标的重评分结果，按请求 target 顺序返回。
type ScoreKnownFaceResult struct {
	FaceID      uint                `json:"face_id"`
	Status      string              `json:"status"` // matched / unmatched / error
	MatchedIoU  *float64            `json:"matched_iou,omitempty"`
	Evidence    *FaceQualityEvidence `json:"evidence,omitempty"`
	QualityScore *float64           `json:"quality_score,omitempty"`
}

// ScoreKnownFacesResponse 历史重评分响应。
type ScoreKnownFacesResponse struct {
	Results      []ScoreKnownFaceResult `json:"results"`
	RuleVersion  string                 `json:"rule_version,omitempty"`
	ModelVersion string                 `json:"model_version,omitempty"`
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
