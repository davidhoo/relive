package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// VLLMConfig VLLM 配置
type VLLMConfig struct {
	Endpoint    string  `yaml:"endpoint"`    // VLLM 服务地址
	Model       string  `yaml:"model"`       // 模型名称
	Temperature float64 `yaml:"temperature"` // 温度参数
	Timeout     int     `yaml:"timeout"`     // 超时（秒）
	MaxTokens   int     `yaml:"max_tokens"`  // 最大 tokens
	Concurrency int     `yaml:"concurrency"` // 并发数（批量分析时）
}

// VLLMProvider VLLM 提供者
type VLLMProvider struct {
	config *VLLMConfig
	client *http.Client
}

// NewVLLMProvider 创建 VLLM provider
func NewVLLMProvider(config *VLLMConfig) (*VLLMProvider, error) {
	if config.Endpoint == "" {
		return nil, fmt.Errorf("vllm endpoint is required")
	}

	// 自动清理 endpoint，去除可能的 API 路径后缀
	// 支持处理: /v1/chat/completions, /v1/models 等后缀
	config.Endpoint = normalizeVLLMEndpoint(config.Endpoint)

	if config.Model == "" {
		config.Model = "llava-v1.6-vicuna-13b" // 默认模型
	}
	if config.Temperature == 0 {
		config.Temperature = 0.7
	}
	if config.Timeout == 0 {
		config.Timeout = 120
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 800
	}
	if config.Concurrency == 0 {
		config.Concurrency = 5 // 默认并发数
	}

	return &VLLMProvider{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
	}, nil
}

// normalizeVLLMEndpoint 规范化 VLLM endpoint，去除 API 路径后缀
func normalizeVLLMEndpoint(endpoint string) string {
	// 去除可能的尾部斜杠
	endpoint = strings.TrimRight(endpoint, "/")

	// 去除常见的 API 路径后缀
	suffixes := []string{
		"/v1/chat/completions",
		"/v1/models",
		"/v1/completions",
		"/chat/completions",
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(endpoint, suffix) {
			endpoint = strings.TrimSuffix(endpoint, suffix)
			break
		}
	}

	// 再次去除尾部斜杠
	return strings.TrimRight(endpoint, "/")
}

// Name 返回 provider 名称
func (p *VLLMProvider) Name() string {
	return "vllm"
}

// Cost 返回单次调用成本（自部署，免费）
func (p *VLLMProvider) Cost() float64 {
	return 0.0
}

// IsAvailable 检查服务是否可用
func (p *VLLMProvider) IsAvailable() bool {
	// 尝试访问健康检查端点
	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	// VLLM 通常提供 /health 或 /v1/models 端点
	req, err := http.NewRequest("GET", p.config.Endpoint+"/v1/models", nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// MaxConcurrency 最大并发数
func (p *VLLMProvider) MaxConcurrency() int {
	return p.config.Concurrency // 使用配置的并发数
}

// SupportsBatch 是否支持批量分析
func (p *VLLMProvider) SupportsBatch() bool {
	return false // VLLM 不支持多图批量分析
}

// MaxBatchSize 最大批量大小
func (p *VLLMProvider) MaxBatchSize() int {
	return 1
}

// AnalyzeBatch 批量分析照片（并发处理）
func (p *VLLMProvider) AnalyzeBatch(requests []*AnalyzeRequest) ([]*AnalyzeResult, error) {
	results := make([]*AnalyzeResult, len(requests))

	// 使用 semaphore 限制并发数
	concurrency := p.config.Concurrency
	if concurrency <= 0 {
		concurrency = 5 // 默认并发数
	}

	semaphore := make(chan struct{}, concurrency)
	errChan := make(chan error, len(requests))

	// 使用 WaitGroup 等待所有 goroutine 完成
	var wg sync.WaitGroup

	for i, req := range requests {
		wg.Add(1)
		go func(idx int, request *AnalyzeRequest) {
			defer wg.Done()

			// 获取 semaphore 许可
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			result, err := p.Analyze(request)
			if err != nil {
				errChan <- fmt.Errorf("photo %d: %w", idx, err)
				return
			}
			results[idx] = result
		}(i, req)
	}

	// 等待所有分析完成
	wg.Wait()
	close(errChan)

	// 检查是否有错误
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		// 部分失败，返回已成功分析的结果和错误
		logger.Warnf("VLLM batch analysis: %d/%d failed", len(errs), len(requests))
		// 如果全部失败，返回错误
		if len(errs) == len(requests) {
			return results, fmt.Errorf("all analyses failed: %v", errs[0])
		}
	}

	return results, nil
}

// BatchCost 批量处理成本
func (p *VLLMProvider) BatchCost() float64 {
	return 0.0
}

// Analyze 分析照片
func (p *VLLMProvider) Analyze(request *AnalyzeRequest) (*AnalyzeResult, error) {
	startTime := time.Now()

	// 构建 prompt
	prompt := p.buildPrompt(request)

	// 将图片转换为 base64 data URL
	imageBase64 := base64.StdEncoding.EncodeToString(request.ImageData)
	imageURL := fmt.Sprintf("data:image/jpeg;base64,%s", imageBase64)

	// 构建 OpenAI 兼容的请求
	reqBody := map[string]interface{}{
		"model": p.config.Model,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": prompt,
					},
					{
						"type": "image_url",
						"image_url": map[string]string{
							"url": imageURL,
						},
					},
				},
			},
		},
		"max_tokens":  p.config.MaxTokens,
		"temperature": p.config.Temperature,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 发送请求到 VLLM 的 OpenAI 兼容端点
	httpReq, err := http.NewRequest("POST", p.config.Endpoint+"/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("vllm api error: %s, body: %s", resp.Status, string(body))
	}

	// 解析响应
	var vllmResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&vllmResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(vllmResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	// 解析 AI 响应
	result, err := p.parseResponse(vllmResp.Choices[0].Message.Content)
	if err != nil {
		// 记录完整的响应内容以便调试
		content := vllmResp.Choices[0].Message.Content
		if len(content) > 1000 {
			content = content[:1000] + "... (truncated)"
		}
		logger.Warnf("VLLM parse response failed: %v. Content: %s", err, content)
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// 填充元数据
	result.Provider = p.Name()
	result.ModelName = vllmResp.Model
	result.Timestamp = time.Now()
	result.Duration = time.Since(startTime)
	result.Cost = p.Cost()

	logger.Infof("VLLM analysis completed: model=%s, tokens=%d, duration=%v",
		result.ModelName, vllmResp.Usage.TotalTokens, result.Duration)

	return result, nil
}

// buildPrompt 构建提示词
func (p *VLLMProvider) buildPrompt(request *AnalyzeRequest) string {
	prompt := `请分析这张照片，并以 JSON 格式返回分析结果。

要求：
1. description: 详细描述照片内容（80-200字），包括人物、场景、活动、氛围等
2. caption: 简短优美的文案（8-30字），适合展示在相框上
3. main_category: 主要分类，从以下8个中选择：
   - portrait（人物/肖像）
   - group（集体/合影）
   - landscape（风景）
   - cityscape（城市）
   - food（美食）
   - pet（宠物）
   - event（事件/活动）
   - other（其他）
4. tags: 标签（逗号分隔），如：旅游,美食,家人,朋友,户外,室内等
5. memory_score: 回忆价值评分（0-100），评估纪念意义和情感价值
6. beauty_score: 美观度评分（0-100），评估构图、光线、色彩等摄影质量
7. reason: 评分理由（40字内）

`

	// 添加 EXIF 信息
	if request.ExifInfo != nil {
		if request.ExifInfo.DateTime != "" {
			prompt += fmt.Sprintf("拍摄时间：%s\n", request.ExifInfo.DateTime)
		}
		if request.ExifInfo.City != "" {
			prompt += fmt.Sprintf("拍摄地点：%s\n", request.ExifInfo.City)
		}
		if request.ExifInfo.Model != "" {
			prompt += fmt.Sprintf("相机型号：%s\n", request.ExifInfo.Model)
		}
	}

	prompt += `

【重要】请直接返回 JSON 结果，不要输出任何思考过程、解释或额外文字。

请严格按照以下 JSON 格式返回结果（只返回 JSON，不要有其他内容）：
{
  "description": "...",
  "caption": "...",
  "main_category": "...",
  "tags": "...",
  "memory_score": 85,
  "beauty_score": 90,
  "reason": "..."
}`

	return prompt
}

// parseResponse 解析 AI 响应
func (p *VLLMProvider) parseResponse(response string) (*AnalyzeResult, error) {
	// 尝试提取 JSON
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		// 记录原始响应用于调试（限制长度避免日志过大）
		preview := response
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		logger.Warnf("VLLM response contains no valid JSON. Raw response preview: %s", preview)
		return nil, fmt.Errorf("no valid JSON found in response")
	}

	// 解析 JSON
	var data struct {
		Description  string  `json:"description"`
		Caption      string  `json:"caption"`
		MainCategory string  `json:"main_category"`
		Tags         string  `json:"tags"`
		MemoryScore  float64 `json:"memory_score"`
		BeautyScore  float64 `json:"beauty_score"`
		Reason       string  `json:"reason"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
		// 记录原始 JSON 和错误
		logger.Warnf("Failed to unmarshal JSON: %v. JSON content: %s", err, jsonStr)
		return nil, fmt.Errorf("unmarshal json: %w", err)
	}

	// 验证必填字段
	if data.Description == "" || data.Caption == "" || data.MainCategory == "" {
		return nil, fmt.Errorf("missing required fields in response")
	}

	return &AnalyzeResult{
		Description:  data.Description,
		Caption:      data.Caption,
		MainCategory: data.MainCategory,
		Tags:         data.Tags,
		MemoryScore:  data.MemoryScore,
		BeautyScore:  data.BeautyScore,
		Reason:       data.Reason,
		Provider:     p.Name(),
	}, nil
}
