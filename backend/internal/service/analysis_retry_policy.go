package service

import "time"

// 失败分类常量。服务端与 Analyzer 共用同一套语义。
const (
	// FailureClassProviderTransient: HTTP 502/503/504、连接失败、请求超时
	FailureClassProviderTransient = "provider_transient"
	// FailureClassRateLimited: HTTP 429
	FailureClassRateLimited = "rate_limited"
	// FailureClassResponseInvalid: JSON 解析失败、缺少必填字段
	FailureClassResponseInvalid = "response_invalid"
	// FailureClassInputPermanent: JPEG 损坏、格式不支持
	FailureClassInputPermanent = "input_permanent"
	// FailureClassClientCancelled: 用户取消、进程退出
	FailureClassClientCancelled = "client_cancelled"
	// FailureClassDownloadFailed: 照片下载失败（Analyzer→Server 网络问题，非 Provider 问题）。
	// 计入退避（避免立即重领热循环），但不触发 Provider 熔断。
	FailureClassDownloadFailed = "download_failed"
)

// AnalysisMaxAttempts 统一最大尝试次数，替代本地 3 次、服务端 10 次、统计 3 次三套口径。
const AnalysisMaxAttempts = 10

// analysisBackoffTable 是固定退避表：第 N 次失败后距下次可重试的等待时长。
// 索引 0 未使用；第 1..9 次对应退避，第 10 次进入终态。
var analysisBackoffTable = [...]time.Duration{
	0:                0,                  // 占位
	1:                30 * time.Second,   // 第 1 次失败：30 秒
	2:                2 * time.Minute,    // 第 2 次失败：2 分钟
	3:                10 * time.Minute,   // 第 3 次失败：10 分钟
	4:                30 * time.Minute,   // 第 4 次失败：30 分钟
	5:                2 * time.Hour,      // 第 5 次失败：2 小时
	6:                2 * time.Hour,      // 第 6 次失败：2 小时
	7:                2 * time.Hour,      // 第 7 次失败：2 小时
	8:                2 * time.Hour,      // 第 8 次失败：2 小时
	9:                2 * time.Hour,      // 第 9 次失败：2 小时
}

// RetryDecision 是 nextAnalysisRetry 的返回：服务端据此更新照片状态。
type RetryDecision struct {
	// NewAttempts 更新后的失败次数（成功时清零，transient 类失败自增，permanent 直接 max）。
	NewAttempts int
	// NextRetryAt 下次可重试时间；终态为 nil。
	NextRetryAt *time.Time
	// Final 是否进入最终失败（不再参与自动领取）。
	Final bool
	// Counted 本次释放是否计入业务失败次数（client_cancelled 不计数）。
	Counted bool
}

// nextAnalysisRetry 根据当前 attempts、失败分类和可选的 Retry-After 计算下次重试决策。
//
// 入参：
//   - attempt: 本次失败前的累计次数（即 release 前的 retry_count）
//   - class: 失败分类
//   - retryAfter: 客户端从 Retry-After 头解析的等待时长；0 表示无
//   - now: 当前时间（便于测试注入）
//
// 语义：
//   - client_cancelled：不计数，next retry 清空，照片回到 pending。
//   - input_permanent：直接推进到最终失败。
//   - 其他可重试分类：attempt+1，按退避表取默认等待；Retry-After 更晚则采用更晚时间。
//   - 第 10 次失败：终态，next retry = nil。
func nextAnalysisRetry(attempt int, class string, retryAfter time.Duration, now time.Time) RetryDecision {
	// client_cancelled 不增加业务失败次数，立即回到 pending。
	if class == FailureClassClientCancelled {
		return RetryDecision{
			NewAttempts: attempt,
			NextRetryAt: nil,
			Final:       false,
			Counted:     false,
		}
	}

	// input_permanent 直接进入最终失败。
	if class == FailureClassInputPermanent {
		return RetryDecision{
			NewAttempts: AnalysisMaxAttempts,
			NextRetryAt: nil,
			Final:       true,
			Counted:     true,
		}
	}

	// 可重试分类：provider_transient / rate_limited / response_invalid / download_failed
	// download_failed 与 provider_transient 走相同退避表，但由熔断器区分是否计数（不计 Provider 熔断）。
	newAttempts := attempt + 1
	if newAttempts >= AnalysisMaxAttempts {
		return RetryDecision{
			NewAttempts: AnalysisMaxAttempts,
			NextRetryAt: nil,
			Final:       true,
			Counted:     true,
		}
	}

	// 默认退避（按 newAttempts 索引退避表）。
	defaultDelay := analysisBackoffTable[newAttempts]
	if defaultDelay <= 0 {
		defaultDelay = 2 * time.Hour
	}

	// Retry-After 更晚则采用更晚时间；客户端不能缩短服务端退避。
	delay := defaultDelay
	if retryAfter > delay {
		delay = retryAfter
	}

	next := now.Add(delay)
	return RetryDecision{
		NewAttempts: newAttempts,
		NextRetryAt: &next,
		Final:       false,
		Counted:     true,
	}
}
