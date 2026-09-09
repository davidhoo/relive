package model

import "time"

const (
	PersonMergeSuggestionStatusPending   = "pending"
	PersonMergeSuggestionStatusApplied   = "applied"
	PersonMergeSuggestionStatusDismissed = "dismissed"
	PersonMergeSuggestionStatusObsolete  = "obsolete"
)

const (
	PersonMergeSuggestionItemStatusPending  = "pending"
	PersonMergeSuggestionItemStatusExcluded = "excluded"
	PersonMergeSuggestionItemStatusMerged   = "merged"
	PersonMergeSuggestionItemStatusObsolete = "obsolete"
)

// 合并建议候选来源。MatchSource 标识候选由哪条召回路径产生。
const (
	// PersonMergeMatchSourceLegacy 表示候选来自现有 prototype ANN 路径
	// （含 profile 不可用或 medoid 验证失败时的逐对回退）。
	PersonMergeMatchSourceLegacy = "legacy"
	// PersonMergeMatchSourceIdentityProfile 表示候选来自身份画像中心召回
	// 并通过活动中心 + 真实 medoid 验证。
	PersonMergeMatchSourceIdentityProfile = "identity_profile"
)

// 合并建议警告类型。Warning 为空表示无警告；目前仅同照片共现一种。
const (
	// PersonMergeWarningSamePhotoCooccurrence 表示目标与候选曾出现在同一张照片，
	// 仅作为人工审核提示，不阻断建议。
	PersonMergeWarningSamePhotoCooccurrence = "same_photo_cooccurrence"
)

// 待审建议过期/需重验原因（写入 StaleReason，保留 pending 供展示，审核前须重验）。
const (
	// PersonMergeStaleReasonRetryExhausted 表示目标因技术不可用耗尽重试预算，
	// 历史 pending 证据可能过期，不能无条件接受。
	PersonMergeStaleReasonRetryExhausted = "retry_exhausted_needs_revalidation"
)

type PersonMergeSuggestion struct {
	ID                     uint       `gorm:"primarykey" json:"id"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	TargetPersonID         uint       `gorm:"not null;index:idx_pms_target_status,priority:1" json:"target_person_id"`
	TargetCategorySnapshot string     `gorm:"type:varchar(20);not null" json:"target_category_snapshot"`
	Status                 string     `gorm:"type:varchar(20);not null;index:idx_pms_status;index:idx_pms_target_status,priority:2;check:chk_pms_status,status IN ('pending','applied','dismissed','obsolete')" json:"status"`
	CandidateCount         int        `gorm:"not null;default:0" json:"candidate_count"`
	TopSimilarity          float64    `gorm:"not null;default:0" json:"top_similarity"`
	ReviewedAt             *time.Time `json:"reviewed_at,omitempty"`

	// primary 统一引擎元数据。legacy/混合路径生成的建议可为空；切换 primary 后旧待审
	// 建议会被标记 obsolete 并写入 StaleReason。
	EngineVersion           string `gorm:"type:varchar(50);not null;default:''" json:"engine_version,omitempty"`
	StrategyVersion         string `gorm:"type:varchar(50);not null;default:''" json:"strategy_version,omitempty"`
	ConfigFingerprint       string `gorm:"type:varchar(64);not null;default:''" json:"config_fingerprint,omitempty"`
	IndexGeneration         int    `gorm:"not null;default:0" json:"index_generation,omitempty"`
	TargetProfileGeneration int    `gorm:"not null;default:0" json:"target_profile_generation,omitempty"`
	StaleReason             string `gorm:"type:varchar(100);not null;default:''" json:"stale_reason,omitempty"`
}

func (PersonMergeSuggestion) TableName() string {
	return "person_merge_suggestions"
}

type PersonMergeSuggestionItem struct {
	ID                uint      `gorm:"primarykey" json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	SuggestionID      uint      `gorm:"not null;index:idx_pmsi_suggestion_status,priority:1;index:idx_pmsi_suggestion_rank,priority:1;uniqueIndex:idx_pmsi_suggestion_candidate,priority:1" json:"suggestion_id"`
	CandidatePersonID uint      `gorm:"not null;index:idx_pmsi_candidate;uniqueIndex:idx_pmsi_suggestion_candidate,priority:2" json:"candidate_person_id"`
	SimilarityScore   float64   `gorm:"not null" json:"similarity_score"`
	Rank              int       `gorm:"not null;default:0;index:idx_pmsi_suggestion_rank,priority:2" json:"rank"`
	Status            string    `gorm:"type:varchar(20);not null;index:idx_pmsi_status;index:idx_pmsi_suggestion_status,priority:2;check:chk_pmsi_status,status IN ('pending','excluded','merged','obsolete')" json:"status"`
	// MatchSource 标识候选来源（legacy / identity_profile）。历史数据迁移后默认 legacy。
	MatchSource string `gorm:"type:varchar(30);not null;default:legacy" json:"match_source"`
	// Warning 为人工审核提示（空字符串或 same_photo_cooccurrence）。仅 identity_profile
	// 候选可能携带同照片共现警告；legacy 候选始终为空。primary 下同照片共现为硬阻断，不进推荐。
	Warning string `gorm:"type:varchar(100)" json:"warning,omitempty"`
	// Reason 是统一引擎给出的结构化原因（证据不足、硬冲突等），供审核 UI 展示。
	Reason string `gorm:"type:varchar(100);not null;default:''" json:"reason,omitempty"`
	// CandidateProfileGeneration 记录生成时候选画像 generation，接受时用于重验。
	CandidateProfileGeneration int `gorm:"not null;default:0" json:"candidate_profile_generation,omitempty"`
	// Margin 是生成时最佳与次佳候选的分数差（可空表示单候选）。
	Margin *float64 `json:"margin,omitempty"`

	// 以下字段仅用于 ReplacePending 写入父建议行，不落 item 表。
	EngineVersion           string `gorm:"-" json:"-"`
	StrategyVersion         string `gorm:"-" json:"-"`
	ConfigFingerprint       string `gorm:"-" json:"-"`
	IndexGeneration         int    `gorm:"-" json:"-"`
	TargetProfileGeneration int    `gorm:"-" json:"-"`
}

func (PersonMergeSuggestionItem) TableName() string {
	return "person_merge_suggestion_items"
}
