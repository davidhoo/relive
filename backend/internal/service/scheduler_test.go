package service

import (
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/model"
)

type mergeSuggestionServiceStub struct {
	runCalls int
}

func (s *mergeSuggestionServiceStub) GetTask() *model.PersonMergeSuggestionTask {
	return nil
}

func (s *mergeSuggestionServiceStub) GetStats() (*model.PersonMergeSuggestionStatsResponse, error) {
	return nil, nil
}

func (s *mergeSuggestionServiceStub) GetBackgroundLogs() []string {
	return nil
}

func (s *mergeSuggestionServiceStub) Pause() error {
	return nil
}

func (s *mergeSuggestionServiceStub) Resume() error {
	return nil
}

func (s *mergeSuggestionServiceStub) Rebuild() error {
	return nil
}

func (s *mergeSuggestionServiceStub) MarkDirty(reason string) error {
	return nil
}

func (s *mergeSuggestionServiceStub) RunBackgroundSlice() error {
	s.runCalls++
	return nil
}

func (s *mergeSuggestionServiceStub) ExcludeCandidates(suggestionID uint, candidateIDs []uint) error {
	return nil
}

func (s *mergeSuggestionServiceStub) ApplySuggestion(suggestionID uint, candidateIDs []uint) error {
	return nil
}

func (s *mergeSuggestionServiceStub) ListPending(page, pageSize int) ([]model.PersonMergeSuggestionResponse, int64, error) {
	return nil, 0, nil
}

func (s *mergeSuggestionServiceStub) GetPendingByID(id uint) (*model.PersonMergeSuggestionResponse, error) {
	return nil, nil
}

func (s *mergeSuggestionServiceStub) AttachThreshold() float64 {
	return 0.65
}

func (s *mergeSuggestionServiceStub) CalculateSimilarity(personID1, personID2 uint) (float64, error) {
	return 0, nil
}

func (s *mergeSuggestionServiceStub) MergeSuggestionThreshold() float64 {
	return 0.55
}

func TestTaskSchedulerRunMergeSuggestionSlice(t *testing.T) {
	stub := &mergeSuggestionServiceStub{}
	scheduler := &TaskScheduler{
		mergeSuggestionService: stub,
		stopCh:                make(chan struct{}),
	}

	scheduler.runMergeSuggestionSlice()

	if stub.runCalls != 1 {
		t.Fatalf("expected merge suggestion slice to run once, got %d", stub.runCalls)
	}
}

func TestTaskSchedulerMergeSuggestionSliceTaskStops(t *testing.T) {
	stub := &mergeSuggestionServiceStub{}
	scheduler := &TaskScheduler{
		mergeSuggestionService: stub,
		stopCh:                make(chan struct{}),
	}

	scheduler.wg.Add(1)
	done := make(chan struct{})
	go func() {
		scheduler.mergeSuggestionSliceTask(5 * time.Millisecond)
		close(done)
	}()

	time.Sleep(12 * time.Millisecond)
	close(scheduler.stopCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected merge suggestion task to stop")
	}

	if stub.runCalls == 0 {
		t.Fatal("expected merge suggestion slice to run at least once")
	}
}
