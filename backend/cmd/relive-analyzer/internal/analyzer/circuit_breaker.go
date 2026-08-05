package analyzer

import (
	"sync"
	"time"

	"github.com/davidhoo/relive/pkg/logger"
)

// CircuitState 熔断状态。
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // 正常领取
	CircuitOpen                         // 暂停领取，等待退避
	CircuitHalfOpen                     // 单一探测任务在途
)

// String 返回状态名。
func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitConfig 熔断器配置。
type CircuitConfig struct {
	// FailureThreshold 连续命中不同 photo ID 的失败阈值，达到后 open。
	FailureThreshold int
	// InitialBackoff 首次 open 后的退避时长。
	InitialBackoff time.Duration
	// MaxBackoff 退避上限。
	MaxBackoff time.Duration
}

// DefaultCircuitConfig 默认配置：连续 3 个不同照片失败后 open，30s→1m→2m→5m→10m。
func DefaultCircuitConfig() CircuitConfig {
	return CircuitConfig{
		FailureThreshold: 3,
		InitialBackoff:   30 * time.Second,
		MaxBackoff:       10 * time.Minute,
	}
}

// backoffTable open 退避阶梯：30s → 1m → 2m → 5m → 10m。
var circuitBackoffTable = []time.Duration{
	30 * time.Second,
	1 * time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	10 * time.Minute,
}

// CircuitBreaker Provider 级熔断状态机。
//
// 线程安全。时间通过 nowFunc 注入，便于测试。
type CircuitBreaker struct {
	mu     sync.Mutex
	cfg    CircuitConfig
	nowFunc func() time.Time

	state          CircuitState
	consecutive    int           // 连续失败计数（不同 photo ID）
	lastFailPhoto  uint          // 上一次失败的 photo ID，防止同照片重复伪造阈值
	openUntil      time.Time     // open 状态到期时间
	openCount      int           // 累计 open 次数，用于退避升级
	probeInFlight  bool          // half-open 探测任务是否在途
	rateLimitedAt  time.Time     // 429 强制 open 的到期时间（遵守 Retry-After）
}

// NewCircuitBreaker 构造熔断器。
func NewCircuitBreaker(cfg CircuitConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 30 * time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 10 * time.Minute
	}
	return &CircuitBreaker{
		cfg:     cfg,
		nowFunc: time.Now,
	}
}

// SetNowFunc 注入时间函数（测试用）。
func (cb *CircuitBreaker) SetNowFunc(fn func() time.Time) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.nowFunc = fn
}

// State 返回当前状态（在调用方判断能否领取前先调用 refresh）。
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.refreshLocked()
	return cb.state
}

// CanFetch 当前是否允许普通领取。
// open 与 half-open-probe-in-flight 时禁止；closed 允许。
func (cb *CircuitBreaker) CanFetch() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.refreshLocked()
	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		return false
	case CircuitHalfOpen:
		// half-open 期间只允许探测任务，普通 fetch 禁止。
		return false
	}
	return true
}

// AcquireProbe 在 half-open 状态下尝试获取一个探测许可。
// 返回 true 表示调用方可执行一次探测任务；false 表示已有探测在途或非 half-open。
func (cb *CircuitBreaker) AcquireProbe() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.refreshLocked()
	if cb.state != CircuitHalfOpen {
		return false
	}
	if cb.probeInFlight {
		return false
	}
	cb.probeInFlight = true
	return true
}

// ReleaseProbe 释放探测许可。仅当当前持有探测时调用。
func (cb *CircuitBreaker) ReleaseProbe() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.probeInFlight = false
}

// RecordSuccess 记录一次成功。
// half-open 探测成功 → close；closed → 重置连续失败计数。
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	wasHalfOpen := cb.state == CircuitHalfOpen
	cb.state = CircuitClosed
	cb.consecutive = 0
	cb.lastFailPhoto = 0
	cb.openUntil = time.Time{}
	cb.openCount = 0
	cb.probeInFlight = false
	if wasHalfOpen {
		logger.Info("Circuit breaker closed after half-open probe success")
	}
}

// RecordFailure 记录一次 Provider 失败。
//
// 行为：
//   - 429（rate_limited）：立即 open，并优先遵守 Retry-After（取更晚者）。
//   - 其他可熔断分类：连续命中阈值后 open。
//   - 同一 photo ID 重复失败不累加阈值。
//   - half-open 探测失败：重新 open 并升级退避。
func (cb *CircuitBreaker) RecordFailure(photoID uint, class string, retryAfter time.Duration) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.refreshLocked()

	now := cb.nowFunc()

	// 429 直接 open，并优先遵守 Retry-After。
	if class == FailureClassRateLimited {
		cb.openLocked(now, retryAfter)
		logger.Warnf("Circuit breaker open due to rate limit (retry-after %v)", retryAfter)
		return
	}

	// half-open 探测失败 → 重新 open 并升级退避（openLocked 内部递增 openCount）。
	if cb.state == CircuitHalfOpen {
		cb.openLocked(now, 0)
		logger.Warnf("Circuit breaker re-opened after half-open probe failure (open count %d)", cb.openCount)
		return
	}

	// closed 状态累计连续失败（不同 photo ID）。
	if class != FailureClassProviderTransient && class != FailureClassResponseInvalid {
		// input_permanent / client_cancelled 不计入 Provider 熔断。
		return
	}

	if photoID == cb.lastFailPhoto {
		// 同一照片重复失败不伪造阈值。
		return
	}
	cb.lastFailPhoto = photoID
	cb.consecutive++

	if cb.consecutive >= cb.cfg.FailureThreshold {
		cb.openLocked(now, 0)
		logger.Warnf("Circuit breaker open after %d consecutive distinct-photo failures", cb.consecutive)
	}
}

// openLocked 进入 open 状态，计算退避到期时间。
// retryAfter 为客户端解析的 Retry-After；取默认退避与 retryAfter 的更晚者。
func (cb *CircuitBreaker) openLocked(now time.Time, retryAfter time.Duration) {
	cb.openCount++
	if cb.openCount > len(circuitBackoffTable) {
		cb.openCount = len(circuitBackoffTable)
	}
	delay := circuitBackoffTable[cb.openCount-1]
	if delay > cb.cfg.MaxBackoff {
		delay = cb.cfg.MaxBackoff
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	cb.state = CircuitOpen
	cb.openUntil = now.Add(delay)
	cb.probeInFlight = false
}

// refreshLocked 在持锁状态下推进状态机：open 到期 → half-open。
func (cb *CircuitBreaker) refreshLocked() {
	if cb.state != CircuitOpen {
		return
	}
	now := cb.nowFunc()
	if !now.Before(cb.openUntil) {
		cb.state = CircuitHalfOpen
		cb.probeInFlight = false
		logger.Info("Circuit breaker entering half-open, allowing one probe")
	}
}

// OpenUntil 返回 open 状态到期时间（仅用于日志/诊断）。
func (cb *CircuitBreaker) OpenUntil() time.Time {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.openUntil
}

// Consecutive 返回当前连续失败计数（诊断用）。
func (cb *CircuitBreaker) Consecutive() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.consecutive
}
