package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidhoo/relive/internal/lifecycle"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
)

type fakeScheduler struct {
	stopped atomic.Bool
}

func (s *fakeScheduler) Stop() {
	s.stopped.Store(true)
}

type fakeShutdownService struct {
	calls atomic.Int32
}

func (s *fakeShutdownService) HandleShutdown() error {
	s.calls.Add(1)
	return nil
}

type fakeAIShutdownService struct {
	stopCalls atomic.Int32
	task      atomic.Pointer[service.AnalyzeTask]
}

func (s *fakeAIShutdownService) StopBackgroundAnalyze() error {
	s.stopCalls.Add(1)
	task := s.task.Load()
	if task != nil {
		task.Status = service.AnalyzeTaskStatusCompleted
	}
	return nil
}

func (s *fakeAIShutdownService) GetTaskStatus() *service.AnalyzeTask {
	return s.task.Load()
}

type fakeEventShutdownService struct {
	stopCalls atomic.Int32
	task      atomic.Pointer[model.EventClusteringTask]
}

func (s *fakeEventShutdownService) StopTask() error {
	s.stopCalls.Add(1)
	task := s.task.Load()
	if task != nil {
		task.Status = model.ScanJobStatusStopped
	}
	return nil
}

func (s *fakeEventShutdownService) GetTask() *model.EventClusteringTask {
	return s.task.Load()
}

type fakeScanTaskProvider struct {
	task atomic.Pointer[model.ScanTask]
}

func (p *fakeScanTaskProvider) GetScanTask() *model.ScanTask {
	return p.task.Load()
}

type fakePeopleTaskProvider struct {
	task atomic.Pointer[model.PeopleTask]
}

func (p *fakePeopleTaskProvider) GetTaskStatus() *model.PeopleTask {
	return p.task.Load()
}

type fakeThumbnailTaskProvider struct {
	task atomic.Pointer[model.ThumbnailTask]
}

func (p *fakeThumbnailTaskProvider) GetTaskStatus() *model.ThumbnailTask {
	return p.task.Load()
}

type fakeGeocodeTaskProvider struct {
	task atomic.Pointer[model.GeocodeTask]
}

func (p *fakeGeocodeTaskProvider) GetTaskStatus() *model.GeocodeTask {
	return p.task.Load()
}

func TestNotifyShutdownMarksDrainingAndStopsServices(t *testing.T) {
	state := lifecycle.NewState()
	scheduler := &fakeScheduler{}
	photo := &fakeShutdownService{}
	thumbnail := &fakeShutdownService{}
	geocode := &fakeShutdownService{}
	people := &fakeShutdownService{}

	aiTask := &service.AnalyzeTask{
		Mode:   model.AnalysisOwnerTypeBackground,
		Status: service.AnalyzeTaskStatusRunning,
	}
	ai := &fakeAIShutdownService{}
	ai.task.Store(aiTask)

	eventTask := &model.EventClusteringTask{Status: model.ScanJobStatusRunning}
	event := &fakeEventShutdownService{}
	event.task.Store(eventTask)

	notifyShutdown(state, scheduler, ai, event, photo, thumbnail, geocode, people)

	if !state.IsDraining() {
		t.Fatal("expected shutdown to mark lifecycle state as draining")
	}
	if !scheduler.stopped.Load() {
		t.Fatal("expected scheduler stop to be requested")
	}
	if ai.stopCalls.Load() != 1 {
		t.Fatalf("expected AI stop to be requested once, got %d", ai.stopCalls.Load())
	}
	if event.stopCalls.Load() != 1 {
		t.Fatalf("expected event clustering stop to be requested once, got %d", event.stopCalls.Load())
	}
	if photo.calls.Load() != 1 || thumbnail.calls.Load() != 1 || geocode.calls.Load() != 1 || people.calls.Load() != 1 {
		t.Fatal("expected all background services to receive HandleShutdown")
	}
}

func TestWaitForShutdownDrainReturnsWhenTasksComplete(t *testing.T) {
	aiTask := &service.AnalyzeTask{
		Mode:   model.AnalysisOwnerTypeBackground,
		Status: service.AnalyzeTaskStatusRunning,
	}
	ai := &fakeAIShutdownService{}
	ai.task.Store(aiTask)

	eventTask := &model.EventClusteringTask{Status: model.ScanJobStatusRunning}
	event := &fakeEventShutdownService{}
	event.task.Store(eventTask)

	scan := &fakeScanTaskProvider{}
	scan.task.Store(&model.ScanTask{Status: model.ScanJobStatusRunning})

	thumbnail := &fakeThumbnailTaskProvider{}
	thumbnail.task.Store(&model.ThumbnailTask{Status: model.TaskStatusStopping})

	geocode := &fakeGeocodeTaskProvider{}
	geocode.task.Store(&model.GeocodeTask{Status: model.TaskStatusStopping})

	people := &fakePeopleTaskProvider{}
	people.task.Store(&model.PeopleTask{Status: model.TaskStatusStopping})

	go func() {
		time.Sleep(20 * time.Millisecond)
		aiTask.Status = service.AnalyzeTaskStatusCompleted
		eventTask.Status = model.ScanJobStatusCompleted
		scan.task.Store(&model.ScanTask{Status: model.ScanJobStatusCompleted})
		thumbnail.task.Store(&model.ThumbnailTask{Status: model.TaskStatusStopped})
		geocode.task.Store(&model.GeocodeTask{Status: model.TaskStatusStopped})
		people.task.Store(&model.PeopleTask{Status: model.TaskStatusStopped})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if err := waitForShutdownDrain(ctx, ai, event, scan, thumbnail, geocode, people); err != nil {
		t.Fatalf("expected drain wait to finish successfully, got %v", err)
	}
}

func TestWaitForShutdownDrainTimesOut(t *testing.T) {
	aiTask := &service.AnalyzeTask{
		Mode:   model.AnalysisOwnerTypeBackground,
		Status: service.AnalyzeTaskStatusRunning,
	}
	ai := &fakeAIShutdownService{}
	ai.task.Store(aiTask)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	if err := waitForShutdownDrain(ctx, ai, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("expected drain wait to time out")
	}
}
