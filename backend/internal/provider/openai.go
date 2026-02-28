package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

// buildPrompt 构建提示词
func (p *OpenAIProvider) buildPrompt(request *AnalyzeRequest) string {
	prompt := `Analyze this photo and return the result in JSON format.

Requirements:
1. description: Detailed description in Chinese (80-200 characters), including people, scenes, activities, atmosphere, etc.
2. caption: Short beautiful caption in Chinese (8-30 characters), suitable for display in a photo frame
3. main_category: Main category, choose from the following 8:
   - portrait (人物/肖像)
   - group (集体/合影)
   - landscape (风景)
   - cityscape (城市)
   - food (美食)
   - pet (宠物)
   - event (事件/活动)
   - other (其他)
4. tags: Tags in Chinese (comma separated), such as: 旅游,美食,家人,朋友,户外,室内
5. memory_score: Memory value score (0-100), assess commemorative significance and emotional value
6. beauty_score: Beauty score (0-100), assess composition, lighting, color and other photographic qualities
7. reason: Scoring reason in Chinese (within 40 characters)

`

	// 添加 EXIF 信息
	if request.ExifInfo != nil {
		if request.ExifInfo.DateTime != "" {
			prompt += fmt.Sprintf("Photo taken at: %s\n", request.ExifInfo.DateTime)
		}
		if request.ExifInfo.City != "" {
			prompt += fmt.Sprintf("Location: %s\n", request.ExifInfo.City)
		}
		if request.ExifInfo.Model != "" {
			prompt += fmt.Sprintf("Camera: %s\n", request.ExifInfo.Model)
		}
	}

	prompt += `
Please return the result strictly in the following JSON format (without any other text):
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
func (p *OpenAIProvider) parseResponse(response string) (*AnalyzeResult, error) {
	// 尝试提取 JSON
	jsonStr := extractJSON(response)
	if jsonStr == "" {
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
	}, nil
}
