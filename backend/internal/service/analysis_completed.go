package service

import (
	"strings"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/pkg/logger"
)

// AnalysisCompletedHandler receives committed photo analysis results. The
// implementation must be idempotent because startup reconciliation may replay
// the same photo after a notification failure.
type AnalysisCompletedHandler interface {
	HandleAnalysisCompleted(photoID uint) error
}

func peopleExclusionFields(mainCategory string) map[string]interface{} {
	if strings.TrimSpace(mainCategory) == model.PhotoMainCategoryScreenshot {
		return map[string]interface{}{
			"people_excluded":         true,
			"people_exclusion_reason": model.PeopleExclusionReasonScreenshot,
		}
	}
	return map[string]interface{}{
		"people_excluded":         false,
		"people_exclusion_reason": "",
	}
}

func notifyAnalysisCompleted(handler AnalysisCompletedHandler, photoID uint) {
	if handler == nil {
		return
	}
	if err := handler.HandleAnalysisCompleted(photoID); err != nil {
		logger.Errorf("handle committed analysis for photo %d failed: %v", photoID, err)
	}
}
