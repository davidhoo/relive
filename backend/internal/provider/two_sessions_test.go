package provider

import (
	"strings"
	"testing"
)

// TestBuildCaptionPrompt 测试第二次会话的prompt构建
func TestBuildCaptionPrompt(t *testing.T) {
	provider := &QwenProvider{
		config: &QwenConfig{
			Model:       "qwen-vl-max",
			Temperature: 0.7,
		},
	}

	prompt := provider.buildCaptionPrompt()

	// 验证prompt只要求看照片生成文案，不依赖分析结果
	if strings.Contains(prompt, "描述") && strings.Contains(prompt, "分类") {
		t.Error("Second session prompt should NOT depend on first analysis result")
	}
	if !strings.Contains(prompt, "电子相框") {
		t.Error("Prompt should mention electronic photo frame")
	}
	if !strings.Contains(prompt, "画外之意") {
		t.Error("Prompt should contain creativity instruction about 'meaning beyond the image'")
	}
	if !strings.Contains(prompt, "8-24") {
		t.Error("Prompt should specify length requirement")
	}
}

// TestParseCaptionResponse 测试文案响应解析
func TestParseCaptionResponse(t *testing.T) {
	provider := &QwenProvider{}

	tests := []struct {
		name     string
		response string
		want     string
		wantErr  bool
	}{
		{
			name:     "plain text caption",
			response: "愿有岁月可回首，且以深情共白头",
			want:     "愿有岁月可回首，且以深情共白头",
			wantErr:  false,
		},
		{
			name:     "caption with quotes",
			response: `"夕阳无限好，只是近黄昏"`,
			want:     "夕阳无限好，只是近黄昏",
			wantErr:  false,
		},
		{
			name:     "caption with JSON wrapper",
			response: `{"caption": "海内存知己，天涯若比邻"}`,
			want:     "海内存知己，天涯若比邻",
			wantErr:  false,
		},
		{
			name:     "caption with whitespace",
			response: "  春风十里不如你  ",
			want:     "春风十里不如你",
			wantErr:  false,
		},
		{
			name:     "empty caption",
			response: "",
			want:     "",
			wantErr:  true,
		},
		{
			name:     "caption too short",
			response: "Hi",
			want:     "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := provider.parseCaptionResponse(tt.response)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCaptionResponse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseCaptionResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAnalyzePromptWithoutCaption 测试第一次会话的prompt不包含caption要求
func TestAnalyzePromptWithoutCaption(t *testing.T) {
	provider := &QwenProvider{
		config: &QwenConfig{
			Model:       "qwen-vl-max",
			Temperature: 0.7,
		},
	}

	request := &AnalyzeRequest{
		ImagePath: "/test/photo.jpg",
		ExifInfo: &ExifInfo{
			DateTime: "2024-06-15 18:30:00",
			City:     "三亚",
		},
	}

	prompt := provider.buildPrompt(request)

	// 验证第一次会话的prompt不包含caption要求
	if strings.Contains(prompt, "caption") {
		t.Error("First session prompt should NOT contain caption requirement")
	}

	// 验证包含其他必要字段
	if !strings.Contains(prompt, "description") {
		t.Error("Prompt should contain description requirement")
	}
	if !strings.Contains(prompt, "main_category") {
		t.Error("Prompt should contain main_category requirement")
	}
	if !strings.Contains(prompt, "memory_score") {
		t.Error("Prompt should contain memory_score requirement")
	}
}

// TestTwoSessionsIntegration 测试两次会话的集成流程（模拟）
func TestTwoSessionsIntegration(t *testing.T) {
	// 模拟第一次会话结果
	firstResult := &AnalyzeResult{
		Description:  "照片中是一对新人在海边举行婚礼，夕阳西下...",
		MainCategory: "event",
		Tags:         "婚礼,海边,夕阳",
		MemoryScore:  92,
		BeautyScore:  88,
		Reason:       "珍贵的婚礼时刻",
	}

	// 验证第一次会话结果包含必要信息但不包含caption
	if firstResult.Description == "" {
		t.Error("First session should return description")
	}
	if firstResult.MainCategory == "" {
		t.Error("First session should return main_category")
	}

	// 模拟第二次会话：生成文案（只看照片，不给第一次结果）
	// 在实际场景中，这会调用 provider.GenerateCaption(request) - 不传firstResult
	caption := "愿有岁月可回首，且以深情共白头"

	// 验证文案长度要求
	if len(caption) < 8 {
		t.Error("Caption should be at least 8 characters")
	}
	if len(caption) > 100 {
		t.Error("Caption should not exceed 100 characters")
	}

	// 最终完整结果
	finalResult := &AnalyzeResult{
		Description:  firstResult.Description,
		Caption:      caption,
		MainCategory: firstResult.MainCategory,
		Tags:         firstResult.Tags,
		MemoryScore:  firstResult.MemoryScore,
		BeautyScore:  firstResult.BeautyScore,
		Reason:       firstResult.Reason,
	}

	// 验证最终结果完整
	if finalResult.Caption == "" {
		t.Error("Final result should have caption")
	}
	if finalResult.Description == "" {
		t.Error("Final result should have description")
	}
}
