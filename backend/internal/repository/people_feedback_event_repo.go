package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/davidhoo/relive/internal/model"
	"gorm.io/gorm"
)

// peopleFeedbackEventListLimit 是 ListForCalibration 的单次上限，避免一次性
// 加载过多反馈事件用于阈值校准。
const peopleFeedbackEventListLimit = 500

// 人工反馈事件类型常量。Task 2 提供了 model 与表结构，事件类型枚举集中在此维护，
// 供 Create 校验与各业务方法引用，避免散落的字符串字面量。
const (
	PeopleFeedbackEventMergeConfirmed  = "merge_confirmed"
	PeopleFeedbackEventMergeRejected   = "merge_rejected"
	PeopleFeedbackEventFaceMoved       = "face_moved"
	PeopleFeedbackEventPersonSplit     = "person_split"
	PeopleFeedbackEventPersonDissolved = "person_dissolved"
)

// allowedFeedbackEventTypes 列出允许持久化的事件类型。Create 拒绝任何不在集合内的
// EventType，防止脏数据混入校准样本。
var allowedFeedbackEventTypes = map[string]struct{}{
	PeopleFeedbackEventMergeConfirmed:  {},
	PeopleFeedbackEventMergeRejected:   {},
	PeopleFeedbackEventFaceMoved:       {},
	PeopleFeedbackEventPersonSplit:     {},
	PeopleFeedbackEventPersonDissolved: {},
}

// PeopleFeedbackEventRepository 持久化人物人工反馈事件。
//
// 反馈事件只用于后续阈值校准与效果评估，不参与实时聚类，也不保存 embedding、
// 图片路径或缩略图路径。Create 仅做轻量写入；ListForCalibration 用 id 游标分页，
// 不反序列化任何人脸 embedding。
type PeopleFeedbackEventRepository interface {
	// Create 写入一条反馈事件。event=nil、未知 EventType 或非法 JSON 字段均返回错误；
	// 创建成功后 ID 与 CreatedAt 由数据库生成。不修改任何业务数据，也不计算相似度。
	Create(event *model.PeopleFeedbackEvent) error
	// ListForCalibration 返回 id > afterID 的事件，按 id ASC，limit 上限 500。
	// limit<=0 返回空 slice；空结果返回空 slice 而非 nil/error。
	ListForCalibration(afterID uint, limit int) ([]*model.PeopleFeedbackEvent, error)
}

type peopleFeedbackEventRepository struct {
	db *gorm.DB
}

// NewPeopleFeedbackEventRepository constructs the repository.
func NewPeopleFeedbackEventRepository(db *gorm.DB) PeopleFeedbackEventRepository {
	return &peopleFeedbackEventRepository{db: db}
}

// MarshalFeedbackIDs 将 ID 列表规范化为 JSON 文本：删除 0、去重、升序排序后
// json.Marshal。空集合统一保存为 "[]"，绝不混用 null 或空字符串。
// 业务方法应统一通过此 helper 序列化，避免在每个调用点重复实现。
func MarshalFeedbackIDs(ids []uint) string {
	seen := make(map[uint]struct{}, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	// 升序排序，保证同一逻辑事件产生确定性 JSON。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

// MarshalFeedbackSnapshot 将相似度快照 map 序列化为 JSON。nil/空 map 统一为 "{}"。
// 序列化失败时回退为 "{}"，保证字段始终是合法 JSON。
func MarshalFeedbackSnapshot(snapshot map[string]interface{}) string {
	if len(snapshot) == 0 {
		return "{}"
	}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// validateFeedbackJSON 校验字段为合法 JSON 或空串（空串会在写入前被规范化为默认值）。
func validateFeedbackJSON(field, name string) error {
	trimmed := strings.TrimSpace(field)
	if trimmed == "" {
		return nil
	}
	if !json.Valid([]byte(trimmed)) {
		return fmt.Errorf("%s is not valid JSON", name)
	}
	return nil
}

func (r *peopleFeedbackEventRepository) Create(event *model.PeopleFeedbackEvent) error {
	if event == nil {
		return errors.New("people feedback event is nil")
	}
	if _, ok := allowedFeedbackEventTypes[event.EventType]; !ok {
		return fmt.Errorf("invalid feedback event type: %q", event.EventType)
	}
	if err := validateFeedbackJSON(event.SourcePersonIDs, "source_person_ids"); err != nil {
		return err
	}
	if err := validateFeedbackJSON(event.FaceIDs, "face_ids"); err != nil {
		return err
	}
	if err := validateFeedbackJSON(event.SimilaritySnapshot, "similarity_snapshot"); err != nil {
		return err
	}
	// 规范化空串为默认值，防止调用方遗漏 helper 时仍落库合法 JSON。
	if strings.TrimSpace(event.SourcePersonIDs) == "" {
		event.SourcePersonIDs = "[]"
	}
	if strings.TrimSpace(event.FaceIDs) == "" {
		event.FaceIDs = "[]"
	}
	if strings.TrimSpace(event.SimilaritySnapshot) == "" {
		event.SimilaritySnapshot = "{}"
	}
	// ID 与 CreatedAt 由数据库生成，清零避免误用调用方传入的值。
	event.ID = 0
	event.CreatedAt = time.Time{}
	return r.db.Create(event).Error
}

func (r *peopleFeedbackEventRepository) ListForCalibration(afterID uint, limit int) ([]*model.PeopleFeedbackEvent, error) {
	if limit <= 0 {
		return []*model.PeopleFeedbackEvent{}, nil
	}
	if limit > peopleFeedbackEventListLimit {
		limit = peopleFeedbackEventListLimit
	}
	var events []*model.PeopleFeedbackEvent
	if err := r.db.Where("id > ?", afterID).
		Order("id ASC").
		Limit(limit).
		Find(&events).Error; err != nil {
		return nil, err
	}
	if events == nil {
		events = []*model.PeopleFeedbackEvent{}
	}
	return events, nil
}
