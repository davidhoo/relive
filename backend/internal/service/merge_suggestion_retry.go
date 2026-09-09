package service

import "time"

// 合并推荐技术失败重试预算：防止永久 unavailable 目标造成全库忙循环与 pending churn。
const (
	mergeSuggestionRetryMaxAttempts = 5
	mergeSuggestionRetryBaseSeconds = 300  // 5 分钟起步
	mergeSuggestionRetryMaxSeconds  = 3600 // 上限 1 小时
)

type mergeSuggestionRetryEntry struct {
	TargetID    uint      `json:"target_id"`
	Attempts    int       `json:"attempts"`
	NextRetryAt time.Time `json:"next_retry_at"`
	Reason      string    `json:"reason,omitempty"`
}

func mergeSuggestionRetryBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	sec := mergeSuggestionRetryBaseSeconds
	for i := 1; i < attempts; i++ {
		if sec >= mergeSuggestionRetryMaxSeconds/2 {
			sec = mergeSuggestionRetryMaxSeconds
			break
		}
		sec *= 2
	}
	if sec > mergeSuggestionRetryMaxSeconds {
		sec = mergeSuggestionRetryMaxSeconds
	}
	return time.Duration(sec) * time.Second
}

// classifyMergeSuggestionRetries 拆出到期可跑、仍需保留、已耗尽的重试条目。
// exhausted：Attempts 已达上限（无论 NextRetryAt）；due：未耗尽且 NextRetryAt<=now。
func classifyMergeSuggestionRetries(entries []mergeSuggestionRetryEntry, now time.Time) (due, remaining, exhausted []mergeSuggestionRetryEntry) {
	for _, e := range entries {
		if e.TargetID == 0 {
			continue
		}
		if e.Attempts >= mergeSuggestionRetryMaxAttempts {
			exhausted = append(exhausted, e)
			continue
		}
		remaining = append(remaining, e)
		if !e.NextRetryAt.After(now) {
			due = append(due, e)
		}
	}
	return due, remaining, exhausted
}

// mergeSuggestionEndOfPassFlags 在主 cursor 扫完后决定是否立即再 Dirty。
// 仅当存在到期重试时保持 Dirty，且标记 RetryOnly，避免无界全库重扫。
func mergeSuggestionEndOfPassFlags(due, remaining []mergeSuggestionRetryEntry) (dirty, retryOnly bool) {
	if len(due) > 0 {
		return true, true
	}
	_ = remaining
	return false, false
}

// upsertMergeSuggestionRetry 记录/推进一次技术失败重试。
func upsertMergeSuggestionRetry(entries []mergeSuggestionRetryEntry, targetID uint, reason string, now time.Time) []mergeSuggestionRetryEntry {
	if targetID == 0 {
		return entries
	}
	for i := range entries {
		if entries[i].TargetID != targetID {
			continue
		}
		entries[i].Attempts++
		if entries[i].Attempts < 1 {
			entries[i].Attempts = 1
		}
		entries[i].NextRetryAt = now.Add(mergeSuggestionRetryBackoff(entries[i].Attempts))
		if reason != "" {
			entries[i].Reason = reason
		}
		return entries
	}
	return append(entries, mergeSuggestionRetryEntry{
		TargetID:    targetID,
		Attempts:    1,
		NextRetryAt: now.Add(mergeSuggestionRetryBackoff(1)),
		Reason:      reason,
	})
}

func removeMergeSuggestionRetries(entries []mergeSuggestionRetryEntry, done map[uint]struct{}) []mergeSuggestionRetryEntry {
	if len(entries) == 0 || len(done) == 0 {
		return entries
	}
	out := make([]mergeSuggestionRetryEntry, 0, len(entries))
	for _, e := range entries {
		if _, ok := done[e.TargetID]; ok {
			continue
		}
		out = append(out, e)
	}
	return out
}

func dueMergeSuggestionRetryIDs(entries []mergeSuggestionRetryEntry, now time.Time) []uint {
	due, _, _ := classifyMergeSuggestionRetries(entries, now)
	ids := make([]uint, 0, len(due))
	for _, e := range due {
		ids = append(ids, e.TargetID)
	}
	return ids
}

// modelPersonMergeItemView 是 pending 幂等比较的轻量视图（避免测试依赖完整 model）。
type modelPersonMergeItemView struct {
	CandidateID uint
	Score       float64
	MatchSource string
	Reason      string
	ProfileGen  int
}

func pendingSuggestionItemsEquivalent(a, b []modelPersonMergeItemView) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	type key struct {
		id     uint
		score  int64 // score * 1e6
		source string
		reason string
		gen    int
	}
	counts := make(map[key]int, len(a))
	for _, it := range a {
		counts[key{id: it.CandidateID, score: int64(it.Score * 1e6), source: it.MatchSource, reason: it.Reason, gen: it.ProfileGen}]++
	}
	for _, it := range b {
		k := key{id: it.CandidateID, score: int64(it.Score * 1e6), source: it.MatchSource, reason: it.Reason, gen: it.ProfileGen}
		if counts[k] == 0 {
			return false
		}
		counts[k]--
	}
	return true
}
