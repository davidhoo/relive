package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/davidhoo/relive/pkg/config"
)

// 统一身份匹配引擎之上的两套命名策略。
//
// 关键契约（实施方案第 3.2 节）：在相同规范化输入、候选集合与引擎版本下，两种策略
// 取得完全相同的原始分数、证据与硬约束，只能因阈值/证据门槛产生不同接受结果。
// 不得为让推荐看起来更好而重新缩放或另算分数。

// 策略版本常量。阈值来源或门槛语义变化时必须提升版本。
const (
	identityAutoStrategyVersion    = "identity-auto-v1"
	identitySuggestStrategyVersion = "identity-suggest-v1"
)

// 策略名称。稳定字符串，可持久化到推荐记录与归属日志。
const (
	identityStrategyNameAuto    = "auto_attach"
	identityStrategyNameSuggest = "manual_suggest"
)

// 策略拒绝原因。稳定枚举字符串，不拼接人名、路径或错误全文。
const (
	identityRejectNoCandidate         = "no_candidate"
	identityRejectUnavailable         = "unavailable"
	identityRejectInvalid             = "invalid"
	identityRejectHardConflict        = "hard_conflict"
	identityRejectNotAutoEligible     = "not_auto_eligible"
	identityRejectScoreBelowStrategy  = "score_below_strategy_threshold"
	identityRejectMarginBelowStrategy = "margin_below_strategy_margin"
	identityRejectMarginUnknown       = "margin_unknown"
	identityRejectUnstableCenters     = "unstable_centers"
	identityRejectInsufficientUnits   = "insufficient_supporting_units"
)

// IdentityStrategy 集中定义一套策略的分数阈值、margin 与最低支持证据要求。
// 所有策略参数只能来自 PeopleConfig，禁止在调用点临时放宽。
type IdentityStrategy struct {
	// Name 是策略名称（auto_attach / manual_suggest）。
	Name string
	// Version 是策略版本常量。
	Version string
	// ScoreThreshold 是最低原始身份分数。
	ScoreThreshold float64
	// Margin 是最佳与次佳的最小分差；0 表示该策略不要求 margin。
	Margin float64
	// MinCenterFaces 是贡献中心的最低支撑样本数（仅在 RequireStableCenters 时生效）。
	MinCenterFaces int
	// MinSupportingUnits 是最少有效贡献证据单元数。
	MinSupportingUnits int
	// RequireStableCenters 要求贡献中心均达到 MinCenterFaces。
	RequireStableCenters bool
	// RequireEngineMatch 要求引擎自身已把结果归类为 match（即通过全部自动资格护栏）。
	// 自动归属为 true；人工推荐为 false，因此「证据不足」仍可推荐并附说明。
	RequireEngineMatch bool
}

// NewIdentityAutoStrategy 构造自动归属策略。
//
// 分数阈值沿用已校准的 IdentityProfileRescueThreshold，margin 与中心样本门槛沿用
// IdentityProfileMargin / IdentityProfileMinCenterFaces，不擅自降低自动归属要求。
func NewIdentityAutoStrategy(cfg config.PeopleConfig) IdentityStrategy {
	return IdentityStrategy{
		Name:                 identityStrategyNameAuto,
		Version:              identityAutoStrategyVersion,
		ScoreThreshold:       cfg.IdentityProfileRescueThreshold,
		Margin:               cfg.IdentityProfileMargin,
		MinCenterFaces:       cfg.IdentityProfileMinCenterFaces,
		MinSupportingUnits:   1,
		RequireStableCenters: true,
		RequireEngineMatch:   true,
	}
}

// NewIdentitySuggestStrategy 构造人工推荐策略。
//
// 分数阈值沿用既有 MergeSuggestionThreshold（配置校验保证不高于自动归属阈值）；
// 不要求 margin 与中心稳定性，因此低支持/不稳定证据可以作为带说明的人工推荐，
// 但无效向量、硬约束冲突与不可用数据仍然不算有效证据。
func NewIdentitySuggestStrategy(cfg config.PeopleConfig) IdentityStrategy {
	return IdentityStrategy{
		Name:                 identityStrategyNameSuggest,
		Version:              identitySuggestStrategyVersion,
		ScoreThreshold:       cfg.MergeSuggestionThreshold,
		Margin:               0,
		MinCenterFaces:       cfg.IdentityProfileMinCenterFaces,
		MinSupportingUnits:   1,
		RequireStableCenters: false,
		RequireEngineMatch:   false,
	}
}

// IdentityStrategyDecision 是策略对一个引擎结果的判定。
type IdentityStrategyDecision struct {
	// Accepted 表示该策略接受本次匹配（自动归属可写入 / 可生成人工推荐）。
	Accepted bool
	// Strategy / StrategyVersion 记录生效策略，便于持久化与审计。
	Strategy        string
	StrategyVersion string
	// EngineVersion 透传引擎版本。
	EngineVersion string
	// Status 是引擎结果状态。
	Status IdentityMatchStatus
	// PersonID / Score / Margin 是被判定的最佳候选数据（无候选时为零值）。
	PersonID uint
	Score    float64
	Margin   float64
	// Reason 是未接受原因（稳定枚举）；Accepted=true 时为空串。
	Reason string
}

// ApplyIdentityStrategy 把一套策略应用到引擎结果上。
//
// 该函数不重新计算分数、不重新排序候选、不改写硬约束，只做阈值与证据门槛判定。
// 硬约束冲突、技术不可用与无效输入在任何策略下都不可接受。
func ApplyIdentityStrategy(result IdentityMatchResult, strategy IdentityStrategy) IdentityStrategyDecision {
	decision := IdentityStrategyDecision{
		Strategy:        strategy.Name,
		StrategyVersion: strategy.Version,
		EngineVersion:   result.EngineVersion,
		Status:          result.Status,
	}

	switch result.Status {
	case IdentityMatchStatusInvalid:
		decision.Reason = identityRejectInvalid
		return decision
	case IdentityMatchStatusUnavailable:
		decision.Reason = identityRejectUnavailable
		return decision
	case IdentityMatchStatusHardConflict:
		decision.Reason = identityRejectHardConflict
		return decision
	case IdentityMatchStatusNoCandidate:
		decision.Reason = identityRejectNoCandidate
		return decision
	}

	best := result.Best
	if best == nil || best.PersonID == 0 {
		// insufficient/match 必须带最佳候选；缺失属于矛盾结果，安全拒绝。
		decision.Reason = identityRejectNoCandidate
		return decision
	}
	decision.PersonID = best.PersonID
	decision.Score = best.Score
	decision.Margin = result.Margin

	// 候选自身被硬阻断时（例如逐对比较命中 cannot-link），任何策略都不接受。
	if best.Status == IdentityMatchStatusHardConflict {
		decision.Reason = identityRejectHardConflict
		return decision
	}

	if strategy.RequireEngineMatch && result.Status != IdentityMatchStatusMatch {
		decision.Reason = identityRejectNotAutoEligible
		return decision
	}
	if best.Score < strategy.ScoreThreshold {
		decision.Reason = identityRejectScoreBelowStrategy
		return decision
	}
	if strategy.MinSupportingUnits > 0 && best.SupportingUnits < strategy.MinSupportingUnits {
		decision.Reason = identityRejectInsufficientUnits
		return decision
	}
	if strategy.RequireStableCenters {
		if !best.StableCenters || best.MinSupportCount < strategy.MinCenterFaces {
			decision.Reason = identityRejectUnstableCenters
			return decision
		}
	}
	if strategy.Margin > 0 || strategy.RequireEngineMatch {
		// margin 只能在同一召回上下文内比较；上下文缺失时自动策略 fail closed。
		if !result.MarginApplicable {
			decision.Reason = identityRejectMarginUnknown
			return decision
		}
		if result.Margin < strategy.Margin {
			decision.Reason = identityRejectMarginBelowStrategy
			return decision
		}
	}

	decision.Accepted = true
	return decision
}

// identityStrategyFingerprintPayload 是参与配置指纹的策略相关参数。
// 字段顺序固定，encoding/json 按结构体声明顺序序列化，因此输出稳定可比较。
type identityStrategyFingerprintPayload struct {
	EngineVersion          string  `json:"engine_version"`
	AutoStrategyVersion    string  `json:"auto_strategy_version"`
	SuggestStrategyVersion string  `json:"suggest_strategy_version"`
	AutoScoreThreshold     float64 `json:"auto_score_threshold"`
	AutoMargin             float64 `json:"auto_margin"`
	MinCenterFaces         int     `json:"min_center_faces"`
	MinCenterPhotos        int     `json:"min_center_photos"`
	MaxCenters             int     `json:"max_centers"`
	SuggestScoreThreshold  float64 `json:"suggest_score_threshold"`
}

// IdentityStrategyFingerprint 返回生效策略配置的 SHA256 十六进制指纹。
//
// 指纹只覆盖影响身份匹配决策的参数与版本，不含端点、超时或调度参数，
// 因此调度类配置变化不会造成历史推荐被误判为过期。
func IdentityStrategyFingerprint(cfg config.PeopleConfig) string {
	auto := NewIdentityAutoStrategy(cfg)
	suggest := NewIdentitySuggestStrategy(cfg)
	payload := identityStrategyFingerprintPayload{
		EngineVersion:          identityEngineVersion,
		AutoStrategyVersion:    auto.Version,
		SuggestStrategyVersion: suggest.Version,
		AutoScoreThreshold:     auto.ScoreThreshold,
		AutoMargin:             auto.Margin,
		MinCenterFaces:         cfg.IdentityProfileMinCenterFaces,
		MinCenterPhotos:        cfg.IdentityProfileMinCenterPhotos,
		MaxCenters:             cfg.IdentityProfileMaxCenters,
		SuggestScoreThreshold:  suggest.ScoreThreshold,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// 该 payload 只含标量字段，Marshal 不会失败；保留分支避免静默忽略错误。
		return ""
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
