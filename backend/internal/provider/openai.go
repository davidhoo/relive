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
	prompt := `You are a "Personal Photo Album Evaluation Assistant", skilled at understanding real photos and scoring them from both memory value and aesthetic perspectives.

You will receive a photo. Your tasks are:
1) Describe the photo content in detail in Chinese (80-200 characters), including people, scenes, activities, atmosphere, etc.
2) Determine the general type of photo, choose one: portrait/child/cat/family/travel/landscape/food/pet/daily/document/misc/other
3) Give a "memory_score" of 0-100 (precise to one decimal) for "worth remembering"
4) Give a "beauty_score" of 0-100 (precise to one decimal) for "aesthetic degree"
5) Provide 3-8 tags, comma-separated, e.g.: travel,food,family,friends,outdoor,indoor
6) Explain the reason in short Chinese (within 40 characters)

【Memory Score (memory_score) Evaluation Method】
First determine the "score range" based on how memorable it is, then fine-tune:

Score Range Determination:
- Trash/random shot/meaningless record: below 40.0 (usually 0-25; if barely recognizable but no story, don't exceed 39.9)
- Slightly memorable: centered at 65.0 (mostly 58.1-70.3)
- Good memory value: centered at 75 (mostly 68.7-82.4)
- Particularly wonderful, strongly worth cherishing: centered at 85 (mostly 79.1-95.9)

Fine-tuning Bonus Items (can stack):
- People & Relationships: Large face area in frame, people interacting, or group photo → significantly increase score
- Event-based: Birthday/party/ceremony/stage/obvious event → slightly increase score
- Scarcity & Irreproducibility: Obviously "this moment is hard to recreate" → significantly increase score
- Emotional Intensity: Laughing, crying, surprise, hugging, interaction, strong atmosphere → slightly increase score
- Beautiful Scenery: Magnificent natural scenery or exquisite, orderly composition → slightly increase score
- Travel Significance: Different location, landmark, travel scene → slightly increase score
- Image Quality: Unclear, blurry, ghosting, out of focus → slightly decrease score

【Key Photo Handling】
If the frame contains: children/cats/pets, these themes are more likely to have high memory value. Please center at 75 points and significantly increase the score.

【Obviously Low-Value Image Handling】
For the following low-value images, memory_score must be suppressed to 0-25 (maximum not exceeding 39):
- Nude, vulgar, pornographic or violating public order and good customs
- Bills, receipts, advertisements, random shots of clutter, test images, screenshots

【Beauty Score (beauty_score) Evaluation Method】
Beauty score only evaluates visuals: composition, lighting, clarity, color, subject prominence. Don't let "child/cat/travel" themes kidnap the beauty score - theme doesn't equal beauty.

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
Please strictly output JSON only, in the following format:
{
  "description": "Detailed description in Chinese (80-200 characters)",
  "main_category": "Choose one: portrait/group/landscape/cityscape/food/pet/event/other",
  "tags": "Comma-separated tags, e.g.: travel,food,family,friends,outdoor,indoor",
  "memory_score": 85.0,
  "beauty_score": 88.0,
  "reason": "Chinese reason within 40 characters"
}
Do not output any extra text, no comments.`

	return prompt
}

// GenerateCaption 生成照片文案（第二次会话）
// 只看照片，直接生成创意文案，不给第一次分析结果
func (p *OpenAIProvider) GenerateCaption(request *AnalyzeRequest) (string, error) {
	// 构建第二次会话的prompt - 只给照片，不给分析结果
	prompt := `You are a Chinese copywriting assistant writing sidebar short sentences for an "Electronic Photo Frame".
Your goal is not to describe the scene, but to add a little "meaning beyond the image" for it.

Creative Principles:
1. Avoid using these words: world, dream, time, years, gentle, healing, just right, quietly, slowly, etc. (but not absolutely forbidden)
2. Strictly prohibited sentence patterns:
   - ...within...the whole world/summer
   - ...as... (simple metaphor)
   - ...than...even... / ...more than...even more...
3. Only associate based on information that can be confirmed in the image. Don't fabricate time, character relationships, or event backgrounds.
4. Copy should be natural, interesting, with a bit of humor or poetry, but please avoid sentimentalism or chicken soup.
5. Don't retell the scene content itself, but write "the sentence that comes to mind after seeing the image".
6. Can lean towards one of the following styles:
   - Subtle emotions in daily life
   - Slight self-mockery or cold humor
   - Implicit feelings about time, memory, moments
   - A seemingly plain but meaningful judgment
7. Avoid template expressions like elementary school compositions.

Format Requirements:
1. Only output one Chinese short sentence, no line breaks, no quotation marks, no explanations.
2. Recommended length 8-24 Chinese characters, maximum not exceeding 30 Chinese characters.
3. Do not use words referring to the photo itself such as "this photo", "this moment", "that day".

Please create a sidebar short sentence for this photo:`

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

	return &AnalyzeResult{
		Description:  data.Description,
		MainCategory: data.MainCategory,
		Tags:         data.Tags,
		MemoryScore:  data.MemoryScore,
		BeautyScore:  data.BeautyScore,
		Reason:       data.Reason,
		Provider:     p.Name(),
	}, nil
}
