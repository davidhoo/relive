package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// OpenAIConfig OpenAI 配置
type OpenAIConfig struct {
	APIKey      string  `yaml:"api_key"`     // API Key
	Endpoint    string  `yaml:"endpoint"`    // API 地址
	Model       string  `yaml:"model"`       // 模型名称（gpt-4-vision-preview）
	Temperature float64 `yaml:"temperature"` // 温度参数
	MaxTokens   int     `yaml:"max_tokens"`  // 最大 tokens
	Timeout     int     `yaml:"timeout"`     // 超时（秒）

	// 提示词配置（可选，为空时使用默认提示词）
	AnalysisPrompt string `yaml:"analysis_prompt,omitempty"` // 分析提示词
	CaptionPrompt  string `yaml:"caption_prompt,omitempty"`  // 文案生成提示词
}

// OpenAIProvider OpenAI 提供者
type OpenAIProvider struct {
	config *OpenAIConfig
	client *http.Client
}

// NewOpenAIProvider 创建 OpenAI provider
func NewOpenAIProvider(config *OpenAIConfig) (*OpenAIProvider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("openai api_key is required")
	}
	if config.Endpoint == "" {
		config.Endpoint = "https://api.openai.com/v1/chat/completions"
	}
	if config.Model == "" {
		config.Model = "gpt-4-vision-preview"
	}
	if config.Temperature == 0 {
		config.Temperature = 0.7
	}
	if config.MaxTokens == 0 {
		config.MaxTokens = 1000
	}
	if config.Timeout == 0 {
		config.Timeout = 60
	}

	return &OpenAIProvider{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
	}, nil
}

// Name 返回 provider 名称
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Cost 返回单次调用成本
func (p *OpenAIProvider) Cost() float64 {
	// GPT-4V: $0.01/1K input tokens, $0.03/1K output tokens
	// 平均约 300 tokens，成本约 $0.01 ≈ ¥0.07
	return 0.07
}

// IsAvailable 检查服务是否可用
func (p *OpenAIProvider) IsAvailable() bool {
	return p.config.APIKey != ""
}

// MaxConcurrency 最大并发数
func (p *OpenAIProvider) MaxConcurrency() int {
	return 5 // OpenAI API 有速率限制
}

// SupportsBatch 是否支持批量分析
func (p *OpenAIProvider) SupportsBatch() bool {
	return false // OpenAI Vision 不支持多图批量分析
}

// MaxBatchSize 最大批量大小
func (p *OpenAIProvider) MaxBatchSize() int {
	return 1
}

// AnalyzeBatch 批量分析照片（OpenAI 不支持多图，逐个处理）
func (p *OpenAIProvider) AnalyzeBatch(requests []*AnalyzeRequest) ([]*AnalyzeResult, error) {
	results := make([]*AnalyzeResult, 0, len(requests))
	for _, req := range requests {
		result, err := p.Analyze(req)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// BatchCost 批量处理成本
func (p *OpenAIProvider) BatchCost() float64 {
	// OpenAI 批量处理没有折扣
	return p.Cost()
}

// Analyze 分析照片
func (p *OpenAIProvider) Analyze(request *AnalyzeRequest) (*AnalyzeResult, error) {
	startTime := time.Now()

	// 构建 prompt
	prompt := p.buildPrompt(request)

	// 将图片转换为 base64
	imageBase64 := base64.StdEncoding.EncodeToString(request.ImageData)
	imageURL := "data:image/jpeg;base64," + imageBase64

	// 构建请求
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
						"image_url": map[string]interface{}{
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

	// 发送请求
	httpReq, err := http.NewRequest("POST", p.config.Endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai api error: %s, body: %s", resp.Status, string(body))
	}

	// 解析响应
	var openaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(openaiResp.Choices) == 0 {
		return nil, fmt.Errorf("no response from openai api")
	}

	responseText := openaiResp.Choices[0].Message.Content

	// 解析 AI 响应
	result, err := p.parseResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// 计算实际成本
	inputCost := float64(openaiResp.Usage.PromptTokens) / 1000.0 * 0.01
	outputCost := float64(openaiResp.Usage.CompletionTokens) / 1000.0 * 0.03
	actualCost := (inputCost + outputCost) * 7.0 // 转换为人民币（汇率约7）

	// 填充元数据
	result.Provider = p.Name()
	result.ModelName = openaiResp.Model
	result.Timestamp = time.Now()
	result.Duration = time.Since(startTime)
	result.TokensUsed = openaiResp.Usage.TotalTokens
	result.Cost = actualCost

	logger.Infof("OpenAI analysis completed: model=%s, tokens=%d, cost=¥%.4f, duration=%v",
		result.ModelName, result.TokensUsed, actualCost, result.Duration)

	return result, nil
}

// buildPrompt 构建提示词（第一次会话，不含caption）
func (p *OpenAIProvider) buildPrompt(request *AnalyzeRequest) string {
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
func (p *OpenAIProvider) GenerateCaption(request *AnalyzeRequest) (string, error) {
	// 使用配置的提示词，如果没有则使用默认提示词
	prompt := p.config.CaptionPrompt
	if prompt == "" {
		prompt = DefaultCaptionPrompt
	}

	// 构建请求 - 第二次会话（新会话）
	imageBase64 := base64.StdEncoding.EncodeToString(request.ImageData)
	imageURL := "data:image/jpeg;base64," + imageBase64

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
						"image_url": map[string]interface{}{
							"url": imageURL,
						},
					},
				},
			},
		},
		"max_tokens":  100,
		"temperature": 0.9, // 更高的temperature增加创意
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", p.config.Endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai api error: %s, body: %s", resp.Status, string(body))
	}

	var openaiResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(openaiResp.Choices) == 0 {
		return "", fmt.Errorf("no response from openai api")
	}

	caption := strings.TrimSpace(openaiResp.Choices[0].Message.Content)
	caption = strings.Trim(caption, `"'`) // 移除可能的引号

	if len(caption) < 5 {
		return "", fmt.Errorf("caption too short")
	}
	if len(caption) > 100 {
		caption = caption[:100]
	}

	return caption, nil
}

// parseResponse 解析 AI 响应（第一次会话，不含caption）
func (p *OpenAIProvider) parseResponse(response string) (*AnalyzeResult, error) {
	// 尝试提取 JSON
	jsonStr := extractJSON(response)
	if jsonStr == "" {
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
		return nil, fmt.Errorf("unmarshal json: %w", err)
	}

	// 验证必填字段（第一次会话不返回caption）
	if data.Description == "" || data.MainCategory == "" {
		return nil, fmt.Errorf("missing required fields in response")
	}

	// 映射英文分类到中文（防止 AI 未按提示词返回）
	mainCategory := mapCategoryToChineseOpenAI(data.MainCategory)

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

// mapCategoryToChineseOpenAI 将英文分类映射到中文
func mapCategoryToChineseOpenAI(category string) string {
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
