package provider

import (
	"strings"
	"testing"
)

func TestCategoryMappingsSupportScreenshot(t *testing.T) {
	tests := []struct {
		name string
		mapFn func(string) string
	}{
		{name: "qwen", mapFn: mapCategoryToChineseQwen},
		{name: "openai", mapFn: mapCategoryToChineseOpenAI},
		{name: "ollama", mapFn: mapCategoryToChineseOllama},
		{name: "vllm", mapFn: mapCategoryToChinese},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mapFn("screenshot"); got != "截屏" {
				t.Fatalf("mapFn(screenshot) = %q, want %q", got, "截屏")
			}
			if got := tt.mapFn("截屏"); got != "截屏" {
				t.Fatalf("mapFn(截屏) = %q, want %q", got, "截屏")
			}
		})
	}
}

func TestProvidersPromptIncludeScreenshotCategory(t *testing.T) {
	request := &AnalyzeRequest{}
	tests := []struct {
		name   string
		prompt string
	}{
		{
			name:   "qwen",
			prompt: (&QwenProvider{config: &QwenConfig{}}).buildPrompt(request),
		},
		{
			name:   "openai",
			prompt: (&OpenAIProvider{config: &OpenAIConfig{}}).buildPrompt(request),
		},
		{
			name:   "ollama",
			prompt: (&OllamaProvider{config: &OllamaConfig{}}).buildPrompt(request),
		},
		{
			name:   "vllm",
			prompt: (&VLLMProvider{config: &VLLMConfig{}}).buildPrompt(request),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(tt.prompt, "截屏") {
				t.Fatalf("prompt should contain 截屏 category")
			}
			if !strings.Contains(tt.prompt, "以下13个") {
				t.Fatalf("prompt should use updated category count")
			}
		})
	}
}

func TestDefaultPromptsUseUpdatedCategoryCount(t *testing.T) {
	if !strings.Contains(DefaultAnalysisPrompt, "以下13个") {
		t.Fatal("DefaultAnalysisPrompt should use updated category count")
	}
	if !strings.Contains(DefaultBatchPrompt, "以下13个") {
		t.Fatal("DefaultBatchPrompt should use updated category count")
	}
}
