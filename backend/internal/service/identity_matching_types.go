package service

// 统一身份匹配引擎的公共类型。
//
// 引擎为「待聚类组件 → 已有人物」与「人物 → 人物」两类输入提供同一套证据规范化、
// 候选召回、打分、硬约束与原因解释。适配层只负责把不同输入转换成 IdentityEvidence，
// 不允许另写第二套评分公式。
//
// 引擎只返回原始分数与证据分类，不代表业务决策；自动归属与人工推荐由
// IdentityStrategy（见 identity_matching_strategy.go）分别应用阈值与证据门槛。

// identityEngineVersion 是统一身份匹配引擎的版本标识。
// 打分公式、证据聚合或状态分类语义发生变化时必须提升该版本。
const identityEngineVersion = "identity-engine-v1"

// IdentityMatchStatus 是引擎结果的固定分类。稳定字符串，可持久化到推荐与撤销日志。
//
// 四类互不混淆的语义（详见实施方案第 3.3 节决策表）：
//   - StatusMatch / StatusInsufficient：匹配正常完成且有候选证据。
//   - StatusNoCandidate：匹配正常完成，确实没有候选。
//   - StatusHardConflict：人工 cannot-link 或同照片共现硬阻断。
//   - StatusUnavailable / StatusInvalid：技术不可用与无效输入，禁止当作「无推荐」。
type IdentityMatchStatus string

const (
	// IdentityMatchStatusMatch 存在明确目标且证据满足引擎的自动资格护栏。
	IdentityMatchStatusMatch IdentityMatchStatus = "match"
	// IdentityMatchStatusNoCandidate 匹配正常完成但没有任何候选。
	IdentityMatchStatusNoCandidate IdentityMatchStatus = "no_candidate"
	// IdentityMatchStatusInsufficient 有有效候选证据，但分数、margin、边界或中心稳定性不足。
	IdentityMatchStatusInsufficient IdentityMatchStatus = "insufficient"
	// IdentityMatchStatusHardConflict 命中人工 cannot-link 或同照片共现，两条链路均硬阻断。
	IdentityMatchStatusHardConflict IdentityMatchStatus = "hard_conflict"
	// IdentityMatchStatusUnavailable 索引、画像、负证据或数据库读取不可用，需等待恢复后重试。
	IdentityMatchStatusUnavailable IdentityMatchStatus = "unavailable"
	// IdentityMatchStatusInvalid 输入无效（无有效向量、NaN/Inf、零范数等），安全拒绝。
	IdentityMatchStatusInvalid IdentityMatchStatus = "invalid"
)

// 证据类型标识。适配层填入，仅用于诊断与日志，不参与打分。
const (
	identityEvidenceKindComponent = "component"
	identityEvidenceKindPerson    = "person"
)

// IdentityEvidenceUnit 是一份规范化的身份证据单元。
//
// 组件证据：每张清洗后的查询人脸一个单元（Vector=人脸 embedding，Weight=质量权重，
// CenterID=0，SupportCount=0）。
// 人物证据：每个活动画像中心一个单元（Vector=中心质心，Weight=1，SupportCount/SimilarityP10
// 来自中心统计，FaceID=MedoidFaceID）。
type IdentityEvidenceUnit struct {
	// CenterID 是画像中心 ID；组件人脸证据为 0。
	CenterID uint
	// FaceID 是人脸 ID（组件人脸自身，或人物中心的 medoid 人脸）。
	FaceID uint
	// PhotoID 是来源照片 ID；人物中心证据可为 0。
	PhotoID uint
	// PersonID 是该单元所属人物 ID；未指派的组件人脸为 0。
	PersonID uint
	// Vector 是已解码的 embedding，必须通过 validVector 校验。
	Vector []float32
	// Weight 是聚合权重（组件人脸质量权重，人物中心固定 1）。必须为正有限值。
	Weight float64
	// SupportCount 是支撑该单元的人脸数；组件人脸为 0，人物中心为中心样本数。
	SupportCount int
	// SimilarityP10 是中心内部相似度的 P10 边界；组件人脸为 0。
	SimilarityP10 float64
}

// tiebreakKey 返回聚合排序的稳定次序键：优先 CenterID，其次 FaceID。
// 保证同分时的浮点累加顺序确定，与 matcher 既有的 faceID 次序规则一致。
func (u IdentityEvidenceUnit) tiebreakKey() uint {
	if u.CenterID != 0 {
		return u.CenterID
	}
	return u.FaceID
}

// IdentityEvidence 是一侧身份的完整规范化证据。
type IdentityEvidence struct {
	// Kind 是证据来源类型（component / person），仅用于诊断。
	Kind string
	// PersonID 是人物证据的人物 ID；待聚类组件为 0（不为组件创建持久化假人物）。
	PersonID uint
	// Units 是证据单元集合。
	Units []IdentityEvidenceUnit
	// SourcePersonIDs 是组件人脸的来源人物 ID（去重升序），用于 cannot-link 查询。
	SourcePersonIDs []uint
	// PhotoIDs 是组件人脸的来源照片 ID（去重升序），用于同照片共现查询。
	PhotoIDs []uint
}

// IdentityPairScore 是一对身份证据的双向打分结果。
type IdentityPairScore struct {
	// Status 只取 match 之外的证据级分类：invalid 表示输入不可用于打分；
	// 其余情况为 insufficient（由调用方结合护栏进一步归类）。
	Status IdentityMatchStatus
	// Score 是最终原始身份分数：两个方向覆盖度的较小值。
	Score float64
	// ForwardScore 是 left→right 方向的聚合覆盖度。
	ForwardScore float64
	// ReverseScore 是 right→left 方向的聚合覆盖度。
	ReverseScore float64
	// Boundary 是两个方向中心 P10 边界的较大值（fail closed）。
	Boundary float64
	// CenterFitOK 表示两个方向的覆盖度均不低于各自的中心 P10 边界。
	CenterFitOK bool
	// CenterIDs 是实际参与最佳匹配的中心 ID，去重升序。
	CenterIDs []uint
	// SupportingUnits 是较弱方向上真正贡献有效权重的证据单元数。
	SupportingUnits int
	// MinSupportCount 是贡献单元所匹配的对侧中心里最小的 SupportCount；
	// 没有中心元数据（如组件人脸对组件人脸）时为 0。
	MinSupportCount int
	// StableCenters 表示所有贡献单元匹配到的对侧中心都带有支撑样本元数据
	// （SupportCount > 0）。这是数据级健全性，具体样本门槛由策略应用 MinCenterFaces 判定。
	StableCenters bool
}

// IdentityCandidateResult 是单个候选人物的打分与证据摘要。
type IdentityCandidateResult struct {
	PersonID        uint
	Score           float64
	Boundary        float64
	CenterFitOK     bool
	CenterIDs       []uint
	SupportingUnits int
	MinSupportCount int
	StableCenters   bool
	// Status 是该候选自身的分类（match / insufficient / hard_conflict）。
	Status IdentityMatchStatus
	// BlockReason 是固定枚举字符串（复用 matcher 的 block* 常量），空串表示未被阻断。
	BlockReason string
}

// IdentityMatchResult 是一次匹配请求的完整结果。
//
// 语义约定：
//   - 每个目标/每一对都必须有明确的 Status，禁止用缺失 map entry 或裸 bool 表达失败。
//   - Best 在 insufficient、hard_conflict 下依然保留，供人工推荐与诊断使用。
//   - Status=no_candidate 时 Best 为 nil。
type IdentityMatchResult struct {
	Status        IdentityMatchStatus
	EngineVersion string
	// BlockReason 是稳定枚举字符串，说明未达自动资格或不可用的具体原因。
	BlockReason string
	// Best 是最高原始身份候选。最佳候选被硬阻断时不会退而选择次佳。
	Best *IdentityCandidateResult
	// Second 是次佳候选（若存在）。
	Second *IdentityCandidateResult
	// Margin 是 Best 与 Second 的分差；仅有一个候选时按 Score-(-1) 计算。
	Margin float64
	// MarginApplicable 表示 Margin 来自同一召回上下文、可用于策略判定。
	// 显式人物对比较没有候选集合，Margin 无意义，此处为 false，自动策略必须 fail closed。
	MarginApplicable bool
	// Candidates 是本次参与排序的全部候选（含被硬阻断者），按分数降序、人物 ID 升序。
	Candidates []IdentityCandidateResult
}

// newIdentityMatchResult 构造带引擎版本的结果骨架。
func newIdentityMatchResult(status IdentityMatchStatus, blockReason string) IdentityMatchResult {
	return IdentityMatchResult{
		Status:        status,
		EngineVersion: identityEngineVersion,
		BlockReason:   blockReason,
	}
}

// IdentityRecallOptions 是候选召回预算。召回预算差异必须显式传入，
// 且不得改变核心打分公式（见实施方案第 3.5 节）。
type IdentityRecallOptions struct {
	// ANNK 是单个查询向量向 ANN 请求的最大候选人物数。
	ANNK int
	// MaxCandidates 是候选并集的最大保留数。
	MaxCandidates int
	// TopK 是每个目标最终保留的候选数（<=0 表示不额外截断）。
	TopK int
}

// normalized 用引擎默认预算补齐未设置项。默认值与既有 matcher 常量一致，
// 保证不显式传入 options 时行为不变。
func (o IdentityRecallOptions) normalized() IdentityRecallOptions {
	if o.ANNK <= 0 {
		o.ANNK = identityProfileMatcherANNK
	}
	if o.MaxCandidates <= 0 {
		o.MaxCandidates = identityProfileMatcherMaxCandidates
	}
	return o
}

// DefaultIdentityRecallOptions 返回与现有 matcher 一致的召回预算。
func DefaultIdentityRecallOptions() IdentityRecallOptions {
	return IdentityRecallOptions{}.normalized()
}
