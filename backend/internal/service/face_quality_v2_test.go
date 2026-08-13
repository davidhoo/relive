package service

import (
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/stretchr/testify/assert"
)

func th() v2QualityThresholds { return defaultV2QualityThresholds() }

// 场景1：低分主检测 + no_face → 自动 non_face（enforce）。
func TestEvaluateFaceQualityV2_LowScoreNoFace_AutoNonFace(t *testing.T) {
	ev := &model.FaceQualityEvidenceV2{
		EvidenceSchemaVersion: "independent_v2",
		PrimaryDetectorScore:  0.3, // < 0.65
		VerificationStatus:    "no_face",
		FaceBoxWidthPx:        60, // >= 48
		FaceBoxHeightPx:       60,
	}

	// enforce → non_face。
	got := evaluateFaceQualityV2(ev, "enforce", th())
	assert.Equal(t, model.FaceQualityDecisionNonFace, got.Decision)
	assert.Equal(t, model.ExclusionReasonNonFace, got.Reason)

	// shadow → review_required + 建议决策 non_face。
	gotShadow := evaluateFaceQualityV2(ev, "shadow", th())
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, gotShadow.Decision)
	assert.Equal(t, model.FaceQualityDecisionNonFace, gotShadow.SuggestedDecision)
	assert.Empty(t, gotShadow.Reason, "shadow 不得写排除 reason")
}

// 场景2：已确认脸(face) + 极小原图框 → low_quality。
func TestEvaluateFaceQualityV2_FaceTooSmall_LowQuality(t *testing.T) {
	ev := &model.FaceQualityEvidenceV2{
		EvidenceSchemaVersion: "independent_v2",
		PrimaryDetectorScore:  0.9,
		VerificationStatus:    "face",
		FaceBoxWidthPx:        30, // < 48
		FaceBoxHeightPx:       30,
	}

	got := evaluateFaceQualityV2(ev, "enforce", th())
	assert.Equal(t, model.FaceQualityDecisionLowQuality, got.Decision)
	assert.Equal(t, model.ExclusionReasonLowQuality, got.Reason)
	assert.Contains(t, got.ReasonCodes, "too_small_original")

	gotShadow := evaluateFaceQualityV2(ev, "shadow", th())
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, gotShadow.Decision)
	assert.Equal(t, model.FaceQualityDecisionLowQuality, gotShadow.SuggestedDecision)
}

// 场景3a：高分主检测 + no_face → review_required（主/独立分歧，不自动 non_face）。
func TestEvaluateFaceQualityV2_HighScoreNoFace_ReviewRequired(t *testing.T) {
	ev := &model.FaceQualityEvidenceV2{
		EvidenceSchemaVersion: "independent_v2",
		PrimaryDetectorScore:  0.8, // >= 0.65 准入线
		VerificationStatus:    "no_face",
		FaceBoxWidthPx:        60,
		FaceBoxHeightPx:       60,
	}
	got := evaluateFaceQualityV2(ev, "enforce", th())
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, got.Decision)
	assert.Contains(t, got.ReasonCodes, "detector_verifier_disagreement")
}

// 场景3b：低分主检测 + no_face 但原图短边<48（极小未确认框）→ review_required。
func TestEvaluateFaceQualityV2_TinyUnconfirmedBox_ReviewRequired(t *testing.T) {
	ev := &model.FaceQualityEvidenceV2{
		EvidenceSchemaVersion: "independent_v2",
		PrimaryDetectorScore:  0.3,
		VerificationStatus:    "no_face",
		FaceBoxWidthPx:        20, // < 48
		FaceBoxHeightPx:       20,
	}
	got := evaluateFaceQualityV2(ev, "enforce", th())
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, got.Decision)
}

// 场景3c：验证器 uncertain → review_required。
func TestEvaluateFaceQualityV2_Uncertain_ReviewRequired(t *testing.T) {
	ev := &model.FaceQualityEvidenceV2{
		EvidenceSchemaVersion: "independent_v2",
		PrimaryDetectorScore:  0.3,
		VerificationStatus:    "uncertain",
		FaceBoxWidthPx:        60,
		FaceBoxHeightPx:       60,
		ReasonCodes:           []string{"input_too_small"},
	}
	got := evaluateFaceQualityV2(ev, "enforce", th())
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, got.Decision)
}

// 场景3d：验证器 error → review_required（技术失败由 worker 层写 technical_error）。
func TestEvaluateFaceQualityV2_Error_ReviewRequired(t *testing.T) {
	ev := &model.FaceQualityEvidenceV2{
		EvidenceSchemaVersion: "independent_v2",
		PrimaryDetectorScore:  0.3,
		VerificationStatus:    "error",
		FaceBoxWidthPx:        60,
		FaceBoxHeightPx:       60,
	}
	got := evaluateFaceQualityV2(ev, "enforce", th())
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, got.Decision)
}

// 场景4：face + 质量达标 → accepted。
func TestEvaluateFaceQualityV2_FaceGoodQuality_Accepted(t *testing.T) {
	ev := &model.FaceQualityEvidenceV2{
		EvidenceSchemaVersion: "independent_v2",
		PrimaryDetectorScore:  0.9,
		VerificationStatus:    "face",
		FaceBoxWidthPx:        80,
		FaceBoxHeightPx:       80,
		SharpnessNorm:         100, // 默认阈值 0，达标
		BrightnessNorm:        120,
		Occluded:              false,
	}
	got := evaluateFaceQualityV2(ev, "enforce", th())
	assert.Equal(t, model.FaceQualityDecisionAccepted, got.Decision)
}

// nil 证据 → review_required（fail-closed）。
func TestEvaluateFaceQualityV2_NilEvidence_ReviewRequired(t *testing.T) {
	got := evaluateFaceQualityV2(nil, "enforce", th())
	assert.Equal(t, model.FaceQualityDecisionReviewRequired, got.Decision)
}

// disabled 模式：不自动判定，non_face/low_quality 候选都 accept。
func TestEvaluateFaceQualityV2_DisabledAcceptsAll(t *testing.T) {
	ev := &model.FaceQualityEvidenceV2{
		PrimaryDetectorScore: 0.3,
		VerificationStatus:   "no_face",
		FaceBoxWidthPx:       60,
		FaceBoxHeightPx:      60,
	}
	got := evaluateFaceQualityV2(ev, "disabled", th())
	assert.Equal(t, model.FaceQualityDecisionAccepted, got.Decision)
}
