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
	Endpoint       string  `yaml:"endpoint"`        // VLLM 服务地址
	Model          string  `yaml:"model"`           // 模型名称
	Temperature    float64 `yaml:"temperature"`     // 温度参数
	Timeout        int     `yaml:"timeout"`         // 超时（秒）
	MaxTokens      int     `yaml:"max_tokens"`      // 最大 tokens
	Concurrency    int     `yaml:"concurrency"`     // 并发数（批量分析时）
	EnableThinking bool    `yaml:"enable_thinking"` // 是否启用思考，默认 false

	// 提示词配置（可选，为空时使用默认提示词）
	AnalysisPrompt string `yaml:"analysis_prompt,omitempty"` // 分析提示词
	CaptionPrompt  string `yaml:"caption_prompt,omitempty"`  // 文案生成提示词
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

	// 如果未启用思考，添加 chat_template_kwargs 参数
	if !p.config.EnableThinking {
		reqBody["chat_template_kwargs"] = map[string]interface{}{
			"enable_thinking": false,
		}
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

// buildPrompt 构建提示词（第一次会话，不含caption）
func (p *VLLMProvider) buildPrompt(request *AnalyzeRequest) string {
	// 使用配置的提示词，如果没有则使用默认提示词
	prompt := p.config.AnalysisPrompt
	if prompt == "" {
		prompt = DefaultAnalysisPrompt
	}

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

请严格只输出 JSON，格式如下：
{
  "description": "详细描述照片内容（80-200字）",
  "main_category": "人物",
  "tags": "标签（逗号分隔），如：旅游,美食,家人,朋友,户外,室内",
  "memory_score": 85.0,
  "beauty_score": 88.0,
  "reason": "不超过40字的中文理由"
}

【重要约束】
- main_category 必须从以下选项中选择（只能是这12个之一）：人物、孩子、猫咪、家庭、旅行、风景、美食、宠物、日常、文档、杂物、其他
- 禁止使用英文分类如 "event", "people", "landscape" 等
- 不要输出任何多余文字，不要加注释。`

	return prompt
}

// GenerateCaption 生成照片文案（第二次会话）
// 只看照片，直接生成创意文案，不给第一次分析结果
func (p *VLLMProvider) GenerateCaption(request *AnalyzeRequest) (string, error) {
	// 使用配置的提示词，如果没有则使用默认提示词
	prompt := p.config.CaptionPrompt
	if prompt == "" {
		prompt = DefaultCaptionPrompt
	}

	imageBase64 := base64.StdEncoding.EncodeToString(request.ImageData)
	imageURL := fmt.Sprintf("data:image/jpeg;base64,%s", imageBase64)

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
		"max_tokens":  100,
		"temperature": 0.9,
	}

	// 如果未启用思考，添加 chat_template_kwargs 参数
	if !p.config.EnableThinking {
		reqBody["chat_template_kwargs"] = map[string]interface{}{
			"enable_thinking": false,
		}
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", p.config.Endpoint+"/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vllm api error: %s, body: %s", resp.Status, string(body))
	}

	var vllmResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&vllmResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(vllmResp.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	caption := strings.TrimSpace(vllmResp.Choices[0].Message.Content)
	caption = strings.Trim(caption, `"'`)

	if len(caption) < 5 {
		return "", fmt.Errorf("caption too short")
	}
	if len(caption) > 100 {
		caption = caption[:100]
	}

	return caption, nil
}

// parseResponse 解析 AI 响应（第一次会话，不含caption）
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

	// 验证必填字段（第一次会话不返回caption）
	if data.Description == "" || data.MainCategory == "" {
		return nil, fmt.Errorf("missing required fields in response")
	}

	// 映射英文分类到中文（防止 AI 未按提示词返回）
	mainCategory := mapCategoryToChinese(data.MainCategory)

	return &AnalyzeResult{
		Description:  data.Description,
		MainCategory: mainCategory,
		Tags:         data.Tags,
		MemoryScore:  data.MemoryScore,
		BeautyScore:  data.BeautyScore,
		Reason:       data.Reason,
		Provider:     p.Name(),
	}, nil
}

// mapCategoryToChinese 将英文分类映射到中文
func mapCategoryToChinese(category string) string {
	// 如果已经是中文，直接返回
	validCategories := []string{"人物", "孩子", "猫咪", "家庭", "旅行", "风景", "美食", "宠物", "日常", "文档", "杂物", "其他"}
	for _, valid := range validCategories {
		if category == valid {
			return category
		}
	}

	// 英文到中文的映射
	mapping := map[string]string{
		"person":      "人物",
		"people":      "人物",
		"human":       "人物",
		"child":       "孩子",
		"kid":         "孩子",
		"baby":        "孩子",
		"cat":         "猫咪",
		"kitten":      "猫咪",
		"family":      "家庭",
		"travel":      "旅行",
		"trip":        "旅行",
		"landscape":   "风景",
		"scenery":     "风景",
		"nature":      "风景",
		"food":        "美食",
		"meal":        "美食",
		"pet":         "宠物",
		"dog":         "宠物",
		"daily":       "日常",
		"life":        "日常",
		"document":    "文档",
		"receipt":     "文档",
		"bill":        "文档",
		"screenshot":  "文档",
		"trash":       "杂物",
		"junk":        "杂物",
		"clutter":     "杂物",
		"other":       "其他",
		"others":      "其他",
		"event":       "日常",
		"activity":    "日常",
		"party":       "家庭",
		"celebration": "家庭",
	}

	// 尝试小写匹配
	lower := strings.ToLower(category)
	if mapped, ok := mapping[lower]; ok {
		return mapped
	}

	// 如果无法映射，返回"其他"
	logger.Warnf("Unknown category '%s', mapping to '其他'", category)
	return "其他"
}
