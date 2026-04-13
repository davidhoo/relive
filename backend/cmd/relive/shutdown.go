package main

import (
	"context"
	"time"

	"github.com/davidhoo/relive/internal/lifecycle"
	"github.com/davidhoo/relive/internal/model"
	"github.com/davidhoo/relive/internal/service"
	"github.com/davidhoo/relive/pkg/logger"
)

const shutdownPollInterval = 50 * time.Millisecond

type schedulerStopper interface {
	Stop()
}

type taskShutdownNotifier interface {
	HandleShutdown() error
}

type aiShutdownService interface {
	StopBackgroundAnalyze() error
	GetTaskStatus() *service.AnalyzeTask
}

type eventShutdownService interface {
	StopTask() error
	GetTask() *model.EventClusteringTask
}

type scanTaskProvider interface {
	GetScanTask() *model.ScanTask
}

type peopleTaskProvider interface {
	GetTaskStatus() *model.PeopleTask
}

type thumbnailTaskProvider interface {
	GetTaskStatus() *model.ThumbnailTask
}

type geocodeTaskProvider interface {
	GetTaskStatus() *model.GeocodeTask
}

func notifyShutdown(
	state *lifecycle.State,
	scheduler schedulerStopper,
	ai aiShutdownService,
	event eventShutdownService,
	photo taskShutdownNotifier,
	thumbnail taskShutdownNotifier,
	geocode taskShutdownNotifier,
	people taskShutdownNotifier,
) {
	if state != nil {
		state.BeginDraining()
	}

	if scheduler != nil {
		scheduler.Stop()
	}

	if ai != nil {
		task := ai.GetTaskStatus()
		if task != nil && task.Mode == model.AnalysisOwnerTypeBackground && task.IsRunning() {
			if err := ai.StopBackgroundAnalyze(); err != nil {
				logger.Warnf("Failed to request AI background shutdown: %v", err)
			}
		}
	}

	if event != nil {
		task := event.GetTask()
		if task != nil && task.IsRunning() {
			if err := event.StopTask(); err != nil {
				logger.Warnf("Failed to request event clustering shutdown: %v", err)
			}
		}
	}

	for _, item := range []struct {
		name    string
		service taskShutdownNotifier
	}{
		{name: "photo", service: photo},
		{name: "thumbnail", service: thumbnail},
		{name: "geocode", service: geocode},
		{name: "people", service: people},
	} {
		if item.service == nil {
			continue
		}
		if err := item.service.HandleShutdown(); err != nil {
			logger.Warnf("Failed to notify %s service shutdown: %v", item.name, err)
		}
	}
}

func waitForShutdownDrain(
	ctx context.Context,
	ai aiShutdownService,
	event eventShutdownService,
	scan scanTaskProvider,
	thumbnail thumbnailTaskProvider,
	geocode geocodeTaskProvider,
	people peopleTaskProvider,
) error {
	ticker := time.NewTicker(shutdownPollInterval)
	defer ticker.Stop()

	for {
		if shutdownDrained(ai, event, scan, thumbnail, geocode, people) {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func shutdownDrained(
	ai aiShutdownService,
	event eventShutdownService,
	scan scanTaskProvider,
	thumbnail thumbnailTaskProvider,
	geocode geocodeTaskProvider,
	people peopleTaskProvider,
) bool {
	if ai != nil {
		task := ai.GetTaskStatus()
		if task != nil && task.Mode == model.AnalysisOwnerTypeBackground && task.IsRunning() {
			return false
		}
	}

	if event != nil {
		task := event.GetTask()
		if task != nil && task.IsRunning() {
			return false
		}
	}

	if scan != nil {
		task := scan.GetScanTask()
		if task != nil && task.IsRunning() {
			return false
		}
	}

	if thumbnail != nil {
		task := thumbnail.GetTaskStatus()
		if task != nil && task.Status != model.TaskStatusStopped {
			return false
		}
	}

	if geocode != nil {
		task := geocode.GetTaskStatus()
		if task != nil && task.Status != model.TaskStatusStopped {
			return false
		}
	}

	if people != nil {
		task := people.GetTaskStatus()
		if task != nil && task.Status != model.TaskStatusStopped {
			return false
		}
	}

	return true
}
