package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
	httpClient *http.Client
	retryCount int
	retryDelay time.Duration
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

// NewAPIClient 创建 API 客户端
func NewAPIClient(baseURL, apiKey string, opts ...ClientOption) *APIClient {
	client := &APIClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
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
	urlStr := c.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// 设置默认头
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// 设置自定义头
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	return c.httpClient.Do(req)
}

// parseResponse 解析响应
func parseResponse(resp *http.Response, result interface{}) error {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	if result != nil {
		if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("unmarshal response: %w", err)
		}
	}

	return nil
}

// CheckHealth 检查服务健康状态
func (c *APIClient) CheckHealth(ctx context.Context) error {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/system/health", nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("service unhealthy: %d", resp.StatusCode)
	}

	return nil
}

// GetTasks 获取待分析任务列表
func (c *APIClient) GetTasks(ctx context.Context, limit int, analyzerID string) (*model.AnalyzerTasksResponse, error) {
	path := fmt.Sprintf("/api/v1/analyzer/tasks?limit=%d", limit)

	headers := make(map[string]string)
	if analyzerID != "" {
		headers["X-Analyzer-ID"] = analyzerID
	}

	resp, err := c.doRequest(ctx, "GET", path, nil, headers)
	if err != nil {
		return nil, err
	}

	var apiResp model.Response
	if err := parseResponse(resp, &apiResp); err != nil {
		return nil, err
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	dataJSON, err := json.Marshal(apiResp.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var tasksResp model.AnalyzerTasksResponse
	if err := json.Unmarshal(dataJSON, &tasksResp); err != nil {
		return nil, fmt.Errorf("unmarshal tasks response: %w", err)
	}

	return &tasksResp, nil
}

// Heartbeat 发送任务心跳
func (c *APIClient) Heartbeat(ctx context.Context, taskID, analyzerID string, progress int, status string) (*model.HeartbeatResponse, error) {
	path := fmt.Sprintf("/api/v1/analyzer/tasks/%s/heartbeat", url.PathEscape(taskID))

	req := model.HeartbeatRequest{
		Progress: progress,
		Status:   status,
	}

	headers := make(map[string]string)
	if analyzerID != "" {
		headers["X-Analyzer-ID"] = analyzerID
	}

	resp, err := c.doRequest(ctx, "POST", path, req, headers)
	if err != nil {
		return nil, err
	}

	var apiResp model.Response
	if err := parseResponse(resp, &apiResp); err != nil {
		return nil, err
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	dataJSON, err := json.Marshal(apiResp.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var heartbeatResp model.HeartbeatResponse
	if err := json.Unmarshal(dataJSON, &heartbeatResp); err != nil {
		return nil, fmt.Errorf("unmarshal heartbeat response: %w", err)
	}

	return &heartbeatResp, nil
}

// ReleaseTask 释放任务
func (c *APIClient) ReleaseTask(ctx context.Context, taskID, analyzerID, reason, errorMsg string, retryLater bool) error {
	path := fmt.Sprintf("/api/v1/analyzer/tasks/%s/release", url.PathEscape(taskID))

	req := model.ReleaseTaskRequest{
		Reason:     reason,
		ErrorMsg:   errorMsg,
		RetryLater: retryLater,
	}

	headers := make(map[string]string)
	if analyzerID != "" {
		headers["X-Analyzer-ID"] = analyzerID
	}

	resp, err := c.doRequest(ctx, "POST", path, req, headers)
	if err != nil {
		return err
	}

	var apiResp model.Response
	if err := parseResponse(resp, &apiResp); err != nil {
		return err
	}

	if !apiResp.Success {
		return fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	return nil
}

// SubmitResults 提交分析结果
func (c *APIClient) SubmitResults(ctx context.Context, results []model.AnalysisResult) (*model.SubmitResultsResponse, error) {
	req := model.SubmitResultsRequest{
		Results: results,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/v1/analyzer/results", req, nil)
	if err != nil {
		return nil, err
	}

	var apiResp model.Response
	if err := parseResponse(resp, &apiResp); err != nil {
		return nil, err
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	dataJSON, err := json.Marshal(apiResp.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var submitResp model.SubmitResultsResponse
	if err := json.Unmarshal(dataJSON, &submitResp); err != nil {
		return nil, fmt.Errorf("unmarshal submit response: %w", err)
	}

	return &submitResp, nil
}

// GetStats 获取统计信息
func (c *APIClient) GetStats(ctx context.Context) (*model.AnalyzerStatsResponse, error) {
	resp, err := c.doRequest(ctx, "GET", "/api/v1/analyzer/stats", nil, nil)
	if err != nil {
		return nil, err
	}

	var apiResp model.Response
	if err := parseResponse(resp, &apiResp); err != nil {
		return nil, err
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("API error: %s", apiResp.Error.Message)
	}

	dataJSON, err := json.Marshal(apiResp.Data)
	if err != nil {
		return nil, fmt.Errorf("marshal data: %w", err)
	}

	var statsResp model.AnalyzerStatsResponse
	if err := json.Unmarshal(dataJSON, &statsResp); err != nil {
		return nil, fmt.Errorf("unmarshal stats response: %w", err)
	}

	return &statsResp, nil
}

// AcquireAnalysisRuntime 获取全局分析运行租约
func (c *APIClient) AcquireAnalysisRuntime(ctx context.Context, ownerType, ownerID, message string) (*model.AnalysisRuntimeLease, *model.AnalysisRuntimeStatusResponse, error) {
	req := model.AnalysisRuntimeAcquireRequest{
		OwnerType: ownerType,
		OwnerID:   ownerID,
		Message:   message,
	}

	resp, err := c.doRequest(ctx, "POST", "/api/v1/analyzer/runtime/acquire", req, nil)
	if err != nil {
		return nil, nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response body: %w", err)
	}

	var apiResp model.Response
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.StatusCode == http.StatusConflict {
		var status model.AnalysisRuntimeStatusResponse
		if apiResp.Data != nil {
			dataJSON, _ := json.Marshal(apiResp.Data)
			_ = json.Unmarshal(dataJSON, &status)
		}
		message := "analysis runtime busy"
		if apiResp.Error != nil && apiResp.Error.Message != "" {
			message = apiResp.Error.Message
		}
		return nil, &status, fmt.Errorf("%s", message)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	dataJSON, err := json.Marshal(apiResp.Data)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal data: %w", err)
	}

	var lease model.AnalysisRuntimeLease
	if err := json.Unmarshal(dataJSON, &lease); err != nil {
		return nil, nil, fmt.Errorf("unmarshal runtime lease: %w", err)
	}

	return &lease, nil, nil
}

// HeartbeatAnalysisRuntime 续约全局分析运行租约
func (c *APIClient) HeartbeatAnalysisRuntime(ctx context.Context, ownerType, ownerID string) error {
	req := model.AnalysisRuntimeHeartbeatRequest{OwnerType: ownerType, OwnerID: ownerID}
	resp, err := c.doRequest(ctx, "POST", "/api/v1/analyzer/runtime/heartbeat", req, nil)
	if err != nil {
		return err
	}

	var apiResp model.Response
	if err := parseResponse(resp, &apiResp); err != nil {
		return err
	}
	if !apiResp.Success {
		return fmt.Errorf("API error: %s", apiResp.Error.Message)
	}
	return nil
}

// ReleaseAnalysisRuntime 释放全局分析运行租约
func (c *APIClient) ReleaseAnalysisRuntime(ctx context.Context, ownerType, ownerID string) error {
	req := model.AnalysisRuntimeReleaseRequest{OwnerType: ownerType, OwnerID: ownerID}
	resp, err := c.doRequest(ctx, "POST", "/api/v1/analyzer/runtime/release", req, nil)
	if err != nil {
		return err
	}

	var apiResp model.Response
	if err := parseResponse(resp, &apiResp); err != nil {
		return err
	}
	if !apiResp.Success {
		return fmt.Errorf("API error: %s", apiResp.Error.Message)
	}
	return nil
}

// DownloadPhoto 下载照片
func (c *APIClient) DownloadPhoto(ctx context.Context, downloadURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// 下载照片时也需要认证
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download photo: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	return resp.Body, nil
}
