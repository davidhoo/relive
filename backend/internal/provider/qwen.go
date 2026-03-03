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
	return p.analyzeWithCaption(request)
}

// analyzeWithCaption 分析照片并生成文案（两次会话）
func (p *QwenProvider) analyzeWithCaption(request *AnalyzeRequest) (*AnalyzeResult, error) {
	startTime := time.Now()
	totalTokens := 0

	// ========== 第一次会话：分析照片 ==========
	logger.Debugf("Starting first session: photo analysis")
	analysisResult, tokens1, err := p.analyzePhoto(request)
	if err != nil {
		return nil, fmt.Errorf("photo analysis failed: %w", err)
	}
	totalTokens += tokens1

	// ========== 第二次会话：生成创意文案 ==========
	logger.Debugf("Starting second session: caption generation")
	caption, tokens2, err := p.generateCaption(request)
	if err != nil {
		// 如果文案生成失败，使用描述的一部分作为fallback
		logger.Warnf("Caption generation failed, using fallback: %v", err)
		if len(analysisResult.Description) > 30 {
			analysisResult.Caption = analysisResult.Description[:30]
		} else {
			analysisResult.Caption = analysisResult.Description
		}
	} else {
		analysisResult.Caption = caption
		totalTokens += tokens2
	}

	// 计算实际成本
	actualCost := float64(totalTokens) / 1000.0 * 0.02

	// 填充元数据
	analysisResult.Provider = p.Name()
	analysisResult.ModelName = p.config.Model
	analysisResult.Timestamp = time.Now()
	analysisResult.Duration = time.Since(startTime)
	analysisResult.TokensUsed = totalTokens
	analysisResult.Cost = actualCost

	logger.Infof("Qwen analysis completed (2 sessions): model=%s, tokens=%d, cost=¥%.4f, duration=%v",
		analysisResult.ModelName, totalTokens, actualCost, analysisResult.Duration)

	return analysisResult, nil
}

// analyzePhoto 第一次会话：分析照片
func (p *QwenProvider) analyzePhoto(request *AnalyzeRequest) (*AnalyzeResult, int, error) {
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
		return nil, 0, fmt.Errorf("marshal request: %w", err)
	}

	// 发送请求
	httpReq, err := http.NewRequest("POST", p.config.Endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("qwen api error: %s, body: %s", resp.Status, string(body))
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
		return nil, 0, fmt.Errorf("decode response: %w", err)
	}

	if len(qwenResp.Output.Choices) == 0 || len(qwenResp.Output.Choices[0].Message.Content) == 0 {
		return nil, 0, fmt.Errorf("no response from qwen api")
	}

	responseText := qwenResp.Output.Choices[0].Message.Content[0].Text

	// 解析 AI 响应
	result, err := p.parseResponse(responseText)
	if err != nil {
		return nil, 0, fmt.Errorf("parse response: %w", err)
	}

	totalTokens := qwenResp.Usage.InputTokens + qwenResp.Usage.OutputTokens
	return result, totalTokens, nil
}

// generateCaption 第二次会话：生成创意文案（只看照片，不给分析结果）
func (p *QwenProvider) generateCaption(request *AnalyzeRequest) (string, int, error) {
	// 构建第二次会话的prompt - 只给照片，不给分析结果
	prompt := p.buildCaptionPrompt()

	// 将图片转换为 base64
	imageBase64 := base64.StdEncoding.EncodeToString(request.ImageData)
	imageURL := "data:image/jpeg;base64," + imageBase64

	// 构建请求 - 开启新的会话（不包含之前的上下文）
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
			"temperature": 0.9, // 第二次会话使用更高的temperature，增加创意性
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", 0, fmt.Errorf("marshal request: %w", err)
	}

	// 发送请求
	httpReq, err := http.NewRequest("POST", p.config.Endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", 0, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return "", 0, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", 0, fmt.Errorf("qwen api error: %s, body: %s", resp.Status, string(body))
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
		return "", 0, fmt.Errorf("decode response: %w", err)
	}

	if len(qwenResp.Output.Choices) == 0 || len(qwenResp.Output.Choices[0].Message.Content) == 0 {
		return "", 0, fmt.Errorf("no response from qwen api")
	}

	responseText := qwenResp.Output.Choices[0].Message.Content[0].Text

	// 解析文案响应
	caption, err := p.parseCaptionResponse(responseText)
	if err != nil {
		return "", 0, fmt.Errorf("parse caption response: %w", err)
	}

	totalTokens := qwenResp.Usage.InputTokens + qwenResp.Usage.OutputTokens

	return caption, totalTokens, nil
}

// buildCaptionPrompt 构建第二次会话的prompt（生成创意文案，只看照片）
func (p *QwenProvider) buildCaptionPrompt() string {
	prompt := `你是一位为「电子相框」撰写旁白短句的中文文案助手。
你的目标不是描述画面，而是为画面补上一点"画外之意"。

创作原则：
1. 避免使用以下词语：世界、梦、时光、岁月、温柔、治愈、刚刚好、悄悄、慢慢 等（但不是绝对禁止）
2. 严禁使用如下句式：
   - ……里……着整个世界/夏天
   - ……得像……（简单的比喻）
   - ……比……还…… / ……得比……更……
3. 只基于图片中能确定的信息进行联想，不要虚构时间、人物关系、事件背景
4. 文案应自然、有趣，带一点幽默或者诗意，但请避免煽情、鸡汤
5. 不要复述画面内容本身，而是写"看完画面后，心里多出来的一句话"
6. 可以偏向以下风格之一：
   - 日常中的微妙情绪
   - 轻微自嘲或冷幽默
   - 对时间、记忆、瞬间的含蓄感受
   - 看似平淡但有余味的一句判断
7. 避免小学生作文式的、套路式的模板化表达

格式要求：
1. 只输出一句中文短句，不要换行，不要引号，不要任何解释
2. 建议长度8-24个汉字，最多不超过30个汉字
3. 不要出现"这张照片""这一刻""那天"等指代照片本身的词

请为这张照片创作一句旁白短句：`

	return prompt
}

// GenerateCaption 生成照片文案（第二次会话）- 实现AIProvider接口
// 只看照片，直接生成创意文案，不给第一次分析结果
func (p *QwenProvider) GenerateCaption(request *AnalyzeRequest) (string, error) {
	caption, _, err := p.generateCaption(request)
	if err != nil {
		return "", err
	}
	return caption, nil
}

// parseCaptionResponse 解析文案响应
func (p *QwenProvider) parseCaptionResponse(response string) (string, error) {
	// 清理响应文本
	caption := strings.TrimSpace(response)

	// 移除可能的引号
	caption = strings.Trim(caption, `"'`)

	// 移除可能的JSON标记
	if strings.Contains(caption, "{") && strings.Contains(caption, "}") {
		// 尝试提取JSON中的caption字段
		jsonStr := extractJSON(caption)
		if jsonStr != "" {
			var data struct {
				Caption string `json:"caption"`
			}
			if err := json.Unmarshal([]byte(jsonStr), &data); err == nil && data.Caption != "" {
				caption = data.Caption
			}
		}
	}

	// 确保文案长度合适
	if len(caption) < 5 {
		return "", fmt.Errorf("caption too short")
	}
	if len(caption) > 100 {
		caption = caption[:100]
	}

	return caption, nil
}

// buildPrompt 构建提示词（第一次会话：分析照片）
func (p *QwenProvider) buildPrompt(request *AnalyzeRequest) string {
	prompt := `你是"个人相册照片评估助手"，擅长理解真实照片的内容，并从回忆价值和美观角度打分。
你会收到一张照片，你的任务是：
1）用中文详细描述照片内容（80-200字），包括人物、场景、活动、氛围等
2）判断照片的大致类型，必须从以下选项中只选其一（禁止使用英文）：人物/孩子/猫咪/家庭/旅行/风景/美食/宠物/日常/文档/杂物/其他
3）给出0-100的"值得回忆度"memory_score（精确到一位小数）
4）给出0-100的"美观程度"beauty_score（精确到一位小数）
5）给出3-8个标签，用逗号分隔，如：旅游,美食,家人,朋友,户外,室内
6）用简短中文reason解释原因（不超过40字）

【值得回忆度（memory_score）评分方法】
请先按照值得回忆的程度，确定照片的"得分区间"，再进行精调：

得分区间判定：
- 垃圾/随手拍/无意义记录：40.0分以下（常见为0-25；若还能勉强辨认但无故事，也不要超过39.9）
- 稍微有点可回忆价值：以65.0分为中心（大多落在58.1-70.3）
- 不错的回忆价值：以75分为中心（大多落在68.7-82.4）
- 特别精彩、强烈值得珍藏：以85分为中心（大多落在79.1-95.9）

精调加分项（可同时叠加）：
- 人物与关系：画面中含有面积较大的人脸，有人物互动，或属于合影 → 大幅提高评分
- 事件性：生日/聚会/仪式/舞台/明显事件 → 少许提高评分
- 稀缺性与不可复现：明显"这一刻很难再来一次" → 大幅提高评分
- 情绪强度：笑、哭、惊喜、拥抱、互动、氛围强 → 少许提高评分
- 优美风景：画面中含有壮丽的自然风光，或精美、有秩序感的构图 → 少许提高评分
- 旅行意义：异地、地标、旅途情景 → 少许提高评分
- 画质：画面不清晰、模糊、有残影、虚焦 → 微微降低评分

【重点照片处理】
如果画面中含有：孩子/猫咪/宠物题材，这些主题更容易产生高回忆价值，请直接以75分为中心，并大幅提高评分

【明显低价值图片处理】
以下低价值图片，必须将memory_score压低到0-25（最多不超过39）：
- 裸露、低俗、色情或违反公序良俗的图片
- 账单、收据、广告、随手拍的杂物、测试图片、屏幕截图等

【美观分（beauty_score）评分方法】
美观分只评价视觉：构图、光线、清晰度、色彩、主体突出。不要被"孩子/猫/旅行"主题绑架美观分，主题不等于好看。

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

// buildBatchPrompt 构建批量分析的 prompt（第一次会话，不含caption）
func (p *QwenProvider) buildBatchPrompt(count int) string {
	prompt := fmt.Sprintf(`请分析上面的 %d 张照片，每张照片以 JSON 对象返回分析结果，所有结果放入一个 JSON 数组中。`, count)

	prompt += `

每张照片的分析要求：
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

请严格按照以下 JSON 格式返回结果数组（不要有任何其他文字）：
[
  {
    "description": "...",
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

// parseBatchResponse 解析批量分析响应（第一次会话，不含caption）
func (p *QwenProvider) parseBatchResponse(response string, expectedCount int) ([]*AnalyzeResult, error) {
	// 尝试提取 JSON 数组
	jsonStr := extractJSONArray(response)
	if jsonStr == "" {
		return nil, fmt.Errorf("no valid JSON found in response")
	}

	// 尝试解析为数组
	var dataArray []struct {
		Description  string  `json:"description"`
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
				MainCategory string  `json:"main_category"`
				Tags         string  `json:"tags"`
				MemoryScore  float64 `json:"memory_score"`
				BeautyScore  float64 `json:"beauty_score"`
				Reason       string  `json:"reason"`
			}{
				Description:  "分析失败",
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
			MainCategory: data.MainCategory,
			Tags:         data.Tags,
			MemoryScore:  data.MemoryScore,
			BeautyScore:  data.BeautyScore,
			Reason:       data.Reason,
			Provider:     p.Name(),
		}
	}

	return results, nil
}

// parseResponse 解析 AI 响应（第一次会话，不含caption）
func (p *QwenProvider) parseResponse(response string) (*AnalyzeResult, error) {
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
	mainCategory := mapCategoryToChineseQwen(data.MainCategory)

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

// mapCategoryToChineseQwen 将英文分类映射到中文
func mapCategoryToChineseQwen(category string) string {
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
