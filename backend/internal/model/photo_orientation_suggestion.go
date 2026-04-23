package model

import "time"

const (
	OrientationSuggestionStatusPending   = "pending"
	OrientationSuggestionStatusApplied   = "applied"
	OrientationSuggestionStatusDismissed = "dismissed"
)

// PhotoOrientationSuggestion represents a suggested rotation for a photo.
// It stores the suggested clockwise rotation angle (0, 90, 180, or 270 degrees)
// that should be applied to make the photo correctly oriented.
type PhotoOrientationSuggestion struct {
	ID                uint      `gorm:"primarykey" json:"id"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	PhotoID           uint      `gorm:"uniqueIndex:idx_pos_photo;not null" json:"photo_id"`
	SuggestedRotation int       `gorm:"not null" json:"suggested_rotation"` // 0, 90, 180, or 270
	Confidence        float64   `gorm:"not null" json:"confidence"`
	LowConfidence     bool      `gorm:"not null;default:false" json:"low_confidence"`
	Status            string    `gorm:"type:varchar(20);not null;default:'pending';index:idx_pos_status;check:chk_pos_status,status IN ('pending','applied','dismissed')" json:"status"`
}

func (PhotoOrientationSuggestion) TableName() string {
	return "photo_orientation_suggestions"
}

// OrientationSuggestionGroup represents a group of suggestions with the same rotation.
type OrientationSuggestionGroup struct {
	SuggestedRotation int     `json:"suggested_rotation"`
	Count             int     `json:"count"`
	AvgConfidence     float64 `json:"avg_confidence"`
	LowConfidenceCount int    `json:"low_confidence_count"`
}

// OrientationSuggestionStats represents statistics for orientation suggestions.
type OrientationSuggestionStats struct {
	Total          int64 `json:"total"`
	Pending        int64 `json:"pending"`
	Applied        int64 `json:"applied"`
	Dismissed      int64 `json:"dismissed"`
	LowConfidence  int64 `json:"low_confidence"`
}

// OrientationSuggestionTask represents the background task state.
type OrientationSuggestionTask struct {
	Status         string     `json:"status"`
	CurrentMessage string     `json:"current_message"`
	ProcessedCount int64      `json:"processed_count"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	StoppedAt      *time.Time `json:"stopped_at,omitempty"`
}

// OrientationSuggestionPhoto represents a photo with its orientation suggestion for display.
type OrientationSuggestionPhoto struct {
	ID                uint    `json:"id"`
	FileName          string  `json:"file_name"`
	ThumbnailPath     string  `json:"thumbnail_path"`
	CurrentRotation   int     `json:"current_rotation"`
	SuggestedRotation int     `json:"suggested_rotation"`
	Confidence        float64 `json:"confidence"`
	LowConfidence     bool    `json:"low_confidence"`
}

// OrientationSuggestionDetail represents detailed suggestions for a specific rotation.
type OrientationSuggestionDetail struct {
	SuggestedRotation int                           `json:"suggested_rotation"`
	Photos            []OrientationSuggestionPhoto  `json:"photos"`
	Total             int64                         `json:"total"`
}
