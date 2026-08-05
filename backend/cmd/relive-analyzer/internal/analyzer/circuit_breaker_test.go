package analyzer

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/stretchr/testify/assert"
)

func init() {
	// 初始化 logger，避免 nil pointer。
	_ = logger.Init(config.LoggingConfig{Level: "info", Console: false})
}

// fakeClock 返回可控的当前时间。
type fakeClock struct {
	t time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{t: start} }
func (f *fakeClock) now() time.Time           { return f.t }
func (f *fakeClock) advance(d time.Duration)  { f.t = f.t.Add(d) }

// TestCircuitBreaker_OpenAfterDistinctFailures
// closed 连续 3 个不同 photo ID 失败后 open。
func TestCircuitBreaker_OpenAfterDistinctFailures(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	assert.Equal(t, CircuitClosed, cb.State())
	assert.True(t, cb.CanFetch())

	cb.RecordFailure(1, FailureClassProviderTransient, 0)
	cb.RecordFailure(2, FailureClassProviderTransient, 0)
	assert.True(t, cb.CanFetch(), "2 次失败未达阈值，仍可领取")

	cb.RecordFailure(3, FailureClassProviderTransient, 0)
	assert.Equal(t, CircuitOpen, cb.State())
	assert.False(t, cb.CanFetch(), "open 后禁止领取")
}

// TestCircuitBreaker_SamePhotoRepeatDoesNotTrigger
// 同一 photo ID 重复失败不能伪造阈值。
func TestCircuitBreaker_SamePhotoRepeatDoesNotTrigger(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	for i := 0; i < 10; i++ {
		cb.RecordFailure(42, FailureClassProviderTransient, 0)
	}
	assert.Equal(t, CircuitClosed, cb.State(), "同照片重复失败不应触发 open")
	assert.True(t, cb.CanFetch())
}

// TestCircuitBreaker_OpenForbidsFetch
func TestCircuitBreaker_OpenForbidsFetch(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	for i := 1; i <= 3; i++ {
		cb.RecordFailure(uint(i), FailureClassProviderTransient, 0)
	}
	assert.False(t, cb.CanFetch())
}

// TestCircuitBreaker_HalfOpenSingleProbe
// 到期只允许一个 half-open probe。
func TestCircuitBreaker_HalfOpenSingleProbe(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	for i := 1; i <= 3; i++ {
		cb.RecordFailure(uint(i), FailureClassProviderTransient, 0)
	}
	assert.Equal(t, CircuitOpen, cb.State())

	// 退避 30s，advance 30s 后进入 half-open。
	clk.advance(30 * time.Second)
	assert.Equal(t, CircuitHalfOpen, cb.State())
	assert.False(t, cb.CanFetch(), "half-open 仍禁止普通 fetch")

	// 第一个 probe 可获取，第二个不行。
	assert.True(t, cb.AcquireProbe())
	assert.False(t, cb.AcquireProbe(), "只允许一个 half-open probe")
}

// TestCircuitBreaker_ProbeSuccessCloses
func TestCircuitBreaker_ProbeSuccessCloses(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	for i := 1; i <= 3; i++ {
		cb.RecordFailure(uint(i), FailureClassProviderTransient, 0)
	}
	clk.advance(30 * time.Second)
	assert.True(t, cb.AcquireProbe())

	cb.RecordSuccess()
	assert.Equal(t, CircuitClosed, cb.State())
	assert.True(t, cb.CanFetch())
	assert.False(t, cb.AcquireProbe(), "closed 后无 probe")
}

// TestCircuitBreaker_ProbeFailureReopensWithEscalation
// probe 失败重新 open 并升级退避，且不超过 max。
func TestCircuitBreaker_ProbeFailureReopensWithEscalation(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	for i := 1; i <= 3; i++ {
		cb.RecordFailure(uint(i), FailureClassProviderTransient, 0)
	}
	// 第一次 open 退避 30s。
	clk.advance(30 * time.Second)
	assert.True(t, cb.AcquireProbe())
	// probe 失败 → 重新 open，退避升级到 1m。
	cb.RecordFailure(100, FailureClassProviderTransient, 0)
	assert.Equal(t, CircuitOpen, cb.State())
	clk.advance(30 * time.Second)
	assert.Equal(t, CircuitOpen, cb.State(), "1m 退避未到应仍 open")
	clk.advance(30 * time.Second) // 累计 1m
	assert.Equal(t, CircuitHalfOpen, cb.State())
}

// TestCircuitBreaker_BackoffCappedAtMax
// 多轮 open 后退避不超过 max（10m）。
func TestCircuitBreaker_BackoffCappedAtMax(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	// 反复 open+probe-failure 升级退避。
	for round := 0; round < 8; round++ {
		// 触发 open（如果还未 open）。
		if cb.State() == CircuitClosed {
			for i := 1; i <= 3; i++ {
				cb.RecordFailure(uint(round*100+i), FailureClassProviderTransient, 0)
			}
		}
		// advance 到 half-open。
		for cb.State() == CircuitOpen {
			clk.advance(10 * time.Minute)
		}
		if cb.State() == CircuitHalfOpen {
			if cb.AcquireProbe() {
				// probe 失败再 open。
				cb.RecordFailure(uint(900+round), FailureClassProviderTransient, 0)
			}
		}
	}
	// openUntil 不应超过起点 + 10m。
	until := cb.OpenUntil()
	assert.True(t, until.Sub(clk.now()) <= 10*time.Minute+time.Second, "退避不应超过 max 10m, got %v", until.Sub(clk.now()))
}

// TestCircuitBreaker_RateLimitedUsesRetryAfter
// 429 直接 open，并优先遵守更晚的 Retry-After。
func TestCircuitBreaker_RateLimitedUsesRetryAfter(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	// 429 + Retry-After 5m：open 退避应取 5m（晚于默认 30s）。
	cb.RecordFailure(1, FailureClassRateLimited, 5*time.Minute)
	assert.Equal(t, CircuitOpen, cb.State())
	clk.advance(30 * time.Second)
	assert.Equal(t, CircuitOpen, cb.State(), "5m 未到应仍 open")
	clk.advance(5 * time.Minute) // 累计 5m+
	assert.Equal(t, CircuitHalfOpen, cb.State())
}

// TestCircuitBreaker_InputPermanentDoesNotOpen
// input_permanent 不触发熔断。
func TestCircuitBreaker_InputPermanentDoesNotOpen(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	for i := 1; i <= 10; i++ {
		cb.RecordFailure(uint(i), FailureClassInputPermanent, 0)
	}
	assert.Equal(t, CircuitClosed, cb.State(), "input_permanent 不应熔断")
}

// TestCircuitBreaker_ClientCancelledDoesNotOpen
func TestCircuitBreaker_ClientCancelledDoesNotOpen(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	for i := 1; i <= 10; i++ {
		cb.RecordFailure(uint(i), FailureClassClientCancelled, 0)
	}
	assert.Equal(t, CircuitClosed, cb.State())
}
