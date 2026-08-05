package analyzer

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/provider"
	"github.com/davidhoo/relive/pkg/config"
	"github.com/davidhoo/relive/pkg/logger"
	"github.com/stretchr/testify/assert"
)

func init() {
	_ = logger.Init(config.LoggingConfig{Level: "info", Console: false})
}

func TestClassifyFailure_TableDriven(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"429", &provider.ProviderError{Provider: "vllm", StatusCode: 429, RetryAfter: 30 * time.Second}, FailureClassRateLimited},
		{"502", &provider.ProviderError{Provider: "vllm", StatusCode: 502}, FailureClassProviderTransient},
		{"503", &provider.ProviderError{Provider: "vllm", StatusCode: 503}, FailureClassProviderTransient},
		{"504", &provider.ProviderError{Provider: "vllm", StatusCode: 504}, FailureClassProviderTransient},
		{"transport connect", &provider.ProviderError{Provider: "vllm", Transport: true, Err: errors.New("connection refused")}, FailureClassProviderTransient},
		{"transport deadline", &provider.ProviderError{Provider: "vllm", Transport: true, Timeout: true, Err: errors.New("context deadline exceeded")}, FailureClassProviderTransient},
		{"response invalid json", &provider.ProviderError{Provider: "vllm", ResponseInvalid: true}, FailureClassResponseInvalid},
		{"response missing fields", &provider.ProviderError{Provider: "vllm", ResponseInvalid: true, BodySummary: "missing required fields"}, FailureClassResponseInvalid},
		{"jpeg corrupt", errors.New("invalid jpeg format: short huffman data"), FailureClassInputPermanent},
		{"unsupported format", errors.New("image: unknown format"), FailureClassInputPermanent},
		{"client cancelled", NewClientCancelledError(), FailureClassClientCancelled},
		{"download failed", NewDownloadFailedError(errors.New("connection reset")), FailureClassDownloadFailed},
		{"unknown plain error", errors.New("something weird happened"), FailureClassProviderTransient},
		{"nil", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, ClassifyFailure(c.err))
		})
	}
}

func TestClassifyFailure_HTTPHelper(t *testing.T) {
	// 通过 NewHTTPError 构造 429，验证 Retry-After 解析与分类联动。
	resp := &http.Response{
		StatusCode: 429,
		Header:     http.Header{"Retry-After": []string{"60"}},
		Body:       http.NoBody,
	}
	pe := provider.NewHTTPError("vllm", resp)
	assert.Equal(t, 60*time.Second, pe.RetryAfter)
	assert.Equal(t, FailureClassRateLimited, ClassifyFailure(pe))
}

func TestIsInputPermanent(t *testing.T) {
	assert.True(t, IsInputPermanent(errors.New("invalid jpeg format")))
	assert.False(t, IsInputPermanent(errors.New("502 bad gateway")))
}

func TestIsClientCancelled(t *testing.T) {
	assert.True(t, IsClientCancelled(NewClientCancelledError()))
	assert.False(t, IsClientCancelled(errors.New("nope")))
}

func TestIsDownloadFailed(t *testing.T) {
	assert.True(t, IsDownloadFailed(NewDownloadFailedError(errors.New("timeout"))))
	assert.False(t, IsDownloadFailed(errors.New("nope")))
}

// TestClassifyFailure_ProviderBodyCorruptNotInputPermanent
// Provider 响应 body 里碰巧含 "corrupt" 不应被误判为 input_permanent。
func TestClassifyFailure_ProviderBodyCorruptNotInputPermanent(t *testing.T) {
	pe := &provider.ProviderError{
		Provider:    "vllm",
		StatusCode:  502,
		BodySummary: "model corrupt state",
	}
	assert.Equal(t, FailureClassProviderTransient, ClassifyFailure(pe))
}

// TestCircuitBreaker_DownloadFailedDoesNotOpen
// download_failed 不应触发 Provider 熔断。
func TestCircuitBreaker_DownloadFailedDoesNotOpen(t *testing.T) {
	clk := newFakeClock(time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC))
	cb := NewCircuitBreaker(DefaultCircuitConfig())
	cb.SetNowFunc(clk.now)

	for i := 1; i <= 10; i++ {
		cb.RecordFailure(uint(i), FailureClassDownloadFailed, 0)
	}
	assert.Equal(t, CircuitClosed, cb.State(), "download_failed 不应熔断 Provider")
}
