package service

import (
	"errors"
	"testing"

	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPersonMergeSuggestionService_ReviewFlowEmitsFeedbackEvents asserts that the
// merge-suggestion review operations record durable feedback events for calibration:
// ExcludeCandidates -> merge_rejected (plus the existing cannot-link constraint),
// ApplySuggestion -> merge_confirmed. Failed/no-op paths must not emit.
func TestPersonMergeSuggestionService_ReviewFlowEmitsFeedbackEvents(t *testing.T) {
	svc, db, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	require.NoError(t, db.AutoMigrate(&model.PeopleFeedbackEvent{}))
	fbRepo := repos.FeedbackEvent
	svc.(*personMergeSuggestionService).SetFeedbackEventRepo(fbRepo)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	excludedCandidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.015})
	mergedCandidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.03})

	require.NoError(t, svc.MarkDirty("feedback"))
	require.NoError(t, svc.RunBackgroundSlice())

	suggestions, _, err := svc.ListPending(1, 10)
	require.NoError(t, err)

	var targetSuggestion *model.PersonMergeSuggestionResponse
	for i := range suggestions {
		if suggestions[i].TargetPersonID == target.ID {
			targetSuggestion = &suggestions[i]
			break
		}
	}
	require.NotNil(t, targetSuggestion, "target should have a pending suggestion")

	// Exclude -> merge_rejected + cannot-link.
	require.NoError(t, svc.ExcludeCandidates(targetSuggestion.ID, []uint{excludedCandidate.ID}))
	blocked, err := repos.CannotLink.ExistsBetween(target.ID, excludedCandidate.ID)
	require.NoError(t, err)
	assert.True(t, blocked, "exclude still creates the cannot-link constraint")

	rejected := feedbackEventsForTarget(t, fbRepo, target.ID)
	require.Len(t, rejected, 1)
	assert.Equal(t, "merge_rejected", rejected[0].EventType)
	assert.Equal(t, peopleMergeSuggestionAlgorithmVersion, rejected[0].AlgorithmVersion)
	assert.Equal(t, repository.MarshalFeedbackIDs([]uint{excludedCandidate.ID}), rejected[0].SourcePersonIDs)
	assert.Equal(t, "[]", rejected[0].FaceIDs)

	// Apply -> merge_confirmed.
	require.NoError(t, svc.ApplySuggestion(targetSuggestion.ID, []uint{mergedCandidate.ID}))
	confirmed := feedbackEventsForTarget(t, fbRepo, target.ID)
	require.Len(t, confirmed, 2)
	assert.Equal(t, "merge_confirmed", confirmed[1].EventType)
	assert.Equal(t, peopleMergeSuggestionAlgorithmVersion, confirmed[1].AlgorithmVersion)
	assert.Equal(t, repository.MarshalFeedbackIDs([]uint{mergedCandidate.ID}), confirmed[1].SourcePersonIDs)
}

// TestPersonMergeSuggestionService_ExcludeFailedEmitsNoFeedback asserts that a failed
// exclude (non-existent suggestion) records no feedback event.
func TestPersonMergeSuggestionService_ExcludeFailedEmitsNoFeedback(t *testing.T) {
	svc, db, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	require.NoError(t, db.AutoMigrate(&model.PeopleFeedbackEvent{}))
	fbRepo := repos.FeedbackEvent
	svc.(*personMergeSuggestionService).SetFeedbackEventRepo(fbRepo)

	err := svc.ExcludeCandidates(999999, []uint{1})
	require.Error(t, err)

	all, err := fbRepo.ListForCalibration(0, 100)
	require.NoError(t, err)
	assert.Empty(t, all, "failed exclude must not emit a feedback event")
}

// feedbackEventsForTarget returns feedback events whose TargetPersonID matches targetID.
// Tests share an in-memory SQLite (cache=shared); filtering by the target created in this
// test isolates it from events left by other tests.
func feedbackEventsForTarget(t *testing.T, repo interface {
	ListForCalibration(afterID uint, limit int) ([]*model.PeopleFeedbackEvent, error)
}, targetID uint) []*model.PeopleFeedbackEvent {
	t.Helper()
	all, err := repo.ListForCalibration(0, 1000)
	require.NoError(t, err)
	var out []*model.PeopleFeedbackEvent
	for _, e := range all {
		if e.TargetPersonID == targetID {
			out = append(out, e)
		}
	}
	return out
}

// TestPersonMergeSuggestionService_ExcludeMultiCandidatesEmitsOneEvent 验证一次剔除多个
// 候选只产生一条 merge_rejected 事件，source_person_ids 含全部被剔除候选（升序去重）。
func TestPersonMergeSuggestionService_ExcludeMultiCandidatesEmitsOneEvent(t *testing.T) {
	svc, db, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	require.NoError(t, db.AutoMigrate(&model.PeopleFeedbackEvent{}))
	fbRepo := repos.FeedbackEvent
	svc.(*personMergeSuggestionService).SetFeedbackEventRepo(fbRepo)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	c1 := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.015})
	c2 := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.03})

	require.NoError(t, svc.MarkDirty("multi-exclude"))
	require.NoError(t, svc.RunBackgroundSlice())

	suggestions, _, err := svc.ListPending(1, 10)
	require.NoError(t, err)
	var sug *model.PersonMergeSuggestionResponse
	for i := range suggestions {
		if suggestions[i].TargetPersonID == target.ID {
			sug = &suggestions[i]
			break
		}
	}
	require.NotNil(t, sug)
	require.Len(t, sug.Items, 2)

	require.NoError(t, svc.ExcludeCandidates(sug.ID, []uint{c1.ID, c2.ID}))

	rejected := feedbackEventsForTarget(t, fbRepo, target.ID)
	require.Len(t, rejected, 1, "multi-candidate exclude emits exactly one event")
	assert.Equal(t, "merge_rejected", rejected[0].EventType)
	assert.Equal(t, repository.MarshalFeedbackIDs([]uint{c1.ID, c2.ID}), rejected[0].SourcePersonIDs)
	assert.Equal(t, "[]", rejected[0].FaceIDs)
}

// TestPersonMergeSuggestionService_ExcludeRepeatedDoesNotDuplicate 验证对已 excluded 的候选
// 重复剔除不会再次产生反馈事件。
func TestPersonMergeSuggestionService_ExcludeRepeatedDoesNotDuplicate(t *testing.T) {
	svc, db, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	require.NoError(t, db.AutoMigrate(&model.PeopleFeedbackEvent{}))
	fbRepo := repos.FeedbackEvent
	svc.(*personMergeSuggestionService).SetFeedbackEventRepo(fbRepo)

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.015})

	require.NoError(t, svc.MarkDirty("repeat-exclude"))
	require.NoError(t, svc.RunBackgroundSlice())

	suggestions, _, err := svc.ListPending(1, 10)
	require.NoError(t, err)
	var sug *model.PersonMergeSuggestionResponse
	for i := range suggestions {
		if suggestions[i].TargetPersonID == target.ID {
			sug = &suggestions[i]
			break
		}
	}
	require.NotNil(t, sug)

	// 第一次剔除：产生一条事件。
	require.NoError(t, svc.ExcludeCandidates(sug.ID, []uint{candidate.ID}))
	require.Len(t, feedbackEventsForTarget(t, fbRepo, target.ID), 1)

	// 重复剔除同一候选：不再产生新事件（候选已 excluded，不再是 pending）。
	require.NoError(t, svc.ExcludeCandidates(sug.ID, []uint{candidate.ID}))
	assert.Len(t, feedbackEventsForTarget(t, fbRepo, target.ID), 1,
		"repeated exclude of already-excluded candidate emits no new event")
}

// TestPersonMergeSuggestionService_FeedbackRepoFailureDoesNotBreakExclude 验证反馈仓库写入
// 失败不影响 ExcludeCandidates 的业务结果（cannot-link 与 item 状态正常变更）。
func TestPersonMergeSuggestionService_FeedbackRepoFailureDoesNotBreakExclude(t *testing.T) {
	svc, db, repos, _ := newPersonMergeSuggestionServiceForTest(t)
	require.NoError(t, db.AutoMigrate(&model.PeopleFeedbackEvent{}))
	svc.(*personMergeSuggestionService).SetFeedbackEventRepo(&failingFeedbackEventRepo{err: errors.New("disk full")})

	target := createSuggestionTestPerson(t, repos, model.PersonCategoryFamily, []float32{1, 0}, []float32{0.99, 0.01})
	candidate := createSuggestionTestPerson(t, repos, model.PersonCategoryStranger, []float32{1, 0.015})

	require.NoError(t, svc.MarkDirty("failing-repo"))
	require.NoError(t, svc.RunBackgroundSlice())

	suggestions, _, err := svc.ListPending(1, 10)
	require.NoError(t, err)
	var sug *model.PersonMergeSuggestionResponse
	for i := range suggestions {
		if suggestions[i].TargetPersonID == target.ID {
			sug = &suggestions[i]
			break
		}
	}
	require.NotNil(t, sug)

	// 反馈仓库失败，业务仍成功。
	require.NoError(t, svc.ExcludeCandidates(sug.ID, []uint{candidate.ID}))

	blocked, err := repos.CannotLink.ExistsBetween(target.ID, candidate.ID)
	require.NoError(t, err)
	assert.True(t, blocked, "cannot-link still written despite feedback repo failure")
	items, err := repos.MergeSuggestion.GetItems(sug.ID, model.PersonMergeSuggestionItemStatusExcluded)
	require.NoError(t, err)
	require.Len(t, items, 1, "item still marked excluded despite feedback repo failure")
}
