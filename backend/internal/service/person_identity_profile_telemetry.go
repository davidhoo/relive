package service

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
)

// identityTelemetryAgreeSamplePercent 是普通 agree 决策的固定采样率（百分比）。
// 仅用于减少普通成功事件的写入量；真正防止重复写入依赖 DecisionKey 唯一约束。
const identityTelemetryAgreeSamplePercent = 10

// identityTelemetryMaxFaceIDsDisplay 是 ComponentFaceIDs 展示字段保留的最多 Face ID 数。
// 超过时截断展示并标记 ComponentFaceIDsTruncated，但 ComponentHash 仍覆盖全部 ID。
const identityTelemetryMaxFaceIDsDisplay = 512

// identityTelemetryElapsedMaxMs 是 ElapsedMilliseconds 的整数上限，防止 32 位平台溢出。
const identityTelemetryElapsedMaxMs = math.MaxInt32

// 身份画像决策遥测的稳定 Decision 枚举。
const (
	identityDecisionLegacyMissProfileHit  = "legacy_miss_profile_hit"
	identityDecisionLegacyMissProfileMiss = "legacy_miss_profile_miss"
	identityDecisionAgree                 = "agree"
	identityDecisionDisagree              = "disagree"
	identityDecisionProfileMiss           = "profile_miss"
	identityDecisionProfileUnavailable    = "profile_unavailable"
	identityDecisionProfileBlocked        = "profile_blocked"
	identityDecisionRescueApplied         = "rescue_applied"
)

// allowedIdentityDecisionReasons 列出允许写入 Reason 字段的稳定枚举。任何不在集合内的
// 原因字符串都被丢弃，避免原始错误堆栈或人名混入遥测。复用 matcher 的 block* 常量。
var allowedIdentityDecisionReasons = map[string]struct{}{
	blockIndexUnavailable:        {},
	blockInvalidQuery:            {},
	blockProfileUnavailable:      {},
	blockScoreBelowThreshold:     {},
	blockMarginTooSmall:          {},
	blockBelowCenterBoundary:     {},
	blockUnstableCenter:          {},
	blockCannotLink:              {},
	blockSamePhotoCooccurrence:   {},
	blockNegativeEvidenceUnavail: {},
}

// IdentityTelemetryInput 是与聚类实现解耦的遥测输入。Task 9 不依赖 Task 11 尚未实现的
// legacyMatchResult，调用方（Task 11）在画像评分完成后构造此结构并调用 Record。
//
// 不包含 embedding、图片路径、缩略图路径或人物名称字段，结构上禁止敏感数据进入遥测。
type IdentityTelemetryInput struct {
	Mode                 string
	ComponentFaceIDs     []uint
	LegacyTargetPersonID uint
	LegacyScore          float64
	LegacyMatched        bool

	Profile          IdentityProfileMatch
	Elapsed          time.Duration
	AlgorithmVersion string
	IndexGeneration  int

	// RescueApplied 标记本次 profile 结果被 rescue 模式应用（legacy miss → 挂靠已有人物）。
	// 为 true 时 Decision 固定为 rescue_applied，且全量记录不采样。Task 12 新增。
	RescueApplied bool
}

// identityProfileTelemetry 以 best-effort 方式记录身份画像 shadow/rescue 决策遥测。
// 遥测失败不影响聚类、合并或人物写入：Record 不返回错误、不重试、不持有聚类事务或 writeGate。
type identityProfileTelemetry struct {
	repo repository.PeopleIdentityDecisionRepository
}

// NewIdentityProfileTelemetry 构造遥测服务。repo 可为 nil（Record 将安全 no-op）。
func NewIdentityProfileTelemetry(repo repository.PeopleIdentityDecisionRepository) *identityProfileTelemetry {
	return &identityProfileTelemetry{repo: repo}
}

// Record 记录一次身份画像决策。legacy 模式完全 no-op；普通 agree 结果按组件 hash 确定性
// 采样；其余高价值场景（legacy miss、分歧、unavailable、rescue_applied）全量记录。Repository
// 写入失败只记录脱敏 warning，不向调用方返回错误，不修改输入对象，不重试，不调用人物/Face/Profile
// 写接口。
func (t *identityProfileTelemetry) Record(input IdentityTelemetryInput) {
	if t == nil {
		return
	}
	// legacy 模式：不计算 hash、不访问 Repository、不写遥测。
	if input.Mode == model.PeopleIdentityModeLegacy {
		return
	}
	if t.repo == nil {
		return
	}

	cleansed := dedupSortUint(input.ComponentFaceIDs)
	hashBytes := computeComponentHashBytes(cleansed)

	decision, reason := classifyIdentityDecision(input)

	// 普通一致结果确定性采样；legacy miss、分歧、unavailable、rescue_applied 永不采样。
	if decision == identityDecisionAgree && !sampleIdentityDecision(hashBytes) {
		return
	}

	d := buildIdentityDecision(input, cleansed, hashBytes, decision, reason)
	if _, err := t.repo.CreateIgnore(d); err != nil {
		// 仅记录脱敏字段，不输出 Face ID、Person ID、决策内容或错误堆栈。
		logger.Warnf("identity decision telemetry write failed: mode=%s decision=%s err_category=%T",
			input.Mode, decision, err)
	}
}

// classifyIdentityDecision 根据输入判定稳定 Decision 值与 sanitized Reason。
//
// RescueApplied 优先级最高：rescue 模式在 legacy miss 边界把组件挂靠到画像找到的已有
// 人物，固定记 rescue_applied，无论 profile 是否 AutoEligible（能 rescue 即已通过全部护栏）。
//
// 画像不可用（Available=false）时优先记录 profile_unavailable，无论 legacy 是否匹配，
// 以便测量不可用比例。AutoEligible=false 且画像人物与 legacy 一致时记 profile_blocked；
// 不一致时记 disagree 并保留阻断原因。
func classifyIdentityDecision(in IdentityTelemetryInput) (decision, reason string) {
	if in.RescueApplied {
		return identityDecisionRescueApplied, ""
	}
	if !in.Profile.Available {
		return identityDecisionProfileUnavailable, sanitizeReason(in.Profile.BlockReason)
	}
	if !in.LegacyMatched {
		if in.Profile.PersonID != 0 {
			return identityDecisionLegacyMissProfileHit, ""
		}
		return identityDecisionLegacyMissProfileMiss, ""
	}
	// legacy 已匹配
	if in.Profile.PersonID == 0 {
		return identityDecisionProfileMiss, ""
	}
	if in.Profile.PersonID == in.LegacyTargetPersonID {
		if in.Profile.AutoEligible {
			return identityDecisionAgree, ""
		}
		return identityDecisionProfileBlocked, sanitizeReason(in.Profile.BlockReason)
	}
	// 画像人物与 legacy 不同
	if !in.Profile.AutoEligible {
		return identityDecisionDisagree, sanitizeReason(in.Profile.BlockReason)
	}
	return identityDecisionDisagree, ""
}

// buildIdentityDecision 构造已清洗的 PeopleIdentityDecision。所有 Face ID / Center ID 均排序
// 去重；Person ID 0 与非法分数转为 NULL；负耗时归零；字段长度受限。不写入 embedding/路径/人名。
func buildIdentityDecision(in IdentityTelemetryInput, cleansedFaceIDs []uint, hashBytes []byte, decision, reason string) *model.PeopleIdentityDecision {
	faceCSV, truncated := formatFaceIDs(cleansedFaceIDs)
	componentHash := hex.EncodeToString(hashBytes)
	algVer := sanitizeAlgVer(in.AlgorithmVersion)

	return &model.PeopleIdentityDecision{
		Mode:                      in.Mode,
		ComponentHash:             componentHash,
		ComponentSize:             len(cleansedFaceIDs),
		ComponentFaceIDs:          faceCSV,
		ComponentFaceIDsTruncated: truncated,
		DecisionKey:               computeDecisionKey(in.Mode, componentHash, in.LegacyTargetPersonID, in.Profile.PersonID, decision, reason, algVer),
		LegacyTargetPersonID:      uintPtrOrNull(in.LegacyTargetPersonID),
		LegacyScore:               scorePtrOrNull(in.LegacyScore),
		ProfileBestPersonID:       uintPtrOrNull(in.Profile.PersonID),
		ProfileBestScore:          scorePtrOrNull(in.Profile.Score),
		ProfileSecondPersonID:     uintPtrOrNull(in.Profile.SecondPersonID),
		ProfileSecondScore:        scorePtrOrNull(in.Profile.SecondScore),
		Margin:                    sanitizeMargin(in.Profile.Margin),
		CenterIDs:                 joinUintCSV(dedupSortUint(in.Profile.CenterIDs)),
		Decision:                  decision,
		Reason:                    reason,
		ElapsedMilliseconds:       sanitizeElapsed(in.Elapsed),
		AlgorithmVersion:          algVer,
		IndexGeneration:           sanitizeIndexGen(in.IndexGeneration),
	}
}

// computeComponentHashBytes 对排序去重后的 Face ID 计算 SHA-256，返回 32 字节摘要。
// 使用大端 uint64 逐 ID 写入，保证输入顺序无关、结果确定。
func computeComponentHashBytes(ids []uint) []byte {
	h := sha256.New()
	var buf [8]byte
	for _, id := range ids {
		binary.BigEndian.PutUint64(buf[:], uint64(id))
		h.Write(buf[:])
	}
	return h.Sum(nil)
}

// computeDecisionKey 计算 DecisionKey（64 字符 hex）。包含 mode、component_hash、
// legacy target、profile target、decision、reason、algorithm version，保证相同决策幂等、
// 组件结果变化时产生新 key。
func computeDecisionKey(mode, componentHash string, legacyTarget, profileTarget uint, decision, reason, algVer string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d|%d|%s|%s|%s", mode, componentHash, legacyTarget, profileTarget, decision, reason, algVer)
	return hex.EncodeToString(h.Sum(nil))
}

// sampleIdentityDecision 以 component hash 前 8 字节大端 uint64 对 100 取模判定是否采样。
// 同一组件每次重试结果一致，不依赖随机数或当前时间。
func sampleIdentityDecision(hashBytes []byte) bool {
	if len(hashBytes) < 8 {
		return false
	}
	v := binary.BigEndian.Uint64(hashBytes[:8])
	return v%100 < uint64(identityTelemetryAgreeSamplePercent)
}

// formatFaceIDs 将 Face ID 切片格式化为逗号分隔字符串，仅保留前 512 个，返回是否截断。
func formatFaceIDs(ids []uint) (string, bool) {
	if len(ids) == 0 {
		return "", false
	}
	end := len(ids)
	truncated := false
	if end > identityTelemetryMaxFaceIDsDisplay {
		end = identityTelemetryMaxFaceIDsDisplay
		truncated = true
	}
	parts := make([]string, 0, end)
	for _, id := range ids[:end] {
		parts = append(parts, strconv.FormatUint(uint64(id), 10))
	}
	return strings.Join(parts, ","), truncated
}

// joinUintCSV 将 ID 切片格式化为逗号分隔字符串（已假定排序去重）。
func joinUintCSV(ids []uint) string {
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatUint(uint64(id), 10))
	}
	return strings.Join(parts, ",")
}

// uintPtrOrNull 将 0 转为 NULL（nil 指针），用于 Person ID 字段。
func uintPtrOrNull(v uint) *uint {
	if v == 0 {
		return nil
	}
	return &v
}

// scorePtrOrNull 将 NaN/Inf 分数转为 NULL（nil 指针），否则返回指针。
func scorePtrOrNull(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

// sanitizeMargin 将 NaN/Inf margin 归零（该字段为 NOT NULL）。
func sanitizeMargin(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return v
}

// sanitizeReason 只允许已知枚举原因，最长 100 字符，其余返回空串。
func sanitizeReason(r string) string {
	if r == "" {
		return ""
	}
	if _, ok := allowedIdentityDecisionReasons[r]; !ok {
		return ""
	}
	if len(r) > 100 {
		return r[:100]
	}
	return r
}

// sanitizeAlgVer 限制算法版本最长 50 字符。
func sanitizeAlgVer(v string) string {
	if len(v) > 50 {
		return v[:50]
	}
	return v
}

// sanitizeIndexGen 禁止负数。
func sanitizeIndexGen(g int) int {
	if g < 0 {
		return 0
	}
	return g
}

// sanitizeElapsed 将耗时转为毫秒整数，负数归零，上限 MaxInt32。
func sanitizeElapsed(d time.Duration) int {
	ms := d.Milliseconds()
	if ms < 0 {
		ms = 0
	}
	if ms > identityTelemetryElapsedMaxMs {
		ms = identityTelemetryElapsedMaxMs
	}
	return int(ms)
}

// dedupSortUint 在 matcher 文件中实现（去重 + 过滤 0 + 升序），本文件直接复用。
