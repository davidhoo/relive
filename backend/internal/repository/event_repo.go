package repository

import (
	"fmt"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// EventRepository 事件仓储接口
type EventRepository interface {
	Create(event *model.Event) error
	Update(event *model.Event) error
	Delete(id uint) error
	GetByID(id uint) (*model.Event, error)
	List(page, pageSize int) ([]*model.Event, int64, error)
	DeleteAll() error
	GetByTimeRange(start, end time.Time) ([]*model.Event, error)
	UpdateProfileFields(id uint, fields map[string]interface{}) error

	// 策展引擎提名查询
	GetOnThisDayEvents(monthDay string, days int, excludeIDs []uint, limit int) ([]*model.Event, error)
	GetTopScoredEvents(excludeIDs []uint, limit int) ([]*model.Event, error)
	GetFarthestEvents(lat, lon float64, excludeIDs []uint, limit int) ([]*model.Event, error)
	GetNeverDisplayedEvents(minScore float64, excludeIDs []uint, limit int) ([]*model.Event, error)
	GetRecentlyDisplayedEventIDs(days int) ([]uint, error)
	IncrementDisplayCount(eventID uint) error
	GetPeopleEvents(excludeIDs []uint, limit int) ([]*model.Event, error)
	GetSeasonEvents(keywords []string, excludeIDs []uint, limit int) ([]*model.Event, error)
}

type eventRepository struct {
	db *gorm.DB
}

// NewEventRepository 创建事件仓储
func NewEventRepository(db *gorm.DB) EventRepository {
	return &eventRepository{db: db}
}

func (r *eventRepository) Create(event *model.Event) error {
	return r.db.Create(event).Error
}

func (r *eventRepository) Update(event *model.Event) error {
	return r.db.Save(event).Error
}

func (r *eventRepository) Delete(id uint) error {
	return r.db.Delete(&model.Event{}, id).Error
}

func (r *eventRepository) GetByID(id uint) (*model.Event, error) {
	var event model.Event
	if err := r.db.First(&event, id).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func (r *eventRepository) List(page, pageSize int) ([]*model.Event, int64, error) {
	var events []*model.Event
	var total int64

	if err := r.db.Model(&model.Event{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	if err := r.db.Order("start_time DESC").Offset(offset).Limit(pageSize).Find(&events).Error; err != nil {
		return nil, 0, err
	}

	return events, total, nil
}

func (r *eventRepository) DeleteAll() error {
	return r.db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&model.Event{}).Error
}

func (r *eventRepository) GetByTimeRange(start, end time.Time) ([]*model.Event, error) {
	var events []*model.Event
	if err := r.db.Where("start_time <= ? AND end_time >= ?", end, start).Find(&events).Error; err != nil {
		return nil, err
	}
	return events, nil
}

func (r *eventRepository) UpdateProfileFields(id uint, fields map[string]interface{}) error {
	return r.db.Model(&model.Event{}).Where("id = ?", id).Updates(fields).Error
}

// validEventScope 过滤有效事件：有照片且有封面照片
func validEventScope(db *gorm.DB) *gorm.DB {
	return db.Where("photo_count > 0 AND cover_photo_id IS NOT NULL")
}

// GetOnThisDayEvents 往年同月日 ±N 天的事件（时光隧道）
func (r *eventRepository) GetOnThisDayEvents(monthDay string, days int, excludeIDs []uint, limit int) ([]*model.Event, error) {
	var events []*model.Event

	// 解析基准月日
	baseDate, err := time.Parse("01-02", monthDay)
	if err != nil {
		return nil, err
	}
	startDate := baseDate.AddDate(0, 0, -days)
	endDate := baseDate.AddDate(0, 0, days)
	mdStart := startDate.Format("01-02")
	mdEnd := endDate.Format("01-02")

	query := r.db.Model(&model.Event{}).Scopes(validEventScope)

	if mdStart > mdEnd {
		// 跨年边界
		query = query.Where("(strftime('%m-%d', start_time) >= ? OR strftime('%m-%d', start_time) <= ?)", mdStart, mdEnd)
	} else {
		query = query.Where("strftime('%m-%d', start_time) BETWEEN ? AND ?", mdStart, mdEnd)
	}

	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}

	err = query.Order("event_score DESC").Limit(limit).Find(&events).Error
	return events, err
}

// GetTopScoredEvents 巅峰回忆：event_score 最高的事件
func (r *eventRepository) GetTopScoredEvents(excludeIDs []uint, limit int) ([]*model.Event, error) {
	var events []*model.Event
	query := r.db.Model(&model.Event{}).Scopes(validEventScope)

	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}

	err := query.Order("event_score DESC").Limit(limit).Find(&events).Error
	return events, err
}

// GetFarthestEvents 地理漂移：距常驻地最远的事件（欧氏近似排序）
func (r *eventRepository) GetFarthestEvents(lat, lon float64, excludeIDs []uint, limit int) ([]*model.Event, error) {
	var events []*model.Event
	query := r.db.Model(&model.Event{}).Scopes(validEventScope).
		Where("gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL")

	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}

	// 欧氏近似距离排序（经度修正 cos(lat)）
	distExpr := "(gps_latitude - ?) * (gps_latitude - ?) + (gps_longitude - ?) * (gps_longitude - ?) * COS(? * 3.14159265 / 180.0) * COS(? * 3.14159265 / 180.0)"
	err := query.Order(fmt.Sprintf("%s DESC", r.db.Statement.Dialector.Explain(distExpr, lat, lat, lon, lon, lat, lat))).
		Limit(limit).Find(&events).Error
	if err != nil {
		// fallback: 用原始 SQL 排序
		events = nil
		query2 := r.db.Model(&model.Event{}).Scopes(validEventScope).
			Where("gps_latitude IS NOT NULL AND gps_longitude IS NOT NULL")
		if len(excludeIDs) > 0 {
			query2 = query2.Where("id NOT IN ?", excludeIDs)
		}
		err = query2.Order(
			gorm.Expr("(gps_latitude - ?) * (gps_latitude - ?) + (gps_longitude - ?) * (gps_longitude - ?) DESC", lat, lat, lon, lon),
		).Limit(limit).Find(&events).Error
	}
	return events, err
}

// GetNeverDisplayedEvents 角落遗珠：从未展示过的事件
func (r *eventRepository) GetNeverDisplayedEvents(minScore float64, excludeIDs []uint, limit int) ([]*model.Event, error) {
	var events []*model.Event
	query := r.db.Model(&model.Event{}).Scopes(validEventScope).
		Where("display_count = 0").
		Where("event_score >= ?", minScore)

	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}

	err := query.Order("event_score DESC").Limit(limit).Find(&events).Error
	return events, err
}

// GetRecentlyDisplayedEventIDs 获取近期已展示的事件 ID
func (r *eventRepository) GetRecentlyDisplayedEventIDs(days int) ([]uint, error) {
	var ids []uint
	cutoff := time.Now().AddDate(0, 0, -days)
	err := r.db.Model(&model.Event{}).
		Where("last_displayed_at IS NOT NULL AND last_displayed_at >= ?", cutoff).
		Pluck("id", &ids).Error
	return ids, err
}

// IncrementDisplayCount 展示计数 +1，更新 last_displayed_at
func (r *eventRepository) IncrementDisplayCount(eventID uint) error {
	now := time.Now()
	return r.db.Model(&model.Event{}).Where("id = ?", eventID).
		Updates(map[string]interface{}{
			"display_count":     gorm.Expr("display_count + 1"),
			"last_displayed_at": now,
		}).Error
}

// peopleKeywords 人物相关关键词（与 isPeopleRelated 保持一致）
var peopleKeywords = []string{
	"人物", "人像", "合影", "家人", "朋友", "孩子", "婚礼", "聚会",
	"portrait", "people", "family", "friend", "wedding", "group",
}

// GetPeopleEvents 人物专题：PrimaryTag 含人物关键词的事件
func (r *eventRepository) GetPeopleEvents(excludeIDs []uint, limit int) ([]*model.Event, error) {
	if limit <= 0 {
		return nil, nil
	}

	selected := make([]*model.Event, 0, limit)
	selectedIDs := make(map[uint]struct{}, limit)
	excluded := make([]uint, 0, len(excludeIDs)+limit)
	excluded = append(excluded, excludeIDs...)

	realEvents, err := r.getRealPeopleEvents(excluded, limit)
	if err != nil {
		return nil, err
	}
	for _, event := range realEvents {
		if event == nil {
			continue
		}
		selected = append(selected, event)
		selectedIDs[event.ID] = struct{}{}
		excluded = append(excluded, event.ID)
	}
	if len(selected) >= limit {
		return selected[:limit], nil
	}

	heuristicEvents, err := r.getHeuristicPeopleEvents(excluded, limit-len(selected))
	if err != nil {
		return nil, err
	}
	for _, event := range heuristicEvents {
		if event == nil {
			continue
		}
		if _, exists := selectedIDs[event.ID]; exists {
			continue
		}
		selected = append(selected, event)
		if len(selected) >= limit {
			break
		}
	}

	return selected, nil
}

func (r *eventRepository) getRealPeopleEvents(excludeIDs []uint, limit int) ([]*model.Event, error) {
	var events []*model.Event
	query := r.db.Table("events e").
		Select("e.*").
		Joins("JOIN photos p ON p.event_id = e.id AND p.status = 'active'").
		Where("e.photo_count > 0 AND e.cover_photo_id IS NOT NULL").
		Where("p.top_person_category != ''")

	if len(excludeIDs) > 0 {
		query = query.Where("e.id NOT IN ?", excludeIDs)
	}

	err := query.
		Group("e.id").
		Order(`MAX(CASE p.top_person_category
			WHEN 'family' THEN 4
			WHEN 'friend' THEN 3
			WHEN 'acquaintance' THEN 2
			WHEN 'stranger' THEN 1
			ELSE 0
		END) DESC`).
		Order("e.event_score DESC").
		Limit(limit).
		Find(&events).Error
	return events, err
}

func (r *eventRepository) getHeuristicPeopleEvents(excludeIDs []uint, limit int) ([]*model.Event, error) {
	var events []*model.Event
	query := r.db.Model(&model.Event{}).Scopes(validEventScope)

	conditions := make([]string, 0, len(peopleKeywords))
	args := make([]interface{}, 0, len(peopleKeywords))
	for _, kw := range peopleKeywords {
		conditions = append(conditions, "LOWER(primary_tag) LIKE ?")
		args = append(args, "%"+kw+"%")
	}
	query = query.Where(strings.Join(conditions, " OR "), args...)

	if len(excludeIDs) > 0 {
		query = query.Where("id NOT IN ?", excludeIDs)
	}

	err := query.Order("event_score DESC").Limit(limit).Find(&events).Error
	return events, err
}

// GetSeasonEvents 季节专题：事件中照片的 tags/caption 含指定季节关键词
func (r *eventRepository) GetSeasonEvents(keywords []string, excludeIDs []uint, limit int) ([]*model.Event, error) {
	if len(keywords) == 0 {
		return nil, nil
	}

	var events []*model.Event

	// 通过 photo_tags + photos JOIN 找到含当季关键词的照片所属事件
	subConditions := make([]string, 0, len(keywords)*2)
	subArgs := make([]interface{}, 0, len(keywords)*2)

	// photo_tags 精确匹配
	tagPlaceholders := make([]string, len(keywords))
	for i, kw := range keywords {
		tagPlaceholders[i] = "?"
		subArgs = append(subArgs, kw)
	}
	subConditions = append(subConditions, "pt.tag IN ("+strings.Join(tagPlaceholders, ",")+")")

	// caption LIKE 模糊匹配
	for _, kw := range keywords {
		subConditions = append(subConditions, "LOWER(p.caption) LIKE ?")
		subArgs = append(subArgs, "%"+kw+"%")
	}

	whereClause := "(" + strings.Join(subConditions, " OR ") + ")"

	query := r.db.Table("events e").
		Select("DISTINCT e.*").
		Joins("JOIN photos p ON p.event_id = e.id AND p.status = 'active'").
		Joins("LEFT JOIN photo_tags pt ON pt.photo_id = p.id").
		Where(whereClause, subArgs...).
		Where("e.photo_count > 0 AND e.cover_photo_id IS NOT NULL")

	if len(excludeIDs) > 0 {
		query = query.Where("e.id NOT IN ?", excludeIDs)
	}

	err := query.Order("e.event_score DESC").Limit(limit).Find(&events).Error
	return events, err
}
