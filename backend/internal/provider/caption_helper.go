package provider

import (
	"fmt"
	"strings"
)

const fallbackCaptionMaxRunes = 30

// EnsureCaption returns a non-empty caption for an analysis result.
//
// If the analysis result already contains a caption, it is reused directly.
// Otherwise, it triggers the provider's second-stage caption generation. When
// caption generation fails, a short fallback is derived from the description.
func EnsureCaption(aiProvider AIProvider, request *AnalyzeRequest, result *AnalyzeResult) (string, error) {
	if result == nil {
		return "", fmt.Errorf("analysis result is required")
	}

	if caption := strings.TrimSpace(result.Caption); caption != "" {
		return caption, nil
	}

	if aiProvider == nil {
		return fallbackCaption(result.Description), fmt.Errorf("ai provider is required")
	}

	caption, err := aiProvider.GenerateCaption(request)
	caption = strings.TrimSpace(caption)
	if err == nil && caption != "" {
		return caption, nil
	}

	if err == nil {
		err = fmt.Errorf("generated caption is empty")
	}

	return fallbackCaption(result.Description), err
}

func fallbackCaption(description string) string {
	description = strings.TrimSpace(description)
	if description == "" {
		return ""
	}

	runes := []rune(description)
	if len(runes) <= fallbackCaptionMaxRunes {
		return description
	}

	return string(runes[:fallbackCaptionMaxRunes])
}
