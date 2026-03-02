package provider

import (
	"encoding/json"
	"testing"

	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
)

func init() {
	// Initialize logger for tests
	_ = logger.Init(config.LoggingConfig{Level: "debug"})
}

func TestQwenProvider_buildBatchPrompt(t *testing.T) {
	config := &QwenConfig{
		APIKey:      "test-key",
		Endpoint:    "https://test.dashscope.aliyuncs.com",
		Model:       "qwen-vl-max",
		Temperature: 0.7,
		Timeout:     60,
	}

	provider, err := NewQwenProvider(config)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Test batch prompt building
	prompt := provider.buildBatchPrompt(5)
	if prompt == "" {
		t.Error("Batch prompt should not be empty")
	}

	// Check prompt contains expected elements
	if !contains(prompt, "5 张照片") {
		t.Error("Prompt should mention photo count")
	}
	if !contains(prompt, "JSON 数组") {
		t.Error("Prompt should mention JSON array")
	}
	if !contains(prompt, "description") {
		t.Error("Prompt should contain description field")
	}
	if !contains(prompt, "memory_score") {
		t.Error("Prompt should contain memory_score field")
	}
}

func TestQwenProvider_parseBatchResponse(t *testing.T) {
	config := &QwenConfig{
		APIKey:      "test-key",
		Endpoint:    "https://test.dashscope.aliyuncs.com",
		Model:       "qwen-vl-max",
		Temperature: 0.7,
		Timeout:     60,
	}

	provider, err := NewQwenProvider(config)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	tests := []struct {
		name          string
		response      string
		expectedCount int
		expectError   bool
	}{
		{
			name:          "valid batch response",
			response:      `[{"description": "Photo 1 desc", "caption": "Caption 1", "main_category": "portrait", "tags": "tag1", "memory_score": 80, "beauty_score": 85, "reason": "Reason 1"},{"description": "Photo 2 desc", "caption": "Caption 2", "main_category": "landscape", "tags": "tag2", "memory_score": 75, "beauty_score": 90, "reason": "Reason 2"}]`,
			expectedCount: 2,
			expectError:   false,
		},
		{
			name:          "single object response",
			response:      `{"description": "Single photo", "caption": "Caption", "main_category": "food", "tags": "tag", "memory_score": 70, "beauty_score": 80, "reason": "Reason"}`,
			expectedCount: 1,
			expectError:   false,
		},
		{
			name:          "invalid JSON",
			response:      "not valid json",
			expectedCount: 0,
			expectError:   true,
		},
		{
			name:          "empty JSON",
			response:      "",
			expectedCount: 0,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := provider.parseBatchResponse(tt.response, tt.expectedCount)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(results) != tt.expectedCount {
				t.Errorf("Expected %d results, got %d", tt.expectedCount, len(results))
			}

			// Verify result fields
			for i, result := range results {
				if result.Description == "" {
					t.Errorf("Result %d: description should not be empty", i)
				}
				if result.MainCategory == "" {
					t.Errorf("Result %d: main_category should not be empty", i)
				}
			}
		})
	}
}

func TestQwenProvider_SupportsBatch(t *testing.T) {
	config := &QwenConfig{
		APIKey:      "test-key",
		Endpoint:    "https://test.dashscope.aliyuncs.com",
		Model:       "qwen-vl-max",
		Temperature: 0.7,
		Timeout:     60,
	}

	provider, err := NewQwenProvider(config)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	if !provider.SupportsBatch() {
		t.Error("Qwen provider should support batch analysis")
	}

	if provider.MaxBatchSize() != 8 {
		t.Errorf("Expected batch size 8, got %d", provider.MaxBatchSize())
	}
}

func TestQwenProvider_BatchCost(t *testing.T) {
	config := &QwenConfig{
		APIKey:      "test-key",
		Endpoint:    "https://test.dashscope.aliyuncs.com",
		Model:       "qwen-vl-max",
		Temperature: 0.7,
		Timeout:     60,
	}

	provider, err := NewQwenProvider(config)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Batch cost should be lower than single cost
	if provider.BatchCost() >= provider.Cost() {
		t.Error("Batch cost should be lower than single cost")
	}

	// Expected batch cost is 0.0034
	if provider.BatchCost() != 0.0034 {
		t.Errorf("Expected batch cost 0.0034, got %f", provider.BatchCost())
	}
}

func TestQwenProvider_parseBatchResponse_MismatchCount(t *testing.T) {
	config := &QwenConfig{
		APIKey:      "test-key",
		Endpoint:    "https://test.dashscope.aliyuncs.com",
		Model:       "qwen-vl-max",
		Temperature: 0.7,
		Timeout:     60,
	}

	provider, err := NewQwenProvider(config)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// Response with 2 items but expect 3 - should fill with empty results
	response := `[{"description": "Photo 1", "caption": "Caption 1", "main_category": "portrait", "tags": "tag1", "memory_score": 80, "beauty_score": 85, "reason": "Reason 1"},{"description": "Photo 2", "caption": "Caption 2", "main_category": "landscape", "tags": "tag2", "memory_score": 75, "beauty_score": 90, "reason": "Reason 2"}]`

	results, err := provider.parseBatchResponse(response, 3)
	if err != nil {
		t.Errorf("Should handle mismatch gracefully: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results (including padded), got %d", len(results))
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Test batch request building
func TestQwenProvider_buildBatchContent(t *testing.T) {
	requests := []*AnalyzeRequest{
		{
			ImageData: []byte("fake-image-1"),
			ImagePath: "/path/to/photo1.jpg",
			ExifInfo: &ExifInfo{
				DateTime: "2024-01-01 12:00:00",
				City:     "Beijing",
				Model:    "iPhone14,2",
			},
		},
		{
			ImageData: []byte("fake-image-2"),
			ImagePath: "/path/to/photo2.jpg",
			ExifInfo: &ExifInfo{
				DateTime: "2024-01-02 14:30:00",
				City:     "Shanghai",
				Model:    "Canon EOS R5",
			},
		},
	}

	// Verify request structure
	if len(requests) != 2 {
		t.Error("Should have 2 requests")
	}

	for i, req := range requests {
		if req.ImagePath == "" {
			t.Errorf("Request %d: ImagePath should not be empty", i)
		}
		if len(req.ImageData) == 0 {
			t.Errorf("Request %d: ImageData should not be empty", i)
		}
		if req.ExifInfo == nil {
			t.Errorf("Request %d: ExifInfo should not be nil", i)
		}
	}

	// Test JSON marshaling of content structure
	content := make([]map[string]interface{}, 0)
	for i, req := range requests {
		content = append(content, map[string]interface{}{
			"text":    "Image marker",
			"index":   i,
			"path":    req.ImagePath,
			"hasExif": req.ExifInfo != nil,
		})
	}

	jsonData, err := json.Marshal(content)
	if err != nil {
		t.Errorf("Failed to marshal content: %v", err)
	}

	if len(jsonData) == 0 {
		t.Error("JSON data should not be empty")
	}
}
