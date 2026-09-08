package service

import (
	"testing"

	"github.com/davidhoo/relive/pkg/config"
	"github.com/stretchr/testify/require"
)

func unitVec(vals ...float32) []float32 {
	return vals
}

func evidenceFromVectors(kind string, personID uint, vectors [][]float32, support int) IdentityEvidence {
	units := make([]IdentityEvidenceUnit, 0, len(vectors))
	for i, v := range vectors {
		units = append(units, IdentityEvidenceUnit{
			CenterID:      uint(i + 1),
			FaceID:        uint(i + 1),
			PersonID:      personID,
			Vector:        v,
			Weight:        1.0,
			SupportCount:  support,
			SimilarityP10: 0.5,
		})
	}
	return IdentityEvidence{Kind: kind, PersonID: personID, Units: units}
}

func TestScoreIdentityEvidence_Symmetric(t *testing.T) {
	left := evidenceFromVectors(identityEvidenceKindPerson, 1, [][]float32{
		unitVec(1, 0, 0),
		unitVec(0.9, 0.1, 0),
	}, 5)
	right := evidenceFromVectors(identityEvidenceKindPerson, 2, [][]float32{
		unitVec(1, 0.05, 0),
		unitVec(0.85, 0.15, 0),
	}, 5)

	ab := ScoreIdentityEvidence(left, right)
	ba := ScoreIdentityEvidence(right, left)
	require.NotEqual(t, IdentityMatchStatusInvalid, ab.Status)
	require.InDelta(t, ab.Score, ba.Score, 1e-9)
	require.InDelta(t, ab.ForwardScore, ba.ReverseScore, 1e-9)
	require.InDelta(t, ab.ReverseScore, ba.ForwardScore, 1e-9)
}

func TestScoreIdentityEvidence_HighCenterCannotHideWeakCoverage(t *testing.T) {
	// left has two centers; right only matches one strongly → reverse/forward min drops.
	left := evidenceFromVectors(identityEvidenceKindPerson, 1, [][]float32{
		unitVec(1, 0, 0),
		unitVec(0, 1, 0), // orthogonal center
	}, 5)
	right := evidenceFromVectors(identityEvidenceKindPerson, 2, [][]float32{
		unitVec(1, 0, 0),
	}, 5)

	pair := ScoreIdentityEvidence(left, right)
	require.NotEqual(t, IdentityMatchStatusInvalid, pair.Status)
	require.Less(t, pair.Score, pair.ForwardScore+1e-9)
	require.Less(t, pair.Score, 0.99)
}

func TestApplyIdentityStrategy_AutoVsSuggestSameRawScore(t *testing.T) {
	cfg := config.PeopleConfig{
		IdentityProfileRescueThreshold: 0.65,
		MergeSuggestionThreshold:       0.55,
		IdentityProfileMargin:          0.05,
		IdentityProfileMinCenterFaces:  3,
		IdentityProfileMinCenterPhotos: 2,
		IdentityProfileMaxCenters:      6,
	}
	auto := NewIdentityAutoStrategy(cfg)
	suggest := NewIdentitySuggestStrategy(cfg)

	margin := 0.1
	result := IdentityMatchResult{
		Status:           IdentityMatchStatusInsufficient,
		EngineVersion:    identityEngineVersion,
		BlockReason:      blockScoreBelowThreshold,
		Margin:           margin,
		MarginApplicable: true,
		Best: &IdentityCandidateResult{
			PersonID:        7,
			Score:           0.60, // between suggest and auto
			SupportingUnits: 2,
			MinSupportCount: 2,
			StableCenters:   true,
			Status:          IdentityMatchStatusInsufficient,
			BlockReason:     blockScoreBelowThreshold,
		},
	}

	autoDec := ApplyIdentityStrategy(result, auto)
	suggestDec := ApplyIdentityStrategy(result, suggest)
	require.False(t, autoDec.Accepted)
	require.True(t, suggestDec.Accepted)
	require.InDelta(t, 0.60, autoDec.Score, 1e-9)
	require.InDelta(t, 0.60, suggestDec.Score, 1e-9)
}

func TestApplyIdentityStrategy_HardConflictNeverAccepted(t *testing.T) {
	cfg := config.PeopleConfig{
		IdentityProfileRescueThreshold: 0.65,
		MergeSuggestionThreshold:       0.55,
		IdentityProfileMargin:          0.05,
		IdentityProfileMinCenterFaces:  3,
	}
	result := IdentityMatchResult{
		Status:           IdentityMatchStatusHardConflict,
		EngineVersion:    identityEngineVersion,
		BlockReason:      blockCannotLink,
		MarginApplicable: true,
		Margin:           0.2,
		Best: &IdentityCandidateResult{
			PersonID: 3,
			Score:    0.95,
			Status:   IdentityMatchStatusHardConflict,
		},
	}
	require.False(t, ApplyIdentityStrategy(result, NewIdentityAutoStrategy(cfg)).Accepted)
	require.False(t, ApplyIdentityStrategy(result, NewIdentitySuggestStrategy(cfg)).Accepted)
}

func TestMapMatcherToEngineResult_UnavailableVsNoCandidate(t *testing.T) {
	unavailable := mapMatcherToEngineResult(IdentityProfileMatch{
		Available:   false,
		BlockReason: blockIndexUnavailable,
	})
	require.Equal(t, IdentityMatchStatusUnavailable, unavailable.Status)

	noCand := mapMatcherToEngineResult(IdentityProfileMatch{Available: true})
	require.Equal(t, IdentityMatchStatusNoCandidate, noCand.Status)

	invalid := mapMatcherToEngineResult(IdentityProfileMatch{
		Available:   false,
		BlockReason: blockInvalidQuery,
	})
	require.Equal(t, IdentityMatchStatusInvalid, invalid.Status)
}

func TestIdentityStrategyFingerprint_Stable(t *testing.T) {
	cfg := config.PeopleConfig{
		IdentityProfileRescueThreshold: 0.65,
		MergeSuggestionThreshold:       0.55,
		IdentityProfileMargin:          0.05,
		IdentityProfileMinCenterFaces:  3,
		IdentityProfileMinCenterPhotos: 2,
		IdentityProfileMaxCenters:      6,
	}
	a := IdentityStrategyFingerprint(cfg)
	b := IdentityStrategyFingerprint(cfg)
	require.NotEmpty(t, a)
	require.Equal(t, a, b)
}
