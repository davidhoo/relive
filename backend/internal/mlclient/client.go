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

type DetectedFace struct {
	BBox         BoundingBox `json:"bbox"`
	Confidence   float64     `json:"confidence"`
	QualityScore float64     `json:"quality_score"`
	Embedding    []float32   `json:"embedding"`
}

type DetectFacesResponse struct {
	Faces            []DetectedFace `json:"faces"`
	ProcessingTimeMS int            `json:"processing_time_ms"`
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
