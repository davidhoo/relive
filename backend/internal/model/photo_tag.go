package model

import "strings"

// PhotoTag 照片标签独立表
type PhotoTag struct {
	ID      uint   `gorm:"primarykey"`
	PhotoID uint   `gorm:"not null;uniqueIndex:idx_photo_tag_unique,priority:1"`
	Tag     string `gorm:"type:varchar(100);not null;index:idx_photo_tag_tag;uniqueIndex:idx_photo_tag_unique,priority:2"`
}

func (PhotoTag) TableName() string {
	return "photo_tags"
}

// SplitTags 将逗号分隔的标签字符串拆分为去重的切片
func SplitTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
