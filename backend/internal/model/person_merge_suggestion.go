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
}

func (PersonMergeSuggestionItem) TableName() string {
	return "person_merge_suggestion_items"
}
