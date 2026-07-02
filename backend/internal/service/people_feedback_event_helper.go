package service

import (
	"strconv"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/davidhoo/relive/pkg/logger"
)

// peopleMergeSuggestionAlgorithmVersion 是合并建议算法的版本标记，写入反馈事件
// 供后续阈值校准区分 manual 合并与建议驱动的合并。
const peopleMergeSuggestionAlgorithmVersion = "suggestion-v1"

// peopleManualAlgorithmVersion 标记由用户手工直接操作（非建议驱动）产生的事件。
const peopleManualAlgorithmVersion = "manual"

// buildFeedbackEvent 构造一条反馈事件，ID 列表经 MarshalFeedbackIDs 规范化
// （删除 0、去重、升序、空集合为 "[]"），快照经 MarshalFeedbackSnapshot 序列化
// （空快照为 "{}"）。各业务方法统一通过此 helper 构造，不重复实现排序与序列化。
func buildFeedbackEvent(eventType string, targetPersonID uint, sourcePersonIDs, faceIDs []uint, algorithmVersion string, snapshot map[string]interface{}) *model.PeopleFeedbackEvent {
	return &model.PeopleFeedbackEvent{
		EventType:          eventType,
		TargetPersonID:     targetPersonID,
		SourcePersonIDs:    repository.MarshalFeedbackIDs(sourcePersonIDs),
		FaceIDs:            repository.MarshalFeedbackIDs(faceIDs),
		AlgorithmVersion:   algorithmVersion,
		SimilaritySnapshot: repository.MarshalFeedbackSnapshot(snapshot),
	}
}

// emitFeedbackEvent 通过 executeWrite 单独写入一条反馈事件。它必须在核心业务写入
// 回调返回之后调用——绝不能在已有的 executeWrite 回调内部再次调用 executeWrite，
// 否则 WriteQueue 的互斥锁会重入死锁。
//
// 反馈记录是辅助能力：写入失败时仅记录脱敏 warning（不含 JSON 全量、embedding、
// 图片或缩略图路径），原业务结果不受影响，也不自动重试。
func emitFeedbackEvent(repo repository.PeopleFeedbackEventRepository, executeWrite func(func() error) error, event *model.PeopleFeedbackEvent) {
	if repo == nil || event == nil {
		return
	}
	if err := executeWrite(func() error {
		return repo.Create(event)
	}); err != nil {
		logger.Warnf("people feedback event record failed type=%s target=%d: %v",
			event.EventType, event.TargetPersonID, err)
	}
}

// candidateScoreSnapshot 从建议项中提取指定候选人物的相似度分数快照。
// 仅利用建议已有的分数，绝不现场重新计算相似度。返回 nil 表示无可用分数。
func candidateScoreSnapshot(items []*model.PersonMergeSuggestionItem, candidateIDs []uint) map[string]interface{} {
	if len(items) == 0 || len(candidateIDs) == 0 {
		return nil
	}
	want := make(map[uint]struct{}, len(candidateIDs))
	for _, id := range candidateIDs {
		if id != 0 {
			want[id] = struct{}{}
		}
	}
	snapshot := make(map[string]interface{})
	for _, item := range items {
		if item == nil {
			continue
		}
		if _, ok := want[item.CandidatePersonID]; !ok {
			continue
		}
		snapshot[strconv.FormatUint(uint64(item.CandidatePersonID), 10)] = item.SimilarityScore
	}
	if len(snapshot) == 0 {
		return nil
	}
	return snapshot
}
