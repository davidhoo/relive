package service

import (
	"math"
	"sort"
)

// 统一身份证据打分。两个方向共用 matcher 已有的 aggregateWeighted 聚合规则与
// cosineSimilarity，不引入第二套评分公式。

// ScoreIdentityEvidence 计算两份身份证据的原始身份分数。
//
// 双向计算：分别求 left→right 与 right→left 的覆盖度，最终分数取两者较小值。
// 取最小值的目的是避免单个高分中心掩盖混合身份——一侧多中心里只要有中心在对侧
// 找不到对应证据，该方向的聚合覆盖度就会被拉低，从而不会因另一方向命中而虚高。
// 该定义对交换输入天然对称：ScoreIdentityEvidence(a, b).Score == ScoreIdentityEvidence(b, a).Score。
//
// 单方向覆盖度：对该侧每个证据单元取其对对侧所有单元的最佳 cosine，再按单元权重
// 用 aggregateWeighted（1 个 / 2–4 个 / >=5 个分档的稳健聚合）汇总。
//
// 任一单元向量非法、权重非正或无有效可比单元时返回 Status=invalid（fail closed），
// 不做静默跳过。任一侧没有证据单元时同样返回 invalid：调用方负责把「召回确实无候选」
// 表达为 no_candidate，把「画像不可读」表达为 unavailable。
func ScoreIdentityEvidence(left, right IdentityEvidence) IdentityPairScore {
	if len(left.Units) == 0 || len(right.Units) == 0 {
		return IdentityPairScore{Status: IdentityMatchStatusInvalid}
	}
	if !identityUnitsUsable(left.Units) || !identityUnitsUsable(right.Units) {
		return IdentityPairScore{Status: IdentityMatchStatusInvalid}
	}

	forward, okFwd := scoreIdentityDirection(left.Units, right.Units)
	reverse, okRev := scoreIdentityDirection(right.Units, left.Units)
	if !okFwd || !okRev {
		// 两侧维度完全不匹配，没有任何可比单元对。
		return IdentityPairScore{Status: IdentityMatchStatusInvalid}
	}

	score := minFloat64(forward.score, reverse.score)
	boundary := forward.boundary
	if reverse.boundary > boundary {
		boundary = reverse.boundary
	}

	// 较弱方向决定最终分数，支撑统计也取该方向，避免用较强方向的覆盖度掩盖不足。
	weaker := forward
	if reverse.score < forward.score {
		weaker = reverse
	}

	centerIDs := append(append([]uint{}, forward.matchedCenterIDs...), reverse.matchedCenterIDs...)

	return IdentityPairScore{
		Status:          IdentityMatchStatusInsufficient,
		Score:           score,
		ForwardScore:    forward.score,
		ReverseScore:    reverse.score,
		Boundary:        boundary,
		CenterFitOK:     forward.score >= forward.boundary && reverse.score >= reverse.boundary,
		CenterIDs:       dedupSortUint(centerIDs),
		SupportingUnits: weaker.supportingUnits,
		MinSupportCount: weaker.minSupportCount,
		StableCenters:   weaker.stableCenters,
	}
}

// identityDirectionScore 是单方向覆盖度的计算结果。
type identityDirectionScore struct {
	score    float64
	boundary float64
	// matchedCenterIDs 是被匹配到的对侧中心 ID（仅贡献单元），去重前的原始集合。
	matchedCenterIDs []uint
	supportingUnits  int
	minSupportCount  int
	stableCenters    bool
}

// identityUnitsUsable 校验一侧全部证据单元：向量必须通过 validVector，
// 权重必须为正有限值。任一单元非法即整体不可用（fail closed）。
func identityUnitsUsable(units []IdentityEvidenceUnit) bool {
	for _, u := range units {
		if !validVector(u.Vector) {
			return false
		}
		if math.IsNaN(u.Weight) || math.IsInf(u.Weight, 0) || u.Weight <= 0 {
			return false
		}
	}
	return true
}

// scoreIdentityDirection 计算 from→to 方向的聚合覆盖度。
//
// 对 from 的每个单元，在 to 中选择 cosine 最高的单元（同分时取次序键较小者）；
// 维度不一致的组合跳过。所有 from 单元都找不到可比对侧单元时返回 ok=false。
func scoreIdentityDirection(from, to []IdentityEvidenceUnit) (identityDirectionScore, bool) {
	// 对侧按次序键升序，保证同分时选择较小 CenterID/FaceID（迭代用严格 > 更新）。
	opposing := make([]IdentityEvidenceUnit, len(to))
	copy(opposing, to)
	sort.SliceStable(opposing, func(i, j int) bool {
		return opposing[i].tiebreakKey() < opposing[j].tiebreakKey()
	})

	items := make([]aggregateInput, 0, len(from))
	p10Items := make([]aggregateInput, 0, len(from))
	matched := make([]IdentityEvidenceUnit, 0, len(from))

	for _, u := range from {
		bestIdx := -1
		bestSim := -2.0
		for k, o := range opposing {
			if len(u.Vector) != len(o.Vector) {
				continue // 维度不一致，跳过该组合
			}
			sim := cosineSimilarity(u.Vector, o.Vector)
			if sim > bestSim {
				bestSim = sim
				bestIdx = k
			}
		}
		if bestIdx < 0 {
			continue
		}
		o := opposing[bestIdx]
		items = append(items, aggregateInput{value: bestSim, weight: u.Weight, faceID: u.tiebreakKey()})
		p10Items = append(p10Items, aggregateInput{value: o.SimilarityP10, weight: u.Weight, faceID: u.tiebreakKey()})
		matched = append(matched, o)
	}
	if len(items) == 0 {
		return identityDirectionScore{}, false
	}

	score, contrib := aggregateWeighted(items)
	boundary, _ := aggregateWeighted(p10Items)

	out := identityDirectionScore{
		score:           score,
		boundary:        boundary,
		supportingUnits: len(contrib),
		stableCenters:   true,
	}
	minSupport := math.MaxInt32
	sawCenter := false
	for _, ci := range contrib {
		o := matched[ci]
		if o.CenterID != 0 {
			out.matchedCenterIDs = append(out.matchedCenterIDs, o.CenterID)
		}
		if o.CenterID == 0 && o.SupportCount == 0 {
			continue // 组件人脸证据没有中心统计，不参与稳定性判断
		}
		sawCenter = true
		if o.SupportCount < minSupport {
			minSupport = o.SupportCount
		}
		if o.SupportCount <= 0 {
			out.stableCenters = false
		}
	}
	if sawCenter {
		out.minSupportCount = minSupport
	} else {
		// 对侧没有中心元数据（组件对组件）：无法判断中心稳定性，按 0 支撑处理。
		out.minSupportCount = 0
		out.stableCenters = false
	}
	return out, true
}
