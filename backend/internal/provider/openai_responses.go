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

// OpenAIResponsesConfig OpenAI Responses API 配置
type OpenAIResponsesConfig struct {
	APIKey   string `yaml:"api_key"`  // API Key
	Endpoint string `yaml:"endpoint"` // API 地址
	Model    string `yaml:"model"`    // 模型名称
	Timeout  int    `yaml:"timeout"`  // 超时（秒）

	// 提示词配置（可选，为空时使用默认提示词）
	AnalysisPrompt string `yaml:"analysis_prompt,omitempty"` // 分析提示词
	CaptionPrompt  string `yaml:"caption_prompt,omitempty"`  // 文案生成提示词
}

// OpenAIResponsesProvider OpenAI Responses API 提供者
type OpenAIResponsesProvider struct {
	config *OpenAIResponsesConfig
	client *http.Client
}

type openAIResponsesResponse struct {
	Model      string `json:"model"`
	OutputText string `json:"output_text"`
	Output     []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// NewOpenAIResponsesProvider 创建 OpenAI Responses provider
func NewOpenAIResponsesProvider(config *OpenAIResponsesConfig) (*OpenAIResponsesProvider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("openai responses api_key is required")
	}
	if config.Endpoint == "" {
		config.Endpoint = "https://api.openai.com/v1/responses"
	}
	if config.Model == "" {
		config.Model = "gpt-5.4"
	}
	if config.Timeout == 0 {
		config.Timeout = 60
	}

	return &OpenAIResponsesProvider{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
	}, nil
}

// Name 返回 provider 名称
func (p *OpenAIResponsesProvider) Name() string {
	return "openai_responses"
}

// Cost 返回单次调用成本
func (p *OpenAIResponsesProvider) Cost() float64 {
	return 0.07
}

// IsAvailable 检查服务是否可用
func (p *OpenAIResponsesProvider) IsAvailable() bool {
	return p.config.APIKey != ""
}

// MaxConcurrency 最大并发数
func (p *OpenAIResponsesProvider) MaxConcurrency() int {
	return 5
}

// SupportsBatch 是否支持批量分析
func (p *OpenAIResponsesProvider) SupportsBatch() bool {
	return false
}

// MaxBatchSize 最大批量大小
func (p *OpenAIResponsesProvider) MaxBatchSize() int {
	return 1
}

// AnalyzeBatch 批量分析照片（Responses API 逐个处理）
func (p *OpenAIResponsesProvider) AnalyzeBatch(requests []*AnalyzeRequest) ([]*AnalyzeResult, error) {
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
func (p *OpenAIResponsesProvider) BatchCost() float64 {
	return p.Cost()
}

// Analyze 分析照片
func (p *OpenAIResponsesProvider) Analyze(request *AnalyzeRequest) (*AnalyzeResult, error) {
	startTime := time.Now()
	resp, err := p.generateText(p.buildPrompt(request), request.ImageData)
	if err != nil {
		return nil, err
	}

	result, err := p.parseResponse(resp.text())
	if err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	result.Provider = p.Name()
	result.ModelName = resp.Model
	result.Timestamp = time.Now()
	result.Duration = time.Since(startTime)
	result.TokensUsed = resp.Usage.TotalTokens
	result.Cost = p.usageCost(resp)

	logger.Infof("OpenAI Responses analysis completed: model=%s, tokens=%d, cost=¥%.4f, duration=%v",
		result.ModelName, result.TokensUsed, result.Cost, result.Duration)

	return result, nil
}

// GenerateCaption 生成照片文案（第二次会话）
func (p *OpenAIResponsesProvider) GenerateCaption(request *AnalyzeRequest) (string, error) {
	prompt := p.config.CaptionPrompt
	if prompt == "" {
		prompt = DefaultCaptionPrompt
	}

	resp, err := p.generateText(prompt, request.ImageData)
	if err != nil {
		return "", err
	}

	caption := strings.TrimSpace(resp.text())
	caption = strings.Trim(caption, `"'`)

	if len(caption) < 5 {
		return "", fmt.Errorf("caption too short")
	}
	if len(caption) > 100 {
		caption = caption[:100]
	}

	return caption, nil
}

func (p *OpenAIResponsesProvider) generateText(prompt string, imageData []byte) (*openAIResponsesResponse, error) {
	imageURL := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(imageData)

	reqBody := map[string]interface{}{
		"model": p.config.Model,
		"input": []map[string]interface{}{
			{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type": "input_text",
						"text": prompt,
					},
					{
						"type":      "input_image",
						"image_url": imageURL,
					},
				},
			},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", p.config.Endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	httpResp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, NewTransportError(p.Name(), isTimeoutErr(err), err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, NewTransportError(p.Name(), false, err)
	}

	if httpResp.StatusCode != http.StatusOK {
		// NewHTTPError 期望读取 resp.Body，这里已读出 body，构造时重新包装。
		pe := &ProviderError{
			Provider:   p.Name(),
			StatusCode: httpResp.StatusCode,
			BodySummary: sanitizeBody(string(body), 500),
		}
		if ra := httpResp.Header.Get("Retry-After"); ra != "" {
			if d, ok := parseRetryAfter(ra); ok {
				pe.RetryAfter = d
			}
		}
		return nil, pe
	}

	resp, err := parseOpenAIResponsesBody(body, httpResp.Header.Get("Content-Type"))
	if err != nil {
		return nil, NewResponseInvalidError(p.Name(), "decode response: "+err.Error())
	}
	if resp.text() == "" {
		return nil, NewResponseInvalidError(p.Name(), "no response from openai responses api")
	}
	return resp, nil
}

func (r *openAIResponsesResponse) text() string {
	if r.OutputText != "" {
		return r.OutputText
	}
	for _, item := range r.Output {
		for _, content := range item.Content {
			if content.Text != "" {
				return content.Text
			}
		}
	}
	return ""
}

func parseOpenAIResponsesBody(body []byte, contentType string) (*openAIResponsesResponse, error) {
	if strings.Contains(contentType, "text/event-stream") || strings.HasPrefix(strings.TrimSpace(string(body)), "event:") {
		return parseOpenAIResponsesStream(body)
	}

	var resp openAIResponsesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func parseOpenAIResponsesStream(body []byte) (*openAIResponsesResponse, error) {
	resp := &openAIResponsesResponse{}
	var output strings.Builder

	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var event struct {
			Type     string                   `json:"type"`
			Delta    string                   `json:"delta"`
			Text     string                   `json:"text"`
			Response *openAIResponsesResponse `json:"response"`
		}
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			return nil, err
		}

		if event.Response != nil {
			if event.Response.Model != "" {
				resp.Model = event.Response.Model
			}
			if event.Response.OutputText != "" {
				resp.OutputText = event.Response.OutputText
			}
			if event.Response.Usage.InputTokens != 0 || event.Response.Usage.OutputTokens != 0 || event.Response.Usage.TotalTokens != 0 {
				resp.Usage = event.Response.Usage
			}
		}

		switch event.Type {
		case "response.output_text.delta":
			output.WriteString(event.Delta)
		case "response.output_text.done":
			resp.OutputText = event.Text
		}
	}

	if resp.OutputText == "" {
		resp.OutputText = output.String()
	}
	return resp, nil
}

func (p *OpenAIResponsesProvider) usageCost(resp *openAIResponsesResponse) float64 {
	inputCost := float64(resp.Usage.InputTokens) / 1000.0 * 0.01
	outputCost := float64(resp.Usage.OutputTokens) / 1000.0 * 0.03
	return (inputCost + outputCost) * 7.0
}

// buildPrompt 构建提示词（第一次会话，不含caption）
func (p *OpenAIResponsesProvider) buildPrompt(request *AnalyzeRequest) string {
	prompt := p.config.AnalysisPrompt
	if prompt == "" {
		prompt = DefaultAnalysisPrompt
	}

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
- main_category 必须从以下选项中选择（只能是这13个之一）：人物、孩子、猫咪、家庭、旅行、风景、美食、宠物、日常、文档、杂物、截屏、其他
- 禁止使用英文分类如 "event", "people", "landscape" 等
- 不要输出任何多余文字，不要加注释。`

	return prompt
}

// parseResponse 解析 AI 响应（第一次会话，不含caption）
func (p *OpenAIResponsesProvider) parseResponse(response string) (*AnalyzeResult, error) {
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return nil, fmt.Errorf("no valid JSON found in response")
	}

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

	if data.Description == "" || data.MainCategory == "" {
		return nil, fmt.Errorf("missing required fields in response")
	}

	return &AnalyzeResult{
		Description:  data.Description,
		MainCategory: mapCategoryToChineseOpenAI(data.MainCategory),
		Tags:         data.Tags,
		MemoryScore:  data.MemoryScore,
		BeautyScore:  data.BeautyScore,
		Reason:       data.Reason,
		Provider:     p.Name(),
	}, nil
}
