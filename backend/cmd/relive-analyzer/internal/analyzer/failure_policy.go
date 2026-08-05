package analyzer

import (
	"errors"
	"strings"

	"github.com/davidhoo/relive/internal/provider"
)

// FailureClass 与 service 层失败分类常量保持一致语义，供 Analyzer 侧使用。
const (
	FailureClassProviderTransient = "provider_transient"
	FailureClassRateLimited       = "rate_limited"
	FailureClassResponseInvalid   = "response_invalid"
	FailureClassInputPermanent    = "input_permanent"
	FailureClassClientCancelled   = "client_cancelled"
)

// ClassifyFailure 把任意 error 映射为失败分类。
//
// 规则（基于结构化类型与状态码，禁止只靠字符串包含）：
//   - ProviderError.ResponseInvalid → response_invalid
//   - ProviderError.StatusCode == 429 → rate_limited
//   - ProviderError.StatusCode in {502,503,504} 或 Transport/Timeout → provider_transient
//   - 输入永久错误（JPEG 损坏、不支持格式）→ input_permanent
//   - 客户端取消 → client_cancelled
//   - 其余未知 → provider_transient（保守，可触发熔断，避免热循环）
func ClassifyFailure(err error) string {
	if err == nil {
		return ""
	}

	// 客户端取消优先。
	if errors.Is(err, errClientCancelled) {
		return FailureClassClientCancelled
	}

	// 输入永久错误（图像解码失败等）。
	if isInputPermanentErr(err) {
		return FailureClassInputPermanent
	}

	// 结构化 ProviderError。
	if pe, ok := provider.IsProviderError(err); ok {
		if pe.ResponseInvalid {
			return FailureClassResponseInvalid
		}
		switch pe.StatusCode {
		case 429:
			return FailureClassRateLimited
		case 502, 503, 504:
			return FailureClassProviderTransient
		}
		if pe.Transport || pe.Timeout {
			return FailureClassProviderTransient
		}
		// 其他 HTTP 状态码（4xx 非 429 等）：保守按 transient，让服务端退避决定。
		if pe.StatusCode != 0 {
			return FailureClassProviderTransient
		}
	}

	// 兜底：未知错误按 transient。
	return FailureClassProviderTransient
}

// errClientCancelled 标记客户端主动取消。
var errClientCancelled = errors.New("client cancelled")

// NewClientCancelledError 返回一个表示客户端取消的 error。
func NewClientCancelledError() error { return errClientCancelled }

// IsClientCancelled 判断是否为客户端取消。
func IsClientCancelled(err error) bool { return errors.Is(err, errClientCancelled) }

// permanentInputPatterns 是输入永久错误的特征串。
// 注意：图像解码错误来自标准库 image/jpeg 等，无结构化类型，只能字符串匹配。
var permanentInputPatterns = []string{
	"invalid jpeg format",
	"short huffman data",
	"invalid jpeg",
	"unknown image format",
	"unsupported image format",
	"image: unknown format",
	"not a valid png",
	"corrupt",
	"unexpected eof",
}

// isInputPermanentErr 判断是否为不可恢复的输入错误。
func isInputPermanentErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range permanentInputPatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// IsInputPermanent 暴露给主循环判断是否应跳过本会话。
func IsInputPermanent(err error) bool { return isInputPermanentErr(err) }
