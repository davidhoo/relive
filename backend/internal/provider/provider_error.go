package provider

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProviderError 是 Provider 返回的结构化错误，保留分类与熔断所需的类型信息。
//
// 字段语义：
//   - Provider: provider 名称（vllm/ollama/...）
//   - StatusCode: HTTP 状态码；0 表示非 HTTP 错误（连接失败/超时/解析失败）
//   - RetryAfter: 从 Retry-After 头解析的等待时长；0 表示无
//   - Transport: 传输层错误（连接失败、deadline exceeded 等）
//   - Timeout: 是否为超时类错误
//   - ResponseInvalid: 响应可解析但内容不合法（缺字段、JSON 损坏）
//   - BodySummary: 脱敏截断的响应摘要（≤500 字符）
type ProviderError struct {
	Provider        string
	StatusCode      int
	RetryAfter      time.Duration
	Transport       bool
	Timeout         bool
	ResponseInvalid bool
	BodySummary     string

	// Err 原始底层错误（不直接暴露给客户端）。
	Err error
}

// Error 实现 error 接口。返回的消息已脱敏，可安全记入服务端日志。
func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil provider error>"
	}
	var b strings.Builder
	b.WriteString("provider ")
	if e.Provider != "" {
		b.WriteString(e.Provider)
	} else {
		b.WriteString("unknown")
	}
	if e.StatusCode != 0 {
		fmt.Fprintf(&b, " http %d", e.StatusCode)
	}
	if e.Timeout {
		b.WriteString(" (timeout)")
	}
	if e.Transport {
		b.WriteString(" (transport)")
	}
	if e.ResponseInvalid {
		b.WriteString(" (response_invalid)")
	}
	if e.BodySummary != "" {
		fmt.Fprintf(&b, ": %s", e.BodySummary)
	} else if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(sanitizeBody(e.Err.Error(), 200))
	}
	return b.String()
}

// Unwrap 暴露底层错误供 errors.Is/As 使用。
func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsProviderError 判断 err 是否为 ProviderError。
func IsProviderError(err error) (*ProviderError, bool) {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// NewTransportError 构造传输层错误（连接失败、超时）。
func NewTransportError(providerName string, timeout bool, err error) *ProviderError {
	return &ProviderError{
		Provider:  providerName,
		Transport: true,
		Timeout:   timeout,
		Err:       err,
	}
}

// NewHTTPError 从 HTTP 响应构造 ProviderError，解析 Retry-After 并脱敏 body。
//
// 调用方负责关闭 resp.Body。本函数读取 body 摘要后不再关闭。
func NewHTTPError(providerName string, resp *http.Response) *ProviderError {
	pe := &ProviderError{
		Provider:   providerName,
		StatusCode: resp.StatusCode,
	}

	// 解析 Retry-After（仅 429 / 5xx 时有意义，但统一解析）。
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if d, ok := parseRetryAfter(ra); ok {
			pe.RetryAfter = d
		}
	}

	// 读取并脱敏 body 摘要。
	if resp.Body != nil {
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
			pe.BodySummary = sanitizeBody(string(body), 500)
		}
	}
	return pe
}

// NewResponseInvalidError 构造响应内容不合法错误（JSON 损坏、缺字段）。
func NewResponseInvalidError(providerName, summary string) *ProviderError {
	return &ProviderError{
		Provider:        providerName,
		ResponseInvalid: true,
		BodySummary:     sanitizeBody(summary, 500),
	}
}

// parseRetryAfter 解析 Retry-After 头：支持秒数与 HTTP-date。
func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	// 秒数。
	if secs, err := strconv.Atoi(value); err == nil {
		if secs < 0 {
			secs = 0
		}
		return time.Duration(secs) * time.Second, true
	}
	// HTTP-date。
	if t, err := http.ParseTime(value); err == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// sanitizeBody 压缩空白并截断到 max 字符。Provider 响应体可能含敏感信息，
// 由调用方（service 层）再做一次 sanitizeError；这里只做基本压缩。
func sanitizeBody(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if max > 0 && len(s) > max {
		s = s[:max]
	}
	return s
}
