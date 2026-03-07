package provider

import (
	"errors"
	"testing"
	"unicode/utf8"
)

type stubCaptionProvider struct {
	caption      string
	err          error
	captionCalls int
}

func (s *stubCaptionProvider) Analyze(request *AnalyzeRequest) (*AnalyzeResult, error) {
	return nil, nil
}

func (s *stubCaptionProvider) AnalyzeBatch(requests []*AnalyzeRequest) ([]*AnalyzeResult, error) {
	return nil, nil
}

func (s *stubCaptionProvider) GenerateCaption(request *AnalyzeRequest) (string, error) {
	s.captionCalls++
	return s.caption, s.err
}

func (s *stubCaptionProvider) Name() string {
	return "stub"
}

func (s *stubCaptionProvider) Cost() float64 {
	return 0
}

func (s *stubCaptionProvider) BatchCost() float64 {
	return 0
}

func (s *stubCaptionProvider) IsAvailable() bool {
	return true
}

func (s *stubCaptionProvider) MaxConcurrency() int {
	return 1
}

func (s *stubCaptionProvider) SupportsBatch() bool {
	return false
}

func (s *stubCaptionProvider) MaxBatchSize() int {
	return 1
}

func TestEnsureCaptionUsesExistingCaption(t *testing.T) {
	provider := &stubCaptionProvider{caption: "不会被调用"}
	result := &AnalyzeResult{
		Description: "一张关于落日和海边的照片",
		Caption:     "已有标题",
	}

	caption, err := EnsureCaption(provider, &AnalyzeRequest{}, result)
	if err != nil {
		t.Fatalf("EnsureCaption returned error: %v", err)
	}
	if caption != "已有标题" {
		t.Fatalf("expected existing caption, got %q", caption)
	}
	if provider.captionCalls != 0 {
		t.Fatalf("expected GenerateCaption not to be called, got %d", provider.captionCalls)
	}
}

func TestEnsureCaptionGeneratesWhenMissing(t *testing.T) {
	provider := &stubCaptionProvider{caption: "晚风拂过的海岸线"}
	result := &AnalyzeResult{
		Description: "一张关于落日和海边的照片",
	}

	caption, err := EnsureCaption(provider, &AnalyzeRequest{}, result)
	if err != nil {
		t.Fatalf("EnsureCaption returned error: %v", err)
	}
	if caption != "晚风拂过的海岸线" {
		t.Fatalf("expected generated caption, got %q", caption)
	}
	if provider.captionCalls != 1 {
		t.Fatalf("expected GenerateCaption to be called once, got %d", provider.captionCalls)
	}
}

func TestEnsureCaptionFallsBackToDescription(t *testing.T) {
	provider := &stubCaptionProvider{err: errors.New("caption too short")}
	result := &AnalyzeResult{
		Description: "这是一段很长很长的中文描述，用来验证回退标题会按照 rune 安全截断，而不是把中文字符截坏掉。",
	}

	caption, err := EnsureCaption(provider, &AnalyzeRequest{}, result)
	if err == nil {
		t.Fatal("expected caption generation error")
	}
	if provider.captionCalls != 1 {
		t.Fatalf("expected GenerateCaption to be called once, got %d", provider.captionCalls)
	}
	if !utf8.ValidString(caption) {
		t.Fatalf("expected valid UTF-8 fallback caption, got %q", caption)
	}
	if len([]rune(caption)) != fallbackCaptionMaxRunes {
		t.Fatalf("expected fallback caption to be %d runes, got %d", fallbackCaptionMaxRunes, len([]rune(caption)))
	}
	expected := string([]rune(result.Description)[:fallbackCaptionMaxRunes])
	if caption != expected {
		t.Fatalf("expected fallback caption %q, got %q", expected, caption)
	}
}
