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

// QwenConfig Qwen 配置
type QwenConfig struct {
	APIKey      string  `yaml:"api_key"`     // API Key
	Endpoint    string  `yaml:"endpoint"`    // API 地址
	Model       string  `yaml:"model"`       // 模型名称（qwen-vl-max/qwen-vl-plus）
	Temperature float64 `yaml:"temperature"` // 温度参数
	Timeout     int     `yaml:"timeout"`     // 超时（秒）
}

// QwenProvider Qwen 提供者
type QwenProvider struct {
	config *QwenConfig
	client *http.Client
}

// NewQwenProvider 创建 Qwen provider
func NewQwenProvider(config *QwenConfig) (*QwenProvider, error) {
	if config.APIKey == "" {
		return nil, fmt.Errorf("qwen api_key is required")
	}
	if config.Endpoint == "" {
		config.Endpoint = "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"
	}
	if config.Model == "" {
		config.Model = "qwen-vl-max"
	}
	if config.Temperature == 0 {
		config.Temperature = 0.7
	}
	if config.Timeout == 0 {
		config.Timeout = 60
	}

	return &QwenProvider{
		config: config,
		client: &http.Client{
			Timeout: time.Duration(config.Timeout) * time.Second,
		},
	}, nil
}

// Name 返回 provider 名称
func (p *QwenProvider) Name() string {
	return "qwen"
}

// Cost 返回单次调用成本
func (p *QwenProvider) Cost() float64 {
	// Qwen-VL-Max: ¥0.02/1000 tokens (约 200 tokens/图)
	return 0.004
}

// IsAvailable 检查服务是否可用
func (p *QwenProvider) IsAvailable() bool {
	// 简单的健康检查（可选）
	return p.config.APIKey != ""
}

// MaxConcurrency 最大并发数
func (p *QwenProvider) MaxConcurrency() int {
	return 10 // Qwen API 支持较高并发
}

// Analyze 分析照片
func (p *QwenProvider) Analyze(request *AnalyzeRequest) (*AnalyzeResult, error) {
	startTime := time.Now()

	// 构建 prompt
	prompt := p.buildPrompt(request)

	// 将图片转换为 base64
	imageBase64 := base64.StdEncoding.EncodeToString(request.ImageData)
	imageURL := "data:image/jpeg;base64," + imageBase64

	// 构建请求
	reqBody := map[string]interface{}{
		"model": p.config.Model,
		"input": map[string]interface{}{
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": []map[string]interface{}{
						{
							"image": imageURL,
						},
						{
							"text": prompt,
						},
					},
				},
			},
		},
		"parameters": map[string]interface{}{
			"temperature": p.config.Temperature,
		},
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
		return nil, fmt.Errorf("qwen api error: %s, body: %s", resp.Status, string(body))
	}

	// 解析响应
	var qwenResp struct {
		Output struct {
			Choices []struct {
				Message struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&qwenResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(qwenResp.Output.Choices) == 0 || len(qwenResp.Output.Choices[0].Message.Content) == 0 {
		return nil, fmt.Errorf("no response from qwen api")
	}

	responseText := qwenResp.Output.Choices[0].Message.Content[0].Text

	// 解析 AI 响应
	result, err := p.parseResponse(responseText)
	if err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// 计算实际成本
	totalTokens := qwenResp.Usage.InputTokens + qwenResp.Usage.OutputTokens
	actualCost := float64(totalTokens) / 1000.0 * 0.02

	// 填充元数据
	result.Provider = p.Name()
	result.ModelName = p.config.Model
	result.Timestamp = time.Now()
	result.Duration = time.Since(startTime)
	result.TokensUsed = totalTokens
	result.Cost = actualCost

	logger.Infof("Qwen analysis completed: model=%s, tokens=%d, cost=¥%.4f, duration=%v",
		result.ModelName, totalTokens, actualCost, result.Duration)

	return result, nil
}

// buildPrompt 构建提示词
func (p *QwenProvider) buildPrompt(request *AnalyzeRequest) string {
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
请严格按照以下 JSON 格式返回结果（不要有任何其他文字）：
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
func (p *QwenProvider) parseResponse(response string) (*AnalyzeResult, error) {
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
