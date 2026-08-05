package analyzer

import (
	"errors"
	"fmt"
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
	FailureClassDownloadFailed    = "download_failed"
)

// ClassifyFailure 把任意 error 映射为失败分类。
//
// 规则（基于结构化类型与状态码，禁止只靠字符串包含）：
//   - 客户端取消 → client_cancelled
//   - 下载失败（Downloader 包装）→ download_failed（计退避，不熔断 Provider）
//   - ProviderError.ResponseInvalid → response_invalid
//   - ProviderError.StatusCode == 429 → rate_limited
//   - ProviderError.StatusCode in {502,503,504} 或 Transport/Timeout → provider_transient
//   - 输入永久错误（JPEG 损坏、不支持格式）→ input_permanent
//   - 其余未知 → provider_transient（保守，可触发熔断，避免热循环）
func ClassifyFailure(err error) string {
	if err == nil {
		return ""
	}

	// 客户端取消优先。
	if errors.Is(err, errClientCancelled) {
		return FailureClassClientCancelled
	}

	// 下载失败（Analyzer→Server 网络问题，不应触发 Provider 熔断）。
	if errors.Is(err, errDownloadFailed) {
		return FailureClassDownloadFailed
	}

	// 输入永久错误（图像解码失败等）。仅对非 ProviderError 作用，
	// 避免把 Provider 响应 body 里碰巧含 "corrupt" 的消息误判为永久输入错误。
	if _, isProvider := provider.IsProviderError(err); !isProvider {
		if isInputPermanentErr(err) {
			return FailureClassInputPermanent
		}
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

// errDownloadFailed 标记下载失败（由 Downloader 包装）。
var errDownloadFailed = errors.New("download failed")

// NewDownloadFailedError 包装一个下载错误，使其可被 ClassifyFailure 识别为 download_failed。
func NewDownloadFailedError(err error) error {
	return fmt.Errorf("%w: %v", errDownloadFailed, err)
}

// IsDownloadFailed 判断是否为下载失败。
func IsDownloadFailed(err error) bool { return errors.Is(err, errDownloadFailed) }

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
