package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNextAnalysisRetry_BackoffTable(t *testing.T) {
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name        string
		attempt     int
		class       string
		wantDelay   time.Duration
		wantFinal   bool
		wantCounted bool
	}{
		{"1st transient", 0, FailureClassProviderTransient, 30 * time.Second, false, true},
		{"2nd transient", 1, FailureClassProviderTransient, 2 * time.Minute, false, true},
		{"3rd transient", 2, FailureClassProviderTransient, 10 * time.Minute, false, true},
		{"4th transient", 3, FailureClassProviderTransient, 30 * time.Minute, false, true},
		{"5th transient", 4, FailureClassProviderTransient, 2 * time.Hour, false, true},
		{"9th transient", 8, FailureClassProviderTransient, 2 * time.Hour, false, true},
		{"10th transient final", 9, FailureClassProviderTransient, 0, true, true},
		{"rate_limited 1st", 0, FailureClassRateLimited, 30 * time.Second, false, true},
		{"response_invalid 1st", 0, FailureClassResponseInvalid, 30 * time.Second, false, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := nextAnalysisRetry(c.attempt, c.class, 0, base)
			assert.Equal(t, c.wantCounted, d.Counted)
			assert.Equal(t, c.wantFinal, d.Final)
			if c.wantFinal {
				assert.Nil(t, d.NextRetryAt)
				assert.Equal(t, AnalysisMaxAttempts, d.NewAttempts)
			} else {
				require.NotNil(t, d.NextRetryAt)
				assert.Equal(t, base.Add(c.wantDelay), *d.NextRetryAt)
				assert.Equal(t, c.attempt+1, d.NewAttempts)
			}
		})
	}
}

func TestNextAnalysisRetry_RetryAfterLowerBound(t *testing.T) {
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)

	// 默认 30s；Retry-After 10s 更短，应采用默认 30s（不能缩短服务端退避）。
	d := nextAnalysisRetry(0, FailureClassProviderTransient, 10*time.Second, base)
	require.NotNil(t, d.NextRetryAt)
	assert.Equal(t, base.Add(30*time.Second), *d.NextRetryAt)

	// Retry-After 5m 更晚，采用 5m。
	d = nextAnalysisRetry(0, FailureClassProviderTransient, 5*time.Minute, base)
	require.NotNil(t, d.NextRetryAt)
	assert.Equal(t, base.Add(5*time.Minute), *d.NextRetryAt)
}

func TestNextAnalysisRetry_InputPermanentFinal(t *testing.T) {
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	d := nextAnalysisRetry(0, FailureClassInputPermanent, 0, base)
	assert.True(t, d.Final)
	assert.True(t, d.Counted)
	assert.Nil(t, d.NextRetryAt)
	assert.Equal(t, AnalysisMaxAttempts, d.NewAttempts)
}

func TestNextAnalysisRetry_ClientCancelledNotCounted(t *testing.T) {
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	d := nextAnalysisRetry(3, FailureClassClientCancelled, 0, base)
	assert.False(t, d.Counted)
	assert.False(t, d.Final)
	assert.Nil(t, d.NextRetryAt)
	assert.Equal(t, 3, d.NewAttempts, "client_cancelled 不增加 attempts")
}

func TestNextAnalysisRetry_TenthFinal(t *testing.T) {
	base := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC)
	d := nextAnalysisRetry(9, FailureClassProviderTransient, 0, base)
	assert.True(t, d.Final)
	assert.Nil(t, d.NextRetryAt)
	assert.Equal(t, 10, d.NewAttempts)
}
