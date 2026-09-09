package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMergeSuggestionRetryBackoff_Bounded(t *testing.T) {
	require.Equal(t, 300*time.Second, mergeSuggestionRetryBackoff(1))
	require.Equal(t, 600*time.Second, mergeSuggestionRetryBackoff(2))
	require.Equal(t, 1200*time.Second, mergeSuggestionRetryBackoff(3))
	require.Equal(t, time.Duration(mergeSuggestionRetryMaxSeconds)*time.Second, mergeSuggestionRetryBackoff(20))
}

func TestMergeSuggestionRetryState_NoBusyLoopWhenDeferred(t *testing.T) {
	now := time.Now()
	state := personMergeSuggestionState{
		Dirty: false,
		RetryTargets: []mergeSuggestionRetryEntry{
			{TargetID: 1, Attempts: 1, NextRetryAt: now.Add(10 * time.Minute)},
			{TargetID: 2, Attempts: mergeSuggestionRetryMaxAttempts, NextRetryAt: now.Add(-time.Minute)},
		},
	}

	due, remaining, exhausted := classifyMergeSuggestionRetries(state.RetryTargets, now)
	require.Empty(t, due, "future retries must not be due")
	require.Len(t, remaining, 1)
	require.Equal(t, uint(1), remaining[0].TargetID)
	require.Len(t, exhausted, 1)
	require.Equal(t, uint(2), exhausted[0].TargetID)

	// 结束一轮：有延期重试时不得保持 Dirty（否则每分钟全库重扫）。
	dirty, retryOnly := mergeSuggestionEndOfPassFlags(due, remaining)
	require.False(t, dirty)
	require.False(t, retryOnly)
}

func TestMergeSuggestionRetryState_DueRetriesActivateRetryOnly(t *testing.T) {
	now := time.Now()
	due, remaining, exhausted := classifyMergeSuggestionRetries([]mergeSuggestionRetryEntry{
		{TargetID: 9, Attempts: 2, NextRetryAt: now.Add(-time.Second)},
	}, now)
	require.Len(t, due, 1)
	require.Len(t, remaining, 1)
	require.Empty(t, exhausted)

	dirty, retryOnly := mergeSuggestionEndOfPassFlags(due, remaining)
	require.True(t, dirty)
	require.True(t, retryOnly)
}

func TestPendingSuggestionItemsEquivalent(t *testing.T) {
	a := []modelPersonMergeItemView{{CandidateID: 2, Score: 0.55}, {CandidateID: 1, Score: 0.6}}
	b := []modelPersonMergeItemView{{CandidateID: 1, Score: 0.6}, {CandidateID: 2, Score: 0.55}}
	require.True(t, pendingSuggestionItemsEquivalent(a, b))
	require.False(t, pendingSuggestionItemsEquivalent(a, []modelPersonMergeItemView{{CandidateID: 1, Score: 0.6}}))
	require.True(t, pendingSuggestionItemsEquivalent(nil, nil))
	require.True(t, pendingSuggestionItemsEquivalent(nil, []modelPersonMergeItemView{}))
}
