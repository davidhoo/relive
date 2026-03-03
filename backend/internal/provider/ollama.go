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

// OllamaConfig Ollama 配置
type OllamaConfig struct {
	Endpoint    string  `yaml:"endpoint"`    // Ollama API 地址
	Model       string  `yaml:"model"`       // 模型名称（如 llava:13b）
	Temperature float64 `yaml:"temperature"` // 温度参数
	Timeout     int     `yaml:"timeout"`     // 超时（秒）
}

// OllamaProvider Ollama 提供者
type OllamaProvider struct {
	config *OllamaConfig
	client *http.Client
}

// NewOllamaProvider 创建 Ollama provider
func NewOllamaProvider(config *OllamaConfig) (*OllamaProvider, error) {
	if config.Endpoint == "" {
		config.Endpoint = "http://localhost:11434" // 默认地址
	}
	if config.Model == "" {
		config.Model = "llava:13b" // 默认模型
	}
	if config.Temperature == 0 {
		config.Temperature = 0.7
	}
	if config.Timeout == 0 {
		config.Timeout = 60
	}

	return &OllamaProvider{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
	}, nil
}

// Name 返回 provider 名称
func (p *OllamaProvider) Name() string {
	return "ollama"
}

// Cost 返回单次调用成本（免费）
func (p *OllamaProvider) Cost() float64 {
	return 0.0
}

// IsAvailable 检查服务是否可用
func (p *OllamaProvider) IsAvailable() bool {
	req, err := http.NewRequest("GET", p.config.Endpoint+"/api/tags", nil)
	if err != nil {
		return false
	}

	// 创建一个带超时的 client
	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// MaxConcurrency 最大并发数
func (p *OllamaProvider) MaxConcurrency() int {
	return 1 // Ollama 本地运行，建议单线程
}

// SupportsBatch 是否支持批量分析
func (p *OllamaProvider) SupportsBatch() bool {
	return false // Ollama 不支持批量分析
}

// MaxBatchSize 最大批量大小
func (p *OllamaProvider) MaxBatchSize() int {
	return 1
}

// AnalyzeBatch 批量分析照片（Ollama 不支持，逐个处理）
func (p *OllamaProvider) AnalyzeBatch(requests []*AnalyzeRequest) ([]*AnalyzeResult, error) {
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

// BatchCost 批量处理成本（Ollama 免费）
func (p *OllamaProvider) BatchCost() float64 {
	return 0.0
}

// Analyze 分析照片
func (p *OllamaProvider) Analyze(request *AnalyzeRequest) (*AnalyzeResult, error) {
	startTime := time.Now()

	// 构建 prompt
	prompt := p.buildPrompt(request)

	// 将图片转换为 base64
	imageBase64 := base64.StdEncoding.EncodeToString(request.ImageData)

	// 构建请求
	reqBody := map[string]interface{}{
		"model":  p.config.Model,
		"prompt": prompt,
		"images": []string{imageBase64},
		"stream": false,
		"options": map[string]interface{}{
			"temperature": p.config.Temperature,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// 发送请求
	httpReq, err := http.NewRequest("POST", p.config.Endpoint+"/api/generate", bytes.NewBuffer(jsonData))
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
		return nil, fmt.Errorf("ollama api error: %s, body: %s", resp.Status, string(body))
	}

	// 解析响应
	var ollamaResp struct {
		Response string `json:"response"`
		Model    string `json:"model"`
		Done     bool   `json:"done"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// 解析 AI 响应
	result, err := p.parseResponse(ollamaResp.Response)
	if err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// 填充元数据
	result.Provider = p.Name()
	result.ModelName = ollamaResp.Model
	result.Timestamp = time.Now()
	result.Duration = time.Since(startTime)
	result.Cost = p.Cost()

	logger.Infof("Ollama analysis completed: model=%s, duration=%v", result.ModelName, result.Duration)

	return result, nil
}

// buildPrompt 构建提示词（第一次会话，不含caption）
func (p *OllamaProvider) buildPrompt(request *AnalyzeRequest) string {
	prompt := `请分析这张照片，并以 JSON 格式返回分析结果。

要求：
1. description: 详细描述照片内容（80-200字），包括人物、场景、活动、氛围等
2. main_category: 主要分类，从以下8个中选择：
   - portrait（人物/肖像）
   - group（集体/合影）
   - landscape（风景）
   - cityscape（城市）
   - food（美食）
   - pet（宠物）
   - event（事件/活动）
   - other（其他）
3. tags: 标签（逗号分隔），如：旅游,美食,家人,朋友,户外,室内等
4. memory_score: 回忆价值评分（0-100），评估纪念意义和情感价值
5. beauty_score: 美观度评分（0-100），评估构图、光线、色彩等摄影质量
6. reason: 评分理由（40字内）

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
请严格按照以下 JSON 格式返回结果（不要有任何其他文字）：
{
  "description": "...",
  "main_category": "...",
  "tags": "...",
  "memory_score": 85,
  "beauty_score": 90,
  "reason": "..."
}`

	return prompt
}

// GenerateCaption 生成照片文案（第二次会话）
// 只看照片，直接生成创意文案，不给第一次分析结果
func (p *OllamaProvider) GenerateCaption(request *AnalyzeRequest) (string, error) {
	prompt := `请仔细看这张照片，为它创作一句富有感染力的文案。

要求：
1. 简短精炼（8-30字），适合电子相框展示
2. 富有情感和艺术感，能唤起回忆
3. 可以是一句诗、一句歌词、一个感悟，或一段温暖的话
4. 不要描述性的文字，而是有温度的表达
5. 充分发挥你的创造力，让文案独特而动人

请直接返回文案内容（不需要JSON格式），只返回一句话：`

	imageBase64 := base64.StdEncoding.EncodeToString(request.ImageData)

	reqBody := map[string]interface{}{
		"model":  p.config.Model,
		"prompt": prompt,
		"images": []string{imageBase64},
		"stream": false,
		"options": map[string]interface{}{
			"temperature": 0.9, // 更高的temperature增加创意
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", p.config.Endpoint+"/api/generate", bytes.NewBuffer(jsonData))
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
		return "", fmt.Errorf("ollama api error: %s, body: %s", resp.Status, string(body))
	}

	var ollamaResp struct {
		Response string `json:"response"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	caption := strings.TrimSpace(ollamaResp.Response)
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
func (p *OllamaProvider) parseResponse(response string) (*AnalyzeResult, error) {
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

// extractJSON 从文本中提取 JSON
func extractJSON(text string) string {
	// 查找第一个 { 和最后一个 }
	start := -1
	end := -1

	for i, ch := range text {
		if ch == '{' && start == -1 {
			start = i
		}
		if ch == '}' {
			end = i
		}
	}

	if start != -1 && end != -1 && end > start {
		return text[start : end+1]
	}

	return ""
}
