package repository

import (
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

type PersonMergeSuggestionRepository interface {
	ReplacePendingForTarget(targetPersonID uint, targetCategory string, items []model.PersonMergeSuggestionItem) error
	ListPending(page, pageSize int) ([]*model.PersonMergeSuggestion, int64, error)
	GetByID(id uint) (*model.PersonMergeSuggestion, error)
	GetItems(suggestionID uint, status string) ([]*model.PersonMergeSuggestionItem, error)
	MarkItemsStatus(suggestionID uint, candidateIDs []uint, status string) error
	UpdateSuggestionStatus(id uint, status string, reviewedAt *time.Time) error
	FindPendingSuggestionByCandidate(candidatePersonID uint) (*model.PersonMergeSuggestion, error)
}

type personMergeSuggestionRepository struct {
	db *gorm.DB
}

func NewPersonMergeSuggestionRepository(db *gorm.DB) PersonMergeSuggestionRepository {
	return &personMergeSuggestionRepository{db: db}
}

func (r *personMergeSuggestionRepository) ReplacePendingForTarget(targetPersonID uint, targetCategory string, items []model.PersonMergeSuggestionItem) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existingSuggestionIDs []uint
		if err := tx.Model(&model.PersonMergeSuggestion{}).
			Where("target_person_id = ? AND status = ?", targetPersonID, model.PersonMergeSuggestionStatusPending).
			Pluck("id", &existingSuggestionIDs).Error; err != nil {
			return err
		}

		if len(existingSuggestionIDs) > 0 {
			if err := tx.Model(&model.PersonMergeSuggestion{}).
				Where("id IN ?", existingSuggestionIDs).
				Update("status", model.PersonMergeSuggestionStatusObsolete).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.PersonMergeSuggestionItem{}).
				Where("suggestion_id IN ? AND status = ?", existingSuggestionIDs, model.PersonMergeSuggestionItemStatusPending).
				Update("status", model.PersonMergeSuggestionItemStatusObsolete).Error; err != nil {
				return err
			}
		}

		candidateIDs := uniqueCandidateIDs(items)
		if len(candidateIDs) > 0 {
			if err := tx.Model(&model.PersonMergeSuggestionItem{}).
				Where("candidate_person_id IN ? AND status = ?", candidateIDs, model.PersonMergeSuggestionItemStatusPending).
				Update("status", model.PersonMergeSuggestionItemStatusObsolete).Error; err != nil {
				return err
			}
			if err := r.markEmptyPendingSuggestionsObsolete(tx); err != nil {
				return err
			}
		}

		if len(items) == 0 {
			return nil
		}

		suggestion := &model.PersonMergeSuggestion{
			TargetPersonID:         targetPersonID,
			TargetCategorySnapshot: targetCategory,
			Status:                 model.PersonMergeSuggestionStatusPending,
			CandidateCount:         len(items),
			TopSimilarity:          topSimilarity(items),
		}
		if err := tx.Create(suggestion).Error; err != nil {
			return err
		}

		records := make([]model.PersonMergeSuggestionItem, 0, len(items))
		for _, item := range items {
			records = append(records, model.PersonMergeSuggestionItem{
				SuggestionID:      suggestion.ID,
				CandidatePersonID: item.CandidatePersonID,
				SimilarityScore:   item.SimilarityScore,
				Rank:              item.Rank,
				Status:            model.PersonMergeSuggestionItemStatusPending,
			})
		}
		return tx.Create(&records).Error
	})
}

func (r *personMergeSuggestionRepository) ListPending(page, pageSize int) ([]*model.PersonMergeSuggestion, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}

	var total int64
	if err := r.db.Model(&model.PersonMergeSuggestion{}).
		Where("status = ?", model.PersonMergeSuggestionStatusPending).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var suggestions []*model.PersonMergeSuggestion
	offset := (page - 1) * pageSize
	if err := r.db.Where("status = ?", model.PersonMergeSuggestionStatusPending).
		Order("id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&suggestions).Error; err != nil {
		return nil, 0, err
	}

	return suggestions, total, nil
}

func (r *personMergeSuggestionRepository) GetByID(id uint) (*model.PersonMergeSuggestion, error) {
	var suggestion model.PersonMergeSuggestion
	if err := r.db.Where("id = ?", id).First(&suggestion).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &suggestion, nil
}

func (r *personMergeSuggestionRepository) GetItems(suggestionID uint, status string) ([]*model.PersonMergeSuggestionItem, error) {
	var items []*model.PersonMergeSuggestionItem
	query := r.db.Where("suggestion_id = ?", suggestionID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if err := query.Order("rank ASC, id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

func (r *personMergeSuggestionRepository) MarkItemsStatus(suggestionID uint, candidateIDs []uint, status string) error {
	if len(candidateIDs) == 0 {
		return nil
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		var pendingSuggestionCount int64
		if err := tx.Model(&model.PersonMergeSuggestion{}).
			Where("id = ? AND status = ?", suggestionID, model.PersonMergeSuggestionStatusPending).
			Count(&pendingSuggestionCount).Error; err != nil {
			return err
		}
		if pendingSuggestionCount == 0 {
			return nil
		}

		updateResult := tx.Model(&model.PersonMergeSuggestionItem{}).
			Where("suggestion_id = ? AND candidate_person_id IN ? AND status = ?", suggestionID, candidateIDs, model.PersonMergeSuggestionItemStatusPending).
			Update("status", status)
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			return nil
		}

		if status == model.PersonMergeSuggestionItemStatusMerged {
			now := time.Now()
			if err := tx.Model(&model.PersonMergeSuggestionItem{}).
				Where("suggestion_id = ? AND status = ?", suggestionID, model.PersonMergeSuggestionItemStatusPending).
				Update("status", model.PersonMergeSuggestionItemStatusObsolete).Error; err != nil {
				return err
			}
			updateSuggestionResult := tx.Model(&model.PersonMergeSuggestion{}).
				Where("id = ? AND status = ?", suggestionID, model.PersonMergeSuggestionStatusPending).
				Updates(map[string]interface{}{
					"status":      model.PersonMergeSuggestionStatusApplied,
					"reviewed_at": &now,
				})
			if updateSuggestionResult.Error != nil {
				return updateSuggestionResult.Error
			}
			return nil
		}

		var pendingCount int64
		if err := tx.Model(&model.PersonMergeSuggestionItem{}).
			Where("suggestion_id = ? AND status = ?", suggestionID, model.PersonMergeSuggestionItemStatusPending).
			Count(&pendingCount).Error; err != nil {
			return err
		}
		if pendingCount > 0 {
			return nil
		}

		var mergedCount int64
		if err := tx.Model(&model.PersonMergeSuggestionItem{}).
			Where("suggestion_id = ? AND status = ?", suggestionID, model.PersonMergeSuggestionItemStatusMerged).
			Count(&mergedCount).Error; err != nil {
			return err
		}
		if mergedCount > 0 {
			now := time.Now()
			updateSuggestionResult := tx.Model(&model.PersonMergeSuggestion{}).
				Where("id = ? AND status = ?", suggestionID, model.PersonMergeSuggestionStatusPending).
				Updates(map[string]interface{}{
					"status":      model.PersonMergeSuggestionStatusApplied,
					"reviewed_at": &now,
				})
			if updateSuggestionResult.Error != nil {
				return updateSuggestionResult.Error
			}
			return nil
		}

		now := time.Now()
		updateSuggestionResult := tx.Model(&model.PersonMergeSuggestion{}).
			Where("id = ? AND status = ?", suggestionID, model.PersonMergeSuggestionStatusPending).
			Updates(map[string]interface{}{
				"status":      model.PersonMergeSuggestionStatusDismissed,
				"reviewed_at": &now,
			})
		if updateSuggestionResult.Error != nil {
			return updateSuggestionResult.Error
		}
		return nil
	})
}

func (r *personMergeSuggestionRepository) UpdateSuggestionStatus(id uint, status string, reviewedAt *time.Time) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if reviewedAt != nil {
		updates["reviewed_at"] = reviewedAt
	}
	return r.db.Model(&model.PersonMergeSuggestion{}).Where("id = ?", id).Updates(updates).Error
}

func (r *personMergeSuggestionRepository) FindPendingSuggestionByCandidate(candidatePersonID uint) (*model.PersonMergeSuggestion, error) {
	var suggestion model.PersonMergeSuggestion
	err := r.db.Model(&model.PersonMergeSuggestion{}).
		Joins("JOIN person_merge_suggestion_items ON person_merge_suggestion_items.suggestion_id = person_merge_suggestions.id").
		Where("person_merge_suggestions.status = ?", model.PersonMergeSuggestionStatusPending).
		Where("person_merge_suggestion_items.candidate_person_id = ? AND person_merge_suggestion_items.status = ?", candidatePersonID, model.PersonMergeSuggestionItemStatusPending).
		Order("person_merge_suggestions.id DESC").
		First(&suggestion).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &suggestion, nil
}

func (r *personMergeSuggestionRepository) markEmptyPendingSuggestionsObsolete(tx *gorm.DB) error {
	pendingItemsSubquery := tx.Model(&model.PersonMergeSuggestionItem{}).
		Select("1").
		Where("person_merge_suggestion_items.suggestion_id = person_merge_suggestions.id").
		Where("person_merge_suggestion_items.status = ?", model.PersonMergeSuggestionItemStatusPending)

	return tx.Model(&model.PersonMergeSuggestion{}).
		Where("status = ?", model.PersonMergeSuggestionStatusPending).
		Where("NOT EXISTS (?)", pendingItemsSubquery).
		Update("status", model.PersonMergeSuggestionStatusObsolete).Error
}

func uniqueCandidateIDs(items []model.PersonMergeSuggestionItem) []uint {
	seen := make(map[uint]struct{}, len(items))
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		if item.CandidatePersonID == 0 {
			continue
		}
		if _, ok := seen[item.CandidatePersonID]; ok {
			continue
		}
		seen[item.CandidatePersonID] = struct{}{}
		ids = append(ids, item.CandidatePersonID)
	}
	return ids
}

func topSimilarity(items []model.PersonMergeSuggestionItem) float64 {
	top := 0.0
	for _, item := range items {
		if item.SimilarityScore > top {
			top = item.SimilarityScore
		}
	}
	return top
}
