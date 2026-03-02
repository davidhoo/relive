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
		config.Timeout = 120  // 默认 120 秒，支持 qwen3.5-plus 等复杂模型
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

// BatchCost 返回批量调用时每张照片的成本（批量更便宜）
func (p *QwenProvider) BatchCost() float64 {
	// 批量处理有轻微折扣，约 15% 节省
	return 0.0034
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

// SupportsBatch 是否支持批量分析
func (p *QwenProvider) SupportsBatch() bool {
	return true
}

// MaxBatchSize 最大批量大小
func (p *QwenProvider) MaxBatchSize() int {
	return 8 // Qwen 建议最多 8 张图片一批
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

// AnalyzeBatch 批量分析照片
// Qwen 支持在一次请求中发送多张图片
func (p *QwenProvider) AnalyzeBatch(requests []*AnalyzeRequest) ([]*AnalyzeResult, error) {
	if len(requests) == 0 {
		return []*AnalyzeResult{}, nil
	}

	startTime := time.Now()

	// 构建批量 prompt
	prompt := p.buildBatchPrompt(len(requests))

	// 构建 content，包含所有图片
	content := make([]map[string]interface{}, 0, len(requests)*2)

	for i, req := range requests {
		// 添加图片标记
		imageBase64 := base64.StdEncoding.EncodeToString(req.ImageData)
		imageURL := "data:image/jpeg;base64," + imageBase64

		content = append(content, map[string]interface{}{
			"text": fmt.Sprintf("\n[图片 %d]\n", i+1),
		})
		content = append(content, map[string]interface{}{
			"image": imageURL,
		})

		// 添加 EXIF 信息
		if req.ExifInfo != nil {
			exifText := ""
			if req.ExifInfo.DateTime != "" {
				exifText += fmt.Sprintf("拍摄时间：%s\n", req.ExifInfo.DateTime)
			}
			if req.ExifInfo.City != "" {
				exifText += fmt.Sprintf("拍摄地点：%s\n", req.ExifInfo.City)
			}
			if req.ExifInfo.Model != "" {
				exifText += fmt.Sprintf("相机型号：%s\n", req.ExifInfo.Model)
			}
			if exifText != "" {
				content = append(content, map[string]interface{}{
					"text": exifText,
				})
			}
		}
	}

	// 最后添加主 prompt
	content = append(content, map[string]interface{}{
		"text": prompt,
	})

	// 构建请求
	reqBody := map[string]interface{}{
		"model": p.config.Model,
		"input": map[string]interface{}{
			"messages": []map[string]interface{}{
				{
					"role":    "user",
					"content": content,
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

	// 解析批量响应
	results, err := p.parseBatchResponse(responseText, len(requests))
	if err != nil {
		return nil, fmt.Errorf("parse batch response: %w", err)
	}

	// 计算实际成本（批量有折扣）
	totalTokens := qwenResp.Usage.InputTokens + qwenResp.Usage.OutputTokens
	actualCost := float64(totalTokens) / 1000.0 * 0.02
	perPhotoCost := actualCost / float64(len(requests))

	duration := time.Since(startTime)

	// 填充元数据
	for i, result := range results {
		result.Provider = p.Name()
		result.ModelName = p.config.Model
		result.Timestamp = time.Now()
		result.Duration = duration / time.Duration(len(requests))
		result.Cost = perPhotoCost
		if i == 0 {
			result.TokensUsed = totalTokens / len(requests) // 近似值
		}
	}

	logger.Infof("Qwen batch analysis completed: photos=%d, model=%s, tokens=%d, cost=¥%.4f, duration=%v",
		len(requests), p.config.Model, totalTokens, actualCost, duration)

	return results, nil
}

// buildBatchPrompt 构建批量分析的 prompt
func (p *QwenProvider) buildBatchPrompt(count int) string {
	prompt := fmt.Sprintf(`请分析上面的 %d 张照片，每张照片以 JSON 对象返回分析结果，所有结果放入一个 JSON 数组中。`, count)

	prompt += `

每张照片的分析要求：
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

请严格按照以下 JSON 格式返回结果数组（不要有任何其他文字）：
[
  {
    "description": "...",
    "caption": "...",
    "main_category": "...",
    "tags": "...",
    "memory_score": 85,
    "beauty_score": 90,
    "reason": "..."
  },
  ...
]`

	return prompt
}

// extractJSONArray 从文本中提取 JSON 数组
func extractJSONArray(text string) string {
	// 查找第一个 [ 和最后一个 ]
	start := -1
	end := -1

	for i, ch := range text {
		if ch == '[' && start == -1 {
			start = i
		}
		if ch == ']' {
			end = i
		}
	}

	if start != -1 && end != -1 && end > start {
		return text[start : end+1]
	}

	// 如果没有找到数组，尝试提取对象
	return extractJSON(text)
}

// parseBatchResponse 解析批量分析响应
func (p *QwenProvider) parseBatchResponse(response string, expectedCount int) ([]*AnalyzeResult, error) {
	// 尝试提取 JSON 数组
	jsonStr := extractJSONArray(response)
	if jsonStr == "" {
		return nil, fmt.Errorf("no valid JSON found in response")
	}

	// 尝试解析为数组
	var dataArray []struct {
		Description  string  `json:"description"`
		Caption      string  `json:"caption"`
		MainCategory string  `json:"main_category"`
		Tags         string  `json:"tags"`
		MemoryScore  float64 `json:"memory_score"`
		BeautyScore  float64 `json:"beauty_score"`
		Reason       string  `json:"reason"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &dataArray); err != nil {
		// 尝试解析为单个对象（兼容旧格式）
		var singleData struct {
			Description  string  `json:"description"`
			Caption      string  `json:"caption"`
			MainCategory string  `json:"main_category"`
			Tags         string  `json:"tags"`
			MemoryScore  float64 `json:"memory_score"`
			BeautyScore  float64 `json:"beauty_score"`
			Reason       string  `json:"reason"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &singleData); err != nil {
			return nil, fmt.Errorf("unmarshal json: %w", err)
		}
		// 包装成数组
		dataArray = append(dataArray, singleData)
	}

	// 验证结果数量
	if len(dataArray) != expectedCount {
		logger.Warnf("Batch response count mismatch: expected %d, got %d", expectedCount, len(dataArray))
		// 如果数量不匹配，填充空结果
		for len(dataArray) < expectedCount {
			dataArray = append(dataArray, struct {
				Description  string  `json:"description"`
				Caption      string  `json:"caption"`
				MainCategory string  `json:"main_category"`
				Tags         string  `json:"tags"`
				MemoryScore  float64 `json:"memory_score"`
				BeautyScore  float64 `json:"beauty_score"`
				Reason       string  `json:"reason"`
			}{
				Description:  "分析失败",
				Caption:      "",
				MainCategory: "other",
				Tags:         "",
				MemoryScore:  50,
				BeautyScore:  50,
				Reason:       "批量分析响应数量不匹配",
			})
		}
	}

	// 转换为 AnalyzeResult
	results := make([]*AnalyzeResult, len(dataArray))
	for i, data := range dataArray {
		results[i] = &AnalyzeResult{
			Description:  data.Description,
			Caption:      data.Caption,
			MainCategory: data.MainCategory,
			Tags:         data.Tags,
			MemoryScore:  data.MemoryScore,
			BeautyScore:  data.BeautyScore,
			Reason:       data.Reason,
		}
	}

	return results, nil
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
