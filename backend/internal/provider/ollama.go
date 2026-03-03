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

// GenerateCaption 生成照片文案（第二次会话）
// 只看照片，直接生成创意文案，不给第一次分析结果
func (p *OllamaProvider) GenerateCaption(request *AnalyzeRequest) (string, error) {
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

	// 映射英文分类到中文（防止 AI 未按提示词返回）
	mainCategory := mapCategoryToChineseOllama(data.MainCategory)

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

// mapCategoryToChineseOllama 将英文分类映射到中文
func mapCategoryToChineseOllama(category string) string {
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
