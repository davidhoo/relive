package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
)

const (
	defaultTimeout    = 30 * time.Second
	defaultRetryCount = 3
	defaultRetryDelay = 1 * time.Second
)

// APIClient HTTP API 客户端
type APIClient struct {
	baseURL    string
	apiKey     string
	workerID   string
	httpClient *http.Client
	retryCount int
	retryDelay time.Duration
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RawBody    string
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("HTTP %d %s: %s", e.StatusCode, e.Code, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Message)
	}
	if e.RawBody != "" {
		return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.RawBody)
	}
	return fmt.Sprintf("HTTP %d", e.StatusCode)
}

func IsPeopleRuntimeConflict(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != http.StatusConflict {
		return false
	}
	switch apiErr.Code {
	case "PEOPLE_RUNTIME_BUSY", "PEOPLE_RUNTIME_OWNED_BY_OTHER", "PEOPLE_RUNTIME_NOT_ACQUIRED":
		return true
	default:
		return strings.HasPrefix(apiErr.Code, "PEOPLE_RUNTIME_")
	}
}

// ClientOption 客户端配置选项
type ClientOption func(*APIClient)

// WithTimeout 设置超时
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *APIClient) {
		c.httpClient.Timeout = timeout
	}
}

// WithRetry 设置重试次数和延迟
func WithRetry(count int, delay time.Duration) ClientOption {
	return func(c *APIClient) {
		c.retryCount = count
		c.retryDelay = delay
	}
}

// WithWorkerID 设置 Worker ID
func WithWorkerID(workerID string) ClientOption {
	return func(c *APIClient) {
		c.workerID = workerID
	}
}

// NewAPIClient 创建 API 客户端
func NewAPIClient(baseURL, apiKey string, opts ...ClientOption) *APIClient {
	client := &APIClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		workerID:   "",
		httpClient: &http.Client{Timeout: defaultTimeout},
		retryCount: defaultRetryCount,
		retryDelay: defaultRetryDelay,
	}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

// doRequest 执行 HTTP 请求（带重试）
func (c *APIClient) doRequest(ctx context.Context, method, path string, body interface{}, headers map[string]string) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= c.retryCount; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.retryDelay * time.Duration(attempt)):
				// 指数退避
			}
		}

		resp, err := c.doRequestOnce(ctx, method, path, body, headers)
		if err != nil {
			lastErr = err
			// 网络错误，继续重试
			continue
		}

		// 服务器错误 (5xx) 重试，客户端错误 (4xx) 不重试
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("server error: %d", resp.StatusCode)
			continue
		}

		return resp, nil
	}

	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// doRequestOnce 执行单次 HTTP 请求
func (c *APIClient) doRequestOnce(ctx context.Context, method, path string, body interface{}, headers map[string]string) (*http.Response, error) {
	url, err := c.buildURL(path)
	if err != nil {
		return nil, err
	}

	var bodyReader io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// 设置默认请求头
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("X-Worker-ID", c.workerID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	// 应用自定义请求头
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	return c.httpClient.Do(req)
}

// buildURL 构建完整 URL
func (c *APIClient) buildURL(path string) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL: %w", err)
	}

	rel, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	return base.ResolveReference(rel).String(), nil
}

// parseResponse 解析响应
func parseResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		apiErr := &APIError{
			StatusCode: resp.StatusCode,
			RawBody:    string(body),
			Message:    string(body),
		}
		var response model.Response
		if err := json.Unmarshal(body, &response); err == nil && response.Error != nil {
			apiErr.Code = response.Error.Code
			apiErr.Message = response.Error.Message
		}
		return apiErr
	}

	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
	}

	return nil
}

// ==================== People Worker API ====================

// GetTasks 获取待处理任务
func (c *APIClient) GetTasks(ctx context.Context, limit int) (*model.PeopleWorkerTasksResponse, error) {
	path := fmt.Sprintf("/api/v1/people/worker/tasks?limit=%d", limit)
	resp, err := c.doRequest(ctx, "GET", path, nil, nil)
	if err != nil {
		return nil, err
	}

	var result model.Response
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("API error: %s", result.Error.Message)
	}

	data, err := json.Marshal(result.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var tasksResp model.PeopleWorkerTasksResponse
	if err := json.Unmarshal(data, &tasksResp); err != nil {
		return nil, fmt.Errorf("unmarshal tasks: %w", err)
	}

	return &tasksResp, nil
}

// HeartbeatTask 发送任务心跳
func (c *APIClient) HeartbeatTask(ctx context.Context, taskID uint, progress int, statusMsg string) (*model.PeopleWorkerHeartbeatResponse, error) {
	path := fmt.Sprintf("/api/v1/people/worker/tasks/%d/heartbeat", taskID)
	req := model.PeopleWorkerHeartbeatRequest{
		Progress:      progress,
		StatusMessage: statusMsg,
	}

	resp, err := c.doRequest(ctx, "POST", path, req, nil)
	if err != nil {
		return nil, err
	}

	var result model.Response
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("API error: %s", result.Error.Message)
	}

	data, err := json.Marshal(result.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var heartbeatResp model.PeopleWorkerHeartbeatResponse
	if err := json.Unmarshal(data, &heartbeatResp); err != nil {
		return nil, fmt.Errorf("unmarshal heartbeat: %w", err)
	}

	return &heartbeatResp, nil
}

// ReleaseTask 释放任务
func (c *APIClient) ReleaseTask(ctx context.Context, taskID uint, reason string, retryLater bool) error {
	path := fmt.Sprintf("/api/v1/people/worker/tasks/%d/release", taskID)
	req := model.PeopleWorkerReleaseTaskRequest{
		Reason:     reason,
		RetryLater: retryLater,
	}

	resp, err := c.doRequest(ctx, "POST", path, req, nil)
	if err != nil {
		return err
	}

	var result model.Response
	if err := parseResponse(resp, &result); err != nil {
		return err
	}

	if !result.Success {
		return fmt.Errorf("API error: %s", result.Error.Message)
	}

	return nil
}

// SubmitResults 提交检测结果
func (c *APIClient) SubmitResults(ctx context.Context, results []model.PeopleDetectionResult) (*model.PeopleWorkerSubmitResultsResponse, error) {
	path := "/api/v1/people/worker/results"
	req := model.PeopleWorkerSubmitResultsRequest{
		Results: results,
	}

	resp, err := c.doRequest(ctx, "POST", path, req, nil)
	if err != nil {
		return nil, err
	}

	var result model.Response
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("API error: %s", result.Error.Message)
	}

	data, err := json.Marshal(result.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var submitResp model.PeopleWorkerSubmitResultsResponse
	if err := json.Unmarshal(data, &submitResp); err != nil {
		return nil, fmt.Errorf("unmarshal submit response: %w", err)
	}

	return &submitResp, nil
}

// ==================== Runtime Lease API ====================

// AcquireRuntime 获取运行时租约
func (c *APIClient) AcquireRuntime(ctx context.Context) (*model.PeopleWorkerRuntimeLeaseResponse, error) {
	path := "/api/v1/people/runtime/acquire"
	req := model.PeopleWorkerRuntimeLeaseRequest{
		WorkerID: c.workerID,
	}

	resp, err := c.doRequest(ctx, "POST", path, req, nil)
	if err != nil {
		return nil, err
	}

	var result model.Response
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("API error: %s", result.Error.Message)
	}

	data, err := json.Marshal(result.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var leaseResp model.PeopleWorkerRuntimeLeaseResponse
	if err := json.Unmarshal(data, &leaseResp); err != nil {
		return nil, fmt.Errorf("unmarshal lease: %w", err)
	}

	return &leaseResp, nil
}

// HeartbeatRuntime 续约运行时租约
func (c *APIClient) HeartbeatRuntime(ctx context.Context) (*model.PeopleWorkerRuntimeLeaseResponse, error) {
	path := "/api/v1/people/runtime/heartbeat"
	req := model.PeopleWorkerRuntimeLeaseRequest{
		WorkerID: c.workerID,
	}

	resp, err := c.doRequest(ctx, "POST", path, req, nil)
	if err != nil {
		return nil, err
	}

	var result model.Response
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("API error: %s", result.Error.Message)
	}

	data, err := json.Marshal(result.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var leaseResp model.PeopleWorkerRuntimeLeaseResponse
	if err := json.Unmarshal(data, &leaseResp); err != nil {
		return nil, fmt.Errorf("unmarshal lease: %w", err)
	}

	return &leaseResp, nil
}

// ReleaseRuntime 释放运行时租约
func (c *APIClient) ReleaseRuntime(ctx context.Context) error {
	path := "/api/v1/people/runtime/release"
	req := model.PeopleWorkerRuntimeLeaseRequest{
		WorkerID: c.workerID,
	}

	resp, err := c.doRequest(ctx, "POST", path, req, nil)
	if err != nil {
		return err
	}

	var result model.Response
	if err := parseResponse(resp, &result); err != nil {
		return err
	}

	if !result.Success {
		return fmt.Errorf("API error: %s", result.Error.Message)
	}

	return nil
}
